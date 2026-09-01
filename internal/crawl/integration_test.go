//go:build integration

package crawl_test

import (
	"context"
	"testing"
	"time"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/crawl"
)

// TestIntegration_Crawl4AI verifies the real docker container responds correctly.
// Run this with: make crawl-up && go test -v -tags=integration ./internal/crawl
func TestIntegration_Crawl4AI(t *testing.T) {
	cfg := config.Load()
	client := crawl.New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := client.Health(ctx); err != nil {
		t.Fatalf("Health check failed, is Crawl4AI running? %v", err)
	}

	page, err := client.Markdown(ctx, "https://example.com")
	if err != nil {
		t.Fatalf("Markdown extraction failed: %v", err)
	}

	if page.Markdown == "" {
		t.Fatal("Expected non-empty markdown result from example.com")
	}

	t.Logf("Successfully fetched %s (len: %d)", page.Title, len(page.Markdown))
}
