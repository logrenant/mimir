// Package memory keeps what this repository already learned, so a session does
// not have to rediscover it.
//
// It joins three things that each already exist: internal/sessionlog turns
// transcripts into episodes, internal/refine distils one episode into a couple
// of lines, and internal/store keeps and searches the result. Nothing here
// opens a socket or spawns a process of its own.
//
// # Why the shape is what it is
//
// The obvious design — one memory document the model rewrites as it learns —
// is the one this repository already tried, in goat v1, and it failed badly
// enough to be worth describing. A single rewrite degenerated, and because the
// document was the memory, the degenerate output *became* the project's
// permanent memory. It is still on disk in that repo, still unreadable.
//
// So there is no document here. There are rows, each holding one episode's
// deterministic facts plus at most a couple of refined lines, and the "briefing"
// a consumer reads is assembled from those rows at read time, every time. Three
// consequences follow, and they are the point of the package:
//
//   - A bad generation can spoil one row. It cannot spoil the memory.
//   - A rejected recap costs nothing: the episode keeps its facts, stays
//     searchable, and is simply less eloquent than its neighbours.
//   - The memory cannot silently drift away from the repository, because the
//     repository is re-read on every brief and always wins.
package memory

import (
	"context"
	"encoding/json"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/refine"
	"github.com/logrenant/mimir/internal/sessionlog"
	"github.com/logrenant/mimir/internal/store"
)

// Store is the persistence this package needs. Narrow on purpose: it is
// satisfied by *store.Store, and by a fake in tests, and by nothing that would
// tempt this package into owning a database.
type Store interface {
	PutEpisode(ctx context.Context, e store.EpisodeRow) error
	UpdateRecap(ctx context.Context, key, title, summary, promptVersion string) error
	PendingRecap(ctx context.Context, projectPath, promptVersion string, maxAttempts, limit int) ([]store.EpisodeRow, error)
	CountPendingRecap(ctx context.Context, projectPath, promptVersion string, maxAttempts int) (int, error)
	RecentEpisodes(ctx context.Context, projectPath string, limit int) ([]store.EpisodeRow, error)
	SearchEpisodes(ctx context.Context, projectPath, query string, limit int) ([]store.EpisodeRow, error)
	PutNote(ctx context.Context, n store.NoteRow) error
	ListNotes(ctx context.Context, projectPath string, limit int) ([]store.NoteRow, error)
	GetIngestState(ctx context.Context, sourcePath string) (store.IngestState, bool, error)
	PutIngestState(ctx context.Context, st store.IngestState) error
	MemoryStats(ctx context.Context, projectPath string) (store.MemoryStats, error)
}

// ChatArchive keeps the conversation itself — the text a Store row deliberately
// clips. *store.Store satisfies it, and it is optional: a nil archive leaves
// ingest exactly as it was before the archive existed (SD-6).
//
// A second interface rather than four more methods on Store because it fails
// separately and is written on a different contract: Store rows are an index a
// model reads, and this is a record no model ever reads.
type ChatArchive interface {
	PutChatTurn(ctx context.Context, t store.ChatTurnRow) error

	// And the two reads that let the archive catch up on what it missed. The
	// archive arrived after the ingest did, so every episode recorded before
	// it has no verbatim text and never will unless its transcript is read
	// again — see backfill.go.
	SourcesMissingArchive(ctx context.Context, limit int) ([]string, error)
	RewindIngest(ctx context.Context, sourcePath string) error
}

// Refiner is the one model call this package makes.
type Refiner interface {
	Recap(ctx context.Context, in refine.RecapInput) (refine.Output, error)
}

// RunSource exposes this daemon's own coding runs. It is optional: mimir-mcp
// runs without a daemon and has no runs to read, and a memory built only from
// interactive sessions is a smaller memory, not a broken one.
type RunSource interface {
	ListRunsByProject(ctx context.Context, projectID string, limit int) ([]store.RunRow, error)
}

// Project is the subject of every call here. Both fields come from
// internal/project.Registry, which is the only thing allowed to turn a
// caller-supplied string into a directory; this package never validates a path
// itself and never accepts one that has not been through there.
type Project struct {
	ID   string
	Path string
}

// Memory is the package handle.
type Memory struct {
	cfg     config.Config
	store   Store
	refiner Refiner
	runs    RunSource
	archive ChatArchive

	// rewound is which transcripts this run has already sent the reader back
	// through, so the archive catch-up cannot loop on a file that has nothing
	// to archive. In process rather than stored: re-reading a transcript costs
	// no model call, so paying it once more after a restart is cheaper than a
	// schema for it. Only the ingest loop touches this, and only from its own
	// goroutine.
	rewound map[string]struct{}
}

// New builds a Memory. runs may be nil.
func New(cfg config.Config, s Store, r Refiner, runs RunSource) *Memory {
	return &Memory{cfg: cfg, store: s, refiner: r, runs: runs, rewound: map[string]struct{}{}}
}

// UseArchive installs the verbatim chat archive.
//
// Set after construction rather than taken by New because it is not part of
// what this package does: the memory distils, and the archive keeps. A Memory
// without one behaves exactly as it did before task-65, which is also what
// mimir-mcp gets — a short-lived stdio process has no backlog to archive.
func (m *Memory) UseArchive(a ChatArchive) { m.archive = a }

// archiveTurn stores the conversation behind an episode. It makes no model
// call, and a failure is logged by the caller rather than aborting the ingest:
// the episode row is the thing the memory promises, and the archive is what it
// keeps beside it.
func (m *Memory) archiveTurn(ctx context.Context, e sessionlog.Episode) error {
	if m.archive == nil {
		return nil
	}
	return m.archive.PutChatTurn(ctx, store.ChatTurnRow{
		EpisodeKey:    e.Key,
		SessionID:     e.SessionID,
		ProjectPath:   e.ProjectPath,
		SourceKind:    string(e.SourceKind),
		GitBranch:     e.GitBranch,
		SourcePath:    e.SourcePath,
		StartedAt:     e.StartedAt,
		EndedAt:       e.EndedAt,
		UserPrompt:    e.UserPrompt,
		AssistantText: e.AssistantText,
		ToolCallsJSON: jsonOrEmpty(e.ToolCalls),
		FilesJSON:     jsonOrEmpty(e.FilesTouched),
		CommandsJSON:  jsonOrEmpty(e.Commands),
		InputTokens:   e.InputTokens,
		OutputTokens:  e.OutputTokens,
		CostUSD:       e.CostUSD,
	})
}

// jsonOrEmpty marshals v, falling back to an empty array. A value that will not
// marshal costs the archive one column, never the turn.
func jsonOrEmpty(v any) string {
	blob, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(blob)
}

// facts is the structured half of an episode row. It is stored as JSON rather
// than columns because nothing queries inside it — the brief counts over it and
// the index reads the flat projections beside it.
type facts struct {
	Files    []string `json:"files,omitempty"`
	Commands []string `json:"commands,omitempty"`
	// Failed carries the labels, not a count: which step broke is the reusable
	// part, and a number cannot be turned back into it.
	Failed     []string `json:"failed,omitempty"`
	ToolCalls  int      `json:"tool_calls,omitempty"`
	Prompt     string   `json:"prompt,omitempty"`
	Outcome    string   `json:"outcome,omitempty"`
	SourceKind string   `json:"source_kind,omitempty"`
}

// toRow converts a parsed episode into the row the store holds.
//
// An open episode is stored with significance 0 while its transcript is still
// warm. That is not a judgement about the work — it is how the ingester avoids
// paying for a recap of an episode that is still being written, and the next
// pass over a cold file restores the real score.
func (m *Memory) toRow(e sessionlog.Episode, hot bool) store.EpisodeRow {
	// The two text ceilings are applied here rather than in the parser: an
	// episode row is an index entry, and since task-65 the same parse also
	// feeds the chat archive, which keeps exactly the text this drops.
	f := facts{
		Files:      e.FilesTouched,
		Commands:   e.Commands,
		Failed:     e.FailedSteps(),
		ToolCalls:  len(e.ToolCalls),
		Prompt:     sessionlog.Clip(e.UserPrompt, sessionlog.MaxPromptChars),
		Outcome:    sessionlog.Clip(e.AssistantText, sessionlog.MaxAssistantChars),
		SourceKind: string(e.SourceKind),
	}
	blob, err := json.Marshal(f)
	if err != nil {
		blob = []byte("{}")
	}

	significance := e.Significance()
	if e.Open && hot {
		significance = 0
	}

	return store.EpisodeRow{
		Key:          e.Key,
		ProjectPath:  e.ProjectPath,
		SourceKind:   string(e.SourceKind),
		SourcePath:   e.SourcePath,
		SessionID:    e.SessionID,
		GitBranch:    e.GitBranch,
		StartedAt:    e.StartedAt,
		EndedAt:      e.EndedAt,
		FactsJSON:    string(blob),
		FilesText:    e.FilesText(),
		CommandsText: e.CommandsText(),
		InputTokens:  e.InputTokens,
		OutputTokens: e.OutputTokens,
		CostUSD:      e.CostUSD,
		Significance: significance,
	}
}

func decodeFacts(blob string) facts {
	var f facts
	if blob == "" {
		return f
	}
	// A row whose facts will not decode is still a usable row: it has a title,
	// a summary and a timestamp. Losing the file list is not worth an error.
	_ = json.Unmarshal([]byte(blob), &f)
	return f
}

// episodeFacts rebuilds the block the recap prompt reads, from a stored row.
//
// It goes through sessionlog.BuildFacts rather than storing the rendered string
// so there is one definition of the format: a row written before a format
// change still renders in the current shape, and a cached recap keyed on that
// shape stays coherent with it.
func episodeFacts(row store.EpisodeRow) string {
	f := decodeFacts(row.FactsJSON)
	return sessionlog.BuildFacts(sessionlog.FactsInput{
		Branch:      row.GitBranch,
		Prompt:      f.Prompt,
		Outcome:     f.Outcome,
		Files:       f.Files,
		Commands:    f.Commands,
		FailedSteps: f.Failed,
	})
}
