// Command mimir-scan reads every project on this machine into Brain.
//
// The MCP tool `brain_scan_repo` does one bounded batch per call, because its
// caller is a session with a request timeout and a context budget. Reading a
// whole machine that way means hundreds of round-trips through a model that is
// not the one doing the reading — the session pays for a job whose actual work
// is a `gemini-3.8-flash-low` call per file (internal/llm routes the distil
// class to `agy`, and falls back to `claude` only when agy is missing or out of
// quota).
//
// So this is the same Core.Scan, driven by a loop instead of by a session: the
// operator starts it, it runs to completion, and it prints its progress to
// stderr. Since task-51 mimir-daemon runs the same sweep continuously over
// ~/development and ~/Documents (internal/brain/supervisor.go); this binary
// stays for the headless case and for a root the daemon does not watch. It is resumable by construction — a file whose content hash and
// prompt version already match is skipped without a model call, so an
// interrupted run costs nothing to restart.
//
//	mimir-scan ~/development          scan every project under a root
//	mimir-scan -n ~/development       report what it would cost, spend nothing
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/logrenant/mimir/internal/brain"
	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/llm"
	mimirmcp "github.com/logrenant/mimir/internal/mcp"
	"github.com/logrenant/mimir/internal/store"
)

// Populated via -ldflags by `make release`.
var (
	version = "dev"
	commit  = "none"
)

func main() {
	mimirmcp.InitLogging(os.Stderr)

	dryRun := flag.Bool("n", false, "report what a scan would do without making any model call")
	flag.Parse()

	if err := run(flag.Args(), *dryRun); err != nil {
		slog.Error("scan failed", "error", err)
		os.Exit(1)
	}
}

func run(roots []string, dryRun bool) error {
	if len(roots) == 0 {
		return errors.New("mimir-scan needs at least one root directory; there is no default")
	}

	slog.Info("mimir-scan starting", "version", version, "commit", commit, "dry_run", dryRun)

	// Interrupting is a supported way to finish: the pass in flight is
	// cancelled, everything already distilled stays in the store, and the next
	// run picks up exactly where this one stopped.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			slog.Info("interrupted: finishing the pass in flight, then stopping")
			cancel()
		case <-ctx.Done():
		}
	}()

	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		return err
	}

	// Unlike the MCP server, this binary is the store: there is nothing useful
	// it can do without one, so a store that will not open is fatal rather than
	// a degraded mode.
	db, err := store.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Warn("closing the store", "error", err)
		}
	}()

	core := brain.New(cfg, db, llm.NewRouter(cfg))

	var projects []string
	for _, root := range roots {
		found, err := brain.DiscoverProjects(root, cfg.BrainScanDepth)
		if err != nil {
			return err
		}
		slog.Info("discovered", "root", root, "projects", len(found))
		projects = append(projects, found...)
	}

	var totals brain.ScanResult
	for i, project := range projects {
		res, err := scanOne(ctx, core, db, project, i+1, len(projects), dryRun)
		totals.Scanned += res.Scanned
		totals.Skipped += res.Skipped
		totals.Failed += res.Failed
		totals.Eligible += res.Eligible
		totals.Remaining += res.Remaining
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			// One project that cannot be read is not a reason to abandon the
			// rest of the machine.
			slog.Warn("project skipped", "project", project, "error", err)
		}
	}

	slog.Info("mimir-scan finished",
		"projects", len(projects),
		"scanned", totals.Scanned,
		"unchanged", totals.Skipped,
		"failed", totals.Failed,
		"eligible", totals.Eligible,
		"remaining", totals.Remaining,
		"dry_run", dryRun)
	return ctx.Err()
}

// scanOne runs one project to completion, one bounded pass at a time.
//
// The loop condition is the pass's own `remaining`, not a count computed up
// front: a file that changes while the scan is running is picked up by the next
// pass rather than being missed by an arithmetic that was decided in advance.
func scanOne(ctx context.Context, core *brain.Core, hashes brain.HashStore, project string, n, of int, dryRun bool) (brain.ScanResult, error) {
	log := slog.With("project", project, "n", n, "of", of)

	var totals brain.ScanResult
	for pass := 1; ; pass++ {
		started := time.Now()
		res, err := core.Scan(ctx, project, hashes, brain.ScanOptions{DryRun: dryRun})
		if err != nil {
			return totals, err
		}

		totals.Scanned += res.Scanned
		totals.Failed += res.Failed
		// Skipped and Eligible are re-counted from scratch by every pass, so
		// they are the pass's figures, not a running sum.
		totals.Skipped = res.Skipped
		totals.Eligible = res.Eligible
		totals.Remaining = res.Remaining

		if dryRun {
			log.Info("would scan", "files", res.Remaining, "unchanged", res.Skipped, "eligible", res.Eligible)
			return totals, nil
		}

		log.Info("pass done",
			"pass", pass,
			"scanned", res.Scanned,
			"failed", res.Failed,
			"remaining", res.Remaining,
			"took", time.Since(started).Round(time.Second).String())

		if res.Remaining == 0 {
			return totals, nil
		}
		// A pass that distilled nothing and still reports work left would spin
		// forever: the files it could not read are unreadable now and will be
		// unreadable next time round.
		if res.Scanned == 0 {
			log.Warn("no progress; leaving the rest of this project", "remaining", res.Remaining)
			return totals, nil
		}
		if ctx.Err() != nil {
			return totals, ctx.Err()
		}
	}
}
