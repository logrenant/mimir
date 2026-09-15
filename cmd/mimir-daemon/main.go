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
	"path/filepath"
	"strings"
	"syscall"

	"github.com/logrenant/mimir/internal/account"
	"github.com/logrenant/mimir/internal/agents"
	"github.com/logrenant/mimir/internal/api"
	"github.com/logrenant/mimir/internal/brain"
	"github.com/logrenant/mimir/internal/catalog"
	"github.com/logrenant/mimir/internal/catalogjob"
	"github.com/logrenant/mimir/internal/coderunner"
	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/connections"
	"github.com/logrenant/mimir/internal/contacts"
	"github.com/logrenant/mimir/internal/crawl"
	"github.com/logrenant/mimir/internal/events"
	"github.com/logrenant/mimir/internal/graphify"
	"github.com/logrenant/mimir/internal/leadgen"
	"github.com/logrenant/mimir/internal/leadgenjob"
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
	"github.com/logrenant/mimir/internal/settings"
	"github.com/logrenant/mimir/internal/skills"
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
		sources          regionsearch.Sources
		outreachStatuser api.OutreachStatusSetter
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

	// The operator's own settings, beside the store like every other directory
	// this process derives. Built before the pipeline because the message runner
	// reads its rule files on every draft.
	operatorSettings := settings.New(filepath.Join(filepath.Dir(cfg.StorePath), "settings"))

	// The sub-agent skills, in their own directory beside the settings. One
	// store, handed to all three consumers below — the HTTP screen, the run
	// dispatcher, and the MCP choke-point — so an edit the operator makes on
	// the screen is the text the next run and the next tool call are held to.
	skillStore := skills.New(cfg.SkillDir)

	categorizer := leadgen.New(cfg, refineClient, db)
	gapRunner := leadgen.NewGapAnalyzer(cfg, refineClient, db)
	messageRunner := leadgen.NewMessageRunner(cfg, refineClient, db)
	// The rule files are read per draft rather than snapshotted here: an
	// operator who edits the rules and drafts again expects the new rules, not
	// the ones the daemon happened to start with.
	messageRunner.UseRules(operatorSettings)
	leadgenPipe := leadgen.NewPipeline(cfg, regions, db, categorizer, gapRunner, messageRunner)
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
	outreachStatuser = db

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
	// What each connection *is* — label, vendor, adapter, whether its model
	// list is discovered. Told to the router rather than reached for: the
	// registry imports `internal/store` and `internal/llm` cannot.
	connectionRegistry := connections.New(cfg)
	router.UseConnections(connectionRegistry.Specs())
	knowledge := brain.New(cfg, db, router)

	srv := mimirmcp.NewServer(cfg)
	// Before RegisterAll: a tool that declares a skill fails closed when the
	// registry has no source for one, and registering first would make that
	// depend on the order of two lines.
	// The catalog studio, built once and shared by all three of its doors: the
	// MCP reads below, the /catalog/* routes, and the board card. A second
	// instance would mean two brand kits derived from one file.
	catalogStudio := catalog.New(cfg, db, router)
	catalogStudio.UseResearcher(catalogjob.Research(pipe))
	// The storefront scan. The same crawler the research pipeline uses, because
	// the politeness interval that keeps this machine welcome on a site is the
	// crawler's and not the caller's.
	catalogStudio.UseSiteReader(catalogjob.Site(crawlClient))
	// The two halves of a draft's cache key that live above the studio: which
	// model the operator standing-chose, and the version of the instructions the
	// catalog agent runs under. Injected here so the HTTP routes, the MCP tools
	// and the board card all compose the identical key — they used to compose it
	// separately and they drifted, which left every draft a bulk rewrite paid for
	// invisible to every read.
	catalogStudio.UseDraftKey(
		func() llm.Selection {
			v, err := operatorSettings.Get()
			if err != nil || v.IsZero() || !cfg.HasLLMModel(v.Provider, v.Model) {
				return llm.Selection{}
			}
			return llm.Selection{Provider: v.Provider, Model: v.Model}
		},
		func() string {
			a, ok := agents.Lookup(agents.KeyCatalog)
			if !ok {
				return ""
			}
			_, version, ok := skills.Require(skillStore, a.RequiredSkills)
			if !ok {
				return ""
			}
			return version
		},
	)

	srv.Registry().SetSkills(skillStore)
	if err := tools.RegisterAll(srv.Registry(), cfg, tools.Deps{
		Search:      searchClient,
		Crawl:       crawlClient,
		Refine:      refineClient,
		Pipeline:    pipe,
		Regions:     regions,
		Memory:      mem,
		Projects:    projects,
		Brain:       knowledge,
		Catalog:     catalogStudio,
		BrainHashes: db,
	}); err != nil {
		return err
	}

	// The project memory keeps itself current for the daemon's lifetime. It is
	// deliberately not part of start-up: a backfill over months of transcripts
	// must not stand between the operator and a working daemon.
	memCtx, stopMemory := context.WithCancel(ctx)
	defer stopMemory()
	// Every project this daemon should remember, not just the ones registered
	// for coding runs.
	//
	// The two lists were the same list, and that was the bug: the registry is
	// "folders I run tasks in" (nine of them) while the scan reads everything
	// under the operator's roots (eighteen). A conversation held in a project
	// that was never registered — this repository, for one — was distilled by
	// the MCP server and then archived by nobody. The registry still supplies
	// the ids, because a project with one is the only kind that has coding runs
	// to correlate.
	knownProjects := func(c context.Context) ([]memory.Project, error) {
		rows, err := projects.List(c)
		if err != nil {
			return nil, err
		}
		out := make([]memory.Project, 0, len(rows))
		seen := make(map[string]struct{}, len(rows))
		for _, p := range rows {
			out = append(out, memory.Project{ID: p.ID, Path: p.Path})
			seen[p.Path] = struct{}{}
		}

		policy := operatorSettings.EffectiveScanPolicy(cfg.BrainScanRoots)
		for _, root := range policy.Roots {
			found, err := brain.DiscoverProjects(root, cfg.BrainScanDepth)
			if err != nil {
				// A root that cannot be walked costs its projects and nothing
				// else; the registered ones are already in the list.
				continue
			}
			for _, path := range found {
				if _, ok := seen[path]; ok {
					continue
				}
				seen[path] = struct{}{}
				out = append(out, memory.Project{Path: path})
			}
		}
		return out, nil
	}

	memoryDone := make(chan struct{})
	go func() {
		defer close(memoryDone)
		mem.Run(memCtx, knownProjects, slog.Default())
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
			rows, err := knownProjects(c)
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
	structural := graphify.NewDetector(graphify.DefaultDetectTTL)
	scanner := brain.NewSupervisor(cfg, brain.SupervisorDeps{
		Core:    knowledge,
		Hashes:  db,
		Cursors: db,
		Counter: db,
		Probe:   router.Provider(llm.Distill),
		Log:     slog.Default(),
		// Read per sweep, not captured once: the operator edits this on the
		// Brain tab while the loop is running, and a value copied in here would
		// mean every change waited for a daemon restart. cfg.BrainScanRoots is
		// the seed the first read falls back to, so a machine nobody has
		// configured sweeps exactly the folders it always did.
		Policy: func() brain.ScanPolicy {
			p := operatorSettings.EffectiveScanPolicy(cfg.BrainScanRoots)
			return brain.ScanPolicy{Roots: p.Roots, Excludes: p.Excludes}
		},
		// The same shape and the same reason: an operator who installs
		// Graphify halfway through the day gets the symbol layer on the next
		// sweep. The detector is what keeps that from costing a Python
		// start-up per project.
		Structural: func() (graphify.Info, bool) {
			st := operatorSettings.EffectiveStructural()
			if !st.Enabled {
				return graphify.Info{}, false
			}
			python := st.Python
			if python == "" {
				python = cfg.BrainStructuralPython
			}
			return structural.Look(ctx, python)
		},
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
	// The one Claude identity Mimir spends, in a credential slot of its own.
	accounts := account.NewRegistry(db, cfg.ClaudeSessionDir, cfg.ClaudeCLIPath)

	// A launch starts as whoever the keychain still holds. The slot outlives
	// the app, so connecting is a one-time act, but a login can lapse or be
	// revoked between launches — and a row the keychain no longer backs would
	// advertise capacity that is not there. So the probe decides, here, before
	// anything can try to spend it.
	if acct, ok, err := accounts.Restore(ctx); err != nil {
		slog.Warn("restoring the Claude account", "dir", cfg.ClaudeSessionDir, "error", err)
	} else if ok {
		slog.Info("claude account", "dir", cfg.ClaudeSessionDir, "state", "connected", "id", acct.ID)
	} else {
		slog.Info("claude account", "dir", cfg.ClaudeSessionDir, "state", "not connected")
	}

	// The daemon's own model calls — refine, distil, recap — are not coding
	// runs: nothing dispatched them, so without this they spend whichever
	// identity this process happens to have inherited.
	//
	// They spend the same account as everything else, because there is only one
	// and "which account paid for this?" should never have two answers. Reading
	// the directory through the registry rather than capturing it keeps that
	// true after a reset. Environ also clears any inherited
	// CLAUDE_SECURESTORAGE_CONFIG_DIR, so this holds whether the daemon was
	// started by launchd or from a shell that had switched accounts.
	router.UseEnviron(func() []string {
		return account.Environ(os.Environ(), accounts.Dir())
	})

	// The operator's standing preference for the classes the daemon routes on
	// its own — Brain's distil and relation passes, refine, the catalog
	// rewrite. Read per call, so editing it in Settings takes effect without a
	// restart, and validated against the published table before it was stored
	// (both halves become argv).
	router.UseDefaults(func(c llm.Class) llm.Selection {
		values, err := operatorSettings.Get()
		if err != nil {
			// A settings file that will not read is not a reason to stop
			// routing: the class defaults are still correct (SD-6).
			return llm.Selection{}
		}
		choice := values.Distill
		if c == llm.Reason {
			choice = values.Reason
		}
		if !connectionRegistry.Allows(choice.Provider, choice.Model) {
			// A preference that names something this daemon will not run is
			// ignored rather than obeyed: it would end as argv otherwise, and
			// a stale settings file must not outrank the allow-list.
			return llm.Selection{}
		}
		return llm.Selection{Provider: choice.Provider, Model: choice.Model}
	})

	runner := coderunner.New(ctx, cfg, bus, projects, accounts, db)
	runner.SetSkills(skillStore)
	// Lead-gen as a sub-agent. Registered before Resume, because an executor
	// registered after the queue has been pumped would leave its own rows
	// refused as unknown. The same pipeline still answers POST /maps/leadgen —
	// one engine, two doors, one permit pool.
	runner.Register(leadgenjob.New(leadgenPipe))

	// The catalog card. Registered here for the same reason lead-gen's is —
	// before Resume, or a row this binary can run is refused as unknown — and
	// it drives the same studio the /catalog/* routes do. One engine, two
	// doors: the routes are "I am looking at this product now", the card is
	// "work through these two hundred and tell me when it is done".
	catalogExec := catalogjob.New(cfg, catalogStudio, catalogjob.FromSettings(cfg, operatorSettings))
	// Which standing instructions a catalog pass runs under depends on the
	// card's target language, not on the agent, so this executor resolves them
	// itself rather than taking what the runner composed.
	catalogExec.UseSkills(skillStore)
	runner.Register(catalogExec)

	// Before the API is serving: a row still marked running belongs to a daemon
	// that is gone, and the queue it left behind is meant to be picked up here.
	// A failure is logged rather than fatal — the daemon is still useful, it
	// just starts with a stale board.
	if err := runner.Resume(ctx); err != nil {
		slog.Warn("resuming coding tasks", "error", err)
	}

	apiDeps := api.Deps{
		Skills:      skillStore,
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
		GraphRead:  knowledge,
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

		// The operator's model choice and the two outreach rule files. Files in
		// a directory, so this one needs neither the store nor a region source.
		Settings: operatorSettings,
		// What this machine can actually run. The provider table is a constant
		// the binary always publishes; whether each entry's CLI is installed
		// and its login works is the machine's answer, and the router is the
		// only thing that can give it.
		LLM:         router,
		Connections: connectionRegistry,
		// The two writes that remove. Wired together because the screen offers
		// them together: moving a project moves its registration with it.
		BrainKeep:   knowledge,
		ProjectKeep: projects,
		// The same detector the sweep asks, so the tab cannot report an
		// availability the loop disagrees with.
		Structural: structural,

		// The product content studio. Wired with the store rather than behind
		// a pipeline guard: importing an export, reading what the brand kit
		// made of it and writing the file back out are all things a daemon
		// that can run no cards at all still does.
		Catalog: catalogStudio,
	}
	// Set the interface field only when there is a real pipeline behind it: a
	// nil *leadgen.Pipeline in an interface is still a non-nil interface, and
	// the route guard checks the interface.
	if leadgenPipe != nil {
		apiDeps.LeadGen = leadgenPipe
		apiDeps.Outreach = outreachStatuser
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

	// The account is deliberately left alone. Quitting is not signing out: the
	// slot is Mimir's own, the keychain keeps what is in it, and the next
	// launch reconciles it. Signing out is the operator's act, and the route
	// that does it is POST /accounts/reset.

	return serveErr
}
