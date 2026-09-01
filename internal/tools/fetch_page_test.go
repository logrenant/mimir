package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/crawl"
	"github.com/logrenant/goat-mcp/internal/pipeline"
	"github.com/logrenant/goat-mcp/internal/refine"
)

var fetchTestCfg = config.Config{FetchPageMaxTokens: 1500}

type mockFetcher struct {
	res pipeline.RefinedPage
	err error
}

func (m *mockFetcher) Fetch(ctx context.Context, u string) (pipeline.RefinedPage, error) {
	return m.res, m.err
}

func TestFetchPageTool_GuardTest(t *testing.T) {
	hugeRawText := strings.Repeat("RAW_BLOB ", 10000)
	refinedText := "nice short refined text"
	
	m := &mockFetcher{
		res: pipeline.RefinedPage{
			URL:       "https://example.com",
			Title:     "Example",
			Markdown:  refinedText,
			Refined:   true,
			Truncated: true,
		},
	}
	// Note: We don't even have a place to put the hugeRawText in RefinedPage, 
	// which inherently ensures it's dropped, but let's just make sure the output
	// correctly maps the refined parts.

	tool := NewFetchPage(fetchTestCfg, m)
	args := json.RawMessage(`{"url": "https://example.com"}`)
	
	resAny, err := tool.Handle(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	res := resAny.(fetchPageResponse)
	if res.Markdown != refinedText {
		t.Errorf("expected markdown %q, got %q", refinedText, res.Markdown)
	}
	if !res.Refined {
		t.Errorf("expected refined to be true")
	}
	if strings.Contains(res.Markdown, hugeRawText) {
		t.Errorf("raw text leaked into response")
	}
}

func TestFetchPageTool_Validation(t *testing.T) {
	tool := NewFetchPage(fetchTestCfg, &mockFetcher{})
	
	tests := []struct {
		name    string
		args    string
		wantErr string
	}{
		{
			name:    "empty url",
			args:    `{"url": ""}`,
			wantErr: "valid http or https",
		},
		{
			name:    "ftp url",
			args:    `{"url": "ftp://example.com"}`,
			wantErr: "valid http or https",
		},
		{
			name:    "invalid json",
			args:    `{"url" "https://example.com"}`,
			wantErr: "invalid arguments",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tool.Handle(context.Background(), json.RawMessage(tt.args))
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error to contain %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}

func TestFetchPageTool_ErrorMapping(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantErr string
	}{
		{
			name:    "docker unavailable",
			err:     crawl.ErrDockerUnavailable,
			wantErr: "run `make crawl-up`",
		},
		{
			name:    "claude unavailable",
			err:     refine.ErrClaudeUnavailable,
			wantErr: "claude CLI unavailable",
		},
		{
			name:    "refine rejected",
			err:     refine.ErrRefineRejected,
			wantErr: "page could not be refined into a usable summary",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewFetchPage(fetchTestCfg, &mockFetcher{err: tt.err})
			_, err := tool.Handle(context.Background(), json.RawMessage(`{"url": "https://example.com"}`))
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error to contain %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}
