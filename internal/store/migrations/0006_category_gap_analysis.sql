-- 0006_category_gap_analysis — Track B / M6 (task-30).
--
-- One row per (region, category, prompt_version, company_set_hash). Stage 3 of
-- internal/leadgen: a Claude-synthesized paragraph of the gaps and needs common
-- to one category of companies in one region.
--
-- Unlike company_categorization (0005) there is a real content key here.
-- The analysis is a synthesis ACROSS a specific set of companies, so if that
-- set changes the old synthesis no longer describes it. company_set_hash is
-- sha256 over the sorted place_id list; prompt_version is
-- config.LeadgenGapVersion, its own key half independent of
-- LeadgenCategoryVersion.
--
-- No TTL column, same reasoning as 0005: what goes stale is the prompt or the
-- company set, and both are already in the key.
--
-- Migrations are append-only: never edit an applied file, add 0007_*.sql.

CREATE TABLE IF NOT EXISTS category_gap_analysis (
    region           TEXT    NOT NULL,
    category         TEXT    NOT NULL,
    prompt_version   TEXT    NOT NULL,
    company_set_hash TEXT    NOT NULL,
    analysis         TEXT    NOT NULL,
    company_count    INTEGER NOT NULL DEFAULT 0,
    truncated        INTEGER NOT NULL DEFAULT 0,
    created_at       INTEGER NOT NULL,
    PRIMARY KEY (region, category, prompt_version, company_set_hash)
);
