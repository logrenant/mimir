package mapscrape

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/config"
)

func ensureConfig(t *testing.T, baseURL string) config.Config {
	t.Helper()
	cfg := config.Load()
	cfg.MapScrapeBaseURL = baseURL
	cfg.MapScrapeStartTimeout = 2 * time.Second
	return cfg
}

// A healthy sidecar costs one loopback GET and never reaches docker. That is
// what makes it safe to call this on every search.
func TestEnsureRunning_HealthyIsANoOp(t *testing.T) {
	var probes int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			probes++
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := ensureConfig(t, srv.URL)
	// Deliberately unusable: reaching docker at all would fail the test.
	cfg.MapScrapeComposeFile = filepath.Join(t.TempDir(), "does-not-exist.yml")

	if err := New(cfg).EnsureRunning(context.Background()); err != nil {
		t.Fatalf("EnsureRunning on a healthy sidecar: %v", err)
	}
	if probes != 1 {
		t.Errorf("health probes: got %d, want 1", probes)
	}
}

// Down and unstartable is one error that names both halves: what is wrong, and
// why this process could not fix it (SD-6).
func TestEnsureRunning_NoComposeFileNamesTheFix(t *testing.T) {
	cfg := ensureConfig(t, "http://127.0.0.1:1")

	cfg.MapScrapeComposeFile = ""
	err := New(cfg).EnsureRunning(context.Background())
	if !errors.Is(err, ErrDockerUnavailable) {
		t.Fatalf("got %v, want ErrDockerUnavailable", err)
	}

	cfg.MapScrapeComposeFile = filepath.Join(t.TempDir(), "missing.yml")
	err = New(cfg).EnsureRunning(context.Background())
	if !errors.Is(err, ErrDockerUnavailable) {
		t.Fatalf("got %v, want ErrDockerUnavailable", err)
	}
	if got := err.Error(); !strings.Contains(got, "make maps-up") {
		t.Errorf("the error must name the fix: %s", got)
	}
}

// The compose file travels with the installed daemon, which is what lets a
// launchd-started process start the container at all.
func TestDefaultComposeFile_IsFoundBesideTheStore(t *testing.T) {
	support := t.TempDir()
	dir := filepath.Join(support, "deploy", "playwright-maps")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	compose := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(compose, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	t.Setenv("MIMIR_STORE_PATH", filepath.Join(support, "mimir.db"))
	if got := config.Load().MapScrapeComposeFile; got != compose {
		t.Errorf("compose file: got %q, want %q", got, compose)
	}
}
