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
	tags_json, aliases_json, provider, model, prompt_version, created_at, updated_at`

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
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// BrainEdgeRow is one link. Edges are stored in one direction and read in both.
type BrainEdgeRow struct {
	Src    string
	Dst    string
	Kind   string
	Weight float64
}

// UpsertBrainNode writes a node, replacing the row that shares its identity.
//
// created_at is preserved across an update on purpose: a node re-ingested a
// month later is the same node, and losing when it was first seen would make
// "most recent work" mean "most recently re-scanned".
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

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO brain_nodes (
			id, project_path, kind, source_key, title, assessment, body,
			tags_json, aliases_json, tags_text, aliases_text,
			provider, model, prompt_version, created_at, updated_at
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
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
			updated_at     = excluded.updated_at`,
		n.ID, n.ProjectPath, n.Kind, n.SourceKey, n.Title, n.Assessment, n.Body,
		string(tagsJSON), string(aliasJSON),
		strings.Join(n.Tags, " "), strings.Join(n.Aliases, " "),
		n.Provider, n.Model, n.PromptVersion,
		n.CreatedAt.Unix(), n.UpdatedAt.Unix())
	if err != nil {
		return unavailable(err)
	}
	return nil
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
		&n.PromptVersion, &createdAt, &updatedAt); err != nil {
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
