package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/logrenant/goat-mcp/internal/config"
)

// testConfig returns a Config pointing the store at a fresh temp database.
func testConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Load()
	cfg.StorePath = filepath.Join(t.TempDir(), "goat.db")
	return cfg
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), testConfig(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpen_CreatesParentDirectory(t *testing.T) {
	cfg := config.Load()
	cfg.StorePath = filepath.Join(t.TempDir(), "nested", "deeper", "goat.db")

	s, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Open into a missing directory: %v", err)
	}
	defer func() { _ = s.Close() }()

	if err := s.Health(context.Background()); err != nil {
		t.Fatalf("Health after Open: %v", err)
	}
}

func TestOpen_IsIdempotent(t *testing.T) {
	cfg := testConfig(t)

	first, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	// Write a row so we can prove the second Open did not reset the schema.
	if err := first.PutCrawl(context.Background(), testPage()); err != nil {
		t.Fatalf("PutCrawl: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("second Open on an existing database: %v", err)
	}
	defer func() { _ = second.Close() }()

	_, ok, err := second.GetCrawl(context.Background(), testPage().URL, time.Hour)
	if err != nil {
		t.Fatalf("GetCrawl after reopen: %v", err)
	}
	if !ok {
		t.Fatal("row written before reopen was lost: migrations are not idempotent")
	}
}

func TestOpen_EmptyPathIsUnavailable(t *testing.T) {
	cfg := config.Load()
	cfg.StorePath = ""

	_, err := Open(context.Background(), cfg)
	if !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("want ErrStoreUnavailable, got %v", err)
	}
}

func TestNilStore_IsSafeAndAlwaysMisses(t *testing.T) {
	var s *Store
	ctx := context.Background()

	if err := s.Close(); err != nil {
		t.Fatalf("Close on nil store: %v", err)
	}
	if err := s.PutCrawl(ctx, testPage()); err != nil {
		t.Fatalf("PutCrawl on nil store: %v", err)
	}
	if _, ok, err := s.GetCrawl(ctx, "https://example.com", time.Hour); err != nil || ok {
		t.Fatalf("GetCrawl on nil store: ok=%v err=%v, want miss and no error", ok, err)
	}
	if err := s.PutRefined(ctx, testKey(), testOutput()); err != nil {
		t.Fatalf("PutRefined on nil store: %v", err)
	}
	if _, ok, err := s.GetRefined(ctx, testKey(), time.Hour); err != nil || ok {
		t.Fatalf("GetRefined on nil store: ok=%v err=%v, want miss and no error", ok, err)
	}
	if err := s.Health(ctx); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("Health on nil store: want ErrStoreUnavailable, got %v", err)
	}
}
