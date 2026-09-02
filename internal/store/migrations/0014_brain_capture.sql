-- 0014_brain_capture — what Brain records on its own (task-45).
--
-- Two additions, both about not doing work twice.
--
-- content_hash lets a re-scan skip a node whose source has not changed. Without
-- it, running a repository scan a second time re-distils every file — which is
-- the difference between a scan people run when a repo changes and one they run
-- once and then avoid (task-47). It is empty for nodes whose source has no
-- stable content to hash, which is most of them.
--
-- brain_capture_state is one cursor per (kind, project). The capture loop is
-- resumable and idempotent, and a daemon restart in the middle of promoting a
-- backlog must not start it over or, worse, promote the same episodes again.
-- One flat table rather than three because the three cursors have identical
-- shape and different names, and three tables would be three sets of the same
-- SQL.
--
-- Migrations are append-only: never edit an applied file, add the next number.

ALTER TABLE brain_nodes ADD COLUMN content_hash TEXT NOT NULL DEFAULT '';

-- "which nodes are stale for this prompt version" is the scan's only non-lookup
-- read, and it is answered per project.
CREATE INDEX IF NOT EXISTS idx_brain_nodes_hash
    ON brain_nodes(project_path, content_hash);

CREATE TABLE IF NOT EXISTS brain_capture_state (
    -- '<kind>:<project_path>', e.g. 'episodes:/Users/x/repo' or 'git:/Users/x/repo'.
    -- The kind is in the key rather than a column because nothing ever queries
    -- across kinds; every read is a point lookup for one cursor.
    key          TEXT PRIMARY KEY,
    project_path TEXT NOT NULL DEFAULT '',
    cursor       TEXT NOT NULL DEFAULT '',
    updated_at   INTEGER NOT NULL
);
