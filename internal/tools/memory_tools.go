package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/mcp"
	"github.com/logrenant/mimir/internal/memory"
	"github.com/logrenant/mimir/internal/project"
)

// ProjectFinder looks a project up without registering it. Satisfied by
// *project.Registry; kept as an interface so these tools cannot reach the
// registry's Register and mint a permission as a side effect of reading.
type ProjectFinder interface {
	Find(ctx context.Context, path string) (project.Project, bool, error)
}

// memoryBase is the project-resolution the three memory tools share.
type memoryBase struct {
	cfg     config.Config
	mem     *memory.Memory
	finder  ProjectFinder
	catchup int
}

// resolve turns an optional caller-supplied path into a project.
//
// Every path goes through project.Canonicalize — symlinks resolved, denylist
// applied — because a memory tool reads transcript files chosen by that path
// and "which directory is this" must be answered in exactly one place
// (internal/project/AGENTS.md). It deliberately stops short of registering:
// reading a project's history is not grounds for creating the durable
// registration that would let a coding run start in it.
//
// The project id is filled in only when the picker has already registered the
// directory. With one, the memory can also read this daemon's own coding runs;
// without one it reads interactive sessions alone, which is a smaller memory,
// not a broken one.
func (b *memoryBase) resolve(ctx context.Context, path string) (memory.Project, error) {
	resolved, err := canonicalProject(path)
	if err != nil {
		return memory.Project{}, err
	}

	p := memory.Project{Path: resolved}
	if b.finder != nil {
		if known, ok, err := b.finder.Find(ctx, resolved); err == nil && ok {
			p.ID = known.ID
		}
	}
	return p, nil
}

// canonicalProject turns an optional caller-supplied path into a canonical one.
//
// It is shared with the brain tools rather than reimplemented there, because
// "which directory is this" has to keep having exactly one answer
// (internal/project/AGENTS.md) — a second copy is how a denylisted path
// eventually gets through one door and not the other.
func canonicalProject(path string) (string, error) {
	if path == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("no project_path given and the working directory is unreadable: %w", err)
		}
		path = cwd
	}

	resolved, err := project.Canonicalize(path)
	if err != nil {
		if errors.Is(err, project.ErrPathNotAllowed) {
			return "", fmt.Errorf("%q is not an allowed project directory", path)
		}
		return "", fmt.Errorf("%q is not a usable project directory: %w", path, err)
	}
	return resolved, nil
}

// catchUp folds a small amount of ingest into a read.
//
// Without it, mimir-mcp — which is a short-lived stdio process with no daemon
// behind it — would only ever serve what some other process had already
// ingested. The batch is deliberately small: phase 1 of an ingest is free, so
// the index stays current either way, and a read should not stall behind a
// backlog of model calls. The daemon's background loop is what actually drains
// history.
//
// Failure is ignored on purpose. A read that could not also write is still a
// perfectly good read.
func (b *memoryBase) catchUp(ctx context.Context, p memory.Project) {
	if b.catchup <= 0 {
		return
	}
	_, _ = b.mem.Ingest(ctx, p, b.catchup)
}

// --- project_context ---------------------------------------------------------

type projectContextTool struct{ memoryBase }

// budget is unexported so it never reaches the wire: it exists only to tell
// internal/mcp's choke-point which ceiling to hold this response to, and the
// consumer has no use for it.
type projectContextResponse struct {
	memory.Brief
	Refined bool `json:"refined"`
	budget  int
}

func (r projectContextResponse) IsRefined() bool       { return r.Refined }
func (r projectContextResponse) SizeBudgetTokens() int { return r.budget }

// NewProjectContext builds the session-opening tool.
func NewProjectContext(cfg config.Config, mem *memory.Memory, finder ProjectFinder) mcp.Tool {
	return &projectContextTool{memoryBase{cfg: cfg, mem: mem, finder: finder, catchup: cfg.MemoryLazyCatchup}}
}

func (t *projectContextTool) Name() string { return "project_context" }

// Description is load-bearing, not decoration. This tool only saves anything if
// it is called *before* the exploration it replaces, so the description has to
// say when to reach for it — a perfectly good memory that nothing thinks to
// consult costs strictly more than no memory at all.
func (t *projectContextTool) Description() string {
	return "Call this FIRST, before exploring a codebase you have worked in before. " +
		"Returns what earlier sessions in this project already established: what the repository is, " +
		"where its rules live, decisions that were pinned, the most recent work, and the files " +
		"currently being worked in. Compact and already distilled — it replaces re-reading the repo " +
		"to rediscover its shape. Defaults to the current working directory."
}

func (t *projectContextTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"project_path": {
				"type": "string",
				"description": "Absolute path of the project. Defaults to the working directory."
			}
		},
		"additionalProperties": false
	}`)
}

func (t *projectContextTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		ProjectPath string `json:"project_path"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, err
	}

	p, err := t.resolve(ctx, in.ProjectPath)
	if err != nil {
		return nil, err
	}
	t.catchUp(ctx, p)

	brief, err := t.mem.Brief(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("reading the project memory: %w", err)
	}
	return projectContextResponse{Brief: brief, Refined: true, budget: t.cfg.MemoryBriefMaxTokens}, nil
}

// --- context_recall ----------------------------------------------------------

type contextRecallTool struct{ memoryBase }

type contextRecallResponse struct {
	memory.Recall
	Refined bool `json:"refined"`
	budget  int
}

func (r contextRecallResponse) IsRefined() bool       { return r.Refined }
func (r contextRecallResponse) SizeBudgetTokens() int { return r.budget }

// NewContextRecall builds the search tool.
func NewContextRecall(cfg config.Config, mem *memory.Memory, finder ProjectFinder) mcp.Tool {
	return &contextRecallTool{memoryBase{cfg: cfg, mem: mem, finder: finder, catchup: cfg.MemoryLazyCatchup}}
}

func (t *contextRecallTool) Name() string { return "context_recall" }

func (t *contextRecallTool) Description() string {
	return "Search what earlier sessions in this project did and decided. " +
		"Use it before investigating why something is the way it is, or before re-deriving where a " +
		"feature lives: ask in plain words (\"why Places API over scraping\", \"the refine choke-point\") " +
		"and get back short summaries with the files each one touched. " +
		"Returns pointers, not file contents — read the files it names if you need the detail."
}

func (t *contextRecallTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "What you want to know, in plain words."
			},
			"limit": {
				"type": "integer",
				"minimum": 1,
				"maximum": 20,
				"description": "Maximum results. Defaults to a compact number."
			},
			"project_path": {
				"type": "string",
				"description": "Absolute path of the project. Defaults to the working directory."
			}
		},
		"required": ["query"],
		"additionalProperties": false
	}`)
}

func (t *contextRecallTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		Query       string `json:"query"`
		Limit       int    `json:"limit"`
		ProjectPath string `json:"project_path"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Query) == "" {
		return nil, errors.New("query is required")
	}

	p, err := t.resolve(ctx, in.ProjectPath)
	if err != nil {
		return nil, err
	}
	t.catchUp(ctx, p)

	recall, err := t.mem.Recall(ctx, p, in.Query, in.Limit)
	if err != nil {
		return nil, fmt.Errorf("searching the project memory: %w", err)
	}
	return contextRecallResponse{Recall: recall, Refined: true, budget: t.cfg.MemoryRecallMaxTokens}, nil
}

// --- context_remember --------------------------------------------------------

type contextRememberTool struct{ memoryBase }

type contextRememberResponse struct {
	Stored   memory.NoteView `json:"stored"`
	Metadata bool            `json:"metadata_only"`
}

func (r contextRememberResponse) MetadataOnly() bool    { return r.Metadata }
func (r contextRememberResponse) SizeBudgetTokens() int { return 400 }

// NewContextRemember builds the pin tool.
func NewContextRemember(cfg config.Config, mem *memory.Memory, finder ProjectFinder) mcp.Tool {
	// No catch-up: a write is not the place to pay for someone else's backlog.
	return &contextRememberTool{memoryBase{cfg: cfg, mem: mem, finder: finder}}
}

func (t *contextRememberTool) Name() string { return "context_remember" }

func (t *contextRememberTool) Description() string {
	return "Pin one durable fact about this project so later sessions start with it: a decision and " +
		"its reason, a convention, or a trap that cost time. Not for what happened today — that is " +
		"captured automatically. Write the kind of sentence a colleague who already knows the project " +
		"would not need to be told twice."
}

func (t *contextRememberTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"text": {
				"type": "string",
				"description": "The fact, in one or two sentences. Include the reason, not just the rule."
			},
			"kind": {
				"type": "string",
				"enum": ["decision", "convention", "trap", "todo"],
				"description": "decision: a choice and why. convention: how this repo does something. trap: something that cost time. todo: work deliberately left."
			},
			"project_path": {
				"type": "string",
				"description": "Absolute path of the project. Defaults to the working directory."
			}
		},
		"required": ["text", "kind"],
		"additionalProperties": false
	}`)
}

func (t *contextRememberTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		Text        string `json:"text"`
		Kind        string `json:"kind"`
		ProjectPath string `json:"project_path"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, err
	}

	p, err := t.resolve(ctx, in.ProjectPath)
	if err != nil {
		return nil, err
	}

	note, err := t.mem.Remember(ctx, p, in.Kind, in.Text)
	if err != nil {
		return nil, err
	}
	return contextRememberResponse{Stored: note, Metadata: true}, nil
}
