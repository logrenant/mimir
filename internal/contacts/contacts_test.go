package contacts

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/crawl"
	"github.com/logrenant/mimir/internal/maps"
	"github.com/logrenant/mimir/internal/refine"
)

// Both fakes are called from Enrich's bounded-parallel goroutines, so their
// counters are locked: an unguarded ++ here is a race in the test, and `make
// check` runs -race.
type fakeFetcher struct {
	mu    sync.Mutex
	calls int
	pages map[string]crawl.Page
	err   error
}

func (f *fakeFetcher) Markdown(_ context.Context, target string) (crawl.Page, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()

	if f.err != nil {
		return crawl.Page{}, f.err
	}
	page, ok := f.pages[target]
	if !ok {
		return crawl.Page{}, errors.New("404")
	}
	return page, nil
}

func (f *fakeFetcher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type fakeExtractor struct {
	mu    sync.Mutex
	calls int
	out   refine.ContactOutput
	err   error
}

func (f *fakeExtractor) ExtractContacts(_ context.Context, _ refine.ContactInput) (refine.ContactOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.out, f.err
}

func (f *fakeExtractor) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func testCfg() config.Config {
	cfg := config.Load()
	cfg.MaxConcurrentRefines = 2
	return cfg
}

// Tier 1 is patterns, and a page with a tel: link must never reach the model.
func TestEnrich_PatternTierCostsNothing(t *testing.T) {
	fetcher := &fakeFetcher{pages: map[string]crawl.Page{
		"https://a.example": {
			RawHTML:  `<a href="tel:+90 216 123 45 67">Ara</a><a href="mailto:info@a.example">Yaz</a>`,
			Markdown: "A Diş Kliniği",
		},
	}}
	model := &fakeExtractor{}

	got := New(testCfg(), fetcher, model).Enrich(context.Background(), []maps.Company{
		{PlaceID: "p1", Name: "A Diş", Website: "https://a.example"},
	})

	en := got["p1"]
	if en.Phone != "+902161234567" || en.Email != "info@a.example" {
		t.Fatalf("pattern tier: %+v", en)
	}
	if en.Method != MethodPattern {
		t.Errorf("method: %q", en.Method)
	}
	if model.callCount() != 0 {
		t.Errorf("the model was called %d times for a page that stated both", model.callCount())
	}
}

// Tier 2 runs only where tier 1 found nothing, and its answer is held to the
// same patterns: a number the model paraphrased does not survive.
func TestEnrich_ModelTierIsValidatedLikeTheFirst(t *testing.T) {
	fetcher := &fakeFetcher{pages: map[string]crawl.Page{
		"https://b.example": {Markdown: "Bize ulaşın: iletişim sayfamızdan."},
	}}
	model := &fakeExtractor{out: refine.ContactOutput{Phone: "beş altı yedi", Email: "info@b.example"}}

	got := New(testCfg(), fetcher, model).Enrich(context.Background(), []maps.Company{
		{PlaceID: "p1", Name: "B", Website: "https://b.example"},
	})

	en := got["p1"]
	if model.callCount() != 1 {
		t.Fatalf("the model should have been asked once, was asked %d", model.callCount())
	}
	if en.Phone != "" {
		t.Errorf("a phone that is not a number must be dropped: %q", en.Phone)
	}
	if en.Email != "info@b.example" || en.Method != MethodModel {
		t.Errorf("the email should have survived: %+v", en)
	}
}

// A company with no website is not a failure, and must not become a fetch.
func TestEnrich_NoWebsiteIsRecordedNotGuessed(t *testing.T) {
	fetcher := &fakeFetcher{}
	got := New(testCfg(), fetcher, &fakeExtractor{}).Enrich(context.Background(), []maps.Company{
		{PlaceID: "p1", Name: "C"},
	})

	en := got["p1"]
	if en.Method != MethodNoSite || en.Phone != "" || en.Email != "" {
		t.Fatalf("no-website row: %+v", en)
	}
	if fetcher.callCount() != 0 {
		t.Errorf("a company with no website was fetched %d times", fetcher.callCount())
	}
}

func TestEnrich_FetchFailureIsRecordedPerCompany(t *testing.T) {
	fetcher := &fakeFetcher{err: errors.New("crawl4ai unavailable")}
	got := New(testCfg(), fetcher, &fakeExtractor{}).Enrich(context.Background(), []maps.Company{
		{PlaceID: "p1", Name: "A", Website: "a.example"},
		{PlaceID: "p2", Name: "B", Website: "b.example"},
	})

	if len(got) != 2 {
		t.Fatalf("every company must get a row: %+v", got)
	}
	for id, en := range got {
		if en.Method != MethodFailed || en.Note == "" {
			t.Errorf("%s: a failure must say so: %+v", id, en)
		}
	}
}

func TestNormalizePhone(t *testing.T) {
	cases := map[string]string{
		"+90 216 123 45 67":   "+902161234567",
		"0216 123 45 67":      "02161234567",
		"(0216) 330 09 99":    "02163300999",
		"12,90":               "",
		"2026":                "",
		"+1 2":                "",
		"1234567890123456789": "",
	}
	for in, want := range cases {
		if got := normalizePhone(in); got != want {
			t.Errorf("normalizePhone(%q) = %q, want %q", in, got, want)
		}
	}
}

// An agency's or a toolchain's address in a footer is the classic wrong answer.
func TestNormalizeEmail_DropsToolchainAddresses(t *testing.T) {
	if got := normalizeEmail("Info@Firma.COM"); got != "info@firma.com" {
		t.Errorf("got %q", got)
	}
	for _, junk := range []string{"a@sentry.io", "x@example.com", "y@wixpress.com", "not-an-email"} {
		if got := normalizeEmail(junk); got != "" {
			t.Errorf("normalizeEmail(%q) = %q, want empty", junk, got)
		}
	}
}
