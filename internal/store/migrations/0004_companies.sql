-- 0004_companies — Track B / M4 (task-23).
--
-- companies: one row per Google place_id, normalized from internal/maps.
-- Longer-lived than a page cache: a business's name, address and phone move
-- on a scale of months, so the caller passes a long TTL here. Cost, not
-- freshness, is the constraint — every miss is a billed Places request.
--
-- region_searches: one row per search (internal/maps Query.Key()), holding the
-- ordered place_id list that search returned. Kept separate from companies so
-- an empty result set is still a recordable cache HIT — "there are no dentists
-- in this square kilometre" is an answer worth not paying for twice — and so a
-- company found by two different searches is stored once.
--
-- Migrations are append-only: never edit an applied file, add 0005_*.sql.

CREATE TABLE IF NOT EXISTS companies (
    place_id          TEXT PRIMARY KEY,
    name              TEXT    NOT NULL DEFAULT '',
    formatted_address TEXT    NOT NULL DEFAULT '',
    latitude          REAL    NOT NULL DEFAULT 0,
    longitude         REAL    NOT NULL DEFAULT 0,
    rating            REAL    NOT NULL DEFAULT 0,
    review_count      INTEGER NOT NULL DEFAULT 0,
    website           TEXT    NOT NULL DEFAULT '',
    phone             TEXT    NOT NULL DEFAULT '',
    types             TEXT    NOT NULL DEFAULT '[]',
    primary_type      TEXT    NOT NULL DEFAULT '',
    business_status   TEXT    NOT NULL DEFAULT '',
    source            TEXT    NOT NULL DEFAULT '',
    fetched_at        INTEGER NOT NULL,
    cached_at         INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS region_searches (
    region_key TEXT PRIMARY KEY,
    query      TEXT    NOT NULL DEFAULT '',
    place_ids  TEXT    NOT NULL DEFAULT '[]',
    cached_at  INTEGER NOT NULL
);
