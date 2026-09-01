package crawl

import (
	"context"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/config"
)

func TestClient_waitPolite(t *testing.T) {
	cfg := config.Config{
		PerHostMinInterval: 50 * time.Millisecond,
	}
	c := New(cfg)
	ctx := context.Background()

	// First hit should be immediate
	start := time.Now()
	err := c.waitPolite(ctx, "http://example.com/page1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	elapsed1 := time.Since(start)
	if elapsed1 > 20*time.Millisecond {
		t.Errorf("expected first wait to be immediate, took %v", elapsed1)
	}

	// Second hit to the SAME host should delay by ~50ms
	start2 := time.Now()
	err = c.waitPolite(ctx, "http://example.com/page2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	elapsed2 := time.Since(start2)
	if elapsed2 < 40*time.Millisecond { // give some buffer
		t.Errorf("expected second wait to delay for politeness, took %v", elapsed2)
	}

	// Hit to a DIFFERENT host should be immediate
	start3 := time.Now()
	err = c.waitPolite(ctx, "http://another.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	elapsed3 := time.Since(start3)
	if elapsed3 > 20*time.Millisecond {
		t.Errorf("expected third wait (different host) to be immediate, took %v", elapsed3)
	}
}

func TestClient_waitPolite_ContextCancel(t *testing.T) {
	cfg := config.Config{
		PerHostMinInterval: 500 * time.Millisecond,
	}
	c := New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// First hit is immediate, sets the timer for the next hit
	_ = c.waitPolite(context.Background(), "http://slow.com")

	// Second hit should delay for 500ms, but our context times out in 50ms
	start := time.Now()
	err := c.waitPolite(ctx, "http://slow.com")
	if err == nil {
		t.Fatalf("expected error from context timeout")
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Errorf("expected context to cancel wait early")
	}
}
