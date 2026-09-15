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
		m.catchUpArchive(ctx, log)
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

	// The recap budget is spent across the pass, not per project.
	//
	// It used to be per project, which was affordable while "every project"
	// meant the handful the operator had registered. It is not affordable now
	// that the loop covers everything the scan discovers: eighteen projects
	// would have spent eighteen batches of model calls per pass, on a machine
	// with one distil provider that a person is also trying to use. Reading
	// transcripts and archiving them is untouched — both are free — so the
	// projects at the end of the list still get their history *recorded* on
	// every pass; what they wait their turn for is the summary.
	budget := m.cfg.MemoryIngestBatch

	pending := false
	for _, p := range projects {
		if ctx.Err() != nil {
			return false
		}
		stats, err := m.Ingest(ctx, p, budget)
		if err != nil {
			if ctx.Err() == nil {
				log.Warn("memory: ingest failed", "project", p.Path, "error", err)
			}
			continue
		}
		budget -= stats.Recapped
		if budget < 0 {
			budget = 0
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

// archiveCatchUpBatch is how many transcripts one pass rewinds.
//
// Small, because rewinding one is not free: the next pass re-reads the whole
// file. Spreading the debt over passes keeps a daemon that has just been
// upgraded from spending its first minutes re-parsing every transcript on the
// machine, and the work is resumable by construction — a source that has been
// archived stops being named.
const archiveCatchUpBatch = 4

// catchUpArchive pays down what the verbatim archive missed.
//
// The archive was added after the ingest had already read most of this
// machine's history, and it only ever caught what came afterwards. This finds
// the episodes with no conversation behind them and sends the reader back to
// the start of the transcripts that hold them; the ordinary ingest does the
// rest. No model call: re-ingesting an episode leaves its recap alone.
//
// It ends on its own. When nothing is missing, this costs one query per pass.
func (m *Memory) catchUpArchive(ctx context.Context, log *slog.Logger) {
	if m.archive == nil {
		return
	}
	sources, err := m.archive.SourcesMissingArchive(ctx, archiveCatchUpBatch)
	if err != nil || len(sources) == 0 {
		return
	}

	rewound := 0
	for _, path := range sources {
		if ctx.Err() != nil {
			return
		}
		// At most once per run, whatever the query says. The criterion is
		// per source and converges for any transcript with something to
		// archive; this is the belt for the one that has not — a file whose
		// every episode is a tool-only exchange would otherwise be rewound on
		// every pass, re-read, and named again.
		if _, done := m.rewound[path]; done {
			continue
		}
		if err := m.archive.RewindIngest(ctx, path); err != nil {
			continue
		}
		if m.rewound == nil {
			m.rewound = map[string]struct{}{}
		}
		m.rewound[path] = struct{}{}
		rewound++
	}
	if rewound > 0 {
		log.Info("memory: archiving history recorded before the archive existed",
			"transcripts", rewound)
	}
}
