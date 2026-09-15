package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/logrenant/mimir/internal/account"
	"github.com/logrenant/mimir/internal/agents"
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

// handleStartLogin connects the one Claude account.
//
// It runs `claude auth login` against Mimir's own credential slot and opens
// the authorization page in a private browser window — private because a
// normal one carries whatever Claude session is already signed in there, and
// the page then never asks which account is connecting.
//
// It answers as soon as the window is open. The operator is in a browser at
// that point, so the client follows GET /accounts/login rather than holding a
// request open for as long as a sign-in takes.
func (s *Server) handleStartLogin(w http.ResponseWriter, r *http.Request) {
	state, err := s.deps.Accounts.StartLogin(r.Context())
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, state)
}

// handleLoginState reports the attempt in flight, or the last one's outcome.
//
// A finished login is also the moment the queue can move again: runs released
// while nothing was connected are sitting there with no slot to claim, and
// nothing else pumps on an account appearing. The client polls this route
// throughout a login, so the kick rides along with the answer it is already
// waiting for.
func (s *Server) handleLoginState(w http.ResponseWriter, r *http.Request) {
	state := s.deps.Accounts.LoginState()
	if state.State == account.LoginDone && s.deps.Runner != nil {
		if err := s.deps.Runner.Kick(r.Context()); err != nil {
			slog.Warn("kicking the queue after a login", "error", err)
		}
	}
	writeJSON(w, http.StatusOK, state)
}

type loginCodeRequest struct {
	Code string `json:"code"`
}

// handleLoginCode answers the CLI's paste prompt.
//
// Only reachable in the flow the CLI falls back to when it could not open a
// browser itself. The code is a single-use authorization code, not a
// credential: it is typed straight into the waiting process and never stored.
func (s *Server) handleLoginCode(w http.ResponseWriter, r *http.Request) {
	var req loginCodeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.deps.Accounts.SubmitCode(req.Code); err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleResetAccounts signs the slot out and forgets it.
//
// The only thing that disconnects. The slot survives a quit — a launch runs
// account.Restore, not Reset — so this route is the operator's deliberate act
// and nothing else calls it: signing out, and switching to another Anthropic
// account, which is the same thing done twice.
func (s *Server) handleResetAccounts(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Accounts.Reset(r.Context()); err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAccountStatus asks the CLI who is signed in to the slot.
//
// It spends nothing — `claude auth status` reads a keychain entry and prints
// JSON — which is why this is a live probe rather than something cached at
// login. A slot whose login has lapsed is a slot whose runs will fail, and the
// operator should see that on the account, not one run at a time.
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

	// Agent is one of the keys GET /agents offers. Absent or empty means the
	// default sub-agent, which is what every card written before sub-agents
	// existed was — so a client that predates them keeps working unchanged.
	Agent string `json:"agent"`

	// Params is the sub-agent executor's own input, an opaque JSON object.
	// Opaque here on purpose: this handler is a door, not a floor, and
	// validating an executor's input would put that executor's knowledge in
	// the HTTP layer.
	Params json.RawMessage `json:"params"`
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
	// project_id is required only for a sub-agent that works inside a folder.
	// Demanding one for the rest would make the operator register a directory
	// to run something that never opens it — and the runner, which owns the
	// agent registry, is where that question is actually answered.
	if strings.TrimSpace(req.ProjectID) == "" && needsProject(req.Agent) {
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
		Agent:         req.Agent,
		Params:        string(req.Params),
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

// handleKickQueue asks the dispatcher to look at the queue again.
//
// The queue is pumped when work is released and when a run frees its slot, so
// nothing pumps it when the *account* is what changed. A queue that filled up
// with nothing connected therefore kept waiting after the login that could
// drain it, and the board had no way to say so. This is that missing edge: the
// login poll calls it when an attempt lands, and a card that has sat in Queued
// offers it as a button.
//
// It answers 409 with the registry's own words when nothing is connected,
// because "why is this card not starting" is the question being asked.
func (s *Server) handleKickQueue(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Runner.Kick(r.Context()); err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleQueueLimits answers "why is nothing running, and when will it be?".
//
// The pause the dispatcher is holding right now, plus the durable log behind
// it. A spent token budget is the one interruption an operator can neither fix
// nor retry their way out of, so what they need is not a button but the time it
// ends — and the record of how often it has been happening.
//
// Registered beside the kick because it is the same question asked the other
// way round: the kick says "go", this says "here is why it will not".
func (s *Server) handleQueueLimits(w http.ResponseWriter, r *http.Request) {
	limit, err := graphLimit(r.URL.Query().Get("limit"), 50, 500)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, err.Error())
		return
	}
	report, err := s.deps.Runner.Limits(r.Context(), limit)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// editCodingTaskRequest is a patch: an absent field is one the client did not
// touch. Pointers rather than empty-means-unchanged, because "" is a real value
// for a title an operator is clearing.
type editCodingTaskRequest struct {
	Title         *string   `json:"title"`
	Prompt        *string   `json:"prompt"`
	Model         *string   `json:"model"`
	AttachmentIDs *[]string `json:"attachment_ids"`
	// Agent and Params are editable under the same guard as the prompt: until
	// tokens have been spent, what a card asks for is still the operator's to
	// change. store.EditableStatuses covers both for free.
	Agent  *string          `json:"agent"`
	Params *json.RawMessage `json:"params"`
}

// handleEditCodingTask rewrites what a card asks for.
//
// PATCH rather than PUT for the reason above: the board sends the fields its
// form owns, and a client that only renames a card should not have to send the
// prompt back to keep it.
//
// A card that is running or finished answers 409. That is not a permission
// check — it is the same rule the rest of this package keeps: what a run was
// asked is the record of what was spent, and the transcript beside it has to
// stay an answer to the prompt above it.
func (s *Server) handleEditCodingTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "run id is required")
		return
	}
	var req editCodingTaskRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	// The one field that cannot be blanked. Checked here, like create's, so it
	// is a 400 about the request rather than a 500 about the daemon.
	if req.Prompt != nil && strings.TrimSpace(*req.Prompt) == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "prompt is required")
		return
	}
	edit := coderunner.EditRequest{
		Title:         req.Title,
		Prompt:        req.Prompt,
		Model:         req.Model,
		Agent:         req.Agent,
		AttachmentIDs: req.AttachmentIDs,
	}
	if req.Params != nil {
		params := string(*req.Params)
		edit.Params = &params
	}
	run, err := s.deps.Runner.Edit(r.Context(), id, edit)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

type retryCodingTaskRequest struct {
	// Fresh asks for the task to be started over instead of continued. Absent
	// means continue, because that is the point of retrying a run that already
	// did half the work — and a client that sends no body at all gets it.
	Fresh bool `json:"fresh"`
}

// handleRetryCodingTask puts a failed or stopped run back in the queue.
//
// Separate from enqueue for the same reason enqueue is separate from create:
// releasing a card nobody has spent anything on and picking a run back up where
// it broke are different decisions, and the second one resumes a session the
// first has none of.
func (s *Server) handleRetryCodingTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "run id is required")
		return
	}
	var req retryCodingTaskRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	run, err := s.deps.Runner.Retry(r.Context(), id, req.Fresh)
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
	// An absent project_id used to be an error, because internal/store only
	// indexed runs by project and there was nothing to answer with. There is
	// now — and it is required rather than convenient, since a worker-lane
	// card belongs to no project and would be invisible to every per-project
	// query the board could make.
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))

	var runs []coderunner.Run
	var err error
	if projectID == "" {
		runs, err = s.deps.Runner.ListAll(r.Context(), 0)
	} else {
		runs, err = s.deps.Runner.List(r.Context(), projectID, 0)
	}
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

// needsProject asks the agent catalogue whether a card must name a folder.
// Here rather than in the runner because it is a 400 about the request, and the
// operator should be told before the card exists rather than after.
func needsProject(agentKey string) bool {
	a, ok := agents.Lookup(strings.TrimSpace(agentKey))
	if !ok {
		a, _ = agents.Lookup(agents.Default)
	}
	return a.NeedsProject
}
