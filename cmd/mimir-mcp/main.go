package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/crawl"
	mimirmcp "github.com/logrenant/mimir/internal/mcp"
	"github.com/logrenant/mimir/internal/memory"
	"github.com/logrenant/mimir/internal/pipeline"
	"github.com/logrenant/mimir/internal/project"
	"github.com/logrenant/mimir/internal/refine"
	"github.com/logrenant/mimir/internal/search"
	"github.com/logrenant/mimir/internal/store"
	"github.com/logrenant/mimir/internal/tools"
)

// Populated via -ldflags "-X main.version=… -X main.commit=…" by `make release`.
var (
	version = "dev"
	commit  = "none"
)

func main() {
	// 6. Logging: slog JSON handler writing to stderr only.
	mimirmcp.InitLogging(os.Stderr)
	slog.Info("mimir-mcp starting", "version", version, "commit", commit)

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
	go func() {
		<-sigCh
		cancel()
	}()

	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		return err
	}

	srv := mimirmcp.NewServer(cfg)

	searchClient := search.New(cfg)
	crawlClient := crawl.New(cfg)
	refineClient := refine.New(cfg)

	// The cache is a nice-to-have, never a requirement: if it cannot be opened
	// the server runs uncached rather than refusing to start (SD-6). A nil
	// pipeline.Cache is the documented "no caching" value.
	var cache pipeline.Cache
	pageStore, err := store.Open(ctx, cfg)
	if err != nil {
		slog.Warn("page cache disabled", "path", cfg.StorePath, "error", err)
	} else {
		defer func() {
			if err := pageStore.Close(); err != nil {
				slog.Warn("closing page cache", "error", err)
			}
		}()
		cache = pageStore
	}

	pipe := pipeline.New(cfg, searchClient, crawlClient, refineClient, cache)

	// The project memory shares the store's fate: no store, no memory, and
	// RegisterAll then omits its tools rather than advertising them. There is
	// no run source here — that is the daemon's to supply — so this process
	// remembers interactive sessions only.
	var mem *memory.Memory
	var projects tools.ProjectFinder
	if pageStore != nil {
		mem = memory.New(cfg, pageStore, refineClient, nil)
		projects = project.NewRegistry(pageStore)
	}

	// One canonical tool list, shared with cmd/mimir-daemon (task-22).
	if err := tools.RegisterAll(srv.Registry(), cfg, tools.Deps{
		Search:   searchClient,
		Crawl:    crawlClient,
		Refine:   refineClient,
		Pipeline: pipe,
		Memory:   mem,
		Projects: projects,
	}); err != nil {
		return err
	}

	return srv.Run(ctx)
}
