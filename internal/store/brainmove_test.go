package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// testIdentity is brain.NodeID's rule, restated so this package can be tested
// without importing the one that owns it. The two must agree, which is why the
// move takes the function rather than knowing it.
func testIdentity(projectPath, kind, sourceKey string) string {
	sum := sha256.Sum256([]byte(projectPath + "|" + kind + "|" + sourceKey))
	return "node-" + hex.EncodeToString(sum[:])[:16]
}

func seedProject(t *testing.T, s *Store, path string, sources ...string) []string {
	t.Helper()
	ctx := context.Background()

	ids := make([]string, 0, len(sources))
	for _, src := range sources {
		id := testIdentity(path, "file", src)
		n := node(id, path, "file", src)
		if err := s.UpsertBrainNode(ctx, n); err != nil {
			t.Fatalf("seeding %s: %v", src, err)
		}
		ids = append(ids, id)
	}
	return ids
}

func countNodes(t *testing.T, s *Store, path string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM brain_nodes WHERE project_path = ?`, path).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func countEdges(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM brain_edges`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The move a renamed repository needs: the history survives, under the new
// path, with new identities — and every edge still points at something.
func TestMoveBrainProject_CarriesTheNodesAndTheirEdges(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	ids := seedProject(t, s, "/old", "a.go", "b.go", "c.go")
	if err := s.UpsertBrainEdges(ctx, []BrainEdgeRow{
		{Src: ids[0], Dst: ids[1], Kind: "calls", Weight: 1},
		{Src: ids[1], Dst: ids[2], Kind: "imports", Weight: 0.5},
	}); err != nil {
		t.Fatal(err)
	}
	before := countEdges(t, s)

	res, err := s.MoveBrainProject(ctx, "/old", "/new", testIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if res.Nodes != 3 || res.Merged != 0 {
		t.Fatalf("result = %+v, want three moved and none merged", res)
	}

	if n := countNodes(t, s, "/old"); n != 0 {
		t.Errorf("%d nodes left under the old path", n)
	}
	if n := countNodes(t, s, "/new"); n != 3 {
		t.Errorf("%d nodes under the new path, want 3", n)
	}
	// A move that lost an edge would lose the thing the graph is for.
	if after := countEdges(t, s); after != before {
		t.Errorf("edges = %d, were %d", after, before)
	}

	// And they point at the new identities, not at ids nothing resolves.
	wantA := testIdentity("/new", "file", "a.go")
	neighbours, err := s.BrainNeighbors(ctx, wantA, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(neighbours) != 1 {
		t.Fatalf("neighbours of the moved node = %d, want 1", len(neighbours))
	}
}

// The same file recorded under two paths is one file. The destination's row is
// the one the scan has been keeping current, so it wins.
func TestMoveBrainProject_MergesWhatTheDestinationAlreadyKnows(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	seedProject(t, s, "/old", "shared.go", "only-old.go")
	seedProject(t, s, "/new", "shared.go")

	res, err := s.MoveBrainProject(ctx, "/old", "/new", testIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if res.Merged != 1 || res.Nodes != 1 {
		t.Fatalf("result = %+v, want one merged and one moved", res)
	}
	if n := countNodes(t, s, "/new"); n != 2 {
		t.Errorf("%d nodes at the destination, want 2 — the shared file must not be duplicated", n)
	}
}

// The tables beside the graph carry the same path and have to come along, or
// the project's own conversations stay filed under a directory that is gone.
func TestMoveBrainProject_MovesTheRowsBesideTheGraph(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	seedProject(t, s, "/old", "a.go")
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO chat_sessions (id, project_path, updated_at) VALUES ('s1','/old',0)`); err != nil {
		t.Fatal(err)
	}

	res, err := s.MoveBrainProject(ctx, "/old", "/new", testIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rows == 0 {
		t.Error("no rows moved beside the graph")
	}

	var path string
	if err := s.db.QueryRow(`SELECT project_path FROM chat_sessions WHERE id = 's1'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if path != "/new" {
		t.Errorf("chat session is still filed under %q", path)
	}
}

func TestMoveBrainProject_RefusesAMoveToItself(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.MoveBrainProject(context.Background(), "/same", "/same", testIdentity); err == nil {
		t.Fatal("want an error when the two paths are the same")
	}
}

// A project the graph has never heard of is not an error: the rows beside it
// may still exist, and the caller asked for those to move too.
func TestMoveBrainProject_AnUnknownProjectIsNotAFailure(t *testing.T) {
	s := openTestStore(t)
	res, err := s.MoveBrainProject(context.Background(), "/nothing", "/somewhere", testIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if res.Nodes != 0 {
		t.Errorf("result = %+v, want nothing moved", res)
	}
}

// Forgetting is what nothing in this repository could do before: no DELETE
// FROM brain_nodes existed anywhere.
func TestForgetBrainProject_RemovesTheProjectAndItsEdges(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	ids := seedProject(t, s, "/gone", "a.go", "b.go")
	keep := seedProject(t, s, "/kept", "c.go")
	if err := s.UpsertBrainEdges(ctx, []BrainEdgeRow{
		{Src: ids[0], Dst: ids[1], Kind: "calls", Weight: 1},
		// An edge with one end in each project: it must go too, or it would
		// point at a node that no longer exists.
		{Src: ids[0], Dst: keep[0], Kind: "semantic", Weight: 1},
	}); err != nil {
		t.Fatal(err)
	}

	removed, err := s.ForgetBrainProject(ctx, "/gone")
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	if n := countNodes(t, s, "/gone"); n != 0 {
		t.Errorf("%d nodes survived", n)
	}
	if n := countNodes(t, s, "/kept"); n != 1 {
		t.Errorf("forgetting one project took %d nodes from another", 1-n)
	}
	if e := countEdges(t, s); e != 0 {
		t.Errorf("%d edges left pointing at nodes that are gone", e)
	}
}

func TestForgetBrainProject_NeedsAPath(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.ForgetBrainProject(context.Background(), ""); err == nil {
		t.Fatal("want an error for an empty path")
	}
}
