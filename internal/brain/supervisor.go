package brain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/llm"
)

// Phases of the supervisor, as the desktop reports them.
const (
	PhaseIdle        = "idle"
	PhaseDiscovering = "discovering"
	PhaseScanning    = "scanning"
	PhaseBackoff     = "backoff"
	PhasePaused      = "paused"
)

// scanCursorKey is where a finished sweep leaves its summary. The value is a
// small JSON blob rather than a typed row: the real progress is content_hash on
// brain_nodes, and a second table that can disagree with the nodes is worse
// than a display hint that can be thrown away.
const scanCursorKey = "scan:supervisor"

// NodeCounter is the one count the tab shows that a scan does not produce.
type NodeCounter interface {
	CountBrainNodes(ctx context.Context, projectPath string) (int, error)
}

// HealthProbe is the cheap "is the distil tier up" check — `agy models`, one
// subprocess, where a scan pass is twelve of them. llm.Provider satisfies it.
type HealthProbe interface {
	Health(ctx context.Context) error
}

// SupervisorDeps are the collaborators, all interfaces so the loop can be
// tested without a database, a repository on disk, or a model.
type SupervisorDeps struct {
	Core    *Core
	Hashes  HashStore
	Cursors CursorStore
	Counter NodeCounter
	Probe   HealthProbe
	Log     *slog.Logger

	// After is a test seam. Nil means time.After — the loop spends most of its
	// life waiting, and a test that actually waited would be a test nobody runs.
	After func(time.Duration) <-chan time.Time

	// Policy is where this loop asks what it is allowed to read. It is a
	// function rather than a value because the answer changes while the loop is
	// running: the operator adds a folder on the Brain tab and the next sweep
	// has to pick it up, without a daemon restart and without this package
	// knowing that a settings file is what is behind it.
	//
	// Nil means cfg.BrainScanRoots and no exclusions — the behaviour every
	// caller had before the policy existed.
	Policy func() ScanPolicy
}

// ScanPolicy is what the supervisor is permitted to read, as it reads it.
//
// The same shape settings.ScanPolicy has, restated here so internal/brain does
// not import the settings package: the loop does not care that this came from a
// file, and a dependency on where it is stored would make it care.
type ScanPolicy struct {
	Roots    []string
	Excludes []string
}

// ScanStatus is the whole of what the Brain tab knows. One snapshot, taken
// under the lock, because a screen that mixed a project name from one pass with
// a count from another would be lying in a way nobody could reproduce.
type ScanStatus struct {
	Phase  string   `json:"phase"`
	Paused bool     `json:"paused"`
	Roots  []string `json:"roots"`
	// Excludes is the operator's "not this one" list, echoed back so the tab
	// can render what it is about to enforce rather than what it last sent.
	Excludes     []string `json:"excludes,omitempty"`
	Provider     string   `json:"provider"`
	Model        string   `json:"model"`
	Project      string   `json:"project,omitempty"`
	ProjectLabel string   `json:"project_label,omitempty"`
	ProjectIndex int      `json:"project_index"`
	ProjectCount int      `json:"project_count"`

	Remaining int `json:"remaining"`
	Skipped   int `json:"skipped_unchanged"`
	Eligible  int `json:"eligible"`

	ScannedSession    int `json:"scanned_session"`
	FailedSession     int `json:"failed_session"`
	UnreadableSession int `json:"unreadable_session"`
	ScannedTotal      int `json:"scanned_total"`
	Sweeps            int `json:"sweeps"`
	NodesTotal        int `json:"nodes_total"`

	SweepStarted   time.Time `json:"sweep_started,omitempty"`
	LastPassAt     time.Time `json:"last_pass_at,omitempty"`
	LastSweepEnded time.Time `json:"last_sweep_ended,omitempty"`
	NextSweepAt    time.Time `json:"next_sweep_at,omitempty"`

	ProviderDown bool      `json:"provider_down"`
	BackoffUntil time.Time `json:"backoff_until,omitempty"`
	LastError    string    `json:"last_error,omitempty"`
}

// scanCursor is the durable half of the status.
type scanCursor struct {
	ScannedTotal   int    `json:"scanned_total"`
	Sweeps         int    `json:"sweeps"`
	LastSweepEnded int64  `json:"last_sweep_ended"`
	LastProject    string `json:"last_project"`
}

// Event kinds, as the console prints them.
const (
	EventSweep   = "sweep"
	EventProject = "project"
	EventFile    = "file"
	// EventChanged is a file that was already known and moved. Distinct from
	// EventFile because it is the one line that says the detection is working:
	// an operator who edits a document and sees it here knows Brain re-read it.
	EventChanged    = "changed"
	EventFailed     = "failed"
	EventUnreadable = "unreadable"
	EventPass       = "pass"
	EventControl    = "control"
	EventBackoff    = "backoff"
	// EventProvider says the tier answered on a provider other than the one
	// this sweep asked for. It exists because the distil fallback is allowed
	// again: a hand-off that moves the bill has to be visible in the console
	// the operator is already watching, not inferred later from node rows.
	EventProvider = "provider"
)

// ScanEvent is one line of the scan's own console.
//
// The scan is not a coding run: it has no transcript, no bus and no run id, so
// none of internal/events applies to it. What a person watching it wants is
// narrower than a run's event stream anyway — which file, in which project,
// and whether it worked — so this is a bounded ring buffer read by polling,
// not a second streaming mechanism.
type ScanEvent struct {
	Seq     int64     `json:"seq"`
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Project string    `json:"project,omitempty"`
	Text    string    `json:"text"`
}

// scanLogSize is how much of the scan's history is kept. A sweep of this
// machine is a few thousand files; keeping all of them in memory to render a
// console nobody may open is the wrong trade, and the store already holds the
// permanent record — one node per file.
const scanLogSize = 600

// Supervisor keeps a scan running for as long as the daemon lives, so there is
// always an agy working.
//
// It is a separate type from Core rather than a method on it because it owns
// mutable runtime state — paused, in flight, how far — and Core is shared with
// cmd/mimir-mcp, a binary with no lifetime to own. What it does is what
// cmd/mimir-scan does by hand: discover the projects under a root, run each to
// `remaining == 0`, then start again. What it adds is everything a loop that
// never ends needs and a one-shot command does not: a pause the operator can
// press, a backoff for the stretch where the provider is signed out, and a
// snapshot a screen can read.
type Supervisor struct {
	cfg  config.Config
	deps SupervisorDeps
	log  *slog.Logger

	mu     sync.Mutex
	status ScanStatus
	paused bool
	// passCancel stops the pass in flight. A pass is roughly a minute of
	// subprocesses, so a pause that waited for it would look broken.
	passCancel context.CancelFunc

	// wake is how Resume and ScanNow shorten a wait. Buffered to one: two
	// clicks are one wake-up, and a send must never block the caller of an HTTP
	// handler.
	wake chan struct{}

	// pending is the routing an operator asked the next sweep to use. It is
	// taken and cleared when that sweep starts, so a hand-started scan spends
	// the budget the operator picked and the resident loop goes straight back
	// to the configured one. Guarded by mu with the rest of the runtime state.
	pending llm.Selection

	// running is the provider the last pass actually answered with. Kept apart
	// from status.Provider — which says what was asked for — because the gap
	// between the two is exactly what a silent fallback looks like.
	running string

	// The console's ring buffer, under the same mutex as the status: a reader
	// that saw a status from one moment and a log from another would be looking
	// at two different scans.
	events   []ScanEvent
	eventSeq int64

	backoff time.Duration
}

// NewSupervisor builds the loop. It does not start it — Run does, and the
// daemon owns that goroutine.
func NewSupervisor(cfg config.Config, deps SupervisorDeps) *Supervisor {
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	if deps.After == nil {
		deps.After = time.After
	}
	s := &Supervisor{
		cfg:  cfg,
		deps: deps,
		log:  deps.Log,
		wake: make(chan struct{}, 1),
	}
	initial := s.policy()
	s.status = ScanStatus{
		Phase:    PhaseIdle,
		Roots:    initial.Roots,
		Excludes: initial.Excludes,
		Provider: cfg.DistillProvider,
		Model:    cfg.DistillModel,
	}
	return s
}

// policy is what this loop may read, right now.
//
// Every call goes through here rather than through cfg directly, so there is
// one place where "the operator has not configured anything" resolves to the
// shipped default — and one place to look when the answer is surprising.
func (s *Supervisor) policy() ScanPolicy {
	if s.deps.Policy == nil {
		return ScanPolicy{Roots: s.cfg.BrainScanRoots}
	}
	return s.deps.Policy()
}

// Run sweeps the roots until ctx is cancelled.
//
// It never returns an error. A root that does not exist, a project that cannot
// be scanned and a provider that is signed out are all states this loop is
// designed to sit in rather than fail out of, and the daemon has nowhere to
// report an error to at this point anyway.
func (s *Supervisor) Run(ctx context.Context) {
	if s.deps.Core == nil || !s.deps.Core.Available() {
		s.log.Info("brain scan supervisor is inert — no knowledge base to write to")
		return
	}
	s.restore(ctx)

	for {
		if ctx.Err() != nil {
			return
		}
		if s.isPaused() {
			s.setPhase(PhasePaused)
			if !s.waitFor(ctx, time.Hour) {
				return
			}
			continue
		}

		// An empty root list is a state to sit in, not one to exit on. It is
		// what the operator sees after removing the last folder, and the loop
		// has to still be here when they add the next one — before this, an
		// empty list ended the goroutine and only a daemon restart brought
		// scanning back.
		if len(s.policy().Roots) == 0 {
			s.setPhase(PhaseIdle)
			if !s.waitFor(ctx, s.cfg.BrainScanIdleInterval) {
				return
			}
			continue
		}

		s.sweep(ctx)
		if ctx.Err() != nil {
			return
		}
		// A sweep the operator interrupted did not finish, so it is not counted
		// as one and the loop goes straight back to the top to report itself
		// paused. Calling finishSweep here would both inflate the sweep count
		// and leave the phase reading "idle" until the next tick — which is
		// what a person who just pressed pause would be staring at.
		if s.isPaused() {
			continue
		}

		s.finishSweep(ctx)
		if !s.waitFor(ctx, s.cfg.BrainScanIdleInterval) {
			return
		}
	}
}

// sweep is one pass over every project under every root.
func (s *Supervisor) sweep(ctx context.Context) {
	s.startSweep()

	// Taken once, here, rather than read per project: a sweep runs on one
	// routing from beginning to end, and a selection that expired half way
	// through would spend two budgets for one decision.
	sel := s.takePending()
	provider, model := s.cfg.DistillProvider, s.cfg.DistillModel
	if sel.Provider != "" {
		provider = sel.Provider
	}
	if sel.Model != "" {
		model = sel.Model
	}

	// Read once per sweep, like the routing above and for the same reason: a
	// sweep that picked up a new exclusion half way through would have already
	// read the file it names, and the operator would have no way to tell which
	// half of the pass their change applied to.
	policy := s.policy()
	exclude := NewExcluder(policy.Excludes)

	projects := s.discover(policy.Roots)
	s.emit(EventSweep, "", fmt.Sprintf("tur başladı — %d proje, %s · %s",
		len(projects), provider, model))
	s.withStatus(func(st *ScanStatus) {
		st.ProjectCount = len(projects)
		st.Phase = PhaseScanning
		st.Provider = provider
		st.Model = model
		st.Roots = policy.Roots
		st.Excludes = exclude.List()
	})

	for i, project := range projects {
		if ctx.Err() != nil || s.isPaused() {
			return
		}
		s.withStatus(func(st *ScanStatus) {
			st.Project = project
			st.ProjectLabel = filepath.Base(project)
			st.ProjectIndex = i + 1
		})
		s.emit(EventProject, project, fmt.Sprintf("%s (%d/%d)", filepath.Base(project), i+1, len(projects)))
		s.scanProject(ctx, project, sel, exclude)
		s.remember(ctx, project)
	}
}

// scanProject runs one project to completion, one bounded pass at a time.
func (s *Supervisor) scanProject(ctx context.Context, project string, sel llm.Selection, exclude Excluder) {
	for {
		if ctx.Err() != nil || s.isPaused() {
			return
		}

		passCtx, cancel := context.WithCancel(ctx)
		s.mu.Lock()
		s.passCancel = cancel
		s.mu.Unlock()

		started := time.Now()
		res, err := s.deps.Core.Scan(passCtx, project, s.deps.Hashes, ScanOptions{Selection: sel, Exclude: exclude})
		cancel()
		s.report(project, res, err, time.Since(started))

		s.mu.Lock()
		s.passCancel = nil
		s.mu.Unlock()

		if err != nil {
			// A cancelled pass is a pause or a shutdown, not a fault.
			if errors.Is(err, context.Canceled) {
				return
			}
			s.record(res, err)
			s.log.Warn("brain scan pass failed", "project", project, "error", err)
			return
		}
		s.record(res, nil)

		// A pass that distilled nothing while files failed is the provider not
		// answering, and it is checked before `remaining`: a batch where every
		// file failed reports nothing left to do, because the failures were
		// counted rather than left pending. Reading that as "this project is
		// finished" is how a signed-out agy would look like a completed sweep.
		if res.Scanned == 0 && res.Failed > 0 {
			if !s.backOff(ctx) {
				return
			}
			continue
		}
		if res.Remaining == 0 {
			return
		}
		// Work left and nothing distilled, with nothing failing either: the
		// batch could not be read for some reason that is this project's, not
		// the provider's. That is the next sweep's business.
		if res.Scanned == 0 {
			s.log.Warn("brain scan made no progress; leaving the project for the next sweep",
				"project", project, "remaining", res.Remaining)
			return
		}
		s.clearBackoff()
	}
}

// report turns one pass into console lines: the files by name, then a summary.
//
// The names are the point. A console that can only say "twelve files" is a
// progress bar with extra steps; one that says which file agy is reading is
// something a person can recognise their own work in.
// noteProvider says so, once, when the tier stops answering on the provider
// this sweep asked for.
//
// Once rather than per pass: a fallback that holds for a whole repository would
// otherwise write the same line a hundred times and bury the scan's own output.
// The state resets when the answer changes back, so a tier that recovers is
// reported too.
func (s *Supervisor) noteProvider(project, actual string) {
	if actual == "" {
		return
	}

	s.mu.Lock()
	asked := s.status.Provider
	last := s.running
	s.running = actual
	s.mu.Unlock()

	if actual == last || asked == "" || actual == asked {
		return
	}
	s.emit(EventProvider, project, fmt.Sprintf(
		"damıtma %s yerine %s ile sürüyor — bu sağlayıcının bütçesi harcanıyor", asked, actual))
}

func (s *Supervisor) report(project string, res ScanResult, err error, took time.Duration) {
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.emit(EventFailed, project, "geçiş başarısız: "+err.Error())
		}
		return
	}
	s.noteProvider(project, res.Provider)

	changed := make(map[string]struct{}, len(res.ChangedFiles))
	for _, f := range res.ChangedFiles {
		changed[f] = struct{}{}
	}
	for _, f := range res.Files {
		if _, ok := changed[f]; ok {
			s.emit(EventChanged, project, f+" — değişmiş, yeniden okundu")
			continue
		}
		s.emit(EventFile, project, f)
	}
	for _, f := range res.FailedFiles {
		s.emit(EventFailed, project, f+" — damıtılamadı, sonraki turda yeniden denenecek")
	}
	if res.Unreadable > 0 {
		s.emit(EventUnreadable, project, fmt.Sprintf("%d dosya okunamadı (metin katmanı yok ya da çıkarıcı kurulu değil)", res.Unreadable))
	}
	if res.Scanned > 0 || res.Failed > 0 {
		s.emit(EventPass, project, fmt.Sprintf("%d damıtıldı (%d değişmiş) · %d başarısız · %d kaldı · %s",
			res.Scanned, res.Changed, res.Failed, res.Remaining, took.Round(time.Second)))
	}
}

// backOff waits out a provider that is not answering, doubling the wait each
// time, and returns false when the caller should stop.
//
// Without it, a signed-out agy costs a failed subprocess every few seconds
// forever: one distil is about eleven seconds and a pass is twelve of them, so
// an immediate retry is roughly a thousand failed spawns an hour. The probe is
// what keeps the cost of being wrong small — `agy models` is one subprocess, and
// the moment it answers the loop goes back to work without waiting out the rest
// of the delay.
func (s *Supervisor) backOff(ctx context.Context) bool {
	s.mu.Lock()
	if s.backoff <= 0 {
		s.backoff = s.cfg.BrainScanBackoffMin
	} else {
		s.backoff *= 2
		if s.backoff > s.cfg.BrainScanBackoffMax {
			s.backoff = s.cfg.BrainScanBackoffMax
		}
	}
	wait := s.backoff
	s.status.Phase = PhaseBackoff
	s.status.ProviderDown = true
	s.status.BackoffUntil = time.Now().Add(wait)
	s.mu.Unlock()

	s.log.Warn("the distil provider is not answering; backing off", "wait", wait.String())
	s.emit(EventBackoff, "", fmt.Sprintf("%s yanıt vermiyor — %s sonra yeniden denenecek",
		s.cfg.DistillProvider, wait.Round(time.Second)))

	if !s.waitFor(ctx, wait) {
		return false
	}
	if s.isPaused() {
		return false
	}
	if s.deps.Probe != nil {
		if err := s.deps.Probe.Health(ctx); err != nil {
			s.withStatus(func(st *ScanStatus) { st.LastError = err.Error() })
			return ctx.Err() == nil
		}
	}
	s.clearBackoff()
	return ctx.Err() == nil
}

func (s *Supervisor) clearBackoff() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.backoff = 0
	s.status.ProviderDown = false
	s.status.BackoffUntil = time.Time{}
	if s.status.Phase == PhaseBackoff {
		s.status.Phase = PhaseScanning
	}
}

// discover lists every project under every root, once, in a stable order. A
// path found under two roots is scanned once: a node is keyed by its project
// path, so scanning it twice is two identical distils and one row.
func (s *Supervisor) discover(roots []string) []string {
	s.setPhase(PhaseDiscovering)

	seen := map[string]struct{}{}
	var out []string
	for _, root := range roots {
		found, err := DiscoverProjects(root, s.cfg.BrainScanDepth)
		if err != nil {
			// A root that is not there is not an error worth stopping for: a
			// machine without ~/Documents is a normal machine.
			s.log.Info("brain scan root is unreadable", "root", root, "error", err)
			continue
		}
		for _, p := range found {
			if _, dup := seen[p]; dup {
				continue
			}
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// waitFor blocks for d, or until woken, or until ctx ends. It reports false
// only when the daemon is going away.
func (s *Supervisor) waitFor(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-s.wake:
		return true
	case <-s.deps.After(d):
		return true
	}
}

// --- control -----------------------------------------------------------------

// Pause stops the scan, including the pass in flight. Files already distilled
// stay in the store with their hashes, so resuming costs nothing.
func (s *Supervisor) Pause() {
	s.mu.Lock()
	s.paused = true
	s.status.Paused = true
	s.status.Phase = PhasePaused
	cancel := s.passCancel
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	s.emit(EventControl, "", "duraklatıldı")
	// Wake the loop as well: without this, a pause pressed while the
	// supervisor is between sweeps is not visible in the status until the idle
	// interval expires, which is fifteen minutes of a screen saying "idle"
	// about a scan that is stopped.
	s.signal()
}

// Resume clears the pause and starts a sweep now rather than after the idle
// interval — an operator who just pressed resume is watching.
func (s *Supervisor) Resume() {
	s.mu.Lock()
	s.paused = false
	s.status.Paused = false
	s.status.Phase = PhaseIdle
	s.mu.Unlock()
	s.emit(EventControl, "", "sürdürüldü")
	s.signal()
}

// ScanNow shortens the idle wait. It refuses while paused and says so, because
// starting work the operator explicitly stopped is not something a button
// should do quietly.
// ScanNow wakes the loop for one sweep, optionally on a routing the operator
// named.
//
// The selection lives for that sweep and no longer: a first mount is where an
// operator reaches for a different provider because one tier's free pool will
// not cover the repository, and that is a decision about this scan, not a new
// default for a loop that will still be running tomorrow.
func (s *Supervisor) ScanNow(sel llm.Selection) bool {
	if s.isPaused() {
		return false
	}
	s.mu.Lock()
	s.pending = sel
	s.mu.Unlock()
	s.signal()
	return true
}

// takePending returns the operator's selection for the sweep that is starting
// and clears it, so the next one is the resident loop's own again.
func (s *Supervisor) takePending() llm.Selection {
	s.mu.Lock()
	defer s.mu.Unlock()
	sel := s.pending
	s.pending = llm.Selection{}
	return sel
}

func (s *Supervisor) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Status returns the snapshot the API serves.
func (s *Supervisor) Status() ScanStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.status
	out.Roots = append([]string(nil), s.status.Roots...)
	return out
}

// --- the console -------------------------------------------------------------

// emit appends one line, dropping the oldest when the buffer is full.
func (s *Supervisor) emit(kind, project, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.eventSeq++
	s.events = append(s.events, ScanEvent{
		Seq:     s.eventSeq,
		At:      time.Now().UTC(),
		Kind:    kind,
		Project: project,
		Text:    text,
	})
	if len(s.events) > scanLogSize {
		s.events = append([]ScanEvent(nil), s.events[len(s.events)-scanLogSize:]...)
	}
}

// Events returns what happened after seq, and the sequence to ask from next.
//
// A caller that has fallen further behind than the buffer is deep gets what is
// left rather than an error: a console that missed a hundred lines while its
// tab was closed wants the recent ones, not a failure.
func (s *Supervisor) Events(after int64, limit int) ([]ScanEvent, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if limit <= 0 || limit > scanLogSize {
		limit = scanLogSize
	}
	out := make([]ScanEvent, 0, limit)
	for _, e := range s.events {
		if e.Seq > after {
			out = append(out, e)
		}
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, s.eventSeq
}

// --- bookkeeping -------------------------------------------------------------

func (s *Supervisor) isPaused() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.paused
}

func (s *Supervisor) setPhase(phase string) {
	s.withStatus(func(st *ScanStatus) {
		if st.Paused && phase != PhasePaused {
			return
		}
		st.Phase = phase
	})
}

func (s *Supervisor) withStatus(f func(*ScanStatus)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.status)
}

func (s *Supervisor) startSweep() {
	s.withStatus(func(st *ScanStatus) {
		st.SweepStarted = time.Now().UTC()
		st.ScannedSession = 0
		st.FailedSession = 0
		st.UnreadableSession = 0
		st.ProjectIndex = 0
		st.NextSweepAt = time.Time{}
	})
}

func (s *Supervisor) record(res ScanResult, err error) {
	s.withStatus(func(st *ScanStatus) {
		st.ScannedSession += res.Scanned
		st.FailedSession += res.Failed
		st.UnreadableSession += res.Unreadable
		st.ScannedTotal += res.Scanned
		st.Remaining = res.Remaining
		st.Skipped = res.Skipped
		st.Eligible = res.Eligible
		st.LastPassAt = time.Now().UTC()
		if err != nil {
			st.LastError = err.Error()
		} else if res.Failed == 0 {
			st.LastError = ""
		}
	})
}

// finishSweep closes one cycle: the count the tab shows, the durable summary,
// and when the next sweep is due.
func (s *Supervisor) finishSweep(ctx context.Context) {
	nodes := -1
	if s.deps.Counter != nil {
		if n, err := s.deps.Counter.CountBrainNodes(ctx, ""); err == nil {
			nodes = n
		}
	}

	s.withStatus(func(st *ScanStatus) {
		st.Sweeps++
		st.Phase = PhaseIdle
		st.Project = ""
		st.ProjectLabel = ""
		st.LastSweepEnded = time.Now().UTC()
		st.NextSweepAt = st.LastSweepEnded.Add(s.cfg.BrainScanIdleInterval)
		if nodes >= 0 {
			st.NodesTotal = nodes
		}
	})
	st := s.Status()
	s.emit(EventSweep, "", fmt.Sprintf("tur bitti — %d damıtıldı, %d düğüm; sıradaki tur %s",
		st.ScannedSession, st.NodesTotal, s.cfg.BrainScanIdleInterval))
	s.remember(ctx, "")
}

// remember writes the durable summary. Once per project rather than once per
// pass: the writes stay bounded, and a crash costs at most one project's worth
// of already-hashed files, which re-scan for free.
func (s *Supervisor) remember(ctx context.Context, project string) {
	if s.deps.Cursors == nil {
		return
	}
	st := s.Status()
	blob, err := json.Marshal(scanCursor{
		ScannedTotal:   st.ScannedTotal,
		Sweeps:         st.Sweeps,
		LastSweepEnded: st.LastSweepEnded.Unix(),
		LastProject:    project,
	})
	if err != nil {
		return
	}
	if err := s.deps.Cursors.SetBrainCursor(ctx, scanCursorKey, "", string(blob)); err != nil {
		s.log.Debug("brain scan cursor not written", "error", err)
	}
}

// restore reads back what earlier runs did, so the tab does not report a fresh
// machine after every restart. A missing or unreadable row starts at zero,
// which is what a fresh install looks like — the store's posture everywhere.
func (s *Supervisor) restore(ctx context.Context) {
	if s.deps.Cursors == nil {
		return
	}
	raw, err := s.deps.Cursors.BrainCursor(ctx, scanCursorKey)
	if err != nil || raw == "" {
		return
	}
	var cur scanCursor
	if err := json.Unmarshal([]byte(raw), &cur); err != nil {
		return
	}
	s.withStatus(func(st *ScanStatus) {
		st.ScannedTotal = cur.ScannedTotal
		st.Sweeps = cur.Sweeps
		if cur.LastSweepEnded > 0 {
			st.LastSweepEnded = time.Unix(cur.LastSweepEnded, 0).UTC()
		}
	})
}

// compile-time assertion that a provider is a probe, so a signature change in
// internal/llm is caught here rather than in cmd/.
var _ HealthProbe = (llm.Provider)(nil)
