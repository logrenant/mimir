package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/mcp"
	"github.com/logrenant/goat-mcp/internal/search"
)

// SearchClient is an interface to allow mocking of search.Client
type SearchClient interface {
	Search(ctx context.Context, query string, count int) ([]search.Result, error)
}

// WebSearchTool executes a web search.
type WebSearchTool struct {
	client SearchClient
	cfg    config.Config
}

// NewWebSearch creates a new WebSearchTool.
func NewWebSearch(cfg config.Config, client SearchClient) *WebSearchTool {
	return &WebSearchTool{
		client: client,
		cfg:    cfg,
	}
}

// Name returns the tool's name.
func (t *WebSearchTool) Name() string {
	return "web_search"
}

// Description returns the tool's description.
func (t *WebSearchTool) Description() string {
	return "Search the web using DuckDuckGo. ≤ 30 results, metadata only, no page content."
}

// InputSchema returns the JSON schema for the tool's arguments.
func (t *WebSearchTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": { "type": "string", "minLength": 1 },
			"count": { "type": "integer", "minimum": 1, "maximum": 30 }
		},
		"required": ["query"],
		"additionalProperties": false
	}`)
}

type webSearchArgs struct {
	Query string `json:"query"`
	Count int    `json:"count"`
}

type webSearchResponse struct {
	Query   string          `json:"query"`
	Count   int             `json:"count"`
	Results []search.Result `json:"results"`

	budget int `json:"-"`
}

func (r webSearchResponse) MetadataOnly() bool {
	return true
}

func (r webSearchResponse) SizeBudgetTokens() int {
	return r.budget
}

// Handle executes the tool.
func (t *WebSearchTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var input webSearchArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	res, err := t.client.Search(ctx, input.Query, input.Count)
	if err != nil {
		if errors.Is(err, search.ErrSearchUnavailable) {
			return nil, errors.New("DuckDuckGo unavailable, retry shortly")
		}
		return nil, err
	}

	if res == nil {
		res = []search.Result{}
	}

	return webSearchResponse{
		Query:   input.Query,
		Count:   len(res),
		Results: res,
		budget:  t.cfg.WebSearchMaxTokens,
	}, nil
}

// Ensure WebSearchTool implements mcp.Tool.
var _ mcp.Tool = (*WebSearchTool)(nil)
