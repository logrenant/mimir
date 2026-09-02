// Package api is the daemon's HTTP surface: the door onto the runtime engine
// that internal/project, internal/coderunner and internal/events already built.
//
// It is deliberately small and deliberately local. The daemon binds loopback
// only, accepts one parent-provided bearer token, and serves a single operator
// — the Tauri shell that spawned it (docs/ROADMAP.md §B.2.1). There is no
// user model, no session, no TLS, because there is no second party.
//
// The MCP tool surface is not reimplemented here. /mcp is the SDK's streamable
// handler over the very same internal/mcp.Registry that cmd/mimir-mcp serves
// over stdio, so the finalize.go choke-point (SD-2) applies to an HTTP tool
// call exactly as it does to a stdio one.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/logrenant/mimir/internal/account"
	"github.com/logrenant/mimir/internal/coderunner"
	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/events"
	"github.com/logrenant/mimir/internal/leadgen"
	mimirmcp "github.com/logrenant/mimir/internal/mcp"
	"github.com/logrenant/mimir/internal/project"
)

// ProjectRegistry is the folder registry this API exposes. Note the contract
// inherited from internal/project: Register validates and guards a path, and
// the id it returns is the only thing a client passes afterwards.
type ProjectRegistry interface {
	Register(ctx context.Context, path string) (project.Project, error)
	List(ctx context.Context) ([]project.Project, error)
	Get(ctx context.Context, id string) (project.Project, error)
}

// AccountRegistry is the credential slots this API exposes. Same contract as
// ProjectRegistry: Register validates a directory and the id it returns is the
// only thing a client passes afterwards.
type AccountRegistry interface {
	Register(ctx context.Context, label, configDir string) (account.Account, error)
	List(ctx context.Context) ([]account.Account, error)
	Get(ctx context.Context, id string) (account.Account, error)
	Delete(ctx context.Context, id string) error
}

// CodeRunner owns the whole life of a coding task, not just its execution.
// Every state change a client can ask for is a method here, because
// internal/coderunner is the only thing allowed to decide that a run may begin
// (docs/ROADMAP.md §B.2.1) — this API asks, it does not schedule.
type CodeRunner interface {
	Create(ctx context.Context, req coderunner.CreateRequest) (coderunner.Run, error)
	Start(ctx context.Context, req coderunner.CreateRequest) (coderunner.Run, error)
	Enqueue(ctx context.Context, runID string) (coderunner.Run, error)
	Stop(ctx context.Context, runID string) (coderunner.Run, error)
	Delete(ctx context.Context, runID string) error
	Get(ctx context.Context, runID string) (coderunner.Run, error)
	List(ctx context.Context, projectID string, limit int) ([]coderunner.Run, error)
	SaveAttachment(filename string, data []byte) (coderunner.Attachment, error)
	LoadAttachment(id string) (coderunner.Attachment, []byte, error)
}

// Healther reports whether the store is usable. *store.Store satisfies it.
type Healther interface {
	Health(ctx context.Context) error
}

// EventSource is the live half of a run stream. *events.Bus satisfies it, with
// the contract that comes with it: delivery is best-effort, so a subscriber
// that falls behind loses events rather than stalling the run.
type EventSource interface {
	Subscribe(runID string) (<-chan events.Event, func())
}

// TranscriptOpener is the complete half. *coderunner.Runner satisfies it.
//
// Two sources rather than one because neither is sufficient alone: the bus is
// live but lossy, the transcript is complete but not a stream. Event.Seq is
// what stitches them together.
type TranscriptOpener interface {
	OpenTranscript(ctx context.Context, runID string) (io.ReadCloser, error)
}

// LeadGenRunner runs the Maps lead-gen pipeline end to end. *leadgen.Pipeline
// satisfies it. Nil when no Places key is configured — the /maps/* routes are
// then not registered, exactly as the maps_search tool is not.
type LeadGenRunner interface {
	Run(ctx context.Context, req leadgen.RunRequest) (leadgen.Report, error)
}

// EmailStatusSetter records a human's decision on a drafted outreach email.
// *store.Store satisfies it.
type EmailStatusSetter interface {
	SetOutreachEmailStatus(ctx context.Context, placeID, promptVersion, status string) error
}

// Deps are the collaborators the API serves. Every one is an interface so the
// handlers can be tested without a database, a subprocess, or a port.
type Deps struct {
	Projects    ProjectRegistry
	Accounts    AccountRegistry
	Runner      CodeRunner
	Store       Healther
	MCP         http.Handler
	Diagnostics mimirmcp.Tool

	// Events and Transcripts are both needed for /ws/runs/{id}; the route is
	// not registered unless both are present.
	Events      EventSource
	Transcripts TranscriptOpener

	// LeadGen and Emails are both needed for the /maps/* routes; they are not
	// registered unless LeadGen is present (it is nil without a Places key).
	LeadGen LeadGenRunner
	Emails  EmailStatusSetter
}

// Server owns the routes and the listener.
type Server struct {
	cfg     config.Config
	deps    Deps
	started time.Time
}

func New(cfg config.Config, deps Deps) *Server {
	return &Server{cfg: cfg, deps: deps, started: time.Now()}
}

// Handler returns the fully wrapped route tree. Every route — /mcp included —
// sits behind the same chain, so there is no path that skips the loopback
// guard or the token.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /diagnostics", s.handleDiagnostics)
	if s.deps.Projects != nil {
		mux.HandleFunc("GET /projects", s.handleListProjects)
		mux.HandleFunc("POST /projects", s.handleRegisterProject)
	}
	// Registered together with the runner they call: a nil Runner used to mean
	// a nil-interface call inside the handler, which recoverPanics turned into
	// a 500 for what is really a route that does not exist here.
	if s.deps.Accounts != nil {
		mux.HandleFunc("GET /accounts", s.handleListAccounts)
		mux.HandleFunc("POST /accounts", s.handleRegisterAccount)
		mux.HandleFunc("DELETE /accounts/{id}", s.handleDeleteAccount)
		mux.HandleFunc("GET /accounts/{id}/status", s.handleAccountStatus)
	}

	if s.deps.Runner != nil {
		mux.HandleFunc("GET /coding-models", s.handleListCodingModels)
		mux.HandleFunc("POST /coding-tasks", s.handleStartCodingTask)
		mux.HandleFunc("GET /coding-tasks", s.handleListCodingTasks)
		mux.HandleFunc("POST /coding-tasks/attachments", s.handleUploadAttachment)
		mux.HandleFunc("GET /coding-tasks/attachments/{id}", s.handleGetAttachment)
		mux.HandleFunc("GET /coding-tasks/{id}", s.handleGetCodingTask)
		mux.HandleFunc("DELETE /coding-tasks/{id}", s.handleDeleteCodingTask)
		mux.HandleFunc("POST /coding-tasks/{id}/enqueue", s.handleEnqueueCodingTask)
		mux.HandleFunc("POST /coding-tasks/{id}/stop", s.handleStopCodingTask)
	}

	// Transcripts belongs in this guard too: without it the socket opens, shows
	// no history and cannot fill a dropped-event gap — a silent degradation
	// rather than a route that is honestly absent.
	if s.deps.Events != nil && s.deps.Runner != nil && s.deps.Transcripts != nil {
		mux.HandleFunc("GET /ws/runs/{id}", s.handleRunStream)
	}

	if s.deps.LeadGen != nil {
		mux.HandleFunc("POST /maps/leadgen", s.handleLeadgen)
		mux.HandleFunc("POST /maps/emails/status", s.handleSetEmailStatus)
	}

	if s.deps.MCP != nil {
		mux.Handle("/mcp", s.deps.MCP)
		mux.Handle("/mcp/", s.deps.MCP)
	}

	// Outermost first: a panic in the auth check must still be recovered, and
	// an unauthenticated request must be logged as one.
	return recoverPanics(logRequests(s.guardLoopback(s.requireToken(limitBody(s.cfg, mux)))))
}

// Serve binds and serves until ctx is cancelled, then drains.
//
// The resolved address is logged to stderr, never printed: goat v1 had its
// parent scrape `MIMIR_PORT=<n>` out of the child's stdout, and this process
// could not do that even if it wanted to (SD-4). The parent already knows the
// port because the parent chose it.
func (s *Server) Serve(ctx context.Context) error {
	var lc net.ListenConfig
	addr := net.JoinHostPort(s.cfg.DaemonHost, strconv.Itoa(s.cfg.DaemonPort))
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return errors.New("mimir-daemon could not listen on " + addr + ": " + err.Error() +
			" — the parent process picks this port, check it is free")
	}

	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: s.cfg.DaemonReadHeaderTimeout,
	}

	slog.Info("mimir-daemon listening", "addr", ln.Addr().String())

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	// The shutdown deadline must outlive the cancellation that triggered it,
	// or in-flight responses are cut off at exactly the moment we are trying
	// to let them finish.
	shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.DaemonShutdownTimeout)
	defer cancel()

	shutErr := srv.Shutdown(shutCtx)
	<-errCh // Serve always returns once Shutdown has been called.
	if shutErr != nil {
		return shutErr
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	// No CORS headers, ever. A browser page on some other origin may be able to
	// send a request to loopback; without these it cannot read the reply, and
	// without the token it cannot get one anyway.
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("writing response", "error", err)
	}
}
