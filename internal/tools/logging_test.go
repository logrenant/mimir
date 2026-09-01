package tools_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/crawl"
	goatmcp "github.com/logrenant/goat-mcp/internal/mcp"
	"github.com/logrenant/goat-mcp/internal/pipeline"
	"github.com/logrenant/goat-mcp/internal/refine"
	"github.com/logrenant/goat-mcp/internal/search"
	"github.com/logrenant/goat-mcp/internal/tools"
)

type mockSearcher struct{}

func (m mockSearcher) Search(ctx context.Context, query string, max int) ([]search.Result, error) {
	return []search.Result{{Title: "T", URL: "U", Snippet: "S"}}, nil
}

type mockCrawler struct{}

func (m mockCrawler) Markdown(ctx context.Context, url string) (crawl.Page, error) {
	return crawl.Page{Title: "T", URL: url, Markdown: "M"}, nil
}

type mockRefiner struct{}

func (m mockRefiner) Distil(ctx context.Context, in refine.Input) (refine.Output, error) {
	return refine.Output{Text: "R", Refined: true}, nil
}

func TestLogging_StdoutEmpty_StderrContainsFields(t *testing.T) {
	// Mock server that returns happy paths for everything
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "chat") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"message": {"content": "refined content"}}`))
			return
		}
		if strings.Contains(r.URL.Path, "crawl") || strings.Contains(r.URL.Path, "task") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"markdown": "test markdown", "title": "test"}`))
			return
		}
		
		// Fallback for DuckDuckGo
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><div class="result__body"><h2 class="result__title"><a class="result__url" href="https://example.com">Example</a></h2><a class="result__snippet">snippet</a></div></body></html>`))
	}))
	ts.Config.SetKeepAlivesEnabled(false)
	ts.Start()
	defer ts.Close()

	// Fake `claude` CLI for the diagnostics tool's refine health check
	// (the research tool below uses mockRefiner, not the real client).
	fakeClaudePath := filepath.Join(t.TempDir(), "fake-claude.sh")
	if err := os.WriteFile(fakeClaudePath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		DuckDuckGoLiteURL: ts.URL,
		Crawl4AIBaseURL:   ts.URL,
		ClaudeCLIPath:     fakeClaudePath,
		ClaudeModel:       "claude-haiku-4-5-20251001",
		CrawlTimeout:      5 * time.Second,
		RefineTimeout:     5 * time.Second,
		SearchTimeout:     5 * time.Second,
		ResearchTimeout:   5 * time.Second,
		SearchDefaultCount: 1,
		GlobalCrawlSlots:  6,
		GlobalRefineSlots: 3,
		MaxConcurrentCrawls: 2,
		MaxConcurrentRefines: 2,
		TopNForResearch: 3,
		ResearchBriefMaxTokens: 1000,
		DuckDuckGoHTMLURL: ts.URL,
	}

	searchClient := search.New(cfg)
	crawlClient := crawl.New(cfg)
	refineClient := refine.New(cfg)
	pipe := pipeline.New(cfg, mockSearcher{}, mockCrawler{}, mockRefiner{}, nil)

	diagTool := tools.NewDiagnostics(cfg, crawlClient, refineClient, searchClient, nil)
	researchTool := tools.NewResearch(pipe, cfg)

	var buf bytes.Buffer
	goatmcp.InitLogging(&buf)

	// SD-4: capture the real os.Stdout for the duration of the tool calls and
	// assert not a single byte lands there — protocol frames are the only thing
	// allowed on stdout, and neither the tools nor the pipeline emit any.
	origStdout := os.Stdout
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = pw
	stdoutCh := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(pr)
		stdoutCh <- b
	}()

	// Run diagnostics and research tools to generate logs
	ctx1 := goatmcp.WithLogger(context.Background(), 123, "diagnostics")
	goatmcp.LogToolStart(ctx1)
	start1 := time.Now()
	_, _ = diagTool.Handle(ctx1, json.RawMessage(`{}`))
	goatmcp.LogToolEnd(ctx1, time.Since(start1).Milliseconds(), nil)

	ctx2 := goatmcp.WithLogger(context.Background(), 456, "research")
	goatmcp.LogToolStart(ctx2)
	start2 := time.Now()
	_, err = researchTool.Handle(ctx2, json.RawMessage(`{"query":"test"}`))
	goatmcp.LogToolEnd(ctx2, time.Since(start2).Milliseconds(), err)

	_ = pw.Close()
	os.Stdout = origStdout
	stdoutBytes := <-stdoutCh

	errStr := buf.String()

	t.Log("STDERR OUTPUT:\n" + errStr)

	if len(stdoutBytes) != 0 {
		t.Errorf("expected zero bytes on stdout during tool calls, got %d: %q", len(stdoutBytes), string(stdoutBytes))
	}

	if err != nil {
		t.Errorf("research tool failed unexpectedly: %v", err)
	}

	// Verify stderr has all the necessary slog fields
	if !strings.Contains(errStr, `"level":"INFO"`) {
		t.Errorf("stderr missing level field")
	}
	if !strings.Contains(errStr, `"msg":"tool.start"`) {
		t.Errorf("stderr missing tool.start msg")
	}
	if !strings.Contains(errStr, `"msg":"tool.end"`) {
		t.Errorf("stderr missing tool.end msg")
	}
	if !strings.Contains(errStr, `"msg":"pipeline.stage"`) {
		t.Errorf("stderr missing pipeline.stage msg")
	}
	if !strings.Contains(errStr, `"request_id":123`) {
		t.Errorf("stderr missing request_id")
	}
	if !strings.Contains(errStr, `"tool":"diagnostics"`) {
		t.Errorf("stderr missing tool")
	}
	if !strings.Contains(errStr, `"duration_ms":`) {
		t.Errorf("stderr missing duration_ms")
	}
	if !strings.Contains(errStr, `"stage":"search"`) {
		t.Errorf("stderr missing stage search")
	}
	if !strings.Contains(errStr, `"stage":"crawl"`) {
		t.Errorf("stderr missing stage crawl")
	}
	if !strings.Contains(errStr, `"stage":"refine"`) {
		t.Errorf("stderr missing stage refine")
	}
	if !strings.Contains(errStr, `"stage":"merge"`) {
		t.Errorf("stderr missing stage merge")
	}
}
