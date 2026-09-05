package coderunner

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/events"
	"github.com/logrenant/mimir/internal/store"
)

// usageLimitResult is what the CLI writes when the account runs out mid-turn:
// a failed result whose text carries the reason and the unix time the window
// rolls over.
func usageLimitResult(resetsAt time.Time) string {
	return `{"type":"result","is_error":true,"subtype":"error_during_execution",` +
		`"session_id":"sess-9","num_turns":2,` +
		`"result":"Claude AI usage limit reached|` +
		strconv.FormatInt(resetsAt.Unix(), 10) + `"}`
}

func rejectedWindow(resetsAt time.Time) string {
	return `{"type":"rate_limit_event","rate_limit_info":{"status":"rejected",` +
		`"unifiedWindows":{"five_hour":{"utilization":1,"resetsAt":` +
		strconv.FormatInt(resetsAt.Unix(), 10) + `}}}}`
}

// A run the token budget cuts off is not a failure. It goes back in the queue
// with its session id, so the attempt that resumes continues the same work —
// which is the entire difference between this and letting the card fail.
func TestRun_SpentBudgetRequeuesInsteadOfFailing(t *testing.T) {
	resets := time.Now().Add(time.Hour).UTC()
	h := newHarness(t, writeFakeClaude(t, 1, lineInit, lineText, usageLimitResult(resets)))

	run, err := h.runner.Start(context.Background(), CreateRequest{
		ProjectID: h.projectID, Prompt: "add a test",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	h.runner.Wait()

	got, err := h.runner.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != store.RunStatusQueued {
		t.Fatalf("status: got %q, want %q", got.Status, store.RunStatusQueued)
	}
	// The session is what makes the resume a continuation rather than a second
	// attempt at the same task. It is learned from the CLI's first line and
	// the row has never seen it, so parking has to write it.
	if got.SessionID != "sess-9" {
		t.Errorf("session id not carried onto the parked row: %q", got.SessionID)
	}
	if got.Error == "" {
		t.Error("the parked card says nothing about why it is waiting")
	}

	// The pause is the run's last event, and it carries the time the work
	// resumes — which "failed" never could.
	evs := readTranscript(t, h, run.ID)
	last := evs[len(evs)-1]
	if last.Kind != events.KindRateLimit {
		t.Fatalf("last event: got %v, want %v", last.Kind, events.KindRateLimit)
	}
	if last.ResetsAt != resets.Unix() {
		t.Errorf("resets_at: got %d, want %d", last.ResetsAt, resets.Unix())
	}
}

// The CLI announces a spent budget two ways, and the one that is not a result
// line has to work too: a rejected window followed by a CLI that simply dies.
// The run never reported an outcome, so this is the path through `fail` — and
// it still parks, using the window's own reset time.
func TestRun_RejectedWindowParksEvenWithoutAResultLine(t *testing.T) {
	resets := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	h := newHarness(t, writeFakeClaude(t, 1, lineInit, rejectedWindow(resets)))

	run, err := h.runner.Start(context.Background(), CreateRequest{
		ProjectID: h.projectID, Prompt: "add a test",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	h.runner.Wait()

	got, err := h.runner.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != store.RunStatusQueued {
		t.Fatalf("status: got %q, want it parked in the queue", got.Status)
	}

	report, err := h.runner.Limits(context.Background(), 10)
	if err != nil {
		t.Fatalf("Limits: %v", err)
	}
	if len(report.Holds) != 1 || !report.Holds[0].ResetsAt.Equal(resets) {
		t.Fatalf("the window's own reset time was not used: %+v", report.Holds)
	}
}

// The whole point of the log: the moment is durable, so an operator asking the
// next morning what happened overnight has rows to read.
func TestRun_SpentBudgetIsLogged(t *testing.T) {
	resets := time.Now().Add(time.Hour).UTC()
	h := newHarness(t, writeFakeClaude(t, 1, lineInit, usageLimitResult(resets)))

	if _, err := h.runner.Start(context.Background(), CreateRequest{
		ProjectID: h.projectID, Prompt: "add a test",
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	h.runner.Wait()

	report, err := h.runner.Limits(context.Background(), 20)
	if err != nil {
		t.Fatalf("Limits: %v", err)
	}
	if len(report.Holds) != 1 {
		t.Fatalf("holds: got %d, want 1 (%+v)", len(report.Holds), report.Holds)
	}
	if !report.Holds[0].ResetsAt.Equal(resets.Truncate(time.Second)) {
		t.Errorf("hold resets at %v, want %v", report.Holds[0].ResetsAt, resets)
	}

	var phases []string
	for _, row := range report.Log {
		phases = append(phases, row.Phase)
	}
	if len(phases) == 0 || phases[len(phases)-1] != store.RateLimitPhaseRun {
		t.Fatalf("log phases: got %v, want the run pause first", phases)
	}
}

// The second half of what the operator asked for: while the budget is spent,
// nothing is claimed off the queue, and that refusal is written down too —
// once, not once per pump.
func TestQueue_HeldWhileTheBudgetIsSpent(t *testing.T) {
	resets := time.Now().Add(time.Hour).UTC()
	h := newHarness(t, writeFakeClaude(t, 1, lineInit, usageLimitResult(resets)))

	first, err := h.runner.Start(context.Background(), CreateRequest{
		ProjectID: h.projectID, Prompt: "the run that runs out",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	h.runner.Wait()

	second, err := h.runner.Start(context.Background(), CreateRequest{
		ProjectID: h.projectID, Prompt: "the one behind it",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	h.runner.Wait()

	// Neither ran again: the slot they both need has nothing left to spend.
	for _, id := range []string{first.ID, second.ID} {
		got, err := h.runner.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Status != store.RunStatusQueued {
			t.Errorf("run %s: got %q, want it waiting in the queue", id, got.Status)
		}
	}

	report, err := h.runner.Limits(context.Background(), 50)
	if err != nil {
		t.Fatalf("Limits: %v", err)
	}
	var dispatch int
	for _, row := range report.Log {
		if row.Phase == store.RateLimitPhaseDispatch {
			dispatch++
		}
	}
	if dispatch != 1 {
		t.Errorf("dispatch rows: got %d, want exactly 1 — one per pause, not one per pump", dispatch)
	}

	// And the operator asking why gets the reason rather than silence.
	if err := h.runner.Kick(context.Background()); err == nil {
		t.Error("Kick reported nothing wrong while the whole queue was held")
	}
}

// The promise the feature is named for: nobody presses anything. When the
// window rolls over the wake-up fires, the queue is pumped, and the parked run
// is claimed again.
//
// The pause is driven by CodingLimitRecheck rather than by a reset time in the
// CLI's message, and deliberately: a timestamp written into the fake's script
// is a wall-clock deadline the test then has to beat, and under -race it does
// not. The recheck interval is measured from the moment the run parks, so this
// waits on the mechanism instead of on the machine.
func TestQueue_RestartsItselfWhenTheBudgetResets(t *testing.T) {
	h := newHarnessWith(t, writeFakeClaude(t, 1, lineInit,
		`{"type":"result","is_error":true,"subtype":"error_during_execution",`+
			`"session_id":"sess-9","result":"Claude AI usage limit reached"}`),
		func(cfg *config.Config) { cfg.CodingLimitRecheck = 2 * time.Second })

	run, err := h.runner.Start(context.Background(), CreateRequest{
		ProjectID: h.projectID, Prompt: "add a test",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	h.runner.Wait()

	parked, err := h.runner.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if parked.Status != store.RunStatusQueued {
		t.Fatalf("the first attempt did not park: %q", parked.Status)
	}

	// Nothing is pressed from here on. The only thing that can move this run
	// is the wake-up armed when the budget ran out.
	deadline := time.Now().Add(30 * time.Second)
	for {
		again, err := h.runner.Get(context.Background(), run.ID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if again.StartedAt.After(parked.StartedAt) {
			break
		}
		if time.Now().After(deadline) {
			report, _ := h.runner.Limits(context.Background(), 50)
			t.Fatalf("the queue never restarted itself: %+v", report)
		}
		time.Sleep(50 * time.Millisecond)
	}
	h.runner.Wait()

	// And the restart is recorded, so the log reads as a closed cycle rather
	// than a pause nothing ever ended.
	report, err := h.runner.Limits(context.Background(), 50)
	if err != nil {
		t.Fatalf("Limits: %v", err)
	}
	var resumed bool
	for _, row := range report.Log {
		if row.Phase == store.RateLimitPhaseResumed {
			resumed = true
		}
	}
	if !resumed {
		t.Errorf("no `resumed` row for a queue that restarted: %+v", report.Log)
	}
}

// A restart inside a spent window must not spend a CLI invocation rediscovering
// what the log already recorded.
func TestResume_RestoresAHoldFromTheLog(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineInit, lineResult))

	resets := time.Now().Add(time.Hour).UTC()
	if err := h.store.InsertRateLimitEvent(context.Background(), store.RateLimitRow{
		At:        time.Now().UTC(),
		Phase:     store.RateLimitPhaseRun,
		AccountID: "acct-mimir",
		ResetsAt:  resets,
		Detail:    "usage limit reached",
	}); err != nil {
		t.Fatalf("InsertRateLimitEvent: %v", err)
	}

	if err := h.runner.Resume(context.Background()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	report, err := h.runner.Limits(context.Background(), 10)
	if err != nil {
		t.Fatalf("Limits: %v", err)
	}
	if len(report.Holds) != 1 || report.Holds[0].AccountID != "acct-mimir" {
		t.Fatalf("the hold was not rebuilt from the log: %+v", report.Holds)
	}
}

// A `resumed` row closes the pause. A restart after one must not rebuild it.
func TestResume_DoesNotRestoreAClosedHold(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineInit, lineResult))

	now := time.Now().UTC()
	rows := []store.RateLimitRow{
		{At: now.Add(-time.Hour), Phase: store.RateLimitPhaseRun,
			AccountID: "acct-mimir", ResetsAt: now.Add(time.Hour)},
		{At: now, Phase: store.RateLimitPhaseResumed, AccountID: "acct-mimir"},
	}
	for _, row := range rows {
		if err := h.store.InsertRateLimitEvent(context.Background(), row); err != nil {
			t.Fatalf("InsertRateLimitEvent: %v", err)
		}
	}

	if err := h.runner.Resume(context.Background()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	report, err := h.runner.Limits(context.Background(), 10)
	if err != nil {
		t.Fatalf("Limits: %v", err)
	}
	if len(report.Holds) != 0 {
		t.Fatalf("a closed pause was rebuilt: %+v", report.Holds)
	}
}

func TestBudgetSpent(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	reset := now.Add(2 * time.Hour)

	cases := []struct {
		name     string
		state    state
		detail   string
		want     time.Time
		spent    bool
		fallback bool
	}{
		{
			name:  "a rejected window carries its own reset time",
			state: state{rejected: true, resetsAt: reset.Unix()},
			want:  reset, spent: true,
		},
		{
			name:   "the CLI's sentence carries one too",
			state:  state{rejected: true},
			detail: "Claude AI usage limit reached|" + strconv.FormatInt(reset.Unix(), 10),
			want:   reset, spent: true,
		},
		{
			name:   "a spent budget with no reset time falls back",
			state:  state{},
			detail: "this account's quota is spent",
			spent:  true, fallback: true,
		},
		{
			name:   "an ordinary failure is not a pause",
			state:  state{},
			detail: "the claude CLI exited with an error: signal: killed",
		},
		{
			name:  "a stale reset time is not trusted",
			state: state{rejected: true, resetsAt: now.Add(-time.Hour).Unix()},
			spent: true, fallback: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := tc.state
			got, spent := st.budgetSpent(tc.detail, now, 15*time.Minute)
			if spent != tc.spent {
				t.Fatalf("spent: got %v, want %v", spent, tc.spent)
			}
			if !spent {
				return
			}
			want := tc.want
			if tc.fallback {
				want = now.Add(15 * time.Minute)
			}
			if !got.Equal(want) {
				t.Errorf("resets at: got %v, want %v", got, want)
			}
		})
	}
}
