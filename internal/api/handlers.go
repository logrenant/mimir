package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/logrenant/goat-mcp/internal/coderunner"
	goatmcp "github.com/logrenant/goat-mcp/internal/mcp"
	"github.com/logrenant/goat-mcp/internal/project"
	"github.com/logrenant/goat-mcp/internal/store"
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

type healthzResponse struct {
	OK       bool   `json:"ok"`
	Version  string `json:"version"`
	UptimeMs int64  `json:"uptime_ms"`
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthzResponse{
		OK:       true,
		Version:  goatmcp.Version,
		UptimeMs: time.Since(s.started).Milliseconds(),
	})
}

type daemonHealth struct {
	OK               bool                  `json:"ok"`
	Version          string                `json:"version"`
	UptimeMs         int64                 `json:"uptime_ms"`
	Store            string                `json:"store"`
	Projects         int                   `json:"projects"`
	PlacesConfigured bool                  `json:"places_configured"`
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
		Version:          goatmcp.Version,
		UptimeMs:         time.Since(s.started).Milliseconds(),
		Store:            "ok",
		PlacesConfigured: s.cfg.PlacesAPIKey != "",
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

type startCodingTaskRequest struct {
	ProjectID string `json:"project_id"`
	Prompt    string `json:"prompt"`
}

// handleStartCodingTask returns as soon as the run is recorded and the CLI is
// spawned — 202, not 200. A coding session runs for minutes; the response
// carries the id a watcher subscribes with, and the run outlives this request
// by design (coderunner.New takes the daemon's lifetime, not the request's).
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

	run, err := s.deps.Runner.Start(r.Context(), req.ProjectID, req.Prompt)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
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
