package store

import (
	"context"
	"testing"
	"time"
)

func node(id, project, kind, source string) BrainNodeRow {
	return BrainNodeRow{
		ID: id, ProjectPath: project, Kind: kind, SourceKey: source,
		Title: "Title " + source, Assessment: "An assessment of " + source,
		Body: "body text about " + source,
		Tags: []string{"alpha", "beta"}, Aliases: []string{"zeta"},
	}
}

func TestUpsertBrainNode_IsIdempotentPerIdentity(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	n := node("node-1", "/p", "repo", "owner/name")
	if err := s.UpsertBrainNode(ctx, n); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	n.Title = "Second title"
	n.Assessment = "A revised assessment"
	if err := s.UpsertBrainNode(ctx, n); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	count, err := s.CountBrainNodes(ctx, "/p")
	if err != nil {
		t.Fatalf("CountBrainNodes: %v", err)
	}
	if count != 1 {
		t.Fatalf("re-ingesting one source left %d nodes, want 1", count)
	}

	got, ok, err := s.BrainNode(ctx, "node-1")
	if err != nil || !ok {
		t.Fatalf("BrainNode: %v (found=%v)", err, ok)
	}
	if got.Title != "Second title" {
		t.Errorf("title = %q, want the revised one", got.Title)
	}
}

// created_at surviving an update is what keeps "most recent work" from meaning
// "most recently re-scanned".
func TestUpsertBrainNode_PreservesTheCallersCreatedAt(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	first := time.Now().Add(-72 * time.Hour).UTC().Truncate(time.Second)
	n := node("node-1", "/p", "note", "src")
	n.CreatedAt = first
	if err := s.UpsertBrainNode(ctx, n); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	n.CreatedAt = first // the core reads it back and passes it again
	n.UpdatedAt = time.Now().UTC()
	if err := s.UpsertBrainNode(ctx, n); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}

	got, _, err := s.BrainNode(ctx, "node-1")
	if err != nil {
		t.Fatalf("BrainNode: %v", err)
	}
	if !got.CreatedAt.Equal(first) {
		t.Errorf("created_at = %v, want the original %v", got.CreatedAt, first)
	}
}

// The alias column is what stands in for a vector index: a query whose word
// appears nowhere in the node's own text still has to find it.
func TestSearchBrainNodes_MatchesOnAliasesAlone(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	n := node("node-1", "/p", "repo", "modelcontextprotocol/go-sdk")
	n.Title = "Go SDK"
	n.Assessment = "A library for building servers."
	n.Body = "Servers and clients."
	n.Tags = []string{"golang", "sdk"}
	n.Aliases = []string{"anthropic", "toolcalling"}
	if err := s.UpsertBrainNode(ctx, n); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := s.SearchBrainNodes(ctx, "/p", "anthropic", 5)
	if err != nil {
		t.Fatalf("SearchBrainNodes: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("searching an alias-only term returned %d nodes, want 1", len(got))
	}
	if got[0].ID != "node-1" {
		t.Errorf("id = %q, want node-1", got[0].ID)
	}
}

// A node about a public repository belongs to no checkout, so the global scope
// has to be visible from every project or it may as well not be stored.
func TestSearchBrainNodes_IncludesGlobalAndExcludesOtherProjects(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	for _, n := range []BrainNodeRow{
		node("mine", "/p", "note", "mine"),
		node("global", "", "repo", "global"),
		node("theirs", "/other", "note", "theirs"),
	} {
		if err := s.UpsertBrainNode(ctx, n); err != nil {
			t.Fatalf("upsert %s: %v", n.ID, err)
		}
	}

	got, err := s.SearchBrainNodes(ctx, "/p", "alpha beta", 10)
	if err != nil {
		t.Fatalf("SearchBrainNodes: %v", err)
	}

	seen := map[string]bool{}
	for _, n := range got {
		seen[n.ID] = true
	}
	if !seen["mine"] || !seen["global"] {
		t.Errorf("want both the project's own node and the global one, got %v", seen)
	}
	if seen["theirs"] {
		t.Error("another project's node leaked into the results")
	}
}

// An unparseable query is a miss, not an error.
func TestSearchBrainNodes_PunctuationOnlyQueryIsAMiss(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if err := s.UpsertBrainNode(ctx, node("n", "/p", "note", "src")); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := s.SearchBrainNodes(ctx, "/p", "?? -- \"", 5)
	if err != nil {
		t.Fatalf("an unparseable query must not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d results, want none", len(got))
	}
}

// FTS5 external-content tables keep no copy of their own, so a stale index
// after an update is the failure this covers.
func TestSearchBrainNodes_IndexFollowsAnUpdate(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	n := node("node-1", "/p", "note", "src")
	n.Assessment = "concerns quicksilver"
	if err := s.UpsertBrainNode(ctx, n); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	n.Assessment = "concerns tungsten"
	if err := s.UpsertBrainNode(ctx, n); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}

	if got, err := s.SearchBrainNodes(ctx, "/p", "tungsten", 5); err != nil || len(got) != 1 {
		t.Fatalf("new term: got %d results, err %v; want 1", len(got), err)
	}
	if got, err := s.SearchBrainNodes(ctx, "/p", "quicksilver", 5); err != nil || len(got) != 0 {
		t.Fatalf("replaced term still matches: got %d results, err %v; want 0", len(got), err)
	}
}

func TestBrainEdges_StoredOnceAndReadBothWays(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	for _, id := range []string{"a", "b"} {
		if err := s.UpsertBrainNode(ctx, node(id, "/p", "note", id)); err != nil {
			t.Fatalf("upsert %s: %v", id, err)
		}
	}

	// The same pair, discovered from both ends, must be one row.
	if err := s.UpsertBrainEdges(ctx, []BrainEdgeRow{
		{Src: "a", Dst: "b", Kind: "tag", Weight: 0.4},
		{Src: "b", Dst: "a", Kind: "tag", Weight: 0.6},
	}); err != nil {
		t.Fatalf("UpsertBrainEdges: %v", err)
	}

	fromA, err := s.BrainNeighbors(ctx, "a", 10)
	if err != nil {
		t.Fatalf("BrainNeighbors(a): %v", err)
	}
	if len(fromA) != 1 {
		t.Fatalf("a has %d edges, want 1", len(fromA))
	}
	if fromA[0].Dst != "b" || fromA[0].Weight != 0.6 {
		t.Errorf("edge from a = %+v, want dst b at the later weight", fromA[0])
	}

	fromB, err := s.BrainNeighbors(ctx, "b", 10)
	if err != nil {
		t.Fatalf("BrainNeighbors(b): %v", err)
	}
	if len(fromB) != 1 || fromB[0].Dst != "a" {
		t.Errorf("edges from b = %+v, want the far end reported as a", fromB)
	}
}

func TestUpsertBrainEdges_DropsSelfLinks(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if err := s.UpsertBrainEdges(ctx, []BrainEdgeRow{{Src: "a", Dst: "a", Kind: "tag", Weight: 1}}); err != nil {
		t.Fatalf("UpsertBrainEdges: %v", err)
	}
	got, err := s.BrainNeighbors(ctx, "a", 10)
	if err != nil {
		t.Fatalf("BrainNeighbors: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a self-link was stored: %+v", got)
	}
}

// Every read has to survive a nil store, because that is the shape mimir-mcp
// runs in when the database could not be opened (SD-6).
func TestBrainReads_TolerateANilStore(t *testing.T) {
	var s *Store
	ctx := context.Background()

	if _, ok, err := s.BrainNode(ctx, "x"); ok || err != nil {
		t.Errorf("BrainNode on a nil store: ok=%v err=%v", ok, err)
	}
	if got, err := s.SearchBrainNodes(ctx, "/p", "q", 5); got != nil || err != nil {
		t.Errorf("SearchBrainNodes on a nil store: %v, %v", got, err)
	}
	if got, err := s.BrainNeighbors(ctx, "x", 5); got != nil || err != nil {
		t.Errorf("BrainNeighbors on a nil store: %v, %v", got, err)
	}
	if n, err := s.CountBrainNodes(ctx, "/p"); n != 0 || err != nil {
		t.Errorf("CountBrainNodes on a nil store: %v, %v", n, err)
	}
}

// --- the graph ---------------------------------------------------------------

// graphFixture writes a hub with five edges, its five leaves, and one node
// nothing links to.
func graphFixture(t *testing.T, s *Store, project string) {
	t.Helper()
	ctx := context.Background()

	all := []BrainNodeRow{node("hub", project, "file", "hub.go")}
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		all = append(all, node(name, project, "file", name+".go"))
	}
	all = append(all, node("lonely", project, "note", "lonely"))

	now := time.Now().UTC()
	for i, n := range all {
		n.CreatedAt = now
		// Staggered so "most recent" is a different order from "most connected"
		// — which is the whole point of the ranking test below.
		n.UpdatedAt = now.Add(time.Duration(i) * time.Minute)
		if err := s.UpsertBrainNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}

	var edges []BrainEdgeRow
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		edges = append(edges, BrainEdgeRow{Src: "hub", Dst: name, Kind: "tag", Weight: 0.6})
	}
	if err := s.UpsertBrainEdges(ctx, edges); err != nil {
		t.Fatal(err)
	}
}

// The hub is what a picture of the brain is about; the newest node is whatever
// the scan happened to reach last.
func TestBrainGraphIDs_RanksByDegree(t *testing.T) {
	s := openTestStore(t)
	graphFixture(t, s, "/repo")

	got, err := s.BrainGraphIDs(context.Background(), "/repo", 3)
	if err != nil {
		t.Fatalf("BrainGraphIDs: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d nodes, want 3: %+v", len(got), got)
	}
	if got[0].ID != "hub" || got[0].Degree != 5 {
		t.Errorf("first = %+v, want the hub with degree 5", got[0])
	}
	if got[len(got)-1].ID == "lonely" {
		t.Error("an unconnected node outranked a connected one")
	}
}

func TestBrainGraphIDs_ScopesToAProjectPlusGlobal(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	graphFixture(t, s, "/repo")

	other := node("elsewhere", "/other", "file", "other.go")
	global := node("global", "", "research", "a public repository")
	for _, n := range []BrainNodeRow{other, global} {
		if err := s.UpsertBrainNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.BrainGraphIDs(ctx, "/repo", 50)
	if err != nil {
		t.Fatalf("BrainGraphIDs: %v", err)
	}
	ids := map[string]bool{}
	for _, d := range got {
		ids[d.ID] = true
	}
	if ids["elsewhere"] {
		t.Error("another project's node is in this project's graph")
	}
	if !ids["global"] {
		t.Error("a global node is missing; it belongs to every project")
	}

	// The whole machine, when no project is named.
	all, err := s.BrainGraphIDs(ctx, "", 50)
	if err != nil {
		t.Fatalf("BrainGraphIDs: %v", err)
	}
	if len(all) != len(got)+1 {
		t.Errorf("the machine-wide graph has %d nodes, the project's has %d", len(all), len(got))
	}
}

// A force layout handed an edge to a node it was never given invents a phantom
// or throws. Neither is something a UI should have to defend against.
func TestBrainEdgesAmong_NeverReturnsADanglingEdge(t *testing.T) {
	s := openTestStore(t)
	graphFixture(t, s, "/repo")

	got, err := s.BrainEdgesAmong(context.Background(), []string{"hub", "a", "b"}, 100)
	if err != nil {
		t.Fatalf("BrainEdgesAmong: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d edges, want the two among the three ids: %+v", len(got), got)
	}
	// Edges are stored once, in one direction (src < dst lexically), so which
	// end is which is not the test — that both ends are in the set is.
	inSet := map[string]bool{"hub": true, "a": true, "b": true}
	for _, e := range got {
		if !inSet[e.Src] || !inSet[e.Dst] {
			t.Errorf("edge %+v points outside the node set", e)
		}
	}
}

// The id list is chunked into several statements; the answer must not be.
func TestBrainEdgesAmong_ChunksALargeIDList(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	graphFixture(t, s, "/repo")

	ids := []string{"hub", "a", "b", "c", "d", "e"}
	for i := 0; i < 900; i++ {
		ids = append(ids, "filler-"+time.Duration(i).String())
	}

	got, err := s.BrainEdgesAmong(ctx, ids, 100)
	if err != nil {
		t.Fatalf("BrainEdgesAmong with %d ids: %v", len(ids), err)
	}
	if len(got) != 5 {
		t.Errorf("got %d edges, want all 5: %+v", len(got), got)
	}
}

func TestBrainProjects_CountsFilesAndKeepsTheGlobalScope(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	graphFixture(t, s, "/repo")
	if err := s.UpsertBrainNode(ctx, node("global", "", "research", "a public repository")); err != nil {
		t.Fatal(err)
	}

	got, err := s.BrainProjects(ctx)
	if err != nil {
		t.Fatalf("BrainProjects: %v", err)
	}
	byPath := map[string]BrainProjectCount{}
	for _, p := range got {
		byPath[p.ProjectPath] = p
	}

	repo, ok := byPath["/repo"]
	if !ok {
		t.Fatalf("the project is missing: %+v", got)
	}
	if repo.Nodes != 7 || repo.Files != 6 {
		t.Errorf("counts = %d nodes / %d files, want 7 and 6", repo.Nodes, repo.Files)
	}
	if repo.UpdatedAt.IsZero() {
		t.Error("updated_at did not come back")
	}
	if _, ok := byPath[""]; !ok {
		t.Error("the global scope was filtered out; every node in it belongs to every project")
	}
}
