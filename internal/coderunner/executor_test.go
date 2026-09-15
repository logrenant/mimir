package coderunner

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/events"

	"github.com/logrenant/mimir/internal/agents"
	"github.com/logrenant/mimir/internal/store"
)

// fakeWorker is a sub-agent that spends no credential slot and opens no
// folder — the shape the whole seam exists for. It runs in-process and
// finishes immediately, so a test can assert dispatch without a subprocess.
type fakeWorker struct {
	agent    string
	started  chan struct{}
	release  chan struct{}
	prepErr  error
	failWith error
}

func newFakeWorker(agent string) *fakeWorker {
	return &fakeWorker{
		agent:   agent,
		started: make(chan struct{}, 8),
		release: make(chan struct{}),
	}
}

func (f *fakeWorker) Agent() string { return f.agent }
func (f *fakeWorker) Lane() Lane    { return LaneWorker }

func (f *fakeWorker) Prepare(context.Context, store.RunRow) error { return f.prepErr }

func (f *fakeWorker) Execute(ctx context.Context, job Job) Outcome {
	f.started <- struct{}{}
	done := job.Out.Step("region_search", map[string]string{"region": "İstanbul"})
	done(true, "62 işletme")
	job.Out.Say("kategorize edildi")

	select {
	case <-f.release:
	case <-ctx.Done():
		return Outcome{Status: store.RunStatusFailed, Err: ctx.Err()}
	}

	if f.failWith != nil {
		return Outcome{Status: store.RunStatusFailed, Err: f.failWith}
	}
	return Outcome{Status: store.RunStatusCompleted, CostUSD: 0, NumTurns: 1}
}

// leadgenCard writes a card for the worker-lane agent straight through the
// runner, which is the path an operator's click takes.
func leadgenCard(t *testing.T, h *harness) Run {
	t.Helper()
	run, err := h.runner.Create(context.Background(), CreateRequest{
		Prompt: "İstanbul'da diş kliniği bul",
		Agent:  "leadgen",
		Params: `{"region":"İstanbul"}`,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return run
}

// TestCreate_TakesAWorkerCardWithNoProjectAtAll.
//
// Demanding a folder for a job that never opens one would make the operator
// register a directory to run a region search.
func TestCreate_TakesAWorkerCardWithNoProjectAtAll(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineResult))
	h.runner.Register(newFakeWorker("leadgen"))

	run := leadgenCard(t, h)
	if run.ProjectID != "" {
		t.Fatalf("project_id = %q, want empty", run.ProjectID)
	}
	if run.Agent != "leadgen" {
		t.Fatalf("agent = %q, want leadgen", run.Agent)
	}
}

// TestDispatch_StartsAWorkerCardWithNoAccountConnected is the single most
// important assertion in this task. "No free account" used to mean "nothing
// can start"; a worker card must not inherit that.
func TestDispatch_StartsAWorkerCardWithNoAccountConnected(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineResult))
	worker := newFakeWorker("leadgen")
	h.runner.Register(worker)

	// Sign every slot out. A claude card could not start now; this one must.
	if err := h.store.DeleteAllAccounts(context.Background()); err != nil {
		t.Fatalf("DeleteAllAccounts: %v", err)
	}

	run := leadgenCard(t, h)
	if _, err := h.runner.Enqueue(context.Background(), run.ID); err != nil {
		t.Fatalf("Enqueue must not demand a login the job never spends: %v", err)
	}

	select {
	case <-worker.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the worker card never started with no account connected")
	}
	close(worker.release)
	waitFor(t, "the card to complete", func() bool { return statusOf(t, h, run.ID) == "completed" })
	h.runner.Wait()
}

// TestDispatch_StartsAWorkerCardWhileTheOnlyAccountIsBusy.
//
// The account lane being saturated is the ordinary state of this daemon — one
// run per identity — so a worker card that waited for it would almost never
// run.
func TestDispatch_StartsAWorkerCardWhileTheOnlyAccountIsBusy(t *testing.T) {
	// A claude stand-in that hangs until the test lets it go.
	claude := writeScriptedClaude(t, "cat >/dev/null\nsleep 30\n")
	h := newHarness(t, claude)
	worker := newFakeWorker("leadgen")
	h.runner.Register(worker)

	coding, err := h.runner.Start(context.Background(), CreateRequest{
		ProjectID: h.projectID,
		Prompt:    "hold the account",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "the coding run to occupy the slot", func() bool {
		return statusOf(t, h, coding.ID) == "running"
	})

	run := leadgenCard(t, h)
	if _, err := h.runner.Enqueue(context.Background(), run.ID); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	select {
	case <-worker.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the worker card waited for a credential slot it does not spend")
	}
	close(worker.release)
	waitFor(t, "the worker card to complete", func() bool {
		return statusOf(t, h, run.ID) == "completed"
	})

	if _, err := h.runner.Stop(context.Background(), coding.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	h.runner.Wait()
}

// TestDispatch_StillPinsAClaudeCardToItsAccount — the account lane's rule did
// not move.
func TestDispatch_StillPinsAClaudeCardToItsAccount(t *testing.T) {
	claude := writeScriptedClaude(t, "cat >/dev/null\nsleep 30\n")
	h := newHarness(t, claude)

	first, err := h.runner.Start(context.Background(), CreateRequest{
		ProjectID: h.projectID, Prompt: "one",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "the first run to start", func() bool { return statusOf(t, h, first.ID) == "running" })

	second, err := h.runner.Start(context.Background(), CreateRequest{
		ProjectID: h.projectID, Prompt: "two",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// One run per identity: the second must wait, not share the slot.
	if got := statusOf(t, h, second.ID); got != "queued" {
		t.Fatalf("second card is %q, want queued behind the busy account", got)
	}

	if _, err := h.runner.Stop(context.Background(), first.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := h.runner.Stop(context.Background(), second.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	h.runner.Wait()
}

// TestDispatch_EndsACardWhoseAgentHasNoExecutor. Stepping over it would leave a
// row nothing can ever claim, picked up on every pump for ever.
func TestDispatch_EndsACardWhoseAgentHasNoExecutor(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineResult))
	// Deliberately no Register.

	run := leadgenCard(t, h)
	if _, err := h.runner.Enqueue(context.Background(), run.ID); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	waitFor(t, "the card to be refused", func() bool { return statusOf(t, h, run.ID) == "failed" })

	got, err := h.runner.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Error == "" {
		t.Fatal("the refusal reached the card with no reason on it")
	}
	h.runner.Wait()
}

// TestWorkerCard_EmitsItsStagesOnTheRunStream: a card on this lane has a
// terminal, it just shows stages instead of tool calls.
func TestWorkerCard_EmitsItsStagesOnTheRunStream(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineResult))
	worker := newFakeWorker("leadgen")
	h.runner.Register(worker)
	close(worker.release)

	run := leadgenCard(t, h)
	if _, err := h.runner.Enqueue(context.Background(), run.ID); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	waitFor(t, "the card to complete", func() bool { return statusOf(t, h, run.ID) == "completed" })
	h.runner.Wait()

	rc, err := h.runner.OpenTranscript(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("OpenTranscript: %v", err)
	}
	defer func() { _ = rc.Close() }()

	kinds := map[string]int{}
	dec := json.NewDecoder(rc)
	for {
		var ev events.Event
		if err := dec.Decode(&ev); err != nil {
			break
		}
		kinds[string(ev.Kind)]++
	}
	for _, want := range []string{"run.started", "tool.call", "tool.result", "text.delta", "run.completed"} {
		if kinds[want] == 0 {
			t.Errorf("the stream carried no %s (got %v)", want, kinds)
		}
	}
}

// TestPark_IsUnreachableFromTheWorkerLane. The park path is entered from an
// Outcome only the claude executor ever sets, so a spent token budget cannot
// hold a job that has no budget.
func TestPark_IsUnreachableFromTheWorkerLane(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineResult))
	worker := newFakeWorker("leadgen")
	worker.failWith = errors.New("Claude AI usage limit reached|9999999999")
	h.runner.Register(worker)
	close(worker.release)

	run := leadgenCard(t, h)
	if _, err := h.runner.Enqueue(context.Background(), run.ID); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	// Failed, not parked back into the queue: the text looks like a rate limit
	// and must not be read as one on a lane that spends no tokens.
	waitFor(t, "the card to fail", func() bool { return statusOf(t, h, run.ID) == "failed" })
	h.runner.Wait()
}

// TestLaneOf_ReadsTheRegistryNotTheExecutorMap: a row whose executor is missing
// is still known to want no account, which is what keeps its refusal a message
// on the card rather than a card stuck behind a credential it never needed.
func TestLaneOf_ReadsTheRegistryNotTheExecutorMap(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineResult))

	if got := h.runner.laneOf("leadgen"); got != LaneWorker {
		t.Fatalf("laneOf(leadgen) = %v with no executor registered, want LaneWorker", got)
	}
	if got := h.runner.laneOf(agents.Default); got != LaneAccount {
		t.Fatalf("laneOf(%s) = %v, want LaneAccount", agents.Default, got)
	}
}

// namingWorker answers for its own model, the way catalogjob does: the card's
// model is the daemon's provider model, and the row's column is not it.
type namingWorker struct {
	*fakeWorker
	model string
}

func (n namingWorker) ModelFor(store.RunRow) string { return n.model }

// TestWorkerCard_TakesAModelThatIsNotACodingModel.
//
// The coding allow-list is about `claude --model`, and a catalog pass never
// runs that binary. Holding a worker card to it forced `claude-sonnet-5` into
// the column, so every screen reported a model the card would not spend — and
// the board's model control edited a field nothing read.
func TestWorkerCard_TakesAModelThatIsNotACodingModel(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineResult))
	h.runner.Register(newFakeWorker("leadgen"))

	run, err := h.runner.Create(context.Background(), CreateRequest{
		Prompt: "İstanbul'da diş kliniği bul",
		Agent:  "leadgen",
		Params: `{"region":"İstanbul"}`,
		Model:  "qwen3:8b",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if run.Model != "qwen3:8b" {
		t.Fatalf("model = %q, want the daemon model the card will spend", run.Model)
	}

	// And the same on the way back in: an edit is a create's twin.
	model := "gemini-2.5-flash"
	edited, err := h.runner.Edit(context.Background(), run.ID, EditRequest{Model: &model})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if edited.Model != model {
		t.Fatalf("edited model = %q, want %q", edited.Model, model)
	}

	// A coding card is still held to the list — this is a lane rule, not a
	// removed check.
	if _, err := h.runner.Create(context.Background(), CreateRequest{
		ProjectID: h.projectID, Prompt: "kod", Model: "gpt-9-turbo",
	}); !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("coding card error = %v, want ErrUnknownModel", err)
	}
}

// TestWorkerCard_TheOpeningLineNamesTheModelThePassWillSpend.
//
// The terminal said `claude-sonnet-5` for a pass spending `qwen3:8b`, because
// the runner announced the row's column. An executor that knows better is
// asked.
func TestWorkerCard_TheOpeningLineNamesTheModelThePassWillSpend(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineResult))
	worker := namingWorker{fakeWorker: newFakeWorker("leadgen"), model: "qwen3:8b"}
	h.runner.Register(worker)
	close(worker.release)

	run := leadgenCard(t, h)
	if _, err := h.runner.Enqueue(context.Background(), run.ID); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	waitFor(t, "the card to complete", func() bool { return statusOf(t, h, run.ID) == "completed" })
	h.runner.Wait()

	rc, err := h.runner.OpenTranscript(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("OpenTranscript: %v", err)
	}
	defer func() { _ = rc.Close() }()

	var started events.Event
	dec := json.NewDecoder(rc)
	for {
		var ev events.Event
		if err := dec.Decode(&ev); err != nil {
			break
		}
		if ev.Kind == events.KindRunStarted {
			started = ev
			break
		}
	}
	if started.Model != "qwen3:8b" {
		t.Fatalf("run.started model = %q, want the model the pass spends", started.Model)
	}
}
