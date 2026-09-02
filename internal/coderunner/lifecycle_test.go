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

// One account, one task. Two runs sharing a Claude Code identity share its rate
// limit and its session state, so the second is contention, not throughput —
// and the whole point of registering a second account is that it lifts this.
func TestDispatch_RunsOneTaskPerAccount(t *testing.T) {
	counter := filepath.Join(t.TempDir(), "concurrent")
	h := newHarness(t, writeScriptedClaude(t,
		"cat >/dev/null\nprintf x >> "+counter+"\necho '"+lineInit+"'\nsleep 0.4\necho '"+lineResult+"'\n"))

	// Two accounts: the CLI's default slot and a second directory.
	first, err := h.accounts.Register(context.Background(), "first", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	second, err := h.accounts.Register(context.Background(), "second",
		filepath.Join(t.TempDir(), "slot-b"))
	if err != nil {
		t.Fatalf("Register: %v", err)
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
		perAccount := map[string]int{}
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
			perAccount[run.AccountID]++
			seen[run.AccountID] = true
		}
		for acct, n := range perAccount {
			if n > 1 {
				t.Fatalf("account %s is running %d tasks at once", acct, n)
			}
		}
		if running > 2 {
			t.Fatalf("%d runs in flight with only 2 accounts", running)
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
	// Both slots were used: automatic assignment is what makes the second
	// account worth registering.
	if !seen[first.ID] || !seen[second.ID] {
		t.Errorf("both accounts should have run something, saw %v", seen)
	}
}

// A run pinned to a busy account waits for that account, and does not hold up
// one behind it that can run somewhere else.
func TestDispatch_APinnedRunWaitsForItsOwnAccount(t *testing.T) {
	h := newHarness(t, writeScriptedClaude(t,
		"cat >/dev/null\necho '"+lineInit+"'\nsleep 30\n"))

	pinned, err := h.accounts.Register(context.Background(), "pinned", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	other, err := h.accounts.Register(context.Background(), "other",
		filepath.Join(t.TempDir(), "slot-b"))
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	blocking, err := h.runner.Start(context.Background(), CreateRequest{
		ProjectID: h.projectID, Prompt: "hold the pinned slot", AccountID: pinned.ID,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "the pinned account to be busy", func() bool {
		return statusOf(t, h, blocking.ID) == store.RunStatusRunning
	})

	waiting, err := h.runner.Start(context.Background(), CreateRequest{
		ProjectID: h.projectID, Prompt: "same account, must wait", AccountID: pinned.ID,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := statusOf(t, h, waiting.ID); got != store.RunStatusQueued {
		t.Errorf("a run pinned to a busy account: got %q, want queued", got)
	}

	// The other account is free, so an unpinned run behind it still starts.
	free, err := h.runner.Start(context.Background(), CreateRequest{
		ProjectID: h.projectID, Prompt: "any account will do",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "the unpinned run to take the free account", func() bool {
		return statusOf(t, h, free.ID) == store.RunStatusRunning
	})
	run, err := h.runner.Get(context.Background(), free.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if run.AccountID != other.ID {
		t.Errorf("the unpinned run should have taken the free slot, got %q", run.AccountID)
	}

	for _, id := range []string{blocking.ID, waiting.ID, free.ID} {
		_, _ = h.runner.Stop(context.Background(), id)
	}
	h.runner.Wait()
}

func TestStart_RefusesAPinToAnAccountThatDoesNotExist(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineResult))

	_, err := h.runner.Start(context.Background(), CreateRequest{
		ProjectID: h.projectID, Prompt: "go", AccountID: "no-such-account",
	})
	if !errors.Is(err, account.ErrAccountNotFound) {
		t.Fatalf("want ErrAccountNotFound, got %v", err)
	}
}

// The board showing work that is not happening was the reported bug; this is
// the fix, and the queue surviving the same restart is the other half of it.
func TestResume_FailsOrphanedRunningRowsAndKeepsTheQueue(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineInit, lineResult))

	orphan := store.RunRow{
		ID:        "orphaned-run",
		ProjectID: h.projectID,
		Prompt:    "interrupted",
		Status:    store.RunStatusRunning,
		CreatedAt: time.Now().UTC(),
		StartedAt: time.Now().UTC(),
	}
	if err := h.store.InsertRun(context.Background(), orphan); err != nil {
		t.Fatalf("InsertRun: %v", err)
	}
	queued := orphan
	queued.ID = "queued-run"
	queued.Status = store.RunStatusQueued
	queued.StartedAt = time.Time{}
	queued.QueuedAt = time.Now().UTC()
	queued.TranscriptPath = filepath.Join(h.cfg.TranscriptDir, "queued-run.jsonl")
	if err := h.store.InsertRun(context.Background(), queued); err != nil {
		t.Fatalf("InsertRun: %v", err)
	}

	if err := h.runner.Resume(context.Background()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	h.runner.Wait()

	got, err := h.runner.Get(context.Background(), orphan.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != store.RunStatusFailed {
		t.Errorf("orphan status: got %q, want failed", got.Status)
	}
	if !strings.Contains(got.Error, "restarted") {
		t.Errorf("orphan error should say why: %q", got.Error)
	}

	if got := statusOf(t, h, queued.ID); got != store.RunStatusCompleted {
		t.Errorf("queued run: got %q, want completed — the queue must survive a restart", got)
	}
}

// "not logged in" is written to stderr. A watcher that never sees it cannot
// tell a broken run from a thinking one.
func TestStderrIsPublishedAsEvents(t *testing.T) {
	h := newHarness(t, writeScriptedClaude(t,
		"cat >/dev/null\necho 'run `claude login` first' >&2\nexit 1\n"))

	run, err := h.runner.Start(context.Background(), CreateRequest{
		ProjectID: h.projectID, Prompt: "do it",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	h.runner.Wait()

	var (
		sawStderr bool
		lastSeq   int64
	)
	for _, ev := range readTranscript(t, h, run.ID) {
		if ev.Seq <= lastSeq {
			t.Fatalf("sequence went backwards: %d after %d", ev.Seq, lastSeq)
		}
		lastSeq = ev.Seq
		if ev.Kind == events.KindStderr && strings.Contains(ev.Text, "claude login") {
			sawStderr = true
		}
	}
	if !sawStderr {
		t.Error("the CLI's stderr never reached the event stream")
	}

	final, err := h.runner.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.Contains(final.Error, "claude login") {
		t.Errorf("the failure should carry the stderr tail: %q", final.Error)
	}
}

func TestDelete_RemovesTheRowAndItsTranscript(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineInit, lineResult))

	run, err := h.runner.Start(context.Background(), CreateRequest{
		ProjectID: h.projectID, Prompt: "do it",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	h.runner.Wait()

	transcript := filepath.Join(h.cfg.TranscriptDir, run.ID+".jsonl")
	if err := h.runner.Delete(context.Background(), run.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := h.runner.Get(context.Background(), run.ID); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("want ErrRunNotFound, got %v", err)
	}
	if _, err := os.Stat(transcript); err == nil {
		t.Error("the transcript outlived its run")
	}
}

func TestDelete_RefusesARunInFlight(t *testing.T) {
	h := newHarnessWith(t, writeScriptedClaude(t,
		"cat >/dev/null\necho '"+lineInit+"'\nsleep 30\n"),
		func(c *config.Config) { c.CodingStopGrace = 200 * time.Millisecond })

	run, err := h.runner.Start(context.Background(), CreateRequest{
		ProjectID: h.projectID, Prompt: "hold on",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "the run to be in flight", func() bool {
		return statusOf(t, h, run.ID) == store.RunStatusRunning
	})

	if err := h.runner.Delete(context.Background(), run.ID); !errors.Is(err, ErrNotDeletable) {
		t.Errorf("want ErrNotDeletable, got %v", err)
	}

	if _, err := h.runner.Stop(context.Background(), run.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	h.runner.Wait()
}

// Every run finishing as the daemon exits used to lose its result row, which is
// how the board filled with work that was not happening.
func TestPersist_WritesTheResultAfterBaseIsCancelled(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineInit, lineResult))

	run, err := h.runner.Start(context.Background(), CreateRequest{
		ProjectID: h.projectID, Prompt: "do it",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	h.runner.Wait()

	// Cancel the daemon's lifetime, then record a second outcome through the
	// same path a shutting-down run takes.
	h.cancel()

	row, _, err := h.store.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	row.Status = store.RunStatusStopped
	row.Error = "stopped by the operator"
	h.runner.persist(row)

	if got := statusOf(t, h, run.ID); got != store.RunStatusStopped {
		t.Errorf("Status: got %q, want stopped — the write must outlive base", got)
	}
}

func TestBuildPrompt_PutsAttachmentPathsAboveThePrompt(t *testing.T) {
	got := buildPrompt("what is wrong here?", []string{"/tmp/a.png", "/tmp/b.png"})

	if !strings.HasPrefix(got, "[attached files]\n/tmp/a.png\n/tmp/b.png\n") {
		t.Errorf("attachment block is wrong:\n%s", got)
	}
	if !strings.HasSuffix(got, "what is wrong here?") {
		t.Errorf("the prompt must come last:\n%s", got)
	}
	if plain := buildPrompt("hello", nil); plain != "hello" {
		t.Errorf("with no attachments the prompt is untouched, got %q", plain)
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

	if _, err := h.accounts.Register(context.Background(), "only", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}

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
