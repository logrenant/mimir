package pipeline

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/crawl"
	"github.com/logrenant/mimir/internal/refine"
	"github.com/logrenant/mimir/internal/search"
	"github.com/logrenant/mimir/internal/store"
)

// fakeCache records every call and can be configured to hit, miss, or error
// independently per method.
type fakeCache struct {
	mu sync.Mutex

	crawlHits   map[string]crawl.Page
	refinedHits map[string]refine.Output

	getCrawlErr   error
	putCrawlErr   error
	getRefinedErr error
	putRefinedErr error

	getCrawlCalls     int32
	putCrawlCalls     int32
	getCrawlFullCalls int32
	getRefinedCalls   int32
	putRefinedCalls   int32

	putCrawlURLs   []string
	putRefinedKeys []store.RefineKey
}

func newFakeCache() *fakeCache {
	return &fakeCache{
		crawlHits:   map[string]crawl.Page{},
		refinedHits: map[string]refine.Output{},
	}
}

func (f *fakeCache) GetCrawl(ctx context.Context, url string, ttl time.Duration) (crawl.Page, bool, error) {
	atomic.AddInt32(&f.getCrawlCalls, 1)
	if f.getCrawlErr != nil {
		return crawl.Page{}, false, f.getCrawlErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	page, ok := f.crawlHits[url]
	return page, ok, nil
}

// GetCrawlFull shares crawlHits with GetCrawl — the fake stores whole
// crawl.Page values (RawHTML included), so there is nothing column-shaped to
// simulate here; only the real store.Store distinguishes the two by SELECT.
func (f *fakeCache) GetCrawlFull(ctx context.Context, url string, ttl time.Duration) (crawl.Page, bool, error) {
	atomic.AddInt32(&f.getCrawlFullCalls, 1)
	if f.getCrawlErr != nil {
		return crawl.Page{}, false, f.getCrawlErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	page, ok := f.crawlHits[url]
	return page, ok, nil
}

func (f *fakeCache) PutCrawl(ctx context.Context, page crawl.Page) error {
	atomic.AddInt32(&f.putCrawlCalls, 1)
	if f.putCrawlErr != nil {
		return f.putCrawlErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.putCrawlURLs = append(f.putCrawlURLs, page.URL)
	return nil
}

func (f *fakeCache) GetRefined(ctx context.Context, k store.RefineKey, ttl time.Duration) (refine.Output, bool, error) {
	atomic.AddInt32(&f.getRefinedCalls, 1)
	if f.getRefinedErr != nil {
		return refine.Output{}, false, f.getRefinedErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out, ok := f.refinedHits[k.URL]
	return out, ok, nil
}

func (f *fakeCache) PutRefined(ctx context.Context, k store.RefineKey, out refine.Output) error {
	atomic.AddInt32(&f.putRefinedCalls, 1)
	if f.putRefinedErr != nil {
		return f.putRefinedErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.putRefinedKeys = append(f.putRefinedKeys, k)
	return nil
}

func (f *fakeCache) putCrawlURLsSnapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.putCrawlURLs...)
}

func (f *fakeCache) refineKeysSnapshot() []store.RefineKey {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]store.RefineKey(nil), f.putRefinedKeys...)
}

// countingCrawler counts how many live crawls actually happened.
type countingCrawler struct {
	page  crawl.Page
	err   error
	calls int32
}

func (c *countingCrawler) Markdown(ctx context.Context, targetURL string) (crawl.Page, error) {
	atomic.AddInt32(&c.calls, 1)
	if c.err != nil {
		return crawl.Page{}, c.err
	}
	page := c.page
	page.URL = targetURL
	return page, nil
}

// countingRefiner counts how many live `claude` calls actually happened.
type countingRefiner struct {
	out   refine.Output
	err   error
	calls int32
}

func (r *countingRefiner) Distil(ctx context.Context, in refine.Input) (refine.Output, error) {
	atomic.AddInt32(&r.calls, 1)
	if r.err != nil {
		return refine.Output{}, r.err
	}
	return r.out, nil
}

func cacheTestConfig() config.Config {
	cfg := config.Load()
	cfg.ResearchTimeout = 5 * time.Second
	cfg.CrawlTimeout = 2 * time.Second
	cfg.RefineTimeout = 2 * time.Second
	return cfg
}

const (
	testURL      = "https://example.com/a"
	testMarkdown = "# A\n\nraw scraped body"
)

func livePage() crawl.Page {
	return crawl.Page{URL: testURL, Title: "A", Markdown: testMarkdown, FetchedAt: time.Now()}
}

func liveOutput() refine.Output {
	return refine.Output{Text: "- live refined", Refined: true, TokenEstimate: 4}
}

func TestFetch_CrawlHitSkipsCrawler(t *testing.T) {
	cache := newFakeCache()
	cached := crawl.Page{URL: testURL, Title: "Cached A", Markdown: "cached body", FetchedAt: time.Now()}
	cache.crawlHits[testURL] = cached

	crawler := &countingCrawler{page: livePage()}
	refiner := &countingRefiner{out: liveOutput()}
	p := New(cacheTestConfig(), &fakeSearcher{}, crawler, refiner, cache)

	got, err := p.Fetch(context.Background(), testURL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if n := atomic.LoadInt32(&crawler.calls); n != 0 {
		t.Errorf("a crawl hit must not call the crawler, got %d calls", n)
	}
	if got.Title != "Cached A" {
		t.Errorf("Title: got %q, want the cached page's title", got.Title)
	}

	// The refine still runs — its input is the cached body, so the key must be
	// derived from the cached content, not the live one.
	keys := cache.refineKeysSnapshot()
	if len(keys) != 1 {
		t.Fatalf("want 1 refine write, got %d", len(keys))
	}
	if keys[0].ContentHash != store.HashContent("cached body") {
		t.Error("refine key was not derived from the cached page content")
	}
}

func TestFetch_RefineHitSkipsClaude(t *testing.T) {
	cache := newFakeCache()
	cache.refinedHits[testURL] = refine.Output{Text: "- cached refined", Refined: true, Truncated: true, TokenEstimate: 9}

	crawler := &countingCrawler{page: livePage()}
	refiner := &countingRefiner{out: liveOutput()}
	p := New(cacheTestConfig(), &fakeSearcher{}, crawler, refiner, cache)

	got, err := p.Fetch(context.Background(), testURL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if n := atomic.LoadInt32(&refiner.calls); n != 0 {
		t.Errorf("a refine hit must not spawn the claude CLI, got %d calls", n)
	}
	if got.Markdown != "- cached refined" {
		t.Errorf("Markdown: got %q, want the cached refined text", got.Markdown)
	}
	if !got.Truncated {
		t.Error("the cached Truncated flag must survive replay — the choke-point relies on it")
	}
	if !got.Refined {
		t.Error("a replayed hit must be marked refined")
	}
}

func TestFetch_MissPopulatesCacheExactlyOnce(t *testing.T) {
	cache := newFakeCache()
	crawler := &countingCrawler{page: livePage()}
	refiner := &countingRefiner{out: liveOutput()}
	p := New(cacheTestConfig(), &fakeSearcher{}, crawler, refiner, cache)

	if _, err := p.Fetch(context.Background(), testURL); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if got := atomic.LoadInt32(&cache.putCrawlCalls); got != 1 {
		t.Errorf("PutCrawl calls: got %d, want 1", got)
	}
	if got := atomic.LoadInt32(&cache.putRefinedCalls); got != 1 {
		t.Errorf("PutRefined calls: got %d, want 1", got)
	}
	if urls := cache.putCrawlURLsSnapshot(); len(urls) != 1 || urls[0] != testURL {
		t.Errorf("PutCrawl URLs: got %v, want [%s]", urls, testURL)
	}
}

func TestFetch_NilCacheBehavesAsUncached(t *testing.T) {
	crawler := &countingCrawler{page: livePage()}
	refiner := &countingRefiner{out: liveOutput()}
	p := New(cacheTestConfig(), &fakeSearcher{}, crawler, refiner, nil)

	got, err := p.Fetch(context.Background(), testURL)
	if err != nil {
		t.Fatalf("Fetch with a nil cache: %v", err)
	}
	if got.Markdown != liveOutput().Text {
		t.Errorf("Markdown: got %q, want the live refined text", got.Markdown)
	}
	if n := atomic.LoadInt32(&crawler.calls); n != 1 {
		t.Errorf("crawler calls: got %d, want 1", n)
	}
	if n := atomic.LoadInt32(&refiner.calls); n != 1 {
		t.Errorf("refiner calls: got %d, want 1", n)
	}
}

// SD-6: a broken cache degrades to the live path, it never fails a call.
func TestFetch_ErroringCacheStillReturnsLiveResult(t *testing.T) {
	boom := errors.New("disk on fire")
	cache := newFakeCache()
	cache.getCrawlErr = boom
	cache.putCrawlErr = boom
	cache.getRefinedErr = boom
	cache.putRefinedErr = boom

	crawler := &countingCrawler{page: livePage()}
	refiner := &countingRefiner{out: liveOutput()}
	p := New(cacheTestConfig(), &fakeSearcher{}, crawler, refiner, cache)

	got, err := p.Fetch(context.Background(), testURL)
	if err != nil {
		t.Fatalf("an erroring cache must not fail the call, got %v", err)
	}
	if got.Markdown != liveOutput().Text {
		t.Errorf("Markdown: got %q, want the live refined text", got.Markdown)
	}
	if n := atomic.LoadInt32(&crawler.calls); n != 1 {
		t.Errorf("crawler calls: got %d, want 1 (live fallback)", n)
	}
}

// A fully-cached research must spend nothing: no crawls, and no claude calls
// at all — including the final summary synthesis.
func TestResearch_FullyCachedSpendsNothing(t *testing.T) {
	cfg := cacheTestConfig()
	cfg.TopNForResearch = 2
	cfg.SearchDefaultCount = 2

	results := []search.Result{
		{Title: "A", URL: "https://example.com/a", Snippet: "a"},
		{Title: "B", URL: "https://example.com/b", Snippet: "b"},
	}

	cache := newFakeCache()
	for _, r := range results {
		cache.crawlHits[r.URL] = crawl.Page{URL: r.URL, Title: r.Title, Markdown: "cached " + r.Title}
		cache.refinedHits[r.URL] = refine.Output{Text: "- cached point " + r.Title, Refined: true, TokenEstimate: 5}
	}
	// The summary pass refines the merged key points with an empty SourceURL.
	cache.refinedHits[""] = refine.Output{Text: "cached summary", Refined: true, TokenEstimate: 3}

	crawler := &countingCrawler{page: livePage()}
	refiner := &countingRefiner{out: liveOutput()}
	p := New(cfg, &fakeSearcher{results: results}, crawler, refiner, cache)

	brief, err := p.Research(context.Background(), Query{Text: "q", TopN: 2})
	if err != nil {
		t.Fatalf("Research: %v", err)
	}

	if n := atomic.LoadInt32(&crawler.calls); n != 0 {
		t.Errorf("fully cached research made %d live crawls, want 0", n)
	}
	if n := atomic.LoadInt32(&refiner.calls); n != 0 {
		t.Errorf("fully cached research made %d claude calls, want 0", n)
	}
	if brief.Summary != "cached summary" {
		t.Errorf("Summary: got %q, want the cached summary", brief.Summary)
	}
	if len(brief.Sources) != 2 {
		t.Errorf("Sources: got %d, want 2", len(brief.Sources))
	}
}

// FetchRaw's cache path must return RawHTML on a hit — that is the entire
// reason it uses GetCrawlFull instead of GetCrawl.
func TestFetchRaw_CacheHitSkipsCrawlerAndKeepsRawHTML(t *testing.T) {
	cache := newFakeCache()
	cache.crawlHits[testURL] = crawl.Page{
		URL: testURL, Title: "Cached A", Markdown: "cached body", RawHTML: "<html>cached</html>",
	}

	crawler := &countingCrawler{page: livePage()}
	p := New(cacheTestConfig(), &fakeSearcher{}, crawler, &countingRefiner{}, cache)

	got, err := p.FetchRaw(context.Background(), testURL, crawl.FetchOptions{})
	if err != nil {
		t.Fatalf("FetchRaw: %v", err)
	}
	if n := atomic.LoadInt32(&crawler.calls); n != 0 {
		t.Errorf("a crawl hit must not call the crawler, got %d calls", n)
	}
	if got.RawHTML != "<html>cached</html>" {
		t.Errorf("RawHTML: got %q, want the cached html", got.RawHTML)
	}
	if n := atomic.LoadInt32(&cache.getCrawlFullCalls); n != 1 {
		t.Errorf("GetCrawlFull calls: got %d, want 1", n)
	}
	if n := atomic.LoadInt32(&cache.getCrawlCalls); n != 0 {
		t.Errorf("FetchRaw must not go through the refine-only GetCrawl, got %d calls", n)
	}
}

// A miss must fetch live and write the RawHTML back via PutCrawl.
func TestFetchRaw_CacheMissPopulatesCacheWithRawHTML(t *testing.T) {
	cache := newFakeCache()
	live := livePage()
	live.RawHTML = "<html>live</html>"
	crawler := &countingCrawler{page: live}
	p := New(cacheTestConfig(), &fakeSearcher{}, crawler, &countingRefiner{}, cache)

	got, err := p.FetchRaw(context.Background(), testURL, crawl.FetchOptions{})
	if err != nil {
		t.Fatalf("FetchRaw: %v", err)
	}
	if got.RawHTML != "<html>live</html>" {
		t.Errorf("RawHTML: got %q, want the live html", got.RawHTML)
	}
	if n := atomic.LoadInt32(&crawler.calls); n != 1 {
		t.Errorf("crawler calls: got %d, want 1", n)
	}
	if n := atomic.LoadInt32(&cache.putCrawlCalls); n != 1 {
		t.Errorf("PutCrawl calls: got %d, want 1", n)
	}
}

// FetchOptions must still reach the crawler on a miss — caching must not
// swallow the WaitForSelector/PageTimeout knobs internal/gmaps relies on.
func TestFetchRaw_CacheMissPassesOptionsThrough(t *testing.T) {
	cache := newFakeCache()
	c := &fakeCrawlerWithOptions{page: crawl.Page{URL: testURL, Markdown: "md", RawHTML: "<html></html>"}}
	p := New(cacheTestConfig(), &fakeSearcher{}, c, &countingRefiner{}, cache)

	opts := crawl.FetchOptions{WaitForSelector: "h1", PageTimeout: 5 * time.Second}
	if _, err := p.FetchRaw(context.Background(), testURL, opts); err != nil {
		t.Fatalf("FetchRaw: %v", err)
	}
	if !c.optsCall {
		t.Fatal("expected MarkdownWithOptions to be called on a miss, not the fallback Markdown")
	}
	if c.gotOpts != opts {
		t.Errorf("expected opts %+v to be passed through, got %+v", opts, c.gotOpts)
	}
}

// SD-6: a broken cache degrades to the live path for FetchRaw too.
func TestFetchRaw_ErroringCacheStillReturnsLiveResult(t *testing.T) {
	boom := errors.New("disk on fire")
	cache := newFakeCache()
	cache.getCrawlErr = boom // shared by GetCrawlFull in the fake, see its doc comment
	cache.putCrawlErr = boom

	live := livePage()
	live.RawHTML = "<html>live</html>"
	crawler := &countingCrawler{page: live}
	p := New(cacheTestConfig(), &fakeSearcher{}, crawler, &countingRefiner{}, cache)

	got, err := p.FetchRaw(context.Background(), testURL, crawl.FetchOptions{})
	if err != nil {
		t.Fatalf("an erroring cache must not fail FetchRaw, got %v", err)
	}
	if got.RawHTML != "<html>live</html>" {
		t.Errorf("RawHTML: got %q, want the live html", got.RawHTML)
	}
	if n := atomic.LoadInt32(&crawler.calls); n != 1 {
		t.Errorf("crawler calls: got %d, want 1 (live fallback)", n)
	}
}

// A FetchRaw cache hit must not consume a global crawl slot either.
func TestCachedFetchRaw_HitTakesNoGlobalSlot(t *testing.T) {
	cache := newFakeCache()
	cache.crawlHits[testURL] = crawl.Page{URL: testURL, Title: "A", Markdown: "cached body", RawHTML: "<html></html>"}

	crawler := &countingCrawler{page: livePage()}
	p := New(cacheTestConfig(), &fakeSearcher{}, crawler, &countingRefiner{}, cache)

	ctx := context.Background()
	var held int64
	for crawlSem.TryAcquire(1) {
		held++
	}
	defer crawlSem.Release(held)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := p.FetchRaw(ctx, testURL, crawl.FetchOptions{}); err != nil {
			t.Errorf("FetchRaw on a fully-cached URL with no free slots: %v", err)
		}
	}()

	select {
	case <-done:
		// Correct: the hit never asked for a slot.
	case <-time.After(2 * time.Second):
		t.Fatal("a FetchRaw cache hit blocked on a global crawl slot")
	}
}

// A crawl hit must not consume a global crawl slot. With exactly one slot
// available and it already held elsewhere, a cache hit must still complete.
func TestCachedCrawl_HitTakesNoGlobalSlot(t *testing.T) {
	cache := newFakeCache()
	cache.crawlHits[testURL] = crawl.Page{URL: testURL, Title: "A", Markdown: "cached body"}
	cache.refinedHits[testURL] = refine.Output{Text: "- cached", Refined: true}

	crawler := &countingCrawler{page: livePage()}
	refiner := &countingRefiner{out: liveOutput()}
	p := New(cacheTestConfig(), &fakeSearcher{}, crawler, refiner, cache)

	// Drain every global crawl slot the process has, so any attempt to acquire
	// one would block. InitLimits is a sync.Once, so the live slot count is
	// whatever the first New in this package set.
	ctx := context.Background()
	var held int64
	for crawlSem.TryAcquire(1) {
		held++
	}
	defer crawlSem.Release(held)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := p.Fetch(ctx, testURL); err != nil {
			t.Errorf("Fetch on a fully-cached URL with no free slots: %v", err)
		}
	}()

	select {
	case <-done:
		// Correct: the hit never asked for a slot.
	case <-time.After(2 * time.Second):
		t.Fatal("a cache hit blocked on a global crawl slot — the cache buys nothing under load")
	}
}
