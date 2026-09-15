-- 0025_catalog — the product content studio (task-85).
--
-- Four tables, split along one line: catalog_imports and catalog_products are
-- a **ledger** and catalog_research and catalog_drafts are **caches**. That is
-- the same split 0016_leads draws, for the same reason — a cache may be thrown
-- away and re-earned, a record may not — and here it decides something
-- concrete: a bulk rewrite that stopped halfway must resume, so what it
-- already paid for has to be addressable by a key that is stable across runs.
--
-- catalog_imports carries the whole source file in file_json: the header, the
-- framing (delimiter, encoding, BOM, line ending) and every raw cell of every
-- row, including the columns this system has no name for. That is what makes
-- export lossless — the bytes read are the bytes written back for every cell
-- nobody approved a change for — and re-deriving them from a file the operator
-- may have deleted is not something the daemon will attempt.
--
-- catalog_research and catalog_drafts are keyed by (product_id, version) and
-- their versions are deliberately DIFFERENT shapes:
--
--   research.version = CatalogResearchVersion + "@" + provider/model
--   drafts.version   = CatalogContentVersion  + "@" + provider/model
--                      + ":" + brand hash + ":" + skill version
--
-- The brand hash is absent from the research key on purpose. An operator who
-- corrects the brand voice and re-runs should have every description rewritten
-- and no competitor research thrown away — that research is about the market,
-- not about how this store writes, and paying for it twice is the cost this
-- whole layer exists to avoid.
--
-- catalog_drafts.edited_by_operator is the guard that makes a re-run safe: a
-- draft somebody corrected by hand is never overwritten by another pass. The
-- guard is in the SQL, not in the caller, so a concurrent write cannot slip
-- past it — the same shape as outreach_emails' `WHERE status = 'draft'`.
--
-- Migrations are append-only: never edit an applied file, add the next number.

CREATE TABLE IF NOT EXISTS catalog_imports (
    id            TEXT PRIMARY KEY,
    filename      TEXT    NOT NULL DEFAULT '',
    dialect       TEXT    NOT NULL DEFAULT '',
    product_count INTEGER NOT NULL DEFAULT 0,
    file_json     TEXT    NOT NULL DEFAULT '',
    brand_json    TEXT    NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_catalog_imports_created
    ON catalog_imports (created_at DESC);

CREATE TABLE IF NOT EXISTS catalog_products (
    id           TEXT PRIMARY KEY,
    import_id    TEXT    NOT NULL,
    row_index    INTEGER NOT NULL DEFAULT 0,
    product_key  TEXT    NOT NULL DEFAULT '',
    handle       TEXT    NOT NULL DEFAULT '',
    sku          TEXT    NOT NULL DEFAULT '',
    category     TEXT    NOT NULL DEFAULT '',
    -- product_json is the Product value: its rows, and the original content of
    -- every writable field as it was read.
    product_json TEXT    NOT NULL DEFAULT '',
    status       TEXT    NOT NULL DEFAULT 'pending',
    reason       TEXT    NOT NULL DEFAULT '',
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_catalog_products_import
    ON catalog_products (import_id, row_index);
CREATE INDEX IF NOT EXISTS idx_catalog_products_status
    ON catalog_products (import_id, status);
CREATE INDEX IF NOT EXISTS idx_catalog_products_category
    ON catalog_products (import_id, category);

CREATE TABLE IF NOT EXISTS catalog_research (
    product_id    TEXT NOT NULL,
    version       TEXT NOT NULL,
    findings_json TEXT NOT NULL DEFAULT '',
    sources_json  TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    PRIMARY KEY (product_id, version)
);

CREATE TABLE IF NOT EXISTS catalog_drafts (
    product_id         TEXT    NOT NULL,
    version            TEXT    NOT NULL,
    content_json       TEXT    NOT NULL DEFAULT '',
    fields_json        TEXT    NOT NULL DEFAULT '',
    notes_json         TEXT    NOT NULL DEFAULT '',
    provider           TEXT    NOT NULL DEFAULT '',
    model              TEXT    NOT NULL DEFAULT '',
    edited_by_operator INTEGER NOT NULL DEFAULT 0,
    created_at         INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL,
    PRIMARY KEY (product_id, version)
);
