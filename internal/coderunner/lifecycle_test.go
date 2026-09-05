package coderunner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/account"
	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/events"
	"github.com/logrenant/mimir/internal/store"
)

// writeScriptedClaude writes a stand-in that can stall, write to stderr, and
// trap SIGINT — the three behaviours the lifecycle depends on and the plain
// fake cannot express. No tokens are spent here either.
func writeScriptedClaude(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "scripted-claude.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("writing scripted claude: %v", err)
	}
	return path
}

// waitFor polls until cond holds, so a test never sleeps for a fixed guess.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func statusOf(t *testing.T, h *harness, runID string) string {
	t.Helper()

	run, err := h.runner.Get(context.Background(), runID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return run.Status
}

// A created task is intent, not work: nothing may be spawned until it is
// released.
func TestCreate_RecordsABacklogTaskWithoutSpawning(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	claude := writeScriptedClaude(t,
		"cat >/dev/null\ntouch "+marker+"\necho '"+lineResult+"'\n")
	h := newHarness(t, claude)

	run, err := h.runner.Create(context.Background(), CreateRequest{
		ProjectID: h.projectID,
		Title:     "later",
		Prompt:    "do it eventually",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	h.runner.Wait()

	if run.Status != store.RunStatusBacklog {
		t.Errorf("Status: got %q, want %q", run.Status, store.RunStatusBacklog)
	}
	if run.Title != "later" {
		t.Errorf("Title: got %q, want %q", run.Title, "later")
	}
	if !run.StartedAt.IsZero() {
		t.Errorf("a backlog task has not started: StartedAt = %v", run.StartedAt)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("Create spawned the CLI")
	}
}

func TestEnqueue_RunsABacklogTask(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineInit, lineText, lineResult))

	run, err := h.runner.Create(context.Background(), CreateRequest{
		ProjectID: h.projectID, Prompt: "do it",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := h.runner.Enqueue(context.Background(), run.ID); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	h.runner.Wait()

	if got := statusOf(t, h, run.ID); got != store.RunStatusCompleted {
		t.Errorf("Status: got %q, want %q", got, store.RunStatusCompleted)
	}
}

// Two clicks on the same card must not become two runs.
func TestEnqueue_RefusesATaskThatIsNotInTheBacklog(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineInit, lineResult))

	run, err := h.runner.Start(context.Background(), CreateRequest{
		ProjectID: h.projectID, Prompt: "do it",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	h.runner.Wait()

	if _, err := h.runner.Enqueue(context.Background(), run.ID); err == nil {
		t.Fatal("enqueueing a finished run must be refused")
	}
}

func TestStop_RunningRunEndsStoppedAndPublishesRunStopped(t *testing.T) {
	// Ignores SIGINT so the WaitDelay kill is what ends it — the worst case,
	// and the one that must still produce a clean `stopped` outcome.
	h := newHarnessWith(t, writeScriptedClaude(t,
		"trap '' INT\ncat >/dev/null\necho '"+lineInit+"'\nsleep 30\n"),
		func(c *config.Config) { c.CodingStopGrace = 200 * time.Millisecond })

	run, err := h.runner.Start(context.Background(), CreateRequest{
		ProjectID: h.projectID, Prompt: "take your time",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for the CLI's first line, not merely for the row: stopping before
	// the process exists is a different path, and this test is about the one
	// where a live process has to be interrupted.
	waitFor(t, "the CLI to start talking", func() bool {
		f, err := os.ReadFile(filepath.Join(h.cfg.TranscriptDir, run.ID+".jsonl"))
		return err == nil && strings.Contains(string(f), string(events.KindRunStarted))
	})

	if _, err := h.runner.Stop(context.Background(), run.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	h.runner.Wait()

	if got := statusOf(t, h, run.ID); got != store.RunStatusStopped {
		t.Fatalf("Status: got %q, want %q", got, store.RunStatusStopped)
	}

	var sawStopped bool
	for _, ev := range readTranscript(t, h, run.ID) {
		if ev.Kind == events.KindRunStopped {
			sawStopped = true
		}
	}
	if !sawStopped {
		t.Error("no run.stopped event was written to the transcript")
	}
}

// A queued run never started, so cancelling it must not invent a failure.
func TestStop_QueuedRunReturnsToTheBacklog(t *testing.T) {
	h := newHarnessWith(t, writeScriptedClaude(t,
		"cat >/dev/null\necho '"+lineInit+"'\nsleep 30\n"),
		func(c *config.Config) { c.CodingStopGrace = 200 * time.Millisecond })

	// With no account registered there is exactly one slot, held by a run that
	// will not finish on its own.
	blocking, err := h.runner.Start(context.Background(), CreateRequest{
		ProjectID: h.projectID, Prompt: "hold the slot",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "the first run to take the slot", func() bool {
		return statusOf(t, h, blocking.ID) == store.RunStatusRunning
	})

	waiting, err := h.runner.Start(context.Background(), CreateRequest{
		ProjectID: h.projectID, Prompt: "wait your turn",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if waiting.Status != store.RunStatusQueued {
		t.Fatalf("second run: got %q, want %q", waiting.Status, store.RunStatusQueued)
	}

	if _, err := h.runner.Stop(context.Background(), waiting.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := statusOf(t, h, waiting.ID); got != store.RunStatusBacklog {
		t.Errorf("Status: got %q, want %q", got, store.RunStatusBacklog)
	}

	if _, err := h.runner.Stop(context.Background(), blocking.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	h.runner.Wait()
}

func TestStop_RefusesAFinishedRun(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineInit, lineResult))

	run, err := h.runner.Start(context.Background(), CreateRequest{
		ProjectID: h.projectID, Prompt: "do it",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	h.runner.Wait()

	if _, err := h.runner.Stop(context.Background(), run.ID); !errors.Is(err, ErrNotStoppable) {
		t.Fatalf("want ErrNotStoppable, got %v", err)
	}
}

// One account, one task at a time. Two runs sharing a Claude Code identity
// share its rate limit and its session state, so the second is contention, not
// throughput — and Mimir has exactly one account, so this is the whole of its
// capacity.
func TestDispatch_RunsOneTaskAtATime(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "concurrent")
	h := newHarness(t, writeScriptedClaude(t,
		"cat >/dev/null\nprintf x >> "+counter+"\necho '"+lineInit+"'\nsleep 0.4\necho '"+lineResult+"'\n"))

	connected, err := h.accounts.List(context.Background())
	if err != nil || len(connected) != 1 {
		t.Fatalf("List: got %+v, %v; want exactly one connected account", connected, err)
	}

	var ids []string
	for i := range 5 {
		run, err := h.runner.Start(context.Background(), CreateRequest{
			ProjectID: h.projectID, Prompt: "run " + strconv.Itoa(i),
		})
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		ids = append(ids, run.ID)
	}

	seen := map[string]bool{}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		running := 0
		for _, id := range ids {
			run, err := h.runner.Get(context.Background(), id)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if run.Status != store.RunStatusRunning {
				continue
			}
			running++
			seen[run.AccountID] = true
		}
		if running > 1 {
			t.Fatalf("%d runs in flight on one account", running)
		}
		if running == 0 && len(seen) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	h.runner.Wait()
	for _, id := range ids {
		run, err := h.runner.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if run.Status != store.RunStatusCompleted {
			t.Errorf("run %s: got %q, want completed — the queue must drain", id, run.Status)
		}
	}
	// Every run spent the connected account, and none reached the CLI's own
	// slot by way of the empty-string sentinel.
	if !seen[connected[0].ID] || len(seen) != 1 {
		t.Errorf("runs spent %v; want only %s", seen, connected[0].ID)
	}
}

// Nothing connected is the state every launch starts in, because closing Mimir
// signs its account out. A task created then would sit in a queue with no
// identity to spend, so it is refused while the operator is still looking at
// the form — and it must not quietly fall through to the CLI's own login,
// which is the terminal session Mimir does not touch.
func TestStart_RefusedWithNoAccountConnected(t *testing.T) {
	h := newHarness(t, writeScriptedClaude(t, "cat >/dev/null\necho '"+lineResult+"'\n"))

	if err := h.store.DeleteAllAccounts(context.Background()); err != nil {
		t.Fatalf("DeleteAllAccounts: %v", err)
	}

	_, err := h.runner.Start(context.Background(), CreateRequest{
		ProjectID: h.projectID, Prompt: "nothing to spend",
	})
	if !errors.Is(err, account.ErrNotConnected) {
		t.Fatalf("want ErrNotConnected, got %v", err)
	}

	runs, err := h.runner.List(context.Background(), h.projectID, 50)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("a refused task still left a row: %+v", runs)
	}
}

// A single slot must run one task, and the window this covers is inside one
// pump — so the queue is filled first and released with a single Resume.
//
// launch used to claim a run and only mark the slot busy from inside the
// goroutine it started. pump loops dispatchOne until no slot is free, so with
// five runs already queued the loop ran again before the child was scheduled,
// saw the same slot free, and claimed another run against the same identity.
// Enqueueing one at a time hides this: each Start pumps a queue of one.
func TestDispatch_OneSlotNeverRunsTwoAtOnce(t *testing.T) {
	h := newHarness(t, writeScriptedClaude(t,
		"cat >/dev/null\necho '"+lineInit+"'\nsleep 0.6\necho '"+lineResult+"'\n"))

	var ids []string
	for i := range 5 {
		id := "queued-" + strconv.Itoa(i)
		row := store.RunRow{
			ID:             id,
			ProjectID:      h.projectID,
			Prompt:         "run " + strconv.Itoa(i),
			Status:         store.RunStatusQueued,
			CreatedAt:      time.Now().UTC(),
			QueuedAt:       time.Now().UTC(),
			TranscriptPath: filepath.Join(h.cfg.TranscriptDir, id+".jsonl"),
		}
		if err := h.store.InsertRun(context.Background(), row); err != nil {
			t.Fatalf("InsertRun: %v", err)
		}
		ids = append(ids, id)
	}

	// One pump against a full queue: the loop gets every chance to see the
	// slot as free before the first child registers it.
	if err := h.runner.Resume(context.Background()); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	sawRunning := false
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		running := 0
		for _, id := range ids {
			run, err := h.runner.Get(context.Background(), id)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if run.Status == store.RunStatusRunning {
				running++
			}
		}
		if running > 1 {
			t.Fatalf("%d runs in flight on a single credential slot", running)
		}
		if running == 1 {
			sawRunning = true
		}
		if sawRunning && running == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	h.runner.Wait()
	if !sawRunning {
		t.Fatal("never observed a run in flight; the test proved nothing")
	}
}

// --- picking a run back up -------------------------------------------------

// writeResumingClaude writes a stand-in that records the prompt it was handed
// as well as its argv, and — unlike writeRecordingClaude — behaves differently
// on the second invocation. Both are what a retry has to be judged by: what
// actually reached the CLI, not what the row says.
//
// The first invocation fails after announcing a session; every one after it
// succeeds. That is the shape the feature exists for: a run cut off partway,
// then picked up.
func writeResumingClaude(t *testing.T, argsFile, stdinFile string) string {
	t.Helper()

	body := `
echo "$@" >> ` + strconv.Quote(argsFile) + `
cat >> ` + strconv.Quote(stdinFile) + `
echo '{"type":"system","subtype":"init","session_id":"sess-9","model":"claude-sonnet-5"}'
if [ ! -f ` + strconv.Quote(argsFile+".done") + ` ]; then
  touch ` + strconv.Quote(argsFile+".done") + `
  echo "upstream connection reset" >&2
  exit 1
fi
echo '{"is_error":false,"subtype":"success","type":"result","session_id":"sess-9","total_cost_usd":0.1,"num_turns":1,"result":"done"}'
exit 0
`
	return writeScriptedClaude(t, body)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

// The whole point of the feature: the second attempt is the same session, so
// the model still has what the first one did.
func TestRetry_ResumesTheSessionTheFirstAttemptLeft(t *testing.T) {
	tmp := t.TempDir()
	argsFile := filepath.Join(tmp, "argv")
	stdinFile := filepath.Join(tmp, "stdin")
	h := newHarness(t, writeResumingClaude(t, argsFile, stdinFile))

	run, err := h.runner.Start(context.Background(), CreateRequest{ProjectID: h.projectID, Prompt: "build the thing"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "the first attempt to fail", func() bool { return statusOf(t, h, run.ID) == store.RunStatusFailed })

	// A run that died after `init` still has a session, and that is the only
	// thing a retry can pick up.
	failed, err := h.runner.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if failed.SessionID != "sess-9" {
		t.Fatalf("a crash after init must keep the session, got %q", failed.SessionID)
	}

	if _, err := h.runner.Retry(context.Background(), run.ID, false); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	waitFor(t, "the retry to finish", func() bool { return statusOf(t, h, run.ID) == store.RunStatusCompleted })

	lines := readArgvLog(t, argsFile)
	if len(lines) != 2 {
		t.Fatalf("want two invocations, got %d: %v", len(lines), lines)
	}
	if strings.Contains(lines[0], "--resume") {
		t.Errorf("the first attempt has nothing to resume: %q", lines[0])
	}
	if !strings.Contains(lines[1], "--resume sess-9") {
		t.Errorf("the retry must resume the session, got %q", lines[1])
	}

	// And the resumed session is told what happened, plus the task again, so a
	// replayed transcript that ends mid-tool-call cannot leave it guessing.
	prompt := readFile(t, stdinFile)
	if !strings.Contains(prompt, "interrupted before it finished") {
		t.Errorf("the resumed session must be told it was cut off: %q", prompt)
	}
	if !strings.Contains(prompt, "connection reset") {
		t.Errorf("the resumed session must be told why: %q", prompt)
	}
	if strings.Count(prompt, "build the thing") != 2 {
		t.Errorf("both attempts must carry the task: %q", prompt)
	}
}

// The escape hatch: a session the CLI can no longer resume would otherwise
// fail on every retry, with the same card and the same reason.
func TestRetry_FreshStartsOverWithNoSession(t *testing.T) {
	tmp := t.TempDir()
	argsFile := filepath.Join(tmp, "argv")
	stdinFile := filepath.Join(tmp, "stdin")
	h := newHarness(t, writeResumingClaude(t, argsFile, stdinFile))

	run, err := h.runner.Start(context.Background(), CreateRequest{ProjectID: h.projectID, Prompt: "build the thing"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "the first attempt to fail", func() bool { return statusOf(t, h, run.ID) == store.RunStatusFailed })

	if _, err := h.runner.Retry(context.Background(), run.ID, true); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	waitFor(t, "the retry to finish", func() bool { return statusOf(t, h, run.ID) == store.RunStatusCompleted })

	lines := readArgvLog(t, argsFile)
	if len(lines) != 2 {
		t.Fatalf("want two invocations, got %d", len(lines))
	}
	if strings.Contains(lines[1], "--resume") {
		t.Errorf("a fresh retry must not resume anything, got %q", lines[1])
	}
	if strings.Contains(readFile(t, stdinFile), "interrupted before it finished") {
		t.Error("a fresh retry is not a continuation and must not be framed as one")
	}
}

// A retry is only meaningful where something stopped short. Everywhere else it
// is a conflict — the operator's view was a moment out of date.
func TestRetry_RefusesEveryStateWithNothingToPickUp(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineInit, lineResult))

	run, err := h.runner.Start(context.Background(), CreateRequest{ProjectID: h.projectID, Prompt: "go"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "the run to finish", func() bool { return statusOf(t, h, run.ID) == store.RunStatusCompleted })

	if _, err := h.runner.Retry(context.Background(), run.ID, false); !errors.Is(err, ErrNotRetryable) {
		t.Errorf("retrying a completed run: want ErrNotRetryable, got %v", err)
	}

	backlog, err := h.runner.Create(context.Background(), CreateRequest{ProjectID: h.projectID, Prompt: "later"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := h.runner.Retry(context.Background(), backlog.ID, false); !errors.Is(err, ErrNotRetryable) {
		t.Errorf("retrying a backlog card: want ErrNotRetryable, got %v", err)
	}
}

// The bug this covers: a retry with nothing connected moved the card to Queued
// and left it there for good, because the dispatcher has no slot to claim it
// with and nothing pumps on an account appearing. The refusal has to happen
// while the operator is still looking at the button they pressed.
func TestRetry_RefusedWithNoAccountConnected(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 1, lineInit))

	run, err := h.runner.Start(context.Background(), CreateRequest{ProjectID: h.projectID, Prompt: "go"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "the run to fail", func() bool { return statusOf(t, h, run.ID) == store.RunStatusFailed })

	if err := h.store.DeleteAllAccounts(context.Background()); err != nil {
		t.Fatalf("DeleteAllAccounts: %v", err)
	}

	if _, err := h.runner.Retry(context.Background(), run.ID, false); !errors.Is(err, account.ErrNotConnected) {
		t.Fatalf("want ErrNotConnected, got %v", err)
	}
	if got := statusOf(t, h, run.ID); got != store.RunStatusFailed {
		t.Errorf("a refused retry must leave the card where it was: got %q", got)
	}
}

// Enqueue is the same edge from the backlog side.
func TestEnqueue_RefusedWithNoAccountConnected(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineInit, lineResult))

	run, err := h.runner.Create(context.Background(), CreateRequest{ProjectID: h.projectID, Prompt: "later"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := h.store.DeleteAllAccounts(context.Background()); err != nil {
		t.Fatalf("DeleteAllAccounts: %v", err)
	}

	if _, err := h.runner.Enqueue(context.Background(), run.ID); !errors.Is(err, account.ErrNotConnected) {
		t.Fatalf("want ErrNotConnected, got %v", err)
	}
	if got := statusOf(t, h, run.ID); got != store.RunStatusBacklog {
		t.Errorf("a refused enqueue must leave the card in the backlog: got %q", got)
	}
}

// A queue that filled up while nothing was connected has to drain once
// something is: Kick is the edge nothing else covers, since pump is only
// called when work is released or a slot is freed.
func TestKick_DrainsAQueueLeftWaitingByASignedOutSlot(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 1, lineInit))

	run, err := h.runner.Start(context.Background(), CreateRequest{ProjectID: h.projectID, Prompt: "go"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "the run to fail", func() bool { return statusOf(t, h, run.ID) == store.RunStatusFailed })

	// The card goes back to the queue behind the daemon's back, which is the
	// state a requeue under a signed-out slot used to leave and the one a
	// restart leaves when the queue outlives the account.
	if err := h.store.DeleteAllAccounts(context.Background()); err != nil {
		t.Fatalf("DeleteAllAccounts: %v", err)
	}
	if moved, err := h.store.RequeueRun(context.Background(), run.ID, time.Now().UTC(), false); err != nil || !moved {
		t.Fatalf("RequeueRun: moved=%v err=%v", moved, err)
	}

	if err := h.runner.Kick(context.Background()); !errors.Is(err, account.ErrNotConnected) {
		t.Fatalf("a kick with nothing connected must say so, got %v", err)
	}
	if got := statusOf(t, h, run.ID); got != store.RunStatusQueued {
		t.Fatalf("the card must still be queued: got %q", got)
	}

	if err := h.store.InsertAccount(context.Background(), store.AccountRow{
		ID:        "acct-again",
		Label:     "Claude",
		ConfigDir: h.cfg.ClaudeSessionDir,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("InsertAccount: %v", err)
	}
	if err := h.runner.Kick(context.Background()); err != nil {
		t.Fatalf("Kick: %v", err)
	}
	// The queue moved: the card was claimed by the slot that just appeared.
	waitFor(t, "the queued card to be claimed", func() bool {
		got, err := h.runner.Get(context.Background(), run.ID)
		return err == nil && got.Status != store.RunStatusQueued && got.AccountID == "acct-again"
	})
}

// The account is signed out on quit and comes back with a new id, so a pin
// written before the last launch names a slot that is gone. Honouring it would
// strand the run in the queue for the life of the row.
func TestDispatch_APinToAVanishedSlotIsNotAPin(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineInit, lineResult))

	run, err := h.runner.Create(context.Background(), CreateRequest{
		ProjectID: h.projectID, Prompt: "go", AccountID: "acct-mimir",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Quit and launch again: the slot is signed out and forgotten, and the one
	// that comes back is a different row with a different id.
	if err := h.store.DeleteAllAccounts(context.Background()); err != nil {
		t.Fatalf("DeleteAllAccounts: %v", err)
	}
	if err := h.store.InsertAccount(context.Background(), store.AccountRow{
		ID:        "acct-after-restart",
		Label:     "Claude",
		ConfigDir: h.cfg.ClaudeSessionDir,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("InsertAccount: %v", err)
	}

	if _, err := h.runner.Enqueue(context.Background(), run.ID); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	waitFor(t, "the stale pin to be dispatched anyway", func() bool {
		return statusOf(t, h, run.ID) == store.RunStatusCompleted
	})
}

// A card is a card until something is spent on it: the operator can rewrite
// what it asks for, and attach the screenshot they forgot.
func TestEdit_RewritesACardAndTakesTheImageAddedAfterwards(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineInit, lineResult))
	ctx := context.Background()

	run, err := h.runner.Create(ctx, CreateRequest{
		ProjectID: h.projectID, Title: "ilk", Prompt: "eski istek",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	shot, err := h.runner.SaveAttachment("shot.png", pngBytes(t))
	if err != nil {
		t.Fatalf("SaveAttachment: %v", err)
	}

	title, prompt, ids := "ikinci", "yeni istek", []string{shot.ID}
	edited, err := h.runner.Edit(ctx, run.ID, EditRequest{
		Title: &title, Prompt: &prompt, AttachmentIDs: &ids,
	})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if edited.Title != "ikinci" || edited.Prompt != "yeni istek" {
		t.Errorf("the edit did not land: %+v", edited)
	}
	if len(edited.Attachments) != 1 || edited.Attachments[0] != shot.ID {
		t.Errorf("Attachments: got %v, want [%s]", edited.Attachments, shot.ID)
	}
	if edited.Status != store.RunStatusBacklog {
		t.Errorf("an edit must not move the card: got %q", edited.Status)
	}
}

// An untouched field stays untouched — a rename must not blank the prompt.
func TestEdit_LeavesWhatItWasNotAskedToChange(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineInit, lineResult))
	ctx := context.Background()

	run, err := h.runner.Create(ctx, CreateRequest{
		ProjectID: h.projectID, Title: "ilk", Prompt: "dokunulmasın",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	title := "sadece ad"
	edited, err := h.runner.Edit(ctx, run.ID, EditRequest{Title: &title})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if edited.Prompt != "dokunulmasın" {
		t.Errorf("Prompt: got %q", edited.Prompt)
	}
	if edited.Model != run.Model {
		t.Errorf("Model: got %q, want %q", edited.Model, run.Model)
	}
}

// An image dropped by an edit is an image nothing points at, and the sweep only
// runs at startup — so the edit itself has to reclaim it.
func TestEdit_ReclaimsAnImageItDropped(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineInit, lineResult))
	ctx := context.Background()

	dropped, err := h.runner.SaveAttachment("dropped.png", pngBytes(t))
	if err != nil {
		t.Fatalf("SaveAttachment: %v", err)
	}
	kept, err := h.runner.SaveAttachment("kept.png", pngBytes(t))
	if err != nil {
		t.Fatalf("SaveAttachment: %v", err)
	}

	run, err := h.runner.Create(ctx, CreateRequest{
		ProjectID: h.projectID, Prompt: "iki görsel",
		AttachmentIDs: []string{dropped.ID, kept.ID},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ids := []string{kept.ID}
	if _, err := h.runner.Edit(ctx, run.ID, EditRequest{AttachmentIDs: &ids}); err != nil {
		t.Fatalf("Edit: %v", err)
	}

	if _, _, err := h.runner.LoadAttachment(dropped.ID); !errors.Is(err, ErrAttachmentNotFound) {
		t.Errorf("the dropped image survived the edit: %v", err)
	}
	if _, _, err := h.runner.LoadAttachment(kept.ID); err != nil {
		t.Errorf("the kept image was reclaimed: %v", err)
	}
}

// The prompt of a run that is spending is the record of what was asked.
func TestEdit_RefusesARunInFlightAndOneThatFinished(t *testing.T) {
	h := newHarness(t, writeScriptedClaude(t,
		"cat >/dev/null\necho '"+lineInit+"'\nsleep 0.6\necho '"+lineResult+"'\n"))
	ctx := context.Background()

	run, err := h.runner.Start(ctx, CreateRequest{ProjectID: h.projectID, Prompt: "as asked"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "the run to start", func() bool { return statusOf(t, h, run.ID) == store.RunStatusRunning })

	rewritten := "rewritten"
	if _, err := h.runner.Edit(ctx, run.ID, EditRequest{Prompt: &rewritten}); !errors.Is(err, ErrNotEditable) {
		t.Fatalf("editing a running card: want ErrNotEditable, got %v", err)
	}

	waitFor(t, "the run to finish", func() bool { return statusOf(t, h, run.ID) == store.RunStatusCompleted })
	if _, err := h.runner.Edit(ctx, run.ID, EditRequest{Prompt: &rewritten}); !errors.Is(err, ErrNotEditable) {
		t.Fatalf("editing a completed card: want ErrNotEditable, got %v", err)
	}

	after, err := h.runner.Get(ctx, run.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if after.Prompt != "as asked" {
		t.Errorf("the prompt was rewritten under the run: %q", after.Prompt)
	}
}

// An id nothing was ever uploaded under would leave a card promising a picture
// the CLI is never handed.
func TestEdit_RefusesAnImageTheDaemonDoesNotHave(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineInit, lineResult))
	ctx := context.Background()

	run, err := h.runner.Create(ctx, CreateRequest{ProjectID: h.projectID, Prompt: "go"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	ids := []string{"deadbeef"}
	if _, err := h.runner.Edit(ctx, run.ID, EditRequest{AttachmentIDs: &ids}); !errors.Is(err, ErrAttachmentNotFound) {
		t.Fatalf("want ErrAttachmentNotFound, got %v", err)
	}
}

// A model that is not on the offer list would fail at dispatch time, three
// seconds into a run, instead of here in front of the operator.
func TestEdit_RefusesAModelThatIsNotOffered(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineInit, lineResult))
	ctx := context.Background()

	run, err := h.runner.Create(ctx, CreateRequest{ProjectID: h.projectID, Prompt: "go"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	model := "gpt-9"
	if _, err := h.runner.Edit(ctx, run.ID, EditRequest{Model: &model}); !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("want ErrUnknownModel, got %v", err)
	}
}
