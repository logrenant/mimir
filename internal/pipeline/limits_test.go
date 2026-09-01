package pipeline

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"
	"golang.org/x/sync/errgroup"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/crawl"
	"github.com/logrenant/goat-mcp/internal/refine"
	"github.com/logrenant/goat-mcp/internal/search"
)

type limitsMockSearcher struct{}
func (s *limitsMockSearcher) Search(ctx context.Context, query string, maxResults int) ([]search.Result, error) {
	return []search.Result{
		{URL: "http://example.com/1", Title: "t1"},
		{URL: "http://example.com/2", Title: "t2"},
		{URL: "http://example.com/3", Title: "t3"},
	}, nil
}

type limitsMockCrawler struct {
	activeCrawls atomic.Int32
	maxCrawls    atomic.Int32
}
func (c *limitsMockCrawler) Markdown(ctx context.Context, targetURL string) (crawl.Page, error) {
	current := c.activeCrawls.Add(1)
	defer c.activeCrawls.Add(-1)
	
	for {
		max := c.maxCrawls.Load()
		if current > max {
			if c.maxCrawls.CompareAndSwap(max, current) {
				break
			}
		} else {
			break
		}
	}
	
	// Simulate work
	select {
	case <-ctx.Done():
		return crawl.Page{}, ctx.Err()
	case <-time.After(10 * time.Millisecond):
		return crawl.Page{URL: targetURL, Markdown: "content", Title: "title"}, nil
	}
}

type limitsMockRefiner struct {
	activeRefines atomic.Int32
	maxRefines    atomic.Int32
}
func (r *limitsMockRefiner) Distil(ctx context.Context, in refine.Input) (refine.Output, error) {
	current := r.activeRefines.Add(1)
	defer r.activeRefines.Add(-1)
	
	for {
		max := r.maxRefines.Load()
		if current > max {
			if r.maxRefines.CompareAndSwap(max, current) {
				break
			}
		} else {
			break
		}
	}

	select {
	case <-ctx.Done():
		return refine.Output{}, ctx.Err()
	case <-time.After(10 * time.Millisecond):
		return refine.Output{Text: "refined", Refined: true}, nil
	}
}

func TestPipeline_GlobalConcurrency_Limits(t *testing.T) {
	defer goleak.VerifyNone(t)

	cfg := config.Config{
		SearchTimeout:        5 * time.Second,
		CrawlTimeout:         5 * time.Second,
		RefineTimeout:        5 * time.Second,
		ResearchTimeout:      5 * time.Second,
		MaxConcurrentCrawls:  4,
		MaxConcurrentRefines: 2,
		GlobalCrawlSlots:     6,
		GlobalRefineSlots:    3,
		SearchDefaultCount:   3,
		SearchMaxCount:       10,
		TopNForResearch:      3,
		FetchPageMaxTokens:   1500,
		ResearchBriefMaxTokens: 2000,
	}

	searcher := &limitsMockSearcher{}
	crawler := &limitsMockCrawler{}
	refiner := &limitsMockRefiner{}

	p := New(cfg, searcher, crawler, refiner, nil)

	var eg errgroup.Group
	ctx := context.Background()

	// Fire 20 concurrent Research requests
	for i := 0; i < 20; i++ {
		eg.Go(func() error {
			_, err := p.Research(ctx, Query{Text: "test query", TopN: 3})
			return err
		})
	}

	if err := eg.Wait(); err != nil {
		t.Fatalf("unexpected error during concurrent requests: %v", err)
	}

	if max := crawler.maxCrawls.Load(); max > int32(cfg.GlobalCrawlSlots) {
		t.Errorf("expected max crawls <= %d, got %d", cfg.GlobalCrawlSlots, max)
	}
	
	if max := refiner.maxRefines.Load(); max > int32(cfg.GlobalRefineSlots) {
		t.Errorf("expected max refines <= %d, got %d", cfg.GlobalRefineSlots, max)
	}
}

func TestPipeline_SlotReleaseOnContextCancel(t *testing.T) {
	cfg := config.Config{
		SearchTimeout:        5 * time.Second,
		CrawlTimeout:         5 * time.Second,
		RefineTimeout:        5 * time.Second,
		ResearchTimeout:      5 * time.Second,
		MaxConcurrentCrawls:  4,
		MaxConcurrentRefines: 2,
		GlobalCrawlSlots:     1, // strict limit of 1
		GlobalRefineSlots:    1,
	}
	
	// Ensure initialized
	InitLimits(cfg)
	
	ctx, cancel := context.WithCancel(context.Background())
	
	rel1, err := acquireCrawlSlot(ctx)
	if err != nil {
		t.Fatalf("unexpected error acquiring slot: %v", err)
	}
	
	// Cancel the context, which should abort the second wait
	cancel()
	
	// This should return a context.Canceled error immediately
	_, err2 := acquireCrawlSlot(ctx)
	if err2 == nil {
		t.Fatalf("expected context cancellation error, got nil")
	}
	
	// Release the first slot
	rel1()
	
	// If context was fresh, we could acquire again (verifying release works)
	rel2, err3 := acquireCrawlSlot(context.Background())
	if err3 != nil {
		t.Fatalf("failed to acquire slot after release: %v", err3)
	}
	rel2()
}
