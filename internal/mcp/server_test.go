package mcp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/config"
	mimirmcp "github.com/logrenant/mimir/internal/mcp"
	"github.com/logrenant/mimir/internal/search"
	"github.com/logrenant/mimir/internal/tools"
)

// mockTool is a simple tool for testing.
type mockTool struct{}

func (m mockTool) Name() string        { return "mock_tool" }
func (m mockTool) Description() string { return "Mock tool" }
func (m mockTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}

type mockResponse struct {
	Result string `json:"result"`
}

func (m mockResponse) MetadataOnly() bool    { return true }
func (m mockResponse) SizeBudgetTokens() int { return 1000 }

func (m mockTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	return mockResponse{Result: "ok"}, nil
}

type mockSearchClient struct{}

func (m *mockSearchClient) Search(ctx context.Context, query string, count int) ([]search.Result, error) {
	if query == "golang context" {
		return []search.Result{
			{Title: "Golang Context", URL: "https://pkg.go.dev/context", Snippet: "Package context defines the Context type"},
		}, nil
	}
	return nil, search.ErrSearchUnavailable
}

// mockPanicTool panics when Handle is called.
type mockPanicTool struct{}

func (m *mockPanicTool) Name() string        { return "panic_tool" }
func (m *mockPanicTool) Description() string { return "Panics" }
func (m *mockPanicTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}
func (m *mockPanicTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	panic("test panic")
}

func TestServerToolsE2E(t *testing.T) {
	oldStdin := os.Stdin
	oldStdout := os.Stdout
	defer func() {
		os.Stdin = oldStdin
		os.Stdout = oldStdout
	}()

	rClient, wServer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	rServer, wClient, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	os.Stdin = rServer
	os.Stdout = wServer

	cfg := config.Load()
	srv := mimirmcp.NewServer(cfg)

	err = srv.Registry().Register(mockTool{})
	if err != nil {
		t.Fatal(err)
	}
	err = srv.Registry().Register(&mockPanicTool{})
	if err != nil {
		t.Fatal(err)
	}
	err = srv.Registry().Register(tools.NewWebSearch(config.Load(), &mockSearchClient{}))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Run(ctx)
	}()

	sendReq := func(req string) map[string]any {
		_, err := wClient.Write([]byte(req + "\n"))
		if err != nil {
			t.Fatalf("failed to write request: %v", err)
		}

		reader := bufio.NewReader(rClient)
		resBytes, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatalf("failed to read response: %v", err)
		}

		var res map[string]any
		if err := json.Unmarshal(resBytes, &res); err != nil {
			t.Fatalf("failed to parse response: %v, raw: %s", err, string(resBytes))
		}
		return res
	}

	// 1. Initialize
	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test-client","version":"1.0"}}}`
	initRes := sendReq(initReq)
	if id, ok := initRes["id"].(float64); !ok || id != 1 {
		t.Fatalf("expected init response id 1, got %v", initRes["id"])
	}

	// 2. Initialized notification
	initNotif := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	_, _ = wClient.Write([]byte(initNotif + "\n"))

	// 3. tools/list (shows mock_tool, panic_tool, web_search)
	toolsReq := `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`
	toolsRes := sendReq(toolsReq)
	result := toolsRes["result"].(map[string]any)
	toolsList := result["tools"].([]any)
	if len(toolsList) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(toolsList))
	}

	// 4. tools/call mock_tool -> {"result":"ok"}
	callMock := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"mock_tool","arguments":{}}}`
	mockRes := sendReq(callMock)
	mockResult := mockRes["result"].(map[string]any)
	if mockResult["isError"] == true {
		t.Fatalf("expected mock_tool success, got error")
	}
	content := mockResult["content"].([]any)
	textObj := content[0].(map[string]any)
	if textObj["text"] != `{"result":"ok"}` {
		t.Fatalf("expected result:ok, got %v", textObj["text"])
	}

	// 5. Bad arguments against schema (yields MCP error)
	callBadArgs := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"mock_tool","arguments":{"invalid_extra":123}}}`
	badArgsRes := sendReq(callBadArgs)
	badArgsResult := badArgsRes["result"].(map[string]any)
	if badArgsResult["isError"] != true {
		t.Fatalf("expected error for bad arguments")
	}

	// 6. Handler panics (yields MCP error)
	callPanic := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"panic_tool","arguments":{}}}`
	panicRes := sendReq(callPanic)
	panicResult := panicRes["result"].(map[string]any)
	if panicResult["isError"] != true {
		t.Fatalf("expected error for panic handler")
	}

	// 7. Still alive after panic
	callMockAgain := `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"mock_tool","arguments":{}}}`
	mockAgainRes := sendReq(callMockAgain)
	mockAgainResult := mockAgainRes["result"].(map[string]any)
	if mockAgainResult["isError"] == true {
		t.Fatalf("expected mock_tool to still work after panic")
	}

	// 8. tools/call web_search -> success
	callWebSearch := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"web_search","arguments":{"query":"golang context","count":5}}}`
	wsRes := sendReq(callWebSearch)
	wsResult := wsRes["result"].(map[string]any)
	if wsResult["isError"] == true {
		t.Fatalf("expected web_search success, got error")
	}
	wsContent := wsResult["content"].([]any)
	wsTextObj := wsContent[0].(map[string]any)
	if !strings.Contains(wsTextObj["text"].(string), `"golang context"`) {
		t.Fatalf("expected web_search response to contain query")
	}

	// 9. tools/call web_search -> out of bounds count
	callWebSearchOOB := `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"web_search","arguments":{"query":"golang","count":100}}}`
	wsOOBRes := sendReq(callWebSearchOOB)
	wsOOBResult := wsOOBRes["result"].(map[string]any)
	if wsOOBResult["isError"] != true {
		t.Fatalf("expected web_search to error on out of bounds count")
	}

	// 10. tools/call web_search -> ErrSearchUnavailable
	callWebSearchUnavail := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"web_search","arguments":{"query":"fail"}}}`
	wsUnavailRes := sendReq(callWebSearchUnavail)
	wsUnavailResult := wsUnavailRes["result"].(map[string]any)
	if wsUnavailResult["isError"] != true {
		t.Fatalf("expected web_search to error on unavailable")
	}
	wsUnavailContent := wsUnavailResult["content"].([]any)
	wsUnavailTextObj := wsUnavailContent[0].(map[string]any)
	if !strings.Contains(wsUnavailTextObj["text"].(string), "DuckDuckGo unavailable") {
		t.Fatalf("expected actionable error message, got %v", wsUnavailTextObj["text"])
	}

	cancel()
	_ = wClient.Close()
	_ = rClient.Close()
	_ = wServer.Close()
	_ = rServer.Close()

	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Server did not shut down cleanly")
	}
}
