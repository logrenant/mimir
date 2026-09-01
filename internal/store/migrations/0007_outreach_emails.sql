-- 0007_outreach_emails — Track B / M6 (task-33).
--
-- One row per (place_id, prompt_version). Stage 4 of internal/leadgen: a
-- Claude-drafted cold-outreach email for one company, written from its
-- category's stage-3 gap analysis.
--
-- status is the human's decision on the draft: 'draft' (generated, untouched),
-- 'sent' (a human sent it — never regenerate), 'skipped' (a human rejected it —
-- do not resurface). A region re-run at the same prompt_version refreshes only
-- rows still in 'draft'; the ON CONFLICT clause in PutOutreachEmail enforces
-- that so a race cannot clobber a 'sent' row.
--
-- prompt_version is config.LeadgenEmailVersion, its own key half independent of
-- the categorization and gap-analysis versions.
--
-- No TTL, same reasoning as 0005/0006: what goes stale is the prompt or the
-- upstream gap analysis, both of which change the key.
--
-- Migrations are append-only: never edit an applied file, add 0008_*.sql.

CREATE TABLE IF NOT EXISTS outreach_emails (
    place_id       TEXT    NOT NULL,
    prompt_version TEXT    NOT NULL,
    email          TEXT    NOT NULL,
    status         TEXT    NOT NULL DEFAULT 'draft',
    truncated      INTEGER NOT NULL DEFAULT 0,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    PRIMARY KEY (place_id, prompt_version)
);
