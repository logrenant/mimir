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

// writeFakeEchoCLI is writeFakeCLI's sibling for the one thing it cannot do:
// prove what went in on stdin. writeFakeCLI discards stdin by design so a
// fake's output is fixed; this one hands it back.
func writeFakeEchoCLI(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\ncat\n"), 0o755); err != nil {
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

// The daemon's own model calls are not coding runs, so nothing else decides
// which credential slot they spend. When the environment builder is set, the
// child gets exactly what it returns — and when it is not, the child inherits
// this process, which is what a binary with no account registry can honestly
// say.
func TestClaude_SpendsTheSlotItIsGiven(t *testing.T) {
	cfg := testConfig(t)
	out := filepath.Join(t.TempDir(), "slot.txt")
	cfg.ClaudeCLIPath = writeFakeCLI(t, "claude",
		`echo "[${CLAUDE_SECURESTORAGE_CONFIG_DIR-unset}]" > `+out+`
echo '{"result":"ok","is_error":false}'`)

	read := func(t *testing.T) string {
		t.Helper()
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		return strings.TrimSpace(string(b))
	}

	c := NewClaude(cfg)
	c.UseEnviron(func() []string { return append(os.Environ(), "CLAUDE_SECURESTORAGE_CONFIG_DIR=/slots/eziode") })
	if _, err := c.Complete(context.Background(), Request{User: "c"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got := read(t); got != "[/slots/eziode]" {
		t.Errorf("the chosen slot did not reach the child: %s", got)
	}

	// The default slot is the variable's absence, not an empty value: an empty
	// string hashes into a third, nameless keychain entry.
	c.UseEnviron(func() []string {
		kept := make([]string, 0)
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "CLAUDE_SECURESTORAGE_CONFIG_DIR=") {
				kept = append(kept, kv)
			}
		}
		return kept
	})
	if _, err := c.Complete(context.Background(), Request{User: "c"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got := read(t); got != "[unset]" {
		t.Errorf("the default slot must be the variable's absence, got %s", got)
	}
}

// The router carries the builder to the claude provider and nowhere else: agy
// is a different CLI with its own login.
func TestRouter_UseEnvironReachesClaude(t *testing.T) {
	cfg := testConfig(t)
	out := filepath.Join(t.TempDir(), "slot.txt")
	cfg.ClaudeCLIPath = writeFakeCLI(t, "claude",
		`echo "[${CLAUDE_SECURESTORAGE_CONFIG_DIR-unset}]" > `+out+`
echo '{"result":"ok","is_error":false}'`)

	router := NewRouter(cfg)
	router.UseEnviron(func() []string {
		return append(os.Environ(), "CLAUDE_SECURESTORAGE_CONFIG_DIR=/slots/eziode")
	})
	if _, err := router.Provider(Reason).Complete(context.Background(), Request{User: "c"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	b, _ := os.ReadFile(out)
	if got := strings.TrimSpace(string(b)); got != "[/slots/eziode]" {
		t.Errorf("the router did not carry the slot: %s", got)
	}

	// A nil router is a real state in the daemon's wiring; it must not panic.
	var nilRouter *Router
	nilRouter.UseEnviron(func() []string { return nil })
}

// --- router ------------------------------------------------------------------

// The fallback mechanism, exercised against a config that asks for one
// explicitly. The shipped config asks for the same thing — see
// TestRouter_DistillFallsBackToClaudeByDefault — and this one keeps the
// machinery covered independently of what that default happens to be.
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

// The shipped configuration has a distil fallback again, and task-51's
// objection is answered rather than overruled.
//
// What task-51 refused was a *silent* hand-off. Two things now stop it being
// silent: a run the operator routed by hand suppresses the fallback outright
// (TestRouter_SelectionSuppressesTheFallback), so a deliberate choice is never
// substituted; and for the unselected case — the resident sweep — the answer
// carries the provider that actually served it, which is what lets the
// supervisor say so in the scan console the first time it changes.
func TestRouter_DistillFallsBackToClaudeByDefault(t *testing.T) {
	cfg := testConfig(t)
	if cfg.DistillFallback != "claude" {
		t.Fatalf("DistillFallback = %q, want claude in the shipped config", cfg.DistillFallback)
	}
	cfg.AgyCLIPath = writeFakeCLI(t, "agy", `exit 3`)
	cfg.ClaudeCLIPath = writeFakeCLI(t, "claude", `echo '{"result":"from claude","is_error":false}'`)

	got, err := NewRouter(cfg).Complete(context.Background(), Distill, Request{User: "c"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got.Text != "from claude" {
		t.Errorf("Text = %q, want the fallback's answer", got.Text)
	}
	// Reported back rather than inferred: a console that printed the configured
	// provider would tell the operator agy is still spending when it is not.
	if got.Provider != "claude" {
		t.Errorf("Provider = %q, want the provider that actually answered", got.Provider)
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

// Each once — and *every* one.
//
// This asserted `want 2`, which was the bug written down as an expectation:
// `Providers()` walked the class map and the fallback, so the two providers
// that no class routes to were never listed and never probed. The intent of the
// test (no duplicates) was always right; the number was the defect.
func TestRouter_ProvidersListsEachOnce(t *testing.T) {
	got := NewRouter(testConfig(t)).Providers()
	if len(got) != 4 {
		t.Fatalf("Providers() = %d entries, want 4 (agy, claude, gemini, ollama)", len(got))
	}
	seen := map[string]bool{}
	for _, p := range got {
		if seen[p.Name()] {
			t.Errorf("%s listed twice", p.Name())
		}
		seen[p.Name()] = true
	}
}

// --- selection ---------------------------------------------------------------

func TestRouter_SelectionSendsTheWorkToTheNamedProvider(t *testing.T) {
	cfg := testConfig(t)
	// Distill routes to agy by default, so a selection naming claude is only
	// honoured if CompleteWith actually overrules the class.
	cfg.AgyCLIPath = writeFakeCLI(t, "agy", `echo '{"status":"SUCCESS","response":"from agy"}'`)
	cfg.ClaudeCLIPath = writeFakeCLI(t, "claude", `echo '{"result":"from claude","is_error":false}'`)

	got, err := NewRouter(cfg).CompleteWith(context.Background(), Distill,
		Selection{Provider: "claude"}, Request{User: "c"})
	if err != nil {
		t.Fatalf("CompleteWith: %v", err)
	}
	if got.Text != "from claude" {
		t.Errorf("Text = %q, want the selected provider's answer", got.Text)
	}
	if got.Provider != "claude" {
		t.Errorf("Provider = %q, want claude", got.Provider)
	}
}

func TestRouter_SelectionSendsTheChosenModelToTheCLI(t *testing.T) {
	cfg := testConfig(t)
	// The script echoes back the --model it was handed, which is the only
	// evidence that the override reached argv rather than being dropped.
	cfg.AgyCLIPath = writeFakeCLI(t, "agy", `
while [ $# -gt 0 ]; do
  if [ "$1" = "--model" ]; then printf '{"status":"SUCCESS","response":"%s"}' "$2"; exit 0; fi
  shift
done
echo '{"status":"SUCCESS","response":"no --model"}'`)

	got, err := NewRouter(cfg).CompleteWith(context.Background(), Distill,
		Selection{Provider: "agy", Model: "gemini-3.1-pro-high"}, Request{User: "c"})
	if err != nil {
		t.Fatalf("CompleteWith: %v", err)
	}
	if got.Text != "gemini-3.1-pro-high" {
		t.Errorf("the CLI was run with model %q, want the selected one", got.Text)
	}
	if got.Model != "gemini-3.1-pro-high" {
		t.Errorf("Model = %q, want the selected one reported back", got.Model)
	}
}

// A selection must not write through to the shared router: the daemon runs
// concurrent calls against one router, and a per-request model that mutated it
// would decide what somebody else's run spends.
func TestRouter_SelectionDoesNotChangeTheConfiguredModel(t *testing.T) {
	cfg := testConfig(t)
	cfg.AgyCLIPath = writeFakeCLI(t, "agy", `echo '{"status":"SUCCESS","response":"ok"}'`)

	router := NewRouter(cfg)
	if _, err := router.CompleteWith(context.Background(), Distill,
		Selection{Provider: "agy", Model: "gemini-3.1-pro-low"}, Request{User: "c"}); err != nil {
		t.Fatalf("CompleteWith: %v", err)
	}

	if got := router.Provider(Distill).Model(); got != cfg.DistillModel {
		t.Errorf("router still routes to model %q, want the configured %q", got, cfg.DistillModel)
	}
}

func TestRouter_SelectionRejectsAnUnknownProvider(t *testing.T) {
	cfg := testConfig(t)
	cfg.AgyCLIPath = writeFakeCLI(t, "agy", `echo '{"status":"SUCCESS","response":"ok"}'`)

	_, err := NewRouter(cfg).CompleteWith(context.Background(), Distill,
		Selection{Provider: "gpt"}, Request{User: "c"})
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("err = %v, want ErrProviderUnavailable", err)
	}
}

// A fallback is availability, and availability is the daemon's policy. Once an
// operator has named a provider, quietly running somewhere else spends a budget
// they did not choose.
func TestRouter_SelectionSuppressesTheFallback(t *testing.T) {
	cfg := testConfig(t)
	cfg.DistillFallback = "claude"
	cfg.AgyCLIPath = writeFakeCLI(t, "agy", `exit 3`)
	cfg.ClaudeCLIPath = writeFakeCLI(t, "claude", `echo '{"result":"from claude","is_error":false}'`)

	_, err := NewRouter(cfg).CompleteWith(context.Background(), Distill,
		Selection{Provider: "agy"}, Request{User: "c"})
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("err = %v, want the selected provider's own failure", err)
	}
	if strings.Contains(err.Error(), "fallback") {
		t.Errorf("err = %v, want no fallback attempt for a selected run", err)
	}
}

func TestSelection_KeyIsEmptyWhenNothingWasChosen(t *testing.T) {
	if key := (Selection{}).Key(); key != "" {
		t.Errorf("Key() = %q, want empty so an unselected run keeps its cache", key)
	}
	if key := (Selection{Provider: "agy", Model: "m"}).Key(); key != "agy/m" {
		t.Errorf("Key() = %q, want agy/m", key)
	}
}

// The Gemini pool running dry must not stop the distil tier while agy's second
// free pool is still full. The chain stays on agy and only changes the model,
// so nothing here can reach a paid login.
func TestRouter_ModelChainKeepsTheDistilTierOnAgy(t *testing.T) {
	cfg := testConfig(t)
	cfg.DistillModel = "gemini-3.8-flash-low"
	cfg.DistillModelChain = []string{"gpt-oss-120b-medium", "claude-sonnet-4-6"}
	// Fails for the Gemini model, answers for anything else — which is how the
	// two independent pools behave when the first is spent.
	cfg.AgyCLIPath = writeFakeCLI(t, "agy", `
case "$*" in
  *gemini*) exit 3 ;;
  *gpt-oss-120b-medium*) echo '{"status":"SUCCESS","response":"from gpt-oss"}' ;;
  *) exit 3 ;;
esac`)

	got, err := NewRouter(cfg).Complete(context.Background(), Distill, Request{User: "c"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got.Text != "from gpt-oss" {
		t.Errorf("Text = %q, want the chain's answer", got.Text)
	}
	if got.Provider != "agy" {
		t.Errorf("Provider = %q, want the work to have stayed on agy", got.Provider)
	}
}

// An operator who named a model gets that model or an error. Substituting one
// silently would spend a pool they did not choose.
func TestRouter_ModelChainIsSkippedForAnExplicitSelection(t *testing.T) {
	cfg := testConfig(t)
	cfg.DistillModelChain = []string{"gpt-oss-120b-medium"}
	cfg.AgyCLIPath = writeFakeCLI(t, "agy", `
case "$*" in
  *gpt-oss-120b-medium*) echo '{"status":"SUCCESS","response":"from gpt-oss"}' ;;
  *) exit 3 ;;
esac`)

	_, err := NewRouter(cfg).CompleteWith(context.Background(), Distill,
		Selection{Provider: "agy", Model: "gemini-3.8-flash-low"}, Request{User: "c"})
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("err = %v, want the selected model's failure", err)
	}
}

// The shipped chain is ordered cheapest first and must not reach for Opus:
// it is the most expensive model agy offers, and the chain exists to protect
// the reserve, not to spend it compressing one node.
func TestRouter_ShippedChainIsTokenSafe(t *testing.T) {
	cfg := testConfig(t)
	if len(cfg.DistillModelChain) == 0 {
		t.Fatal("DistillModelChain is empty; the second free pool is unreachable")
	}
	if cfg.DistillModelChain[0] != "gpt-oss-120b-medium" {
		t.Errorf("chain starts with %q, want the non-thinking model first",
			cfg.DistillModelChain[0])
	}
	for _, m := range cfg.DistillModelChain {
		if strings.Contains(m, "opus") {
			t.Errorf("chain reaches for %q, the most expensive model agy offers", m)
		}
	}
	if !strings.HasSuffix(cfg.DistillModel, "-low") {
		t.Errorf("DistillModel = %q, want the lowest effort tier", cfg.DistillModel)
	}
}
