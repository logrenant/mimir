package coderunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/logrenant/mimir/internal/agents"
	"github.com/logrenant/mimir/internal/events"
	"github.com/logrenant/mimir/internal/store"
)

// The executor seam.
//
// This package owns the only queue in Mimir, and that is a rule rather than an
// accident: pump() is the single place a job begins, so there is one dispatch
// order, one restart reconciliation, and one status vocabulary. A second
// scheduler beside it would mean a second board state mirrored from this one,
// with no compare-and-swap to win the race between them — which is precisely
// what every CAS in internal/store/runs.go exists to avoid.
//
// So a new kind of work does not get its own runner. It gets an Executor, and
// the dispatcher already knows how to queue it, hold it, reconcile it after a
// crash and stream it.
//
// The seam is cut *beside* execute() rather than through it. execute() is the
// claude path — its stop semantics, its process-group signalling and its
// rate-limit park are load-bearing and were arrived at by trial — and rewriting
// it to fit an interface would put the riskiest code in this repository inside
// a refactor whose whole point is that nothing about it changes. What an
// Executor gets instead is the same bookkeeping through Sink, and the runner
// keeps the transcript, the sequencing and the terminal record.

// ErrUnknownAgent means a row names a sub-agent this binary has no executor
// for. It ends the card rather than leaving it queued: a row nothing can claim
// would be picked up on every pump for ever, and the operator would watch a
// card that never starts and never says why.
var ErrUnknownAgent = errors.New("coderunner: no executor for this agent")

// Lane is how capacity is measured for one kind of work.
//
// It is a property of the executor, never of a row: what a job costs is
// decided by what runs it. A row cannot ask to be cheap.
type Lane int

const (
	// LaneAccount spends one claude credential slot for the life of the job.
	// Capacity is therefore "one run per identity" — not a tuning number, but
	// the fact that two runs sharing one login share its rate limit and its
	// session state.
	LaneAccount Lane = iota

	// LaneWorker spends a permit from a fixed in-process pool and no identity
	// at all. There is nothing for such a job to be signed out of and no token
	// budget for it to exhaust, so gating it on a connected account would
	// refuse work for a reason that does not apply to it.
	LaneWorker
)

// Executor is one kind of work the dispatcher can start.
type Executor interface {
	// Agent is the sub-agent name this executor answers to. It matches the
	// row's agent column, and it is the only thing dispatch routes on.
	Agent() string
	Lane() Lane

	// Prepare is everything that must hold before a row is claimed. A non-nil
	// error refuses the job, and the refusal lands on the card by the same
	// claim-then-finish path an unresolvable project takes.
	Prepare(ctx context.Context, row store.RunRow) error

	// Execute owns one job from claim to terminal outcome. It returns no
	// error, for the same reason execute() does not: by the time it runs there
	// is no caller left to return to, and the outcome is the report.
	Execute(ctx context.Context, job Job) Outcome
}

// ModelNamer is an executor that knows which model its card will actually
// spend. Optional, and checked with a type assertion rather than added to
// Executor, because most executors do not: a claude session's model *is* the
// row's model column, which is what the runner announces when nobody answers.
//
// It exists because that column is not the answer on the worker lane. A catalog
// card spends the daemon's own model — the card's pair, or the operator's saved
// one — and the run's opening line used to name the coding model instead, so
// the terminal said `claude-sonnet-5` for a pass that spent `qwen3:8b`. An
// empty answer means "I have nothing better", and the column is used.
type ModelNamer interface {
	ModelFor(row store.RunRow) string
}

// Job is everything an executor is handed.
type Job struct {
	Row store.RunRow
	// Skill is the composed body of every skill the agent requires, already
	// through the gate. An executor never resolves its own: the mandate is
	// checked in one place or it is not a mandate.
	Skill        string
	SkillVersion string
	Out          Sink
}

// Sink is the one way a job speaks.
//
// An interface rather than a bus handle so an executor cannot stamp its own
// sequence number, skip the transcript, or publish after the terminal event —
// the three ways this stream has broken before. The runner implements it over
// the same (state, transcript, bus) triple execute() already owns, so both
// paths produce the same event shapes and the desktop renders them with the
// code it already has.
type Sink interface {
	// Started announces the run. Model and session may be empty for an
	// executor that has neither.
	Started(model, sessionID string)
	// Say emits a line of prose, as a text delta.
	Step(name string, args any) func(ok bool, output string)
	Say(text string)
	Stderr(text string)
}

// Outcome is how a job ended, in the vocabulary the runner records.
type Outcome struct {
	// Status is store.RunStatusCompleted or store.RunStatusFailed.
	Status    string
	Err       error
	SessionID string
	Model     string
	CostUSD   float64
	NumTurns  int
	// ParkUntil asks for the rate-limit treatment rather than a failure: the
	// row goes back to `queued` with its reason on it, the lane's pause is
	// armed, and the wake-up restarts the queue by itself.
	//
	// Only an executor that spends a token budget ever sets it. That is not the
	// same as holding a credential slot, which is what this comment used to say
	// and what task-89 corrected: a worker-lane job holds no slot but does
	// spend the daemon's own model identity, and that identity runs out. The
	// pause for every worker job in the process is one, under WorkerSlot.
	ParkUntil time.Time
}

// Registered returns the executors this runner can dispatch to, for tests and
// for diagnostics.
func (r *Runner) Registered() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.executors))
	for name := range r.executors {
		out = append(out, name)
	}
	return out
}

// Register adds an executor for one sub-agent. Called at wiring time, before
// Resume: an executor registered after the queue has been pumped would leave
// its own rows refused as unknown.
//
// The claude agents need no registration — they are this package's own path,
// and the dispatcher falls back to it for any agent the registry says is
// ExecClaude.
func (r *Runner) Register(ex Executor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.executors == nil {
		r.executors = map[string]Executor{}
	}
	r.executors[ex.Agent()] = ex
}

// executorFor resolves a row's executor. The second return says whether this
// job is the built-in claude path, which has no Executor value because it is
// not behind the seam.
func (r *Runner) executorFor(agentName string) (ex Executor, builtin bool, err error) {
	a := agentFor(agentName)
	if a.Exec == agents.ExecClaude {
		return nil, true, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	ex, ok := r.executors[a.Key]
	if !ok {
		return nil, false, errors.New("coderunner: " + a.Key + ": " + ErrUnknownAgent.Error())
	}
	return ex, false, nil
}

// laneOf is what the dispatcher asks before it looks for capacity.
//
// It reads the agent registry rather than the executor map, so a row whose
// executor is not registered is still known to want no account — which is what
// keeps ErrUnknownAgent a refusal on the card rather than a card stuck behind
// a credential it never needed.
func (r *Runner) laneOf(agentName string) Lane {
	if agentFor(agentName).Exec == agents.ExecClaude {
		return LaneAccount
	}
	return LaneWorker
}

// grant is the capacity one job holds while it runs.
//
// One type rather than two maps, because "which accounts are busy" and "how
// many workers are busy" are the same question asked of the same set. The lane
// field is what disambiguates them: defaultAccountID is the empty string, so
// filtering on "no account id" alone would confuse a worker with a runner built
// against no account registry at all.
type grant struct {
	lane      Lane
	agent     string
	accountID string
	configDir string
}

// runSink is the runner's own Sink: the same sequencer, the same transcript and
// the same bus execute() writes through.
type runSink struct {
	r          *Runner
	st         *state
	transcript *os.File
}

func (s *runSink) publish(ev events.Event) {
	writeTranscript(s.transcript, ev)
	s.r.bus.Publish(ev)
}

func (s *runSink) Started(model, sessionID string) {
	s.st.model = model
	s.st.sessionID = sessionID
	ev := s.st.next(events.KindRunStarted, time.Now().UTC())
	ev.Model = model
	ev.SessionID = sessionID
	s.publish(ev)
}

func (s *runSink) Say(text string) {
	if text == "" {
		return
	}
	ev := s.st.next(events.KindTextDelta, time.Now().UTC())
	ev.Text = text
	s.publish(ev)
}

func (s *runSink) Stderr(text string) {
	if text == "" {
		return
	}
	ev := s.st.next(events.KindStderr, time.Now().UTC())
	ev.Text = text
	s.publish(ev)
}

// Step announces a stage and returns the function that closes it. The pair
// rather than two calls, so a stage cannot be opened and left open: the
// returned closure is the only way to report the result, and it carries the
// name the call already knows.
func (s *runSink) Step(name string, args any) func(ok bool, output string) {
	callID := name + "-" + strconv.FormatInt(s.st.seq+1, 10)

	ev := s.st.next(events.KindToolCall, time.Now().UTC())
	ev.ToolName = name
	ev.CallID = callID
	// Read, always: a stage of a Go pipeline neither writes to the operator's
	// disk nor spends their tokens, and the desktop colours this field.
	ev.Risk = "read"
	if args != nil {
		if raw, err := json.Marshal(args); err == nil {
			ev.Args = raw
		}
	}
	s.publish(ev)

	return func(ok bool, output string) {
		done := s.st.next(events.KindToolResult, time.Now().UTC())
		done.ToolName = name
		done.CallID = callID
		done.Output = output
		done.OK = &ok
		s.publish(done)
	}
}

// runExecutor is the worker-lane counterpart of execute().
//
// It keeps the same skeleton — transcript opened here, inflight cleared here,
// bus closed here, terminal outcome recorded through the same finish/record
// helpers — so a job on either lane leaves the same trail. What it does not
// have is the whole of the subprocess: no process group, no SIGINT dance, no
// stream parser, no rate-limit sniffing. Those belong to the CLI, and an
// executor that has none of them should not carry the code for them.
func (r *Runner) runExecutor(ctx context.Context, h *inflight, row store.RunRow,
	ex Executor, skillBody, skillVersion string) {
	defer h.cancel()
	defer r.bus.CloseRun(row.ID)
	defer func() {
		r.mu.Lock()
		delete(r.inflight, row.ID)
		r.mu.Unlock()
	}()

	st := newState(row.ID)

	// Same reason as execute(): the directory is created when the card is
	// written down, but a job can be claimed long afterwards, from a queue
	// that survived a restart into a process that has not created it yet.
	if dir := filepath.Dir(row.TranscriptPath); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	transcript, err := os.OpenFile(row.TranscriptPath,
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		r.finish(row, st, fmt.Errorf("opening transcript: %w", err), nil)
		return
	}
	defer func() { _ = transcript.Close() }()

	sink := &runSink{r: r, st: st, transcript: transcript}
	sink.Started(startingModel(ex, row), row.SessionID)
	slog.Info("job is bound to its agent's skills",
		"run_id", row.ID, "agent", row.Agent, "skills", skillVersion)

	out := ex.Execute(ctx, Job{
		Row:          row,
		Skill:        skillBody,
		SkillVersion: skillVersion,
		Out:          sink,
	})

	stopped := func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return h.stopped
	}

	// A stop reaches a worker job the only way it can: the context is
	// cancelled and the executor returns. Reporting that as a failure would
	// send the operator to fix something they had just chosen to do.
	if stopped() {
		r.stop(row, st, nil, transcript)
		return
	}

	if out.SessionID != "" {
		st.sessionID = out.SessionID
	}
	if out.Model != "" {
		st.model = out.Model
	}

	if out.Status == store.RunStatusFailed || out.Err != nil {
		cause := out.Err
		if cause == nil {
			cause = errors.New("the job failed without saying why")
		}
		// A pause rather than a failure, when the executor asked for one. The
		// distinction is the whole of it: a failed card is something to go and
		// fix, and a parked one is something that resumes on its own — and the
		// operator cannot tell them apart from a status alone.
		if until := out.ParkUntil; !until.IsZero() && until.After(time.Now()) {
			r.park(row, st, cause.Error(), until.UTC(), transcript)
			return
		}
		r.finish(row, st, cause, transcript)
		return
	}

	ev := st.next(events.KindRunCompleted, time.Now().UTC())
	ev.SessionID = st.sessionID
	ev.Model = st.model
	ev.CostUSD = out.CostUSD
	ev.NumTurns = out.NumTurns
	writeTranscript(transcript, ev)
	r.bus.Publish(ev)
	r.record(row, ev)
}

// startingModel is what the run's opening event names: the executor's own
// answer when it has one, and the row's column when it does not.
func startingModel(ex Executor, row store.RunRow) string {
	if namer, ok := ex.(ModelNamer); ok {
		if model := namer.ModelFor(row); model != "" {
			return model
		}
	}
	return row.Model
}
