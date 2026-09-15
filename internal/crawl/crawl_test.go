package crawl_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/crawl"
)

func TestClient_Health(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.Config{Crawl4AIBaseURL: srv.URL, CrawlTimeout: 1 * time.Second}
	client := crawl.New(cfg)
	if err := client.Health(context.Background()); err != nil {
		t.Fatalf("expected health ok, got: %v", err)
	}
}

func TestClient_Health_Down(t *testing.T) {
	cfg := config.Config{Crawl4AIBaseURL: "http://127.0.0.1:0", CrawlTimeout: 1 * time.Second}
	client := crawl.New(cfg)
	err := client.Health(context.Background())
	if !errors.Is(err, crawl.ErrDockerUnavailable) {
		t.Fatalf("expected ErrDockerUnavailable, got: %v", err)
	}
}

// realShapeBody mirrors Crawl4AI 0.8.9's actual /crawl response, verified
// against a live container: "urls" (plural, array) request; "results" array
// response with a nested "markdown.raw_markdown" and "metadata.title".
const realShapeBody = `{
	"success": true,
	"results": [{
		"url": "https://example.com",
		"success": true,
		"cleaned_html": "<html><body><h1>Hi</h1></body></html>",
		"html": "<html><head><meta property=\"og:title\" content=\"Raw\"></head><body><h1>Hi</h1></body></html>",
		"metadata": {"title": "Test"},
		"markdown": {"raw_markdown": "hello world"}
	}]
}`

func TestClient_Markdown_HappyPath(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(realShapeBody))
	}))
	defer srv.Close()

	cfg := config.Config{Crawl4AIBaseURL: srv.URL, CrawlTimeout: 1 * time.Second}
	client := crawl.New(cfg)

	page, err := client.Markdown(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if page.Markdown != "hello world" {
		t.Errorf("unexpected markdown: %q", page.Markdown)
	}
	if page.Title != "Test" {
		t.Errorf("unexpected title: %q", page.Title)
	}
	// RawHTML must come from "html" (which carries <meta>/attributes), not
	// "cleaned_html" (verified empirically to strip them) — see crawl.go.
	if page.RawHTML != `<html><head><meta property="og:title" content="Raw"></head><body><h1>Hi</h1></body></html>` {
		t.Errorf("unexpected raw html: %q", page.RawHTML)
	}
	if page.URL != "https://example.com" {
		t.Errorf("unexpected url: %q", page.URL)
	}
	// The real endpoint requires "urls" (plural array) — {"url": "..."} is
	// rejected with a 422 by the pinned image. Guard against regressing to
	// the old, broken singular shape.
	if !jsonContains(gotBody, `"urls":["https://example.com"]`) {
		t.Errorf("request body did not use the plural \"urls\" array shape: %s", gotBody)
	}
}

func jsonContains(body, substr string) bool {
	// cheap substring check tolerant of key ordering elsewhere in the body
	for i := 0; i+len(substr) <= len(body); i++ {
		if body[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestClient_Markdown_LegacyFlatShape(t *testing.T) {
	// Tolerate an alternate/legacy flat shape as a fallback.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"markdown": "flat shape", "title": "Flat"}`))
	}))
	defer srv.Close()

	cfg := config.Config{Crawl4AIBaseURL: srv.URL, CrawlTimeout: 1 * time.Second}
	client := crawl.New(cfg)

	page, err := client.Markdown(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if page.Markdown != "flat shape" {
		t.Errorf("unexpected markdown: %q", page.Markdown)
	}
	if page.Title != "Flat" {
		t.Errorf("unexpected title: %q", page.Title)
	}
}

func TestClient_Markdown_EmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success": true, "results": [{"url": "https://example.com", "success": true, "metadata": {"title": "Test"}, "markdown": {"raw_markdown": ""}}]}`))
	}))
	defer srv.Close()

	cfg := config.Config{Crawl4AIBaseURL: srv.URL, CrawlTimeout: 1 * time.Second}
	client := crawl.New(cfg)

	_, err := client.Markdown(context.Background(), "https://example.com")
	if !errors.Is(err, crawl.ErrEmptyResult) {
		t.Fatalf("expected ErrEmptyResult, got: %v", err)
	}
}

func TestClient_Markdown_PerURLFailure(t *testing.T) {
	// Crawl4AI can return HTTP 200 with a per-result success:false + error_message
	// (e.g. the target blocked/refused the fetch) — this must surface as a
	// clear ErrDockerUnavailable-wrapped error, not a silent empty result.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success": true, "results": [{"url": "https://example.com", "success": false, "error_message": "blocked by target"}]}`))
	}))
	defer srv.Close()

	cfg := config.Config{Crawl4AIBaseURL: srv.URL, CrawlTimeout: 1 * time.Second}
	client := crawl.New(cfg)

	_, err := client.Markdown(context.Background(), "https://example.com")
	if !errors.Is(err, crawl.ErrDockerUnavailable) {
		t.Fatalf("expected ErrDockerUnavailable, got: %v", err)
	}
}

func TestClient_MarkdownWithOptions_SendsCrawlerConfig(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(realShapeBody))
	}))
	defer srv.Close()

	cfg := config.Config{Crawl4AIBaseURL: srv.URL, CrawlTimeout: 1 * time.Second}
	client := crawl.New(cfg)

	_, err := client.MarkdownWithOptions(context.Background(), "https://example.com", crawl.FetchOptions{
		WaitForSelector: "h1",
		PageTimeout:     5 * time.Second,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !jsonContains(gotBody, `"wait_for":"h1"`) {
		t.Errorf("request body missing wait_for: %s", gotBody)
	}
	if !jsonContains(gotBody, `"page_timeout":5000`) {
		t.Errorf("request body missing page_timeout in ms: %s", gotBody)
	}
}

func TestClient_Markdown_Retry(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(realShapeBody))
	}))
	defer srv.Close()

	cfg := config.Config{Crawl4AIBaseURL: srv.URL, CrawlTimeout: 1 * time.Second}
	client := crawl.New(cfg)

	page, err := client.Markdown(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if page.Markdown != "hello world" {
		t.Errorf("unexpected markdown: %q", page.Markdown)
	}
	if attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts)
	}
}

func TestClient_Markdown_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond) // Block to simulate slow response
	}))
	defer srv.Close()

	cfg := config.Config{Crawl4AIBaseURL: srv.URL, CrawlTimeout: 2 * time.Second}
	client := crawl.New(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately
	cancel()

	_, err := client.Markdown(ctx, "https://example.com")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled error, got: %v", err)
	}
}

// The script a caller sends is how the catalog's site scan reads a storefront's
// *computed* styles: the browser resolves the cascade, which parsing the
// stylesheets by hand cannot do. Verified against the pinned image — the probe
// runs and its result comes back on the document element.
func TestClient_MarkdownWithOptions_SendsJSCode(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(realShapeBody))
	}))
	defer srv.Close()

	client := crawl.New(config.Config{Crawl4AIBaseURL: srv.URL, CrawlTimeout: time.Second})
	_, err := client.MarkdownWithOptions(context.Background(), "https://example.com",
		crawl.FetchOptions{JSCode: "document.title='x';"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !jsonContains(gotBody, `"js_code":"document.title='x';"`) {
		t.Errorf("request body missing js_code: %s", gotBody)
	}
}

// The zero value still sends no crawler_config at all, so every existing call
// keeps the request shape it was verified against.
func TestClient_MarkdownWithOptions_NoJSCodeMeansNoCrawlerConfig(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(realShapeBody))
	}))
	defer srv.Close()

	client := crawl.New(config.Config{Crawl4AIBaseURL: srv.URL, CrawlTimeout: time.Second})
	if _, err := client.Markdown(context.Background(), "https://example.com"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if jsonContains(gotBody, `"crawler_config"`) {
		t.Errorf("plain call sent a crawler_config: %s", gotBody)
	}
}
