-- What the graph actually answered.
--
-- Graphify calls this save-result and reflect: a query records whether it led
-- anywhere, and the outcomes are aggregated into a note about which parts of
-- the graph are worth trusting. The aggregation is deterministic — a weighted
-- count with a half-life, no model call — which is the half worth having.
--
-- The rows are a record, not a cache: nothing here expires on read, and a
-- deleted node leaves its history behind, because "we asked about this and it
-- went nowhere" stays true after the node is gone.
CREATE TABLE IF NOT EXISTS graph_results (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    project_path TEXT NOT NULL DEFAULT '',
    question     TEXT NOT NULL,
    -- The node ids the answer actually cited, as a JSON array. Cited, not
    -- traversed: a walk touches hundreds, and crediting all of them would make
    -- the signal meaningless.
    nodes        TEXT NOT NULL DEFAULT '[]',
    -- useful | dead_end | corrected
    outcome      TEXT NOT NULL,
    -- What the right answer was, when outcome is corrected. The most valuable
    -- row in the table and the one nobody writes unless it is easy to.
    correction   TEXT NOT NULL DEFAULT '',
    at           INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_graph_results_project
    ON graph_results (project_path, at DESC);
