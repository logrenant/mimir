//go:build integration

package search_test

import (
	"context"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/search"
)

func TestIntegration_DuckDuckGo(t *testing.T) {
	cfg := config.Load()
	// Force it to use real URLs

	client := search.New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	results, err := client.Search(ctx, "golang", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected results from DuckDuckGo, got 0")
	}
}
