package brain

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/llm"
)

// --- fakes -------------------------------------------------------------------

type fakeCounter struct{ n int }

func (f *fakeCounter) CountBrainNodes(context.Context, string) (int, error) { return f.n, nil }

type fakeProbe struct {
	mu sync.Mutex
	up bool
}

func (p *fakeProbe) Health(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.up {
		return nil
	}
	return llm.ErrProviderUnavailable
}

func (p *fakeProbe) bringUp() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.up = true
}

// cursorMap is the durable half of the status, in memory.
type cursorMap struct {
	mu sync.Mutex
	m  map[string]string
}

func newCursors() *cursorMap { return &cursorMap{m: map[string]string{}} }

func (c *cursorMap) BrainCursor(_ context.Context, key string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m[key], nil
}

func (c *cursorMap) SetBrainCursor(_ context.Context, key, _, cursor string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[key] = cursor
	return nil
}

// supervisorFixture builds a supervisor over one throwaway project.
func supervisorFixture(t *testing.T, model Completer) (*Supervisor, *cursorMap, string) {
	t.Helper()
	root := t.TempDir()
	project := filepath.Join(root, "repo")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"main.go":   "package main\n\nfunc main() {}\n",
		"README.md": "# Title\n\nSome prose.\n",
	} {
		if err := os.WriteFile(filepath.Join(project, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.Load()
	cfg.StorePath = filepath.Join(t.TempDir(), "mimir.db")
	cfg.BrainScanRoots = []string{root}
	cfg.BrainScanIdleInterval = time.Hour
	cfg.BrainScanBackoffMin = 10 * time.Millisecond
	cfg.BrainScanBackoffMax = 40 * time.Millisecond

	cursors := newCursors()
	s := NewSupervisor(cfg, SupervisorDeps{
		Core:    New(cfg, newFakeStore(), model),
		Hashes:  &fakeHashes{},
		Cursors: cursors,
		Counter: &fakeCounter{n: 2},
	})
	return s, cursors, project
}

// runSweep drives one sweep directly rather than racing Run's timer.
func runSweep(t *testing.T, s *Supervisor) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s.sweep(ctx)
	s.finishSweep(ctx)
}

// --- the properties ----------------------------------------------------------

func TestSupervisor_ScansEveryProjectUnderItsRoots(t *testing.T) {
	s, _, _ := supervisorFixture(t, &fakeLLM{distil: goodDistil, relate: `{"related":[]}`})
	runSweep(t, s)

	got := s.Status()
	if got.ScannedSession != 2 {
		t.Fatalf("scanned = %d, want the 2 files: %+v", got.ScannedSession, got)
	}
	if got.Phase != PhaseIdle || got.Sweeps != 1 {
		t.Errorf("status after a sweep = %+v", got)
	}
	if got.NodesTotal != 2 {
		t.Errorf("nodes_total = %d, want the counter's answer", got.NodesTotal)
	}
}

// The property the whole feature rests on: a sweep over a machine where nothing
// changed makes no model call at all. A Completer that panics is the assertion.
func TestSupervisor_MakesNoModelCallWhenNothingChanged(t *testing.T) {
	s, _, _ := supervisorFixture(t, &fakeLLM{distil: goodDistil, relate: `{"related":[]}`})
	runSweep(t, s)

	// Feed the stored hashes back the way the store would, then take the model
	// away entirely.
	store := s.deps.Core.store.(*fakeStore)
	known := map[string]string{}
	for _, n := range store.nodes {
		known[n.SourceKey] = n.ContentHash
	}
	s.deps.Hashes = &fakeHashes{m: known}
	s.deps.Core = New(s.cfg, store, nil) // a model call here panics

	runSweep(t, s)
	if got := s.Status(); got.ScannedSession != 0 {
		t.Fatalf("a second sweep scanned %d files: %+v", got.ScannedSession, got)
	}
}

// A paused supervisor spawns nothing. Not "stops soon" — nothing.
func TestSupervisor_PausedSpawnsNothing(t *testing.T) {
	s, _, _ := supervisorFixture(t, nil) // any model call panics
	s.Pause()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not settle while paused")
	}
	if got := s.Status(); got.Phase != PhasePaused || !got.Paused {
		t.Errorf("status = %+v, want paused", got)
	}
}

// Pause has to reach the pass in flight. A pass is a minute of subprocesses, and
// a pause that waited for it would look broken to the person who pressed it.
func TestSupervisor_PauseCancelsThePassInFlight(t *testing.T) {
	blocked := make(chan struct{})
	model := &blockingLLM{entered: make(chan struct{}, 1), release: blocked}
	s, _, _ := supervisorFixture(t, model)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() { defer close(done); s.sweep(ctx) }()

	select {
	case <-model.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the pass never started")
	}
	s.Pause()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("pause did not reach the pass in flight")
	}
	close(blocked)
}

func TestSupervisor_ScanNowIsRefusedWhilePaused(t *testing.T) {
	s, _, _ := supervisorFixture(t, nil)
	s.Pause()
	if s.ScanNow(llm.Selection{}) {
		t.Error("a paused scan must refuse to be started by a button")
	}
	s.Resume()
	if !s.ScanNow(llm.Selection{}) {
		t.Error("a resumed scan must accept it")
	}
}

// A provider that is signed out must not become a thousand failed subprocesses
// an hour: the waits grow, the status says so, and the probe answering is what
// ends the wait rather than the clock running out.
func TestSupervisor_BacksOffWhenTheProviderIsDown(t *testing.T) {
	model := &flakyLLM{}
	s, _, _ := supervisorFixture(t, model)

	var (
		mu        sync.Mutex
		waits     []time.Duration
		downWhile []bool
		probe     = &fakeProbe{}
	)
	s.deps.Probe = probe
	s.deps.After = func(d time.Duration) <-chan time.Time {
		mu.Lock()
		waits = append(waits, d)
		downWhile = append(downWhile, s.Status().ProviderDown)
		n := len(waits)
		mu.Unlock()

		// The provider comes back during the third wait. Both halves flip: the
		// probe is what the supervisor asks, the model is what actually has to
		// work for the pass to succeed.
		if n >= 3 {
			probe.bringUp()
			model.bringUp()
		}
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.sweep(ctx)

	mu.Lock()
	defer mu.Unlock()
	if len(waits) < 2 {
		t.Fatalf("the supervisor did not back off: %v", waits)
	}
	if waits[0] != s.cfg.BrainScanBackoffMin {
		t.Errorf("first wait = %v, want the floor %v", waits[0], s.cfg.BrainScanBackoffMin)
	}
	if waits[1] <= waits[0] {
		t.Errorf("the backoff did not grow: %v", waits)
	}
	for _, w := range waits {
		if w > s.cfg.BrainScanBackoffMax {
			t.Errorf("a wait ran past the ceiling: %v", waits)
		}
	}
	for i, down := range downWhile {
		if !down {
			t.Errorf("wait %d did not report the provider as down: %v", i, downWhile)
		}
	}
	if got := s.Status(); got.ScannedSession == 0 || got.ProviderDown {
		t.Errorf("the sweep did not recover once the provider came back: %+v", got)
	}
}

// An unreadable file is not a provider outage, so it must not trigger the
// backoff — it leaves the project for the next sweep instead.
func TestSupervisor_UnreadableWorkDoesNotBackOff(t *testing.T) {
	s, _, project := supervisorFixture(t, &fakeLLM{distil: goodDistil, relate: `{"related":[]}`})
	if err := os.WriteFile(filepath.Join(project, "notes.pdf"), []byte("%PDF-1.4\nstable bytes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.cfg.BrainScanPDFPath = filepath.Join(t.TempDir(), "no-such-pdftotext")
	s.deps.Core = New(s.cfg, s.deps.Core.store, &fakeLLM{distil: goodDistil, relate: `{"related":[]}`})

	waits := 0
	s.deps.After = func(time.Duration) <-chan time.Time {
		waits++
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}

	runSweep(t, s)
	if waits != 0 {
		t.Errorf("an unreadable file caused %d backoff waits", waits)
	}
	if got := s.Status(); got.ProviderDown {
		t.Errorf("an unreadable file was reported as a provider outage: %+v", got)
	}
}

func TestSupervisor_ResumesFromTheStoredCursor(t *testing.T) {
	s, cursors, _ := supervisorFixture(t, &fakeLLM{distil: goodDistil, relate: `{"related":[]}`})
	runSweep(t, s)

	next := NewSupervisor(s.cfg, SupervisorDeps{
		Core:    New(s.cfg, newFakeStore(), nil),
		Hashes:  &fakeHashes{},
		Cursors: cursors,
	})
	next.restore(context.Background())

	got := next.Status()
	if got.ScannedTotal != 2 || got.Sweeps != 1 {
		t.Errorf("a restart forgot what earlier runs did: %+v", got)
	}
}

func TestSupervisor_ARootThatDoesNotExistIsNotFatal(t *testing.T) {
	s, _, _ := supervisorFixture(t, &fakeLLM{distil: goodDistil, relate: `{"related":[]}`})
	s.cfg.BrainScanRoots = append(s.cfg.BrainScanRoots, filepath.Join(t.TempDir(), "nope"))

	runSweep(t, s)
	if got := s.Status(); got.ScannedSession != 2 {
		t.Errorf("a missing root stopped the sweep: %+v", got)
	}
}

// Status is read by an HTTP handler while the loop writes it. -race is the
// assertion; this test is the thing that gives it something to watch.
func TestSupervisor_StatusIsSafeUnderConcurrentReads(t *testing.T) {
	s, _, _ := supervisorFixture(t, &fakeLLM{distil: goodDistil, relate: `{"related":[]}`})

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = s.Status()
			}
		}
	}()

	runSweep(t, s)
	close(stop)
	wg.Wait()
}

// A scan with no folders is a setting, not a fault — and the status has to say
// so in a shape the desktop can read. `roots` carries no omitempty, so a nil
// slice reaches the screen as JSON `null`, `status.roots.join(...)` throws, and
// the whole window goes blank with no message.
func TestSupervisor_StatusRootsAreNeverNil(t *testing.T) {
	s, _, _ := supervisorFixture(t, &fakeLLM{distil: goodDistil, relate: `{"related":[]}`})
	s.deps.Policy = func() ScanPolicy { return ScanPolicy{} }

	// Not from the fixture's own seeding: the interesting moment is after the
	// operator has removed the last folder, which the supervisor records on its
	// next look at the policy.
	s.withStatus(func(st *ScanStatus) {
		st.Roots = nil
		st.Excludes = nil
	})

	status := s.Status()
	if status.Roots == nil {
		t.Fatal("Roots is nil; it must be an empty list so the status marshals as []")
	}
	if len(status.Roots) != 0 {
		t.Fatalf("Roots = %v, want empty", status.Roots)
	}
	if status.Excludes == nil {
		t.Fatal("Excludes is nil; it must be an empty list")
	}

	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"roots":[]`) {
		t.Fatalf("marshalled status does not carry an empty roots list: %s", raw)
	}
}

// The copy is the other half of the same method: a caller must not be handed a
// slice the sweep still writes into.
func TestSupervisor_StatusRootsAreACopy(t *testing.T) {
	s, _, _ := supervisorFixture(t, &fakeLLM{distil: goodDistil, relate: `{"related":[]}`})
	s.withStatus(func(st *ScanStatus) { st.Roots = []string{"/one", "/two"} })

	status := s.Status()
	status.Roots[0] = "/scribbled"

	if again := s.Status(); again.Roots[0] != "/one" {
		t.Fatalf("Roots[0] = %q; the caller wrote through into the supervisor", again.Roots[0])
	}
}

// flakyLLM fails every distil until it is brought up, which is what a signed-out
// agy looks like from here.
type flakyLLM struct {
	mu sync.Mutex
	up bool
}

func (f *flakyLLM) bringUp() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.up = true
}

func (f *flakyLLM) CompleteWith(ctx context.Context, c llm.Class, _ llm.Selection, r llm.Request) (llm.Response, error) {
	return f.Complete(ctx, c, r)
}

func (f *flakyLLM) Complete(_ context.Context, c llm.Class, req llm.Request) (llm.Response, error) {
	f.mu.Lock()
	up := f.up
	f.mu.Unlock()
	if !up {
		return llm.Response{}, llm.ErrProviderUnavailable
	}
	if strings.Contains(req.System, "knowledge graph") {
		return llm.Response{Structured: json.RawMessage(`{"related":[]}`), Provider: "agy", Model: "test"}, nil
	}
	return llm.Response{Structured: json.RawMessage(goodDistil), Provider: "agy", Model: "test"}, nil
}

// blockingLLM parks the first distil until it is released, so a test can pause
// a pass that is genuinely in flight.
// A selection is for the sweep the operator started and no longer.
//
// The resident loop keeps running after that sweep, and a routing that outlived
// it would quietly spend the chosen budget on every sweep that follows —
// including the ones nobody is watching, which is the whole reason this is not
// a setting.
func TestSupervisor_ScanNowSelectionLastsOneSweep(t *testing.T) {
	s, _, _ := supervisorFixture(t, &fakeLLM{distil: goodDistil, relate: `{"related":[]}`})

	sel := llm.Selection{Provider: "claude", Model: "claude-haiku-4-5-20251001"}
	if !s.ScanNow(sel) {
		t.Fatal("ScanNow refused an unpaused supervisor")
	}

	if got := s.takePending(); got != sel {
		t.Fatalf("the sweep took %+v, want the operator's %+v", got, sel)
	}
	if got := s.takePending(); !got.IsZero() {
		t.Errorf("the next sweep took %+v, want the zero selection", got)
	}
}

// A paused scan must not bank a routing for whenever it is resumed: the button
// was refused, so nothing about it may take effect later.
func TestSupervisor_ScanNowOnAPausedScanKeepsNoSelection(t *testing.T) {
	s, _, _ := supervisorFixture(t, &fakeLLM{distil: goodDistil, relate: `{"related":[]}`})
	s.Pause()

	if s.ScanNow(llm.Selection{Provider: "claude"}) {
		t.Fatal("ScanNow accepted a paused supervisor")
	}
	if got := s.takePending(); !got.IsZero() {
		t.Errorf("a refused ScanNow left %+v behind", got)
	}
}

type blockingLLM struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingLLM) CompleteWith(ctx context.Context, c llm.Class, _ llm.Selection, r llm.Request) (llm.Response, error) {
	return b.Complete(ctx, c, r)
}

func (b *blockingLLM) Complete(ctx context.Context, _ llm.Class, _ llm.Request) (llm.Response, error) {
	b.once.Do(func() { b.entered <- struct{}{} })
	select {
	case <-b.release:
		return llm.Response{}, errors.New("released")
	case <-ctx.Done():
		return llm.Response{}, ctx.Err()
	}
}

// A pause has to be visible immediately. The loop waits out a long idle
// interval between sweeps, and a status that kept saying "idle" until that
// expired would be a screen contradicting the button somebody just pressed.
func TestSupervisor_PauseIsVisibleWithoutWaitingOutTheIdleInterval(t *testing.T) {
	s, _, _ := supervisorFixture(t, &fakeLLM{distil: goodDistil, relate: `{"related":[]}`})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()

	// Wait for the first sweep to finish, then pause between sweeps.
	deadline := time.Now().Add(3 * time.Second)
	for s.Status().Sweeps == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	s.Pause()

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := s.Status(); got.Phase == PhasePaused && got.Paused {
			cancel()
			<-done
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("status still reads %+v after a pause", s.Status())
}

// The console is what the Terminals tab reads. It has to name files, survive a
// reader that fell behind, and never grow without bound.
func TestSupervisor_EventsNameTheFilesAndStayBounded(t *testing.T) {
	s, _, _ := supervisorFixture(t, &fakeLLM{distil: goodDistil, relate: `{"related":[]}`})
	runSweep(t, s)

	events, seq := s.Events(0, 0)
	if seq == 0 || len(events) == 0 {
		t.Fatalf("the sweep wrote no console lines: %d events, seq %d", len(events), seq)
	}

	var kinds, text string
	for _, e := range events {
		kinds += e.Kind + " "
		text += e.Text + "\n"
	}
	for _, want := range []string{EventSweep, EventProject, EventFile, EventPass} {
		if !strings.Contains(kinds, want) {
			t.Errorf("no %q line: %s", want, kinds)
		}
	}
	if !strings.Contains(text, "main.go") {
		t.Errorf("the files were not named:\n%s", text)
	}

	// A reader that has seen everything gets nothing new, and one that has seen
	// nothing gets the tail rather than an error.
	if rest, _ := s.Events(seq, 0); len(rest) != 0 {
		t.Errorf("a caught-up reader was handed %d lines again", len(rest))
	}

	for i := 0; i < scanLogSize+50; i++ {
		s.emit(EventFile, "/x", "filler")
	}
	if got, _ := s.Events(0, 0); len(got) > scanLogSize {
		t.Errorf("the console kept %d lines, over the %d cap", len(got), scanLogSize)
	}
}

// Pausing and resuming are the operator's own actions, and a console that does
// not show them makes the scan look like it stopped on its own.
func TestSupervisor_ControlsAreOnTheConsole(t *testing.T) {
	s, _, _ := supervisorFixture(t, nil)
	s.Pause()
	s.Resume()

	events, _ := s.Events(0, 0)
	var text string
	for _, e := range events {
		if e.Kind == EventControl {
			text += e.Text + " "
		}
	}
	if !strings.Contains(text, "duraklat") || !strings.Contains(text, "sürdür") {
		t.Errorf("controls are not on the console: %q", text)
	}
}

// --- the scan permission surface ---------------------------------------------

// The loop must ask for the policy every sweep, not capture it once. This is
// the property that makes the Brain tab's folder list a control rather than a
// display: the operator excludes something and the *next* sweep obeys, with no
// daemon restart.
func TestSupervisor_PolicyIsReReadEverySweep(t *testing.T) {
	s, _, project := supervisorFixture(t, &fakeLLM{distil: goodDistil, relate: `{"related":[]}`})

	var mu sync.Mutex
	policy := ScanPolicy{Roots: s.cfg.BrainScanRoots}
	s.deps.Policy = func() ScanPolicy {
		mu.Lock()
		defer mu.Unlock()
		return policy
	}

	runSweep(t, s)
	if got := s.Status().ScannedSession; got != 2 {
		t.Fatalf("first sweep scanned %d files, want 2", got)
	}

	// Exclude one of the two files between sweeps.
	mu.Lock()
	policy.Excludes = []string{filepath.Join(project, "README.md")}
	mu.Unlock()

	// A fresh hash store, so the second sweep would otherwise re-read both.
	s.deps.Hashes = &fakeHashes{}
	s.mu.Lock()
	s.status.ScannedSession = 0
	s.mu.Unlock()

	runSweep(t, s)

	got := s.Status()
	if got.ScannedSession != 1 {
		t.Errorf("after excluding one file, the sweep scanned %d, want 1", got.ScannedSession)
	}
	if len(got.Excludes) != 1 {
		t.Errorf("status excludes = %v, want the one entry the policy carries", got.Excludes)
	}
}

// A root list the operator emptied has to leave the loop alive. Before this,
// an empty list ended Run's goroutine and only a daemon restart brought
// scanning back — so removing the last folder was a one-way door.
func TestSupervisor_EmptyRootsPauseTheLoopRatherThanEndIt(t *testing.T) {
	s, _, _ := supervisorFixture(t, &fakeLLM{distil: goodDistil, relate: `{"related":[]}`})
	roots := s.cfg.BrainScanRoots

	var mu sync.Mutex
	var empty bool
	s.deps.Policy = func() ScanPolicy {
		mu.Lock()
		defer mu.Unlock()
		if empty {
			return ScanPolicy{}
		}
		return ScanPolicy{Roots: roots}
	}

	// With no roots, a sweep discovers nothing and spends nothing.
	mu.Lock()
	empty = true
	mu.Unlock()
	runSweep(t, s)
	if got := s.Status().ScannedSession; got != 0 {
		t.Fatalf("an empty policy scanned %d files, want 0", got)
	}

	// Adding a root back must be enough; nothing was torn down.
	mu.Lock()
	empty = false
	mu.Unlock()
	runSweep(t, s)
	if got := s.Status().ScannedSession; got != 2 {
		t.Errorf("after restoring the root, scanned = %d, want 2", got)
	}
}

// With no Policy wired at all, the loop behaves exactly as it did before the
// policy existed. Every other caller of this package depends on that.
func TestSupervisor_NilPolicyFallsBackToTheConfiguredRoots(t *testing.T) {
	s, _, _ := supervisorFixture(t, &fakeLLM{distil: goodDistil, relate: `{"related":[]}`})
	s.deps.Policy = nil

	got := s.policy()
	if len(got.Roots) != len(s.cfg.BrainScanRoots) || got.Roots[0] != s.cfg.BrainScanRoots[0] {
		t.Errorf("policy() = %+v, want cfg.BrainScanRoots %v", got, s.cfg.BrainScanRoots)
	}
	if len(got.Excludes) != 0 {
		t.Errorf("policy() excludes = %v, want none", got.Excludes)
	}
}
