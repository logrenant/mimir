package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/logrenant/mimir/internal/account"
	"github.com/logrenant/mimir/internal/coderunner"
	mimirmcp "github.com/logrenant/mimir/internal/mcp"
	"github.com/logrenant/mimir/internal/project"
	"github.com/logrenant/mimir/internal/store"
)

// decodeJSON reads a request body into v, rejecting anything unexpected rather
// than silently ignoring it — a client sending "projectId" instead of
// "project_id" should be told, not handed a confusing validation error later.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

// decodeOptionalJSON is decodeJSON for a request whose body is allowed to be
// absent.
//
// An empty body decodes to EOF, which decodeJSON rightly treats as malformed —
// there, a missing body means a missing request. Here it means "no override",
// and the endpoint has a defined answer for that, so the zero value is handed
// back and the caller carries on. Unknown fields are still refused: a typo in
// an optional body must not be read as having sent nothing.
func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return true
		}
		writeError(w, http.StatusBadRequest, codeBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

type healthzResponse struct {
	OK       bool   `json:"ok"`
	Version  string `json:"version"`
	UptimeMs int64  `json:"uptime_ms"`
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthzResponse{
		OK:       true,
		Version:  mimirmcp.Version,
		UptimeMs: time.Since(s.started).Milliseconds(),
	})
}

type daemonHealth struct {
	OK               bool   `json:"ok"`
	Version          string `json:"version"`
	UptimeMs         int64  `json:"uptime_ms"`
	Store            string `json:"store"`
	Projects         int    `json:"projects"`
	PlacesConfigured bool   `json:"places_configured"`
	// RegionSources names the region-search providers in the order they will
	// be tried, and RegionSearchFree says whether the first one spends
	// nothing. PlacesConfigured alone stopped describing this the moment the
	// free scrape became the primary: a machine with no key is not a machine
	// without region search.
	RegionSources    []string              `json:"region_sources"`
	RegionSearchFree bool                  `json:"region_search_free"`
	CodingRuns       *store.CodingRunStats `json:"coding_runs,omitempty"`
}

type diagnosticsResponse struct {
	Daemon       daemonHealth `json:"daemon"`
	Dependencies any          `json:"dependencies,omitempty"`
}

// handleDiagnostics answers with the daemon's own state plus the existing
// `diagnostics` tool payload, so there is one health story rather than two
// that can disagree.
func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	health := daemonHealth{
		OK:               true,
		Version:          mimirmcp.Version,
		UptimeMs:         time.Since(s.started).Milliseconds(),
		Store:            "ok",
		PlacesConfigured: s.cfg.PlacesAPIKey != "",
		RegionSources:    []string{},
	}
	if s.deps.Regions != nil {
		health.RegionSources = s.deps.Regions.Sources()
		health.RegionSearchFree = s.deps.Regions.Free()
	}

	if s.deps.Store != nil {
		if err := s.deps.Store.Health(r.Context()); err != nil {
			health.OK = false
			health.Store = err.Error()
		}
	}
	if s.deps.Projects != nil {
		if projects, err := s.deps.Projects.List(r.Context()); err == nil {
			health.Projects = len(projects)
		}
	}
	// Coding-run cost/usage rollup, when the store can supply it. A failing
	// probe is itself diagnostic — it must not 500 the endpoint.
	if rs, ok := s.deps.Store.(interface {
		CodingRunStats(context.Context) (store.CodingRunStats, error)
	}); ok {
		if stats, err := rs.CodingRunStats(r.Context()); err == nil {
			health.CodingRuns = &stats
		}
	}

	resp := diagnosticsResponse{Daemon: health}
	if s.deps.Diagnostics != nil {
		// A failing dependency probe is itself diagnostic information; it must
		// not turn the whole endpoint into a 500.
		if payload, err := s.deps.Diagnostics.Handle(r.Context(), json.RawMessage(`{}`)); err == nil {
			resp.Dependencies = payload
		} else {
			resp.Dependencies = errorDetail{Code: codeInternal, Message: err.Error()}
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

type projectListResponse struct {
	Projects []project.Project `json:"projects"`
}

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.deps.Projects.List(r.Context())
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	if projects == nil {
		projects = []project.Project{}
	}
	writeJSON(w, http.StatusOK, projectListResponse{Projects: projects})
}

type registerProjectRequest struct {
	Path string `json:"path"`
}

// handleRegisterProject is the only route that ever accepts a filesystem path,
// and it is the point where a path stops being one: everything afterwards
// takes the opaque id this returns. The picker in the desktop app produces the
// path; internal/project decides whether it may become a project at all.
func (s *Server) handleRegisterProject(w http.ResponseWriter, r *http.Request) {
	var req registerProjectRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "path is required")
		return
	}

	proj, err := s.deps.Projects.Register(r.Context(), req.Path)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, proj)
}

type accountListResponse struct {
	Accounts []account.Account `json:"accounts"`
}

func (s *Server) handleListAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.deps.Accounts.List(r.Context())
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	if accounts == nil {
		accounts = []account.Account{}
	}
	writeJSON(w, http.StatusOK, accountListResponse{Accounts: accounts})
}

// handleScanAccounts re-reads the accounts directory and registers what it
// finds.
//
// The daemon already scans at startup, so this is for the moment after the
// operator creates a slot and signs into it: they click refresh in the app
// rather than restarting a background service. Adding only — a row whose
// directory has since gone stays, because a run may be pinned to it and a
// failing probe is the honest report for it.
func (s *Server) handleScanAccounts(w http.ResponseWriter, r *http.Request) {
	accounts, err := s.deps.Accounts.Sync(r.Context(), account.Discover(s.cfg.ClaudeAccountsDir))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	if accounts == nil {
		accounts = []account.Account{}
	}
	writeJSON(w, http.StatusOK, accountListResponse{Accounts: accounts})
}

type registerAccountRequest struct {
	Label string `json:"label"`
	// ConfigDir is the second — and last — place this API accepts a filesystem
	// path. It is the same discipline as POST /projects and for the same
	// reason: the path is validated once here, and everything afterwards
	// carries the opaque id this returns. It is also not a secret. The CLI
	// hashes it to name a keychain entry; the credential itself never leaves
	// the keychain and Mimir never reads it.
	ConfigDir string `json:"config_dir"`
}

// handleRegisterAccount records a Claude Code credential slot.
//
// An empty config_dir registers the CLI's own default slot, which is what a
// single-account machine has always been using. Registration is idempotent by
// directory: the same path is the same identity, and a second row for it would
// let the dispatcher believe one account could run two tasks at once.
func (s *Server) handleRegisterAccount(w http.ResponseWriter, r *http.Request) {
	var req registerAccountRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	acct, err := s.deps.Accounts.Register(r.Context(), req.Label, req.ConfigDir)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, acct)
}

func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "account id is required")
		return
	}
	if err := s.deps.Accounts.Delete(r.Context(), id); err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAccountStatus asks the CLI who is signed in to one slot.
//
// It spends nothing — `claude auth status` reads a keychain entry and prints
// JSON — which is why this is a live probe rather than something cached at
// registration. A slot whose login has lapsed is a slot whose runs will fail,
// and the operator should see that on the account, not one run at a time.
func (s *Server) handleAccountStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "account id is required")
		return
	}
	acct, err := s.deps.Accounts.Get(r.Context(), id)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	status, err := account.Probe(r.Context(), s.cfg.ClaudeCLIPath, acct.ConfigDir)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

type startCodingTaskRequest struct {
	ProjectID     string   `json:"project_id"`
	Title         string   `json:"title"`
	Prompt        string   `json:"prompt"`
	AttachmentIDs []string `json:"attachment_ids"`

	// AccountID pins the run to one credential slot. Absent or empty means
	// "any free one", which is the default because it is what makes a second
	// account worth registering.
	AccountID string `json:"account_id"`

	// Model is one of the ids GET /coding-models offers. Absent or empty means
	// the daemon's default — a client that predates the picker keeps working.
	Model string `json:"model"`

	// Start distinguishes "run this now" from "put this on the board". A
	// pointer so its absence means the historical behaviour — a client that
	// predates the board still gets a run, not a card nobody releases.
	Start *bool `json:"start"`
}

// handleStartCodingTask returns as soon as the task is recorded — 202, not 200.
// A coding session runs for minutes; the response carries the id a watcher
// subscribes with, and the run outlives this request by design (coderunner.New
// takes the daemon's lifetime, not the request's).
//
// A queued task may also wait behind others: the runner's concurrency limit is
// what decides when it actually spawns, and 202 is honest about both cases.
func (s *Server) handleStartCodingTask(w http.ResponseWriter, r *http.Request) {
	var req startCodingTaskRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ProjectID) == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "project_id is required")
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "prompt is required")
		return
	}

	create := coderunner.CreateRequest{
		ProjectID:     req.ProjectID,
		Title:         req.Title,
		Prompt:        req.Prompt,
		AccountID:     req.AccountID,
		Model:         req.Model,
		AttachmentIDs: req.AttachmentIDs,
	}

	start := req.Start == nil || *req.Start
	var (
		run coderunner.Run
		err error
	)
	if start {
		run, err = s.deps.Runner.Start(r.Context(), create)
	} else {
		run, err = s.deps.Runner.Create(r.Context(), create)
	}
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

type codingModel struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Default bool   `json:"default"`
}

type codingModelListResponse struct {
	Models []codingModel `json:"models"`
}

// handleListCodingModels publishes the model allow-list so the desktop picker
// is a view of the daemon's constant rather than a second copy of it. Nothing
// here is per-operator or secret; it is the same list Validate checks against.
func (s *Server) handleListCodingModels(w http.ResponseWriter, r *http.Request) {
	models := make([]codingModel, 0, len(s.cfg.CodingModels))
	for _, m := range s.cfg.CodingModels {
		models = append(models, codingModel{
			ID:      m.ID,
			Label:   m.Label,
			Default: m.ID == s.cfg.CodingModel,
		})
	}
	writeJSON(w, http.StatusOK, codingModelListResponse{Models: models})
}

// handleEnqueueCodingTask releases a backlog task. Separate from the create
// route because they are different decisions: one writes intent down, the other
// spends tokens on it.
func (s *Server) handleEnqueueCodingTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "run id is required")
		return
	}
	run, err := s.deps.Runner.Enqueue(r.Context(), id)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

// handleStopCodingTask cancels a run.
//
// A route rather than a frame on /ws/runs/{id}: that socket is deliberately
// one-directional (internal/api/AGENTS.md), and a control channel would give it
// a second threat model. This one has the same one every other route has — the
// loopback guard and the bearer token — and its whole authority is to interrupt
// a process this daemon started itself.
func (s *Server) handleStopCodingTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "run id is required")
		return
	}
	run, err := s.deps.Runner.Stop(r.Context(), id)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleDeleteCodingTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "run id is required")
		return
	}
	if err := s.deps.Runner.Delete(r.Context(), id); err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type uploadAttachmentRequest struct {
	Filename   string `json:"filename"`
	DataBase64 string `json:"data_base64"`
}

type attachmentResponse struct {
	coderunner.Attachment
	DataBase64 string `json:"data_base64,omitempty"`
}

// handleUploadAttachment takes one image for a task prompt.
//
// Base64 in JSON rather than multipart because every other route here is JSON
// and the desktop app reaches the daemon through a Rust proxy that forwards a
// string body — one encoding for the whole surface is worth the 33% framing.
// The bytes are typed by the daemon, never by the client: see
// coderunner.SaveAttachment.
func (s *Server) handleUploadAttachment(w http.ResponseWriter, r *http.Request) {
	var req uploadAttachmentRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	data, err := base64.StdEncoding.DecodeString(req.DataBase64)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, "data_base64 is not valid base64")
		return
	}

	att, err := s.deps.Runner.SaveAttachment(req.Filename, data)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, attachmentResponse{Attachment: att})
}

// handleGetAttachment returns an image as base64 so a preview can be rendered
// from a data: URI. The desktop app's CSP allows data: images and does not
// allow http://127.0.0.1 ones, so this is the shape that can actually be shown.
func (s *Server) handleGetAttachment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "attachment id is required")
		return
	}
	att, data, err := s.deps.Runner.LoadAttachment(id)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, attachmentResponse{
		Attachment: att,
		DataBase64: base64.StdEncoding.EncodeToString(data),
	})
}

type codingTaskListResponse struct {
	Runs []coderunner.Run `json:"runs"`
}

// handleListCodingTasks is what the desktop app's board renders: a project's
// runs, most recent first. Project-scoped because internal/store only indexes
// runs by project — there is no cross-project query, so the desktop app calls
// this once per registered project and merges the results itself.
func (s *Server) handleListCodingTasks(w http.ResponseWriter, r *http.Request) {
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "project_id is required")
		return
	}

	runs, err := s.deps.Runner.List(r.Context(), projectID, 0)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	if runs == nil {
		runs = []coderunner.Run{}
	}
	writeJSON(w, http.StatusOK, codingTaskListResponse{Runs: runs})
}

func (s *Server) handleGetCodingTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "run id is required")
		return
	}

	run, err := s.deps.Runner.Get(r.Context(), id)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// compile-time assurance that the runtime types still satisfy what this
// package asks of them.
var _ CodeRunner = (*coderunner.Runner)(nil)
var _ ProjectRegistry = (*project.Registry)(nil)
var _ AccountRegistry = (*account.Registry)(nil)
