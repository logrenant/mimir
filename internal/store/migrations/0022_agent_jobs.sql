-- Every job on one board.
--
-- The table keeps its name. There are 32 references to coding_runs across this
-- package, internal/api and five earlier migrations, and renaming it buys
-- nothing at runtime while colliding with every other line the executor seam
-- touches — the same call internal/store/accounts.go already made about a
-- column it had outgrown.
--
-- DEFAULT 'coding' is not a fallback, it is a fact: every row written before
-- this migration was a claude subprocess. No backfill UPDATE is needed, unlike
-- 0011, which had to invent a created_at.
ALTER TABLE coding_runs ADD COLUMN agent TEXT NOT NULL DEFAULT 'coding';

-- The skills the row was actually run under, as a JSON array of "id:version".
-- A record rather than an input: it answers "which instructions produced this"
-- for a run somebody reads a month later, which the id alone cannot, because
-- the body behind it is the operator's and changes.
ALTER TABLE coding_runs ADD COLUMN skills TEXT NOT NULL DEFAULT '';

-- The executor's own input, as a JSON object. It is written when the card is
-- created, not derived at dispatch: deriving it would put a parser inside the
-- executor, where a failure is a failed card the operator cannot diagnose, and
-- it would make the card uneditable.
ALTER TABLE coding_runs ADD COLUMN params TEXT NOT NULL DEFAULT '';

-- The dispatcher walks the queue per lane once more than one agent exists.
CREATE INDEX IF NOT EXISTS idx_coding_runs_agent
    ON coding_runs (status, agent, queued_at);
