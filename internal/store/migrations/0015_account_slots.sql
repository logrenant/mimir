-- Where a credential slot came from, and which one the daemon's own model
-- calls spend.
--
-- `discovered` marks a slot Mimir found on disk rather than one a human
-- registered. The distinction is not bookkeeping: the filesystem is the
-- authority for those rows — ~/.claude-accounts is the same tree the
-- operator's shell switches between — so forgetting one in the app would
-- promise something the next scan takes back.
--
-- `is_background` is the slot the daemon's refine/distil subprocesses spend.
-- Those are not coding runs and never went through the dispatcher, so before
-- this they quietly spent whichever identity the daemon happened to inherit.
-- At most one row carries it; no row carrying it means the CLI's own slot.

ALTER TABLE accounts ADD COLUMN discovered    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE accounts ADD COLUMN is_background INTEGER NOT NULL DEFAULT 0;

-- Partial, so the zeroes do not collide: exactly one background slot, or none.
CREATE UNIQUE INDEX IF NOT EXISTS idx_accounts_background
    ON accounts (is_background) WHERE is_background = 1;
