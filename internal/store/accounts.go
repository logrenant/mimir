package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// AccountRow is the Claude Code credential slot Mimir signs into. There is at
// most one row.
//
// ConfigDir is the value handed to the CLI as CLAUDE_SECURESTORAGE_CONFIG_DIR,
// from which it derives its keychain entry. It is always Mimir's own directory
// (config.ClaudeSessionDir) — the empty string, which would mean the CLI's own
// default slot, is what this deliberately never writes.
type AccountRow struct {
	ID        string
	Label     string
	ConfigDir string
	// Discovered is vestigial: it belonged to the scan of ~/.claude-accounts
	// that made the filesystem the authority for which slots existed. Mimir
	// has one account and signs into it itself, so nothing sets this. The
	// column stays because migrations are append-only.
	Discovered bool
	CreatedAt  time.Time
	LastUsedAt time.Time
}

const accountColumns = `id, label, config_dir, discovered, created_at, last_used_at`

func scanAccount(scan func(dest ...any) error) (AccountRow, error) {
	var (
		a                     AccountRow
		createdAt, lastUsedAt int64
	)
	if err := scan(&a.ID, &a.Label, &a.ConfigDir, &a.Discovered,
		&createdAt, &lastUsedAt); err != nil {
		return AccountRow{}, err
	}
	a.CreatedAt = timeOrZero(createdAt)
	a.LastUsedAt = timeOrZero(lastUsedAt)
	return a, nil
}

// InsertAccount records a credential slot.
func (s *Store) InsertAccount(ctx context.Context, a AccountRow) error {
	if s == nil || s.db == nil {
		return unavailable(errors.New("store not open"))
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO accounts (id, label, config_dir, discovered,
		                      created_at, last_used_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		a.ID, a.Label, a.ConfigDir, a.Discovered,
		unixOrZero(a.CreatedAt), unixOrZero(a.LastUsedAt))
	if err != nil {
		return unavailable(err)
	}
	return nil
}

// GetAccount returns the account with this id.
func (s *Store) GetAccount(ctx context.Context, id string) (AccountRow, bool, error) {
	if s == nil || s.db == nil {
		return AccountRow{}, false, unavailable(errors.New("store not open"))
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+accountColumns+` FROM accounts WHERE id = ?`, id)

	a, err := scanAccount(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return AccountRow{}, false, nil
	}
	if err != nil {
		return AccountRow{}, false, unavailable(err)
	}
	return a, true, nil
}

// FindAccountByConfigDir is what makes registration idempotent: the same
// directory is the same credential slot, not a second one.
func (s *Store) FindAccountByConfigDir(ctx context.Context, dir string) (AccountRow, bool, error) {
	if s == nil || s.db == nil {
		return AccountRow{}, false, unavailable(errors.New("store not open"))
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+accountColumns+` FROM accounts WHERE config_dir = ?`, dir)

	a, err := scanAccount(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return AccountRow{}, false, nil
	}
	if err != nil {
		return AccountRow{}, false, unavailable(err)
	}
	return a, true, nil
}

// ListAccounts returns every registered slot, oldest first — the order they
// were added is the order the dispatcher tries them in, which makes automatic
// assignment predictable rather than arbitrary.
func (s *Store) ListAccounts(ctx context.Context) ([]AccountRow, error) {
	if s == nil || s.db == nil {
		return nil, unavailable(errors.New("store not open"))
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+accountColumns+` FROM accounts ORDER BY created_at, rowid`)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()

	var out []AccountRow
	for rows.Next() {
		a, err := scanAccount(rows.Scan)
		if err != nil {
			return nil, unavailable(err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}

// TouchAccount records that a run just used this slot.
func (s *Store) TouchAccount(ctx context.Context, id string, at time.Time) error {
	if s == nil || s.db == nil {
		return unavailable(errors.New("store not open"))
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE accounts SET last_used_at = ? WHERE id = ?`, at.Unix(), id); err != nil {
		return unavailable(err)
	}
	return nil
}

// DeleteAllAccounts forgets every slot.
//
// This is the store half of "closing Mimir resets the accounts": the login
// itself is signed out through the CLI, and what is left here is bookkeeping
// for an identity that no longer exists. A truncate rather than a delete by id
// because there is only ever one row and the caller is not holding its id — it
// is clearing whatever a previous, possibly crashed, run left behind.
func (s *Store) DeleteAllAccounts(ctx context.Context) error {
	if s == nil || s.db == nil {
		return unavailable(errors.New("store not open"))
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM accounts`); err != nil {
		return unavailable(err)
	}
	return nil
}
