package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/pipeline"
)

type mockResearcher struct {
	brief pipeline.Brief
	err   error
}

func (m *mockResearcher) Research(ctx context.Context, q pipeline.Query) (pipeline.Brief, error) {
	return m.brief, m.err
}

func TestResearchTool_LimitsAndTruncation(t *testing.T) {
	cfg := config.Config{TopNForResearch: 5, ResearchBriefMaxTokens: 2000}

	verboseSummary := "This is sentence one. This is sentence two. This is sentence three. This is sentence four. This is sentence five. This is sentence six. This is sentence seven."
	
	m := &mockResearcher{
		brief: pipeline.Brief{
			Summary:   verboseSummary,
			KeyPoints: []string{"kp1"},
			Sources:   []pipeline.Source{{N: 1, Title: "s1", URL: "u1"}},
			Gaps:      []string{"g1"},
			Refined:   true,
			Truncated: false,
		},
	}

	tool := NewResearch(m, cfg)
	// depth:100 out of range is rejected at the schema layer over MCP (see
	// TestResearchTool_DepthSchema); Handle is called directly here, so the
	// in-handler clamp to TopNForResearch is what applies.
	args := json.RawMessage(`{"query": "test query", "depth": 100}`)

	resAny, err := tool.Handle(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	res := resAny.(researchResponse)

	// SD-7: the choke-point budget is the config ceiling, not a literal.
	if res.SizeBudgetTokens() != cfg.ResearchBriefMaxTokens {
		t.Errorf("expected size budget %d, got %d", cfg.ResearchBriefMaxTokens, res.SizeBudgetTokens())
	}

	// Summary should be truncated to 5 sentences
	if strings.Contains(res.Summary, "sentence six") {
		t.Errorf("summary was not truncated to 5 sentences: %q", res.Summary)
	}
	
	expectedSummary := "This is sentence one. This is sentence two. This is sentence three. This is sentence four. This is sentence five."
	if res.Summary != expectedSummary {
		t.Errorf("expected summary %q, got %q", expectedSummary, res.Summary)
	}

	if !res.Truncated {
		t.Errorf("expected truncated to be true")
	}

	if !res.Refined {
		t.Errorf("expected refined to be true")
	}
}

func TestResearchTool_ErrorMapping(t *testing.T) {
	cfg := config.Config{TopNForResearch: 5, ResearchBriefMaxTokens: 2000}

	tests := []struct {
		name    string
		err     error
		wantErr string
	}{
		{
			name:    "no usable sources",
			err:     pipeline.ErrNoUsableSources,
			wantErr: "no usable sources for this query",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewResearch(&mockResearcher{err: tt.err}, cfg)
			_, err := tool.Handle(context.Background(), json.RawMessage(`{"query": "test"}`))
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error to contain %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}

func TestResearchTool_DepthSchema(t *testing.T) {
	cfg := config.Config{TopNForResearch: 5}
	tool := NewResearch(&mockResearcher{}, cfg)

	var schema struct {
		Properties struct {
			Depth struct {
				Minimum int `json:"minimum"`
				Maximum int `json:"maximum"`
			} `json:"depth"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.InputSchema(), &schema); err != nil {
		t.Fatalf("input schema is not valid JSON: %v", err)
	}
	if schema.Properties.Depth.Maximum != cfg.TopNForResearch {
		t.Errorf("expected depth maximum %d, got %d", cfg.TopNForResearch, schema.Properties.Depth.Maximum)
	}
	if schema.Properties.Depth.Minimum != 1 {
		t.Errorf("expected depth minimum 1, got %d", schema.Properties.Depth.Minimum)
	}
}

func TestResearchTool_Validation(t *testing.T) {
	cfg := config.Config{TopNForResearch: 5, ResearchBriefMaxTokens: 2000}
	tool := NewResearch(&mockResearcher{}, cfg)

	// Missing required query
	args := json.RawMessage(`{"depth": 3}`)
	_, err := tool.Handle(context.Background(), args)
	if err == nil {
		t.Fatalf("expected error for missing query, got nil")
	}
}
