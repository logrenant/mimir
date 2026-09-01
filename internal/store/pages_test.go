package store

import (
	"context"
	"testing"
	"time"

	"github.com/logrenant/goat-mcp/internal/crawl"
	"github.com/logrenant/goat-mcp/internal/refine"
)

func testPage() crawl.Page {
	return crawl.Page{
		URL:       "https://example.com/pricing",
		Title:     "Pricing",
		Markdown:  "# Pricing\n\nPro tier is $20/mo.",
		FetchedAt: time.Now().Truncate(time.Second),
	}
}

func testKey() RefineKey {
	return RefineKey{
		URL:           "https://example.com/pricing",
		Query:         "what does it cost",
		MaxTokens:     1500,
		ContentHash:   HashContent("# Pricing\n\nPro tier is $20/mo."),
		PromptVersion: "v1",
	}
}

func testOutput() refine.Output {
	return refine.Output{
		Text:          "- Pro tier costs $20/mo.",
		Refined:       true,
		Truncated:     true,
		TokenEstimate: 7,
	}
}

func TestCrawlCache_RoundTrip(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	want := testPage()

	if err := s.PutCrawl(ctx, want); err != nil {
		t.Fatalf("PutCrawl: %v", err)
	}

	got, ok, err := s.GetCrawl(ctx, want.URL, time.Hour)
	if err != nil {
		t.Fatalf("GetCrawl: %v", err)
	}
	if !ok {
		t.Fatal("want a hit, got a miss")
	}
	if got.URL != want.URL || got.Title != want.Title || got.Markdown != want.Markdown {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", got, want)
	}
	if !got.FetchedAt.Equal(want.FetchedAt.UTC()) {
		t.Errorf("FetchedAt: got %v, want %v", got.FetchedAt, want.FetchedAt.UTC())
	}
}

// GetCrawlFull must return RawHTML round-tripped through PutCrawl, while
// GetCrawl on the exact same row must never expose it — the isolation
// invariant is structural (GetCrawl's SELECT never names the column), not a
// runtime check, but this locks in the observable behaviour.
func TestCrawlCache_RawHTMLViaGetCrawlFullOnly(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	want := testPage()
	want.RawHTML = "<html><body>raw</body></html>"

	if err := s.PutCrawl(ctx, want); err != nil {
		t.Fatalf("PutCrawl: %v", err)
	}

	full, ok, err := s.GetCrawlFull(ctx, want.URL, time.Hour)
	if err != nil {
		t.Fatalf("GetCrawlFull: %v", err)
	}
	if !ok {
		t.Fatal("want a hit, got a miss")
	}
	if full.RawHTML != want.RawHTML {
		t.Fatalf("RawHTML round-trip mismatch: got %q, want %q", full.RawHTML, want.RawHTML)
	}
	if full.Markdown != want.Markdown || full.Title != want.Title {
		t.Fatalf("GetCrawlFull round-trip mismatch: got %+v\nwant %+v", full, want)
	}

	plain, ok, err := s.GetCrawl(ctx, want.URL, time.Hour)
	if err != nil {
		t.Fatalf("GetCrawl: %v", err)
	}
	if !ok {
		t.Fatal("GetCrawl: want a hit, got a miss")
	}
	if plain.RawHTML != "" {
		t.Fatalf("GetCrawl must never expose RawHTML, got %q", plain.RawHTML)
	}
}

func TestCrawlCache_GetCrawlFullMissOnUnknownURL(t *testing.T) {
	s := openTestStore(t)

	_, ok, err := s.GetCrawlFull(context.Background(), "https://example.com/never-seen", time.Hour)
	if err != nil {
		t.Fatalf("GetCrawlFull: %v", err)
	}
	if ok {
		t.Fatal("want a miss for an unknown URL, got a hit")
	}
}

func TestCrawlCache_GetCrawlFullMissWhenExpired(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	page := testPage()
	page.RawHTML = "<html></html>"

	if err := s.PutCrawl(ctx, page); err != nil {
		t.Fatalf("PutCrawl: %v", err)
	}

	_, ok, err := s.GetCrawlFull(ctx, page.URL, time.Nanosecond)
	if err != nil {
		t.Fatalf("GetCrawlFull: %v", err)
	}
	if ok {
		t.Fatal("want a miss for an expired row, got a hit")
	}
}

func TestCrawlCache_MissOnUnknownURL(t *testing.T) {
	s := openTestStore(t)

	_, ok, err := s.GetCrawl(context.Background(), "https://example.com/never-seen", time.Hour)
	if err != nil {
		t.Fatalf("GetCrawl: %v", err)
	}
	if ok {
		t.Fatal("want a miss for an unknown URL, got a hit")
	}
}

func TestCrawlCache_MissWhenExpired(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	page := testPage()

	if err := s.PutCrawl(ctx, page); err != nil {
		t.Fatalf("PutCrawl: %v", err)
	}

	// A one-nanosecond TTL is already exceeded by the time we read back.
	_, ok, err := s.GetCrawl(ctx, page.URL, time.Nanosecond)
	if err != nil {
		t.Fatalf("GetCrawl: %v", err)
	}
	if ok {
		t.Fatal("want a miss for an expired row, got a hit")
	}

	// The expired row is dropped on read, so it stays a miss under a long TTL.
	if _, ok, err := s.GetCrawl(ctx, page.URL, time.Hour); err != nil || ok {
		t.Fatalf("expired row was not evicted: ok=%v err=%v", ok, err)
	}
}

func TestCrawlCache_PutReplacesExisting(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	page := testPage()
	if err := s.PutCrawl(ctx, page); err != nil {
		t.Fatalf("first PutCrawl: %v", err)
	}

	page.Markdown = "# Pricing\n\nPro tier is now $25/mo."
	page.Title = "Pricing (updated)"
	if err := s.PutCrawl(ctx, page); err != nil {
		t.Fatalf("second PutCrawl: %v", err)
	}

	got, ok, err := s.GetCrawl(ctx, page.URL, time.Hour)
	if err != nil || !ok {
		t.Fatalf("GetCrawl: ok=%v err=%v", ok, err)
	}
	if got.Markdown != page.Markdown || got.Title != page.Title {
		t.Fatalf("re-put did not replace the row: got %+v", got)
	}
}

func TestRefinedCache_RoundTrip(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	key, want := testKey(), testOutput()

	if err := s.PutRefined(ctx, key, want); err != nil {
		t.Fatalf("PutRefined: %v", err)
	}

	got, ok, err := s.GetRefined(ctx, key, time.Hour)
	if err != nil {
		t.Fatalf("GetRefined: %v", err)
	}
	if !ok {
		t.Fatal("want a hit, got a miss")
	}
	if got.Text != want.Text {
		t.Errorf("Text: got %q, want %q", got.Text, want.Text)
	}
	if got.Truncated != want.Truncated {
		t.Errorf("Truncated: got %v, want %v", got.Truncated, want.Truncated)
	}
	if got.TokenEstimate != want.TokenEstimate {
		t.Errorf("TokenEstimate: got %d, want %d", got.TokenEstimate, want.TokenEstimate)
	}
	if !got.Refined {
		t.Error("a cached hit must always replay Refined: true")
	}
}

// Each component of RefineKey must independently invalidate the entry: a
// refine output is only valid for the exact content, query, ceiling, and
// prompt it was produced under.
func TestRefinedCache_EveryKeyComponentInvalidates(t *testing.T) {
	base := testKey()

	tests := []struct {
		name   string
		mutate func(RefineKey) RefineKey
	}{
		{"changed content", func(k RefineKey) RefineKey {
			k.ContentHash = HashContent("# Pricing\n\nPro tier is now $25/mo.")
			return k
		}},
		{"changed query", func(k RefineKey) RefineKey {
			k.Query = "does it have a free tier"
			return k
		}},
		{"changed max tokens", func(k RefineKey) RefineKey {
			k.MaxTokens = 400
			return k
		}},
		{"changed prompt version", func(k RefineKey) RefineKey {
			k.PromptVersion = "v2"
			return k
		}},
		{"changed url", func(k RefineKey) RefineKey {
			k.URL = "https://example.com/other"
			return k
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			ctx := context.Background()

			if err := s.PutRefined(ctx, base, testOutput()); err != nil {
				t.Fatalf("PutRefined: %v", err)
			}

			_, ok, err := s.GetRefined(ctx, tc.mutate(base), time.Hour)
			if err != nil {
				t.Fatalf("GetRefined: %v", err)
			}
			if ok {
				t.Fatalf("%s must miss, but hit the cached entry", tc.name)
			}

			// The original key must still hit — the mutation is a different
			// entry, not a corruption of the stored one.
			if _, ok, err := s.GetRefined(ctx, base, time.Hour); err != nil || !ok {
				t.Fatalf("original key stopped hitting: ok=%v err=%v", ok, err)
			}
		})
	}
}

func TestRefinedCache_MissWhenExpired(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	key := testKey()

	if err := s.PutRefined(ctx, key, testOutput()); err != nil {
		t.Fatalf("PutRefined: %v", err)
	}

	_, ok, err := s.GetRefined(ctx, key, time.Nanosecond)
	if err != nil {
		t.Fatalf("GetRefined: %v", err)
	}
	if ok {
		t.Fatal("want a miss for an expired row, got a hit")
	}
}

// SD-2: replaying an unrefined output would hand the consumer text that never
// passed the refiner, so it is never stored in the first place.
func TestRefinedCache_RejectsUnrefinedOutput(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	key := testKey()

	unrefined := testOutput()
	unrefined.Refined = false

	if err := s.PutRefined(ctx, key, unrefined); err != nil {
		t.Fatalf("PutRefined: %v", err)
	}

	if _, ok, err := s.GetRefined(ctx, key, time.Hour); err != nil || ok {
		t.Fatalf("an unrefined output must not be cached: ok=%v err=%v", ok, err)
	}
}

func TestHashContent_IsStableAndDistinguishing(t *testing.T) {
	a := HashContent("same")
	if a != HashContent("same") {
		t.Error("HashContent is not deterministic")
	}
	if a == HashContent("different") {
		t.Error("HashContent collided on different input")
	}
}

// Field boundaries must not be ambiguous: NUL separation means no two
// different field splits can produce the same key.
func TestRefineKey_NoFieldBoundaryCollision(t *testing.T) {
	a := RefineKey{URL: "ab", Query: "c", PromptVersion: "v1"}
	b := RefineKey{URL: "a", Query: "bc", PromptVersion: "v1"}

	if a.id() == b.id() {
		t.Fatal("keys differing only in field boundaries collided")
	}
}
