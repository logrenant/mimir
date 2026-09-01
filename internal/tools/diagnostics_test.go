package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/crawl"
	"github.com/logrenant/goat-mcp/internal/refine"
	"github.com/logrenant/goat-mcp/internal/search"
)

// writeFakeClaude writes an executable script standing in for the `claude`
// CLI's `--version` health check and returns its path.
func writeFakeClaude(t *testing.T, exitCode int, sleep time.Duration) string {
	t.Helper()
	script := "#!/bin/sh\n"
	if sleep > 0 {
		script += fmt.Sprintf("sleep %g\n", sleep.Seconds())
	}
	script += fmt.Sprintf("exit %d\n", exitCode)

	path := filepath.Join(t.TempDir(), "fake-claude.sh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDiagnostics_HappyPath(t *testing.T) {
	// Mock Crawl4AI
	mockCrawl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mockCrawl.Close()

	// Mock DuckDuckGo
	mockDDG := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mockDDG.Close()

	cfg := config.Config{
		Crawl4AIBaseURL:   mockCrawl.URL,
		ClaudeCLIPath:     writeFakeClaude(t, 0, 0),
		ClaudeModel:       "claude-haiku-4-5-20251001",
		DuckDuckGoLiteURL: mockDDG.URL,
		CrawlTimeout:      1 * time.Second,
		RefineTimeout:     5 * time.Second,
		SearchTimeout:     1 * time.Second,
	}

	tool := NewDiagnostics(cfg, crawl.New(cfg), refine.New(cfg), search.New(cfg), nil)
	resAny, err := tool.Handle(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	res := resAny.(diagnosticsResponse)
	if !res.Crawl4AI.Ok {
		t.Errorf("expected Crawl4AI to be Ok")
	}
	if !res.Claude.Ok {
		t.Errorf("expected Claude to be Ok")
	}
	if !res.DuckDuckGo.Ok {
		t.Errorf("expected DuckDuckGo to be Ok")
	}
}

func TestDiagnostics_ClaudeUnavailable(t *testing.T) {
	mockCrawl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mockCrawl.Close()

	mockDDG := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mockDDG.Close()

	cfg := config.Config{
		Crawl4AIBaseURL:   mockCrawl.URL,
		ClaudeCLIPath:     writeFakeClaude(t, 1, 0), // exits non-zero: not authenticated
		ClaudeModel:       "claude-haiku-4-5-20251001",
		DuckDuckGoLiteURL: mockDDG.URL,
		CrawlTimeout:      1 * time.Second,
		RefineTimeout:     5 * time.Second,
		SearchTimeout:     1 * time.Second,
	}

	tool := NewDiagnostics(cfg, crawl.New(cfg), refine.New(cfg), search.New(cfg), nil)
	resAny, err := tool.Handle(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	res := resAny.(diagnosticsResponse)
	if res.Claude.Ok {
		t.Errorf("expected Claude to be unhealthy")
	}
	if res.Claude.Detail == "" {
		t.Errorf("expected actionable detail for unavailable claude CLI")
	}
}

func TestDiagnostics_Timeout(t *testing.T) {
	// A blocking HTTP server for Crawl4AI/DDG, and a slow fake CLI for Claude.
	mockBlock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Second)
	}))
	defer mockBlock.Close()

	cfg := config.Config{
		Crawl4AIBaseURL:   mockBlock.URL,
		ClaudeCLIPath:     writeFakeClaude(t, 0, 10*time.Second),
		ClaudeModel:       "claude-haiku-4-5-20251001",
		DuckDuckGoLiteURL: mockBlock.URL,
		RefineTimeout:     10 * time.Second,
	}

	tool := NewDiagnostics(cfg, crawl.New(cfg), refine.New(cfg), search.New(cfg), nil)

	start := time.Now()
	resAny, err := tool.Handle(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	elapsed := time.Since(start)

	// Context should cancel inside Handle within 5 seconds.
	if elapsed > 6*time.Second {
		t.Errorf("expected diagnostics to bound itself to ~5s, took %v", elapsed)
	}

	res := resAny.(diagnosticsResponse)
	if res.Crawl4AI.Ok || res.Claude.Ok || res.DuckDuckGo.Ok {
		t.Errorf("expected all dependencies to fail, got: %+v", res)
	}
}

func TestDiagnostics_Marshalling(t *testing.T) {
	// A mock to pass through finalizeResponse tests
	cfg := config.Config{ClaudeCLIPath: writeFakeClaude(t, 1, 0)}
	tool := NewDiagnostics(cfg, crawl.New(cfg), refine.New(cfg), search.New(cfg), nil)

	// Fast failure output check
	resAny, err := tool.Handle(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	b, err := json.Marshal(resAny)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	if len(b) == 0 {
		t.Errorf("empty json")
	}
}
