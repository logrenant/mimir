package coderunner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/logrenant/mimir/internal/account"
	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/events"
	"github.com/logrenant/mimir/internal/project"
	"github.com/logrenant/mimir/internal/store"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// writeFakeClaude writes an executable stand-in for the `claude` CLI that
// emits body on stdout, then exits with exitCode. No tokens are spent
// anywhere in this package's tests.
func writeFakeClaude(t *testing.T, exitCode int, lines ...string) string {
	t.Helper()

	var script strings.Builder
	script.WriteString("#!/bin/sh\n")
	script.WriteString("cat >/dev/null\n") // consume the prompt on stdin
	for _, l := range lines {
		// Single-quote the payload, escaping any embedded single quote.
		script.WriteString("echo '" + strings.ReplaceAll(l, "'", `'\''`) + "'\n")
	}
	script.WriteString("exit " + strconv.Itoa(exitCode) + "\n")

	path := filepath.Join(t.TempDir(), "fake-claude.sh")
	if err := os.WriteFile(path, []byte(script.String()), 0o755); err != nil {
		t.Fatalf("writing fake claude: %v", err)
	}
	return path
}

// harness wires a Runner to a real store, a real registry, and a real project
// directory — only the `claude` binary is faked.
type harness struct {
	runner    *Runner
	bus       *events.Bus
	store     *store.Store
	accounts  *account.Registry
	projectID string
	cfg       config.Config
	cancel    context.CancelFunc
}

func newHarness(t *testing.T, claudePath string) *harness {
	t.Helper()
	return newHarnessWith(t, claudePath, nil)
}

// newHarnessWith is newHarness with a hook for the settings a test needs to
// differ on — the concurrency limit above all, since a queue only forms when
// there are fewer slots than runs.
func newHarnessWith(t *testing.T, claudePath string, tune func(*config.Config)) *harness {
	t.Helper()

	tmp := t.TempDir()
	cfg := config.Load()
	cfg.StorePath = filepath.Join(tmp, "mimir.db")
	cfg.TranscriptDir = filepath.Join(tmp, "transcripts")
	cfg.AttachmentDir = filepath.Join(tmp, "attachments")
	cfg.ClaudeCLIPath = claudePath
	cfg.ClaudeSessionDir = filepath.Join(tmp, "claude-session")
	cfg.CodingRunTimeout = 30 * time.Second
	if tune != nil {
		tune(&cfg)
	}

	st, err := store.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	workdir := filepath.Join(tmp, "myproject")
	if err := os.Mkdir(workdir, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	reg := project.NewRegistry(st)
	proj, err := reg.Register(context.Background(), workdir)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	base, cancel := context.WithCancel(context.Background())
	bus := events.NewBus()
	accounts := account.NewRegistry(st, cfg.ClaudeSessionDir, cfg.ClaudeCLIPath)
	// Connected, because that is the state a run happens in: the daemon
	// refuses a task outright when nothing is. The row is written through the
	// store rather than through the registry because connecting for real means
	// `claude auth login` and a browser.
	if err := st.InsertAccount(context.Background(), store.AccountRow{
		ID:        "acct-mimir",
		Label:     "Claude",
		ConfigDir: cfg.ClaudeSessionDir,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("InsertAccount: %v", err)
	}
	h := &harness{
		runner:    New(base, cfg, bus, reg, accounts, st),
		bus:       bus,
		store:     st,
		accounts:  accounts,
		projectID: proj.ID,
		cfg:       cfg,
		cancel:    cancel,
	}
	t.Cleanup(func() {
		h.runner.Wait()
		bus.Close()
		cancel()
	})
	return h
}

// collect subscribes before starting a run and drains until the channel closes.
func collect(t *testing.T, h *harness, prompt string) (Run, []events.Event) {
	t.Helper()

	// Subscribing needs the run id, which Start returns — so start first and
	// rely on the transcript for anything published before we attach. For
	// these tests the fake CLI is fast enough that we read the transcript
	// instead, which is the complete record by contract.
	run, err := h.runner.Start(context.Background(), CreateRequest{ProjectID: h.projectID, Prompt: prompt})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	h.runner.Wait()

	return run, readTranscript(t, h, run.ID)
}

func readTranscript(t *testing.T, h *harness, runID string) []events.Event {
	t.Helper()

	f, err := os.Open(filepath.Join(h.cfg.TranscriptDir, runID+".jsonl"))
	if err != nil {
		t.Fatalf("opening transcript: %v", err)
	}
	defer func() { _ = f.Close() }()

	var out []events.Event
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var ev events.Event
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			t.Fatalf("transcript line is not an Event: %v", err)
		}
		out = append(out, ev)
	}
	return out
}

const (
	lineInit   = `{"type":"system","subtype":"init","session_id":"sess-9","model":"claude-sonnet-5"}`
	lineText   = `{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"working"}]}}`
	lineResult = `{"is_error":false,"subtype":"success","type":"result","session_id":"sess-9","total_cost_usd":0.25,"num_turns":2,"duration_ms":1234,"result":"done"}`
)

func TestStart_HappyPathRecordsAndStreams(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineInit, lineText, lineResult))

	run, evs := collect(t, h, "add a test")

	if run.Status != store.RunStatusRunning {
		t.Errorf("Start should return a running run, got %q", run.Status)
	}

	var kinds []events.Kind
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
	}
	want := []events.Kind{events.KindRunStarted, events.KindTextDelta, events.KindRunCompleted}
	if len(kinds) != len(want) {
		t.Fatalf("transcript kinds: got %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("transcript kinds: got %v, want %v", kinds, want)
		}
	}

	final, err := h.runner.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if final.Status != store.RunStatusCompleted {
		t.Errorf("Status: got %q, want %q", final.Status, store.RunStatusCompleted)
	}
	if final.SessionID != "sess-9" || final.CostUSD != 0.25 || final.NumTurns != 2 {
		t.Errorf("result metadata not persisted: %+v", final)
	}
	if final.EndedAt.IsZero() {
		t.Error("EndedAt was not set")
	}
}

// A watcher attached to the bus sees the run live, and its channel closes when
// the run ends so a range over it terminates.
func TestStart_PublishesToTheBusAndClosesOnCompletion(t *testing.T) {
	// The fake stalls before its first line so the subscription is definitely
	// in place before anything is published — otherwise this test would race
	// the run and pass vacuously.
	claude := writeFakeClaude(t, 0, lineInit, lineText, lineResult)
	body, err := os.ReadFile(claude)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	stalled := strings.Replace(string(body), "cat >/dev/null\n", "cat >/dev/null\nsleep 1\n", 1)
	if err := os.WriteFile(claude, []byte(stalled), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	h := newHarness(t, claude)

	run, err := h.runner.Start(context.Background(), CreateRequest{ProjectID: h.projectID, Prompt: "do it"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	ch, cancel := h.bus.Subscribe(run.ID)
	defer cancel()

	var got []events.Kind
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range ch { // terminates because the runner calls CloseRun
			got = append(got, ev.Kind)
		}
	}()

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the bus channel never closed after the run ended")
	}

	want := []events.Kind{events.KindRunStarted, events.KindTextDelta, events.KindRunCompleted}
	if len(got) != len(want) {
		t.Fatalf("bus events: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("bus events: got %v, want %v", got, want)
		}
	}
}

func TestStart_FailingCLIProducesAFailedRun(t *testing.T) {
	// Exits non-zero having emitted no result line.
	h := newHarness(t, writeFakeClaude(t, 3, lineInit))

	run, evs := collect(t, h, "break it")

	last := evs[len(evs)-1]
	if last.Kind != events.KindRunFailed {
		t.Fatalf("want a terminal run.failed, got %v", last.Kind)
	}
	if last.Error == "" {
		t.Error("run.failed must carry an error message")
	}

	final, err := h.runner.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if final.Status != store.RunStatusFailed {
		t.Errorf("Status: got %q, want %q", final.Status, store.RunStatusFailed)
	}
	if final.Error == "" {
		t.Error("the failed row must record why")
	}
}

// The CLI's own error result is authoritative, even on a zero exit code.
func TestStart_CLIReportedErrorFailsTheRun(t *testing.T) {
	errResult := `{"is_error":true,"subtype":"error_during_execution","type":"result","session_id":"s"}`
	h := newHarness(t, writeFakeClaude(t, 0, lineInit, errResult))

	run, _ := collect(t, h, "fail cleanly")

	final, err := h.runner.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if final.Status != store.RunStatusFailed {
		t.Errorf("Status: got %q, want %q", final.Status, store.RunStatusFailed)
	}
}

// Exiting cleanly with no result line is a failure, not a silent success.
func TestStart_NoResultLineIsAFailure(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineInit, lineText))

	run, _ := collect(t, h, "vanish")

	final, err := h.runner.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if final.Status != store.RunStatusFailed {
		t.Errorf("a run with no result line must fail, got %q", final.Status)
	}
}

func TestStart_MissingCLIIsUnavailable(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "no-such-binary"))

	run, evs := collect(t, h, "nope")

	last := evs[len(evs)-1]
	if last.Kind != events.KindRunFailed {
		t.Fatalf("want run.failed, got %v", last.Kind)
	}
	if !strings.Contains(last.Error, "claude login") {
		t.Errorf("the error must name the fix, got %q", last.Error)
	}

	final, _ := h.runner.Get(context.Background(), run.ID)
	if final.Status != store.RunStatusFailed {
		t.Errorf("Status: got %q, want failed", final.Status)
	}
}

// A malformed line mid-stream must not kill an otherwise good run.
func TestStart_MalformedLineDoesNotKillTheRun(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineInit, "not json at all", lineText, lineResult))

	run, _ := collect(t, h, "resilience")

	final, err := h.runner.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if final.Status != store.RunStatusCompleted {
		t.Errorf("Status: got %q, want completed", final.Status)
	}
}

func TestStart_RejectsUnknownProject(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineResult))

	_, err := h.runner.Start(context.Background(), CreateRequest{ProjectID: "no-such-project", Prompt: "hello"})
	if !errors.Is(err, project.ErrProjectNotFound) {
		t.Fatalf("want ErrProjectNotFound, got %v", err)
	}
}

func TestStart_RejectsEmptyPrompt(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineResult))

	if _, err := h.runner.Start(context.Background(), CreateRequest{ProjectID: h.projectID, Prompt: "   "}); err == nil {
		t.Fatal("an empty prompt must be refused")
	}
}

// List is what the desktop app's board renders, so it must come back
// most-recent-first and scoped to the project asked for.
func TestList_ReturnsTheProjectsRunsMostRecentFirst(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineInit, lineText, lineResult))

	first, _ := collect(t, h, "first task")
	// started_at is second-granularity (internal/store's unixOrZero), so the
	// two runs need to land in different seconds for the order to be
	// unambiguous — exactly what a real board sees between two real runs.
	time.Sleep(1100 * time.Millisecond)
	second, _ := collect(t, h, "second task")

	runs, err := h.runner.List(context.Background(), h.projectID, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("runs: got %d, want 2", len(runs))
	}
	if runs[0].ID != second.ID || runs[1].ID != first.ID {
		t.Fatalf("order: got [%s, %s], want most-recent-first [%s, %s]",
			runs[0].ID, runs[1].ID, second.ID, first.ID)
	}
	if runs[0].Status != store.RunStatusCompleted {
		t.Errorf("Status: got %q, want %q", runs[0].Status, store.RunStatusCompleted)
	}
}

func TestList_UnknownProjectIsAnEmptyList(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineResult))

	runs, err := h.runner.List(context.Background(), "no-such-project", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("runs: got %d, want 0", len(runs))
	}
}

// Start returns before the run finishes — the whole point of streaming.
func TestStart_ReturnsBeforeTheRunCompletes(t *testing.T) {
	slow := writeFakeClaude(t, 0, lineInit)
	// Append a sleep so the process is still alive when Start returns.
	body, err := os.ReadFile(slow)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	patched := strings.Replace(string(body), "exit 0", "sleep 1\necho '"+lineResult+"'\nexit 0", 1)
	if err := os.WriteFile(slow, []byte(patched), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	h := newHarness(t, slow)

	start := time.Now()
	run, err := h.runner.Start(context.Background(), CreateRequest{ProjectID: h.projectID, Prompt: "take your time"})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if elapsed > 500*time.Millisecond {
		t.Errorf("Start blocked for %v — it must return immediately", elapsed)
	}
	if run.Status != store.RunStatusRunning {
		t.Errorf("Status: got %q, want running", run.Status)
	}

	h.runner.Wait()

	final, _ := h.runner.Get(context.Background(), run.ID)
	if final.Status != store.RunStatusCompleted {
		t.Errorf("after Wait, Status: got %q, want completed", final.Status)
	}
}

// Tool output can be an entire file; the event carries a preview and the
// transcript keeps what the CLI actually said.
func TestClampEvent_TruncatesHugeToolOutput(t *testing.T) {
	huge := strings.Repeat("x", maxToolOutput*2)
	got := clampEvent(events.Event{Kind: events.KindToolResult, Output: huge})

	if len(got.Output) >= len(huge) {
		t.Fatalf("output was not clamped: %d bytes", len(got.Output))
	}
	if !strings.Contains(got.Output, "truncated") {
		t.Error("a clamped output must say so")
	}
}
