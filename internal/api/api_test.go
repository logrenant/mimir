package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/coderunner"
	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/project"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

const testToken = "parent-provided-token"

// --- fakes -----------------------------------------------------------------

type fakeProjects struct {
	calls    int
	project  project.Project
	list     []project.Project
	err      error
	lastPath string
}

func (f *fakeProjects) Register(_ context.Context, path string) (project.Project, error) {
	f.calls++
	f.lastPath = path
	return f.project, f.err
}

func (f *fakeProjects) List(context.Context) ([]project.Project, error) {
	f.calls++
	return f.list, f.err
}

func (f *fakeProjects) Get(context.Context, string) (project.Project, error) {
	f.calls++
	return f.project, f.err
}

type fakeRunner struct {
	calls         int
	run           coderunner.Run
	list          []coderunner.Run
	err           error
	lastPrompt    string
	lastProjectID string
	lastCreate    coderunner.CreateRequest
	created       bool
	enqueued      string
	retried       string
	retriedFresh  bool
	edited        string
	lastEdit      coderunner.EditRequest
	stopped       string
	kicked        int
	kickErr       error
	limits        coderunner.LimitReport
	limitsErr     error
	deleted       string
	attachment    coderunner.Attachment
	attachmentRaw []byte
}

func (f *fakeRunner) Start(_ context.Context, req coderunner.CreateRequest) (coderunner.Run, error) {
	f.calls++
	f.lastPrompt = req.Prompt
	f.lastCreate = req
	f.run.ProjectID = req.ProjectID
	return f.run, f.err
}

func (f *fakeRunner) Create(_ context.Context, req coderunner.CreateRequest) (coderunner.Run, error) {
	f.calls++
	f.created = true
	f.lastPrompt = req.Prompt
	f.lastCreate = req
	f.run.ProjectID = req.ProjectID
	return f.run, f.err
}

func (f *fakeRunner) Enqueue(_ context.Context, runID string) (coderunner.Run, error) {
	f.calls++
	f.enqueued = runID
	return f.run, f.err
}

func (f *fakeRunner) Retry(_ context.Context, runID string, fresh bool) (coderunner.Run, error) {
	f.calls++
	f.retried = runID
	f.retriedFresh = fresh
	return f.run, f.err
}

func (f *fakeRunner) Edit(_ context.Context, runID string, req coderunner.EditRequest) (coderunner.Run, error) {
	f.calls++
	f.edited = runID
	f.lastEdit = req
	return f.run, f.err
}

func (f *fakeRunner) Kick(context.Context) error {
	f.calls++
	f.kicked++
	return f.kickErr
}

func (f *fakeRunner) Limits(context.Context, int) (coderunner.LimitReport, error) {
	f.calls++
	return f.limits, f.limitsErr
}

func (f *fakeRunner) Stop(_ context.Context, runID string) (coderunner.Run, error) {
	f.calls++
	f.stopped = runID
	return f.run, f.err
}

func (f *fakeRunner) Delete(_ context.Context, runID string) error {
	f.calls++
	f.deleted = runID
	return f.err
}

func (f *fakeRunner) SaveAttachment(filename string, data []byte) (coderunner.Attachment, error) {
	f.calls++
	f.attachmentRaw = data
	f.attachment.Filename = filename
	f.attachment.Bytes = len(data)
	return f.attachment, f.err
}

func (f *fakeRunner) LoadAttachment(string) (coderunner.Attachment, []byte, error) {
	f.calls++
	return f.attachment, f.attachmentRaw, f.err
}

func (f *fakeRunner) Get(context.Context, string) (coderunner.Run, error) {
	f.calls++
	return f.run, f.err
}

func (f *fakeRunner) List(_ context.Context, projectID string, _ int) ([]coderunner.Run, error) {
	f.calls++
	f.lastProjectID = projectID
	return f.list, f.err
}

// ListAll answers the cross-project read. lastProjectID stays empty, which is
// how a test tells the two paths apart.
func (f *fakeRunner) ListAll(_ context.Context, _ int) ([]coderunner.Run, error) {
	f.calls++
	f.lastProjectID = ""
	return f.list, f.err
}

type fakeHealther struct{ err error }

func (f fakeHealther) Health(context.Context) error { return f.err }

func testConfig() config.Config {
	cfg := config.Load()
	cfg.DaemonAuthToken = testToken
	return cfg
}

// do issues a request against h with a loopback peer and, unless token is
// empty, an Authorization header.
func do(h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	r.RemoteAddr = "127.0.0.1:54321"
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func decodeError(t *testing.T, w *httptest.ResponseRecorder) errorEnvelope {
	t.Helper()
	var env errorEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("response is not an error envelope: %v (%s)", err, w.Body.String())
	}
	return env
}

// --- auth and peer guards --------------------------------------------------

func TestAuth_RejectsAnythingButTheExactToken(t *testing.T) {
	cases := []struct {
		name   string
		header string
	}{
		{"no header", ""},
		{"wrong token", "Bearer wrong-token"},
		{"empty bearer", "Bearer "},
		{"prefix of the real token", "Bearer " + testToken[:5]},
		{"token without the scheme", testToken},
		{"wrong scheme", "Basic " + testToken},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			projects := &fakeProjects{}
			h := New(testConfig(), Deps{Projects: projects}).Handler()

			r := httptest.NewRequest(http.MethodGet, "/projects", nil)
			r.RemoteAddr = "127.0.0.1:54321"
			if tc.header != "" {
				r.Header.Set("Authorization", tc.header)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)

			if w.Code != http.StatusUnauthorized {
				t.Errorf("status: got %d, want 401", w.Code)
			}
			// The point of the check is that the handler is never reached.
			if projects.calls != 0 {
				t.Errorf("handler ran despite a rejected request (%d calls)", projects.calls)
			}
			if got := decodeError(t, w).Error.Code; got != codeUnauthorized {
				t.Errorf("code: got %q, want %q", got, codeUnauthorized)
			}
		})
	}
}

// A Server built with no token must not turn into an open server. Startup
// already refuses this state (config.ValidateDaemon); this keeps it true one
// layer down.
func TestAuth_EmptyConfiguredTokenRejectsEveryone(t *testing.T) {
	cfg := testConfig()
	cfg.DaemonAuthToken = ""
	h := New(cfg, Deps{Projects: &fakeProjects{}}).Handler()

	for _, presented := range []string{"", "anything"} {
		w := do(h, http.MethodGet, "/projects", presented, "")
		if w.Code != http.StatusUnauthorized {
			t.Errorf("token %q: got %d, want 401", presented, w.Code)
		}
	}
}

func TestGuard_RejectsNonLoopbackPeer(t *testing.T) {
	projects := &fakeProjects{}
	h := New(testConfig(), Deps{Projects: projects}).Handler()

	r := httptest.NewRequest(http.MethodGet, "/projects", nil)
	r.RemoteAddr = "203.0.113.5:44444" // a routable address, correct token
	r.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403", w.Code)
	}
	if projects.calls != 0 {
		t.Errorf("handler ran for a non-loopback peer")
	}
}

func TestGuard_RunsBeforeAuth(t *testing.T) {
	// A remote caller must not even be told whether its token was right.
	h := New(testConfig(), Deps{Projects: &fakeProjects{}}).Handler()

	r := httptest.NewRequest(http.MethodGet, "/projects", nil)
	r.RemoteAddr = "203.0.113.5:44444"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403 (the peer check must come first)", w.Code)
	}
}

func TestNoCORSHeaders(t *testing.T) {
	h := New(testConfig(), Deps{Projects: &fakeProjects{}}).Handler()
	w := do(h, http.MethodGet, "/healthz", testToken, "")

	for _, header := range []string{
		"Access-Control-Allow-Origin",
		"Access-Control-Allow-Credentials",
		"Access-Control-Allow-Headers",
	} {
		if got := w.Header().Get(header); got != "" {
			t.Errorf("%s must not be set, got %q", header, got)
		}
	}
}

// --- projects --------------------------------------------------------------

func TestRegisterProject_MapsGuardFailuresTo400(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
		code string
	}{
		{"invalid path", fmt.Errorf("%w: /nope", project.ErrInvalidPath), http.StatusBadRequest, codeBadRequest},
		{"too broad", fmt.Errorf("%w: /", project.ErrPathNotAllowed), http.StatusBadRequest, codeBadRequest},
		{"not found", fmt.Errorf("%w: x", project.ErrProjectNotFound), http.StatusNotFound, codeNotFound},
		{"unknown", errors.New("disk on fire"), http.StatusInternalServerError, codeInternal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := New(testConfig(), Deps{Projects: &fakeProjects{err: tc.err}}).Handler()
			w := do(h, http.MethodPost, "/projects", testToken, `{"path":"/tmp/x"}`)

			if w.Code != tc.want {
				t.Fatalf("status: got %d, want %d", w.Code, tc.want)
			}
			env := decodeError(t, w)
			if env.Error.Code != tc.code {
				t.Errorf("code: got %q, want %q", env.Error.Code, tc.code)
			}
			// An unrecognised internal failure must not leak its text.
			if tc.name == "unknown" && strings.Contains(env.Error.Message, "disk on fire") {
				t.Errorf("internal error text leaked into the response: %q", env.Error.Message)
			}
		})
	}
}

func TestRegisterProject_RejectsEmptyAndMalformedBodies(t *testing.T) {
	cases := []struct{ name, body string }{
		{"empty path", `{"path":""}`},
		{"whitespace path", `{"path":"   "}`},
		{"not json", `nope`},
		{"unknown field", `{"projectPath":"/tmp/x"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			projects := &fakeProjects{}
			h := New(testConfig(), Deps{Projects: projects}).Handler()
			w := do(h, http.MethodPost, "/projects", testToken, tc.body)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status: got %d, want 400", w.Code)
			}
			if projects.calls != 0 {
				t.Errorf("registry was called with a rejected body")
			}
		})
	}
}

func TestListProjects_EmptyIsAnArrayNotNull(t *testing.T) {
	h := New(testConfig(), Deps{Projects: &fakeProjects{}}).Handler()
	w := do(h, http.MethodGet, "/projects", testToken, "")

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"projects":[]`) {
		t.Errorf("empty list should encode as [], got %s", w.Body.String())
	}
}

// --- coding tasks ----------------------------------------------------------

func TestStartCodingTask_RequiresProjectIDAndPrompt(t *testing.T) {
	cases := []struct{ name, body string }{
		{"no project", `{"prompt":"do it"}`},
		{"no prompt", `{"project_id":"abc"}`},
		{"blank prompt", `{"project_id":"abc","prompt":"  "}`},
		{"a raw path instead of an id", `{"path":"/tmp","prompt":"do it"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{}
			h := New(testConfig(), Deps{Runner: runner}).Handler()
			w := do(h, http.MethodPost, "/coding-tasks", testToken, tc.body)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status: got %d, want 400", w.Code)
			}
			if runner.calls != 0 {
				t.Errorf("runner was started from a rejected request")
			}
		})
	}
}

func TestStartCodingTask_UnknownProjectIs404(t *testing.T) {
	runner := &fakeRunner{err: fmt.Errorf("%w: zzz", project.ErrProjectNotFound)}
	h := New(testConfig(), Deps{Runner: runner}).Handler()
	w := do(h, http.MethodPost, "/coding-tasks", testToken, `{"project_id":"zzz","prompt":"go"}`)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", w.Code)
	}
}

func TestGetCodingTask_UnknownRunIs404(t *testing.T) {
	runner := &fakeRunner{err: fmt.Errorf("%w: zzz", coderunner.ErrRunNotFound)}
	h := New(testConfig(), Deps{Runner: runner}).Handler()
	w := do(h, http.MethodGet, "/coding-tasks/zzz", testToken, "")

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", w.Code)
	}
}

// An absent project_id used to be a 400, because internal/store only indexed
// runs by project and there was nothing to answer with. It is now the board's
// own read: a card on the worker lane belongs to no project, so no per-project
// query could ever have shown it.
func TestListCodingTasks_WithoutAProjectIDReturnsEveryProjectsCards(t *testing.T) {
	runner := &fakeRunner{list: []coderunner.Run{
		{ID: "r1", ProjectID: "proj-1", Agent: "coding"},
		{ID: "r2", ProjectID: "", Agent: "leadgen"},
	}}
	h := New(testConfig(), Deps{Runner: runner}).Handler()
	w := do(h, http.MethodGet, "/coding-tasks", testToken, "")

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	if runner.lastProjectID != "" {
		t.Errorf("the per-project read answered a cross-project request")
	}

	var got codingTaskListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Runs) != 2 {
		t.Fatalf("got %d cards, want both", len(got.Runs))
	}
	if got.Runs[1].ProjectID != "" {
		t.Error("a card with no project did not survive the round trip")
	}
}

func TestListCodingTasks_ReturnsTheProjectsRuns(t *testing.T) {
	runner := &fakeRunner{list: []coderunner.Run{
		{ID: "r1", ProjectID: "proj-1", Status: "completed"},
		{ID: "r2", ProjectID: "proj-1", Status: "running"},
	}}
	h := New(testConfig(), Deps{Runner: runner}).Handler()
	w := do(h, http.MethodGet, "/coding-tasks?project_id=proj-1", testToken, "")

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	if runner.lastProjectID != "proj-1" {
		t.Errorf("project_id: got %q, want %q", runner.lastProjectID, "proj-1")
	}

	var resp codingTaskListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(resp.Runs) != 2 {
		t.Fatalf("runs: got %d, want 2", len(resp.Runs))
	}
}

func TestListCodingTasks_EmptyListEncodesAsEmptyArray(t *testing.T) {
	runner := &fakeRunner{}
	h := New(testConfig(), Deps{Runner: runner}).Handler()
	w := do(h, http.MethodGet, "/coding-tasks?project_id=proj-1", testToken, "")

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	if strings.TrimSpace(w.Body.String()) != `{"runs":[]}` {
		t.Errorf("empty list should encode as [], got %s", w.Body.String())
	}
}

// --- diagnostics and health ------------------------------------------------

func TestDiagnostics_ReportsAnUnhealthyStoreWithout500ing(t *testing.T) {
	h := New(testConfig(), Deps{
		Projects: &fakeProjects{},
		Store:    fakeHealther{err: errors.New("database is locked")},
	}).Handler()

	w := do(h, http.MethodGet, "/diagnostics", testToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 — a health report is not an error", w.Code)
	}

	var resp diagnosticsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if resp.Daemon.OK {
		t.Error("daemon reported ok with a broken store")
	}
	if !strings.Contains(resp.Daemon.Store, "locked") {
		t.Errorf("store detail: got %q, want the underlying cause", resp.Daemon.Store)
	}
}

func TestDiagnostics_ReportsPlacesConfiguredFromConfig(t *testing.T) {
	cfg := testConfig()
	cfg.PlacesAPIKey = "operator-key"
	h := New(cfg, Deps{Projects: &fakeProjects{}, Store: fakeHealther{}}).Handler()

	w := do(h, http.MethodGet, "/diagnostics", testToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp diagnosticsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Daemon.PlacesConfigured {
		t.Error("places_configured should be true when a key is set")
	}
}

func TestHealthz(t *testing.T) {
	h := New(testConfig(), Deps{}).Handler()
	w := do(h, http.MethodGet, "/healthz", testToken, "")

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	var resp healthzResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if !resp.OK || resp.Version == "" {
		t.Errorf("unexpected health response: %+v", resp)
	}
}

// --- middleware ------------------------------------------------------------

func TestPanicInAHandlerBecomesA500(t *testing.T) {
	h := New(testConfig(), Deps{Projects: &panickingProjects{}}).Handler()
	w := do(h, http.MethodGet, "/projects", testToken, "")

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500", w.Code)
	}
}

type panickingProjects struct{ fakeProjects }

func (*panickingProjects) List(context.Context) ([]project.Project, error) {
	panic("boom")
}

func TestBodyLimit_RejectsAnOversizedRequest(t *testing.T) {
	cfg := testConfig()
	cfg.DaemonMaxRequestBytes = 64
	h := New(cfg, Deps{Runner: &fakeRunner{}}).Handler()

	body := `{"project_id":"abc","prompt":"` + strings.Repeat("x", 4096) + `"}`
	w := do(h, http.MethodPost, "/coding-tasks", testToken, body)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
}

// --- lifecycle -------------------------------------------------------------

func TestServe_StopsOnContextCancelAndReleasesThePort(t *testing.T) {
	cfg := testConfig()
	cfg.DaemonShutdownTimeout = 2 * time.Second

	srv := New(cfg, Deps{})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()

	// Give Serve time to bind before cancelling, so this exercises shutdown
	// rather than a race against startup.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after its context was cancelled")
	}
}

func TestServe_ReportsAnUnusableAddress(t *testing.T) {
	cfg := testConfig()
	cfg.DaemonHost = "203.0.113.5" // not an address on this machine

	err := New(cfg, Deps{}).Serve(context.Background())
	if err == nil {
		t.Fatal("expected an error binding an unusable address")
	}
	if !strings.Contains(err.Error(), "203.0.113.5") {
		t.Errorf("the error should name the address it tried: %v", err)
	}
}
