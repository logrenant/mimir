// Command mimir-daemon is the long-running half of Mimir.
//
// cmd/mimir-mcp is a stdio entrypoint whose lifetime an MCP client owns — it
// starts and stops with a Claude Code session, so it can never be the process
// the desktop app talks to. This binary is that process: it owns the coding-
// task runner, the event bus, and the store, and it re-exposes the same MCP
// tool set over HTTP.
//
// It is spawned by the Tauri shell as a sidecar, which hands it a port and a
// per-launch bearer token (docs/ROADMAP.md §B.2.1). It will not start
// without the token.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/logrenant/mimir/internal/account"
	"github.com/logrenant/mimir/internal/api"
	"github.com/logrenant/mimir/internal/brain"
	"github.com/logrenant/mimir/internal/coderunner"
	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/contacts"
	"github.com/logrenant/mimir/internal/crawl"
	"github.com/logrenant/mimir/internal/events"
	"github.com/logrenant/mimir/internal/leadgen"
	"github.com/logrenant/mimir/internal/llm"
	"github.com/logrenant/mimir/internal/maps"
	"github.com/logrenant/mimir/internal/mapscrape"
	"github.com/logrenant/mimir/internal/mapsllm"
	mimirmcp "github.com/logrenant/mimir/internal/mcp"
	"github.com/logrenant/mimir/internal/memory"
	"github.com/logrenant/mimir/internal/pipeline"
	"github.com/logrenant/mimir/internal/project"
	"github.com/logrenant/mimir/internal/refine"
	"github.com/logrenant/mimir/internal/regionsearch"
	"github.com/logrenant/mimir/internal/search"
	"github.com/logrenant/mimir/internal/store"
	"github.com/logrenant/mimir/internal/tools"
)

// Populated via -ldflags by `make release`.
var (
	version = "dev"
	commit  = "none"
)

func main() {
	mimirmcp.InitLogging(os.Stderr)
	slog.Info("mimir-daemon starting", "version", version, "commit", commit)

	if err := run(); err != nil {
		slog.Error("Fatal error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			slog.Info("shutdown signal received")
			cancel()
		case <-ctx.Done():
		}
	}()

	cfg := config.Load()
	if err := cfg.ValidateDaemon(); err != nil {
		return err
	}

	// Unlike mimir-mcp, which degrades to an uncached pipeline if the store will
	// not open, the daemon cannot: projects and runs *are* the store. Failing
	// here with the path named beats starting a server whose every route 500s.
	db, err := store.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Warn("closing store", "error", err)
		}
	}()

	searchClient := search.New(cfg)
	crawlClient := crawl.New(cfg)
	refineClient := refine.New(cfg)
	pipe := pipeline.New(cfg, searchClient, crawlClient, refineClient, db)

	// Region search, and the lead-gen pipeline over it.
	//
	// The free scrape provider always exists — it needs no credential, only the
	// local sidecar, which it starts itself when a search finds it down — so
	// lead-gen is no longer gated on an operator-provisioned Places key. The
	// key, when there is one, adds a second source behind the free one
	// (internal/regionsearch owns that order).
	//
	// The nil here is a nil *interface*, assigned only when the client exists:
	// a nil *maps.Client inside a non-nil interface would look like a working
	// provider until it was called.
	var (
		sources       regionsearch.Sources
		emailStatuser api.EmailStatusSetter
	)
	if cfg.PlacesAPIKey != "" {
		mc, err := maps.New(cfg, maps.Options{APIKey: cfg.PlacesAPIKey})
		if err != nil {
			return err
		}
		sources.Places = mc
	}

	// The sidecar scrape, and the model that recovers a feed it could not read.
	// The second is both a provider of its own — for a machine where the
	// container cannot run — and the fallback the scraper itself calls when
	// Google moves the markup out from under its selectors.
	sidecar := mapscrape.New(cfg)
	modelMaps := mapsllm.New(cfg, refineClient, crawlClient)
	sidecar.UseExtractor(modelMaps)

	sources.Sidecar = sidecar
	sources.Model = modelMaps
	regions := regionsearch.Standard(sources)
	slog.Info("region search", "sources", strings.Join(regions.Sources(), ", "),
		"free_primary", regions.Free())

	categorizer := leadgen.New(cfg, refineClient, db)
	gapRunner := leadgen.NewGapAnalyzer(cfg, refineClient, db)
	emailRunner := leadgen.NewEmailRunner(cfg, refineClient, db)
	leadgenPipe := leadgen.NewPipeline(cfg, regions, db, categorizer, gapRunner, emailRunner)
	// Contact enrichment is a stage of the run, not just of the export.
	//
	// It used to be the export's alone, on the reasoning that a run nobody
	// exports must not fetch sixty websites. The ledger made that the wrong
	// trade: a run's findings are now kept, so the fetch is paid once and every
	// later read has the number, where before a ledger of seventy companies
	// held zero. The searcher is what lets it help the companies whose listing
	// carried no site at all.
	enricher := contacts.New(cfg, crawlClient, refineClient)
	enricher.UseSearcher(searchClient)
	leadgenPipe.UseContacts(enricher)
	// The ledger is what makes a run outlive its response. Installed like the
	// enricher: something a finished run feeds, not a stage of it.
	leadgenPipe.UseLedger(db)
	emailStatuser = db

	// Built before RegisterAll because the memory tools look projects up
	// through it; the registry itself has no dependency of its own beyond db.
	projects := project.NewRegistry(db)

	// Unlike mimir-mcp, the daemon passes a run source: it owns the coding-task
	// runner, so its memory covers both interactive sessions and the runs it
	// executed itself.
	mem := memory.New(cfg, db, refineClient, db)
	// The same parse that builds an episode row also keeps the conversation it
	// was distilled from. Installed here rather than taken by New because
	// mimir-mcp has no backlog to archive.
	mem.UseArchive(db)

	// The node core shares the store the memory does. Unlike the memory it is
	// not project-scoped by construction: a node about a public repository
	// belongs to no checkout, and the store keeps those under the empty scope.
	router := llm.NewRouter(cfg)
	knowledge := brain.New(cfg, db, router)

	srv := mimirmcp.NewServer(cfg)
	if err := tools.RegisterAll(srv.Registry(), cfg, tools.Deps{
		Search:      searchClient,
		Crawl:       crawlClient,
		Refine:      refineClient,
		Pipeline:    pipe,
		Regions:     regions,
		Memory:      mem,
		Projects:    projects,
		Brain:       knowledge,
		BrainHashes: db,
	}); err != nil {
		return err
	}

	// The project memory keeps itself current for the daemon's lifetime. It is
	// deliberately not part of start-up: a backfill over months of transcripts
	// must not stand between the operator and a working daemon.
	memCtx, stopMemory := context.WithCancel(ctx)
	defer stopMemory()
	memoryDone := make(chan struct{})
	go func() {
		defer close(memoryDone)
		mem.Run(memCtx, func(c context.Context) ([]memory.Project, error) {
			rows, err := projects.List(c)
			if err != nil {
				return nil, err
			}
			out := make([]memory.Project, 0, len(rows))
			for _, p := range rows {
				out = append(out, memory.Project{ID: p.ID, Path: p.Path})
			}
			return out, nil
		}, slog.Default())
	}()
	defer func() {
		stopMemory()
		<-memoryDone
	}()

	// Brain records what the memory has already distilled, plus what git says
	// happened. A separate loop rather than a step inside the one above: the
	// promotion reads recaps that loop is still producing, and folding them
	// together would make every promotion wait on a batch of model calls it
	// does not need. This one makes no model call at all.
	brainCtx, stopBrain := context.WithCancel(ctx)
	defer stopBrain()
	brainDone := make(chan struct{})
	go func() {
		defer close(brainDone)
		knowledge.Run(brainCtx, func(c context.Context) ([]string, error) {
			rows, err := projects.List(c)
			if err != nil {
				return nil, err
			}
			out := make([]string, 0, len(rows))
			for _, p := range rows {
				out = append(out, p.Path)
			}
			return out, nil
		}, brain.CaptureDeps{Episodes: db, Cursors: db}, slog.Default())
	}()
	defer func() {
		stopBrain()
		<-brainDone
	}()

	// The resident scan: the half of Brain that costs something. Where the loop
	// above records what already happened for free, this one reads the
	// operator's folders through agy, one file at a time, for as long as the
	// daemon lives — so there is always an agy working and a session's first
	// question about an unfamiliar file is answered from the store.
	//
	// Its own goroutine and its own drain, like the two loops above: the
	// deferred db.Close() runs last, so a pass in flight always finishes
	// against an open store.
	scanner := brain.NewSupervisor(cfg, brain.SupervisorDeps{
		Core:    knowledge,
		Hashes:  db,
		Cursors: db,
		Counter: db,
		Probe:   router.Provider(llm.Distill),
		Log:     slog.Default(),
	})
	scanCtx, stopScan := context.WithCancel(ctx)
	defer stopScan()
	scanDone := make(chan struct{})
	go func() {
		defer close(scanDone)
		scanner.Run(scanCtx)
	}()
	defer func() {
		stopScan()
		<-scanDone
	}()

	bus := events.NewBus()
	defer bus.Close()

	// The runner takes the daemon's lifetime, not a request's: a coding session
	// runs for minutes and must not die when the POST that started it returns.
	// Which Claude Code identity a run spends. Registered like a project: the
	// path is accepted once and everything afterwards carries an id.
	accounts := account.NewRegistry(db)

	// The accounts directory is the authority for which slots exist — the same
	// tree the operator's shell switches between — so the daemon reads it
	// rather than waiting to be told. Adding only, idempotent by directory, and
	// not fatal: a directory that cannot be read costs the operator a slot on
	// the picker, not the daemon.
	if slots, err := accounts.Sync(ctx, account.Discover(cfg.ClaudeAccountsDir)); err != nil {
		slog.Warn("scanning credential slots", "dir", cfg.ClaudeAccountsDir, "error", err)
	} else {
		labels := make([]string, 0, len(slots))
		for _, a := range slots {
			labels = append(labels, a.Label)
		}
		slog.Info("credential slots", "dir", cfg.ClaudeAccountsDir,
			"count", len(slots), "slots", strings.Join(labels, ", "))
	}

	// The daemon's own model calls — refine, distil, recap — are not coding
	// runs: nothing dispatched them, so without this they spend whichever
	// identity this process happens to have inherited.
	//
	// One identity, named here, rather than a slot the operator picks. Credential
	// slots exist for coding runs, where a human dispatched the work and can say
	// which account pays for it; the background loop has no such moment, and a
	// mark on a slot silently redirected every sweep, recap and page summary the
	// daemon made afterwards. The empty ConfigDir is the CLI's own login, and
	// Environ clears any inherited CLAUDE_SECURESTORAGE_CONFIG_DIR so this is the
	// same identity whether the daemon was started by launchd or from a shell
	// that had switched accounts.
	router.UseEnviron(func() []string {
		return account.Environ(os.Environ(), "")
	})

	runner := coderunner.New(ctx, cfg, bus, projects, accounts, db)

	// Before the API is serving: a row still marked running belongs to a daemon
	// that is gone, and the queue it left behind is meant to be picked up here.
	// A failure is logged rather than fatal — the daemon is still useful, it
	// just starts with a stale board.
	if err := runner.Resume(ctx); err != nil {
		slog.Warn("resuming coding tasks", "error", err)
	}

	apiDeps := api.Deps{
		Projects:    projects,
		Accounts:    accounts,
		Runner:      runner,
		Store:       db,
		MCP:         srv.MCPHandler(),
		Diagnostics: tools.NewDiagnostics(cfg, crawlClient, refineClient, searchClient, mem),

		// The live half and the complete half of a run stream. /ws/runs/{id}
		// needs both: the bus is lossy by design, the transcript is the record.
		Events:      bus,
		Transcripts: runner,

		// The Brain tab: the resident scan, the node core behind node detail,
		// and the store behind the graph.
		BrainScan:  scanner,
		Brain:      knowledge,
		BrainGraph: db,

		// What /diagnostics says about region search: which sources exist and
		// whether the first one is free.
		Regions: regions,

		// The ledger reads. Wired unconditionally with the store rather than
		// behind the pipeline guard below: saved businesses are readable on a
		// machine where no region source is available at all.
		Leads: db,

		// The chat archive, likewise: reading what was said needs the store
		// and nothing else.
		Chat: db,
	}
	// Set the interface field only when there is a real pipeline behind it: a
	// nil *leadgen.Pipeline in an interface is still a non-nil interface, and
	// the route guard checks the interface.
	if leadgenPipe != nil {
		apiDeps.LeadGen = leadgenPipe
		apiDeps.Emails = emailStatuser
	}

	httpAPI := api.New(cfg, apiDeps)

	serveErr := httpAPI.Serve(ctx)

	// An MCP-over-HTTP session outlives the request that opened it; a client
	// that went away without deleting its own leaves the server half alive.
	srv.CloseMCPSessions()

	// Interactive shells outlive their viewer on purpose, so they have to be
	// ended here: nothing else will. A shell left running holds a pty this
	// process owned, which after exit is a terminal nobody can see or reach.
	httpAPI.CloseTerminals()

	// Drain before the deferred store close: a run still writing its result
	// row needs the database to still be open.
	slog.Info("waiting for in-flight runs")
	runner.Wait()

	return serveErr
}
