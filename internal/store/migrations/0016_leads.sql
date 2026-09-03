-- 0016_leads — the lead ledger (task-63).
--
-- `companies` and `region_searches` (0004) are caches: they expire, and
-- GetRegionSearch deliberately misses the whole region when one member has
-- aged out, because a partial lead list is worse than paying for the search
-- again. That is the right rule for a cache and the wrong one for a record.
-- So the ledger is its own table beside them, and nothing here has a TTL.
--
-- leads: one row per business, ever. `category` is denormalized from
-- company_categorization on purpose — the ledger has to be readable without a
-- taxonomy version in hand, and it is what the operator filters on.
--
-- lead_runs / lead_run_members: which search found which business, and when.
-- Kept separate so a business found by three searches is one row in `leads`
-- and three memberships, and so "what did the Kadıköy run return on the 2nd"
-- is still a question with an answer after the region cache has expired.
-- The membership carries its own `category` because a later taxonomy version
-- rewrites `leads.category`, and a past run's answer should stay readable.
--
-- Migrations are append-only: never edit an applied file, add the next number.

CREATE TABLE IF NOT EXISTS leads (
    place_id        TEXT PRIMARY KEY,
    name            TEXT    NOT NULL DEFAULT '',
    address         TEXT    NOT NULL DEFAULT '',
    latitude        REAL    NOT NULL DEFAULT 0,
    longitude       REAL    NOT NULL DEFAULT 0,
    rating          REAL    NOT NULL DEFAULT 0,
    review_count    INTEGER NOT NULL DEFAULT 0,
    website         TEXT    NOT NULL DEFAULT '',
    phone           TEXT    NOT NULL DEFAULT '',
    primary_type    TEXT    NOT NULL DEFAULT '',
    business_status TEXT    NOT NULL DEFAULT '',
    source          TEXT    NOT NULL DEFAULT '',
    category        TEXT    NOT NULL DEFAULT '',
    category_method TEXT    NOT NULL DEFAULT '',
    first_seen_at   INTEGER NOT NULL,
    last_seen_at    INTEGER NOT NULL
);

-- The two reads that are not a point lookup: the category rail, and the table
-- ordered by when a business was last seen.
CREATE INDEX IF NOT EXISTS idx_leads_category
    ON leads(category, last_seen_at DESC);
CREATE INDEX IF NOT EXISTS idx_leads_seen
    ON leads(last_seen_at DESC);

CREATE TABLE IF NOT EXISTS lead_runs (
    id            TEXT PRIMARY KEY,
    region_key    TEXT    NOT NULL DEFAULT '',
    query         TEXT    NOT NULL DEFAULT '',
    region_label  TEXT    NOT NULL DEFAULT '',
    source        TEXT    NOT NULL DEFAULT '',
    company_count INTEGER NOT NULL DEFAULT 0,
    with_gaps     INTEGER NOT NULL DEFAULT 0,
    with_emails   INTEGER NOT NULL DEFAULT 0,
    ran_at        INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_lead_runs_time ON lead_runs(ran_at DESC);

CREATE TABLE IF NOT EXISTS lead_run_members (
    run_id   TEXT NOT NULL,
    place_id TEXT NOT NULL,
    category TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (run_id, place_id)
);

CREATE INDEX IF NOT EXISTS idx_lead_run_members_place
    ON lead_run_members(place_id);
