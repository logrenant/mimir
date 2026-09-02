-- A coding run gains a life before and after the subprocess.
--
-- Until now a run row was created in the `running` state and updated once, at
-- the end. That made two things impossible: a task the operator writes now and
-- runs later, and an honest answer to "is this actually running?" after the
-- daemon restarted. The columns below carry the first; the new `backlog`,
-- `queued` and `stopped` status values (internal/store/runs.go) carry the rest.
--
-- started_at keeps its NOT NULL, but 0 now means "never started" — the same
-- convention ended_at already used.

ALTER TABLE coding_runs ADD COLUMN title       TEXT    NOT NULL DEFAULT '';
ALTER TABLE coding_runs ADD COLUMN created_at  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE coding_runs ADD COLUMN queued_at   INTEGER NOT NULL DEFAULT 0;
ALTER TABLE coding_runs ADD COLUMN attachments TEXT    NOT NULL DEFAULT '';

-- Existing rows were created at the moment they started.
UPDATE coding_runs SET created_at = started_at WHERE created_at = 0;

-- The dispatcher claims the oldest queued run; without this index that claim is
-- a table scan on every completion.
CREATE INDEX IF NOT EXISTS idx_coding_runs_status
    ON coding_runs (status, queued_at);
