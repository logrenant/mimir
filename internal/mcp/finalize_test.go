package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// fakeGateTool is a minimal Tool used to exercise finalizeResponse directly.
type fakeGateTool struct{ name string }

func (f fakeGateTool) Name() string                 { return f.name }
func (f fakeGateTool) Description() string          { return "" }
func (f fakeGateTool) InputSchema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (f fakeGateTool) Handle(context.Context, json.RawMessage) (any, error) {
	return nil, nil
}

type mockRefined struct {
	Markdown string `json:"markdown"`
	Refined  bool   `json:"refined"`
}

func (m mockRefined) IsRefined() bool {
	return m.Refined
}

func (m mockRefined) SizeBudgetTokens() int {
	return 4000
}

type mockMetadata struct {
	Results string `json:"results"`
}

func (m mockMetadata) MetadataOnly() bool {
	return true
}

func (m mockMetadata) SizeBudgetTokens() int {
	return 2000
}

type unmarkedResponse struct {
	Data string `json:"data"`
}

func TestFinalizeResponse(t *testing.T) {
	tests := []struct {
		name    string
		v       any
		wantErr error
	}{
		{
			name:    "unmarked response",
			v:       unmarkedResponse{Data: "hello"},
			wantErr: ErrIsolationViolation,
		},
		{
			name:    "refined with oversized payload",
			v:       mockRefined{Refined: true, Markdown: strings.Repeat("a", 20000)}, // 20k chars / 4 = 5k tokens > 4000
			wantErr: ErrResponseTooLarge,
		},
		{
			name:    "metadata oversized",
			v:       mockMetadata{Results: strings.Repeat("b", 10000)}, // 10k chars / 4 = 2.5k tokens > 2000
			wantErr: ErrResponseTooLarge,
		},
		{
			name:    "metadata valid",
			v:       mockMetadata{Results: "short data"},
			wantErr: nil,
		},
		{
			name:    "refined unflagged",
			v:       mockRefined{Refined: false, Markdown: "hello"},
			wantErr: ErrIsolationViolation,
		},
		{
			name:    "refined compact valid",
			v:       mockRefined{Refined: true, Markdown: "short valid summary"},
			wantErr: nil,
		},
		{
			name:    "raw html signature",
			v:       mockRefined{Refined: true, Markdown: "<html><body>raw</body></html>"},
			wantErr: ErrIsolationViolation,
		},
		{
			name:    "raw script signature",
			v:       mockRefined{Refined: true, Markdown: "content <script>alert(1)</script>"},
			wantErr: ErrIsolationViolation,
		},
		{
			name:    "base64 image signature",
			v:       mockRefined{Refined: true, Markdown: "img src=\"data:image/png;base64,iVBOR\""},
			wantErr: ErrIsolationViolation,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := finalizeResponse(fakeGateTool{name: "test_tool"}, tt.v)
			if tt.wantErr == nil {
				if err != nil {
					t.Errorf("finalizeResponse() unexpected error = %v", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("finalizeResponse() error = %v, want wrap of %v", err, tt.wantErr)
			}
		})
	}
}
