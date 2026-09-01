package mapscrape

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/maps"
)

func testCfg(baseURL string) config.Config {
	return config.Config{
		MapScrapeBaseURL:      baseURL,
		MapScrapeTimeout:      5 * time.Second,
		MapScrapeWaitSelector: `div[role="feed"]`,
		MapScrapeMaxResults:   60,
		MapScrapeMaxScrolls:   12,
	}
}

func fixtureHTML(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "feed.html"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// stubSidecar stands in for deploy/playwright-maps. It records the last request
// so the tests can assert what the client asked for, which is the only part of
// the sidecar contract Go owns.
type stubSidecar struct {
	*httptest.Server
	lastRequest searchRequest
}

func newStubSidecar(t *testing.T, status int, respond func() any) *stubSidecar {
	t.Helper()
	stub := &stubSidecar{}
	stub.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&stub.lastRequest)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(respond())
	}))
	t.Cleanup(stub.Close)
	return stub
}

func TestSearch_HappyPath(t *testing.T) {
	stub := newStubSidecar(t, http.StatusOK, func() any {
		return searchResponse{HTML: fixtureHTML(t), ResultCount: 6, Scrolls: 2}
	})
	c := New(testCfg(stub.URL))

	companies, err := c.Search(context.Background(), maps.Query{
		Text:         "dentists in Kadıköy",
		LanguageCode: "tr",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(companies) != 4 {
		t.Fatalf("got %d companies, want 4", len(companies))
	}
	for _, company := range companies {
		if company.Source != maps.SourceScrape {
			t.Errorf("%q has source %q, want %q", company.Name, company.Source, maps.SourceScrape)
		}
		if !strings.HasPrefix(company.PlaceID, PlaceIDPrefix) {
			t.Errorf("%q has un-namespaced id %q", company.Name, company.PlaceID)
		}
	}

	if !strings.Contains(stub.lastRequest.URL, "google.com/maps/search/") {
		t.Errorf("sidecar was asked for %q", stub.lastRequest.URL)
	}
	if stub.lastRequest.MaxScrolls != 12 || stub.lastRequest.WaitSelector != `div[role="feed"]` {
		t.Errorf("bounds not passed through: %+v", stub.lastRequest)
	}
	if stub.lastRequest.Locale != "tr" {
		t.Errorf("locale = %q, want tr", stub.lastRequest.Locale)
	}
}

// The caller's cap is the caller's, but it may never exceed ours: the feed
// loads in pages and routinely overshoots on the last scroll.
func TestSearch_RespectsCaps(t *testing.T) {
	stub := newStubSidecar(t, http.StatusOK, func() any {
		return searchResponse{HTML: fixtureHTML(t), ResultCount: 6}
	})

	cfg := testCfg(stub.URL)
	cfg.MapScrapeMaxResults = 2
	c := New(cfg)

	companies, err := c.Search(context.Background(), maps.Query{Text: "dentists", MaxResults: 500})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(companies) != 2 {
		t.Errorf("got %d companies, want the configured ceiling of 2", len(companies))
	}
	if stub.lastRequest.MaxResults != 2 {
		t.Errorf("asked the sidecar for %d results, want 2", stub.lastRequest.MaxResults)
	}
}

func TestSearch_SidecarDownNamesTheFix(t *testing.T) {
	c := New(testCfg("http://127.0.0.1:1")) // nothing listens here

	_, err := c.Search(context.Background(), maps.Query{Text: "dentists"})
	if !errors.Is(err, ErrSidecarUnavailable) {
		t.Fatalf("err = %v, want ErrSidecarUnavailable", err)
	}
	if !strings.Contains(err.Error(), "make maps-up") {
		t.Errorf("error must name its fix, got: %v", err)
	}
}

func TestSearch_ConsentWallIsItsOwnError(t *testing.T) {
	stub := newStubSidecar(t, http.StatusConflict, func() any {
		return searchResponse{Error: "google served a consent or captcha wall"}
	})
	c := New(testCfg(stub.URL))

	_, err := c.Search(context.Background(), maps.Query{Text: "dentists"})
	if !errors.Is(err, ErrBlocked) {
		t.Errorf("err = %v, want ErrBlocked", err)
	}
	// A consent wall is not a broken container: reporting it as one would send
	// an operator to restart docker for a problem docker does not have.
	if errors.Is(err, ErrSidecarUnavailable) {
		t.Error("a block must not be reported as an unavailable sidecar")
	}
}

func TestSearch_EmptyFeedIsItsOwnError(t *testing.T) {
	stub := newStubSidecar(t, http.StatusOK, func() any {
		return searchResponse{HTML: `<div role="feed"></div>`, ResultCount: 0}
	})
	c := New(testCfg(stub.URL))

	_, err := c.Search(context.Background(), maps.Query{Text: "dentists"})
	if !errors.Is(err, ErrNoResults) {
		t.Errorf("err = %v, want ErrNoResults", err)
	}
}

func TestSearch_BadStatusAndGarbage(t *testing.T) {
	t.Run("500", func(t *testing.T) {
		stub := newStubSidecar(t, http.StatusInternalServerError, func() any {
			return searchResponse{Error: "boom"}
		})
		c := New(testCfg(stub.URL))
		_, err := c.Search(context.Background(), maps.Query{Text: "x"})
		if !errors.Is(err, ErrSidecarUnavailable) {
			t.Errorf("err = %v, want ErrSidecarUnavailable", err)
		}
	})

	t.Run("not JSON", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("<html>a proxy error page</html>"))
		}))
		defer srv.Close()

		c := New(testCfg(srv.URL))
		_, err := c.Search(context.Background(), maps.Query{Text: "x"})
		if !errors.Is(err, ErrSidecarUnavailable) {
			t.Errorf("err = %v, want ErrSidecarUnavailable", err)
		}
	})
}

func TestSearch_EmptyQueryIsRejectedBeforeTheNetwork(t *testing.T) {
	c := New(testCfg("http://127.0.0.1:1"))

	_, err := c.Search(context.Background(), maps.Query{Text: "   "})
	if !errors.Is(err, maps.ErrBadRequest) {
		t.Errorf("err = %v, want maps.ErrBadRequest", err)
	}
}

// A cancelled caller is not a broken container, and must not be reported as
// one — that is the difference between "you stopped" and "go restart docker".
func TestSearch_CancellationIsNotAnOutage(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	c := New(testCfg(srv.URL))
	_, err := c.Search(ctx, maps.Query{Text: "dentists"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestHealth(t *testing.T) {
	stub := newStubSidecar(t, http.StatusOK, func() any { return searchResponse{} })
	c := New(testCfg(stub.URL))

	ok, err := c.Health(context.Background())
	if err != nil || !ok {
		t.Errorf("Health = %v, %v; want true, nil", ok, err)
	}

	down := New(testCfg("http://127.0.0.1:1"))
	ok, err = down.Health(context.Background())
	if ok || !errors.Is(err, ErrSidecarUnavailable) {
		t.Errorf("Health = %v, %v; want false, ErrSidecarUnavailable", ok, err)
	}
}

// Politeness is process-wide and cancellable: it must delay a second call to
// the same host rather than let two searches arrive together.
func TestWaitPolite(t *testing.T) {
	politeMu.Lock()
	delete(politeHosts, "example.test")
	politeMu.Unlock()

	ctx := context.Background()
	start := time.Now()
	if err := waitPolite(ctx, 50*time.Millisecond, "https://example.test/a"); err != nil {
		t.Fatal(err)
	}
	if err := waitPolite(ctx, 50*time.Millisecond, "https://example.test/b"); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Errorf("second call waited %v, want at least 50ms", elapsed)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitPolite(cancelled, time.Hour, "https://example.test/c"); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
