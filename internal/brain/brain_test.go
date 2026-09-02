package brain

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/llm"
	"github.com/logrenant/mimir/internal/refine"
	"github.com/logrenant/mimir/internal/store"
)

// --- fakes -------------------------------------------------------------------

// fakeStore is guarded because brain.Store requires implementations to be safe
// for concurrent use — Scan ingests several files at once — and an unguarded
// fake turns that contract into a flaky test instead of a compile-time one.
type fakeStore struct {
	mu      sync.Mutex
	nodes   map[string]store.BrainNodeRow
	edges   []store.BrainEdgeRow
	results []store.BrainNodeRow // what SearchBrainNodes returns
	searchN int
	failAll error
}

func newFakeStore() *fakeStore {
	return &fakeStore{nodes: map[string]store.BrainNodeRow{}}
}

func (f *fakeStore) UpsertBrainNode(_ context.Context, n store.BrainNodeRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failAll != nil {
		return f.failAll
	}
	if existing, ok := f.nodes[n.ID]; ok && !existing.CreatedAt.IsZero() && n.CreatedAt.IsZero() {
		n.CreatedAt = existing.CreatedAt
	}
	f.nodes[n.ID] = n
	return nil
}

func (f *fakeStore) BrainNode(_ context.Context, id string) (store.BrainNodeRow, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	n, ok := f.nodes[id]
	return n, ok, nil
}

func (f *fakeStore) BrainNodesByIDs(_ context.Context, ids []string) ([]store.BrainNodeRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []store.BrainNodeRow
	for _, id := range ids {
		if n, ok := f.nodes[id]; ok {
			out = append(out, n)
		}
	}
	return out, nil
}

func (f *fakeStore) SearchBrainNodes(_ context.Context, _, _ string, _ int) ([]store.BrainNodeRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.searchN++
	return f.results, nil
}

func (f *fakeStore) UpsertBrainEdges(_ context.Context, e []store.BrainEdgeRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.edges = append(f.edges, e...)
	return nil
}

func (f *fakeStore) BrainNeighbors(_ context.Context, id string, limit int) ([]store.BrainEdgeRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []store.BrainEdgeRow
	for _, e := range f.edges {
		switch id {
		case e.Src:
			out = append(out, e)
		case e.Dst:
			out = append(out, store.BrainEdgeRow{Src: e.Dst, Dst: e.Src, Kind: e.Kind, Weight: e.Weight})
		}
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// fakeLLM answers the two prompts this package sends. It tells them apart the
// way a reader would: only the relation pass mentions a knowledge graph.
type fakeLLM struct {
	mu        sync.Mutex
	distil    string
	relate    string
	distilErr error
	relateErr error
	calls     int
}

func (f *fakeLLM) Complete(_ context.Context, _ llm.Class, req llm.Request) (llm.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if strings.Contains(req.System, "knowledge graph") {
		if f.relateErr != nil {
			return llm.Response{}, f.relateErr
		}
		return llm.Response{Structured: json.RawMessage(f.relate), Provider: "agy", Model: "test-model"}, nil
	}
	if f.distilErr != nil {
		return llm.Response{}, f.distilErr
	}
	return llm.Response{Structured: json.RawMessage(f.distil), Provider: "agy", Model: "test-model"}, nil
}

const goodDistil = `{"title":"Mimir store","assessment":"Yerel SQLite deposu, WAL modunda acilir ve FTS5 ile arama yapar.","tags":["sqlite","fts5","wal"],"aliases":["full-text-search","bm25"]}`

func testCore(t *testing.T, st Store, model Completer) *Core {
	t.Helper()
	cfg := config.Load()
	return New(cfg, st, model)
}

// --- identity ----------------------------------------------------------------

func TestNodeID_IsIdentityNotTime(t *testing.T) {
	a := NodeID("/p", KindRepo, "owner/name")
	time.Sleep(2 * time.Millisecond)
	b := NodeID("/p", KindRepo, "owner/name")
	if a != b {
		t.Fatalf("the same source produced two ids: %s vs %s", a, b)
	}
	if NodeID("/q", KindRepo, "owner/name") == a {
		t.Error("a different project produced the same id")
	}
	if NodeID("/p", KindNote, "owner/name") == a {
		t.Error("a different kind produced the same id")
	}
}

// --- ingest ------------------------------------------------------------------

func TestIngest_StoresAndDistils(t *testing.T) {
	st := newFakeStore()
	model := &fakeLLM{distil: goodDistil, relate: `{"related":[]}`}
	c := testCore(t, st, model)

	res, err := c.Ingest(context.Background(), Input{
		Source: "store", Kind: KindNote, Content: "SQLite WAL and FTS5.", ProjectPath: "/p",
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if !res.Distilled {
		t.Fatalf("Distilled = false, note %q", res.Note)
	}

	stored, ok := st.nodes[NodeID("/p", KindNote, "store")]
	if !ok {
		t.Fatal("the node was not stored under its identity")
	}
	if stored.Title != "Mimir store" {
		t.Errorf("title = %q", stored.Title)
	}
	if got := strings.Join(stored.Tags, ","); got != "sqlite,fts5,wal" {
		t.Errorf("tags = %q", got)
	}
	if got := strings.Join(stored.Aliases, ","); got != "full-text-search,bm25" {
		t.Errorf("aliases = %q", got)
	}
	if stored.Provider != "agy" || stored.Model != "test-model" {
		t.Errorf("provenance = %s/%s, want the provider that answered", stored.Provider, stored.Model)
	}
}

// The invariant the first version claimed and broke: a failed distil costs the
// assessment, never the node.
func TestIngest_StoresTheNodeWhenNoProviderAnswers(t *testing.T) {
	st := newFakeStore()
	model := &fakeLLM{
		distilErr: llm.ErrProviderUnavailable,
		relateErr: llm.ErrProviderUnavailable,
	}
	c := testCore(t, st, model)

	res, err := c.Ingest(context.Background(), Input{
		Source: "store", Content: "# Heading\nSome body.", ProjectPath: "/p",
	})
	if err != nil {
		t.Fatalf("a failed distil must not fail the ingest: %v", err)
	}
	if res.Distilled {
		t.Error("Distilled = true with no provider")
	}
	if res.Note == "" {
		t.Error("a rejected distil has to be reported, not swallowed")
	}

	stored, ok := st.nodes[NodeID("/p", KindNote, "store")]
	if !ok {
		t.Fatal("the node was not stored")
	}
	if stored.Assessment != "" {
		t.Errorf("assessment = %q, want empty", stored.Assessment)
	}
	if stored.Title == "" {
		t.Error("a node nothing can name is a node nothing will pick out of a list")
	}
}

func TestIngest_RejectsUnusableInput(t *testing.T) {
	c := testCore(t, newFakeStore(), &fakeLLM{distil: goodDistil, relate: `{"related":[]}`})
	ctx := context.Background()

	cases := []struct {
		name string
		in   Input
	}{
		{"no source", Input{Content: "x"}},
		{"no content", Input{Source: "s"}},
		{"unknown kind", Input{Source: "s", Content: "x", Kind: "sideways"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := c.Ingest(ctx, tc.in); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestCore_WithoutAStore(t *testing.T) {
	c := New(config.Load(), nil, &fakeLLM{})
	if c.Available() {
		t.Fatal("Available() with no store")
	}
	if _, err := c.Ingest(context.Background(), Input{Source: "s", Content: "c"}); !errors.Is(err, ErrNoStore) {
		t.Errorf("Ingest err = %v, want ErrNoStore", err)
	}
	if _, err := c.Search(context.Background(), "/p", "q", 5); !errors.Is(err, ErrNoStore) {
		t.Errorf("Search err = %v, want ErrNoStore", err)
	}
}

// --- linking -----------------------------------------------------------------

func TestRelate_TagEdgesSurviveALostRelationPass(t *testing.T) {
	st := newFakeStore()
	st.results = []store.BrainNodeRow{
		{ID: "near", Title: "Neighbour", Kind: KindNote, Tags: []string{"sqlite", "fts5", "wal"}},
		{ID: "far", Title: "Stranger", Kind: KindNote, Tags: []string{"tauri", "rust"}},
	}
	model := &fakeLLM{distil: goodDistil, relateErr: llm.ErrProviderUnavailable}
	c := testCore(t, st, model)

	res, err := c.Ingest(context.Background(), Input{Source: "store", Content: "body", ProjectPath: "/p"})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.Linked != 1 {
		t.Fatalf("Linked = %d, want only the tag-overlapping neighbour", res.Linked)
	}
	if len(st.edges) != 1 || st.edges[0].Dst != "near" || st.edges[0].Kind != "tag" {
		t.Fatalf("edges = %+v, want one tag edge to near", st.edges)
	}
	if res.Note == "" {
		t.Error("a skipped relation pass has to be reported")
	}
}

func TestRelate_SemanticVerdictOutranksTagOverlap(t *testing.T) {
	st := newFakeStore()
	st.results = []store.BrainNodeRow{
		{ID: "near", Title: "Neighbour", Kind: KindNote, Tags: []string{"sqlite", "fts5", "wal"}},
	}
	model := &fakeLLM{distil: goodDistil, relate: `{"related":[{"id":"near","weight":0.95,"why":"same subsystem"}]}`}
	c := testCore(t, st, model)

	if _, err := c.Ingest(context.Background(), Input{Source: "store", Content: "body", ProjectPath: "/p"}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(st.edges) != 1 {
		t.Fatalf("edges = %+v, want one", st.edges)
	}
	if st.edges[0].Kind != "semantic" || st.edges[0].Weight != 0.95 {
		t.Errorf("edge = %+v, want the semantic verdict to win", st.edges[0])
	}
}

// An id the model invented would create an edge to a node that does not exist,
// which the neighbour resolver then silently drops forever.
func TestSemanticEdges_IgnoresInventedIDsAndOutOfRangeWeights(t *testing.T) {
	st := newFakeStore()
	candidates := []store.BrainNodeRow{
		{ID: "real", Title: "Real", Kind: KindNote, Tags: []string{"x"}},
	}
	model := &fakeLLM{relate: `{"related":[
		{"id":"hallucinated","weight":0.9},
		{"id":"real","weight":7},
		{"id":"real","weight":0.01}
	]}`}
	c := testCore(t, st, model)

	got, err := c.semanticEdges(context.Background(),
		store.BrainNodeRow{ID: "self", Title: "Self", Tags: []string{"x"}}, candidates)
	if err != nil {
		t.Fatalf("semanticEdges: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("kept %d edges, want none: %+v", len(got), got)
	}
}

func TestRelate_HonoursTheNeighbourCap(t *testing.T) {
	st := newFakeStore()
	related := make([]relateVerdict, 0, 20)
	for i := 0; i < 20; i++ {
		id := string(rune('a'+i)) + "-node"
		st.results = append(st.results, store.BrainNodeRow{ID: id, Title: id, Kind: KindNote})
		related = append(related, relateVerdict{ID: id, Weight: 0.9})
	}
	payload, _ := json.Marshal(relateOutput{Related: related})
	model := &fakeLLM{distil: goodDistil, relate: string(payload)}

	cfg := config.Load()
	c := New(cfg, st, model)

	res, err := c.Ingest(context.Background(), Input{Source: "store", Content: "body", ProjectPath: "/p"})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.Linked != cfg.BrainNeighborCap {
		t.Errorf("Linked = %d, want the cap %d", res.Linked, cfg.BrainNeighborCap)
	}
}

func TestJaccard(t *testing.T) {
	cases := []struct {
		a, b []string
		want float64
	}{
		{[]string{"a", "b", "c"}, []string{"a", "b", "c"}, 1},
		{[]string{"a", "b"}, []string{"c", "d"}, 0},
		{[]string{"a", "b"}, []string{"b", "c"}, 1.0 / 3.0},
		{nil, []string{"a"}, 0},
	}
	for _, tc := range cases {
		if got := jaccard(termSet(tc.a), termSet(tc.b)); got != tc.want {
			t.Errorf("jaccard(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// --- the distil contract -----------------------------------------------------

func TestValidateDistilled_Rejections(t *testing.T) {
	cases := []struct {
		name string
		in   distilled
	}{
		{"empty title", distilled{Assessment: "fine", Tags: []string{"a"}}},
		{"empty assessment", distilled{Title: "T", Tags: []string{"a"}}},
		{"no usable tags", distilled{Title: "T", Assessment: "fine", Tags: []string{"", "x"}}},
		{"meta-commentary", distilled{Title: "T", Assessment: "I'm sorry, let me rewrite that.", Tags: []string{"aa"}}},
		{"stuck decoder", distilled{
			Title:      "T",
			Assessment: strings.Repeat("the same fragment repeated again and again ", 12),
			Tags:       []string{"aa"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := tc.in
			if err := validateDistilled(&d, "plain ascii source text"); err == nil {
				t.Fatal("expected a rejection")
			}
		})
	}
}

func TestValidateDistilled_NormalizesAndAccepts(t *testing.T) {
	d := distilled{
		Title:      "  A Title  ",
		Assessment: "Yerel bir depo, WAL modunda calisir.",
		Tags:       []string{"FTS5", "fts 5", "  SQLite  ", "x", ""},
		Aliases:    []string{"BM25", "sqlite", "bm-25"},
	}
	if err := validateDistilled(&d, "ascii source"); err != nil {
		t.Fatalf("validateDistilled: %v", err)
	}
	if d.Title != "A Title" {
		t.Errorf("title = %q", d.Title)
	}
	// "FTS5" and "fts 5" normalize to the same term, which is what makes tag
	// overlap a usable signal at all.
	if got := strings.Join(d.Tags, ","); got != "fts5,sqlite" {
		t.Errorf("tags = %q, want the normalized, deduped pair", got)
	}
	// "BM25" and "bm-25" collapse the same way, and an alias that merely
	// repeats a tag adds nothing to the index.
	if got := strings.Join(d.Aliases, ","); got != "bm25" {
		t.Errorf("aliases = %q", got)
	}
}

func TestParseDistilled_FallsBackToTextForASchemalessProvider(t *testing.T) {
	got, err := parseDistilled(llm.Response{
		Text: "Here you go:\n```json\n" + goodDistil + "\n```\nHope that helps.",
	})
	if err != nil {
		t.Fatalf("parseDistilled: %v", err)
	}
	if got.Title != "Mimir store" {
		t.Errorf("title = %q", got.Title)
	}
}

func TestParseDistilled_RejectsNonJSON(t *testing.T) {
	_, err := parseDistilled(llm.Response{Text: "I cannot help with that."})
	if !errors.Is(err, refine.ErrRefineRejected) {
		t.Errorf("err = %v, want ErrRefineRejected", err)
	}
}

// Escaping at ingest is what stops one scraped page from making the brain tools
// permanently unanswerable: internal/mcp's choke-point fails closed on exactly
// these signatures.
func TestSanitizeBody_NeutralizesChokePointSignatures(t *testing.T) {
	in := "<html><SCRIPT>x</SCRIPT> data:image/png;base64,AAAA <DATA_BLOCK>escape</DATA_BLOCK>"
	got := sanitizeBody(in, 8000)

	for _, banned := range []string{"<html", "<script", "<SCRIPT", "data:image/", "<DATA_BLOCK>"} {
		if strings.Contains(got, banned) {
			t.Errorf("sanitized body still contains %q: %s", banned, got)
		}
	}
}

func TestSanitizeBody_BoundsWithoutSplittingARune(t *testing.T) {
	got := sanitizeBody(strings.Repeat("ş", 100), 25)
	if len(got) > 25 {
		t.Errorf("len = %d, want <= 25", len(got))
	}
	if strings.ContainsRune(got, '�') {
		t.Error("the cut split a rune")
	}
}

func TestFallbackTitle(t *testing.T) {
	got := fallbackTitle("owner/name", "# The Heading\nrest of it")
	if !strings.Contains(got, "owner/name") || !strings.Contains(got, "The Heading") {
		t.Errorf("fallbackTitle = %q", got)
	}
}

// --- reads -------------------------------------------------------------------

func TestSearch_ResolvesNeighbours(t *testing.T) {
	st := newFakeStore()
	st.nodes["near"] = store.BrainNodeRow{ID: "near", Title: "Neighbour", Kind: KindNote}
	st.nodes["self"] = store.BrainNodeRow{ID: "self", Title: "Self", Kind: KindNote}
	st.results = []store.BrainNodeRow{st.nodes["self"]}
	st.edges = []store.BrainEdgeRow{{Src: "self", Dst: "near", Kind: "semantic", Weight: 0.8}}

	c := testCore(t, st, &fakeLLM{})
	got, err := c.Search(context.Background(), "/p", "self", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d nodes, want 1", len(got))
	}
	if len(got[0].Neighbors) != 1 || got[0].Neighbors[0].Title != "Neighbour" {
		t.Errorf("neighbours = %+v", got[0].Neighbors)
	}
}

// The view is what leaves the package, and the body is deliberately not in it.
func TestNodeViewOmitsTheBody(t *testing.T) {
	v := toView(store.BrainNodeRow{ID: "n", Body: "SECRET-BODY-MARKER"})
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "SECRET-BODY-MARKER") {
		t.Errorf("the node body reached the wire: %s", b)
	}
}

func TestNormalizeRepo(t *testing.T) {
	cases := map[string]string{
		"owner/name":                          "owner/name",
		"https://github.com/owner/name":       "owner/name",
		"https://github.com/owner/name.git":   "owner/name",
		"github.com/owner/name/tree/main/sub": "owner/name",
	}
	for in, want := range cases {
		got, err := normalizeRepo(in)
		if err != nil {
			t.Errorf("normalizeRepo(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("normalizeRepo(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := normalizeRepo("nameonly"); err == nil {
		t.Error("a bare name must not parse as a repository")
	}
}

// --- the budget ---------------------------------------------------------------

// The choke-point rejects an over-budget response rather than truncating it, so
// a result set that outgrew its ceiling would not arrive shortened — it would
// not arrive at all, and only on the queries that matched the most. Eight nodes
// with real assessments clear the ceiling on their own, so the default limit
// alone is enough to trigger this.
func TestSearch_FitsTheBudgetTheChokePointEnforces(t *testing.T) {
	cfg := config.Load()
	st := newFakeStore()

	for i := 0; i < cfg.BrainSearchLimit; i++ {
		id := "node-" + strings.Repeat("x", 12) + string(rune('a'+i))
		row := store.BrainNodeRow{
			ID: id, Kind: KindDecision, SourceKey: "source-" + id,
			Title:      "A reasonably long node title of the kind a distiller writes",
			Assessment: strings.Repeat("Bu duguemun degerlendirmesi epey uzun bir paragraf. ", 12),
			Tags:       []string{"sqlite", "fts5", "bm25", "wal", "migration"},
		}
		st.nodes[id] = row
		st.results = append(st.results, row)
		for j := 0; j < cfg.BrainNeighborCap; j++ {
			st.edges = append(st.edges, store.BrainEdgeRow{
				Src: id, Dst: st.results[0].ID, Kind: "semantic", Weight: 0.9,
			})
		}
	}

	// Prove the fitter is load-bearing rather than decorative: the unfitted
	// list is over the ceiling, so without it this response would be rejected
	// outright.
	unfitted := make([]NodeView, 0, len(st.results))
	for _, r := range st.results {
		v := toView(r)
		v.Neighbors = make([]NeighborView, cfg.BrainNeighborCap)
		unfitted = append(unfitted, v)
	}
	if raw, _ := json.Marshal(unfitted); len(raw)/4 <= cfg.BrainSearchMaxTokens {
		t.Fatalf("the fixture is too small to exercise the budget: ~%d tokens against a %d ceiling",
			len(raw)/4, cfg.BrainSearchMaxTokens)
	}

	c := New(cfg, st, &fakeLLM{})
	got, err := c.Search(context.Background(), "/p", "sqlite", cfg.BrainSearchLimit)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("everything was dropped; the floor is one node")
	}

	// The same estimate internal/mcp/finalize.go applies, on the same shape the
	// tool wraps.
	resp := struct {
		Query   string     `json:"query"`
		Nodes   []NodeView `json:"nodes"`
		Refined bool       `json:"refined"`
	}{"sqlite", got, true}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	if tokens := len(b) / 4; tokens > cfg.BrainSearchMaxTokens {
		t.Fatalf("response is ~%d tokens, over the %d budget the choke-point would reject it at",
			tokens, cfg.BrainSearchMaxTokens)
	}
	// The best match has to keep its assessment, or the list has stopped
	// answering the question.
	if got[0].Assessment == "" {
		t.Error("the top result lost its assessment before anything else was dropped")
	}
}

func TestFitToBudget_FloorIsOneNode(t *testing.T) {
	huge := NodeView{
		ID: "n", Title: "t",
		Assessment: strings.Repeat("uzun ", 5000),
		Neighbors:  []NeighborView{{ID: "x", Title: "y"}},
	}
	got := fitToBudget([]NodeView{huge, huge, huge}, 100)
	if len(got) != 1 {
		t.Fatalf("got %d nodes, want the floor of 1", len(got))
	}
	if got[0].Assessment != "" || got[0].Neighbors != nil {
		t.Errorf("the floor still carries droppable material: %+v", got[0])
	}
}
