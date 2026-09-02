package brain

import (
	"context"
	"log/slog"
	"time"
)

// CaptureDeps are the two things a capture pass reads besides the store: the
// project memory it promotes from, and the bookmark it keeps.
//
// Both are interfaces so the loop can be tested without a database and without
// a repository on disk, and so this package stays a reader of the memory rather
// than a second writer of it.
type CaptureDeps struct {
	Episodes EpisodeSource
	Cursors  CursorStore
}

// CaptureStats is what one pass over one project recorded.
type CaptureStats struct {
	Sessions int
	Files    int
	Edges    int
	Commits  int
}

// CaptureProject runs one pass for one project.
//
// Every step is independent and none is fatal: a project whose git history
// cannot be read still gets its sessions promoted, and a store that goes away
// mid-pass costs this tick, not the loop. Nothing here calls a model — that is
// the property that makes running it over a long backlog a migration rather
// than a bill, and it is worth checking every time this file is touched.
func (c *Core) CaptureProject(ctx context.Context, projectPath string, deps CaptureDeps) (CaptureStats, error) {
	var stats CaptureStats
	if !c.Available() || projectPath == "" {
		return stats, nil
	}

	promoted, err := c.Promote(ctx, projectPath, deps.Episodes, c.cfg.BrainPromoteBatch)
	stats.Sessions, stats.Files, stats.Edges = promoted.Sessions, promoted.Files, promoted.Edges
	if err != nil {
		return stats, err
	}
	if ctx.Err() != nil {
		return stats, ctx.Err()
	}

	commits, err := c.CaptureCommits(ctx, projectPath, deps.Cursors, c.cfg.BrainCommitBatch)
	stats.Commits = commits
	return stats, err
}

// Run captures for every known project, forever.
//
// It is a separate loop from internal/memory's rather than a step inside it,
// and the reason is ordering: promotion reads recaps that the memory loop is
// still producing, so folding them together would mean every promotion waited
// on a batch of model calls it does not need. Two loops on the same interval
// means a recap written now is promoted within one tick, which is soon enough
// for something nothing is blocking on.
func (c *Core) Run(ctx context.Context, list func(context.Context) ([]string, error), deps CaptureDeps, log *slog.Logger) {
	if !c.Available() || list == nil {
		return
	}
	if log == nil {
		log = slog.Default()
	}

	for {
		c.pass(ctx, list, deps, log)
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(c.cfg.MemoryIngestInterval):
		}
	}
}

func (c *Core) pass(ctx context.Context, list func(context.Context) ([]string, error), deps CaptureDeps, log *slog.Logger) {
	projects, err := list(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Warn("brain: listing projects failed", "error", err)
		}
		return
	}

	for _, p := range projects {
		if ctx.Err() != nil {
			return
		}
		stats, err := c.CaptureProject(ctx, p, deps)
		if err != nil && ctx.Err() == nil {
			log.Warn("brain: capture failed", "project", p, "error", err)
		}
		if stats.Sessions > 0 || stats.Commits > 0 {
			log.Info("brain: captured",
				"project", p,
				"sessions", stats.Sessions,
				"files", stats.Files,
				"edges", stats.Edges,
				"commits", stats.Commits)
		}
	}
}
