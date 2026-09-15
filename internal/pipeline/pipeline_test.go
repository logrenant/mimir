package pipeline

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/crawl"
	"github.com/logrenant/mimir/internal/refine"
	"github.com/logrenant/mimir/internal/search"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

type fakeSearcher struct {
	results []search.Result
	err     error
}

func (f *fakeSearcher) Search(ctx context.Context, query string, maxResults int) ([]search.Result, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.results, nil
}

type fakeCrawler struct {
	pages  map[string]crawl.Page
	errs   map[string]error
	delay  time.Duration
	delays map[string]time.Duration // per-URL delay override

	active    int32
	maxActive int32
}

func (f *fakeCrawler) Markdown(ctx context.Context, targetURL string) (crawl.Page, error) {
	cur := atomic.AddInt32(&f.active, 1)
	defer atomic.AddInt32(&f.active, -1)

	for {
		old := atomic.LoadInt32(&f.maxActive)
		if cur <= old || atomic.CompareAndSwapInt32(&f.maxActive, old, cur) {
			break
		}
	}

	d := f.delay
	if dd, ok := f.delays[targetURL]; ok {
		d = dd
	}

	if d > 0 {
		select {
		case <-ctx.Done():
			return crawl.Page{}, ctx.Err()
		case <-time.After(d):
		}
	} else {
		if err := ctx.Err(); err != nil {
			return crawl.Page{}, err
		}
	}

	if err, ok := f.errs[targetURL]; ok {
		return crawl.Page{}, err
	}
	return f.pages[targetURL], nil
}

type fakeRefiner struct {
	out   map[string]refine.Output
	errs  map[string]error
	delay time.Duration

	active    int32
	maxActive int32
}

func (f *fakeRefiner) Distil(ctx context.Context, in refine.Input) (refine.Output, error) {
	cur := atomic.AddInt32(&f.active, 1)
	defer atomic.AddInt32(&f.active, -1)

	for {
		old := atomic.LoadInt32(&f.maxActive)
		if cur <= old || atomic.CompareAndSwapInt32(&f.maxActive, old, cur) {
			break
		}
	}

	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return refine.Output{}, ctx.Err()
		case <-time.After(f.delay):
		}
	} else {
		if err := ctx.Err(); err != nil {
			return refine.Output{}, err
		}
	}

	// Final synthesis pass (pipeline passes an empty SourceURL).
	if in.SourceURL == "" {
		if o, ok := f.out[""]; ok {
			return o, nil
		}
		return refine.Output{Text: "Synthesized summary of findings.", Refined: true}, nil
	}

	if err, ok := f.errs[in.SourceURL]; ok {
		return refine.Output{}, err
	}
	return f.out[in.SourceURL], nil
}

func TestPipeline_Research_HappyPath(t *testing.T) {
	cfg := config.Config{
		ResearchTimeout:        5 * time.Second,
		TopNForResearch:        5,
		SearchDefaultCount:     5,
		MaxConcurrentCrawls:    2,
		MaxConcurrentRefines:   2,
		ResearchBriefMaxTokens: 1000,
	}

	s := &fakeSearcher{
		results: []search.Result{
			{URL: "u1", Title: "t1"},
			{URL: "u2", Title: "t2"},
		},
	}
	c := &fakeCrawler{
		pages: map[string]crawl.Page{
			"u1": {URL: "u1", Title: "t1", Markdown: "md1"},
			"u2": {URL: "u2", Title: "t2", Markdown: "md2"},
		},
	}
	r := &fakeRefiner{
		out: map[string]refine.Output{
			"u1": {Text: "- point 1\n- point 2", Refined: true},
			"u2": {Text: "- point 3\n- point 4", Refined: true},
		},
	}

	p := New(cfg, s, c, r, nil)
	brief, err := p.Research(context.Background(), Query{Text: "test", TopN: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(brief.Sources) != 2 {
		t.Errorf("expected 2 sources, got %d", len(brief.Sources))
	}
	if len(brief.Gaps) != 0 {
		t.Errorf("expected 0 gaps, got %d", len(brief.Gaps))
	}
	if len(brief.KeyPoints) != 4 {
		t.Errorf("expected 4 key points, got %d", len(brief.KeyPoints))
	}
	if !brief.Refined {
		t.Errorf("expected refined to be true")
	}
	// FIX-4: summary comes from the synthesis pass, not the merge template.
	if brief.Summary != "Synthesized summary of findings." {
		t.Errorf("expected synthesized summary, got %q", brief.Summary)
	}
}

func TestPipeline_Research_SummaryFallback(t *testing.T) {
	cfg := config.Config{
		ResearchTimeout:        5 * time.Second,
		TopNForResearch:        5,
		SearchDefaultCount:     5,
		MaxConcurrentCrawls:    2,
		MaxConcurrentRefines:   2,
		ResearchBriefMaxTokens: 1000,
		SummaryMaxTokens:       200,
	}

	s := &fakeSearcher{results: []search.Result{{URL: "u1", Title: "t1"}}}
	c := &fakeCrawler{pages: map[string]crawl.Page{"u1": {URL: "u1", Title: "t1", Markdown: "md1"}}}
	r := &fakeRefiner{
		out: map[string]refine.Output{
			"u1": {Text: "- point 1", Refined: true},
			"":   {}, // synthesis pass returns empty -> fallback to template
		},
	}

	p := New(cfg, s, c, r, nil)
	brief, err := p.Research(context.Background(), Query{Text: "test", TopN: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if brief.Summary != "Synthesized findings from 1 source(s)." {
		t.Errorf("expected templated fallback summary, got %q", brief.Summary)
	}
}

// SD-3 / task-10 DoD: 5 sources, 2 slow past the per-crawl timeout + 1 crawl
// error -> a Brief is still built from the 2 fast sources, the 3 missing ones are
// recorded in Gaps, and the call returns well before a naive (unbounded) wait for
// the slow ones.
func TestPipeline_Research_PartialResultsOnDeadline(t *testing.T) {
	cfg := config.Config{
		ResearchTimeout:        5 * time.Second,
		CrawlTimeout:           60 * time.Millisecond, // per-source crawl budget
		RefineTimeout:          1 * time.Second,
		TopNForResearch:        5,
		SearchDefaultCount:     5,
		MaxConcurrentCrawls:    5,
		MaxConcurrentRefines:   5,
		ResearchBriefMaxTokens: 1000,
		SummaryMaxTokens:       200,
	}

	s := &fakeSearcher{
		results: []search.Result{
			{URL: "fast1", Title: "f1"}, {URL: "fail_crawl", Title: "fc"},
			{URL: "fast2", Title: "f2"}, {URL: "slow1", Title: "s1"}, {URL: "slow2", Title: "s2"},
		},
	}
	c := &fakeCrawler{
		errs: map[string]error{"fail_crawl": errors.New("boom crawl")},
		delays: map[string]time.Duration{
			"slow1": 2 * time.Second,
			"slow2": 2 * time.Second,
		},
		pages: map[string]crawl.Page{
			"fast1": {URL: "fast1", Title: "f1", Markdown: "md1"},
			"fast2": {URL: "fast2", Title: "f2", Markdown: "md2"},
			"slow1": {URL: "slow1", Title: "s1", Markdown: "md3"},
			"slow2": {URL: "slow2", Title: "s2", Markdown: "md4"},
		},
	}
	r := &fakeRefiner{
		out: map[string]refine.Output{
			"fast1": {Text: "- point 1", Refined: true},
			"fast2": {Text: "- point 2", Refined: true},
		},
	}

	p := New(cfg, s, c, r, nil)

	start := time.Now()
	brief, err := p.Research(context.Background(), Query{Text: "test", TopN: 5})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected a partial brief, got error: %v", err)
	}
	if len(brief.Sources) != 2 {
		t.Errorf("expected 2 sources from the fast crawls, got %d (%+v)", len(brief.Sources), brief.Sources)
	}
	if len(brief.Gaps) != 3 {
		t.Errorf("expected 3 gap entries (1 crawl error + 2 timeouts), got %d: %v", len(brief.Gaps), brief.Gaps)
	}
	if !brief.Refined {
		t.Errorf("expected refined to be true")
	}
	// Must not have waited on the 2s slow crawls.
	if elapsed > 800*time.Millisecond {
		t.Errorf("expected prompt return near ResearchTimeout, took %v", elapsed)
	}
}

// A genuine caller cancellation (parent context) still aborts promptly.
func TestPipeline_Research_ParentCancelAborts(t *testing.T) {
	cfg := config.Config{
		ResearchTimeout:        5 * time.Second,
		TopNForResearch:        5,
		SearchDefaultCount:     5,
		MaxConcurrentCrawls:    2,
		MaxConcurrentRefines:   2,
		ResearchBriefMaxTokens: 1000,
		SummaryMaxTokens:       200,
	}

	s := &fakeSearcher{results: []search.Result{{URL: "u1"}, {URL: "u2"}}}
	c := &fakeCrawler{
		delay: 2 * time.Second,
		pages: map[string]crawl.Page{"u1": {URL: "u1", Markdown: "md1"}, "u2": {URL: "u2", Markdown: "md2"}},
	}
	r := &fakeRefiner{out: map[string]refine.Output{}}

	p := New(cfg, s, c, r, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := p.Research(ctx, Query{Text: "test", TopN: 5})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Errorf("expected prompt abort on caller cancel, took %v", time.Since(start))
	}
}

func TestPipeline_Research_AllFailed(t *testing.T) {
	cfg := config.Config{
		ResearchTimeout:        5 * time.Second,
		TopNForResearch:        5,
		SearchDefaultCount:     5,
		MaxConcurrentCrawls:    2,
		MaxConcurrentRefines:   2,
		ResearchBriefMaxTokens: 1000,
	}

	s := &fakeSearcher{
		results: []search.Result{
			{URL: "fail_crawl1"}, {URL: "fail_crawl2"},
		},
	}
	c := &fakeCrawler{
		errs: map[string]error{
			"fail_crawl1": errors.New("boom"),
			"fail_crawl2": errors.New("boom"),
		},
	}
	r := &fakeRefiner{}

	p := New(cfg, s, c, r, nil)
	_, err := p.Research(context.Background(), Query{Text: "test", TopN: 5})
	if !errors.Is(err, ErrNoUsableSources) {
		t.Fatalf("expected ErrNoUsableSources, got %v", err)
	}
}

func TestPipeline_ConcurrencyLimits(t *testing.T) {
	cfg := config.Config{
		ResearchTimeout:        5 * time.Second,
		TopNForResearch:        10,
		SearchDefaultCount:     10,
		MaxConcurrentCrawls:    3,
		MaxConcurrentRefines:   2,
		ResearchBriefMaxTokens: 1000,
	}

	results := make([]search.Result, 10)
	pages := make(map[string]crawl.Page)
	outs := make(map[string]refine.Output)
	for i := 0; i < 10; i++ {
		url := fmt.Sprintf("u%d", i)
		results[i] = search.Result{URL: url}
		pages[url] = crawl.Page{URL: url, Markdown: "md"}
		outs[url] = refine.Output{Text: "txt", Refined: true}
	}

	s := &fakeSearcher{results: results}
	c := &fakeCrawler{pages: pages, delay: 50 * time.Millisecond}
	r := &fakeRefiner{out: outs, delay: 50 * time.Millisecond}

	p := New(cfg, s, c, r, nil)
	_, err := p.Research(context.Background(), Query{Text: "test", TopN: 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if atomic.LoadInt32(&c.maxActive) > int32(cfg.MaxConcurrentCrawls) {
		t.Errorf("expected max crawls %d, got %d", cfg.MaxConcurrentCrawls, c.maxActive)
	}
	if atomic.LoadInt32(&r.maxActive) > int32(cfg.MaxConcurrentRefines) {
		t.Errorf("expected max refines %d, got %d", cfg.MaxConcurrentRefines, r.maxActive)
	}
}

// fakeCrawlerWithOptions implements both Markdown and MarkdownWithOptions,
// recording the FetchOptions it was called with for assertion.
type fakeCrawlerWithOptions struct {
	page     crawl.Page
	err      error
	gotOpts  crawl.FetchOptions
	optsCall bool
}

func (f *fakeCrawlerWithOptions) Markdown(ctx context.Context, targetURL string) (crawl.Page, error) {
	return f.page, f.err
}

func (f *fakeCrawlerWithOptions) MarkdownWithOptions(ctx context.Context, targetURL string, opts crawl.FetchOptions) (crawl.Page, error) {
	f.optsCall = true
	f.gotOpts = opts
	return f.page, f.err
}

func TestPipeline_FetchRaw_FallbackToPlainMarkdown(t *testing.T) {
	cfg := config.Config{GlobalCrawlSlots: 6, GlobalRefineSlots: 3, CrawlTimeout: time.Second}
	c := &fakeCrawler{pages: map[string]crawl.Page{"u1": {URL: "u1", Markdown: "md"}}}
	p := New(cfg, &fakeSearcher{}, c, &fakeRefiner{}, nil)

	page, err := p.FetchRaw(context.Background(), "u1", crawl.FetchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if page.Markdown != "md" {
		t.Errorf("unexpected markdown: %q", page.Markdown)
	}
}

func TestPipeline_FetchRaw_PassesOptionsWhenSupported(t *testing.T) {
	cfg := config.Config{GlobalCrawlSlots: 6, GlobalRefineSlots: 3, CrawlTimeout: time.Second}
	c := &fakeCrawlerWithOptions{page: crawl.Page{URL: "u1", Markdown: "md", RawHTML: "<html></html>"}}
	p := New(cfg, &fakeSearcher{}, c, &fakeRefiner{}, nil)

	opts := crawl.FetchOptions{WaitForSelector: "h1", PageTimeout: 5 * time.Second}
	page, err := p.FetchRaw(context.Background(), "u1", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !c.optsCall {
		t.Fatal("expected MarkdownWithOptions to be called, not the fallback Markdown")
	}
	if c.gotOpts != opts {
		t.Errorf("expected opts %+v to be passed through, got %+v", opts, c.gotOpts)
	}
	if page.RawHTML != "<html></html>" {
		t.Errorf("unexpected cleaned html: %q", page.RawHTML)
	}
}
