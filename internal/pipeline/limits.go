package pipeline

import (
	"context"
	"sync"

	"golang.org/x/sync/semaphore"

	"github.com/logrenant/goat-mcp/internal/config"
)

var (
	initLimits   sync.Once
	crawlSem     *semaphore.Weighted
	refineSem    *semaphore.Weighted
)

// InitLimits initializes the global semaphores used to bound concurrency
// for crawls and refines process-wide.
func InitLimits(cfg config.Config) {
	initLimits.Do(func() {
		crawlSlots := cfg.GlobalCrawlSlots
		if crawlSlots <= 0 {
			crawlSlots = 6 // Sensible fallback for tests missing this
		}
		refineSlots := cfg.GlobalRefineSlots
		if refineSlots <= 0 {
			refineSlots = 3
		}
		
		crawlSem = semaphore.NewWeighted(int64(crawlSlots))
		refineSem = semaphore.NewWeighted(int64(refineSlots))
	})
}

// acquireCrawlSlot blocks until a global crawl slot is available or the context is canceled.
func acquireCrawlSlot(ctx context.Context) (func(), error) {
	if crawlSem == nil {
		panic("pipeline.InitLimits must be called before acquireCrawlSlot")
	}
	if err := crawlSem.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	return func() { crawlSem.Release(1) }, nil
}

// acquireRefineSlot blocks until a global refine slot is available or the context is canceled.
func acquireRefineSlot(ctx context.Context) (func(), error) {
	if refineSem == nil {
		panic("pipeline.InitLimits must be called before acquireRefineSlot")
	}
	if err := refineSem.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	return func() { refineSem.Release(1) }, nil
}
