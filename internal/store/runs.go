package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Run status values. Wire strings — the daemon's HTTP API and the desktop app
// both read them.
const (
	// RunStatusBacklog is a task the operator wrote down and has not released.
	// Nothing schedules it; it exists so a board can hold intent.
	RunStatusBacklog = "backlog"
	// RunStatusQueued is released and waiting for a dispatcher slot. It
	// survives a restart, which is what makes the queue durable rather than a
	// property of one process's memory.
	RunStatusQueued    = "queued"
	RunStatusRunning   = "running"
	RunStatusCompleted = "completed"
	RunStatusFailed    = "failed"
	// RunStatusStopped is a run the operator cancelled. Kept apart from
	// failed: nothing went wrong, and nobody should be asked to debug it.
	RunStatusStopped = "stopped"
)

// IsTerminalStatus reports whether a run in this state will never publish
// another event. It is the single definition of "over" — the websocket, the
// dispatcher and the memory ingest loop all ask it rather than each keeping
// their own list, which is how the previous three-value set drifted.
func IsTerminalStatus(status string) bool {
	switch status {
	case RunStatusCompleted, RunStatusFailed, RunStatusStopped:
		return true
	default:
		return false
	}
}

// RunRow is one `claude` coding session.
type RunRow struct {
	ID        string
	ProjectID string
	Title     string
	Prompt    string
	Status    string
	SessionID string
	Model     string
	// RequestedAccountID is the slot the operator pinned; empty means "any
	// free one". AccountID is the slot it actually ran on, filled in when the
	// dispatcher claims it. Two fields because they answer different
	// questions, and collapsing them would lose the pin the moment a run
	// started.
	RequestedAccountID string
	AccountID          string
	TranscriptPath     string
	Attachments        string // JSON array of attachment ids, "" when there are none
	CostUSD            float64
	NumTurns           int
	Error              string
	CreatedAt          time.Time
	QueuedAt           time.Time
	StartedAt          time.Time
	EndedAt            time.Time
}

// runColumns is positional: it and scanRun are read together, and a column
// added to one without the other silently shifts every field after it.
const runColumns = `id, project_id, title, prompt, status, session_id, model,
	requested_account_id, account_id, transcript_path, attachments,
	cost_usd, num_turns, error, created_at, queued_at, started_at, ended_at`

// CodingRunStats is a lightweight cost/usage rollup for the daemon's
// /diagnostics endpoint (M7). It is a single aggregate query, not a scan of
// every row.
type CodingRunStats struct {
	Total   int     `json:"total"`
	Backlog int     `json:"backlog"`
	Queued  int     `json:"queued"`
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
			COALESCE(SUM(CASE WHEN status = 'backlog' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'queued'  THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'running' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'failed'  THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(cost_usd), 0)
		FROM coding_runs`).Scan(&st.Total, &st.Backlog, &st.Queued,
		&st.Running, &st.Failed, &st.CostUSD)
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
			(id, project_id, title, prompt, status, session_id, model,
			 requested_account_id, account_id, transcript_path, attachments,
			 cost_usd, num_turns, error,
			 created_at, queued_at, started_at, ended_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.ProjectID, r.Title, r.Prompt, r.Status, r.SessionID, r.Model,
		r.RequestedAccountID, r.AccountID, r.TranscriptPath, r.Attachments,
		r.CostUSD, r.NumTurns, r.Error,
		unixOrZero(r.CreatedAt), unixOrZero(r.QueuedAt),
		unixOrZero(r.StartedAt), unixOrZero(r.EndedAt))
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
		r                                       RunRow
		createdAt, queuedAt, startedAt, endedAt int64
	)
	if err := scan(
		&r.ID, &r.ProjectID, &r.Title, &r.Prompt, &r.Status, &r.SessionID,
		&r.Model, &r.RequestedAccountID, &r.AccountID, &r.TranscriptPath,
		&r.Attachments, &r.CostUSD, &r.NumTurns, &r.Error,
		&createdAt, &queuedAt, &startedAt, &endedAt,
	); err != nil {
		return RunRow{}, err
	}
	r.CreatedAt = timeOrZero(createdAt)
	r.QueuedAt = timeOrZero(queuedAt)
	r.StartedAt = timeOrZero(startedAt)
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
	// Ordered by whichever timestamp the row actually has: a backlog task has
	// never started, so ordering by started_at alone would bury every one of
	// them under the oldest finished run.
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+runColumns+` FROM coding_runs WHERE project_id = ?
		 ORDER BY MAX(started_at, queued_at, created_at) DESC, rowid DESC
		 LIMIT ?`, projectID, limit)
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

// UpdateRunStatus moves a run between states, but only from the state the
// caller believes it is in.
//
// The `from` guard is what makes the transition safe without a lock: two
// operators clicking "run" on the same card, or a stop racing the dispatcher's
// claim, both resolve to one winner and one `false`. Returns whether the row
// moved.
func (s *Store) UpdateRunStatus(ctx context.Context, id, from, to string,
	queuedAt, endedAt time.Time, runErr string) (bool, error) {

	if s == nil || s.db == nil {
		return false, unavailable(errors.New("store not open"))
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE coding_runs SET
			status    = ?,
			queued_at = ?,
			ended_at  = ?,
			error     = ?
		WHERE id = ? AND status = ?`,
		to, unixOrZero(queuedAt), unixOrZero(endedAt), runErr, id, from)
	if err != nil {
		return false, unavailable(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, unavailable(err)
	}
	return n > 0, nil
}

// EditableStatuses are the states a card's own text may still be rewritten in.
//
// Running and completed are missing on purpose, and for the same reason: the
// prompt of a run that is spending or has spent tokens is a record of what was
// asked, not a field. Everything else has spent nothing yet — a backlog card is
// intent, a queued one is intent waiting its turn, and a failed or stopped one
// is intent that has to be corrected before it is worth another attempt.
var EditableStatuses = []string{
	RunStatusBacklog, RunStatusQueued, RunStatusFailed, RunStatusStopped,
}

// RunEdit is what an operator may change about a card. Every field is written,
// so a caller sends the row it wants to exist, not a patch.
type RunEdit struct {
	Title       string
	Prompt      string
	Model       string
	Attachments string // JSON array of attachment ids, "" when there are none
}

// EditRun rewrites what a card asks for, reporting whether the row moved.
//
// The status guard is in the WHERE clause rather than in a read-then-write for
// the same reason every other transition here is: the dispatcher can claim a
// queued run between the two, and an edit that landed on a run already talking
// to the CLI would change the prompt out from under it. A row that has moved on
// affects nothing, and the caller answers 409.
func (s *Store) EditRun(ctx context.Context, id string, e RunEdit) (bool, error) {
	if s == nil || s.db == nil {
		return false, unavailable(errors.New("store not open"))
	}
	args := []any{e.Title, e.Prompt, e.Model, e.Attachments, id}
	placeholders := make([]string, 0, len(EditableStatuses))
	for _, status := range EditableStatuses {
		args = append(args, status)
		placeholders = append(placeholders, "?")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE coding_runs SET title = ?, prompt = ?, model = ?, attachments = ?
		 WHERE id = ? AND status IN (`+strings.Join(placeholders, ", ")+`)`, args...)
	if err != nil {
		return false, unavailable(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, unavailable(err)
	}
	return n > 0, nil
}

// RequeueRun puts a finished run back in the queue, reporting whether the row
// moved.
//
// Only from a state a retry means something in: `failed` and `stopped`. The
// same compare-and-swap discipline as UpdateRunStatus, for the same reason — an
// operator retrying a card that has already moved on must lose, not queue a run
// that is running.
//
// The session id survives, and that is the whole of the feature: it is what the
// runner hands `claude --resume`, so the second attempt continues the first
// instead of starting the task over. keepSession = false clears it, which is
// the way out of a session the CLI can no longer resume.
//
// The error is deliberately left in place. ClaimRun clears it when the run
// actually starts again, so until then the card still says why it failed — and
// the runner reads it to tell the resumed session what interrupted it.
func (s *Store) RequeueRun(ctx context.Context, id string, at time.Time, keepSession bool) (bool, error) {
	if s == nil || s.db == nil {
		return false, unavailable(errors.New("store not open"))
	}
	set := `status = ?, queued_at = ?, ended_at = 0`
	if !keepSession {
		set += `, session_id = ''`
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE coding_runs SET `+set+` WHERE id = ? AND status IN (?, ?)`,
		RunStatusQueued, unixOrZero(at), id, RunStatusFailed, RunStatusStopped)
	if err != nil {
		return false, unavailable(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, unavailable(err)
	}
	return n > 0, nil
}

// ListQueuedRuns returns the queue, oldest first.
//
// The dispatcher reads the queue rather than being handed its head, because
// which run may start next is not a property of the queue alone: a run pinned
// to a busy account has to be stepped over so one behind it that can run does.
func (s *Store) ListQueuedRuns(ctx context.Context, limit int) ([]RunRow, error) {
	if s == nil || s.db == nil {
		return nil, unavailable(errors.New("store not open"))
	}
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+runColumns+` FROM coding_runs WHERE status = ?
		 ORDER BY queued_at, rowid LIMIT ?`, RunStatusQueued, limit)
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

// ClaimRun marks one queued run as running on a given account, reporting
// whether this caller is the one that got it.
//
// A compare-and-swap rather than a transaction: the dispatcher claims from
// several goroutines as slots free up, and a read-then-write transaction would
// have to upgrade a deferred lock, which SQLite answers with SQLITE_BUSY rather
// than by waiting. The conditional UPDATE is the atomic step; the loser sees
// zero rows affected and looks again.
func (s *Store) ClaimRun(ctx context.Context, id, accountID string, at time.Time) (bool, error) {
	if s == nil || s.db == nil {
		return false, unavailable(errors.New("store not open"))
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE coding_runs
		 SET status = ?, account_id = ?, started_at = ?, ended_at = 0, error = ''
		 WHERE id = ? AND status = ?`,
		RunStatusRunning, accountID, at.Unix(), id, RunStatusQueued)
	if err != nil {
		return false, unavailable(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, unavailable(err)
	}
	return n > 0, nil
}

// RunningAccountIDs reports which slots are occupied according to the store.
//
// The dispatcher's own in-flight map is the live answer for this process; this
// is the answer across a restart, before Resume has run.
func (s *Store) RunningAccountIDs(ctx context.Context) ([]string, error) {
	if s == nil || s.db == nil {
		return nil, unavailable(errors.New("store not open"))
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT account_id FROM coding_runs WHERE status = ?`, RunStatusRunning)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, unavailable(err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}

// ReconcileRunningRuns fails every row still marked running, and reports how
// many it moved.
//
// Called once at startup, before anything is dispatched. A `running` row means
// a subprocess this process owns, and at startup this process owns none — so
// every such row is the residue of a daemon that was killed, and leaving it
// alone is how the board came to show work that was not happening. Queued rows
// are deliberately untouched: they are the durable queue, and they are meant to
// survive exactly this.
func (s *Store) ReconcileRunningRuns(ctx context.Context, reason string, at time.Time) (int, error) {
	if s == nil || s.db == nil {
		return 0, unavailable(errors.New("store not open"))
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE coding_runs SET status = ?, error = ?, ended_at = ?
		WHERE status = ?`,
		RunStatusFailed, reason, at.Unix(), RunStatusRunning)
	if err != nil {
		return 0, unavailable(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, unavailable(err)
	}
	return int(n), nil
}

// DeleteRun removes a run row. The transcript and any attachments are the
// caller's to clean up: this package knows about rows, not files.
func (s *Store) DeleteRun(ctx context.Context, id string) error {
	if s == nil || s.db == nil {
		return unavailable(errors.New("store not open"))
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM coding_runs WHERE id = ?`, id); err != nil {
		return unavailable(err)
	}
	return nil
}

// ReferencedAttachments returns the raw attachments column of every run that
// has one, so a caller can work out which files on disk are still owned.
func (s *Store) ReferencedAttachments(ctx context.Context) ([]string, error) {
	if s == nil || s.db == nil {
		return nil, unavailable(errors.New("store not open"))
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT attachments FROM coding_runs WHERE attachments <> ''`)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, unavailable(err)
		}
		out = append(out, raw)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}

// ParkRun puts a run that is in flight back in the queue, reporting whether the
// row moved.
//
// The one state RequeueRun cannot start from, and for a reason it cannot serve:
// this is not a retry of something that finished, it is a run interrupted by a
// spent token budget while it was still working. Failing it first and requeuing
// afterwards would put a failure on the card that nothing went wrong to cause,
// and would race the operator reading it.
//
// sessionID is written when it is non-empty, which is what makes the resume a
// continuation. A run only learns its session from the CLI's first line and the
// row does not carry it until the run ends, so a run parked mid-task has one in
// hand that the row has never seen — dropping it here would restart the task
// from nothing when the window rolls over.
//
// The reason is left on the row exactly as RequeueRun leaves an error: ClaimRun
// clears it when the run actually starts again, so until then the card can say
// why it is waiting, and the resumed session is told what cut it off.
func (s *Store) ParkRun(ctx context.Context, id, sessionID string, at time.Time, reason string) (bool, error) {
	if s == nil || s.db == nil {
		return false, unavailable(errors.New("store not open"))
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE coding_runs SET
			status     = ?,
			queued_at  = ?,
			ended_at   = 0,
			error      = ?,
			session_id = CASE WHEN ? = '' THEN session_id ELSE ? END
		WHERE id = ? AND status = ?`,
		RunStatusQueued, unixOrZero(at), reason,
		sessionID, sessionID, id, RunStatusRunning)
	if err != nil {
		return false, unavailable(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, unavailable(err)
	}
	return n > 0, nil
}
