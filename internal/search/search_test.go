package search_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/search"
)

func TestSearch_FallbackAndError(t *testing.T) {
	// 503 for HTML, success for lite
	htmlCalled := 0
	htmlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		htmlCalled++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer htmlSrv.Close()

	liteCalled := 0
	liteSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		liteCalled++
		_, _ = w.Write([]byte(`<html><body><tr><td></td><td><a class="result-link" href="https://example.com">Example</a></td></tr><tr><td></td><td class="result-snippet">snippet</td></tr></body></html>`))
	}))
	defer liteSrv.Close()

	cfg := config.Config{
		DuckDuckGoHTMLURL:  htmlSrv.URL,
		DuckDuckGoLiteURL:  liteSrv.URL,
		SearchMaxCount:     10,
		SearchDefaultCount: 5,
		SearchTimeout:      1 * time.Second,
	}

	client := search.New(cfg)
	res, err := client.Search(context.Background(), "test", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 result from lite fallback, got %d", len(res))
	}
	if htmlCalled != 1 || liteCalled != 1 {
		t.Errorf("expected 1 call to html and 1 to lite, got %d %d", htmlCalled, liteCalled)
	}
}

func TestSearch_BothFail(t *testing.T) {
	htmlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer htmlSrv.Close()

	liteSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer liteSrv.Close()

	cfg := config.Config{
		DuckDuckGoHTMLURL: htmlSrv.URL,
		DuckDuckGoLiteURL: liteSrv.URL,
		SearchTimeout:     1 * time.Second,
	}

	client := search.New(cfg)
	_, err := client.Search(context.Background(), "test", 10)
	if err != search.ErrSearchUnavailable {
		t.Fatalf("expected ErrSearchUnavailable, got %v", err)
	}
}

func TestSearch_DedupeAndClamp(t *testing.T) {
	htmlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body>
			<div class="web-result">
				<div class="result__body">
					<h2 class="result__title"><a href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com">Example 1</a></h2>
					<a class="result__snippet" href="#">s1</a>
				</div>
			</div>
			<div class="web-result">
				<div class="result__body">
					<h2 class="result__title"><a href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com">Example 2</a></h2>
					<a class="result__snippet" href="#">s2</a>
				</div>
			</div>
			<div class="web-result">
				<div class="result__body">
					<h2 class="result__title"><a href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample2.com">Example 3</a></h2>
					<a class="result__snippet" href="#">s3</a>
				</div>
			</div>
		</body></html>`))
	}))
	defer htmlSrv.Close()

	cfg := config.Config{
		DuckDuckGoHTMLURL:  htmlSrv.URL,
		SearchMaxCount:     10,
		SearchDefaultCount: 5,
		SearchTimeout:      1 * time.Second,
	}

	client := search.New(cfg)
	res, err := client.Search(context.Background(), "test", 1) // limit to 1
	if err != nil {
		t.Fatal(err)
	}

	// Should be deduped (so 2 total) and then clamped to 1
	if len(res) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res))
	}
	if res[0].Title != "Example 1" {
		t.Errorf("unexpected title: %q", res[0].Title)
	}
}

// The live endpoint answers HEAD with 400 no matter the User-Agent, which is
// why the probe is a GET that looks like the search path's own requests.
func TestHealth_ProbesWithBrowserGET(t *testing.T) {
	var gotMethod, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotUA = r.Header.Get("User-Agent")
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte("<html><body>lite</body></html>"))
	}))
	defer srv.Close()

	cfg := config.Config{DuckDuckGoLiteURL: srv.URL, SearchTimeout: 2 * time.Second}

	if err := search.New(cfg).Health(context.Background()); err != nil {
		t.Fatalf("Health: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("probe method = %q, want GET", gotMethod)
	}
	if !strings.HasPrefix(gotUA, "Mozilla/") {
		t.Errorf("probe User-Agent = %q, want the browser agent the search path sends", gotUA)
	}
}

func TestHealth_NonSuccessStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	cfg := config.Config{DuckDuckGoLiteURL: srv.URL, SearchTimeout: 2 * time.Second}

	err := search.New(cfg).Health(context.Background())
	if !errors.Is(err, search.ErrSearchUnavailable) {
		t.Fatalf("Health error = %v, want ErrSearchUnavailable", err)
	}
}

func TestSearch_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond) // block
	}))
	defer srv.Close()

	cfg := config.Config{
		DuckDuckGoHTMLURL: srv.URL,
		SearchMaxCount:    10,
		SearchTimeout:     2 * time.Second,
	}
	client := search.New(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := client.Search(ctx, "test", 10)
	if err == nil || err == search.ErrSearchUnavailable {
		t.Fatalf("expected context cancellation error, got %v", err)
	}
}
