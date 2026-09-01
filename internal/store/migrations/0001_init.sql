-- 0001_init — Phase 2 / M1 page cache (task-17).
--
-- Migrations are append-only: never edit an applied file, add 0002_*.sql.

CREATE TABLE IF NOT EXISTS crawl_pages (
    url_hash   TEXT PRIMARY KEY,
    url        TEXT NOT NULL,
    title      TEXT NOT NULL DEFAULT '',
    markdown   TEXT NOT NULL,
    fetched_at INTEGER NOT NULL,
    cached_at  INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_crawl_pages_cached_at ON crawl_pages (cached_at);

CREATE TABLE IF NOT EXISTS refined_pages (
    refine_key     TEXT PRIMARY KEY,
    url            TEXT NOT NULL,
    query          TEXT NOT NULL DEFAULT '',
    max_tokens     INTEGER NOT NULL,
    content_hash   TEXT NOT NULL,
    prompt_version TEXT NOT NULL,
    text           TEXT NOT NULL,
    truncated      INTEGER NOT NULL DEFAULT 0,
    token_estimate INTEGER NOT NULL DEFAULT 0,
    cached_at      INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_refined_pages_cached_at ON refined_pages (cached_at);
