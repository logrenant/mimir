package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/account"
	"github.com/logrenant/mimir/internal/brain"
	"github.com/logrenant/mimir/internal/coderunner"
	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/crawl"
	"github.com/logrenant/mimir/internal/events"
	"github.com/logrenant/mimir/internal/llm"
	mimirmcp "github.com/logrenant/mimir/internal/mcp"
	"github.com/logrenant/mimir/internal/pipeline"
	"github.com/logrenant/mimir/internal/project"
	"github.com/logrenant/mimir/internal/refine"
	"github.com/logrenant/mimir/internal/search"
	"github.com/logrenant/mimir/internal/store"
	"github.com/logrenant/mimir/internal/tools"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The canned stream a fake `claude` emits. Same shape as the one captured from
// the real CLI in internal/coderunner's tests; no tokens are spent here either.
const (
	lineInit   = `{"type":"system","subtype":"init","session_id":"sess-1","model":"claude-sonnet-5"}`
	lineText   = `{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"working"}]}}`
	lineResult = `{"is_error":false,"subtype":"success","type":"result","session_id":"sess-1","total_cost_usd":0.01,"num_turns":1,"duration_ms":12,"result":"done"}`
)

// writeFakeClaude writes a stand-in for the CLI. If gate is non-empty the
// script emits its first line, then blocks until that file appears — which is
// what lets a test observe a run that is genuinely mid-flight, with some
// events already on disk and the rest still to come, rather than one that
// raced to finish.
func writeFakeClaude(t *testing.T, gate string) string {
	t.Helper()

	var b strings.Builder
	b.WriteString("#!/bin/sh\ncat >/dev/null\n")
	for i, line := range []string{lineInit, lineText, lineResult} {
		b.WriteString("echo '" + line + "'\n")
		if i == 0 && gate != "" {
			b.WriteString("while [ ! -f '" + gate + "' ]; do sleep 0.02; done\n")
		}
	}
	b.WriteString("exit 0\n")

	path := filepath.Join(t.TempDir(), "fake-claude.sh")
	if err := os.WriteFile(path, []byte(b.String()), 0o755); err != nil {
		t.Fatalf("writing fake claude: %v", err)
	}
	return path
}

// live wires the real runtime packages — store, registry, runner, MCP registry
// — behind a real HTTP server. Only the `claude` binary and the network are
// faked.
type live struct {
	url     string
	client  *http.Client
	runner  *coderunner.Runner
	cfg     config.Config
	workdir string
}

func newLive(t *testing.T, claudePath string) *live {
	t.Helper()

	tmp := t.TempDir()
	cfg := config.Load()
	cfg.StorePath = filepath.Join(tmp, "mimir.db")
	cfg.TranscriptDir = filepath.Join(tmp, "transcripts")
	cfg.ClaudeCLIPath = claudePath
	cfg.CodingRunTimeout = 30 * time.Second
	cfg.DaemonAuthToken = testToken
	// The canonical tool set is credential-gated (task-24), so an operator who
	// happens to have a Places key exported must not change what this test
	// sees. The gate itself is covered in internal/tools.
	cfg.PlacesAPIKey = ""

	ctx, cancel := context.WithCancel(context.Background())

	db, err := store.Open(ctx, cfg)
	if err != nil {
		cancel()
		t.Fatalf("store.Open: %v", err)
	}

	workdir := filepath.Join(tmp, "myproject")
	if err := os.Mkdir(workdir, 0o755); err != nil {
		cancel()
		t.Fatalf("Mkdir: %v", err)
	}

	searchClient := search.New(cfg)
	crawlClient := crawl.New(cfg)
	refineClient := refine.New(cfg)
	pipe := pipeline.New(cfg, searchClient, crawlClient, refineClient, db)

	mcpServer := mimirmcp.NewServer(cfg)
	if err := tools.RegisterAll(mcpServer.Registry(), cfg, tools.Deps{
		Search:   searchClient,
		Crawl:    crawlClient,
		Refine:   refineClient,
		Pipeline: pipe,
		// The node core follows the store the daemon's does, so the canonical
		// list below actually covers the brain tools instead of silently
		// dropping them the moment they became conditional.
		Brain: brain.New(cfg, db, llm.NewRouter(cfg)), BrainHashes: db,
	}); err != nil {
		cancel()
		t.Fatalf("RegisterAll: %v", err)
	}

	bus := events.NewBus()
	registry := project.NewRegistry(db)
	runner := coderunner.New(ctx, cfg, bus, registry, account.NewRegistry(db), db)

	srv := httptest.NewServer(New(cfg, Deps{
		Projects:    registry,
		Runner:      runner,
		Store:       db,
		MCP:         mcpServer.MCPHandler(),
		Diagnostics: tools.NewDiagnostics(cfg, crawlClient, refineClient, searchClient, nil),
		Events:      bus,
		Transcripts: runner,
	}).Handler())

	t.Cleanup(func() {
		srv.Close()
		mcpServer.CloseMCPSessions()
		runner.Wait()
		bus.Close()
		cancel()
		_ = db.Close()
	})

	return &live{url: srv.URL, client: srv.Client(), runner: runner, cfg: cfg, workdir: workdir}
}

func (l *live) request(t *testing.T, method, path, body string) (*http.Response, []byte) {
	t.Helper()

	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	var req *http.Request
	var err error
	if reader == nil {
		req, err = http.NewRequest(method, l.url+path, nil)
	} else {
		req, err = http.NewRequest(method, l.url+path, reader)
	}
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)

	resp, err := l.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	return resp, buf
}

// A registered path stops being a path: the id is what every later call uses,
// and registering the same directory twice is one project, not two.
func TestLive_RegisterProjectIsIdempotentAndGuarded(t *testing.T) {
	l := newLive(t, writeFakeClaude(t, ""))

	resp, body := l.request(t, http.MethodPost, "/projects",
		`{"path":`+quote(l.workdir)+`}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status: got %d, want 201 (%s)", resp.StatusCode, body)
	}

	var first project.Project
	if err := json.Unmarshal(body, &first); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if first.ID == "" {
		t.Fatal("no project id returned")
	}

	_, body = l.request(t, http.MethodPost, "/projects", `{"path":`+quote(l.workdir)+`}`)
	var second project.Project
	if err := json.Unmarshal(body, &second); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("re-registering the same directory made a second project: %q vs %q",
			second.ID, first.ID)
	}

	resp, _ = l.request(t, http.MethodGet, "/projects", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("listing: got %d, want 200", resp.StatusCode)
	}
}

// The guards in internal/project must be reachable through the HTTP surface,
// not just in unit tests: this is the route a picker in the desktop app hits.
func TestLive_RegisterProjectRefusesBroadRoots(t *testing.T) {
	l := newLive(t, writeFakeClaude(t, ""))

	file := filepath.Join(l.workdir, "not-a-dir.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	for _, path := range []string{"/", os.Getenv("HOME"), file, "relative/path"} {
		if path == "" {
			continue
		}
		resp, body := l.request(t, http.MethodPost, "/projects", `{"path":`+quote(path)+`}`)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("path %q: got %d, want 400 (%s)", path, resp.StatusCode, body)
		}
	}
}

// POST /coding-tasks must answer while the run is still going. The fake CLI is
// gated so the assertion cannot pass by racing a run that already finished.
func TestLive_StartCodingTaskAnswersBeforeTheRunFinishes(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "release")
	l := newLive(t, writeFakeClaude(t, gate))

	_, body := l.request(t, http.MethodPost, "/projects", `{"path":`+quote(l.workdir)+`}`)
	var proj project.Project
	if err := json.Unmarshal(body, &proj); err != nil {
		t.Fatalf("decoding project: %v", err)
	}

	resp, body := l.request(t, http.MethodPost, "/coding-tasks",
		`{"project_id":`+quote(proj.ID)+`,"prompt":"add a test"}`)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202 (%s)", resp.StatusCode, body)
	}

	var run coderunner.Run
	if err := json.Unmarshal(body, &run); err != nil {
		t.Fatalf("decoding run: %v", err)
	}
	if run.ID == "" {
		t.Fatal("no run id returned")
	}
	if run.Status != store.RunStatusRunning {
		t.Errorf("status: got %q, want %q — the response must not wait for the run",
			run.Status, store.RunStatusRunning)
	}

	// Still in flight, and already findable.
	resp, body = l.request(t, http.MethodGet, "/coding-tasks/"+run.ID, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET run: got %d, want 200 (%s)", resp.StatusCode, body)
	}

	// Let the fake finish, then confirm the outcome was recorded.
	if err := os.WriteFile(gate, []byte("go"), 0o600); err != nil {
		t.Fatalf("releasing the fake CLI: %v", err)
	}
	l.runner.Wait()

	_, body = l.request(t, http.MethodGet, "/coding-tasks/"+run.ID, "")
	var final coderunner.Run
	if err := json.Unmarshal(body, &final); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if final.Status != store.RunStatusCompleted {
		t.Errorf("final status: got %q, want %q", final.Status, store.RunStatusCompleted)
	}
	if final.SessionID != "sess-1" {
		t.Errorf("session id: got %q, want sess-1", final.SessionID)
	}
}

// /mcp is the same registry cmd/mimir-mcp serves over stdio. If this list and
// that binary's ever diverge, "two transports, one engine" has stopped being
// true — which is the whole reason tools.RegisterAll exists.
func TestLive_MCPRouteExposesTheCanonicalToolSet(t *testing.T) {
	l := newLive(t, writeFakeClaude(t, ""))

	client := sdk.NewClient(&sdk.Implementation{Name: "api-test", Version: "0"}, nil)
	transport := &sdk.StreamableClientTransport{
		Endpoint:             l.url + "/mcp",
		HTTPClient:           &http.Client{Transport: bearerTransport{}},
		DisableStandaloneSSE: true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connecting over /mcp: %v", err)
	}
	defer func() { _ = session.Close() }()

	res, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}

	got := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		got = append(got, tool.Name)
	}
	sort.Strings(got)

	// The keyless set. maps_search is in it: region search no longer follows
	// the Places credential, because its primary source is a local scrape that
	// needs none (see TestRegisterAll_MapsSearchIsOfferedWithoutACredential).
	want := []string{
		"brain_ingest_data", "brain_ingest_github", "brain_query_nodes",
		"brain_related", "brain_scan_repo", "diagnostics", "ecommerce_product_lookup",
		"fetch_page", "gmaps_business_lookup", "instagram_profile_lookup",
		"maps_search", "research", "tiktok_profile_lookup", "web_search",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("tool set over HTTP:\n got %v\nwant %v", got, want)
	}
}

// The MCP route is behind the same chain as everything else — an unauthenticated
// client must not be able to call a tool.
func TestLive_MCPRouteRequiresTheToken(t *testing.T) {
	l := newLive(t, writeFakeClaude(t, ""))

	req, err := http.NewRequest(http.MethodPost, l.url+"/mcp", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	resp, err := l.client.Do(req)
	if err != nil {
		t.Fatalf("POST /mcp: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", resp.StatusCode)
	}
}

type bearerTransport struct{}

func (bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("Authorization", "Bearer "+testToken)
	return http.DefaultTransport.RoundTrip(r)
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
