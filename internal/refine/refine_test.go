package refine

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/config"
)

var updateGolden = flag.Bool("update", false, "rewrite golden files")

// writeFakeClaude writes an executable shell script standing in for the
// `claude` CLI and returns its path. `--version` always succeeds unless
// versionFail is set; any other invocation runs `body`.
func writeFakeClaude(t *testing.T, versionFail bool, body string) string {
	t.Helper()

	versionBlock := "echo '2.0.0 (Claude Code)'\n  exit 0"
	if versionFail {
		versionBlock = "exit 1"
	}

	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--version\" ]; then\n  " + versionBlock + "\nfi\n" +
		"cat >/dev/null\n" + // consume stdin (the untrusted page content)
		body + "\n"

	path := filepath.Join(t.TempDir(), "fake-claude.sh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeFakeAgy writes an executable shell script standing in for the agy CLI.
// Its envelope is agy's, not claude's: a `status` and a `response`, and the
// `models` subcommand answering so Health can pass.
func writeFakeAgy(t *testing.T, body string) string {
	t.Helper()

	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"models\" ]; then\n  exit 0\nfi\n" +
		"cat >/dev/null\n" + // consume stdin (the untrusted content)
		body + "\n"

	path := filepath.Join(t.TempDir(), "fake-agy.sh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBuildPrompt_Golden(t *testing.T) {
	// The two shapes are separate goldens because they are separate prompts:
	// fetch_page distils a page with no query at all, and that branch must not
	// mention a query the refiner would then ask the caller to supply.
	cases := []struct {
		name   string
		golden string
		in     Input
	}{
		{
			name:   "with query",
			golden: "prompt.golden",
			in: Input{
				Query:        "test query",
				PageMarkdown: "test markdown content",
				SourceURL:    "https://example.com",
				MaxTokens:    1000,
			},
		},
		{
			name:   "no query",
			golden: "prompt_noquery.golden",
			in: Input{
				PageMarkdown: "test markdown content",
				SourceURL:    "https://example.com",
				MaxTokens:    1000,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			system, user := buildPrompt(tc.in)
			b, err := json.MarshalIndent(struct {
				System string `json:"system"`
				User   string `json:"user"`
			}{system, user}, "", "  ")
			if err != nil {
				t.Fatal(err)
			}

			goldenPath := filepath.Join("testdata", tc.golden)

			if *updateGolden {
				if err := os.WriteFile(goldenPath, b, 0o644); err != nil {
					t.Fatal(err)
				}
				t.Log("golden file rewritten")
			}

			expected, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden %s (run `go test -run TestBuildPrompt_Golden -update ./internal/refine` to regenerate): %v", goldenPath, err)
			}

			if string(b) != string(expected) {
				t.Fatalf("prompt output does not match golden file\nGot:\n%s\nExpected:\n%s", string(b), string(expected))
			}
		})
	}
}

// The no-query prompt exists to stop the refiner asking for a query it will
// never be given; if the word leaks back into that branch, this catches it.
func TestBuildPrompt_NoQueryPromptNeverMentionsAQuery(t *testing.T) {
	system, user := buildPrompt(Input{
		PageMarkdown: "test markdown content",
		SourceURL:    "https://example.com",
		MaxTokens:    1000,
	})

	for _, s := range []string{system, user} {
		if strings.Contains(strings.ToLower(s), "query") {
			t.Errorf("no-query prompt mentions a query: %q", s)
		}
	}
}

func TestClient_Health(t *testing.T) {
	cliPath := writeFakeClaude(t, false, "exit 0")

	cfg := config.Config{
		ClaudeCLIPath: cliPath,
		ClaudeModel:   "claude-haiku-4-5-20251001",
		RefineTimeout: 5 * time.Second,
	}
	client := New(cfg)

	ok, err := client.Health(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected CLI to be healthy")
	}
}

func TestClient_Health_CLIMissing(t *testing.T) {
	cfg := config.Config{
		ClaudeCLIPath: filepath.Join(t.TempDir(), "no-such-binary"),
		ClaudeModel:   "claude-haiku-4-5-20251001",
		RefineTimeout: 5 * time.Second,
	}
	client := New(cfg)

	ok, err := client.Health(context.Background())
	if !errors.Is(err, ErrClaudeUnavailable) {
		t.Fatalf("expected ErrClaudeUnavailable, got %v", err)
	}
	if ok {
		t.Fatal("expected healthy=false")
	}
}

func TestClient_Distil_Success(t *testing.T) {
	cliPath := writeFakeClaude(t, false, `echo '{"result": "refined content", "is_error": false, "subtype": "success"}'`)

	cfg := config.Config{
		ClaudeCLIPath: cliPath,
		ClaudeModel:   "claude-haiku-4-5-20251001",
		RefineTimeout: 5 * time.Second,
	}
	client := New(cfg)

	out, err := client.Distil(context.Background(), Input{MaxTokens: 100})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.Refined {
		t.Fatal("expected Refined to be true")
	}
	if out.Text != "refined content" {
		t.Fatalf("unexpected text: %q", out.Text)
	}
}

func TestClient_Distil_IsError(t *testing.T) {
	cliPath := writeFakeClaude(t, false, `echo '{"result": "", "is_error": true, "subtype": "error_max_turns"}'`)

	cfg := config.Config{
		ClaudeCLIPath: cliPath,
		ClaudeModel:   "claude-haiku-4-5-20251001",
		RefineTimeout: 5 * time.Second,
	}
	client := New(cfg)

	_, err := client.Distil(context.Background(), Input{MaxTokens: 100})
	if !errors.Is(err, ErrClaudeUnavailable) {
		t.Fatalf("expected ErrClaudeUnavailable, got %v", err)
	}
}

func TestClient_Distil_Retry(t *testing.T) {
	attemptFile := filepath.Join(t.TempDir(), "attempts")
	cliPath := writeFakeClaude(t, false, `
count=0
if [ -f "`+attemptFile+`" ]; then count=$(cat "`+attemptFile+`"); fi
count=$((count+1))
echo $count > "`+attemptFile+`"
if [ "$count" -eq 1 ]; then
  exit 1
fi
echo '{"result": "refined content", "is_error": false, "subtype": "success"}'`)

	cfg := config.Config{
		ClaudeCLIPath: cliPath,
		ClaudeModel:   "claude-haiku-4-5-20251001",
		RefineTimeout: 5 * time.Second,
	}
	client := New(cfg)

	out, err := client.Distil(context.Background(), Input{MaxTokens: 100})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Text != "refined content" {
		t.Fatalf("unexpected text after retry: %q", out.Text)
	}

	got, err := os.ReadFile(attemptFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "2\n" {
		t.Fatalf("expected 2 attempts, got %q", string(got))
	}
}

func TestClient_Distil_CLIMissing(t *testing.T) {
	cfg := config.Config{
		ClaudeCLIPath: filepath.Join(t.TempDir(), "no-such-binary"),
		ClaudeModel:   "claude-haiku-4-5-20251001",
		RefineTimeout: 5 * time.Second,
	}
	client := New(cfg)

	_, err := client.Distil(context.Background(), Input{MaxTokens: 100})
	if !errors.Is(err, ErrClaudeUnavailable) {
		t.Fatalf("expected ErrClaudeUnavailable, got %v", err)
	}
}

func TestClient_Distil_ContextCancel(t *testing.T) {
	cliPath := writeFakeClaude(t, false, "sleep 0.5")

	cfg := config.Config{
		ClaudeCLIPath: cliPath,
		ClaudeModel:   "claude-haiku-4-5-20251001",
		RefineTimeout: 5 * time.Second,
	}
	client := New(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.Distil(ctx, Input{MaxTokens: 100})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled error, got %v", err)
	}
}
