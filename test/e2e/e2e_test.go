package e2e_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/mcp"
)

func TestE2E(t *testing.T) {
	// Build the binary
	binPath := filepath.Join(t.TempDir(), "mimir-mcp")
	buildCmd := exec.Command("go", "build", "-o", binPath, "../../cmd/mimir-mcp")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build binary: %v\n%s", err, string(out))
	}

	// Mock Servers
	ddgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`
			<html>
			<div class="web-result">
				<div class="result__title">
					<a href="/url?uddg=https%3A%2F%2Fexample.com%2Fmock">Mock Title</a>
				</div>
				<div class="result__snippet">Mock snippet</div>
			</div>
			</html>
		`))
	}))
	defer ddgSrv.Close()

	crawlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Simulate a long response to test refining/ceiling
		longMarkdown := strings.Repeat("This is a long mock markdown content. ", 500)
		resp := map[string]interface{}{
			"url":      "https://example.com/mock",
			"success":  true,
			"markdown": longMarkdown,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer crawlSrv.Close()

	// Fake `claude` CLI: handles `--version` (health check) and `-p
	// --output-format json ...` (Distil) with a long refined text, to ensure
	// it's still clamped if needed.
	fakeClaudePath := filepath.Join(t.TempDir(), "fake-claude.sh")
	fakeClaudeScript := `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "2.0.0 (Claude Code)"
  exit 0
fi
cat >/dev/null
printf '{"result": "%s", "is_error": false, "subtype": "success"}' "` + strings.Repeat("Refined content. ", 200) + `"
`
	if err := os.WriteFile(fakeClaudePath, []byte(fakeClaudeScript), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binPath)
	// Only the test-URL/path overrides are honoured by config.Load() (SD-1).
	//
	// MIMIR_STORE_PATH is not optional here. Without it this test runs the real
	// binary against the operator's real store — the one holding their
	// registered projects, run history and project memory — and creates it if
	// it is absent. A test must not be able to touch that file at all.
	cmd.Env = append(os.Environ(),
		"MIMIR_DDG_HTML_URL="+ddgSrv.URL,
		"MIMIR_DDG_LITE_URL="+ddgSrv.URL,
		"MIMIR_CRAWL4AI_URL="+crawlSrv.URL,
		"MIMIR_CLAUDE_CLI_PATH="+fakeClaudePath,
		// The distil tier's primary provider is agy, and this spawns the real
		// binary. Without pointing it somewhere that does not exist, the test
		// would send its prompts to whatever agy is installed on the machine
		// and assert against that answer. Pointing it at a missing path makes
		// the provider unavailable, which is exactly the case the router falls
		// back to the fake claude for.
		"MIMIR_AGY_CLI_PATH="+filepath.Join(t.TempDir(), "no-agy-here"),
		"MIMIR_STORE_PATH="+filepath.Join(t.TempDir(), "mimir.db"),
	)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()

		// For debugging if something goes wrong
		if t.Failed() {
			t.Logf("Stderr output:\n%s", stderrBuf.String())
		}
	}()

	reader := bufio.NewReader(stdout)

	sendReq := func(req string) map[string]any {
		_, err := stdin.Write([]byte(req + "\n"))
		if err != nil {
			t.Fatalf("failed to write to stdin: %v", err)
		}

		// Read one line
		line, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatalf("failed to read from stdout: %v", err)
		}

		// It MUST be a valid JSON-RPC message
		var res map[string]any
		if err := json.Unmarshal(line, &res); err != nil {
			t.Fatalf("stdout contained invalid JSON (not a clean RPC response): %v, raw: %s", err, string(line))
		}
		return res
	}

	// 1. initialize
	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test-client","version":"1.0"}}}`
	initRes := sendReq(initReq)
	if id, ok := initRes["id"].(float64); !ok || id != 1 {
		t.Fatalf("expected init response id 1")
	}

	initNotif := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	_, _ = stdin.Write([]byte(initNotif + "\n"))

	// 2. tools/list
	toolsReq := `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`
	toolsRes := sendReq(toolsReq)
	result := toolsRes["result"].(map[string]any)
	toolsList := result["tools"].([]any)

	foundTools := make(map[string]bool)
	for _, toolItem := range toolsList {
		toolMap := toolItem.(map[string]any)
		foundTools[toolMap["name"].(string)] = true
	}

	expectedTools := []string{"web_search", "fetch_page", "research", "diagnostics"}
	for _, expected := range expectedTools {
		if !foundTools[expected] {
			t.Errorf("missing tool in tools/list: %s", expected)
		}
	}

	// 3. diagnostics
	diagReq := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"diagnostics","arguments":{}}}`
	diagRes := sendReq(diagReq)
	diagResult := diagRes["result"].(map[string]any)
	if diagResult["isError"] == true {
		t.Fatalf("expected diagnostics success")
	}
	diagContent := diagResult["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(diagContent, `"ok":true`) {
		t.Errorf("diagnostics reported failure: %s", diagContent)
	}
	// The versions block reflects the pinned constants. The binary version is
	// read from the constant rather than repeated as a literal: a release bumps
	// it, and a test that had to be edited alongside would just be edited wrong.
	for _, want := range []string{`"binary":"` + mcp.Version + `"`, `"mcp_sdk":"v1.7.0"`, `"crawl4ai_image":"unclecode/crawl4ai:0.8.9"`, `"claude_model":"claude-haiku-4-5-20251001"`} {
		if !strings.Contains(diagContent, want) {
			t.Errorf("diagnostics versions missing %s: %s", want, diagContent)
		}
	}

	// 4. web_search
	searchReq := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"web_search","arguments":{"query":"test"}}}`
	searchRes := sendReq(searchReq)
	searchResult := searchRes["result"].(map[string]any)
	if searchResult["isError"] == true {
		t.Fatalf("expected web_search success")
	}
	searchContent := searchResult["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(searchContent, "Mock Title") {
		t.Errorf("web_search did not contain mock title: %s", searchContent)
	}

	// 5. fetch_page
	fetchReq := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"fetch_page","arguments":{"url":"https://example.com/mock"}}}`
	fetchRes := sendReq(fetchReq)
	fetchResult := fetchRes["result"].(map[string]any)
	if fetchResult["isError"] == true {
		t.Fatalf("expected fetch_page success")
	}

	// We expect fetch_page to return a JSON string with metadata
	fetchContent := fetchResult["content"].([]any)[0].(map[string]any)["text"].(string)
	var fetchOut map[string]any
	if err := json.Unmarshal([]byte(fetchContent), &fetchOut); err != nil {
		t.Fatalf("fetch_page output was not JSON: %v", err)
	}
	if fetchOut["refined"] != true {
		t.Errorf("fetch_page was not refined")
	}
	// SD-7 ceiling: refined Markdown must fit ~1500 tokens (chars/4 estimate).
	if got := len(fetchContent) / 4; got > 1500 {
		t.Errorf("fetch_page response ~%d tokens exceeds 1500 ceiling", got)
	}

	// 6. research
	resReq := `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"research","arguments":{"query":"test"}}}`
	resRes := sendReq(resReq)
	resResult := resRes["result"].(map[string]any)
	if resResult["isError"] == true {
		t.Fatalf("expected research success")
	}
	resContent := resResult["content"].([]any)[0].(map[string]any)["text"].(string)
	var resOut map[string]any
	if err := json.Unmarshal([]byte(resContent), &resOut); err != nil {
		t.Fatalf("research output was not JSON: %v", err)
	}
	if resOut["summary"] == nil || resOut["key_points"] == nil {
		t.Errorf("research output missing summary or key_points")
	}
	// SD-7 ceiling: the whole brief must fit ~2000 tokens.
	if got := len(resContent) / 4; got > 2000 {
		t.Errorf("research response ~%d tokens exceeds 2000 ceiling", got)
	}
	// summary is capped at 5 sentences.
	if s, _ := resOut["summary"].(string); strings.Count(s, ".") > 5 {
		t.Errorf("research summary has more than 5 sentences: %q", s)
	}

	// 6b. research with out-of-range depth -> schema error, still a clean JSON
	// frame on stdout (tool-error path stdout-cleanliness).
	badDepthReq := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"research","arguments":{"query":"test","depth":99}}}`
	badDepthRes := sendReq(badDepthReq)
	badDepthResult := badDepthRes["result"].(map[string]any)
	if badDepthResult["isError"] != true {
		t.Errorf("expected schema error for depth:99, got %v", badDepthResult)
	}

	// 7. Verify no garbage on stdout (since we parsed JSON exactly for every line, any garbage would have failed)

	// Shutdown gracefully
	_ = stdin.Close()

	// Give process a moment to exit
	select {
	case err := <-func() chan error {
		c := make(chan error, 1)
		go func() { c <- cmd.Wait() }()
		return c
	}():
		if err != nil {
			// A clean exit after stdin closes might return an error if it doesn't handle EOF perfectly,
			// but our server.Run handles context cancellation. Stdin close might just EOF scanner.
			// Let's just log it.
			t.Logf("Process exit: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Logf("Process did not exit within 1s after closing stdin")
	}
}
