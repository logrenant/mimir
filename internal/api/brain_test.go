package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/brain"
	"github.com/logrenant/mimir/internal/store"
)

// --- fakes -------------------------------------------------------------------

type fakeScanner struct {
	status brain.ScanStatus
	events []brain.ScanEvent
	paused bool
	woken  int
}

func (f *fakeScanner) Status() brain.ScanStatus {
	s := f.status
	s.Paused = f.paused
	if f.paused {
		s.Phase = brain.PhasePaused
	}
	return s
}

func (f *fakeScanner) Events(after int64, limit int) ([]brain.ScanEvent, int64) {
	var out []brain.ScanEvent
	for _, e := range f.events {
		if e.Seq > after {
			out = append(out, e)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	var seq int64
	for _, e := range f.events {
		seq = e.Seq
	}
	return out, seq
}

func (f *fakeScanner) Pause()  { f.paused = true }
func (f *fakeScanner) Resume() { f.paused = false }

func (f *fakeScanner) ScanNow() bool {
	if f.paused {
		return false
	}
	f.woken++
	return true
}

type fakeGraphStore struct {
	ranked       []store.BrainNodeDegree
	nodes        []store.BrainNodeRow
	edges        []store.BrainEdgeRow
	projects     []store.BrainProjectCount
	versions     []store.BrainNodeVersion
	lastPath     string
	limit        int
	versionLimit int
}

func (f *fakeGraphStore) BrainGraphIDs(_ context.Context, projectPath string, limit int) ([]store.BrainNodeDegree, error) {
	f.lastPath = projectPath
	f.limit = limit
	if limit < len(f.ranked) {
		return f.ranked[:limit], nil
	}
	return f.ranked, nil
}

func (f *fakeGraphStore) BrainNodesByIDs(_ context.Context, ids []string) ([]store.BrainNodeRow, error) {
	want := map[string]struct{}{}
	for _, id := range ids {
		want[id] = struct{}{}
	}
	var out []store.BrainNodeRow
	for _, n := range f.nodes {
		if _, ok := want[n.ID]; ok {
			out = append(out, n)
		}
	}
	return out, nil
}

func (f *fakeGraphStore) BrainEdgesAmong(context.Context, []string, int) ([]store.BrainEdgeRow, error) {
	return f.edges, nil
}

func (f *fakeGraphStore) BrainProjects(context.Context) ([]store.BrainProjectCount, error) {
	return f.projects, nil
}

func (f *fakeGraphStore) BrainNodeVersions(_ context.Context, _ string, limit int) ([]store.BrainNodeVersion, error) {
	f.versionLimit = limit
	if limit < len(f.versions) {
		return f.versions[:limit], nil
	}
	return f.versions, nil
}

type fakeBrainReader struct {
	node brain.NodeView
	err  error
}

func (f *fakeBrainReader) Related(context.Context, string, int) (brain.NodeView, error) {
	return f.node, f.err
}

func graphDeps() (Deps, *fakeGraphStore, *fakeScanner) {
	gs := &fakeGraphStore{
		ranked: []store.BrainNodeDegree{{ID: "n1", Degree: 3}, {ID: "n2", Degree: 1}},
		nodes: []store.BrainNodeRow{
			{ID: "n1", Kind: "file", Title: "scan.go", ProjectPath: "/repo", Tags: []string{"scan"}, UpdatedAt: time.Now()},
			{ID: "n2", Kind: "note", Title: "a decision", ProjectPath: "/repo", UpdatedAt: time.Now()},
		},
		edges: []store.BrainEdgeRow{
			{Src: "n1", Dst: "n2", Kind: "tag", Weight: 0.7},
			// A dangling edge: the store promises this cannot happen, and the
			// handler drops it anyway — the thing that breaks is a canvas.
			{Src: "n1", Dst: "ghost", Kind: "semantic", Weight: 0.9},
		},
		projects: []store.BrainProjectCount{
			{ProjectPath: "/repo", Nodes: 2, Files: 1, UpdatedAt: time.Now()},
			{ProjectPath: "", Nodes: 1, UpdatedAt: time.Now()},
		},
	}
	sc := &fakeScanner{
		status: brain.ScanStatus{Phase: brain.PhaseScanning, Roots: []string{"/roots"}},
		events: []brain.ScanEvent{
			{Seq: 1, Kind: brain.EventSweep, Text: "tur başladı"},
			{Seq: 2, Kind: brain.EventFile, Project: "/repo", Text: "internal/brain/scan.go"},
			{Seq: 3, Kind: brain.EventPass, Project: "/repo", Text: "12 damıtıldı"},
		},
	}
	return Deps{BrainScan: sc, BrainGraph: gs, Brain: &fakeBrainReader{node: brain.NodeView{ID: "n1", Title: "scan.go"}}}, gs, sc
}

// --- tests -------------------------------------------------------------------

func TestBrain_RoutesUnregisteredWithoutTheirDeps(t *testing.T) {
	h := New(testConfig(), Deps{}).Handler()

	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/brain/scan"},
		{http.MethodPost, "/brain/scan/pause"},
		{http.MethodPost, "/brain/scan/now"},
		{http.MethodGet, "/brain/graph"},
		{http.MethodGet, "/brain/projects"},
		{http.MethodGet, "/brain/nodes/n1"},
	} {
		w := do(h, c.method, c.path, testToken, "")
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s without its dep = %d, want 404", c.method, c.path, w.Code)
		}
	}
}

func TestBrainScan_PauseResumeRoundTrip(t *testing.T) {
	deps, _, sc := graphDeps()
	h := New(testConfig(), deps).Handler()

	if w := do(h, http.MethodPost, "/brain/scan/pause", testToken, ""); w.Code != http.StatusOK {
		t.Fatalf("pause = %d, body %s", w.Code, w.Body.String())
	}
	if !sc.paused {
		t.Fatal("pause did not reach the supervisor")
	}

	// A button drawn before the pause must not restart the work quietly.
	w := do(h, http.MethodPost, "/brain/scan/now", testToken, "")
	if w.Code != http.StatusConflict {
		t.Fatalf("scan/now while paused = %d, want 409", w.Code)
	}

	if w := do(h, http.MethodPost, "/brain/scan/resume", testToken, ""); w.Code != http.StatusOK {
		t.Fatalf("resume = %d", w.Code)
	}
	if w := do(h, http.MethodPost, "/brain/scan/now", testToken, ""); w.Code != http.StatusAccepted {
		t.Fatalf("scan/now = %d, want 202", w.Code)
	}
	if sc.woken != 1 {
		t.Errorf("the supervisor was woken %d times, want 1", sc.woken)
	}
}

// The console is a tail, not a dump: a tab that has been open for an hour asks
// for what it is missing.
func TestBrainScanLog_ReturnsOnlyWhatIsNewerThanTheCallersSeq(t *testing.T) {
	deps, _, _ := graphDeps()
	h := New(testConfig(), deps).Handler()

	w := do(h, http.MethodGet, "/brain/scan/log", testToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("log = %d, body %s", w.Code, w.Body.String())
	}
	var first scanLogResponse
	if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Events) != 3 || first.Seq != 3 {
		t.Fatalf("first read = %+v", first)
	}

	w = do(h, http.MethodGet, "/brain/scan/log?after=2", testToken, "")
	var next scanLogResponse
	if err := json.Unmarshal(w.Body.Bytes(), &next); err != nil {
		t.Fatal(err)
	}
	if len(next.Events) != 1 || next.Events[0].Seq != 3 {
		t.Errorf("after=2 returned %+v", next.Events)
	}

	// A caught-up reader gets an empty array, never null: the client renders it.
	w = do(h, http.MethodGet, "/brain/scan/log?after=3", testToken, "")
	if body := w.Body.String(); !strings.Contains(body, `"events":[]`) {
		t.Errorf("a caught-up read is not an empty array: %s", body)
	}

	for _, bad := range []string{"after=abc", "after=-1"} {
		if w := do(h, http.MethodGet, "/brain/scan/log?"+bad, testToken, ""); w.Code != http.StatusBadRequest {
			t.Errorf("?%s = %d, want 400", bad, w.Code)
		}
	}
}

func TestBrainScanStatus_IsAnObjectWithItsRoots(t *testing.T) {
	deps, _, _ := graphDeps()
	h := New(testConfig(), deps).Handler()

	w := do(h, http.MethodGet, "/brain/scan", testToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got scanStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not a status: %v (%s)", err, w.Body.String())
	}
	if got.Scan.Phase != brain.PhaseScanning || len(got.Scan.Roots) != 1 {
		t.Errorf("status = %+v", got.Scan)
	}
}

// A force layout handed an endpoint it was never given draws a phantom or
// throws, and the failure lands in a canvas with no message.
func TestBrainGraph_NeverReturnsAnEdgeWithoutBothEndpoints(t *testing.T) {
	deps, _, _ := graphDeps()
	h := New(testConfig(), deps).Handler()

	w := do(h, http.MethodGet, "/brain/graph", testToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("graph = %d, body %s", w.Code, w.Body.String())
	}
	var got graphResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(got.Nodes))
	}
	if len(got.Edges) != 1 {
		t.Fatalf("edges = %+v, want only the one with both ends present", got.Edges)
	}
	if got.Nodes[0].Degree != 3 {
		t.Errorf("degree did not survive: %+v", got.Nodes[0])
	}
}

func TestBrainGraph_LimitIsClampedAndAnInvalidLimitIs400(t *testing.T) {
	deps, gs, _ := graphDeps()
	cfg := testConfig()
	cfg.BrainGraphMaxNodes = 10
	cfg.BrainGraphDefaultNodes = 5
	h := New(cfg, deps).Handler()

	if w := do(h, http.MethodGet, "/brain/graph?limit=9999", testToken, ""); w.Code != http.StatusOK {
		t.Fatalf("clamped limit = %d", w.Code)
	}
	if gs.limit != 10 {
		t.Errorf("limit = %d, want the ceiling 10", gs.limit)
	}

	if w := do(h, http.MethodGet, "/brain/graph", testToken, ""); w.Code != http.StatusOK || gs.limit != 5 {
		t.Errorf("default limit = %d (status %d), want 5", gs.limit, w.Code)
	}

	for _, bad := range []string{"limit=abc", "limit=0", "limit=-4"} {
		w := do(h, http.MethodGet, "/brain/graph?"+bad, testToken, "")
		if w.Code != http.StatusBadRequest {
			t.Errorf("?%s = %d, want 400", bad, w.Code)
		}
	}
}

// The rule this keeps honest: a filesystem path is accepted at exactly two
// routes on this daemon, and the graph is not one of them.
func TestBrainGraph_ProjectIsAnIDNotAPath(t *testing.T) {
	deps, gs, _ := graphDeps()
	h := New(testConfig(), deps).Handler()

	w := do(h, http.MethodGet, "/brain/graph?project=%2FUsers%2Flogrenant%2Fdevelopment", testToken, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("a path as ?project = %d, want 400", w.Code)
	}
	if env := decodeError(t, w); env.Error.Code != codeBadRequest {
		t.Errorf("error code = %q", env.Error.Code)
	}

	w = do(h, http.MethodGet, "/brain/graph?project=proj-doesnotexist", testToken, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("an unknown id = %d, want 404", w.Code)
	}

	id := brain.ProjectID("/repo")
	if w := do(h, http.MethodGet, "/brain/graph?project="+id, testToken, ""); w.Code != http.StatusOK {
		t.Fatalf("a known id = %d, body %s", w.Code, w.Body.String())
	}
	if gs.lastPath != "/repo" {
		t.Errorf("the id resolved to %q, want /repo", gs.lastPath)
	}
}

func TestBrainProjects_LabelsTheGlobalScope(t *testing.T) {
	deps, _, _ := graphDeps()
	h := New(testConfig(), deps).Handler()

	w := do(h, http.MethodGet, "/brain/projects", testToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("projects = %d", w.Code)
	}
	var got brainProjectsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Projects) != 2 {
		t.Fatalf("projects = %+v", got.Projects)
	}
	var labels []string
	for _, p := range got.Projects {
		labels = append(labels, p.Label)
		if p.ID == "" {
			t.Error("a project came back without an id, so the graph cannot be filtered by it")
		}
	}
	if labels[0] != "repo" || labels[1] != "global" {
		t.Errorf("labels = %v", labels)
	}
}

func TestBrainNode_UnknownIDIs404(t *testing.T) {
	deps, _, _ := graphDeps()
	deps.Brain = &fakeBrainReader{err: brain.ErrNodeNotFound}
	h := New(testConfig(), deps).Handler()

	w := do(h, http.MethodGet, "/brain/nodes/nope", testToken, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown node = %d, want 404: %s", w.Code, w.Body.String())
	}
}

// --- version history -------------------------------------------------------

func twoVersions() []store.BrainNodeVersion {
	now := time.Unix(1_700_000_000, 0).UTC()
	return []store.BrainNodeVersion{
		{ContentHash: "hash-2", SeenAt: now, Title: "scan.go", Assessment: "yeni okuma", SizeBytes: 900},
		{ContentHash: "hash-1", SeenAt: now.Add(-time.Hour), Title: "scan.go", Assessment: "eski okuma", SizeBytes: 800},
	}
}

func TestBrainNodeVersions_ServesTheHistory(t *testing.T) {
	deps, gs, _ := graphDeps()
	gs.versions = twoVersions()
	h := New(testConfig(), deps).Handler()

	w := do(h, http.MethodGet, "/brain/nodes/n1/versions", testToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "eski okuma") {
		t.Errorf("the superseded reading must be readable: %s", w.Body.String())
	}
	if gs.versionLimit != testConfig().BrainVersionsPerNode {
		t.Errorf("want the configured bound, got %d", gs.versionLimit)
	}
}

// History rides node detail so the panel does not fetch an empty list for
// every file on the machine — and a node with one version has no history worth
// showing.
func TestBrainNode_CarriesHistoryOnlyWhenThereIsSome(t *testing.T) {
	deps, gs, _ := graphDeps()
	gs.versions = twoVersions()
	h := New(testConfig(), deps).Handler()

	w := do(h, http.MethodGet, "/brain/nodes/n1", testToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (%s)", w.Code, w.Body.String())
	}
	var got brainNodeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Versions) != 2 {
		t.Fatalf("want 2 inline versions, got %d", len(got.Versions))
	}

	gs.versions = twoVersions()[:1]
	w = do(h, http.MethodGet, "/brain/nodes/n1", testToken, "")
	got = brainNodeResponse{}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Versions) != 0 {
		t.Errorf("a single version is not a history: %+v", got.Versions)
	}
}
