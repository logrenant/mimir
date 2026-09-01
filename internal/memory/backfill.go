package memory

import (
	"context"
	"log/slog"
	"time"
)

// betweenBatches is the breath a catching-up loop takes between passes, so a
// long backfill cannot monopolise the machine's single claude CLI while
// somebody is trying to use it interactively.
const betweenBatches = time.Second

// Run keeps every known project's memory current for as long as ctx lives.
//
// It exists because the interesting history is already on disk. A repository
// worked in for months has that work sitting in transcripts, and a memory that
// only started remembering today would take months to become worth consulting.
// So the loop's first job is to catch up, and only then to keep up.
//
// Each pass spends at most cfg.MemoryIngestBatch model calls per project and
// then yields. That is what makes an interrupted backfill cheap: it loses one
// batch, and every completed pass leaves the memory strictly more useful than
// it found it. While anything is still pending the next pass follows quickly;
// once nothing is, the loop settles onto cfg.MemoryIngestInterval, because the
// only new history then is whatever a session has written since.
//
// list is re-consulted every pass rather than captured once, so a directory
// registered through the picker after start-up is picked up without a restart.
//
// Errors are logged and retried, never fatal: the daemon does not exit because
// a transcript was unreadable, and a project that fails one pass is tried again
// on the next.
func (m *Memory) Run(ctx context.Context, list func(context.Context) ([]Project, error), log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}

	for {
		pending := m.pass(ctx, list, log)
		if ctx.Err() != nil {
			return
		}

		wait := m.cfg.MemoryIngestInterval
		if pending {
			wait = betweenBatches
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// pass ingests one batch for every known project and reports whether any of
// them still has history waiting.
func (m *Memory) pass(ctx context.Context, list func(context.Context) ([]Project, error), log *slog.Logger) bool {
	projects, err := list(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Warn("memory: listing projects failed", "error", err)
		}
		return false
	}

	pending := false
	for _, p := range projects {
		if ctx.Err() != nil {
			return false
		}
		stats, err := m.Ingest(ctx, p, m.cfg.MemoryIngestBatch)
		if err != nil {
			if ctx.Err() == nil {
				log.Warn("memory: ingest failed", "project", p.Path, "error", err)
			}
			continue
		}
		if stats.Remaining > 0 {
			pending = true
		}
		if stats.Episodes > 0 || stats.Recapped > 0 {
			log.Info("memory: ingested",
				"project", p.Path,
				"episodes", stats.Episodes,
				"recapped", stats.Recapped,
				"rejected", stats.Rejected,
				"remaining", stats.Remaining)
		}
	}
	return pending
}
