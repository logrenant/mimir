package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// chatTurnColumns is the single column list every turn read shares.
const chatTurnColumns = `episode_key, chat_session_id, project_path, source_kind,
	started_at, ended_at, user_prompt, assistant_text, tool_calls_json,
	files_json, commands_json, input_tokens, output_tokens, cost_usd`

// ChatTurnRow is one exchange stored verbatim: what was asked, what came back,
// and what the assistant did in between.
//
// Unlike EpisodeRow this is not an index entry. Nothing here is clipped, and
// nothing here is distilled — this is the conversation, and the summary of it
// lives next door in memory_episodes.
type ChatTurnRow struct {
	EpisodeKey    string
	SessionID     string
	ProjectPath   string
	SourceKind    string
	GitBranch     string
	SourcePath    string
	StartedAt     time.Time
	EndedAt       time.Time
	UserPrompt    string
	AssistantText string
	ToolCallsJSON string
	FilesJSON     string
	CommandsJSON  string
	InputTokens   int
	OutputTokens  int
	CostUSD       float64
}

// ChatSessionRow is one conversation: the grouping the transcript file already
// implies, kept as a row so listing conversations is not a scan over every turn.
type ChatSessionRow struct {
	ID          string
	SourceKind  string
	SourcePath  string
	SessionID   string
	ProjectPath string
	GitBranch   string
	Title       string
	StartedAt   time.Time
	EndedAt     time.Time
	TurnCount   int
}

// ChatFilter narrows a session listing.
type ChatFilter struct {
	ProjectPath string
	SourceKind  string
	Limit       int
}

// ChatSessionID is the archive's grouping key: which transcript, and which
// session inside it. Derived rather than random so the same conversation read
// twice groups into one row.
func ChatSessionID(sourceKind, sourcePath, sessionID string) string {
	sum := sha256.Sum256([]byte(sourceKind + "|" + sourcePath + "|" + sessionID))
	return hex.EncodeToString(sum[:])[:32]
}

// PutChatTurn stores one turn and folds it into its session row, in one
// transaction so a listed session can never claim turns the archive does not
// hold.
//
// Keyed by the episode key, which is the same key memory_episodes uses: a
// session still being written is re-read from its stored offset, and its
// trailing turn must update rather than duplicate.
//
// Nil-Store tolerant (SD-6): with no store the ingest is exactly what it was
// before the archive existed.
func (s *Store) PutChatTurn(ctx context.Context, t ChatTurnRow) error {
	if s == nil || s.db == nil || t.EpisodeKey == "" {
		return nil
	}
	// A turn with neither side of the conversation is not a conversation.
	if t.UserPrompt == "" && t.AssistantText == "" {
		return nil
	}

	sessionRowID := ChatSessionID(t.SourceKind, t.SourcePath, t.SessionID)
	started := t.StartedAt.UTC().Unix()
	ended := t.EndedAt.UTC().Unix()
	if ended == 0 {
		ended = started
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return unavailable(err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO chat_turns (`+chatTurnColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(episode_key) DO UPDATE SET
			chat_session_id = excluded.chat_session_id,
			project_path    = excluded.project_path,
			source_kind     = excluded.source_kind,
			started_at      = excluded.started_at,
			ended_at        = excluded.ended_at,
			user_prompt     = excluded.user_prompt,
			assistant_text  = excluded.assistant_text,
			tool_calls_json = excluded.tool_calls_json,
			files_json      = excluded.files_json,
			commands_json   = excluded.commands_json,
			input_tokens    = excluded.input_tokens,
			output_tokens   = excluded.output_tokens,
			cost_usd        = excluded.cost_usd`,
		t.EpisodeKey, sessionRowID, t.ProjectPath, t.SourceKind, started, ended,
		t.UserPrompt, t.AssistantText,
		orJSON(t.ToolCallsJSON), orJSON(t.FilesJSON), orJSON(t.CommandsJSON),
		t.InputTokens, t.OutputTokens, t.CostUSD,
	); err != nil {
		return unavailable(err)
	}

	// The session's span and turn count are recomputed from the turns rather
	// than incremented: an updated turn must not add a second one, and a
	// re-read backlog must not inflate the count on every pass.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO chat_sessions
			(id, source_kind, source_path, session_id, project_path, git_branch,
			 title, started_at, ended_at, turn_count, updated_at)
		SELECT ?, ?, ?, ?, ?, ?, ?,
		       MIN(started_at), MAX(ended_at), COUNT(*), ?
		FROM chat_turns WHERE chat_session_id = ?
		ON CONFLICT(id) DO UPDATE SET
			project_path = excluded.project_path,
			git_branch   = CASE WHEN excluded.git_branch <> '' THEN excluded.git_branch ELSE chat_sessions.git_branch END,
			title        = CASE WHEN chat_sessions.title <> '' THEN chat_sessions.title ELSE excluded.title END,
			started_at   = excluded.started_at,
			ended_at     = excluded.ended_at,
			turn_count   = excluded.turn_count,
			updated_at   = excluded.updated_at`,
		sessionRowID, t.SourceKind, t.SourcePath, t.SessionID, t.ProjectPath,
		t.GitBranch, chatTitle(t.UserPrompt), time.Now().Unix(), sessionRowID,
	); err != nil {
		return unavailable(err)
	}

	return unavailableOrNil(tx.Commit())
}

// ListChatSessions returns conversations, most recently ended first.
func (s *Store) ListChatSessions(ctx context.Context, f ChatFilter) ([]ChatSessionRow, error) {
	if s == nil || s.db == nil || f.Limit <= 0 {
		return nil, nil
	}

	var (
		clauses []string
		args    []any
	)
	if f.ProjectPath != "" {
		clauses = append(clauses, "project_path = ?")
		args = append(args, f.ProjectPath)
	}
	if f.SourceKind != "" {
		clauses = append(clauses, "source_kind = ?")
		args = append(args, f.SourceKind)
	}
	where := ""
	if len(clauses) > 0 {
		where = " WHERE " + strings.Join(clauses, " AND ")
	}
	args = append(args, f.Limit)

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, source_kind, source_path, session_id, project_path,
		       git_branch, title, started_at, ended_at, turn_count
		FROM chat_sessions`+where+`
		ORDER BY ended_at DESC, id
		LIMIT ?`, args...)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()

	var out []ChatSessionRow
	for rows.Next() {
		var (
			c              ChatSessionRow
			started, ended int64
		)
		if err := rows.Scan(&c.ID, &c.SourceKind, &c.SourcePath, &c.SessionID,
			&c.ProjectPath, &c.GitBranch, &c.Title, &started, &ended, &c.TurnCount); err != nil {
			return nil, unavailable(err)
		}
		c.StartedAt = time.Unix(started, 0).UTC()
		c.EndedAt = time.Unix(ended, 0).UTC()
		out = append(out, c)
	}
	return out, unavailableOrNil(rows.Err())
}

// ChatSession returns one conversation's header, or ok=false on a miss.
func (s *Store) ChatSession(ctx context.Context, id string) (ChatSessionRow, bool, error) {
	if s == nil || s.db == nil || id == "" {
		return ChatSessionRow{}, false, nil
	}

	var (
		c              ChatSessionRow
		started, ended int64
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, source_kind, source_path, session_id, project_path,
		       git_branch, title, started_at, ended_at, turn_count
		FROM chat_sessions WHERE id = ?`, id).
		Scan(&c.ID, &c.SourceKind, &c.SourcePath, &c.SessionID, &c.ProjectPath,
			&c.GitBranch, &c.Title, &started, &ended, &c.TurnCount)
	if errors.Is(err, sql.ErrNoRows) {
		return ChatSessionRow{}, false, nil
	}
	if err != nil {
		return ChatSessionRow{}, false, unavailable(err)
	}
	c.StartedAt = time.Unix(started, 0).UTC()
	c.EndedAt = time.Unix(ended, 0).UTC()
	return c, true, nil
}

// ChatTurns returns one conversation's turns in the order they happened.
func (s *Store) ChatTurns(ctx context.Context, sessionRowID string, limit, offset int) ([]ChatTurnRow, error) {
	if s == nil || s.db == nil || sessionRowID == "" || limit <= 0 {
		return nil, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT `+chatTurnColumns+`
		FROM chat_turns
		WHERE chat_session_id = ?
		ORDER BY started_at, episode_key
		LIMIT ? OFFSET ?`, sessionRowID, limit, max(offset, 0))
	if err != nil {
		return nil, unavailable(err)
	}
	return scanChatTurns(rows)
}

// SearchChatTurns is full-text over what was actually said.
//
// The FTS table is never aliased: SQLite resolves MATCH and bm25() against the
// virtual table's real name, and an alias fails with "no such column". The
// query text goes through the same ftsQuery sanitiser the memory uses — an
// unparseable question is a miss, never an error.
func (s *Store) SearchChatTurns(ctx context.Context, query, projectPath string, limit int) ([]ChatTurnRow, error) {
	if s == nil || s.db == nil || limit <= 0 {
		return nil, nil
	}
	match := ftsQuery(query)
	if match == "" {
		return nil, nil
	}

	args := []any{match}
	scope := ""
	if projectPath != "" {
		scope = " AND t.project_path = ?"
		args = append(args, projectPath)
	}
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, `
		SELECT `+prefixed(chatTurnColumns, "t")+`
		FROM chat_fts
		JOIN chat_turns t ON t.rowid = chat_fts.rowid
		WHERE chat_fts MATCH ?`+scope+`
		ORDER BY bm25(chat_fts)
		LIMIT ?`, args...)
	if err != nil {
		return nil, unavailable(err)
	}
	return scanChatTurns(rows)
}

func scanChatTurns(rows *sql.Rows) ([]ChatTurnRow, error) {
	defer func() { _ = rows.Close() }()

	var out []ChatTurnRow
	for rows.Next() {
		var (
			t              ChatTurnRow
			started, ended int64
		)
		if err := rows.Scan(&t.EpisodeKey, &t.SessionID, &t.ProjectPath,
			&t.SourceKind, &started, &ended, &t.UserPrompt, &t.AssistantText,
			&t.ToolCallsJSON, &t.FilesJSON, &t.CommandsJSON,
			&t.InputTokens, &t.OutputTokens, &t.CostUSD); err != nil {
			return nil, unavailable(err)
		}
		t.StartedAt = time.Unix(started, 0).UTC()
		t.EndedAt = time.Unix(ended, 0).UTC()
		out = append(out, t)
	}
	return out, unavailableOrNil(rows.Err())
}

// chatTitle names a conversation by its opening prompt. A name, not a summary:
// nothing in this package calls a model, and a first line is what a person
// scanning a list recognises anyway.
func chatTitle(prompt string) string {
	line := strings.TrimSpace(prompt)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	r := []rune(line)
	if len(r) > 120 {
		return strings.TrimSpace(string(r[:120])) + " …"
	}
	return line
}

func orJSON(s string) string {
	if strings.TrimSpace(s) == "" {
		return "[]"
	}
	return s
}

func unavailableOrNil(err error) error {
	if err == nil {
		return nil
	}
	return unavailable(err)
}
