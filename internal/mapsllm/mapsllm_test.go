package mapsllm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/crawl"
	"github.com/logrenant/mimir/internal/maps"
	"github.com/logrenant/mimir/internal/mapscrape"
	"github.com/logrenant/mimir/internal/refine"
)

type fakeExtractor struct {
	calls   int
	gotHTML string
	out     refine.FeedOutput
	err     error
}

func (f *fakeExtractor) ExtractFeed(_ context.Context, in refine.FeedInput) (refine.FeedOutput, error) {
	f.calls++
	f.gotHTML = in.HTML
	return f.out, f.err
}

type fakeFetcher struct {
	page crawl.Page
	err  error
}

func (f *fakeFetcher) MarkdownWithOptions(_ context.Context, _ string, _ crawl.FetchOptions) (crawl.Page, error) {
	return f.page, f.err
}

func testCfg() config.Config { return config.Load() }

// The id comes from the Maps URL when the model reported one — the same
// feature id the selector path reads — so the two providers agree on identity.
func TestExtractPlaces_DerivesTheFeatureIDFromTheURL(t *testing.T) {
	extractor := &fakeExtractor{out: refine.FeedOutput{Places: []refine.PlaceItem{{
		Name:    "A Diş",
		MapsURL: "https://www.google.com/maps/place/A/data=!3m1!4b1!4m6!1s0x14cab8:0x1234!3d40.9!4d29.02",
		Rating:  4.9,
		Reviews: 120,
	}}}}

	got, err := New(testCfg(), extractor, nil).ExtractPlaces(context.Background(), "<html>…</html>")
	if err != nil {
		t.Fatalf("ExtractPlaces: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d companies", len(got))
	}
	c := got[0]
	if c.PlaceID != mapscrape.PlaceIDPrefix+"0x14cab8:0x1234" {
		t.Errorf("place id: %q", c.PlaceID)
	}
	if c.Latitude == 0 || c.Longitude == 0 {
		t.Errorf("coordinates were not read: %+v", c)
	}
	if c.Source != maps.SourceScrape {
		t.Errorf("provenance: %q", c.Source)
	}
}

// No URL is still a usable row, but its id must be stable across runs and must
// still carry the prefix — a model-read row can never overwrite a billed one.
func TestExtractPlaces_DerivesAStableIDWithoutAURL(t *testing.T) {
	extractor := &fakeExtractor{out: refine.FeedOutput{Places: []refine.PlaceItem{
		{Name: "B Diş", Address: "Moda"},
		{Name: "B Diş", Address: "Moda"},
	}}}

	got, err := New(testCfg(), extractor, nil).ExtractPlaces(context.Background(), "<html>…</html>")
	if err != nil {
		t.Fatalf("ExtractPlaces: %v", err)
	}
	// The same business twice is a model repeating a card, not two businesses.
	if len(got) != 1 {
		t.Fatalf("duplicates were not collapsed: %+v", got)
	}
	if !strings.HasPrefix(got[0].PlaceID, mapscrape.PlaceIDPrefix) {
		t.Errorf("place id must carry the scrape prefix: %q", got[0].PlaceID)
	}
}

// A page that did not render is this provider's normal failure, and it is its
// own error so the router can move on rather than report a broken component.
func TestSearch_EmptyPageIsErrNoPage(t *testing.T) {
	c := New(testCfg(), &fakeExtractor{}, &fakeFetcher{page: crawl.Page{}})

	if _, err := c.Search(context.Background(), maps.Query{Text: "x"}); !errors.Is(err, ErrNoPage) {
		t.Fatalf("got %v, want ErrNoPage", err)
	}
}

// The model is fed the trimmed page, not the megabytes of script the feed
// arrives with: those are billed input tokens that contain no business name.
func TestSearch_FeedsTheModelTheTrimmedPage(t *testing.T) {
	html := `<html><head><style>.a{color:red}</style></head><body>` +
		`<script>var junk="` + strings.Repeat("x", 5000) + `";</script>` +
		`<div role="feed"><a aria-label="A Diş" href="/maps/place/A">A Diş</a></div></body></html>`

	extractor := &fakeExtractor{out: refine.FeedOutput{Places: []refine.PlaceItem{{Name: "A Diş"}}}}
	c := New(testCfg(), extractor, &fakeFetcher{page: crawl.Page{RawHTML: html}})

	if _, err := c.Search(context.Background(), maps.Query{Text: "x"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if strings.Contains(extractor.gotHTML, "junk") || strings.Contains(extractor.gotHTML, "color:red") {
		t.Errorf("script and style survived the trim: %s", extractor.gotHTML[:200])
	}
	if !strings.Contains(extractor.gotHTML, "A Diş") {
		t.Errorf("the trim dropped the content: %s", extractor.gotHTML)
	}
}
