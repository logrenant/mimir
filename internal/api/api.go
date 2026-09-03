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
	"github.com/logrenant/mimir/internal/ptyterm"
	"github.com/logrenant/mimir/internal/store"
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
	// Sync registers the slots a scan of the accounts directory found. The
	// scan itself is the handler's, so this interface stays about the
	// registry rather than about the filesystem.
	Sync(ctx context.Context, slots []account.Slot) ([]account.Account, error)
	SetBackground(ctx context.Context, id string) error
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
// satisfies it. It is no longer conditional on a Places key: the free scrape
// provider needs no credential, so the /maps/* routes exist on every machine.
type LeadGenRunner interface {
	Run(ctx context.Context, req leadgen.RunRequest) (leadgen.Report, error)
	Export(ctx context.Context, req leadgen.ExportRequest) (leadgen.ExportResult, error)
}

// RegionSourceReporter is what the diagnostics surface asks about region
// search: which providers exist, and whether the first one is free.
// *regionsearch.Router satisfies it.
type RegionSourceReporter interface {
	Sources() []string
	Free() bool
}

// EmailStatusSetter records a human's decision on a drafted outreach email.
// *store.Store satisfies it.
type EmailStatusSetter interface {
	SetOutreachEmailStatus(ctx context.Context, placeID, promptVersion, status string) error
}

// LeadLedger is the durable lead record behind the /maps/leads routes.
// *store.Store satisfies it. Separate from LeadGenRunner because it fails
// separately: a daemon whose store would not open can still run a search, and
// one with no region source can still read what earlier runs found.
type LeadLedger interface {
	ListLeads(ctx context.Context, f store.LeadFilter) ([]store.LeadRow, error)
	LeadCategoryCounts(ctx context.Context, f store.LeadFilter) ([]store.CategoryCount, error)
	ListLeadRuns(ctx context.Context, limit int) ([]store.LeadRun, error)
	ListLeadRegions(ctx context.Context) ([]store.LeadRegion, error)
	OutreachEmailsFor(ctx context.Context, placeIDs []string, promptVersion string) (map[string]store.OutreachEmail, error)
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
	// registered unless LeadGen is present. Regions is what /diagnostics
	// reports about the sources behind it.
	LeadGen LeadGenRunner
	Emails  EmailStatusSetter
	Regions RegionSourceReporter

	// Leads is the ledger — the reads that cost nothing. Gated on its own so a
	// store that opened serves saved businesses even where a search cannot run.
	Leads LeadLedger

	// The three halves of the Brain tab, separately gated because they fail
	// separately: the scan is a daemon-lifetime loop, the graph is three store
	// reads, and node detail is the node core. A machine whose store would not
	// open has none of them; a build without the supervisor still has the
	// graph.
	BrainScan  BrainScanner
	Brain      BrainReader
	BrainGraph BrainGraphStore

	// Chat is the verbatim conversation archive. Gated on its own: it is a
	// read over rows the ingest loop wrote, and it works on a daemon with no
	// scan, no region source, and no runner.
	Chat ChatArchiveReader
}

// Server owns the routes and the listener.
type Server struct {
	cfg     config.Config
	deps    Deps
	started time.Time

	// terminals owns the interactive shells. Held by the server rather than
	// passed in Deps because it is not a dependency the daemon injects — it is
	// state this process keeps, and a shell has to outlive the request that
	// opened it or two profiles could never be open at once.
	terminals *ptyterm.Registry
}

func New(cfg config.Config, deps Deps) *Server {
	return &Server{
		cfg:       cfg,
		deps:      deps,
		started:   time.Now(),
		terminals: ptyterm.NewRegistry(),
	}
}

// CloseTerminals ends every interactive shell, for daemon shutdown. Without it
// the shells would outlive the daemon that has their pty, which is a leak the
// operator cannot see or reach.
func (s *Server) CloseTerminals() {
	if s.terminals != nil {
		s.terminals.CloseAll()
	}
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
	// The interactive terminal needs no injected dependency — it runs the
	// operator's own shell — so it is registered unconditionally.
	mux.HandleFunc("GET /terminals/profiles", s.handleTerminalProfiles)
	mux.HandleFunc("GET /ws/terminals/pty", s.handleTerminalPTY)
	mux.HandleFunc("DELETE /terminals/{profile}", s.handleKillTerminal)

	if s.deps.Accounts != nil {
		mux.HandleFunc("GET /accounts", s.handleListAccounts)
		mux.HandleFunc("POST /accounts", s.handleRegisterAccount)
		mux.HandleFunc("DELETE /accounts/{id}", s.handleDeleteAccount)
		mux.HandleFunc("GET /accounts/{id}/status", s.handleAccountStatus)
		mux.HandleFunc("POST /accounts/scan", s.handleScanAccounts)
		mux.HandleFunc("POST /accounts/background", s.handleSetBackgroundAccount)
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

	// The Brain tab. The scan's three controls are routes rather than frames on
	// a socket for the same reason stopping a run is: the socket, when there is
	// one, stays one-directional.
	if s.deps.BrainScan != nil {
		mux.HandleFunc("GET /brain/scan", s.handleBrainScanStatus)
		mux.HandleFunc("GET /brain/scan/log", s.handleBrainScanLog)
		mux.HandleFunc("POST /brain/scan/pause", s.handleBrainScanPause)
		mux.HandleFunc("POST /brain/scan/resume", s.handleBrainScanResume)
		mux.HandleFunc("POST /brain/scan/now", s.handleBrainScanNow)
	}
	if s.deps.BrainGraph != nil {
		mux.HandleFunc("GET /brain/graph", s.handleBrainGraph)
		mux.HandleFunc("GET /brain/projects", s.handleBrainProjects)
	}
	if s.deps.Brain != nil {
		mux.HandleFunc("GET /brain/nodes/{id}", s.handleBrainNode)
	}
	if s.deps.BrainGraph != nil {
		mux.HandleFunc("GET /brain/nodes/{id}/versions", s.handleBrainNodeVersions)
	}

	// The model picker's allow-list. Registered unconditionally: it is a view
	// of a constant, it costs nothing, and a client that can read it before
	// the lead-gen deps are wired gets a picker that is right rather than
	// empty.
	mux.HandleFunc("GET /llm/providers", s.handleListLLMProviders)

	if s.deps.LeadGen != nil {
		mux.HandleFunc("POST /maps/leadgen", s.handleLeadgen)
		mux.HandleFunc("POST /maps/leadgen/export", s.handleLeadgenExport)
		mux.HandleFunc("POST /maps/emails/status", s.handleSetEmailStatus)
	}

	// The ledger reads. Registered apart from the run routes above: they need
	// only the store, and they are the screen's opening state.
	if s.deps.Leads != nil {
		mux.HandleFunc("GET /maps/leads", s.handleListLeads)
		mux.HandleFunc("GET /maps/leads/categories", s.handleLeadCategories)
		mux.HandleFunc("GET /maps/leads/runs", s.handleListLeadRuns)
		mux.HandleFunc("GET /maps/leads/regions", s.handleListLeadRegions)
	}

	// The chat archive. Read-only: what a conversation said is not a decision
	// a client gets to change, and the ingest loop is its only writer.
	if s.deps.Chat != nil {
		mux.HandleFunc("GET /chat/sessions", s.handleListChatSessions)
		mux.HandleFunc("GET /chat/sessions/{id}", s.handleChatSession)
		mux.HandleFunc("GET /chat/search", s.handleSearchChat)
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
