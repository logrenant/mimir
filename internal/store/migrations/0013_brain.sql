-- 0013_brain — the node core (task-41).
--
-- A node is one thing worth remembering across sessions and across models: a
-- session, a repository, a piece of research, a note, a file, a commit. It
-- carries the deterministic facts always, and a distilled assessment plus tags
-- when the distil profile produced an acceptable one.
--
-- Why rows and not files. The layer this replaces kept one Markdown file per
-- node and rewrote its neighbours in place on every ingest. docs/ROADMAP.md
-- §B.9 records why that shape is not allowed here: goat v1 kept its memory as a
-- document a model rewrote, one rewrite degenerated, and because the document
-- *was* the memory the damage was permanent. Rows make that unreachable — a
-- rejected distil costs one assessment and nothing else, and no ingest ever
-- rewrites a neighbour's content.
--
-- `assessment = ''` is therefore a normal state, not a failure: the distil was
-- not attempted yet, or was rejected by the validator. The node is still
-- findable by title, tags and body, and still linkable.
--
-- Identity is (project_path, kind, source_key), not a timestamp. The previous
-- id was salted with UnixNano, so ingesting one repository twice produced two
-- unrelated nodes that then linked to each other. Re-ingesting a source must
-- update one row.
--
-- project_path = '' means global — a node that is not about any one checkout
-- (a public repository, say). Everything else is scoped the way memory_episodes
-- is, because "which project is this" has exactly one answer per row.
--
-- tags_text / aliases_text are denormalized copies of what tags_json and
-- aliases_json already hold, for the same reason memory_episodes has
-- files_text: FTS5 external-content tables can only index real columns of their
-- content table. The JSON columns stay the structured source; nothing should
-- read the _text pair for anything but MATCH.
--
-- aliases are what stands in for a vector index (ROADMAP §A.4 stays parked):
-- the distil writes each node's synonyms, abbreviations and adjacent terms into
-- the index, so bm25 can match a query whose words appear nowhere in the node.
--
-- Invalidation: config.BrainPromptVersion.
--
-- Migrations are append-only: never edit an applied file, add the next number.

CREATE TABLE IF NOT EXISTS brain_nodes (
    id             TEXT PRIMARY KEY,
    project_path   TEXT NOT NULL DEFAULT '',
    kind           TEXT NOT NULL,
    source_key     TEXT NOT NULL,
    title          TEXT NOT NULL DEFAULT '',
    assessment     TEXT NOT NULL DEFAULT '',
    body           TEXT NOT NULL DEFAULT '',
    tags_json      TEXT NOT NULL DEFAULT '[]',
    aliases_json   TEXT NOT NULL DEFAULT '[]',
    tags_text      TEXT NOT NULL DEFAULT '',
    aliases_text   TEXT NOT NULL DEFAULT '',
    provider       TEXT NOT NULL DEFAULT '',
    model          TEXT NOT NULL DEFAULT '',
    prompt_version TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    UNIQUE (project_path, kind, source_key)
);

-- "the newest nodes in this project" and "this project's nodes of one kind"
-- are the two reads that are not full-text.
CREATE INDEX IF NOT EXISTS idx_brain_nodes_project_time
    ON brain_nodes(project_path, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_brain_nodes_project_kind
    ON brain_nodes(project_path, kind, updated_at DESC);

-- Edges are stored once, in one direction, and read with (src = ? OR dst = ?).
-- Storing both directions is what forced the previous layer to rewrite a
-- neighbour's file on every ingest, which is where its lost-edge race lived.
--
-- kind: 'tag' (shared vocabulary, free), 'semantic' (one relation pass over the
-- FTS candidates), 'provenance' (a node derived from another).
CREATE TABLE IF NOT EXISTS brain_edges (
    src        TEXT NOT NULL,
    dst        TEXT NOT NULL,
    kind       TEXT NOT NULL,
    weight     REAL NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (src, dst, kind)
);
CREATE INDEX IF NOT EXISTS idx_brain_edges_dst ON brain_edges(dst, weight DESC);
CREATE INDEX IF NOT EXISTS idx_brain_edges_src ON brain_edges(src, weight DESC);

CREATE VIRTUAL TABLE IF NOT EXISTS brain_fts USING fts5(
    title, assessment, tags_text, aliases_text, body,
    content='brain_nodes', content_rowid='rowid'
);

-- External-content FTS5 keeps no copy of its own, so these triggers are the
-- only thing keeping the index truthful. The 'delete' row before each re-insert
-- is required by FTS5's external-content contract: without it an upsert leaves
-- the previous terms matching forever.
CREATE TRIGGER IF NOT EXISTS brain_nodes_ai AFTER INSERT ON brain_nodes BEGIN
    INSERT INTO brain_fts(rowid, title, assessment, tags_text, aliases_text, body)
    VALUES (new.rowid, new.title, new.assessment, new.tags_text, new.aliases_text, new.body);
END;

CREATE TRIGGER IF NOT EXISTS brain_nodes_ad AFTER DELETE ON brain_nodes BEGIN
    INSERT INTO brain_fts(brain_fts, rowid, title, assessment, tags_text, aliases_text, body)
    VALUES ('delete', old.rowid, old.title, old.assessment, old.tags_text, old.aliases_text, old.body);
END;

CREATE TRIGGER IF NOT EXISTS brain_nodes_au AFTER UPDATE ON brain_nodes BEGIN
    INSERT INTO brain_fts(brain_fts, rowid, title, assessment, tags_text, aliases_text, body)
    VALUES ('delete', old.rowid, old.title, old.assessment, old.tags_text, old.aliases_text, old.body);
    INSERT INTO brain_fts(rowid, title, assessment, tags_text, aliases_text, body)
    VALUES (new.rowid, new.title, new.assessment, new.tags_text, new.aliases_text, new.body);
END;
