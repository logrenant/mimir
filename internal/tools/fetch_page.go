package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/crawl"
	"github.com/logrenant/goat-mcp/internal/mcp"
	"github.com/logrenant/goat-mcp/internal/pipeline"
	"github.com/logrenant/goat-mcp/internal/refine"
)

// Fetcher represents an interface to allow mocking of pipeline.Fetch
type Fetcher interface {
	Fetch(ctx context.Context, u string) (pipeline.RefinedPage, error)
}

// FetchPageTool executes a fetch and refine operation on a single URL.
type FetchPageTool struct {
	fetcher Fetcher
	cfg     config.Config
}

// NewFetchPage creates a new FetchPageTool.
func NewFetchPage(cfg config.Config, fetcher Fetcher) *FetchPageTool {
	return &FetchPageTool{
		fetcher: fetcher,
		cfg:     cfg,
	}
}

// Name returns the tool's name.
func (t *FetchPageTool) Name() string {
	return "fetch_page"
}

// Description returns the tool's description.
func (t *FetchPageTool) Description() string {
	return "Fetch a single web page. Returns refined Markdown, ≤ ~1500 tokens, never the raw page."
}

// InputSchema returns the JSON schema for the tool's arguments.
func (t *FetchPageTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"url": { "type": "string", "minLength": 1, "description": "The URL to fetch. Must start with http:// or https://" }
		},
		"required": ["url"],
		"additionalProperties": false
	}`)
}

type fetchPageArgs struct {
	URL string `json:"url"`
}

type fetchPageResponse struct {
	URL       string `json:"url"`
	Title     string `json:"title"`
	Refined   bool   `json:"refined"`
	Truncated bool   `json:"truncated"`
	Markdown  string `json:"markdown"`

	budget int `json:"-"`
}

func (r fetchPageResponse) IsRefined() bool {
	return r.Refined
}

func (r fetchPageResponse) SizeBudgetTokens() int {
	return r.budget
}

// Handle executes the tool.
func (t *FetchPageTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var input fetchPageArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	u, err := url.Parse(input.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("url must be a valid http or https URL")
	}

	page, err := t.fetcher.Fetch(ctx, input.URL)
	if err != nil {
		if errors.Is(err, crawl.ErrDockerUnavailable) {
			return nil, errors.New("crawl4ai not reachable — run `make crawl-up`")
		}
		if errors.Is(err, refine.ErrClaudeUnavailable) {
			return nil, errors.New("claude CLI unavailable — run `claude login` (or check it is on PATH)")
		}
		if errors.Is(err, refine.ErrRefineRejected) {
			return nil, errors.New("page could not be refined into a usable summary")
		}
		return nil, err
	}

	return fetchPageResponse{
		URL:       page.URL,
		Title:     page.Title,
		Refined:   page.Refined,
		Truncated: page.Truncated,
		Markdown:  page.Markdown,
		budget:    t.cfg.FetchPageMaxTokens,
	}, nil
}

// Ensure FetchPageTool implements mcp.Tool.
var _ mcp.Tool = (*FetchPageTool)(nil)
