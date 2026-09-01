// Package store provides the local SQLite persistence layer for goat-mcp.
//
// It exists to stop the process paying twice for work it has already done:
// a crawl already fetched, or a refine already distilled by the `claude` CLI.
// Everything here is a cache or a local record — the store is never a source
// of truth the consumer sees directly, and losing it must only cost time, not
// correctness (SD-6).
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	// Pure-Go SQLite driver: no CGO, so `make release` keeps cross-compiling.
	_ "modernc.org/sqlite"

	"github.com/logrenant/goat-mcp/internal/config"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// ErrStoreUnavailable means the local cache database could not be opened or
// queried. Callers must treat this as a degradation, never a fatal error: the
// pipeline runs without a cache (SD-6).
var ErrStoreUnavailable = errors.New("store: sqlite database unavailable")

// Store is a handle on the local cache database.
type Store struct {
	db *sql.DB
}

func unavailable(cause error) error {
	return fmt.Errorf("%w: %v — goat-mcp runs without a cache until the store path is writable", ErrStoreUnavailable, cause)
}

// Open opens (creating if needed) the database at cfg.StorePath and applies
// any pending migrations under ctx. A non-nil error means the caller should
// continue with a nil Store rather than abort.
func Open(ctx context.Context, cfg config.Config) (*Store, error) {
	if cfg.StorePath == "" {
		return nil, unavailable(errors.New("StorePath is empty"))
	}
	if dir := filepath.Dir(cfg.StorePath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, unavailable(err)
		}
	}

	// WAL lets the short-lived goat-mcp process and a long-running reader share
	// the file; busy_timeout absorbs the brief write contention that follows.
	dsn := cfg.StorePath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, unavailable(err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)

	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database handle. Safe on a nil Store.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Health reports whether the database is reachable.
func (s *Store) Health(ctx context.Context) error {
	if s == nil || s.db == nil {
		return unavailable(errors.New("store not open"))
	}
	if err := s.db.PingContext(ctx); err != nil {
		return unavailable(err)
	}
	return nil
}

// migrate applies every embedded migration not yet recorded, in lexical
// filename order. Applying an already-applied migration is a no-op, so Open is
// idempotent.
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name       TEXT PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return unavailable(err)
	}

	names, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return unavailable(err)
	}
	sort.Strings(names)

	for _, name := range names {
		var applied int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM schema_migrations WHERE name = ?`, name).Scan(&applied); err != nil {
			return unavailable(err)
		}
		if applied > 0 {
			continue
		}

		body, err := migrationFS.ReadFile(name)
		if err != nil {
			return unavailable(err)
		}

		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return unavailable(err)
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			return unavailable(fmt.Errorf("migration %s: %w", name, err))
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`,
			name, time.Now().Unix()); err != nil {
			_ = tx.Rollback()
			return unavailable(err)
		}
		if err := tx.Commit(); err != nil {
			return unavailable(err)
		}
	}
	return nil
}
