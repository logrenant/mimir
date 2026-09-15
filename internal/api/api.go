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
	"github.com/logrenant/mimir/internal/graphify"
	"github.com/logrenant/mimir/internal/leadgen"
	"github.com/logrenant/mimir/internal/llm"
	mimirmcp "github.com/logrenant/mimir/internal/mcp"
	"github.com/logrenant/mimir/internal/project"
	"github.com/logrenant/mimir/internal/ptyterm"
	"github.com/logrenant/mimir/internal/settings"
	"github.com/logrenant/mimir/internal/skills"
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

// AccountRegistry is the one Claude account this API exposes.
//
// There is no Register here and no path on the wire. Mimir signs into its own
// credential slot, whose directory it derived for itself, so connecting is a
// login the daemon performs rather than a directory a client names — and
// disconnecting is a logout, not a row being forgotten.
type AccountRegistry interface {
	List(ctx context.Context) ([]account.Account, error)
	Get(ctx context.Context, id string) (account.Account, error)
	// StartLogin runs `claude auth login` against Mimir's slot and opens the
	// authorization page in a private browser window. It returns once the
	// window is open, not once the login is finished.
	StartLogin(ctx context.Context) (account.LoginState, error)
	// LoginState is how a client follows an attempt it started.
	LoginState() account.LoginState
	// SubmitCode answers the CLI's paste prompt, for the flow it falls back to
	// when it could not open a browser itself.
	SubmitCode(code string) error
	// Reset signs the slot out, removes it, and forgets the row — what
	// closing the app does.
	Reset(ctx context.Context) error
}

// CodeRunner owns the whole life of a coding task, not just its execution.
// Every state change a client can ask for is a method here, because
// internal/coderunner is the only thing allowed to decide that a run may begin
// (docs/ROADMAP.md §B.2.1) — this API asks, it does not schedule.
type CodeRunner interface {
	Create(ctx context.Context, req coderunner.CreateRequest) (coderunner.Run, error)
	Start(ctx context.Context, req coderunner.CreateRequest) (coderunner.Run, error)
	Enqueue(ctx context.Context, runID string) (coderunner.Run, error)
	Retry(ctx context.Context, runID string, fresh bool) (coderunner.Run, error)
	Edit(ctx context.Context, runID string, req coderunner.EditRequest) (coderunner.Run, error)
	Kick(ctx context.Context) error
	Limits(ctx context.Context, limit int) (coderunner.LimitReport, error)
	Stop(ctx context.Context, runID string) (coderunner.Run, error)
	Delete(ctx context.Context, runID string) error
	Get(ctx context.Context, runID string) (coderunner.Run, error)
	List(ctx context.Context, projectID string, limit int) ([]coderunner.Run, error)
	ListAll(ctx context.Context, limit int) ([]coderunner.Run, error)
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
	// DraftOutreach writes messages for companies the operator picked, rather
	// than for everything a search returned. Same pipeline, entered after the
	// region search instead of before it.
	DraftOutreach(ctx context.Context, req leadgen.OutreachRequest) (leadgen.OutreachResult, error)
}

// RegionSourceReporter is what the diagnostics surface asks about region
// search: which providers exist, and whether the first one is free.
// *regionsearch.Router satisfies it.
type RegionSourceReporter interface {
	Sources() []string
	Free() bool
}

// OutreachStatusSetter records a human's decision on a drafted message.
// *store.Store satisfies it.
type OutreachStatusSetter interface {
	SetOutreachStatus(ctx context.Context, placeID, channel, status string) error
}

// SkillStore is the operator's copy of the sub-agent skills. An interface, so
// the daemon can serve the screen without the runner, and *skills.Store
// satisfies it.
type SkillStore interface {
	Skills() ([]skills.Skill, error)
	PutSkill(id, body string) (skills.Skill, error)
	ResetSkill(id string) (skills.Skill, error)
}

// SettingsStore is the operator's own configuration: the default model their
// lead-gen runs spend, and the rule file behind each outreach channel.
// *settings.Store satisfies it.
//
// Its own Deps field rather than a corner of the store because it fails on its
// own terms: it is files in a directory, and a daemon whose database will not
// open still has a settings screen — which, on a machine where something is
// wrong, is exactly the screen an operator wants to reach.
type SettingsStore interface {
	Get() (settings.Values, error)
	Put(v settings.Values) error
	Rule(ch settings.Channel) (settings.Rule, error)
	Rules() ([]settings.Rule, error)
	PutRule(ch settings.Channel, body string) (settings.Rule, error)
	ResetRule(ch settings.Channel) (settings.Rule, error)

	// The scan permission surface. Here rather than on BrainScanner because it
	// outlives any one scan: the folders Mimir may read are a saved decision,
	// and a daemon whose supervisor never started still has to be able to show
	// and change them — that is the state somebody goes looking for when the
	// scan is the thing that is wrong.
	ScanPolicy() (settings.ScanPolicy, bool, error)
	EffectiveScanPolicy(defaultRoots []string) settings.ScanPolicy
	PutScanPolicy(p settings.ScanPolicy) (settings.ScanPolicy, error)

	// And the structural layer's one switch, here for the same reason: it is a
	// saved decision about what this machine may run.
	EffectiveStructural() settings.Structural
	PutStructural(in settings.Structural) (settings.Structural, error)
}

// StructuralProbe answers "is Graphify installed", for the screen that offers
// to use it. *graphify.Detector satisfies it, and the daemon passes the same
// one the sweep uses so the tab cannot report an availability the loop
// disagrees with.
type StructuralProbe interface {
	Look(ctx context.Context, python string) (graphify.Info, bool)
	Forget()
}

// LeadLedger is the durable lead record behind the /maps/leads routes.
// *store.Store satisfies it. Separate from LeadGenRunner because it fails
// separately: a daemon whose store would not open can still run a search, and
// one with no region source can still read what earlier runs found.
type LeadLedger interface {
	ListLeads(ctx context.Context, f store.LeadFilter) ([]store.LeadRow, error)
	// LeadsByPlaceID resolves the operator's own selection — the checkbox
	// column's rows, in the order they were picked.
	LeadsByPlaceID(ctx context.Context, placeIDs []string) ([]store.LeadRow, error)
	LeadCategoryCounts(ctx context.Context, f store.LeadFilter) ([]store.CategoryCount, error)
	ListLeadRuns(ctx context.Context, limit int) ([]store.LeadRun, error)
	ListLeadRegions(ctx context.Context) ([]store.LeadRegion, error)
	OutreachMessagesFor(ctx context.Context, placeIDs []string) (map[string][]store.OutreachMessage, error)
}

// Deps are the collaborators the API serves. Every one is an interface so the
// handlers can be tested without a database, a subprocess, or a port.
// LLMDiscoverer reports what the registered providers look like on this
// machine. `probe` is the price: false asks the binaries for their version
// (free), true spends one trivial completion each to find out whether the
// logins work.
type LLMDiscoverer interface {
	Discover(ctx context.Context, probe bool, probeTimeout time.Duration) []llm.Availability
	// ProbeOne asks about one connection, because the screen asks about one
	// row: an operator pressing "sına" on a line is waiting for that line, not
	// for every login on the machine.
	ProbeOne(ctx context.Context, id string, probeTimeout time.Duration) (llm.Availability, bool)
}

// ConnectionRegistry answers the two questions every model-spending route
// asks. It is a one-method-per-question interface for the reason this package
// states about itself: a handler is a door, not a floor.
type ConnectionRegistry interface {
	Specs() []llm.ConnectionSpec
	Catalogue() []llm.CatalogueEntry
	Add(catalogueID, label string) (llm.ConnectionSpec, error)
	Allows(id, model string) bool
	DefaultModel(id string) string
}

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

	// LeadGen and Outreach are both needed for the /maps/* routes; they are not
	// registered unless LeadGen is present. Regions is what /diagnostics
	// reports about the sources behind it.
	LeadGen  LeadGenRunner
	Outreach OutreachStatusSetter
	Regions  RegionSourceReporter

	// Settings is the operator's own configuration, gated on its own: the model
	// picker and the two rule files are useful on a daemon with no region source
	// and no store at all.
	Settings SettingsStore

	// LLM answers what this *machine* can actually run. The provider table is
	// a constant and always published; which of its entries have their CLI
	// installed and their login working is a property of the machine, and a
	// picker that offers a provider that is not there offers a failure.
	// Absent on a daemon built without a router: the table still ships, the
	// availability column simply is not there.
	LLM LLMDiscoverer

	// Connections is the allow-list's new home: which connections exist and
	// which (connection, model) pairs this daemon will spend. Absent on a
	// daemon built without one, and then the shipped table answers — the same
	// degradation `LLM` has.
	Connections ConnectionRegistry

	// Skills is the operator's copy of each sub-agent's standing
	// instructions. Gated on its own, like Settings and for the same reason:
	// "what is this agent told to do" is answerable on a daemon that can run
	// nothing, and that is exactly when somebody goes looking for it.
	Skills SkillStore

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

	// GraphRead is the traversal half — query, affected, hubs. Gated apart
	// from Brain because it reads the parser's edges, which a daemon can be
	// missing while node detail still answers.
	GraphRead GraphReader

	// BrainKeep and ProjectKeep are the two writes that remove: moving a
	// project's history onto a new path, and forgetting it. Gated on their own
	// because a daemon that cannot do them should not offer the buttons.
	BrainKeep   BrainKeeper
	ProjectKeep ProjectKeeper

	// Structural is optional even where Settings is not: a daemon built
	// without it answers "not installed", which is the truthful answer.
	Structural StructuralProbe

	// Catalog is the product content studio. Gated on its own: an operator can
	// import an export, read what the brand kit made of it and write the file
	// back out on a daemon that can run no cards at all, and that is a useful
	// daemon rather than a broken one.
	Catalog CatalogStudio

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

	// terminals owns the interactive shell. Held by the server rather than
	// passed in Deps because it is not a dependency the daemon injects — it is
	// state this process keeps, and a shell has to outlive the request that
	// opened it or a trip to another screen would hang it up.
	terminals *ptyterm.Registry
}

func New(cfg config.Config, deps Deps) *Server {
	return &Server{
		cfg:       cfg,
		deps:      deps,
		started:   time.Now(),
		terminals: ptyterm.NewRegistry(cfg.ClaudeSessionDir),
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
		mux.HandleFunc("GET /accounts/{id}/status", s.handleAccountStatus)
		// Connecting is a login the daemon runs, so it is three routes rather
		// than a POST with a body: start it, follow it, and — only when the
		// CLI could not open a browser itself — hand it the pasted code.
		mux.HandleFunc("POST /accounts/login", s.handleStartLogin)
		mux.HandleFunc("GET /accounts/login", s.handleLoginState)
		mux.HandleFunc("POST /accounts/login/code", s.handleLoginCode)
		mux.HandleFunc("POST /accounts/reset", s.handleResetAccounts)
	}

	if s.deps.Runner != nil {
		mux.HandleFunc("GET /coding-models", s.handleListCodingModels)
		mux.HandleFunc("POST /coding-tasks", s.handleStartCodingTask)
		mux.HandleFunc("POST /coding-tasks/queue/kick", s.handleKickQueue)
		mux.HandleFunc("GET /coding-tasks/queue/limits", s.handleQueueLimits)
		mux.HandleFunc("GET /coding-tasks", s.handleListCodingTasks)
		mux.HandleFunc("POST /coding-tasks/attachments", s.handleUploadAttachment)
		mux.HandleFunc("GET /coding-tasks/attachments/{id}", s.handleGetAttachment)
		mux.HandleFunc("GET /coding-tasks/{id}", s.handleGetCodingTask)
		mux.HandleFunc("PATCH /coding-tasks/{id}", s.handleEditCodingTask)
		mux.HandleFunc("DELETE /coding-tasks/{id}", s.handleDeleteCodingTask)
		mux.HandleFunc("POST /coding-tasks/{id}/enqueue", s.handleEnqueueCodingTask)
		mux.HandleFunc("POST /coding-tasks/{id}/retry", s.handleRetryCodingTask)
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

		// Both need BrainGraph as well as BrainKeep: the id on the wire is
		// resolved against the project list, and a route that could not do
		// that would have to take a path instead.
		if s.deps.BrainKeep != nil {
			mux.HandleFunc("POST /brain/projects/{id}/move", s.handleMoveBrainProject)
			mux.HandleFunc("DELETE /brain/projects/{id}", s.handleForgetBrainProject)
		}
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
	mux.HandleFunc("GET /llm/connections", s.handleListConnections)
	mux.HandleFunc("POST /llm/connections", s.handleAddConnection)
	mux.HandleFunc("POST /llm/connections/{id}/probe", s.handleProbeConnection)

	// The operator's own configuration. Registered apart from everything else
	// and before the guards below: which model writes an outreach message, and
	// how, is answerable on a daemon that can neither search a region nor open
	// its database — and that is precisely when somebody goes looking for it.
	// The sub-agent catalogue is a view of a constant this binary ships, like
	// GET /coding-models: it costs nothing, it cannot fail, and a client that
	// reads it before the rest of the daemon is wired draws a picker that is
	// right rather than empty. So it is registered unconditionally.
	mux.HandleFunc("GET /agents", s.handleListAgents)

	if s.deps.Skills != nil {
		mux.HandleFunc("GET /skills", s.handleListSkills)
		mux.HandleFunc("PUT /skills/{id}", s.handleSaveSkill)
		mux.HandleFunc("POST /skills/{id}/reset", s.handleResetSkill)
	}

	if s.deps.Settings != nil {
		mux.HandleFunc("GET /settings", s.handleGetSettings)
		mux.HandleFunc("PUT /settings", s.handleSaveSettings)
		mux.HandleFunc("PUT /settings/rules", s.handleSaveRule)
		mux.HandleFunc("POST /settings/rules/reset", s.handleResetRule)

		// Under /brain because that is what they govern, and gated on Settings
		// because that is what stores them. A daemon with no supervisor still
		// serves these: "which folders may be read" is answerable, and worth
		// answering, on a machine where the scan itself will not start.
		mux.HandleFunc("GET /brain/scan/policy", s.handleGetScanPolicy)
		mux.HandleFunc("PUT /brain/scan/policy", s.handleSaveScanPolicy)
		mux.HandleFunc("POST /brain/scan/policy/reset", s.handleResetScanPolicy)

		// The structural layer's switch and its "is it there yet" — the same
		// surface and the same gate.
		mux.HandleFunc("GET /brain/structural", s.handleGetStructural)
		mux.HandleFunc("PUT /brain/structural", s.handleSaveStructural)
	}

	// Asking the graph a question, as opposed to drawing it.
	if s.deps.GraphRead != nil {
		mux.HandleFunc("POST /brain/query", s.handleBrainQuery)
		mux.HandleFunc("POST /brain/affected", s.handleBrainAffected)
		mux.HandleFunc("GET /brain/hubs", s.handleBrainHubs)
	}

	if s.deps.LeadGen != nil {
		mux.HandleFunc("POST /maps/leadgen", s.handleLeadgen)
		mux.HandleFunc("POST /maps/leadgen/export", s.handleLeadgenExport)
		// Drafting for a chosen set, rather than for a whole search. Needs the
		// ledger as well as the pipeline — it resolves place ids through it —
		// and the handler says so rather than the route disappearing, because
		// "this daemon has no ledger" is a different answer from "no such
		// route".
		mux.HandleFunc("POST /maps/outreach", s.handleDraftOutreach)
		mux.HandleFunc("POST /maps/outreach/status", s.handleSetOutreachStatus)
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

	// The product content studio. The import route is the one place in this
	// surface whose payload is a document rather than a prompt, which is why it
	// has a row in bodyLimits rather than a raised cap for everything.
	if s.deps.Catalog != nil {
		mux.HandleFunc("POST /catalog/imports", s.handleCatalogImport)
		mux.HandleFunc("GET /catalog/imports", s.handleListCatalogImports)
		mux.HandleFunc("GET /catalog/imports/{id}", s.handleGetCatalogImport)
		mux.HandleFunc("DELETE /catalog/imports/{id}", s.handleDeleteCatalogImport)
		mux.HandleFunc("GET /catalog/profiles", s.handleListCatalogProfiles)
		mux.HandleFunc("PUT /catalog/imports/{id}/mapping", s.handleSetCatalogMapping)
		mux.HandleFunc("PUT /catalog/imports/{id}/dialect", s.handleSetCatalogDialect)
		mux.HandleFunc("PUT /catalog/imports/{id}/fields", s.handleSetCatalogWrite)
		mux.HandleFunc("PUT /catalog/imports/{id}/target-lang", s.handleSetCatalogTargetLang)
		mux.HandleFunc("POST /catalog/imports/{id}/reread", s.handleRereadCatalogImport)
		mux.HandleFunc("GET /catalog/imports/{id}/brand", s.handleGetCatalogBrand)
		mux.HandleFunc("PUT /catalog/imports/{id}/brand", s.handleSaveCatalogBrand)
		mux.HandleFunc("POST /catalog/imports/{id}/brand/rescan", s.handleRescanCatalogBrand)
		mux.HandleFunc("POST /catalog/imports/{id}/site/scan", s.handleScanCatalogSite)
		mux.HandleFunc("POST /catalog/imports/{id}/export", s.handleExportCatalog)
		mux.HandleFunc("GET /catalog/outputs", s.handleListCatalogOutputs)
		mux.HandleFunc("GET /catalog/products", s.handleListCatalogProducts)
		mux.HandleFunc("GET /catalog/products/{id}", s.handleGetCatalogProduct)
		mux.HandleFunc("PUT /catalog/products/{id}/draft", s.handleSaveCatalogDraft)
		mux.HandleFunc("POST /catalog/products/{id}/status", s.handleSetCatalogProductStatus)

		// The bulk pass is a card, so it needs the queue as well as the studio.
		// Gated apart: every route above answers on a daemon with no runner —
		// importing a file, reading what the brand kit made of it and writing
		// the file back out are all things that need no queue at all.
		if s.deps.Runner != nil {
			mux.HandleFunc("POST /catalog/rewrite", s.handleRewriteCatalog)
		}
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
