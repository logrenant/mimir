package coderunner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/store"
)

// parkingWorker is a worker-lane executor that asks to be paused rather than
// failed — the case task-89 exists to make reachable.
type parkingWorker struct {
	agent    string
	attempts chan struct{}
	until    time.Time
	// parkOnce makes the second attempt succeed, so a test can watch the queue
	// resume by itself and see the work finish.
	parkOnce bool
	tried    int
}

func newParkingWorker(agent string, until time.Time) *parkingWorker {
	return &parkingWorker{agent: agent, attempts: make(chan struct{}, 8), until: until}
}

func (p *parkingWorker) Agent() string                               { return p.agent }
func (p *parkingWorker) Lane() Lane                                  { return LaneWorker }
func (p *parkingWorker) Prepare(context.Context, store.RunRow) error { return nil }

func (p *parkingWorker) Execute(_ context.Context, job Job) Outcome {
	p.tried++
	p.attempts <- struct{}{}
	job.Out.Say("yarısına kadar yazıldı")
	if p.parkOnce && p.tried > 1 {
		return Outcome{Status: store.RunStatusCompleted}
	}
	return Outcome{
		Status:    store.RunStatusFailed,
		Err:       errors.New("model limiti doldu"),
		ParkUntil: p.until,
	}
}

func workerCard(t *testing.T, h *harness, agent string) Run {
	t.Helper()
	run, err := h.runner.Create(context.Background(), CreateRequest{
		Prompt: "iki yüz ürünü yeniden yaz",
		Agent:  agent,
		Params: `{"import_id":"imp_1","product_ids":["a"]}`,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return run
}

// The seam declared ParkUntil and never read it, so an executor that spent a
// budget could only fail. A failed card is something to go and fix; a parked
// one resumes on its own, and an operator cannot tell them apart from a status.
func TestRunExecutor_ParksAJobThatAskedToBeParked(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineResult))
	until := time.Now().Add(30 * time.Minute).UTC()
	h.runner.Register(newParkingWorker("leadgen", until))

	run := workerCard(t, h, "leadgen")
	if _, err := h.runner.Enqueue(context.Background(), run.ID); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	waitFor(t, "parked back to queued", func() bool {
		return statusOf(t, h, run.ID) == store.RunStatusQueued
	})

	got, err := h.runner.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// The reason stays on the row: it is read by the operator looking at the
	// board and it is what "queued" alone could never say.
	if got.Error == "" {
		t.Error("park edilmiş kart neden beklediğini söylemiyor")
	}

	// The pause is held under the lane's own key, not under a credential slot
	// this job never had.
	report, err := h.runner.Limits(context.Background(), 10)
	if err != nil {
		t.Fatalf("Limits: %v", err)
	}
	var found bool
	for _, hold := range report.Holds {
		if hold.AccountID == WorkerSlot {
			found = true
			if !hold.ResetsAt.Equal(until) {
				t.Errorf("beklenen %v, alınan %v", until, hold.ResetsAt)
			}
		}
	}
	if !found {
		t.Errorf("worker lane duraklaması bildirilmiyor: %+v", report.Holds)
	}
}

// The other half: an executor that did not ask for a pause still fails, exactly
// as before. This is the behaviour task-89 must not change.
func TestRunExecutor_StillFailsAJobThatDidNotAskForOne(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineResult))
	w := newFakeWorker("leadgen")
	w.failWith = errors.New("bozuk params")
	h.runner.Register(w)

	run := workerCard(t, h, "leadgen")
	if _, err := h.runner.Enqueue(context.Background(), run.ID); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	<-w.started
	close(w.release)

	waitFor(t, "failed", func() bool {
		return statusOf(t, h, run.ID) == store.RunStatusFailed
	})

	report, _ := h.runner.Limits(context.Background(), 10)
	for _, hold := range report.Holds {
		if hold.AccountID == WorkerSlot {
			t.Error("park istemeyen bir iş lane'i duraklattı")
		}
	}
}

// A pause that has already expired is not a pause. Parking on it would arm a
// wake-up in the past and hand the operator a card that says it resumes at a
// time that has gone.
func TestRunExecutor_DoesNotParkOnATimeThatHasPassed(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineResult))
	h.runner.Register(newParkingWorker("leadgen", time.Now().Add(-time.Hour).UTC()))

	run := workerCard(t, h, "leadgen")
	if _, err := h.runner.Enqueue(context.Background(), run.ID); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	waitFor(t, "failed", func() bool {
		return statusOf(t, h, run.ID) == store.RunStatusFailed
	})
}

// While the lane is held, a worker card is stepped over rather than started:
// claiming it would spend a subprocess to be told again what the last one was
// told.
func TestDispatch_HoldsTheWorkerLaneWhileItsBudgetIsSpent(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineResult))
	w := newParkingWorker("leadgen", time.Now().Add(30*time.Minute).UTC())
	h.runner.Register(w)

	first := workerCard(t, h, "leadgen")
	if _, err := h.runner.Enqueue(context.Background(), first.ID); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	waitFor(t, "the first card to park", func() bool {
		return statusOf(t, h, first.ID) == store.RunStatusQueued
	})
	attempts := w.tried

	second := workerCard(t, h, "leadgen")
	if _, err := h.runner.Enqueue(context.Background(), second.ID); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := h.runner.Kick(context.Background()); err != nil {
		t.Fatalf("Kick: %v", err)
	}

	// Nothing new may start while the budget is spent.
	time.Sleep(200 * time.Millisecond)
	if w.tried != attempts {
		t.Errorf("tutulan lane'de %d yeni deneme başladı", w.tried-attempts)
	}
	if s := statusOf(t, h, second.ID); s != store.RunStatusQueued {
		t.Errorf("ikinci kart %q durumunda", s)
	}
}

// The pause survives a restart: it is rebuilt from the durable log, under the
// same key it was written with.
func TestResume_RestoresTheWorkerLaneHold(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineResult))
	until := time.Now().Add(45 * time.Minute).UTC()

	if err := h.store.InsertRateLimitEvent(context.Background(), store.RateLimitRow{
		At:        time.Now().UTC(),
		Phase:     store.RateLimitPhaseRun,
		AccountID: WorkerSlot,
		ResetsAt:  until,
		Detail:    "model limiti doldu",
	}); err != nil {
		t.Fatalf("InsertRateLimitEvent: %v", err)
	}

	if err := h.runner.Resume(context.Background()); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if _, out := h.runner.heldUntil(WorkerSlot, time.Now().UTC()); !out {
		t.Fatal("yeniden başlatmadan sonra worker lane duraklaması kayboldu")
	}
}

// ResetFromMessage is exported so an Executor answers the same question the
// claude path answers, from the same wording — one parser of one CLI's
// sentence, not two.
func TestResetFromMessage_ReadsTheProvidersOwnWindow(t *testing.T) {
	at, ok := ResetFromMessage("Claude AI usage limit reached|1788104400")
	if !ok || at.Unix() != 1788104400 {
		t.Fatalf("sağlayıcının kendi zamanı okunmadı: %v %v", at, ok)
	}
	if _, ok := ResetFromMessage("rate limited, try later"); ok {
		t.Error("zaman içermeyen bir cümleden zaman uyduruldu")
	}
}
