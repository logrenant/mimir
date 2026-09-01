-- 0002_projects_runs — Phase 2 / M2 (task-20).
--
-- projects: the folder-scoping security boundary for the coding-task runner.
-- coding_runs: one row per `claude` session, created here because migrations
-- are append-only and task-21 populates it.
--
-- Migrations are append-only: never edit an applied file, add the next number.

CREATE TABLE IF NOT EXISTS projects (
    id           TEXT PRIMARY KEY,
    path         TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS coding_runs (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL,
    prompt          TEXT NOT NULL,
    status          TEXT NOT NULL,
    session_id      TEXT NOT NULL DEFAULT '',
    model           TEXT NOT NULL DEFAULT '',
    transcript_path TEXT NOT NULL DEFAULT '',
    cost_usd        REAL NOT NULL DEFAULT 0,
    num_turns       INTEGER NOT NULL DEFAULT 0,
    error           TEXT NOT NULL DEFAULT '',
    started_at      INTEGER NOT NULL,
    ended_at        INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_coding_runs_project ON coding_runs (project_id, started_at);
