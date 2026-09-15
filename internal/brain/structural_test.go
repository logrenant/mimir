package brain

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/graphify"
	"github.com/logrenant/mimir/internal/store"
)

// No model is given to the core in any test here, and that is the assertion:
// "the structural pass costs no model call" is the whole claim of the layer,
// and a nil Completer turns a broken claim into a panic rather than a silent
// subprocess.
func structuralCore(t *testing.T) (*Core, *fakeStore) {
	t.Helper()
	st := newFakeStore()
	return New(config.Load(), st, nil), st
}

func extraction() graphify.Extraction {
	return graphify.Extraction{
		Nodes: []graphify.Node{
			{ID: "f", Label: "client.go", FileType: "code", SourceFile: "api/client.go", SourceLocation: "L1"},
			{ID: "c", Label: "Client", FileType: "code", SourceFile: "api/client.go", SourceLocation: "L20"},
			{ID: "m", Label: ".Do()", FileType: "code", SourceFile: "api/client.go", SourceLocation: "L44"},
			{ID: "doc", Label: "design", FileType: "document", SourceFile: "docs/design.md", SourceLocation: "§1"},
		},
		Edges: []graphify.Edge{
			{Source: "f", Target: "c", Relation: "contains", Confidence: "EXTRACTED", Weight: 1},
			{Source: "c", Target: "m", Relation: "method", Confidence: "EXTRACTED", Weight: 1},
			{Source: "m", Target: "doc", Relation: "implements", Confidence: "INFERRED", Weight: 0.8},
		},
	}
}

func TestIngestStructural_WritesSymbolsWithoutAModel(t *testing.T) {
	c, st := structuralCore(t)

	res, err := c.IngestStructural(context.Background(), "/repo", extraction(), "0.9.54")
	if err != nil {
		t.Fatal(err)
	}
	if res.Symbols != 2 {
		t.Fatalf("symbols = %d, want the class and the method", res.Symbols)
	}

	id := NodeID("/repo", KindSymbol, "api/client.go::Client")
	node, ok := st.nodes[id]
	if !ok {
		t.Fatal("the class was not stored under its path::name key")
	}
	if node.Kind != KindSymbol || node.Title != "Client" {
		t.Errorf("node = %+v", node)
	}
	// The claim of the whole layer: nothing imagined this.
	if node.Provider != "" || node.Model != "" {
		t.Errorf("provider = %q, model = %q; a structural node has neither", node.Provider, node.Model)
	}
	if node.PromptVersion != "graphify@0.9.54" {
		t.Errorf("prompt_version = %q", node.PromptVersion)
	}
	if node.Assessment != "api/client.go L20" {
		t.Errorf("assessment = %q, want where it is and nothing more", node.Assessment)
	}
}

// The file node belongs to the resident scan, which gives it a model's
// sentence. Overwriting that with a filename is the one thing this pass must
// not do — but it must still link to it, or the two layers are two graphs.
func TestIngestStructural_LinksToTheFileNodeWithoutCreatingIt(t *testing.T) {
	c, st := structuralCore(t)

	if _, err := c.IngestStructural(context.Background(), "/repo", extraction(), ""); err != nil {
		t.Fatal(err)
	}

	fileID := NodeID("/repo", KindFile, "api/client.go")
	if _, ok := st.nodes[fileID]; ok {
		t.Error("the structural pass created the file node; the scan owns it")
	}

	classID := NodeID("/repo", KindSymbol, "api/client.go::Client")
	var joined bool
	for _, e := range st.edges {
		if (e.Src == fileID && e.Dst == classID) || (e.Src == classID && e.Dst == fileID) {
			joined = true
			if e.Kind != "defines" {
				t.Errorf("edge kind = %q, want defines", e.Kind)
			}
		}
	}
	if !joined {
		t.Error("the symbol is not joined to its file; the layers would be two graphs")
	}
}

// A concept from a document is the semantic scan's territory. Taking it here
// would put a second, unreconciled opinion about the same file in the graph.
func TestIngestStructural_LeavesDocumentsAlone(t *testing.T) {
	c, st := structuralCore(t)

	if _, err := c.IngestStructural(context.Background(), "/repo", extraction(), ""); err != nil {
		t.Fatal(err)
	}
	for _, n := range st.nodes {
		if n.SourceKey == "docs/design.md::design" {
			t.Fatal("a document concept was stored as a symbol")
		}
	}
	// And the edge that reached it is dropped rather than left dangling.
	for _, e := range st.edges {
		if e.Dst == NodeID("/repo", KindSymbol, "docs/design.md::design") {
			t.Fatal("an edge to a node that was never written survived")
		}
	}
}

func TestIngestStructural_TheCapKeepsTheHubs(t *testing.T) {
	cfg := config.Load()
	cfg.BrainSymbolsPerProject = 1
	st := newFakeStore()
	c := New(cfg, st, nil)

	res, err := c.IngestStructural(context.Background(), "/repo", extraction(), "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Symbols != 1 || res.Dropped != 1 {
		t.Fatalf("symbols = %d, dropped = %d; want one of each", res.Symbols, res.Dropped)
	}
	// Client has two relations, .Do() has one after the document edge is
	// dropped — so the class is the one worth keeping.
	if _, ok := st.nodes[NodeID("/repo", KindSymbol, "api/client.go::Client")]; !ok {
		t.Error("the cap kept a leaf and dropped the hub")
	}
}

// Re-running over an unchanged project must write the same rows, so the store's
// version history does not grow a row per sweep.
func TestIngestStructural_IsStableAcrossRuns(t *testing.T) {
	c, st := structuralCore(t)

	if _, err := c.IngestStructural(context.Background(), "/repo", extraction(), "0.9.54"); err != nil {
		t.Fatal(err)
	}
	first := st.nodes[NodeID("/repo", KindSymbol, "api/client.go::Client")]

	if _, err := c.IngestStructural(context.Background(), "/repo", extraction(), "0.9.54"); err != nil {
		t.Fatal(err)
	}
	second := st.nodes[NodeID("/repo", KindSymbol, "api/client.go::Client")]

	if first.ContentHash != second.ContentHash {
		t.Error("the same symbol hashed differently on a second run")
	}
	if first.ID != second.ID {
		t.Error("the same symbol got a second identity")
	}
}

func TestRelationKind_KeepsTheWordsAReaderKnows(t *testing.T) {
	// The vocabulary is the one a real corpus actually produces: this repo's
	// 300 Go and TypeScript files yielded calls, references, contains,
	// imports_from, method, imports, embeds, dynamic_import, indirect_call and
	// implements — in that order of frequency.
	cases := map[string]string{
		"calls": "calls", "indirect_call": "calls",
		"imports_from": "imports", "imports": "imports", "dynamic_import": "imports",
		"contains": "defines", "method": "defines",
		"inherits": "inherits", "embeds": "inherits",
		"implements": "implements",
		"references": "references",
		"uses":       "uses",
		// Anything the tool adds upstream lands here rather than minting a
		// category from a package that ships most days.
		"something_new_upstream": "structural",
	}
	for in, want := range cases {
		if got := relationKind(in); got != want {
			t.Errorf("relationKind(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEdgeWeight_SeparatesWhatWasSeenFromWhatWasGuessed(t *testing.T) {
	seen := edgeWeight(graphify.Edge{Confidence: "EXTRACTED"})
	inferred := edgeWeight(graphify.Edge{Confidence: "INFERRED"})
	guessed := edgeWeight(graphify.Edge{Confidence: "AMBIGUOUS"})

	if !(seen > inferred && inferred > guessed) {
		t.Errorf("weights = %v / %v / %v; want them ordered by confidence", seen, inferred, guessed)
	}
	// Graphify's own number wins where it has one.
	if got := edgeWeight(graphify.Edge{Confidence: "INFERRED", Weight: 0.8}); got != 0.8 {
		t.Errorf("weight = %v, want the tool's own 0.8", got)
	}
}

// The whole point of the structural layer feeding the semantic pass: a link
// the parser is certain of must not cost a model call to re-discover.
func TestWithoutKnown_TakesSettledLinksOutOfTheQuestion(t *testing.T) {
	candidates := []store.BrainNodeRow{
		{ID: "a", Title: "client.go"},
		{ID: "b", Title: "README.md"},
		{ID: "c", Title: "store.go"},
	}
	got := withoutKnown(candidates, []string{"client.go", "store.go"})

	if len(got) != 1 || got[0].Title != "README.md" {
		t.Fatalf("candidates = %+v; want only the one no parser can link", got)
	}
}

func TestWithoutKnown_LeavesEverythingWhenNothingIsSettled(t *testing.T) {
	candidates := []store.BrainNodeRow{{ID: "a", Title: "a.go"}}
	if got := withoutKnown(candidates, nil); len(got) != 1 {
		t.Fatalf("candidates = %+v, want all of them", got)
	}
}

// The pass's own output must not count as settled, or it could never revise
// itself: a "semantic" edge it wrote last sweep would silently remove the
// candidate from every later question.
func TestIsStructuralEdge_ExcludesThisPassesOwnKinds(t *testing.T) {
	for _, kind := range []string{"calls", "imports", "defines", "inherits", "references", "uses", "structural"} {
		if !isStructuralEdge(kind) {
			t.Errorf("%s: want structural", kind)
		}
	}
	for _, kind := range []string{"tag", "semantic", "provenance"} {
		if isStructuralEdge(kind) {
			t.Errorf("%s: want not structural — it is a model's own verdict", kind)
		}
	}
}

// The digest is what stops the parser re-reading a project nothing has touched.
func TestStructuralDigest_ChangesOnlyWhenTheFilesDo(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", "package a")
	write("b.go", "package b")

	first := StructuralDigest(dir, []string{"a.go", "b.go"})
	if first != StructuralDigest(dir, []string{"a.go", "b.go"}) {
		t.Fatal("the same tree hashed differently twice")
	}

	// A different file list is a different project state, even untouched.
	if StructuralDigest(dir, []string{"a.go"}) == first {
		t.Error("dropping a file did not change the digest")
	}

	write("a.go", "package a // changed, and longer")
	if StructuralDigest(dir, []string{"a.go", "b.go"}) == first {
		t.Error("editing a file did not change the digest")
	}
}

// A file that is not there is not an error: the list came from git, and a file
// can be deleted between listing and hashing.
func TestStructuralDigest_ToleratesAMissingFile(t *testing.T) {
	dir := t.TempDir()
	if got := StructuralDigest(dir, []string{"gone.go"}); got == "" {
		t.Error("a missing file produced no digest at all")
	}
}

func TestStructuralCursorKey_CarriesThePathTheStoreExpects(t *testing.T) {
	if got := StructuralCursorKey("/repo"); got != "structural:/repo" {
		t.Errorf("key = %q", got)
	}
}
