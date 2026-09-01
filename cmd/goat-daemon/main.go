// Command goat-daemon is the long-running half of GOAT.
//
// cmd/goat-mcp is a stdio entrypoint whose lifetime an MCP client owns — it
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
	"syscall"

	"github.com/logrenant/goat-mcp/internal/api"
	"github.com/logrenant/goat-mcp/internal/coderunner"
	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/crawl"
	"github.com/logrenant/goat-mcp/internal/events"
	"github.com/logrenant/goat-mcp/internal/leadgen"
	"github.com/logrenant/goat-mcp/internal/maps"
	"github.com/logrenant/goat-mcp/internal/mapscrape"
	goatmcp "github.com/logrenant/goat-mcp/internal/mcp"
	"github.com/logrenant/goat-mcp/internal/memory"
	"github.com/logrenant/goat-mcp/internal/pipeline"
	"github.com/logrenant/goat-mcp/internal/project"
	"github.com/logrenant/goat-mcp/internal/refine"
	"github.com/logrenant/goat-mcp/internal/search"
	"github.com/logrenant/goat-mcp/internal/store"
	"github.com/logrenant/goat-mcp/internal/tools"
)

// Populated via -ldflags by `make release`.
var (
	version = "dev"
	commit  = "none"
)

func main() {
	goatmcp.InitLogging(os.Stderr)
	slog.Info("goat-daemon starting", "version", version, "commit", commit)

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

	// Unlike goat-mcp, which degrades to an uncached pipeline if the store will
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

	// The Places client and the lead-gen pipeline built on top of it. Both are
	// nil without an operator-provisioned key: the daemon still runs, it just
	// does not offer maps_search or the /maps/* routes — availability follows
	// the credential, exactly as it does for the tool (docs/SECURITY.md).
	var (
		mapsClient    *maps.Client
		leadgenPipe   *leadgen.Pipeline
		emailStatuser api.EmailStatusSetter
	)
	if cfg.PlacesAPIKey != "" {
		mc, err := maps.New(cfg, maps.Options{APIKey: cfg.PlacesAPIKey})
		if err != nil {
			return err
		}
		mapsClient = mc

		categorizer := leadgen.New(cfg, refineClient, db)
		gapRunner := leadgen.NewGapAnalyzer(cfg, refineClient, db)
		emailRunner := leadgen.NewEmailRunner(cfg, refineClient, db)
		leadgenPipe = leadgen.NewPipeline(cfg, mapsClient, mapscrape.New(cfg), db, categorizer, gapRunner, emailRunner)
		emailStatuser = db
	}

	// Built before RegisterAll because the memory tools look projects up
	// through it; the registry itself has no dependency of its own beyond db.
	projects := project.NewRegistry(db)

	// Unlike goat-mcp, the daemon passes a run source: it owns the coding-task
	// runner, so its memory covers both interactive sessions and the runs it
	// executed itself.
	mem := memory.New(cfg, db, refineClient, db)

	srv := goatmcp.NewServer(cfg)
	if err := tools.RegisterAll(srv.Registry(), cfg, tools.Deps{
		Search:   searchClient,
		Crawl:    crawlClient,
		Refine:   refineClient,
		Pipeline: pipe,
		Maps:     mapsClient,
		Memory:   mem,
		Projects: projects,
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

	bus := events.NewBus()
	defer bus.Close()

	// The runner takes the daemon's lifetime, not a request's: a coding session
	// runs for minutes and must not die when the POST that started it returns.
	runner := coderunner.New(ctx, cfg, bus, projects, db)

	apiDeps := api.Deps{
		Projects:    projects,
		Runner:      runner,
		Store:       db,
		MCP:         srv.MCPHandler(),
		Diagnostics: tools.NewDiagnostics(cfg, crawlClient, refineClient, searchClient, mem),

		// The live half and the complete half of a run stream. /ws/runs/{id}
		// needs both: the bus is lossy by design, the transcript is the record.
		Events:      bus,
		Transcripts: runner,
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

	// Drain before the deferred store close: a run still writing its result
	// row needs the database to still be open.
	slog.Info("waiting for in-flight runs")
	runner.Wait()

	return serveErr
}
