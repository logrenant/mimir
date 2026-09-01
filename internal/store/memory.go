package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode"
)

// EpisodeRow is one distilled iteration: a prompt and the work that answered
// it. Title and Summary are the refined half and may be empty; everything else
// is derived deterministically from the transcript and is always present.
type EpisodeRow struct {
	Key           string
	ProjectPath   string
	SourceKind    string
	SourcePath    string
	SessionID     string
	GitBranch     string
	StartedAt     time.Time
	EndedAt       time.Time
	Title         string
	Summary       string
	FactsJSON     string
	FilesText     string
	CommandsText  string
	InputTokens   int
	OutputTokens  int
	CostUSD       float64
	Significance  int
	RecapAttempts int
	PromptVersion string
}

// NoteRow is a fact somebody chose to pin, as opposed to one the ingester
// inferred.
type NoteRow struct {
	ID          string
	ProjectPath string
	Kind        string
	Text        string
	CreatedAt   time.Time
}

// IngestState is how far the parser got through one transcript file.
type IngestState struct {
	SourcePath  string
	ProjectPath string
	ByteOffset  int64
	SizeSeen    int64
	UpdatedAt   time.Time
}

// MemoryStats is the rollup behind the diagnostics tool: enough to answer "is
// the memory working", nothing more. How much is left to do is deliberately
// absent — that answer depends on the current prompt version and attempt
// ceiling, so it comes from CountPendingRecap rather than from a field here
// that could only ever be a stale guess.
type MemoryStats struct {
	Episodes      int     `json:"episodes"`
	WithSummary   int     `json:"with_summary"`
	Notes         int     `json:"notes"`
	TrackedFiles  int     `json:"tracked_files"`
	CapturedCost  float64 `json:"captured_cost_usd"`
	OldestEpisode int64   `json:"oldest_episode_unix,omitempty"`
	NewestEpisode int64   `json:"newest_episode_unix,omitempty"`
}

const episodeColumns = `key, project_path, source_kind, source_path, session_id,
	git_branch, started_at, ended_at, title, summary, facts_json, files_text,
	commands_text, input_tokens, output_tokens, cost_usd, significance,
	recap_attempts, prompt_version`

// PutEpisode writes one episode, replacing any row with the same key.
//
// The upsert deliberately does not touch title/summary/recap_attempts: phase 1
// of an ingest re-derives the deterministic half of every episode it sees, and
// it must not wipe a recap phase 2 already paid a model call for. UpdateRecap
// is the only writer of those three columns.
//
// Nil-Store tolerant like the rest of this package (SD-6).
func (s *Store) PutEpisode(ctx context.Context, e EpisodeRow) error {
	if s == nil || s.db == nil {
		return nil
	}
	if e.Key == "" || e.ProjectPath == "" {
		return nil
	}
	if e.FactsJSON == "" {
		e.FactsJSON = "{}"
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO memory_episodes (`+episodeColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			project_path  = excluded.project_path,
			source_kind   = excluded.source_kind,
			source_path   = excluded.source_path,
			session_id    = excluded.session_id,
			git_branch    = excluded.git_branch,
			started_at    = excluded.started_at,
			ended_at      = excluded.ended_at,
			facts_json    = excluded.facts_json,
			files_text    = excluded.files_text,
			commands_text = excluded.commands_text,
			input_tokens  = excluded.input_tokens,
			output_tokens = excluded.output_tokens,
			cost_usd      = excluded.cost_usd,
			significance  = excluded.significance`,
		e.Key, e.ProjectPath, e.SourceKind, e.SourcePath, e.SessionID,
		e.GitBranch, e.StartedAt.UTC().Unix(), e.EndedAt.UTC().Unix(), e.Title,
		e.Summary, e.FactsJSON, e.FilesText, e.CommandsText, e.InputTokens,
		e.OutputTokens, e.CostUSD, e.Significance, e.RecapAttempts,
		e.PromptVersion)
	if err != nil {
		return unavailable(err)
	}
	return nil
}

// UpdateRecap records the outcome of one recap attempt.
//
// It is called for a rejected recap too, with empty title/summary: the attempt
// counter is what stops the ingester paying for the same hopeless episode on
// every pass, so not recording a failure would be worse than not trying.
func (s *Store) UpdateRecap(ctx context.Context, key, title, summary, promptVersion string) error {
	if s == nil || s.db == nil || key == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE memory_episodes
		SET title = ?, summary = ?, prompt_version = ?, recap_attempts = recap_attempts + 1
		WHERE key = ?`,
		title, summary, promptVersion, key)
	if err != nil {
		return unavailable(err)
	}
	return nil
}

// PendingRecap returns episodes that deserve a recap and do not have a current
// one, best candidates first.
//
// "Current" is version-scoped: bumping config.MemoryPromptVersion makes every
// stored recap eligible again, which is the documented way to re-derive them.
// maxAttempts caps the retry budget per episode.
func (s *Store) PendingRecap(ctx context.Context, projectPath, promptVersion string, maxAttempts, limit int) ([]EpisodeRow, error) {
	if s == nil || s.db == nil || projectPath == "" || limit <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+episodeColumns+` FROM memory_episodes
		WHERE project_path = ?
		  AND significance > 0
		  AND recap_attempts < ?
		  AND (summary = '' OR prompt_version <> ?)
		ORDER BY significance DESC, started_at DESC
		LIMIT ?`,
		projectPath, maxAttempts, promptVersion, limit)
	if err != nil {
		return nil, unavailable(err)
	}
	return scanEpisodes(rows)
}

// CountPendingRecap is PendingRecap's "how much is left" counterpart. The
// daemon's backfill loop needs a remaining count to know when to stop, and
// counting is far cheaper than fetching.
func (s *Store) CountPendingRecap(ctx context.Context, projectPath, promptVersion string, maxAttempts int) (int, error) {
	if s == nil || s.db == nil || projectPath == "" {
		return 0, nil
	}
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM memory_episodes
		WHERE project_path = ? AND significance > 0 AND recap_attempts < ?
		  AND (summary = '' OR prompt_version <> ?)`,
		projectPath, maxAttempts, promptVersion).Scan(&n)
	if err != nil {
		return 0, unavailable(err)
	}
	return n, nil
}

// RecentEpisodes returns the newest episodes for a project, newest first.
func (s *Store) RecentEpisodes(ctx context.Context, projectPath string, limit int) ([]EpisodeRow, error) {
	if s == nil || s.db == nil || projectPath == "" || limit <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+episodeColumns+` FROM memory_episodes
		WHERE project_path = ?
		ORDER BY started_at DESC
		LIMIT ?`, projectPath, limit)
	if err != nil {
		return nil, unavailable(err)
	}
	return scanEpisodes(rows)
}

// SearchEpisodes runs a full-text search over the project's episodes, best
// match first.
//
// An unparseable query is a miss, never an error: the consumer asked a
// question, and "no results" is a truthful answer to a question this index
// cannot express, whereas an error implies the memory is broken.
func (s *Store) SearchEpisodes(ctx context.Context, projectPath, query string, limit int) ([]EpisodeRow, error) {
	if s == nil || s.db == nil || projectPath == "" || limit <= 0 {
		return nil, nil
	}
	match := ftsQuery(query)
	if match == "" {
		return nil, nil
	}

	// The FTS table is not aliased on purpose: SQLite resolves `MATCH` and
	// `bm25()` against the virtual table's real name, and an alias makes both
	// fail with "no such column".
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+prefixed(episodeColumns, "e")+`
		FROM memory_fts
		JOIN memory_episodes e ON e.rowid = memory_fts.rowid
		WHERE memory_fts MATCH ? AND e.project_path = ?
		ORDER BY bm25(memory_fts)
		LIMIT ?`, match, projectPath, limit)
	if err != nil {
		return nil, unavailable(err)
	}
	return scanEpisodes(rows)
}

// ftsQuery turns arbitrary consumer text into a safe FTS5 MATCH expression.
//
// FTS5's query language is a syntax, not a string: a bare `"`, `*`, `:`, `-`
// or the bare word `OR` from a natural-language question is either a syntax
// error or, worse, a silent change of meaning. So the input is reduced to
// alphanumeric terms, each quoted as a literal, joined with OR. Ranking by
// bm25 then does the work that boolean precision would have.
func ftsQuery(raw string) string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	terms := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, f := range fields {
		f = strings.ToLower(f)
		// Single characters carry no signal and match nearly everything.
		if len([]rune(f)) < 2 {
			continue
		}
		if _, dup := seen[f]; dup {
			continue
		}
		seen[f] = struct{}{}
		terms = append(terms, `"`+f+`"`)
		if len(terms) == 24 {
			break
		}
	}
	return strings.Join(terms, " OR ")
}

// prefixed qualifies a column list with a table alias, so the shared
// episodeColumns constant can be reused in a join without ambiguity.
func prefixed(columns, alias string) string {
	parts := strings.Split(columns, ",")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

func scanEpisodes(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}) ([]EpisodeRow, error) {
	defer func() { _ = rows.Close() }()

	var out []EpisodeRow
	for rows.Next() {
		var e EpisodeRow
		var startedAt, endedAt int64
		if err := rows.Scan(&e.Key, &e.ProjectPath, &e.SourceKind, &e.SourcePath,
			&e.SessionID, &e.GitBranch, &startedAt, &endedAt, &e.Title, &e.Summary,
			&e.FactsJSON, &e.FilesText, &e.CommandsText, &e.InputTokens,
			&e.OutputTokens, &e.CostUSD, &e.Significance, &e.RecapAttempts,
			&e.PromptVersion); err != nil {
			return nil, unavailable(err)
		}
		e.StartedAt = time.Unix(startedAt, 0).UTC()
		e.EndedAt = time.Unix(endedAt, 0).UTC()
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}

// PutNote pins one fact. Notes are append-only; correcting one means adding a
// newer note, which is also how a human reading the brief sees that a decision
// changed.
func (s *Store) PutNote(ctx context.Context, n NoteRow) error {
	if s == nil || s.db == nil {
		return nil
	}
	if n.ID == "" || n.ProjectPath == "" || strings.TrimSpace(n.Text) == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO memory_notes (id, project_path, kind, text, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			kind = excluded.kind,
			text = excluded.text`,
		n.ID, n.ProjectPath, n.Kind, n.Text, n.CreatedAt.UTC().Unix())
	if err != nil {
		return unavailable(err)
	}
	return nil
}

// ListNotes returns a project's pinned notes, newest first.
func (s *Store) ListNotes(ctx context.Context, projectPath string, limit int) ([]NoteRow, error) {
	if s == nil || s.db == nil || projectPath == "" || limit <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project_path, kind, text, created_at FROM memory_notes
		WHERE project_path = ?
		ORDER BY created_at DESC
		LIMIT ?`, projectPath, limit)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()

	var out []NoteRow
	for rows.Next() {
		var n NoteRow
		var createdAt int64
		if err := rows.Scan(&n.ID, &n.ProjectPath, &n.Kind, &n.Text, &createdAt); err != nil {
			return nil, unavailable(err)
		}
		n.CreatedAt = time.Unix(createdAt, 0).UTC()
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}

// GetIngestState reports how far a transcript file has been parsed.
func (s *Store) GetIngestState(ctx context.Context, sourcePath string) (IngestState, bool, error) {
	var st IngestState
	if s == nil || s.db == nil || sourcePath == "" {
		return st, false, nil
	}
	var updatedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT source_path, project_path, byte_offset, size_seen, updated_at
		FROM memory_ingest_state WHERE source_path = ?`, sourcePath).
		Scan(&st.SourcePath, &st.ProjectPath, &st.ByteOffset, &st.SizeSeen, &updatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return IngestState{}, false, nil
		}
		return IngestState{}, false, unavailable(err)
	}
	st.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return st, true, nil
}

// PutIngestState records how far a transcript file has been parsed.
func (s *Store) PutIngestState(ctx context.Context, st IngestState) error {
	if s == nil || s.db == nil || st.SourcePath == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO memory_ingest_state (source_path, project_path, byte_offset, size_seen, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(source_path) DO UPDATE SET
			project_path = excluded.project_path,
			byte_offset  = excluded.byte_offset,
			size_seen    = excluded.size_seen,
			updated_at   = excluded.updated_at`,
		st.SourcePath, st.ProjectPath, st.ByteOffset, st.SizeSeen, time.Now().UTC().Unix())
	if err != nil {
		return unavailable(err)
	}
	return nil
}

// MemoryStats rolls up one project's memory for the diagnostics tool.
func (s *Store) MemoryStats(ctx context.Context, projectPath string) (MemoryStats, error) {
	var st MemoryStats
	if s == nil || s.db == nil || projectPath == "" {
		return st, nil
	}
	var oldest, newest *int64
	err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN summary <> '' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(cost_usd), 0),
			MIN(started_at),
			MAX(started_at)
		FROM memory_episodes WHERE project_path = ?`, projectPath).
		Scan(&st.Episodes, &st.WithSummary, &st.CapturedCost, &oldest, &newest)
	if err != nil {
		return MemoryStats{}, unavailable(err)
	}
	if oldest != nil {
		st.OldestEpisode = *oldest
	}
	if newest != nil {
		st.NewestEpisode = *newest
	}

	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memory_notes WHERE project_path = ?`, projectPath).
		Scan(&st.Notes); err != nil {
		return MemoryStats{}, unavailable(err)
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memory_ingest_state WHERE project_path = ?`, projectPath).
		Scan(&st.TrackedFiles); err != nil {
		return MemoryStats{}, unavailable(err)
	}
	return st, nil
}

// DeleteEpisode removes one episode and its index entry.
//
// A memory needs a way to forget. Two things call for it: an episode the parser
// has since learned to recognise as noise, and a row a person wants gone.
// Deletion is by key and complete — the FTS delete trigger takes the index with
// it — because a half-forgotten episode that still matched a search would be
// worse than one that was never stored.
func (s *Store) DeleteEpisode(ctx context.Context, key string) error {
	if s == nil || s.db == nil || key == "" {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM memory_episodes WHERE key = ?`, key); err != nil {
		return unavailable(err)
	}
	return nil
}

// SetEpisodeTitle rewrites one episode's title without touching its summary or
// its attempt count.
//
// It exists for presentation fixes — stripping markdown a model added out of
// habit — that would otherwise need a full re-recap to apply. UpdateRecap is
// still the only thing that records an attempt.
func (s *Store) SetEpisodeTitle(ctx context.Context, key, title string) error {
	if s == nil || s.db == nil || key == "" {
		return nil
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE memory_episodes SET title = ? WHERE key = ?`, title, key); err != nil {
		return unavailable(err)
	}
	return nil
}
