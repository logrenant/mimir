package brain

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/logrenant/mimir/internal/store"
)

// The two reads brain.Store grew for the traversals. They live here rather than
// beside the rest of fakeStore so brain_test.go — which is about ingest, not
// about reading the graph — did not have to be opened to add them.

// BrainInboundEdges keeps the direction, which is the whole point of it
// existing beside BrainNeighbors.
func (f *fakeStore) BrainInboundEdges(_ context.Context, dst string, kinds []string, limit int) ([]store.BrainEdgeRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	want := map[string]struct{}{}
	for _, k := range kinds {
		want[k] = struct{}{}
	}

	var out []store.BrainEdgeRow
	for _, e := range f.edges {
		if e.Dst != dst {
			continue
		}
		if len(want) > 0 {
			if _, ok := want[e.Kind]; !ok {
				continue
			}
		}
		out = append(out, e)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (f *fakeStore) BrainGraphIDs(_ context.Context, _ string, limit int, _ []string) ([]store.BrainNodeDegree, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	degree := map[string]int{}
	for _, e := range f.edges {
		degree[e.Src]++
		degree[e.Dst]++
	}
	out := make([]store.BrainNodeDegree, 0, len(degree))
	for id, n := range degree {
		out = append(out, store.BrainNodeDegree{ID: id, Degree: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Degree != out[j].Degree {
			return out[i].Degree > out[j].Degree
		}
		return out[i].ID < out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// --- fixtures ---------------------------------------------------------------

// callGraph builds a tiny directed call graph:
//
//	pump  ──calls──▶ dispatchOne ──calls──▶ launch
//	Kick  ──calls──▶ dispatchOne
//
// Directed on purpose: "who calls dispatchOne" and "what does dispatchOne call"
// are different answers over the same two edges.
func callGraph(t *testing.T) (*Core, map[string]string) {
	t.Helper()

	st := newFakeStore()
	ids := map[string]string{}
	for _, name := range []string{"pump", "dispatchOne", "launch", "Kick"} {
		id := NodeID("/repo", KindSymbol, "internal/coderunner/runner.go::"+name)
		ids[name] = id
		if err := st.UpsertBrainNode(context.Background(), store.BrainNodeRow{
			ID:          id,
			ProjectPath: "/repo",
			Kind:        KindSymbol,
			Title:       name,
			Tags:        []string{"symbol", "go"},
			Assessment:  "internal/coderunner/runner.go 689",
		}); err != nil {
			t.Fatal(err)
		}
	}
	edges := []store.BrainEdgeRow{
		{Src: ids["pump"], Dst: ids["dispatchOne"], Kind: "calls", Weight: 1},
		{Src: ids["Kick"], Dst: ids["dispatchOne"], Kind: "calls", Weight: 1},
		{Src: ids["dispatchOne"], Dst: ids["launch"], Kind: "calls", Weight: 1},
	}
	if err := st.UpsertBrainEdges(context.Background(), edges); err != nil {
		t.Fatal(err)
	}
	return testCore(t, st, nil), ids
}

// --- affected ---------------------------------------------------------------

// TestAffected_FindsTheCallersOfASymbol is the question that could not be asked
// before this task: BrainNeighbors normalises every edge so the node asked
// about is Src, which erases the direction "who calls me" is made of.
func TestAffected_FindsTheCallersOfASymbol(t *testing.T) {
	c, ids := callGraph(t)

	hits, err := c.Affected(context.Background(), ids["dispatchOne"], nil, 2)
	if err != nil {
		t.Fatal(err)
	}

	got := titles(hits)
	if !has(got, "pump") || !has(got, "Kick") {
		t.Fatalf("callers = %v, want pump and Kick", got)
	}
	// launch is what dispatchOne calls, not what calls it. A read that lost the
	// direction would return it here, and the answer would look right.
	if has(got, "launch") {
		t.Fatalf("callers = %v — the callee came back as a caller, so direction was lost", got)
	}
}

func TestAffected_WalksMoreThanOneHop(t *testing.T) {
	c, ids := callGraph(t)

	oneHop, err := c.Affected(context.Background(), ids["launch"], nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(oneHop) != 1 || oneHop[0].Title != "dispatchOne" {
		t.Fatalf("one hop = %v, want just dispatchOne", titles(oneHop))
	}

	twoHops, err := c.Affected(context.Background(), ids["launch"], nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !has(titles(twoHops), "pump") {
		t.Fatalf("two hops = %v, want pump to appear", titles(twoHops))
	}
}

func TestAffected_RefusesANodeThatIsNotThere(t *testing.T) {
	c, _ := callGraph(t)
	if _, err := c.Affected(context.Background(), "node-nope", nil, 2); err == nil {
		t.Fatal("expected ErrNodeNotFound")
	}
}

// TestAffected_CarriesTheEdgeItArrivedThrough — a path with no reasons on it is
// a list, not an explanation.
func TestAffected_CarriesTheEdgeItArrivedThrough(t *testing.T) {
	c, ids := callGraph(t)
	hits, err := c.Affected(context.Background(), ids["dispatchOne"], nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Why != "calls" {
			t.Fatalf("hit %q arrived with no reason (%q)", h.Title, h.Why)
		}
	}
}

// --- query ------------------------------------------------------------------

// TestQuery_ExpandsOnlyIntoVocabularyTheGraphActuallyHas.
//
// The index matches literally. A word the graph does not contain must be
// dropped rather than approximated, because an approximated token is an
// invented search and the answer built on it cannot be told from a real one.
func TestQuery_ExpandsOnlyIntoVocabularyTheGraphActuallyHas(t *testing.T) {
	st := newFakeStore()
	id := NodeID("/repo", KindSymbol, "internal/coderunner/runner.go::dispatchOne")
	row := store.BrainNodeRow{
		ID: id, ProjectPath: "/repo", Kind: KindSymbol,
		Title: "dispatchOne", Tags: []string{"symbol", "go"},
	}
	if err := st.UpsertBrainNode(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	st.results = []store.BrainNodeRow{row}
	c := testCore(t, st, nil)

	got, err := c.Query(context.Background(), "/repo", "dispatch kimlik doğrulama", 10)
	if err != nil {
		t.Fatal(err)
	}
	if !has(got.Expanded, "dispatch") {
		t.Fatalf("expanded = %v, want the word the graph has", got.Expanded)
	}
	for _, tok := range got.Expanded {
		if tok == "kimlik" || tok == "doğrulama" {
			t.Fatalf("expanded = %v — a word absent from the graph was searched anyway", got.Expanded)
		}
	}
}

// TestQuery_SaysSoInsteadOfGuessingWhenNothingMatches. An empty list reads as
// "there is nothing there"; the note says what actually happened.
func TestQuery_SaysSoInsteadOfGuessingWhenNothingMatches(t *testing.T) {
	st := newFakeStore()
	c := testCore(t, st, nil)

	got, err := c.Query(context.Background(), "/repo", "kuantum hesaplama", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Hits) != 0 {
		t.Fatalf("hits = %v, want none", titles(got.Hits))
	}
	if !strings.Contains(got.Note, "kelime yok") {
		t.Fatalf("note = %q, want it to say the graph has no vocabulary for this", got.Note)
	}
}

// TestQuery_ReportsWhatItActuallySearched — a reader who cannot see which words
// were used cannot tell a miss from an absence.
func TestQuery_ReportsWhatItActuallySearched(t *testing.T) {
	st := newFakeStore()
	row := store.BrainNodeRow{
		ID: "n1", ProjectPath: "/repo", Kind: KindSymbol, Title: "handleListCodingTasks",
	}
	if err := st.UpsertBrainNode(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	st.results = []store.BrainNodeRow{row}
	c := testCore(t, st, nil)

	got, err := c.Query(context.Background(), "/repo", "coding tasks", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Expanded) == 0 {
		t.Fatal("the answer did not say what it searched")
	}
}

// TestQuery_KeepsAWordTheGraphOnlyWroteInAnAssessment.
//
// The expansion used to rebuild its vocabulary from the titles of the best
// matches, which is one of the five fields the index covers. A word written
// into assessments and nowhere else was therefore reported absent from a graph
// that plainly contains it — the "impossible to miss" bug.
func TestQuery_KeepsAWordTheGraphOnlyWroteInAnAssessment(t *testing.T) {
	st := newFakeStore()
	row := store.BrainNodeRow{
		ID: "n1", ProjectPath: "/repo", Kind: KindFile,
		Title:      "Floating panel primitive",
		Assessment: "Anchors a popover to the control that opened it.",
	}
	if err := st.UpsertBrainNode(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	st.results = []store.BrainNodeRow{row}
	c := testCore(t, st, nil)

	got, err := c.Query(context.Background(), "/repo", "popover", 10)
	if err != nil {
		t.Fatal(err)
	}
	if !has(got.Expanded, "popover") {
		t.Fatalf("expanded = %v, note = %q — the word is in the graph", got.Expanded, got.Note)
	}
}

// TestQuery_ReadsADecomposedWordAsOneWord.
//
// macOS hands back decomposed filenames, and this store holds one: a node whose
// source key is `İlk-çizim.uskroki` with its cedilla as a separate codepoint. A
// combining mark is not a letter, so the word splitter cut that into `c` and
// `izim` and searched for neither of the words on the screen.
func TestQuery_ReadsADecomposedWordAsOneWord(t *testing.T) {
	st := newFakeStore()
	row := store.BrainNodeRow{
		ID: "n1", ProjectPath: "/repo", Kind: KindFile, Title: "cizim katalogu",
	}
	if err := st.UpsertBrainNode(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	st.results = []store.BrainNodeRow{row}
	c := testCore(t, st, nil)

	// "çizim", written the way a filesystem hands it over: c + combining cedilla.
	got, err := c.Query(context.Background(), "/repo", "c\u0327izim", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Expanded) != 1 || got.Expanded[0] != "cizim" {
		t.Fatalf("expanded = %v, want the one folded word that was asked", got.Expanded)
	}
}

// --- path and hubs ----------------------------------------------------------

func TestPath_FindsTheChainBetweenTwoSymbols(t *testing.T) {
	c, ids := callGraph(t)

	hits, err := c.Path(context.Background(), ids["pump"], ids["launch"])
	if err != nil {
		t.Fatal(err)
	}
	got := titles(hits)
	if len(got) < 2 || got[0] != "pump" {
		t.Fatalf("path = %v, want it to start at pump", got)
	}
	if !has(got, "dispatchOne") {
		t.Fatalf("path = %v, want the node in between", got)
	}
}

func TestPath_RefusesAnEndpointThatIsNotThere(t *testing.T) {
	c, ids := callGraph(t)
	if _, err := c.Path(context.Background(), ids["pump"], "node-nope"); err == nil {
		t.Fatal("expected ErrNodeNotFound")
	}
}

// TestHubs_RanksByDegree — the architectural centres are the most connected
// things, which is the same question the graph view already answers by drawing.
func TestHubs_RanksByDegree(t *testing.T) {
	c, ids := callGraph(t)

	hits, err := c.Hubs(context.Background(), "/repo", 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no hubs")
	}
	if hits[0].NodeID != ids["dispatchOne"] {
		t.Fatalf("top hub = %q, want dispatchOne (degree 3)", hits[0].Title)
	}
	for i := 1; i < len(hits); i++ {
		if hits[i-1].Hops < hits[i].Hops {
			t.Fatalf("hubs are not ordered by degree: %v", hits)
		}
	}
}

// TestExplain_ReturnsTheNodeAndWhatHangsOffIt.
func TestExplain_ReturnsTheNodeAndWhatHangsOffIt(t *testing.T) {
	c, ids := callGraph(t)

	got, err := c.Explain(context.Background(), ids["dispatchOne"])
	if err != nil {
		t.Fatal(err)
	}
	names := titles(got.Hits)
	if len(names) == 0 || names[0] != "dispatchOne" {
		t.Fatalf("explain = %v, want the node itself first", names)
	}
	if len(names) < 2 {
		t.Fatalf("explain = %v, want its neighbours too", names)
	}
}

// TestHitOf_CarriesWhereTheSymbolIs — an answer that cannot be checked is an
// answer that has to be believed.
func TestHitOf_CarriesWhereTheSymbolIs(t *testing.T) {
	h := hitOf(store.BrainNodeRow{
		ID: "n1", Kind: KindSymbol, Title: "pump",
		Assessment: "internal/coderunner/runner.go 689",
	}, "calls", 1)

	if h.File != "internal/coderunner/runner.go" || h.Location != "689" {
		t.Fatalf("file=%q location=%q, want the path and the line", h.File, h.Location)
	}
}

func titles(hits []Hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Title)
	}
	return out
}

func has(all []string, want string) bool {
	for _, v := range all {
		if v == want {
			return true
		}
	}
	return false
}
