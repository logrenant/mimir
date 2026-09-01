//go:build integration

package refine_test

import (
	"context"
	"testing"
	"time"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/refine"
)

// TestIntegration_Claude runs a real query against the local `claude` CLI.
func TestIntegration_Claude(t *testing.T) {
	cfg := config.Load()
	client := refine.New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ok, err := client.Health(ctx)
	if err != nil {
		t.Fatalf("health check failed: %v", err)
	}
	if !ok {
		t.Fatalf("claude CLI (%s) not usable — run `claude login`", cfg.ClaudeCLIPath)
	}

	in := refine.Input{
		Query:        "What is goat-mcp?",
		PageMarkdown: "# Goat-MCP\nGoat-MCP is a Model Context Protocol server that helps AI agents.",
		SourceURL:    "https://example.com/goat-mcp",
		MaxTokens:    100,
	}

	out, err := client.Distil(ctx, in)
	if err != nil {
		t.Fatalf("Distil failed: %v", err)
	}

	if !out.Refined {
		t.Fatalf("expected Refined to be true")
	}
	if out.Text == "" {
		t.Fatalf("expected non-empty output")
	}

	t.Logf("Refined output (len: %d):\n%s", len(out.Text), out.Text)
}
