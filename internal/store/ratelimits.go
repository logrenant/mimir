package store

import (
	"context"
	"errors"
	"time"
)

// Phases of the rate-limit log. Wire strings — the daemon's HTTP API and the
// desktop app both read them.
const (
	// RateLimitPhaseRun is a run the token budget cut off mid-task. The run is
	// not failed: it goes back in the queue with its session id, so the window
	// reset picks the same work up rather than starting it over.
	RateLimitPhaseRun = "run"
	// RateLimitPhaseDispatch is a queued task the dispatcher stepped over
	// because the slot it needs has nothing left to spend. Written once per
	// pause, not once per pump: the queue is pumped on every release, and a
	// row for each would bury the moment that matters.
	RateLimitPhaseDispatch = "dispatch"
	// RateLimitPhaseResumed is the window rolling over — the pipeline picking
	// itself back up without anybody pressing anything. It is the row that
	// closes a pause, and its absence is how an operator sees one still open.
	RateLimitPhaseResumed = "resumed"
)

// RateLimitRow is one moment in the life of a spent token budget.
//
// RunID is empty for the phases that are about the queue rather than about one
// task. ResetsAt is zero when the CLI never said when the window rolls over,
// which is why the runner carries its own fallback rather than treating zero as
// "now".
type RateLimitRow struct {
	ID        int64     `json:"id"`
	At        time.Time `json:"at"`
	Phase     string    `json:"phase"`
	AccountID string    `json:"account_id,omitempty"`
	RunID     string    `json:"run_id,omitempty"`
	ResetsAt  time.Time `json:"resets_at,omitempty"`
	Detail    string    `json:"detail,omitempty"`
}

// InsertRateLimitEvent appends one moment to the log.
//
// Append-only on purpose: a pause and the resume that ended it are two facts
// about two different times, and collapsing them into one mutable row would
// lose exactly the history this table exists to keep.
func (s *Store) InsertRateLimitEvent(ctx context.Context, e RateLimitRow) error {
	if s == nil || s.db == nil {
		return unavailable(errors.New("store not open"))
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO rate_limit_log (at, phase, account_id, run_id, resets_at, detail)
		VALUES (?, ?, ?, ?, ?, ?)`,
		unixOrZero(e.At), e.Phase, e.AccountID, e.RunID,
		unixOrZero(e.ResetsAt), e.Detail)
	if err != nil {
		return unavailable(err)
	}
	return nil
}

// ListRateLimitEvents returns the log, most recent first.
func (s *Store) ListRateLimitEvents(ctx context.Context, limit int) ([]RateLimitRow, error) {
	if s == nil || s.db == nil {
		return nil, unavailable(errors.New("store not open"))
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, at, phase, account_id, run_id, resets_at, detail
		FROM rate_limit_log ORDER BY at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()

	var out []RateLimitRow
	for rows.Next() {
		var (
			e            RateLimitRow
			at, resetsAt int64
		)
		if err := rows.Scan(&e.ID, &at, &e.Phase, &e.AccountID, &e.RunID,
			&resetsAt, &e.Detail); err != nil {
			return nil, unavailable(err)
		}
		e.At = timeOrZero(at)
		e.ResetsAt = timeOrZero(resetsAt)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}
