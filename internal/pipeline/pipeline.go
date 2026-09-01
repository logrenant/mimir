package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/crawl"
	"github.com/logrenant/goat-mcp/internal/mcp"
	"github.com/logrenant/goat-mcp/internal/refine"
	"github.com/logrenant/goat-mcp/internal/search"
	"github.com/logrenant/goat-mcp/internal/store"
)

var ErrNoUsableSources = errors.New("pipeline: all sources failed")

type Query struct {
	Text string
	TopN int
}

type Source struct {
	N     int
	Title string
	URL   string
}

type RefinedPage struct {
	URL       string
	Title     string
	Markdown  string
	Refined   bool
	Truncated bool
}

type Brief struct {
	Summary   string
	KeyPoints []string
	Sources   []Source
	Gaps      []string
	Refined   bool
	Truncated bool
}

// Searcher represents search client interface.
type Searcher interface {
	Search(ctx context.Context, query string, maxResults int) ([]search.Result, error)
}

// Crawler represents crawl client interface.
type Crawler interface {
	Markdown(ctx context.Context, targetURL string) (crawl.Page, error)
}

// Refiner represents refine client interface.
type Refiner interface {
	Distil(ctx context.Context, in refine.Input) (refine.Output, error)
}

// Cache is the optional read-through page cache (task-18). A nil Cache
// disables caching entirely and restores the uncached behaviour exactly.
//
// Implementations may return an error; the pipeline treats any error as a miss
// and proceeds with the live path. A broken cache costs time, never a result
// (SD-6).
type Cache interface {
	GetCrawl(ctx context.Context, url string, ttl time.Duration) (crawl.Page, bool, error)
	PutCrawl(ctx context.Context, page crawl.Page) error
	// GetCrawlFull is GetCrawl plus RawHTML — see FetchRaw / cachedFetchRaw
	// below and store.Store.GetCrawlFull's doc comment for the SD-2
	// justification. PutCrawl already persists RawHTML when present, so
	// there is no separate PutCrawlFull.
	GetCrawlFull(ctx context.Context, url string, ttl time.Duration) (crawl.Page, bool, error)
	GetRefined(ctx context.Context, k store.RefineKey, ttl time.Duration) (refine.Output, bool, error)
	PutRefined(ctx context.Context, k store.RefineKey, out refine.Output) error
}

type Pipeline struct {
	search Searcher
	crawl  Crawler
	refine Refiner
	cache  Cache
	cfg    config.Config
}

// New creates a new pipeline orchestrator. We use interfaces to allow easy
// faking in tests. cache may be nil, which disables caching.
func New(cfg config.Config, s Searcher, c Crawler, r Refiner, cache Cache) *Pipeline {
	InitLimits(cfg)
	return &Pipeline{
		search: s,
		crawl:  c,
		refine: r,
		cache:  cache,
		cfg:    cfg,
	}
}

// cachedCrawl returns the page for url, preferring a cached copy. On a miss it
// takes a global crawl slot and fetches live, then writes back.
//
// A hit takes no slot and makes no external call — that is the entire point.
// Charging a hit a concurrency slot would give the cache back to contention
// under exactly the load it exists to relieve.
func (p *Pipeline) cachedCrawl(ctx context.Context, url string) (crawl.Page, error) {
	if p.cache != nil {
		page, ok, err := p.cache.GetCrawl(ctx, url, p.cfg.PageCacheTTL)
		if err != nil {
			mcp.LoggerFrom(ctx).Debug("cache read failed", "stage", "crawl", "url", url, "error", err)
		} else if ok {
			return page, nil
		}
	}

	rel, err := acquireCrawlSlot(ctx)
	if err != nil {
		return crawl.Page{}, err
	}
	cctx, ccancel := callCtx(ctx, p.cfg.CrawlTimeout)
	page, err := p.crawl.Markdown(cctx, url)
	ccancel()
	rel()
	if err != nil {
		return crawl.Page{}, err
	}

	if p.cache != nil {
		if err := p.cache.PutCrawl(ctx, page); err != nil {
			mcp.LoggerFrom(ctx).Debug("cache write failed", "stage", "crawl", "url", url, "error", err)
		}
	}
	return page, nil
}

// cachedRefine returns the refine output for in, preferring a cached copy. On
// a miss it takes a global refine slot and calls the `claude` CLI, then writes
// back. A hit is the token saving this whole layer exists for.
func (p *Pipeline) cachedRefine(ctx context.Context, in refine.Input) (refine.Output, error) {
	key := store.RefineKey{
		URL:           in.SourceURL,
		Query:         in.Query,
		MaxTokens:     in.MaxTokens,
		ContentHash:   store.HashContent(in.PageMarkdown),
		PromptVersion: p.cfg.RefinePromptVersion,
	}

	if p.cache != nil {
		out, ok, err := p.cache.GetRefined(ctx, key, p.cfg.PageCacheTTL)
		if err != nil {
			mcp.LoggerFrom(ctx).Debug("cache read failed", "stage", "refine", "url", in.SourceURL, "error", err)
		} else if ok {
			return out, nil
		}
	}

	rel, err := acquireRefineSlot(ctx)
	if err != nil {
		return refine.Output{}, err
	}
	rctx, rcancel := callCtx(ctx, p.cfg.RefineTimeout)
	out, err := p.refine.Distil(rctx, in)
	rcancel()
	rel()
	if err != nil {
		return refine.Output{}, err
	}

	if p.cache != nil {
		if err := p.cache.PutRefined(ctx, key, out); err != nil {
			mcp.LoggerFrom(ctx).Debug("cache write failed", "stage", "refine", "url", in.SourceURL, "error", err)
		}
	}
	return out, nil
}

// rawCrawler is the optional widened interface a Crawler implementation may
// satisfy to accept crawl.FetchOptions. Checked via a local type-assertion
// (duck typing) rather than added to the exported Crawler interface, so the
// existing single-method fake Crawler test doubles across the package need
// no changes.
type rawCrawler interface {
	MarkdownWithOptions(ctx context.Context, targetURL string, opts crawl.FetchOptions) (crawl.Page, error)
}

// cachedFetchRaw is FetchRaw's read-through counterpart to cachedCrawl,
// caching the *full* page (RawHTML included) via store.GetCrawlFull /
// PutCrawl instead of the refine-only GetCrawl.
//
// Caching by url alone (opts is not part of the key) is deliberate, not an
// oversight: every FetchRaw call site passes a fixed, tool-specific
// FetchOptions for its own URL domain (internal/gmaps always waits for "h1"
// on maps.google.com URLs; internal/tiktok/instagram/ecommerce pass the
// zero value for their own domains) — SD-1 means there is no knob that could
// vary opts for a given URL between calls. If a future caller ever needs the
// same URL fetched under two different option sets, opts must join the key.
//
// Same slot discipline as cachedCrawl: a hit takes no global crawl slot.
func (p *Pipeline) cachedFetchRaw(ctx context.Context, url string, opts crawl.FetchOptions) (crawl.Page, error) {
	if p.cache != nil {
		page, ok, err := p.cache.GetCrawlFull(ctx, url, p.cfg.PageCacheTTL)
		if err != nil {
			mcp.LoggerFrom(ctx).Debug("cache read failed", "stage", "crawl_raw", "url", url, "error", err)
		} else if ok {
			return page, nil
		}
	}

	rel, err := acquireCrawlSlot(ctx)
	if err != nil {
		return crawl.Page{}, err
	}
	cctx, ccancel := callCtx(ctx, p.cfg.CrawlTimeout)
	var page crawl.Page
	if oc, ok := p.crawl.(rawCrawler); ok {
		page, err = oc.MarkdownWithOptions(cctx, url, opts)
	} else {
		page, err = p.crawl.Markdown(cctx, url)
	}
	ccancel()
	rel()
	if err != nil {
		return crawl.Page{}, err
	}

	if p.cache != nil {
		if err := p.cache.PutCrawl(ctx, page); err != nil {
			mcp.LoggerFrom(ctx).Debug("cache write failed", "stage", "crawl_raw", "url", url, "error", err)
		}
	}
	return page, nil
}

// FetchRaw crawls a single URL and returns the raw Page — no refine call.
// For tools that extract structured fields deterministically
// (internal/extract) instead of LLM-distilling free text (SD-2 does not
// apply to the tool's own response: nothing here is returned to the MCP
// consumer as free text without that tool's own clamping — see
// internal/extract.ClampText). Bounded by the same GlobalCrawlSlots
// semaphore as Fetch/Research on a miss, so tools calling FetchRaw share one
// process-wide crawl budget instead of an ungoverned direct path to
// Crawl4AI. Read-through cached like Fetch/Research (task-18); see
// cachedFetchRaw for the contract.
func (p *Pipeline) FetchRaw(ctx context.Context, url string, opts crawl.FetchOptions) (crawl.Page, error) {
	return p.cachedFetchRaw(ctx, url, opts)
}

// Fetch handles crawling and refining a single page.
func (p *Pipeline) Fetch(ctx context.Context, url string) (RefinedPage, error) {
	page, err := p.cachedCrawl(ctx, url)
	if err != nil {
		return RefinedPage{}, fmt.Errorf("crawl failed: %w", err)
	}

	out, err := p.cachedRefine(ctx, refine.Input{
		Query:        "", // Fetch usually doesn't have a specific query
		PageMarkdown: page.Markdown,
		SourceURL:    page.URL,
		MaxTokens:    p.cfg.FetchPageMaxTokens,
	})
	if err != nil {
		return RefinedPage{}, fmt.Errorf("refine failed: %w", err)
	}

	return RefinedPage{
		URL:       page.URL,
		Title:     page.Title,
		Markdown:  out.Text,
		Refined:   out.Refined,
		Truncated: out.Truncated,
	}, nil
}

// Research orchestrates searching, fanned-out crawling, and fanned-out refining.
//
// SD-3: partial results are tolerated. The overall ResearchTimeout firing does
// NOT abort the call — the brief is built from whatever crawl+refine produced,
// with the missing sources noted in Gaps. Only genuine caller cancellation
// (the parent context) or "every source failed" returns an error.
func (p *Pipeline) Research(ctx context.Context, q Query) (Brief, error) {
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, p.cfg.ResearchTimeout)
	defer cancel()

	topN := q.TopN
	if topN > p.cfg.TopNForResearch {
		topN = p.cfg.TopNForResearch
	}
	
	searchCount := topN
	if searchCount < p.cfg.SearchDefaultCount {
		searchCount = p.cfg.SearchDefaultCount
	}

	// 1. Search
	sStart := time.Now()
	sRes, err := p.search.Search(ctx, q.Text, searchCount)
	mcp.LogStage(ctx, "search", time.Since(sStart).Milliseconds())
	if err != nil {
		return Brief{}, fmt.Errorf("search failed: %w", err)
	}

	if len(sRes) > topN {
		sRes = sRes[:topN]
	}
	if len(sRes) == 0 {
		return Brief{Gaps: []string{"Search returned no results"}}, ErrNoUsableSources
	}

	var mu sync.Mutex
	var rawPages []crawl.Page
	var gaps []string

	// 2. Crawl phase
	cStart := time.Now()
	gCrawl, ctxCrawl := errgroup.WithContext(ctx)
	gCrawl.SetLimit(p.cfg.MaxConcurrentCrawls)

	for _, res := range sRes {
		res := res // loop capture
		gCrawl.Go(func() error {
			// Deadline already reached before this source got a slot: record it as
			// a gap, do not fail the group (that would cancel healthy siblings).
			select {
			case <-ctxCrawl.Done():
				mu.Lock()
				gaps = append(gaps, fmt.Sprintf("skipped %s: %v", res.URL, context.Cause(ctxCrawl)))
				mu.Unlock()
				return nil
			default:
			}

			page, err := p.cachedCrawl(ctxCrawl, res.URL)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				gaps = append(gaps, fmt.Sprintf("Failed to crawl %s: %v", res.URL, err))
			} else {
				// use search title if crawl title is empty
				if page.Title == "" {
					page.Title = res.Title
				}
				rawPages = append(rawPages, page)
			}
			return nil // Never fail the group on individual crawl error
		})
	}
	_ = gCrawl.Wait()
	mcp.LogStage(ctx, "crawl", time.Since(cStart).Milliseconds())

	// Only the caller cancelling (parent ctx) aborts; our own ResearchTimeout
	// degrades to partial results.
	if err := parent.Err(); err != nil {
		return Brief{}, err
	}

	if len(rawPages) == 0 {
		return Brief{Gaps: gaps}, ErrNoUsableSources
	}

	// 3. Refine phase
	var refinedPages []RefinedPage
	tokensPerSource := p.cfg.ResearchBriefMaxTokens / len(rawPages)
	if tokensPerSource < 100 {
		tokensPerSource = 100 // sensible lower bound
	}

	gRefine, ctxRefine := errgroup.WithContext(ctx)
	gRefine.SetLimit(p.cfg.MaxConcurrentRefines)
	rStart := time.Now()

	for _, page := range rawPages {
		page := page
		gRefine.Go(func() error {
			select {
			case <-ctxRefine.Done():
				mu.Lock()
				gaps = append(gaps, fmt.Sprintf("skipped refine %s: %v", page.URL, context.Cause(ctxRefine)))
				mu.Unlock()
				return nil
			default:
			}

			out, err := p.cachedRefine(ctxRefine, refine.Input{
				Query:        q.Text,
				PageMarkdown: page.Markdown,
				SourceURL:    page.URL,
				MaxTokens:    tokensPerSource,
			})

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				gaps = append(gaps, fmt.Sprintf("Failed to refine %s: %v", page.URL, err))
			} else {
				refinedPages = append(refinedPages, RefinedPage{
					URL:       page.URL,
					Title:     page.Title,
					Markdown:  out.Text,
					Refined:   out.Refined,
					Truncated: out.Truncated,
				})
			}
			return nil
		})
	}
	_ = gRefine.Wait()
	mcp.LogStage(ctx, "refine", time.Since(rStart).Milliseconds())

	if err := parent.Err(); err != nil {
		return Brief{}, err
	}

	if len(refinedPages) == 0 {
		return Brief{Gaps: gaps}, ErrNoUsableSources
	}

	// 4. Merge results
	mStart := time.Now()
	brief := mergeResults(refinedPages, gaps, p.cfg.ResearchBriefMaxTokens)
	mcp.LogStage(ctx, "merge", time.Since(mStart).Milliseconds())

	// Final verification
	if len(brief.Sources) == 0 {
		return Brief{Gaps: brief.Gaps}, ErrNoUsableSources
	}

	// 5. Synthesis pass: one final bounded refine over the merged key points to
	// produce a real summary. Degrade to the templated summary on failure (SD-6).
	brief.Summary = p.synthesizeSummary(ctx, q.Text, brief)

	// Every included key point came through refine.
	brief.Refined = true

	return brief, nil
}

// synthesizeSummary runs a final refine over the merged key points. On any
// failure it returns the templated fallback already on brief.Summary.
//
// This goes through the cache like every other refine: when the sources all
// hit, the merged key points are byte-identical to the previous run, so the
// summary hits too. That is what takes a repeated `research` from one `claude`
// call down to zero.
func (p *Pipeline) synthesizeSummary(ctx context.Context, query string, brief Brief) string {
	if len(brief.KeyPoints) == 0 {
		return brief.Summary
	}

	out, err := p.cachedRefine(ctx, refine.Input{
		Query:        query,
		PageMarkdown: strings.Join(brief.KeyPoints, "\n"),
		SourceURL:    "",
		MaxTokens:    p.cfg.SummaryMaxTokens,
	})
	if err != nil || strings.TrimSpace(out.Text) == "" {
		return brief.Summary
	}
	return strings.TrimSpace(out.Text)
}

// callCtx derives a per-call context bounded by d. If d <= 0 the parent is
// returned unchanged with a no-op cancel (keeps zero-value test configs working).
func callCtx(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}
