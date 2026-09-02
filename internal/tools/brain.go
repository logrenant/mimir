package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/logrenant/mimir/internal/brain"
	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/mcp"
)

// brainBase is what the four brain tools share.
//
// The core arrives through tools.Deps like every other collaborator. The
// version this replaces kept an unsynchronised package-level singleton that
// pinned the first caller's config forever and raced under the daemon's
// concurrent handlers.
type brainBase struct {
	cfg  config.Config
	core *brain.Core
}

// resolveScope turns the caller's optional project_path into a scope.
//
// It goes through the same canonicalizer the memory tools use. An empty result
// is the global scope, which is a real answer: a node about a public repository
// is not about any one checkout.
func (b *brainBase) resolveScope(path string) (string, error) {
	return canonicalProject(path)
}

// --- brain_ingest_data -------------------------------------------------------

type brainIngestDataTool struct{ brainBase }

// brainIngestResponse carries model-written text (the assessment), so it is a
// refined response and not a metadata one — the choke-point holds it to a
// budget accordingly.
type brainIngestResponse struct {
	brain.IngestResult
	Refined bool `json:"refined"`
	budget  int
}

func (r brainIngestResponse) IsRefined() bool       { return r.Refined }
func (r brainIngestResponse) SizeBudgetTokens() int { return r.budget }

// NewBrainIngestData builds the general ingest tool.
func NewBrainIngestData(cfg config.Config, core *brain.Core) mcp.Tool {
	return &brainIngestDataTool{brainBase{cfg: cfg, core: core}}
}

func (t *brainIngestDataTool) Name() string { return "brain_ingest_data" }

func (t *brainIngestDataTool) Description() string {
	return "Store one thing worth remembering across sessions and across models: a decision and its " +
		"reason, a piece of research, a convention, a finding. It is distilled to a title, a short " +
		"assessment and retrieval terms, then linked to what is already known. " +
		"Not for what happened in this repository today — that is captured automatically; use this for " +
		"knowledge that outlives the session. Read it back with brain_query_nodes."
}

func (t *brainIngestDataTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"source": {
				"type": "string",
				"description": "Stable name for what this is about. Re-using it updates the same node instead of creating another."
			},
			"content": {
				"type": "string",
				"description": "The material to remember."
			},
			"kind": {
				"type": "string",
				"enum": ["note", "research", "decision", "session", "file", "commit"],
				"description": "What sort of thing this is. Defaults to note."
			},
			"project_path": {
				"type": "string",
				"description": "Absolute path of the project this belongs to. Defaults to the working directory."
			}
		},
		"required": ["source", "content"],
		"additionalProperties": false
	}`)
}

func (t *brainIngestDataTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		Source      string `json:"source"`
		Content     string `json:"content"`
		Kind        string `json:"kind"`
		ProjectPath string `json:"project_path"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Source) == "" {
		return nil, errors.New("source is required")
	}
	if strings.TrimSpace(in.Content) == "" {
		return nil, errors.New("content is required")
	}

	scope, err := t.resolveScope(in.ProjectPath)
	if err != nil {
		return nil, err
	}

	res, err := t.core.Ingest(ctx, brain.Input{
		Source:      in.Source,
		Kind:        in.Kind,
		Content:     in.Content,
		ProjectPath: scope,
	})
	if err != nil {
		return nil, err
	}
	return brainIngestResponse{IngestResult: res, Refined: true, budget: t.cfg.BrainSearchMaxTokens}, nil
}

// --- brain_ingest_github -----------------------------------------------------

type brainIngestGitHubTool struct{ brainBase }

// NewBrainIngestGitHub builds the repository ingest tool.
func NewBrainIngestGitHub(cfg config.Config, core *brain.Core) mcp.Tool {
	return &brainIngestGitHubTool{brainBase{cfg: cfg, core: core}}
}

func (t *brainIngestGitHubTool) Name() string { return "brain_ingest_github" }

func (t *brainIngestGitHubTool) Description() string {
	return "Read a GitHub repository's README and store what it is as a node, so a later session can " +
		"be told about the library instead of fetching and re-reading its documentation. " +
		"Stored globally, not against one project. Private repositories need MIMIR_GITHUB_TOKEN."
}

func (t *brainIngestGitHubTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"repo": {
				"type": "string",
				"description": "owner/name, or the repository URL."
			}
		},
		"required": ["repo"],
		"additionalProperties": false
	}`)
}

func (t *brainIngestGitHubTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		Repo string `json:"repo"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Repo) == "" {
		return nil, errors.New("repo is required")
	}

	res, err := t.core.IngestRepo(ctx, in.Repo)
	if err != nil {
		return nil, err
	}
	return brainIngestResponse{IngestResult: res, Refined: true, budget: t.cfg.BrainSearchMaxTokens}, nil
}

// --- brain_query_nodes -------------------------------------------------------

type brainQueryNodesTool struct{ brainBase }

type brainQueryResponse struct {
	Query   string           `json:"query"`
	Nodes   []brain.NodeView `json:"nodes"`
	Refined bool             `json:"refined"`
	budget  int
}

func (r brainQueryResponse) IsRefined() bool       { return r.Refined }
func (r brainQueryResponse) SizeBudgetTokens() int { return r.budget }

// NewBrainQueryNodes builds the search tool.
func NewBrainQueryNodes(cfg config.Config, core *brain.Core) mcp.Tool {
	return &brainQueryNodesTool{brainBase{cfg: cfg, core: core}}
}

func (t *brainQueryNodesTool) Name() string { return "brain_query_nodes" }

func (t *brainQueryNodesTool) Description() string {
	return "Search what is already known about a topic, a library or a decision — across every session " +
		"and every model that wrote into this knowledge base, not just this project. " +
		"Ask in plain words; each result comes back as a short assessment plus the nodes it is linked " +
		"to. Returns pointers, not source material."
}

func (t *brainQueryNodesTool) InputSchema() json.RawMessage {
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
				"description": "Absolute path of the project to scope to. Global nodes are always included. Defaults to the working directory."
			}
		},
		"required": ["query"],
		"additionalProperties": false
	}`)
}

func (t *brainQueryNodesTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
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

	scope, err := t.resolveScope(in.ProjectPath)
	if err != nil {
		return nil, err
	}

	nodes, err := t.core.Search(ctx, scope, in.Query, in.Limit)
	if err != nil {
		return nil, fmt.Errorf("searching the knowledge base: %w", err)
	}
	return brainQueryResponse{
		Query: in.Query, Nodes: nodes, Refined: true, budget: t.cfg.BrainSearchMaxTokens,
	}, nil
}

// --- brain_related -----------------------------------------------------------

type brainRelatedTool struct{ brainBase }

type brainRelatedResponse struct {
	Node    brain.NodeView `json:"node"`
	Refined bool           `json:"refined"`
	budget  int
}

func (r brainRelatedResponse) IsRefined() bool       { return r.Refined }
func (r brainRelatedResponse) SizeBudgetTokens() int { return r.budget }

// NewBrainRelated builds the edge-walk tool.
func NewBrainRelated(cfg config.Config, core *brain.Core) mcp.Tool {
	return &brainRelatedTool{brainBase{cfg: cfg, core: core}}
}

func (t *brainRelatedTool) Name() string { return "brain_related" }

func (t *brainRelatedTool) Description() string {
	return "Given a node id from brain_query_nodes, return what it is linked to. " +
		"Use it to follow a thread — the library a decision was about, the other places a convention " +
		"applies — without running a second search."
}

func (t *brainRelatedTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"node_id": {
				"type": "string",
				"description": "A node id, as returned by brain_query_nodes."
			},
			"limit": {
				"type": "integer",
				"minimum": 1,
				"maximum": 40,
				"description": "Maximum neighbours. Defaults to a compact number."
			}
		},
		"required": ["node_id"],
		"additionalProperties": false
	}`)
}

func (t *brainRelatedTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		NodeID string `json:"node_id"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.NodeID) == "" {
		return nil, errors.New("node_id is required")
	}

	node, err := t.core.Related(ctx, in.NodeID, in.Limit)
	if err != nil {
		return nil, err
	}
	return brainRelatedResponse{Node: node, Refined: true, budget: t.cfg.BrainSearchMaxTokens}, nil
}
