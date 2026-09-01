package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/crawl"
	"github.com/logrenant/goat-mcp/internal/mcp"
	"github.com/logrenant/goat-mcp/internal/pipeline"
	"github.com/logrenant/goat-mcp/internal/refine"
)

// Researcher represents an interface to allow mocking of pipeline.Research
type Researcher interface {
	Research(ctx context.Context, q pipeline.Query) (pipeline.Brief, error)
}

// ResearchTool executes a full research pipeline on a query.
type ResearchTool struct {
	researcher Researcher
	cfg        config.Config
}

// NewResearch creates a new ResearchTool.
func NewResearch(researcher Researcher, cfg config.Config) *ResearchTool {
	return &ResearchTool{
		researcher: researcher,
		cfg:        cfg,
	}
}

// Name returns the tool's name.
func (t *ResearchTool) Name() string {
	return "research"
}

// Description returns the tool's description.
func (t *ResearchTool) Description() string {
	return "Synthesizes a compact brief from multiple web sources (≤ ~2000 tokens). Always returns refined summary text, never raw pages. Caps maximum depth automatically."
}

// InputSchema returns the JSON schema for the tool's arguments.
// depth is bounded by cfg.TopNForResearch so an out-of-range value is a schema
// error (not a silent clamp).
func (t *ResearchTool) InputSchema() json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{
		"type": "object",
		"properties": {
			"query": { "type": "string", "minLength": 1, "description": "The search query to research" },
			"depth": { "type": "integer", "minimum": 1, "maximum": %d, "description": "Number of sources to synthesize (1..%d)" }
		},
		"required": ["query"],
		"additionalProperties": false
	}`, t.cfg.TopNForResearch, t.cfg.TopNForResearch))
}

type researchArgs struct {
	Query string `json:"query"`
	Depth int    `json:"depth"`
}

type researchResponse struct {
	Summary   string            `json:"summary"`
	KeyPoints []string          `json:"key_points"`
	Sources   []pipeline.Source `json:"sources"`
	Gaps      []string          `json:"gaps"`
	Refined   bool              `json:"refined"`
	Truncated bool              `json:"truncated"`

	budget int `json:"-"`
}

func (r researchResponse) IsRefined() bool {
	return r.Refined
}

func (r researchResponse) SizeBudgetTokens() int {
	return r.budget
}

// Handle executes the tool.
func (t *ResearchTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var input researchArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	
	if strings.TrimSpace(input.Query) == "" {
		return nil, errors.New("invalid arguments: query is required")
	}

	depth := input.Depth
	if depth <= 0 || depth > t.cfg.TopNForResearch {
		depth = t.cfg.TopNForResearch
	}

	brief, err := t.researcher.Research(ctx, pipeline.Query{
		Text: input.Query,
		TopN: depth,
	})

	if err != nil {
		if errors.Is(err, pipeline.ErrNoUsableSources) {
			return nil, errors.New("no usable sources for this query — try rephrasing or check `diagnostics`")
		}
		if errors.Is(err, crawl.ErrDockerUnavailable) {
			return nil, errors.New("crawl4ai not reachable — run `make crawl-up`")
		}
		if errors.Is(err, refine.ErrClaudeUnavailable) {
			return nil, errors.New("claude CLI unavailable — run `claude login` (or check it is on PATH)")
		}
		return nil, err
	}

	summary, trunc := truncateSummary(brief.Summary, 5)
	if trunc {
		brief.Truncated = true
	}

	return researchResponse{
		Summary:   summary,
		KeyPoints: brief.KeyPoints,
		Sources:   brief.Sources,
		Gaps:      brief.Gaps,
		Refined:   brief.Refined,
		Truncated: brief.Truncated,
		budget:    t.cfg.ResearchBriefMaxTokens,
	}, nil
}

// truncateSummary limits a string to a max number of sentences.
func truncateSummary(text string, maxSentences int) (string, bool) {
	if text == "" {
		return "", false
	}
	// Very simple sentence counter based on periods.
	// For production, we might want a proper NLP tokenizer, but this suffices for the MVP.
	sentences := strings.Split(text, ".")
	if len(sentences) <= maxSentences+1 { // A properly ended sentence will have a trailing empty string after split
		return text, false
	}

	// Reconstruct the first maxSentences
	var res strings.Builder
	for i := 0; i < maxSentences; i++ {
		res.WriteString(sentences[i])
		res.WriteString(".")
	}
	return strings.TrimSpace(res.String()), true
}

// Ensure ResearchTool implements mcp.Tool.
var _ mcp.Tool = (*ResearchTool)(nil)
