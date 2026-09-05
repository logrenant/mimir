-- Why the coding pipeline stopped, and when it may spend a token again.
--
-- A spent token budget is the one interruption the operator can neither fix nor
-- retry their way out of: the work is fine, the account simply has nothing left
-- until the window rolls over. Before this table that moment was a `failed`
-- card with a message nobody could act on, and the queue behind it sat still
-- until somebody noticed and pressed something.
--
-- Durable rather than an in-memory ring like the brain scan's console, because
-- the whole point is the gap between the two ends of it: the run that ran out
-- and the pump that picks the queue back up can be hours and a daemon restart
-- apart, and an operator asking the next morning what happened overnight is
-- asking about rows, not about a buffer that died with the process.
--
-- One row per moment, never updated:
--   'run'      a run was cut off mid-task and put back in the queue
--   'dispatch' a queued task could not be claimed, the budget is still spent
--   'resumed'  the window reset and the queue was pumped again
CREATE TABLE IF NOT EXISTS rate_limit_log (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    at         INTEGER NOT NULL,
    phase      TEXT    NOT NULL,
    account_id TEXT    NOT NULL DEFAULT '',
    run_id     TEXT    NOT NULL DEFAULT '',
    resets_at  INTEGER NOT NULL DEFAULT 0,
    detail     TEXT    NOT NULL DEFAULT ''
);

-- The only read is "the most recent N", which is what a console shows.
CREATE INDEX IF NOT EXISTS idx_rate_limit_log_at ON rate_limit_log (at DESC, id DESC);
