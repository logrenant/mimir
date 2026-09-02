package llm

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/config"
)

// writeFakeCLI writes an executable shell script standing in for a provider's
// CLI. It always consumes stdin, so a test cannot pass by accident when the
// content never reaches the subprocess.
func writeFakeCLI(t *testing.T, name, body string) string {
	t.Helper()
	script := "#!/bin/sh\ncat >/dev/null\n" + body + "\n"
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Load()
	cfg.StorePath = filepath.Join(t.TempDir(), "mimir.db")
	cfg.AgyPrintTimeout = 10 * time.Second
	cfg.RefineTimeout = 10 * time.Second
	return cfg
}

// --- agy ---------------------------------------------------------------------

func TestAgy_ReadsStructuredOutput(t *testing.T) {
	cfg := testConfig(t)
	cfg.AgyCLIPath = writeFakeCLI(t, "agy",
		`echo '{"status":"SUCCESS","response":"{\"tags\":[\"a\"],\"toolAction\":\"noise\"}","structured_output":{"tags":["a"]}}'`)

	got, err := NewAgy(cfg).Complete(context.Background(), Request{
		System: "sys", User: "content", Schema: json.RawMessage(`{"type":"object"}`),
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	// structured_output is preferred over response precisely because response
	// carries the CLI's own keys, which are not ours to hand on.
	if string(got.Structured) != `{"tags":["a"]}` {
		t.Errorf("Structured = %s", got.Structured)
	}
	if got.Provider != "agy" {
		t.Errorf("Provider = %q", got.Provider)
	}
}

func TestAgy_FallsBackToTheResponseTextWithNoSchema(t *testing.T) {
	cfg := testConfig(t)
	cfg.AgyCLIPath = writeFakeCLI(t, "agy", `echo '{"status":"SUCCESS","response":"plain answer"}'`)

	got, err := NewAgy(cfg).Complete(context.Background(), Request{User: "content"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got.Text != "plain answer" {
		t.Errorf("Text = %q", got.Text)
	}
	if len(got.Structured) != 0 {
		t.Errorf("Structured = %s, want empty with no schema", got.Structured)
	}
}

func TestAgy_NonSuccessStatusIsUnavailable(t *testing.T) {
	cfg := testConfig(t)
	cfg.AgyCLIPath = writeFakeCLI(t, "agy", `echo '{"status":"ERROR","response":""}'`)

	_, err := NewAgy(cfg).Complete(context.Background(), Request{User: "content"})
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("err = %v, want ErrProviderUnavailable", err)
	}
}

// The three mitigations that stand in for the flags agy does not have. If any
// of them stops being passed, the refiner subprocess quietly regains a tool
// surface, so they are asserted rather than trusted.
func TestAgy_IsolatesTheSubprocess(t *testing.T) {
	cfg := testConfig(t)
	out := filepath.Join(t.TempDir(), "argv.txt")
	cfg.AgyCLIPath = writeFakeCLI(t, "agy",
		`{ echo "ARGS:$*"; echo "NESTED:$MIMIR_NESTED"; echo "PWD:$PWD"; } > `+out+`
echo '{"status":"SUCCESS","response":"ok"}'`)

	if _, err := NewAgy(cfg).Complete(context.Background(), Request{User: "content"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)

	for _, want := range []string{"--sandbox", "--disable-slash-commands", "--input-format text"} {
		if !strings.Contains(got, want) {
			t.Errorf("argv is missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "NESTED:1") {
		t.Errorf("MIMIR_NESTED was not set:\n%s", got)
	}
	// Starting agy inside the repository would feed the repo's own AGENTS.md to
	// a subprocess whose entire input is untrusted scraped text.
	//
	// The comparison resolves symlinks because macOS reports the temp root as
	// /private/var while t.TempDir hands back /var.
	wantDir, err := filepath.EvalSymlinks(filepath.Join(filepath.Dir(cfg.StorePath), "scratch"))
	if err != nil {
		t.Fatalf("the scratch directory was not created: %v", err)
	}
	if !strings.Contains(got, "PWD:"+wantDir) {
		t.Errorf("working directory is not the scratch dir (want %s):\n%s", wantDir, got)
	}
}

func TestAgy_SchemaIsPassedInline(t *testing.T) {
	cfg := testConfig(t)
	out := filepath.Join(t.TempDir(), "argv.txt")
	cfg.AgyCLIPath = writeFakeCLI(t, "agy",
		`echo "$*" > `+out+`
echo '{"status":"SUCCESS","response":"ok"}'`)

	schema := json.RawMessage(`{"type":"object"}`)
	if _, err := NewAgy(cfg).Complete(context.Background(), Request{User: "c", Schema: schema}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	b, _ := os.ReadFile(out)
	if !strings.Contains(string(b), `--json-schema {"type":"object"}`) {
		t.Errorf("schema was not passed inline: %s", b)
	}
}

// --- claude ------------------------------------------------------------------

func TestClaude_ParsesTheResultField(t *testing.T) {
	cfg := testConfig(t)
	cfg.ClaudeCLIPath = writeFakeCLI(t, "claude", `echo '{"result":"distilled text","is_error":false}'`)

	got, err := NewClaude(cfg).Complete(context.Background(), Request{User: "content"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got.Text != "distilled text" {
		t.Errorf("Text = %q", got.Text)
	}
	if got.Provider != "claude" {
		t.Errorf("Provider = %q", got.Provider)
	}
}

func TestClaude_IsErrorIsUnavailable(t *testing.T) {
	cfg := testConfig(t)
	cfg.ClaudeCLIPath = writeFakeCLI(t, "claude", `echo '{"result":"","is_error":true,"subtype":"auth"}'`)

	_, err := NewClaude(cfg).Complete(context.Background(), Request{User: "content"})
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("err = %v, want ErrProviderUnavailable", err)
	}
}

// The subprocess must never be able to act on injected content.
func TestClaude_DeniesEveryBuiltInTool(t *testing.T) {
	cfg := testConfig(t)
	out := filepath.Join(t.TempDir(), "argv.txt")
	cfg.ClaudeCLIPath = writeFakeCLI(t, "claude",
		`echo "$*" > `+out+`
echo '{"result":"ok","is_error":false}'`)

	if _, err := NewClaude(cfg).Complete(context.Background(), Request{User: "c"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	b, _ := os.ReadFile(out)
	got := string(b)
	for _, want := range []string{"--restricted", "--strict-mcp-config", "--no-session-persistence", "--disallowedTools"} {
		if !strings.Contains(got, want) {
			t.Errorf("argv is missing %q:\n%s", want, got)
		}
	}
	for _, tool := range disallowedTools {
		if !strings.Contains(got, tool) {
			t.Errorf("%s is not in --disallowedTools:\n%s", tool, got)
		}
	}
}

// --- router ------------------------------------------------------------------

// The fallback mechanism, exercised against a config that asks for one. The
// shipped config does not — see TestRouter_DistillHasNoFallbackByDefault — but
// the machinery stays tested so that turning it back on is one word in config
// rather than a rewrite.
func TestRouter_FallsBackWhenThePrimaryIsUnavailable(t *testing.T) {
	cfg := testConfig(t)
	cfg.DistillFallback = "claude"
	cfg.AgyCLIPath = writeFakeCLI(t, "agy", `exit 3`)
	cfg.ClaudeCLIPath = writeFakeCLI(t, "claude", `echo '{"result":"from claude","is_error":false}'`)

	got, err := NewRouter(cfg).Complete(context.Background(), Distill, Request{User: "c"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got.Text != "from claude" {
		t.Errorf("Text = %q, want the fallback's answer", got.Text)
	}
}

func TestRouter_ReportsBothFailuresWhenTheFallbackAlsoFails(t *testing.T) {
	cfg := testConfig(t)
	cfg.DistillFallback = "claude"
	cfg.AgyCLIPath = writeFakeCLI(t, "agy", `exit 3`)
	cfg.ClaudeCLIPath = writeFakeCLI(t, "claude", `exit 4`)

	_, err := NewRouter(cfg).Complete(context.Background(), Distill, Request{User: "c"})
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("err = %v, want ErrProviderUnavailable", err)
	}
	if !strings.Contains(err.Error(), "fallback") {
		t.Errorf("the fallback's failure is not reported: %v", err)
	}
}

// The shipped configuration has no distil fallback (task-51). agy being signed
// out or out of quota must stop the distil tier, not move the work — and the
// bill — to claude behind the operator's back.
func TestRouter_DistillHasNoFallbackByDefault(t *testing.T) {
	cfg := testConfig(t)
	if cfg.DistillFallback != "" {
		t.Fatalf("DistillFallback = %q, want empty in the shipped config", cfg.DistillFallback)
	}
	cfg.AgyCLIPath = writeFakeCLI(t, "agy", `exit 3`)
	claudeCalls := filepath.Join(t.TempDir(), "called")
	cfg.ClaudeCLIPath = writeFakeCLI(t, "claude",
		`touch `+claudeCalls+`
echo '{"result":"x","is_error":false}'`)

	_, err := NewRouter(cfg).Complete(context.Background(), Distill, Request{User: "c"})
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("err = %v, want ErrProviderUnavailable", err)
	}
	if strings.Contains(err.Error(), "fallback") {
		t.Errorf("a fallback was attempted: %v", err)
	}
	if _, err := os.Stat(claudeCalls); err == nil {
		t.Error("claude ran for a distil call; the shipped config has no fallback")
	}
}

// A cancelled caller is not a reason to start a second subprocess on its behalf.
func TestRouter_DoesNotFallBackOnACancelledContext(t *testing.T) {
	cfg := testConfig(t)
	cfg.DistillFallback = "claude"
	cfg.AgyCLIPath = writeFakeCLI(t, "agy", `exit 3`)
	claudeCalls := filepath.Join(t.TempDir(), "called")
	cfg.ClaudeCLIPath = writeFakeCLI(t, "claude",
		`touch `+claudeCalls+`
echo '{"result":"x","is_error":false}'`)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := NewRouter(cfg).Complete(ctx, Distill, Request{User: "c"}); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := os.Stat(claudeCalls); err == nil {
		t.Error("the fallback ran for a caller that had already gone away")
	}
}

// Reason work must not silently land on the cheap tier.
func TestRouter_ReasonUsesClaudeAndHasNoFallback(t *testing.T) {
	cfg := testConfig(t)
	cfg.AgyCLIPath = writeFakeCLI(t, "agy", `echo '{"status":"SUCCESS","response":"from agy"}'`)
	cfg.ClaudeCLIPath = writeFakeCLI(t, "claude", `exit 5`)

	r := NewRouter(cfg)
	if p := r.Provider(Reason); p == nil || p.Name() != "claude" {
		t.Fatalf("reason provider = %v, want claude", p)
	}
	_, err := r.Complete(context.Background(), Reason, Request{User: "c"})
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("err = %v, want ErrProviderUnavailable", err)
	}
	if strings.Contains(err.Error(), "from agy") {
		t.Error("reason work fell through to the distil tier")
	}
}

func TestRouter_ProvidersListsEachOnce(t *testing.T) {
	got := NewRouter(testConfig(t)).Providers()
	if len(got) != 2 {
		t.Fatalf("Providers() = %d entries, want 2", len(got))
	}
	seen := map[string]bool{}
	for _, p := range got {
		if seen[p.Name()] {
			t.Errorf("%s listed twice", p.Name())
		}
		seen[p.Name()] = true
	}
}
