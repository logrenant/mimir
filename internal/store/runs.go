package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Run status values. Wire strings — the daemon's HTTP API and the desktop app
// both read them.
const (
	RunStatusRunning   = "running"
	RunStatusCompleted = "completed"
	RunStatusFailed    = "failed"
)

// RunRow is one `claude` coding session.
type RunRow struct {
	ID             string
	ProjectID      string
	Prompt         string
	Status         string
	SessionID      string
	Model          string
	TranscriptPath string
	CostUSD        float64
	NumTurns       int
	Error          string
	StartedAt      time.Time
	EndedAt        time.Time
}

const runColumns = `id, project_id, prompt, status, session_id, model,
	transcript_path, cost_usd, num_turns, error, started_at, ended_at`

// CodingRunStats is a lightweight cost/usage rollup for the daemon's
// /diagnostics endpoint (M7). It is a single aggregate query, not a scan of
// every row.
type CodingRunStats struct {
	Total   int     `json:"total"`
	Running int     `json:"running"`
	Failed  int     `json:"failed"`
	CostUSD float64 `json:"cost_usd"`
}

// CodingRunStats returns the counts and total spend across every recorded
// coding run. Nil-Store tolerant: no store is a zero rollup, not an error
// (SD-6).
func (s *Store) CodingRunStats(ctx context.Context) (CodingRunStats, error) {
	var st CodingRunStats
	if s == nil || s.db == nil {
		return st, nil
	}
	err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN status = 'running' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'failed'  THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(cost_usd), 0)
		FROM coding_runs`).Scan(&st.Total, &st.Running, &st.Failed, &st.CostUSD)
	if err != nil {
		return CodingRunStats{}, unavailable(err)
	}
	return st, nil
}

// InsertRun records a run at the moment it starts, before the CLI is spawned,
// so a run is never in flight without a row to find it by.
func (s *Store) InsertRun(ctx context.Context, r RunRow) error {
	if s == nil || s.db == nil {
		return unavailable(errors.New("store not open"))
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO coding_runs
			(id, project_id, prompt, status, session_id, model, transcript_path,
			 cost_usd, num_turns, error, started_at, ended_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.ProjectID, r.Prompt, r.Status, r.SessionID, r.Model,
		r.TranscriptPath, r.CostUSD, r.NumTurns, r.Error,
		r.StartedAt.Unix(), unixOrZero(r.EndedAt))
	if err != nil {
		return unavailable(err)
	}
	return nil
}

// UpdateRunResult records how a run ended. Called once, from the goroutine
// that owns the run.
func (s *Store) UpdateRunResult(ctx context.Context, r RunRow) error {
	if s == nil || s.db == nil {
		return unavailable(errors.New("store not open"))
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE coding_runs SET
			status     = ?,
			session_id = ?,
			model      = ?,
			cost_usd   = ?,
			num_turns  = ?,
			error      = ?,
			ended_at   = ?
		WHERE id = ?`,
		r.Status, r.SessionID, r.Model, r.CostUSD, r.NumTurns, r.Error,
		unixOrZero(r.EndedAt), r.ID)
	if err != nil {
		return unavailable(err)
	}
	return nil
}

func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func timeOrZero(unix int64) time.Time {
	if unix == 0 {
		return time.Time{}
	}
	return time.Unix(unix, 0).UTC()
}

func scanRun(scan func(dest ...any) error) (RunRow, error) {
	var (
		r                  RunRow
		startedAt, endedAt int64
	)
	if err := scan(
		&r.ID, &r.ProjectID, &r.Prompt, &r.Status, &r.SessionID, &r.Model,
		&r.TranscriptPath, &r.CostUSD, &r.NumTurns, &r.Error,
		&startedAt, &endedAt,
	); err != nil {
		return RunRow{}, err
	}
	r.StartedAt = time.Unix(startedAt, 0).UTC()
	r.EndedAt = timeOrZero(endedAt)
	return r, nil
}

// GetRun returns the run with this id.
func (s *Store) GetRun(ctx context.Context, id string) (RunRow, bool, error) {
	if s == nil || s.db == nil {
		return RunRow{}, false, unavailable(errors.New("store not open"))
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+runColumns+` FROM coding_runs WHERE id = ?`, id)

	r, err := scanRun(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return RunRow{}, false, nil
	}
	if err != nil {
		return RunRow{}, false, unavailable(err)
	}
	return r, true, nil
}

// ListRunsByProject returns a project's runs, most recent first.
func (s *Store) ListRunsByProject(ctx context.Context, projectID string, limit int) ([]RunRow, error) {
	if s == nil || s.db == nil {
		return nil, unavailable(errors.New("store not open"))
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+runColumns+` FROM coding_runs WHERE project_id = ?
		 ORDER BY started_at DESC LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()

	var out []RunRow
	for rows.Next() {
		r, err := scanRun(rows.Scan)
		if err != nil {
			return nil, unavailable(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}
