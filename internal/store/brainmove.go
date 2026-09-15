package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Moving and forgetting a project.
//
// Neither existed before, and their absence was a bug with a shape: a folder
// that was renamed left its old self in the knowledge base forever, being
// re-recorded every five minutes from transcripts that still named it, with no
// way to say so. There was no `DELETE FROM brain_nodes` anywhere in this
// repository.
//
// Identity is why this is not one UPDATE. A node's id is
// sha256(project_path|kind|source_key) — see brain.NodeID — so moving a project
// changes every id in it, and every edge that named one. The mapping is
// computed by internal/brain, which owns that rule; this file is the
// transaction that applies it.

// tablesWithProjectPath are every table that files a row under a project.
//
// Listed rather than discovered, so adding a table is a decision somebody makes
// here rather than a silent omission that leaves rows behind. brain_nodes is
// not among them: its rows move by id, above.
var tablesWithProjectPath = []string{
	"memory_episodes",
	"memory_ingest_state",
	"memory_notes",
	"chat_sessions",
	"chat_turns",
}

// MoveResult says what a move did.
type MoveResult struct {
	// Nodes is how many kept their content and took a new identity.
	Nodes int
	// Merged is how many found a node already at the destination and were
	// folded into it — the same file, recorded twice under two paths.
	Merged int
	// Edges is how many links were re-pointed. A move must not lose one.
	Edges int
	// Rows is what moved in the tables beside the graph: episodes, chat turns,
	// ingest offsets.
	Rows int
}

// NodeIdentity is the rule that turns a project path, a kind and a source key
// into a node id. internal/brain owns it (brain.NodeID); this package is handed
// it rather than knowing it, so there is still exactly one definition of what a
// node's identity is.
type NodeIdentity func(projectPath, kind, sourceKey string) string

// MoveBrainProject re-files everything under `from` as being under `to`.
//
// One transaction, all of it, and the new ids are computed inside it. A
// half-moved project would be a graph with edges pointing at nodes that no
// longer exist and a screen showing the same file twice — and unlike a failed
// scan, there would be no later pass that repaired it.
func (s *Store) MoveBrainProject(ctx context.Context, from, to string, identity NodeIdentity) (MoveResult, error) {
	var out MoveResult
	if s == nil || s.db == nil {
		return out, unavailable(errors.New("store is not open"))
	}
	if from == "" || to == "" {
		return out, errors.New("store: a move needs both a source and a destination path")
	}
	if from == to {
		return out, errors.New("store: the source and the destination are the same path")
	}
	if identity == nil {
		return out, errors.New("store: a move needs the identity rule")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, unavailable(err)
	}
	defer func() { _ = tx.Rollback() }()

	ids, err := remapping(ctx, tx, from, to, identity)
	if err != nil {
		return out, err
	}
	if len(ids) == 0 {
		// Nothing under that path. Not an error: the tables beside the graph
		// may still have rows, and the caller asked for those to move too.
		out.Rows, err = movePathColumns(ctx, tx, from, to)
		if err != nil {
			return out, err
		}
		if err := tx.Commit(); err != nil {
			return out, unavailable(err)
		}
		return out, nil
	}

	// The edges come out first and go back in last. Rewriting them in place is
	// not possible: an edge's primary key is (src, dst, kind) and both ends are
	// about to change, so an UPDATE would collide with rows it has not reached
	// yet.
	edges, err := edgesTouching(ctx, tx, ids)
	if err != nil {
		return out, err
	}
	if err := deleteEdgesTouching(ctx, tx, ids); err != nil {
		return out, err
	}

	for old, next := range ids {
		var exists int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM brain_nodes WHERE id = ?`, next).Scan(&exists); err != nil {
			return out, unavailable(err)
		}

		if exists > 0 {
			// The destination already knows this file. Its row wins — it is the
			// one the scan has been keeping current — and the old row's history
			// is kept, because a reading from before the move is still a
			// reading of the same file.
			if _, err := tx.ExecContext(ctx,
				`UPDATE brain_node_versions SET node_id = ? WHERE node_id = ?`, next, old); err != nil {
				return out, unavailable(err)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM brain_nodes WHERE id = ?`, old); err != nil {
				return out, unavailable(err)
			}
			out.Merged++
			continue
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE brain_nodes SET id = ?, project_path = ? WHERE id = ?`, next, to, old); err != nil {
			return out, unavailable(err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE brain_node_versions SET node_id = ? WHERE node_id = ?`, next, old); err != nil {
			return out, unavailable(err)
		}
		out.Nodes++
	}

	written, err := reinsertEdges(ctx, tx, edges, ids)
	if err != nil {
		return out, err
	}
	out.Edges = written

	rows, err := movePathColumns(ctx, tx, from, to)
	if err != nil {
		return out, err
	}
	out.Rows = rows

	// The capture cursors are dropped rather than carried. A cursor says how
	// far this project's history has been promoted, and the destination has its
	// own; keeping both would need a rule for which is further along, when
	// re-reading is idempotent and costs one pass. brain_capture_state keys are
	// '<kind>:<project_path>', so the path is in the key as well as the column.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM brain_capture_state WHERE project_path = ? OR key LIKE '%:' || ?`,
		from, from); err != nil {
		return out, unavailable(err)
	}

	if err := tx.Commit(); err != nil {
		return out, unavailable(err)
	}
	return out, nil
}

// ForgetBrainProject removes a project and everything filed under it.
//
// Irreversible, and deliberately not clever: no archive, no tombstone, no
// "deleted" flag that every later query has to remember to filter. The
// transcripts on disk are untouched — they are Claude Code's, not ours — so the
// one thing this cannot do is lose the source.
func (s *Store) ForgetBrainProject(ctx context.Context, path string) (int, error) {
	if s == nil || s.db == nil {
		return 0, unavailable(errors.New("store is not open"))
	}
	if path == "" {
		return 0, errors.New("store: forgetting needs a project path")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, unavailable(err)
	}
	defer func() { _ = tx.Rollback() }()

	// Edges first, while the nodes they name are still there to be found.
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM brain_edges
		WHERE src IN (SELECT id FROM brain_nodes WHERE project_path = ?)
		   OR dst IN (SELECT id FROM brain_nodes WHERE project_path = ?)`,
		path, path); err != nil {
		return 0, unavailable(err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM brain_node_versions
		WHERE node_id IN (SELECT id FROM brain_nodes WHERE project_path = ?)`, path); err != nil {
		return 0, unavailable(err)
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM brain_nodes WHERE project_path = ?`, path)
	if err != nil {
		return 0, unavailable(err)
	}
	removed, _ := res.RowsAffected()

	for _, table := range tablesWithProjectPath {
		if _, err := tx.ExecContext(ctx,
			fmt.Sprintf(`DELETE FROM %s WHERE project_path = ?`, table), path); err != nil {
			return 0, unavailable(err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM brain_capture_state WHERE project_path = ? OR key LIKE '%:' || ?`,
		path, path); err != nil {
		return 0, unavailable(err)
	}

	if err := tx.Commit(); err != nil {
		return 0, unavailable(err)
	}
	return int(removed), nil
}

// --- the pieces -------------------------------------------------------------

// remapping reads the project's nodes and works out what each one's id becomes.
//
// Read inside the transaction rather than handed in, so nothing can have been
// written between deciding the mapping and applying it — a scan writing one
// more file mid-move would otherwise leave that file behind under the old path.
func remapping(ctx context.Context, tx *sql.Tx, from, to string, identity NodeIdentity) (map[string]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT id, kind, source_key FROM brain_nodes WHERE project_path = ?`, from)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string]string{}
	for rows.Next() {
		var id, kind, sourceKey string
		if err := rows.Scan(&id, &kind, &sourceKey); err != nil {
			return nil, unavailable(err)
		}
		out[id] = identity(to, kind, sourceKey)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return out, nil
}

type edgeRow struct {
	src, dst, kind string
	weight         float64
	createdAt      int64
}

func edgesTouching(ctx context.Context, tx *sql.Tx, ids map[string]string) ([]edgeRow, error) {
	rows, err := tx.QueryContext(ctx, `SELECT src, dst, kind, weight, created_at FROM brain_edges`)
	if err != nil {
		return nil, unavailable(err)
	}
	defer func() { _ = rows.Close() }()

	var out []edgeRow
	for rows.Next() {
		var e edgeRow
		if err := rows.Scan(&e.src, &e.dst, &e.kind, &e.weight, &e.createdAt); err != nil {
			return nil, unavailable(err)
		}
		_, hasSrc := ids[e.src]
		_, hasDst := ids[e.dst]
		if hasSrc || hasDst {
			out = append(out, e)
		}
	}
	return out, func() error {
		if err := rows.Err(); err != nil {
			return unavailable(err)
		}
		return nil
	}()
}

func deleteEdgesTouching(ctx context.Context, tx *sql.Tx, ids map[string]string) error {
	for old := range ids {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM brain_edges WHERE src = ? OR dst = ?`, old, old); err != nil {
			return unavailable(err)
		}
	}
	return nil
}

// reinsertEdges puts the links back with both ends translated.
//
// An edge whose two ends map onto the same node is dropped: after a merge the
// file that was recorded twice is one node, and a link from it to itself is a
// loop nobody can see. Everything else survives — a move that lost an edge
// would be a move that lost the thing the graph is for.
func reinsertEdges(ctx context.Context, tx *sql.Tx, edges []edgeRow, ids map[string]string) (int, error) {
	written := 0
	for _, e := range edges {
		src, dst := e.src, e.dst
		if next, ok := ids[src]; ok {
			src = next
		}
		if next, ok := ids[dst]; ok {
			dst = next
		}
		if src == dst {
			continue
		}
		// The same normalisation UpsertBrainEdges uses, so a pair discovered
		// from either end stays one row.
		if src > dst {
			src, dst = dst, src
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO brain_edges (src, dst, kind, weight, created_at)
			VALUES (?,?,?,?,?)
			ON CONFLICT(src, dst, kind) DO UPDATE SET weight = MAX(weight, excluded.weight)`,
			src, dst, e.kind, e.weight, e.createdAt); err != nil {
			return written, unavailable(err)
		}
		written++
	}
	return written, nil
}

func movePathColumns(ctx context.Context, tx *sql.Tx, from, to string) (int, error) {
	total := 0
	for _, table := range tablesWithProjectPath {
		res, err := tx.ExecContext(ctx,
			fmt.Sprintf(`UPDATE %s SET project_path = ? WHERE project_path = ?`, table), to, from)
		if err != nil {
			return total, unavailable(err)
		}
		n, _ := res.RowsAffected()
		total += int(n)
	}
	return total, nil
}

// DeleteProject removes a folder from the registry.
//
// Separate from ForgetBrainProject and callable on its own: unregistering a
// folder the operator no longer codes in is not the same decision as throwing
// away what was learned about it.
func (s *Store) DeleteProject(ctx context.Context, id string) error {
	if s == nil || s.db == nil {
		return unavailable(errors.New("store is not open"))
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id); err != nil {
		return unavailable(err)
	}
	return nil
}

// SetProjectPath re-points a registered folder, so a move updates the registry
// rather than leaving it naming a directory that is gone.
func (s *Store) SetProjectPath(ctx context.Context, id, path string) error {
	if s == nil || s.db == nil {
		return unavailable(errors.New("store is not open"))
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE projects SET path = ?, last_used_at = ? WHERE id = ?`,
		path, time.Now().UTC().Unix(), id); err != nil {
		return unavailable(err)
	}
	return nil
}
