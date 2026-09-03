-- 0018_brain_node_versions — what a file used to mean (task-67).
--
-- Detection already worked: internal/brain/scan.go hashes a file's raw bytes
-- and skips it when the hash matches brain_nodes.content_hash (0014), and the
-- resident scan sweeps the operator's roots every BrainScanIdleInterval — so a
-- file that changes is re-read and re-distilled on the next pass. What was
-- missing is the history. UpsertBrainNode replaces the row, so each assessment
-- overwrote the last and "what did this file mean last week" had no answer.
--
-- One row per distinct content hash a node has carried, appended by
-- UpsertBrainNode itself. Written there rather than by a caller because the old
-- hash is only visible at the upsert, and because every ingest path — the
-- resident scan, mimir-scan, brain_scan_repo, capture — then gets history
-- without a second writer existing. internal/brain/AGENTS.md's "one ingest
-- path" rule is the same rule.
--
-- No file content is stored. A version says "at this moment, at this hash, this
-- file meant that" — the file itself is still on disk, and for anything under
-- version control git already keeps the bytes. Bounded per node by
-- config.BrainVersionsPerNode; the oldest are pruned on insert, because a file
-- edited every minute for a year must not become the largest table here.
--
-- A rowid table on purpose: reverting a file to a previous version is a real
-- event and deserves its own row, so (node_id, content_hash) would be the wrong
-- key.
--
-- Migrations are append-only: never edit an applied file, add the next number.

CREATE TABLE IF NOT EXISTS brain_node_versions (
    node_id        TEXT    NOT NULL,
    content_hash   TEXT    NOT NULL,
    seen_at        INTEGER NOT NULL,
    size_bytes     INTEGER NOT NULL DEFAULT 0,
    modified_at    INTEGER NOT NULL DEFAULT 0,
    title          TEXT    NOT NULL DEFAULT '',
    assessment     TEXT    NOT NULL DEFAULT '',
    tags_json      TEXT    NOT NULL DEFAULT '[]',
    provider       TEXT    NOT NULL DEFAULT '',
    model          TEXT    NOT NULL DEFAULT '',
    prompt_version TEXT    NOT NULL DEFAULT ''
);

-- "this node's history, newest first" is the only read, and the prune uses the
-- same order.
CREATE INDEX IF NOT EXISTS idx_brain_node_versions_node
    ON brain_node_versions(node_id, seen_at DESC);
