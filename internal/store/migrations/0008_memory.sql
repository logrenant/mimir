-- 0008_memory — long-lived per-project memory (M8).
--
-- memory_episodes: one row per "iteration" — a user prompt and the assistant
-- chain that answered it — distilled from a Claude Code session transcript
-- (~/.claude/projects/<slug>/*.jsonl) or one of this daemon's own coding-run
-- transcripts. The row carries deterministic facts always, and a short refined
-- summary when the recap profile produced an acceptable one.
--
-- `summary = ''` is a normal, useful state, not a failure: the recap was never
-- attempted yet, or was rejected by refine's degeneration checks. The
-- deterministic columns still make the episode searchable and rankable. This is
-- the whole reason the summary is a column rather than a separate table --
-- there is no version of this schema where a missing recap loses the episode.
--
-- files_text / commands_text are denormalized copies of what facts_json already
-- holds, because FTS5 external-content tables can only index real columns of
-- their content table. facts_json stays the structured source; these two are an
-- index projection and nothing should read them for anything but MATCH.
--
-- Invalidation: config.MemoryPromptVersion. Bumping it makes every stored recap
-- stale, and the ingester re-derives them; the deterministic columns survive.
--
-- Migrations are append-only: never edit an applied file, add the next number.

CREATE TABLE IF NOT EXISTS memory_episodes (
    key            TEXT PRIMARY KEY,
    project_path   TEXT NOT NULL,
    source_kind    TEXT NOT NULL,
    source_path    TEXT NOT NULL,
    session_id     TEXT NOT NULL DEFAULT '',
    git_branch     TEXT NOT NULL DEFAULT '',
    started_at     INTEGER NOT NULL,
    ended_at       INTEGER NOT NULL,
    title          TEXT NOT NULL DEFAULT '',
    summary        TEXT NOT NULL DEFAULT '',
    facts_json     TEXT NOT NULL DEFAULT '{}',
    files_text     TEXT NOT NULL DEFAULT '',
    commands_text  TEXT NOT NULL DEFAULT '',
    input_tokens   INTEGER NOT NULL DEFAULT 0,
    output_tokens  INTEGER NOT NULL DEFAULT 0,
    cost_usd       REAL    NOT NULL DEFAULT 0,
    significance   INTEGER NOT NULL DEFAULT 0,
    recap_attempts INTEGER NOT NULL DEFAULT 0,
    prompt_version TEXT NOT NULL DEFAULT ''
);

-- The brief reads "most recent work in this project"; recall reads
-- "highest-signal episodes still missing a recap". Both are covered here.
CREATE INDEX IF NOT EXISTS idx_memory_episodes_project_time
    ON memory_episodes(project_path, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_memory_episodes_pending_recap
    ON memory_episodes(project_path, significance DESC, started_at DESC);

-- Notes are the one thing in here a human or an agent wrote on purpose rather
-- than had inferred from a transcript, so they are never rewritten by the
-- ingester and never expire.
CREATE TABLE IF NOT EXISTS memory_notes (
    id           TEXT PRIMARY KEY,
    project_path TEXT NOT NULL,
    kind         TEXT NOT NULL,
    text         TEXT NOT NULL,
    created_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_memory_notes_project_time
    ON memory_notes(project_path, created_at DESC);

-- One row per transcript file. byte_offset is how far the parser got; size_seen
-- detects the rewrite/truncate case, where an offset would otherwise point into
-- the middle of a different file.
CREATE TABLE IF NOT EXISTS memory_ingest_state (
    source_path  TEXT PRIMARY KEY,
    project_path TEXT NOT NULL,
    byte_offset  INTEGER NOT NULL DEFAULT 0,
    size_seen    INTEGER NOT NULL DEFAULT 0,
    updated_at   INTEGER NOT NULL
);

CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
    title, summary, files_text, commands_text,
    content='memory_episodes', content_rowid='rowid'
);

-- External-content FTS5 keeps no copy of its own, so these triggers are the
-- only thing keeping the index truthful. The 'delete' row before each re-insert
-- is required by FTS5's external-content contract: without it an upsert leaves
-- the previous terms matching forever.
CREATE TRIGGER IF NOT EXISTS memory_episodes_ai AFTER INSERT ON memory_episodes BEGIN
    INSERT INTO memory_fts(rowid, title, summary, files_text, commands_text)
    VALUES (new.rowid, new.title, new.summary, new.files_text, new.commands_text);
END;

CREATE TRIGGER IF NOT EXISTS memory_episodes_ad AFTER DELETE ON memory_episodes BEGIN
    INSERT INTO memory_fts(memory_fts, rowid, title, summary, files_text, commands_text)
    VALUES ('delete', old.rowid, old.title, old.summary, old.files_text, old.commands_text);
END;

CREATE TRIGGER IF NOT EXISTS memory_episodes_au AFTER UPDATE ON memory_episodes BEGIN
    INSERT INTO memory_fts(memory_fts, rowid, title, summary, files_text, commands_text)
    VALUES ('delete', old.rowid, old.title, old.summary, old.files_text, old.commands_text);
    INSERT INTO memory_fts(rowid, title, summary, files_text, commands_text)
    VALUES (new.rowid, new.title, new.summary, new.files_text, new.commands_text);
END;
