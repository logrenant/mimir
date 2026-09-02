package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// AccountRow is one Claude Code credential slot.
//
// ConfigDir is the value handed to the CLI as CLAUDE_SECURESTORAGE_CONFIG_DIR,
// from which it derives its keychain entry. Empty means the CLI's own default
// slot: the variable is not set at all, rather than set to "".
type AccountRow struct {
	ID         string
	Label      string
	ConfigDir  string
	CreatedAt  time.Time
	LastUsedAt time.Time
}

const accountColumns = `id, label, config_dir, created_at, last_used_at`

func scanAccount(scan func(dest ...any) error) (AccountRow, error) {
	var (
		a                     AccountRow
		createdAt, lastUsedAt int64
	)
	if err := scan(&a.ID, &a.Label, &a.ConfigDir, &createdAt, &lastUsedAt); err != nil {
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
		INSERT INTO accounts (id, label, config_dir, created_at, last_used_at)
		VALUES (?, ?, ?, ?, ?)`,
		a.ID, a.Label, a.ConfigDir, unixOrZero(a.CreatedAt), unixOrZero(a.LastUsedAt))
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

// DeleteAccount forgets a slot. The credentials themselves live in the
// keychain and are untouched: this removes Mimir's knowledge of the slot, not
// the login.
func (s *Store) DeleteAccount(ctx context.Context, id string) error {
	if s == nil || s.db == nil {
		return unavailable(errors.New("store not open"))
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, id); err != nil {
		return unavailable(err)
	}
	return nil
}

// CountRunsForAccount reports how many of a slot's runs are in these statuses —
// what a delete has to check before it orphans a queue.
func (s *Store) CountRunsForAccount(ctx context.Context, id string, statuses ...string) (int, error) {
	if s == nil || s.db == nil {
		return 0, unavailable(errors.New("store not open"))
	}
	if len(statuses) == 0 {
		return 0, nil
	}
	query := `SELECT COUNT(*) FROM coding_runs
	          WHERE (account_id = ? OR requested_account_id = ?) AND status IN (?`
	args := []any{id, id, statuses[0]}
	for _, s := range statuses[1:] {
		query += ", ?"
		args = append(args, s)
	}
	query += ")"

	var n int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, unavailable(err)
	}
	return n, nil
}
