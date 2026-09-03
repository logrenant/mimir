package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// brainNodeColumns is the one column list every node read uses, so a column
// added to the table cannot be picked up by one query and missed by another.
const brainNodeColumns = `id, project_path, kind, source_key, title, assessment, body,
	tags_json, aliases_json, provider, model, prompt_version, content_hash, created_at, updated_at`

// BrainNodeRow is one node.
//
// Tags and Aliases are the structured source; the tags_text/aliases_text
// columns behind them exist only so FTS5 has real columns to index and are
// derived here rather than by the caller.
type BrainNodeRow struct {
	ID            string
	ProjectPath   string
	Kind          string
	SourceKey     string
	Title         string
	Assessment    string
	Body          string
	Tags          []string
	Aliases       []string
	Provider      string
	Model         string
	PromptVersion string

	// ContentHash is the digest of whatever this node was derived from, when
	// that source has stable content. It is the whole reason a repository scan
	// can be run twice without paying twice; empty means "no comparable
	// source", which is the normal case for a session or a commit.
	ContentHash string

	// SizeBytes and ModifiedAt describe the source as the scanner found it.
	// They are carried on the version row, not on the node: what a file weighs
	// now is a fact about the file, and what it weighed then is history.
	SizeBytes  int64
	ModifiedAt time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
}

// BrainNodeVersion is one entry in a node's history: at this moment, at this
// content hash, this is what the source meant.
//
// It holds no file content. The file is still on disk, and for anything under
// version control git already keeps the bytes; what git does not keep is the
// assessment, which is exactly what this row is for.
type BrainNodeVersion struct {
	NodeID        string
	ContentHash   string
	SeenAt        time.Time
	SizeBytes     int64
	ModifiedAt    time.Time
	Title         string
	Assessment    string
	Tags          []string
	Provider      string
	Model         string
	PromptVersion string
}

// BrainEdgeRow is one link. Edges are stored in one direction and read in both.
type BrainEdgeRow struct {
	Src    string
	Dst    string
	Kind   string
	Weight float64
}

// UpsertBrainNode writes a node, replacing the row that shares its identity,
// and appends a version row when the content it was derived from has changed.
//
// created_at is preserved across an update on purpose: a node re-ingested a
// month later is the same node, and losing when it was first seen would make
// "most recent work" mean "most recently re-scanned".
//
// The version is appended here rather than by a caller for two reasons. The old
// hash is only visible at the upsert — a caller would have to read the row
// first and race itself — and every ingest path (the resident scan, mimir-scan,
// brain_scan_repo, the capture loop) then gets history without a second writer
// existing, which is internal/brain/AGENTS.md's "one ingest path" rule applied
// to the store side.
//
// How much history to keep is config.BrainVersionsPerNode, read once at Open.
func (s *Store) UpsertBrainNode(ctx context.Context, n BrainNodeRow) error {
	if s == nil || s.db == nil {
		return unavailable(errors.New("store is not open"))
	}
	if n.ID == "" || n.Kind == "" || n.SourceKey == "" {
		return errors.New("store: brain node needs an id, a kind and a source key")
	}

	tagsJSON, err := json.Marshal(nonNil(n.Tags))
	if err != nil {
		return err
	}
	aliasJSON, err := json.Marshal(nonNil(n.Aliases))
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	if n.CreatedAt.IsZero() {
		n.CreatedAt = now
	}
	if n.UpdatedAt.IsZero() {
		n.UpdatedAt = now
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return unavailable(err)
	}
	defer func() { _ = tx.Rollback() }()

	// Read before writing: after the upsert the old hash is gone, and whether
	// this is a change or a first sighting is the only question history asks.
	var previousHash string
	err = tx.QueryRowContext(ctx, `SELECT content_hash FROM brain_nodes WHERE id = ?`, n.ID).
		Scan(&previousHash)
	seenBefore := true
	if errors.Is(err, sql.ErrNoRows) {
		seenBefore = false
	} else if err != nil {
		return unavailable(err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO brain_nodes (
			id, project_path, kind, source_key, title, assessment, body,
			tags_json, aliases_json, tags_text, aliases_text,
			provider, model, prompt_version, content_hash, created_at, updated_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			title          = excluded.title,
			assessment     = excluded.assessment,
			body           = excluded.body,
			tags_json      = excluded.tags_json,
			aliases_json   = excluded.aliases_json,
			tags_text      = excluded.tags_text,
			aliases_text   = excluded.aliases_text,
			provider       = excluded.provider,
			model          = excluded.model,
			prompt_version = excluded.prompt_version,
			content_hash   = excluded.content_hash,
			updated_at     = excluded.updated_at`,
		n.ID, n.ProjectPath, n.Kind, n.SourceKey, n.Title, n.Assessment, n.Body,
		string(tagsJSON), string(aliasJSON),
		strings.Join(n.Tags, " "), strings.Join(n.Aliases, " "),
		n.Provider, n.Model, n.PromptVersion, n.ContentHash,
		n.CreatedAt.Unix(), n.UpdatedAt.Unix())
	if err != nil {
		return unavailable(err)
	}

	// A node with no comparable source has no history to keep, and a failed
	// distil deliberately arrives with an empty hash so the file is offered
	// again next pass — recording that as a version would write a row saying
	// the file became unreadable.
	if s.brainVersionsPerNode > 0 && n.ContentHash != "" && (!seenBefore || previousHash != n.ContentHash) {
		if err := appendBrainVersion(ctx, tx, n, string(tagsJSON), s.brainVersionsPerNode); err != nil {
			return err
		}
	}

	return unavailableOrNil(tx.Commit())
}

// appendBrainVersion records one entry and prunes the node's oldest.
func appendBrainVersion(ctx context.Context, tx *sql.Tx, n BrainNodeRow, tagsJSON string, keep int) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO brain_node_versions
			(node_id, content_hash, seen_at, size_bytes, modified_at,
			 title, assessment, tags_json, provider, model, prompt_version)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		n.ID, n.ContentHash, n.UpdatedAt.Unix(), n.SizeBytes,
		unixOrZero(n.ModifiedAt), n.Title, n.Assessment, tagsJSON,
		n.Provider, n.Model, n.PromptVersion,
	); err != nil {
		return unavailable(err)
	}

	// A file edited every minute for a year must not become the largest table
	// here. Pruned on insert rather than by a sweeper: this package has no
	// background goroutines, and the writer already holds the transaction.
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM brain_node_versions
		WHERE rowid IN (
			SELECT rowid FROM brain_node_versions
			WHERE node_id = ?
			ORDER BY seen_at DESC, rowid DESC
			LIMIT -1 OFFSET ?
		)`, n.ID, keep); err != nil {
		return unavailable(err)
	}
	return nil
}

// BrainNodeVersions reads a node's history, newest first.
func (s *Store) BrainNodeVersions(ctx context.Context, nodeID string, limit int) ([]BrainNodeVersion, error) {
	if s == nil || s.db == nil || nodeID == "" || limit <= 0 {
		return nil, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT node_id, content_hash, seen_at, size_bytes, modified_at,
		       title, assessment, tags_json, provider, model, prompt_version
		FROM brain_node_versions
		WHERE node_id = ?
		ORDER BY seen_at DESC, rowid DESC
		LIMIT ?`, nodeID, limit)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()

	var out []BrainNodeVersion
	for rows.Next() {
		var (
			v                BrainNodeVersion
			seenAt, modified int64
			tagsJSON         string
		)
		if err := rows.Scan(&v.NodeID, &v.ContentHash, &seenAt, &v.SizeBytes,
			&modified, &v.Title, &v.Assessment, &tagsJSON, &v.Provider,
			&v.Model, &v.PromptVersion); err != nil {
			return nil, unavailable(err)
		}
		v.SeenAt = time.Unix(seenAt, 0).UTC()
		if modified > 0 {
			v.ModifiedAt = time.Unix(modified, 0).UTC()
		}
		// A row written by a future writer with unparseable tags is still a
		// usable version: it loses its vocabulary, not its assessment.
		_ = json.Unmarshal([]byte(tagsJSON), &v.Tags)
		out = append(out, v)
	}
	return out, unavailableOrNil(rows.Err())
}

// BrainNode reads one node by id.
func (s *Store) BrainNode(ctx context.Context, id string) (BrainNodeRow, bool, error) {
	if s == nil || s.db == nil || id == "" {
		return BrainNodeRow{}, false, nil
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+brainNodeColumns+` FROM brain_nodes WHERE id = ?`, id)

	n, err := scanBrainNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return BrainNodeRow{}, false, nil
	}
	if err != nil {
		return BrainNodeRow{}, false, unavailable(err)
	}
	return n, true, nil
}

// BrainNodesByIDs reads a set of nodes in one round trip, for resolving the
// far side of a neighbour list.
func (s *Store) BrainNodesByIDs(ctx context.Context, ids []string) ([]BrainNodeRow, error) {
	if s == nil || s.db == nil || len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+brainNodeColumns+` FROM brain_nodes WHERE id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, unavailable(err)
	}
	return scanBrainNodes(rows)
}

// SearchBrainNodes runs a full-text search, best match first.
//
// Scope is "this project plus everything global": a node about a public
// repository is not about any one checkout, and hiding it from every project
// would be the same as not storing it.
//
// An unparseable query is a miss, never an error — the consumer asked a
// question, and "nothing matched" is a truthful answer where an error would
// imply the index is broken.
func (s *Store) SearchBrainNodes(ctx context.Context, projectPath, query string, limit int) ([]BrainNodeRow, error) {
	if s == nil || s.db == nil || limit <= 0 {
		return nil, nil
	}
	match := ftsQuery(query)
	if match == "" {
		return nil, nil
	}

	// The FTS table is not aliased on purpose: SQLite resolves `MATCH` and
	// `bm25()` against the virtual table's real name, and an alias makes both
	// fail with "no such column".
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+prefixed(brainNodeColumns, "n")+`
		FROM brain_fts
		JOIN brain_nodes n ON n.rowid = brain_fts.rowid
		WHERE brain_fts MATCH ? AND (n.project_path = ? OR n.project_path = '')
		ORDER BY bm25(brain_fts)
		LIMIT ?`, match, projectPath, limit)
	if err != nil {
		return nil, unavailable(err)
	}
	return scanBrainNodes(rows)
}

// UpsertBrainEdges writes links, replacing the weight of any that already
// exist. It is one transaction so a partially-linked node is not a state the
// reader can observe.
func (s *Store) UpsertBrainEdges(ctx context.Context, edges []BrainEdgeRow) error {
	if s == nil || s.db == nil || len(edges) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return unavailable(err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC().Unix()
	for _, e := range edges {
		if e.Src == "" || e.Dst == "" || e.Src == e.Dst || e.Kind == "" {
			continue
		}
		// One direction only, chosen deterministically, so the same pair
		// discovered from either end is one row rather than two.
		src, dst := e.Src, e.Dst
		if src > dst {
			src, dst = dst, src
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO brain_edges (src, dst, kind, weight, created_at)
			VALUES (?,?,?,?,?)
			ON CONFLICT(src, dst, kind) DO UPDATE SET weight = excluded.weight`,
			src, dst, e.Kind, e.Weight, now); err != nil {
			return unavailable(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return unavailable(err)
	}
	return nil
}

// BrainNeighbors returns the links touching id, strongest first, with Src
// always set to id so the caller reads Dst as "the other end" without caring
// which direction the row was stored in.
func (s *Store) BrainNeighbors(ctx context.Context, id string, limit int) ([]BrainEdgeRow, error) {
	if s == nil || s.db == nil || id == "" || limit <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT src, dst, kind, weight FROM brain_edges
		WHERE src = ? OR dst = ?
		ORDER BY weight DESC
		LIMIT ?`, id, id, limit)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()

	var out []BrainEdgeRow
	for rows.Next() {
		var e BrainEdgeRow
		if err := rows.Scan(&e.Src, &e.Dst, &e.Kind, &e.Weight); err != nil {
			return nil, unavailable(err)
		}
		if e.Dst == id {
			e.Src, e.Dst = e.Dst, e.Src
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}

// CountBrainNodes reports how many nodes a project can see, for diagnostics.
func (s *Store) CountBrainNodes(ctx context.Context, projectPath string) (int, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM brain_nodes WHERE project_path = ? OR project_path = ''`,
		projectPath).Scan(&n)
	if err != nil {
		return 0, unavailable(err)
	}
	return n, nil
}

// nonNil keeps a nil slice from marshalling to `null`, so the JSON column
// always holds a list and a reader never has to handle two empty shapes.
func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

type scannable interface{ Scan(...any) error }

func scanBrainNode(row scannable) (BrainNodeRow, error) {
	var (
		n                    BrainNodeRow
		tagsJSON, aliasJSON  string
		createdAt, updatedAt int64
	)
	if err := row.Scan(&n.ID, &n.ProjectPath, &n.Kind, &n.SourceKey, &n.Title,
		&n.Assessment, &n.Body, &tagsJSON, &aliasJSON, &n.Provider, &n.Model,
		&n.PromptVersion, &n.ContentHash, &createdAt, &updatedAt); err != nil {
		return BrainNodeRow{}, err
	}
	// A malformed list is treated as an empty one: the node's own text is
	// still worth returning, and failing the whole read over a tag column
	// would make one bad row hide every good one.
	_ = json.Unmarshal([]byte(tagsJSON), &n.Tags)
	_ = json.Unmarshal([]byte(aliasJSON), &n.Aliases)
	n.CreatedAt = time.Unix(createdAt, 0).UTC()
	n.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return n, nil
}

func scanBrainNodes(rows *sql.Rows) ([]BrainNodeRow, error) {
	defer func() { _ = rows.Close() }()

	var out []BrainNodeRow
	for rows.Next() {
		n, err := scanBrainNode(rows)
		if err != nil {
			return nil, unavailable(err)
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}

// --- capture cursors ---------------------------------------------------------

// BrainCursor reads how far a capture pass got. A missing cursor is the empty
// string, which every caller treats as "start from the beginning" — an absent
// cursor and a fresh one are the same thing and neither is an error.
func (s *Store) BrainCursor(ctx context.Context, key string) (string, error) {
	if s == nil || s.db == nil || key == "" {
		return "", nil
	}
	var cursor string
	err := s.db.QueryRowContext(ctx,
		`SELECT cursor FROM brain_capture_state WHERE key = ?`, key).Scan(&cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", unavailable(err)
	}
	return cursor, nil
}

// SetBrainCursor records how far a capture pass got.
func (s *Store) SetBrainCursor(ctx context.Context, key, projectPath, cursor string) error {
	if s == nil || s.db == nil || key == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO brain_capture_state (key, project_path, cursor, updated_at)
		VALUES (?,?,?,?)
		ON CONFLICT(key) DO UPDATE SET
			cursor     = excluded.cursor,
			updated_at = excluded.updated_at`,
		key, projectPath, cursor, time.Now().UTC().Unix())
	if err != nil {
		return unavailable(err)
	}
	return nil
}

// BrainNodeHashes returns the content hashes already stored for a project at a
// given prompt version, keyed by source. It is one query rather than one lookup
// per file because a scan asks about every path in a repository, and several
// hundred round trips is the difference between a scan that feels instant on a
// second run and one that does not.
func (s *Store) BrainNodeHashes(ctx context.Context, projectPath, kind, promptVersion string) (map[string]string, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT source_key, content_hash FROM brain_nodes
		WHERE project_path = ? AND kind = ? AND prompt_version = ? AND content_hash <> ''`,
		projectPath, kind, promptVersion)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]string)
	for rows.Next() {
		var key, hash string
		if err := rows.Scan(&key, &hash); err != nil {
			return nil, unavailable(err)
		}
		out[key] = hash
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}

// --- the graph ---------------------------------------------------------------
//
// Three reads that exist for one screen: the force-directed picture of what
// Brain knows. None of them touches brain_fts, which is worth saying out loud
// because the obvious next request — "let me filter the graph by a search term"
// — is exactly where somebody joins the virtual table and aliases it, and an
// aliased FTS5 table fails with "no such column" the moment bm25() is involved.

// BrainNodeDegree is one node's id and how many edges touch it. Only the graph
// query computes it, so it is not a field on BrainNodeRow.
type BrainNodeDegree struct {
	ID     string
	Degree int
}

// BrainProjectCount is one project's share of the graph.
type BrainProjectCount struct {
	ProjectPath string
	Nodes       int
	Files       int
	UpdatedAt   time.Time
}

// BrainGraphIDs returns the nodes worth drawing, most connected first.
//
// Ranked by degree rather than by recency, and that is the whole decision.
// After a machine-wide sweep the newest few hundred nodes are a few hundred
// files from whichever project was scanned last — a picture of the scan order,
// not of the brain. Worse, a recency cut slices through the edge set, so most
// of what survives arrives as unconnected dots. Degree keeps the hubs and what
// hangs off them, which is the structure a force layout exists to show, and
// cutting the tail removes leaves instead.
//
// The scope is this project plus the global nodes, the same rule
// SearchBrainNodes uses: a node about a public repository is not about any one
// checkout, and hiding it is the same as not having stored it.
func (s *Store) BrainGraphIDs(ctx context.Context, projectPath string, limit int) ([]BrainNodeDegree, error) {
	if s == nil || s.db == nil || limit <= 0 {
		return nil, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT n.id, COALESCE(d.degree, 0) AS degree
		FROM brain_nodes n
		LEFT JOIN (
			SELECT node, count(*) AS degree FROM (
				SELECT src AS node FROM brain_edges
				UNION ALL
				SELECT dst AS node FROM brain_edges
			)
			GROUP BY node
		) d ON d.node = n.id
		WHERE (? = '' OR n.project_path = ? OR n.project_path = '')
		ORDER BY degree DESC, n.updated_at DESC, n.id
		LIMIT ?`, projectPath, projectPath, limit)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()

	var out []BrainNodeDegree
	for rows.Next() {
		var d BrainNodeDegree
		if err := rows.Scan(&d.ID, &d.Degree); err != nil {
			return nil, unavailable(err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}

// brainEdgeChunk is how many ids go into one IN clause. The driver's own
// ceiling is far higher, but chunking here means the SQL does not depend on a
// build-time constant of the driver we happen to link.
const brainEdgeChunk = 400

// BrainEdgesAmong returns only the edges whose *both* endpoints are in ids.
//
// The invariant lives here rather than in the caller because this is the layer
// that can guarantee it. A force layout handed an edge to a node it was never
// given either invents a phantom node or throws, and neither is something a UI
// should have to defend against.
func (s *Store) BrainEdgesAmong(ctx context.Context, ids []string, limit int) ([]BrainEdgeRow, error) {
	if s == nil || s.db == nil || len(ids) == 0 || limit <= 0 {
		return nil, nil
	}

	want := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		want[id] = struct{}{}
	}

	seen := make(map[string]struct{}, limit)
	var out []BrainEdgeRow

	for start := 0; start < len(ids); start += brainEdgeChunk {
		end := start + brainEdgeChunk
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]

		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, 0, len(chunk)*2+1)
		for _, id := range chunk {
			args = append(args, id)
		}
		for _, id := range chunk {
			args = append(args, id)
		}
		args = append(args, limit)

		rows, err := s.db.QueryContext(ctx, `
			SELECT src, dst, kind, weight FROM brain_edges
			WHERE src IN (`+placeholders+`) OR dst IN (`+placeholders+`)
			ORDER BY weight DESC
			LIMIT ?`, args...)
		if err != nil {
			return nil, unavailable(err)
		}

		for rows.Next() {
			var e BrainEdgeRow
			if err := rows.Scan(&e.Src, &e.Dst, &e.Kind, &e.Weight); err != nil {
				_ = rows.Close()
				return nil, unavailable(err)
			}
			// One endpoint matched the chunk; the other has to be in the whole
			// set, not just this chunk.
			if _, ok := want[e.Src]; !ok {
				continue
			}
			if _, ok := want[e.Dst]; !ok {
				continue
			}
			key := e.Src + "\x00" + e.Dst + "\x00" + e.Kind
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, e)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, unavailable(err)
		}
		_ = rows.Close()

		if len(out) >= limit {
			return out[:limit], nil
		}
	}
	return out, nil
}

// BrainProjects lists what Brain knows, by project.
//
// The empty project_path row is kept rather than filtered: it is the global
// scope, and dropping it would hide every node that is not about one checkout.
func (s *Store) BrainProjects(ctx context.Context) ([]BrainProjectCount, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT project_path,
		       count(*)                                       AS nodes,
		       sum(CASE WHEN kind = 'file' THEN 1 ELSE 0 END) AS files,
		       max(updated_at)                                AS updated_at
		FROM brain_nodes
		GROUP BY project_path
		ORDER BY nodes DESC, project_path`)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()

	var out []BrainProjectCount
	for rows.Next() {
		var (
			p       BrainProjectCount
			updated int64
		)
		if err := rows.Scan(&p.ProjectPath, &p.Nodes, &p.Files, &updated); err != nil {
			return nil, unavailable(err)
		}
		p.UpdatedAt = time.Unix(updated, 0).UTC()
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}
