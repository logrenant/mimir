package leadgen

import (
	"context"
	"errors"
	"testing"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/maps"
)

// blockingSource holds a run inside the pipeline so a second one meets a
// pipeline that is genuinely busy.
type blockingSource struct {
	entered chan struct{}
	release chan struct{}
}

func (b *blockingSource) Available() bool { return true }

func (b *blockingSource) Search(ctx context.Context, _ maps.Query) ([]maps.Company, string, []string, error) {
	b.entered <- struct{}{}
	select {
	case <-b.release:
	case <-ctx.Done():
		return nil, "", nil, ctx.Err()
	}
	return []maps.Company{{PlaceID: "p1", Name: "A"}}, "test", nil, nil
}

// TestPipeline_SharesOnePermitPoolBetweenEveryEntryPoint.
//
// The limit lives on the pipeline rather than on a caller precisely because
// there are two callers — the HTTP route and a board card — and a limit held by
// one would leave the other unbounded against somebody else's servers.
func TestPipeline_SharesOnePermitPoolBetweenEveryEntryPoint(t *testing.T) {
	cfg := config.Load()
	cfg.MaxConcurrentLeadgenRuns = 1

	src := &blockingSource{entered: make(chan struct{}, 2), release: make(chan struct{})}
	p := NewPipeline(cfg, src, nil, nil, nil, nil)

	done := make(chan error, 1)
	go func() {
		_, err := p.Run(context.Background(), RunRequest{Query: maps.Query{Text: "a"}, Region: "a"})
		done <- err
	}()
	<-src.entered // the first run holds the only permit

	// The second caller — a different door, the same pool.
	if _, err := p.Run(context.Background(), RunRequest{Query: maps.Query{Text: "b"}, Region: "b"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}

	close(src.release)
	if err := <-done; err != nil {
		t.Fatalf("the first run failed: %v", err)
	}

	// The permit came back.
	if _, err := p.Run(context.Background(), RunRequest{Query: maps.Query{Text: "c"}, Region: "c"}); errors.Is(err, ErrBusy) {
		t.Fatal("the permit was never released")
	}
}

// TestPipeline_DoesNotBlockWaitingForAPermit: a caller that waited here would
// hold an HTTP request open behind work the operator cannot see.
func TestPipeline_DoesNotBlockWaitingForAPermit(t *testing.T) {
	cfg := config.Load()
	cfg.MaxConcurrentLeadgenRuns = 1

	src := &blockingSource{entered: make(chan struct{}, 2), release: make(chan struct{})}
	p := NewPipeline(cfg, src, nil, nil, nil, nil)

	go func() {
		_, _ = p.Run(context.Background(), RunRequest{Query: maps.Query{Text: "a"}, Region: "a"})
	}()
	<-src.entered

	answered := make(chan struct{})
	go func() {
		_, _ = p.Run(context.Background(), RunRequest{Query: maps.Query{Text: "b"}, Region: "b"})
		close(answered)
	}()

	select {
	case <-answered:
	case <-context.Background().Done():
	}
	<-answered // must already be closed; a blocking acquire would hang here

	close(src.release)
}
