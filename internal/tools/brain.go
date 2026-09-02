package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/logrenant/mimir/internal/brain"
	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/mcp"
)

var brainCore *brain.Core // Singleton

func initBrainCore(cfg config.Config) error {
	if brainCore != nil {
		return nil
	}
	storageDir := filepath.Join("data", "brain")
	core, err := brain.NewCore(cfg, storageDir)
	if err != nil {
		return err
	}
	brainCore = core
	return nil
}

type BrainIngestData struct {
	cfg config.Config
}

func NewBrainIngestData(cfg config.Config) mcp.Tool {
	return &BrainIngestData{cfg: cfg}
}

func (t *BrainIngestData) Name() string { return "brain_ingest_data" }
func (t *BrainIngestData) Description() string {
	return "Ingests raw text data (chat logs, research, notes) into the Mimir Nervous System. Creates a semantically linked Markdown node with a 1-paragraph Turkish assessment."
}
func (t *BrainIngestData) InputSchema() json.RawMessage {
	return []byte(`{
		"type": "object",
		"properties": {
			"source": { "type": "string", "description": "The source of the data (e.g. 'chat-session-1', 'research-go-mcp')" },
			"content": { "type": "string", "description": "The raw content to ingest" }
		},
		"required": ["source", "content"]
	}`)
}
func (t *BrainIngestData) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	if err := initBrainCore(t.cfg); err != nil {
		return nil, err
	}
	var input struct {
		Source  string `json:"source"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}
	node, err := brainCore.IngestData(ctx, input.Source, "chat_session", input.Content)
	if err != nil {
		return nil, err
	}
	return fmt.Sprintf("Successfully ingested node %s with tags %v", node.ID, node.Tags), nil
}

type BrainIngestGitHub struct {
	cfg config.Config
}

func NewBrainIngestGitHub(cfg config.Config) mcp.Tool {
	return &BrainIngestGitHub{cfg: cfg}
}

func (t *BrainIngestGitHub) Name() string { return "brain_ingest_github" }
func (t *BrainIngestGitHub) Description() string {
	return "Ingests a GitHub repository (README) into the Mimir Nervous System. Use GITHUB_TOKEN environment variable for private repos."
}
func (t *BrainIngestGitHub) InputSchema() json.RawMessage {
	return []byte(`{
		"type": "object",
		"properties": {
			"repo": { "type": "string", "description": "The repository URL or handle (e.g. 'logrenant/goat-remastered')" }
		},
		"required": ["repo"]
	}`)
}
func (t *BrainIngestGitHub) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	if err := initBrainCore(t.cfg); err != nil {
		return nil, err
	}
	var input struct {
		Repo string `json:"repo"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}
	node, err := brainCore.IngestGitHubRepo(ctx, input.Repo)
	if err != nil {
		return nil, err
	}
	return fmt.Sprintf("Successfully ingested GitHub repo node %s with tags %v", node.ID, node.Tags), nil
}

type BrainQueryNodes struct {
	cfg config.Config
}

func NewBrainQueryNodes(cfg config.Config) mcp.Tool {
	return &BrainQueryNodes{cfg: cfg}
}

func (t *BrainQueryNodes) Name() string { return "brain_query_nodes" }
func (t *BrainQueryNodes) Description() string {
	return "Queries the Mimir Nervous System for nodes matching a tag or keyword."
}
func (t *BrainQueryNodes) InputSchema() json.RawMessage {
	return []byte(`{
		"type": "object",
		"properties": {
			"query": { "type": "string", "description": "The tag or keyword to search for" }
		},
		"required": ["query"]
	}`)
}
func (t *BrainQueryNodes) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	if err := initBrainCore(t.cfg); err != nil {
		return nil, err
	}
	var input struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, err
	}
	nodes, err := brainCore.QueryNodes(ctx, input.Query)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return "No nodes found matching the query.", nil
	}
	var out string
	for _, n := range nodes {
		out += fmt.Sprintf("Node: %s\nType: %s\nTags: %v\nAssessment: %s\n\n", n.ID, n.Type, n.Tags, n.Assessment)
	}
	return out, nil
}
