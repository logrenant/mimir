package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/logrenant/mimir/internal/brain"
	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/mcp"
	"github.com/logrenant/mimir/internal/skills"
)

// The four graph tools.
//
// They answer the questions Graphify worked out are worth asking of a code
// graph, over Mimir's own edges rather than a second graph file: what is this
// connected to, how do these two reach each other, who depends on this, where
// are the architectural centres.
//
// All four declare the graph-query skill, so they go through the mandate at the
// choke-point (internal/mcp/finalize.go): the consumer is handed the discipline
// once — expand into the graph's own vocabulary, invent nothing, cite the
// source location, say so when there is no answer — and then only its version.
// Without that discipline these tools are a way to produce confident nonsense,
// because a literal matcher returns zero for a question phrased in other words
// and zero looks exactly like "there is nothing there".
//
// Every response is metadata: a symbol's name, its file, its line, the kind of
// edge it was reached through. No page text, so nothing here can carry
// unrefined scraped content past SD-2.

type graphBase struct {
	cfg  config.Config
	core *brain.Core
}

// Skills is what makes these tools go through the mandate. Declared on the
// base, so a fifth graph tool cannot be added without it.
func (b *graphBase) Skills() []string { return []string{skills.GraphQuery} }

// SkillBudgetTokens is the room the attached skill is allowed. It is added to
// the response's own budget rather than taken out of it — the instructions must
// not shrink the answer they came with.
func (b *graphBase) SkillBudgetTokens() int { return b.cfg.SkillBodyMaxTokens }

func (b *graphBase) scope(path string) (string, error) { return canonicalProject(path) }

// graphResponse is every graph tool's answer.
type graphResponse struct {
	// Expanded is present on a query: the vocabulary the question was actually
	// run as, so the reader can tell a miss from an absence.
	Expanded []string    `json:"expanded,omitempty"`
	Hits     []brain.Hit `json:"hits"`
	Note     string      `json:"note,omitempty"`

	Metadata bool `json:"metadata"`
	budget   int
}

func (r graphResponse) MetadataOnly() bool    { return r.Metadata }
func (r graphResponse) SizeBudgetTokens() int { return r.budget }

func (b *graphBase) answer(res brain.QueryResult) graphResponse {
	return graphResponse{
		Expanded: res.Expanded,
		Hits:     res.Hits,
		Note:     res.Note,
		Metadata: true,
		budget:   b.cfg.BrainSearchMaxTokens,
	}
}

func (b *graphBase) hits(hits []brain.Hit, note string) graphResponse {
	return graphResponse{
		Hits: hits, Note: note, Metadata: true, budget: b.cfg.BrainSearchMaxTokens,
	}
}

// --- graph_query -------------------------------------------------------------

type graphQueryTool struct{ graphBase }

// NewGraphQuery builds the traversal tool.
func NewGraphQuery(cfg config.Config, core *brain.Core) mcp.Tool {
	return &graphQueryTool{graphBase{cfg: cfg, core: core}}
}

func (t *graphQueryTool) Name() string { return "graph_query" }

func (t *graphQueryTool) Description() string {
	return "Ask the code graph a question in plain words and walk out from whatever it matches. " +
		"Returns symbols and files with the edge each was reached through and where it lives, " +
		"plus the vocabulary the question was actually searched as — so a miss can be told " +
		"from an absence. Costs no model call. Use it before reading files to find out where " +
		"something is; use fetch_page or Read for what it says."
}

func (t *graphQueryTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"question": { "type": "string", "minLength": 1 },
			"budget": {
				"type": "integer",
				"minimum": 1,
				"maximum": 200,
				"description": "Maximum nodes to return. Defaults to a compact number."
			},
			"project_path": {
				"type": "string",
				"description": "Absolute path of the project to scope to. Defaults to the working directory."
			}
		},
		"required": ["question"],
		"additionalProperties": false
	}`)
}

func (t *graphQueryTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		Question    string `json:"question"`
		Budget      int    `json:"budget"`
		ProjectPath string `json:"project_path"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Question) == "" {
		return nil, errors.New("question is required")
	}
	scope, err := t.scope(in.ProjectPath)
	if err != nil {
		return nil, err
	}
	res, err := t.core.Query(ctx, scope, in.Question, in.Budget)
	if err != nil {
		return nil, err
	}
	return t.answer(res), nil
}

// --- graph_affected ----------------------------------------------------------

type graphAffectedTool struct{ graphBase }

// NewGraphAffected builds the reverse-traversal tool.
func NewGraphAffected(cfg config.Config, core *brain.Core) mcp.Tool {
	return &graphAffectedTool{graphBase{cfg: cfg, core: core}}
}

func (t *graphAffectedTool) Name() string { return "graph_affected" }

func (t *graphAffectedTool) Description() string {
	return "Walk the call and import edges backwards to find what depends on a symbol — who calls " +
		"it, what imports it, what would have to change if it did. Takes a node id from " +
		"graph_query. Costs no model call."
}

func (t *graphAffectedTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"node_id": { "type": "string", "minLength": 1 },
			"depth": {
				"type": "integer",
				"minimum": 1,
				"maximum": 5,
				"description": "How many hops back to walk. Defaults to 2."
			},
			"kinds": {
				"type": "array",
				"items": { "type": "string" },
				"description": "Edge kinds to follow. Defaults to every parser-derived kind."
			}
		},
		"required": ["node_id"],
		"additionalProperties": false
	}`)
}

func (t *graphAffectedTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		NodeID string   `json:"node_id"`
		Depth  int      `json:"depth"`
		Kinds  []string `json:"kinds"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.NodeID) == "" {
		return nil, errors.New("node_id is required")
	}
	hits, err := t.core.Affected(ctx, in.NodeID, in.Kinds, in.Depth)
	if err != nil {
		return nil, err
	}
	note := ""
	if len(hits) == 0 {
		note = "nothing in the graph depends on this node"
	}
	return t.hits(hits, note), nil
}

// --- graph_path --------------------------------------------------------------

type graphPathTool struct{ graphBase }

// NewGraphPath builds the shortest-chain tool.
func NewGraphPath(cfg config.Config, core *brain.Core) mcp.Tool {
	return &graphPathTool{graphBase{cfg: cfg, core: core}}
}

func (t *graphPathTool) Name() string { return "graph_path" }

func (t *graphPathTool) Description() string {
	return "Find the shortest chain of edges between two nodes — how one part of a codebase " +
		"actually reaches another. Takes two node ids from graph_query. Costs no model call."
}

func (t *graphPathTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"from": { "type": "string", "minLength": 1 },
			"to":   { "type": "string", "minLength": 1 }
		},
		"required": ["from", "to"],
		"additionalProperties": false
	}`)
}

func (t *graphPathTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.From) == "" || strings.TrimSpace(in.To) == "" {
		return nil, errors.New("from and to are required")
	}
	hits, err := t.core.Path(ctx, in.From, in.To)
	if err != nil {
		return nil, err
	}
	note := ""
	if len(hits) == 0 {
		note = "the graph holds no chain between these two"
	}
	return t.hits(hits, note), nil
}

// --- graph_hubs --------------------------------------------------------------

type graphHubsTool struct{ graphBase }

// NewGraphHubs builds the architectural-centres tool.
func NewGraphHubs(cfg config.Config, core *brain.Core) mcp.Tool {
	return &graphHubsTool{graphBase{cfg: cfg, core: core}}
}

func (t *graphHubsTool) Name() string { return "graph_hubs" }

func (t *graphHubsTool) Description() string {
	return "List the most connected nodes in a project — the architectural centres, the files and " +
		"symbols everything else hangs off. The fastest way to learn the shape of an unfamiliar " +
		"repository. Costs no model call."
}

func (t *graphHubsTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"top": {
				"type": "integer",
				"minimum": 1,
				"maximum": 50,
				"description": "How many to return. Defaults to 10."
			},
			"project_path": {
				"type": "string",
				"description": "Absolute path of the project to scope to. Defaults to the working directory."
			}
		},
		"additionalProperties": false
	}`)
}

func (t *graphHubsTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		Top         int    `json:"top"`
		ProjectPath string `json:"project_path"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return nil, err
		}
	}
	scope, err := t.scope(in.ProjectPath)
	if err != nil {
		return nil, err
	}
	hits, err := t.core.Hubs(ctx, scope, in.Top)
	if err != nil {
		return nil, err
	}
	note := ""
	if len(hits) == 0 {
		note = "this project has no graph yet — run a scan first"
	}
	return t.hits(hits, note), nil
}
