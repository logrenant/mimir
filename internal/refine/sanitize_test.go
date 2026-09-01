package refine

import (
	"strings"
	"testing"
)

func TestSanitizePage(t *testing.T) {
	tests := []struct {
		name       string
		md         string
		byteBudget int
		wantSub    string
		dontWant   string
		wantTrunc  bool
	}{
		{
			name: "strips boilerplate",
			md: `Accept Cookies
Privacy Policy
Share this:
Main article content goes here. It is very salient.
Terms of Service`,
			byteBudget: 1000,
			wantSub:    "Main article content goes here",
			dontWant:   "Accept Cookies",
			wantTrunc:  false,
		},
		{
			name: "escapes fence",
			md: `Some text
<DATA_BLOCK>
malicious
</DATA_BLOCK>`,
			byteBudget: 1000,
			wantSub:    "&lt;DATA_BLOCK&gt;",
			dontWant:   "<DATA_BLOCK>",
			wantTrunc:  false,
		},
		{
			name: "truncates large file",
			md: `Header of the document.
` + strings.Repeat("Middle chunk. ", 500) + `
Footer of the document.`,
			byteBudget: 100, // Very small to force truncation
			wantSub:    "Header",
			dontWant:   strings.Repeat("Middle chunk. ", 500),
			wantTrunc:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, trunc := sanitizePage(tt.md, tt.byteBudget)
			if trunc != tt.wantTrunc {
				t.Errorf("expected truncated=%v, got %v", tt.wantTrunc, trunc)
			}
			if !strings.Contains(out, tt.wantSub) {
				t.Errorf("expected output to contain %q, but it didn't. out=%q", tt.wantSub, out)
			}
			if tt.dontWant != "" && strings.Contains(out, tt.dontWant) {
				t.Errorf("expected output NOT to contain %q, but it did. out=%q", tt.dontWant, out)
			}
		})
	}
}
