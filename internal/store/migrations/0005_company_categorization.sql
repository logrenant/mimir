-- 0005_company_categorization — Track B / M5 (task-29).
--
-- One row per (company, taxonomy version). The version is
-- config.LeadgenCategoryVersion, which covers both internal/leadgen's rule
-- table and the classify prompt: bumping it re-categorizes everything and
-- leaves the old rows in place, so a taxonomy change is reversible.
--
-- Deliberately no TTL column. Every other cache here expires on a clock
-- because the world moves; a category does not. What goes stale is our
-- taxonomy, and that is a code change, not a timestamp.
--
-- method records which tier answered ('rule' or 'model'), so a later stage can
-- tell a free answer from one that cost tokens. Only resolved answers are
-- written: a company the model failed to classify is left absent, never
-- recorded as 'unknown', or a broken subprocess would become a permanent fact.
--
-- Migrations are append-only: never edit an applied file, add 0006_*.sql.

CREATE TABLE IF NOT EXISTS company_categorization (
    place_id   TEXT    NOT NULL,
    version    TEXT    NOT NULL,
    category   TEXT    NOT NULL,
    method     TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    PRIMARY KEY (place_id, version)
);
