package tools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/search"
	"github.com/logrenant/goat-mcp/internal/tools"
)

var webSearchTestCfg = config.Config{WebSearchMaxTokens: 1200}

type mockSearchClient struct {
	results []search.Result
	err     error
}

func (m *mockSearchClient) Search(ctx context.Context, query string, count int) ([]search.Result, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.results, nil
}



func TestWebSearch_Success(t *testing.T) {
	mockClient := &mockSearchClient{
		results: []search.Result{
			{Title: "Golang Context", URL: "https://pkg.go.dev/context", Snippet: "Package context defines the Context type"},
		},
	}
	tool := tools.NewWebSearch(webSearchTestCfg, mockClient)

	resAny, err := tool.Handle(context.Background(), json.RawMessage(`{"query":"golang context","count":5}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	bytes, err := json.Marshal(resAny)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}
	
	resStr := string(bytes)
	if !strings.Contains(resStr, `"golang context"`) {
		t.Errorf("missing query in response: %s", resStr)
	}
	if !strings.Contains(resStr, `"https://pkg.go.dev/context"`) {
		t.Errorf("missing url in response: %s", resStr)
	}
}

func TestWebSearch_Unavailable(t *testing.T) {
	mockClient := &mockSearchClient{
		err: search.ErrSearchUnavailable,
	}
	tool := tools.NewWebSearch(webSearchTestCfg, mockClient)

	_, err := tool.Handle(context.Background(), json.RawMessage(`{"query":"golang"}`))
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	if err.Error() != "DuckDuckGo unavailable, retry shortly" {
		t.Errorf("unexpected error message: %q", err.Error())
	}
}
