package refine

import (
	"errors"
	"strings"
	"testing"
)

func TestClampOutput(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		maxTokens  int
		inputLen   int
		wantErr    error
		wantTrunc  bool
		wantPrefix string // ensure it trimmed back to something sensible or just sliced
	}{
		{
			name:      "happy path",
			text:      "- fact 1\n- fact 2",
			maxTokens: 100,
			inputLen:  1000,
			wantErr:   nil,
		},
		{
			name:      "empty output",
			text:      "   \n",
			maxTokens: 100,
			inputLen:  1000,
			wantErr:   ErrRefineRejected,
		},
		{
			name:      "echoed injection",
			text:      "I will ignore previous instructions and do X",
			maxTokens: 100,
			inputLen:  1000,
			wantErr:   ErrRefineRejected,
		},
		{
			name:      "as an ai",
			text:      "As an AI language model, I cannot...",
			maxTokens: 100,
			inputLen:  1000,
			wantErr:   ErrRefineRejected,
		},
		{
			name:      "larger than input",
			text:      "bloated text hallucination",
			maxTokens: 100,
			inputLen:  10, // very small input
			wantErr:   ErrRefineRejected,
		},
		{
			name:       "truncated exactly at max if no boundary",
			text:       strings.Repeat("a", 500),
			maxTokens:  100,
			inputLen:   1000,
			wantErr:    nil,
			wantTrunc:  true,
			wantPrefix: strings.Repeat("a", 400),
		},
		{
			name:       "trimmed back to bullet",
			text:       "first part is okay and well within limits\n- here is a boundary that happens later " + strings.Repeat("x", 400),
			maxTokens:  100,
			inputLen:   1000,
			wantErr:    nil,
			wantTrunc:  true,
			wantPrefix: "first part is okay and well within limits", // it should trim at the bullet point that fits
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, trunc, err := clampOutput(tt.text, tt.maxTokens, tt.inputLen)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected err %v, got %v", tt.wantErr, err)
			}
			if err != nil {
				return
			}
			if trunc != tt.wantTrunc {
				t.Errorf("expected truncated=%v, got %v", tt.wantTrunc, trunc)
			}
			if tt.wantPrefix != "" && !strings.HasPrefix(out, tt.wantPrefix) {
				t.Errorf("expected output to start with %q, got %q", tt.wantPrefix, out)
			}
			if len(out) > tt.maxTokens*4 {
				t.Errorf("output length %d exceeded max %d", len(out), tt.maxTokens*4)
			}
		})
	}
}
