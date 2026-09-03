-- 0017_chat_archive — conversations kept, not just remembered (task-65).
--
-- memory_episodes (0008) is an index entry: a title, a recap, and a few
-- structured facts. The conversation itself lived only in the transcript on
-- disk — ~/.claude/projects/<slug>/*.jsonl, this daemon's own run transcripts,
-- the agy spool — all of which rotate, move, and get deleted. Losing one of
-- those files loses the conversation; what survived was a summary of it.
--
-- chat_turns is that text, stored. One row per turn — a human prompt and the
-- assistant chain that answered it — keyed by the same `episode_key`
-- memory_episodes uses, so one parse feeds both tables and a session that is
-- still growing updates its rows rather than duplicating them.
--
-- This table is deliberately the opposite trade from memory_episodes: it is
-- large, it is never distilled, and no model ever reads it whole. Nothing here
-- costs a model call to write, which is what makes running it over an existing
-- backlog a migration rather than a bill.
--
-- chat_sessions is the grouping the transcript file already implies, kept as a
-- row so listing conversations is not a GROUP BY over every turn ever stored.
--
-- Migrations are append-only: never edit an applied file, add the next number.

CREATE TABLE IF NOT EXISTS chat_sessions (
    id           TEXT PRIMARY KEY,
    source_kind  TEXT    NOT NULL DEFAULT '',
    source_path  TEXT    NOT NULL DEFAULT '',
    session_id   TEXT    NOT NULL DEFAULT '',
    project_path TEXT    NOT NULL DEFAULT '',
    git_branch   TEXT    NOT NULL DEFAULT '',
    title        TEXT    NOT NULL DEFAULT '',
    started_at   INTEGER NOT NULL DEFAULT 0,
    ended_at     INTEGER NOT NULL DEFAULT 0,
    turn_count   INTEGER NOT NULL DEFAULT 0,
    updated_at   INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_chat_sessions_project_time
    ON chat_sessions(project_path, ended_at DESC);

CREATE TABLE IF NOT EXISTS chat_turns (
    episode_key     TEXT PRIMARY KEY,
    chat_session_id TEXT    NOT NULL DEFAULT '',
    project_path    TEXT    NOT NULL DEFAULT '',
    source_kind     TEXT    NOT NULL DEFAULT '',
    started_at      INTEGER NOT NULL DEFAULT 0,
    ended_at        INTEGER NOT NULL DEFAULT 0,
    user_prompt     TEXT    NOT NULL DEFAULT '',
    assistant_text  TEXT    NOT NULL DEFAULT '',
    tool_calls_json TEXT    NOT NULL DEFAULT '[]',
    files_json      TEXT    NOT NULL DEFAULT '[]',
    commands_json   TEXT    NOT NULL DEFAULT '[]',
    input_tokens    INTEGER NOT NULL DEFAULT 0,
    output_tokens   INTEGER NOT NULL DEFAULT 0,
    cost_usd        REAL    NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_chat_turns_session
    ON chat_turns(chat_session_id, started_at);
CREATE INDEX IF NOT EXISTS idx_chat_turns_project_time
    ON chat_turns(project_path, started_at DESC);

CREATE VIRTUAL TABLE IF NOT EXISTS chat_fts USING fts5(
    user_prompt, assistant_text,
    content='chat_turns', content_rowid='rowid'
);

-- External-content FTS5 keeps no copy of its own, so these triggers are the
-- only thing keeping the index truthful. The 'delete' row before each re-insert
-- is required by FTS5's external-content contract: without it an upsert leaves
-- the previous terms matching forever — which here would mean the archive
-- answering with words a conversation no longer contains.
CREATE TRIGGER IF NOT EXISTS chat_turns_ai AFTER INSERT ON chat_turns BEGIN
    INSERT INTO chat_fts(rowid, user_prompt, assistant_text)
    VALUES (new.rowid, new.user_prompt, new.assistant_text);
END;

CREATE TRIGGER IF NOT EXISTS chat_turns_ad AFTER DELETE ON chat_turns BEGIN
    INSERT INTO chat_fts(chat_fts, rowid, user_prompt, assistant_text)
    VALUES ('delete', old.rowid, old.user_prompt, old.assistant_text);
END;

CREATE TRIGGER IF NOT EXISTS chat_turns_au AFTER UPDATE ON chat_turns BEGIN
    INSERT INTO chat_fts(chat_fts, rowid, user_prompt, assistant_text)
    VALUES ('delete', old.rowid, old.user_prompt, old.assistant_text);
    INSERT INTO chat_fts(rowid, user_prompt, assistant_text)
    VALUES (new.rowid, new.user_prompt, new.assistant_text);
END;
