// Package coderunner runs a `claude` coding session inside one registered
// project directory and reports what it does as it happens.
//
// This is a second, deliberately different invocation profile from
// internal/refine. The refiner is headless, tool-less and --restricted because
// it handles untrusted scraped text; this runner has file tools enabled
// because it does work in the operator's own repo. What keeps that safe is not
// the flags but the directory: internal/project decided it, and both cmd.Dir
// and --add-dir point at exactly that path.
//
// It is also the only thing in Mimir that starts a coding run. A task can be
// written down (Create), released (Enqueue), claimed by the dispatcher, and
// cancelled (Stop) — but every one of those paths ends at the same execute(),
// and nothing outside this package decides that a subprocess may begin. That is
// the single-engine rule from docs/ROADMAP.md §B.2.1, and the reason the queue
// added here is a semaphore on this type rather than a second scheduler beside
// it.
package coderunner

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/logrenant/mimir/internal/account"
	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/events"
	"github.com/logrenant/mimir/internal/project"
	"github.com/logrenant/mimir/internal/store"
)

// ErrClaudeUnavailable means the `claude` CLI could not be started at all —
// missing, not on PATH, or not authenticated. Distinct from a run that started
// and then failed, because the fix is different (SD-6).
var ErrClaudeUnavailable = errors.New("coderunner: claude CLI unavailable")

// ErrRunNotFound means no run has that id. A sentinel because callers across a
// transport boundary have to tell "you asked for something that does not
// exist" (404) apart from "the store broke" (500).
var ErrRunNotFound = errors.New("coderunner: no such run")

// ErrNotStoppable means the run is in a state a stop cannot act on — already
// finished, or never released. A conflict, not a failure: the operator's view
// was simply a moment out of date.
var ErrNotStoppable = errors.New("coderunner: this run cannot be stopped")

// ErrUnknownModel means the task named a model that is not on the offer list.
// Refused at create time so the operator hears about it while they are still
// looking at the form, instead of three seconds into a run that dies.
var ErrUnknownModel = errors.New("coderunner: unknown model")

// ErrNotDeletable means the run is queued or in flight. Deleting it would leave
// a subprocess writing to a transcript nothing refers to, so the operator is
// asked to stop it first.
var ErrNotDeletable = errors.New("coderunner: stop this run before deleting it")

// ErrNotRetryable means the run is not in a state a retry acts on. A conflict
// like ErrNotStoppable: only a run that failed or was stopped has something
// left to pick up.
var ErrNotRetryable = errors.New("coderunner: this run cannot be retried")

// ErrNotEditable means the card is running or finished. A run that is spending
// or has spent tokens keeps the prompt it was given: that text is the record of
// what was asked, and rewriting it would make the transcript answer a question
// nobody posed.
var ErrNotEditable = errors.New("coderunner: this run cannot be edited")

// defaultAccountID is the slot a run uses when this runner was built with no
// account registry at all: the CLI's own, reached by not setting the variable.
//
// It is only ever reached by a wiring that has no accounts — the daemon always
// passes one. With a registry present, no connected account means no capacity,
// not a quiet fall back to whichever identity the daemon inherited.
const defaultAccountID = ""

// maxToolOutput caps how much of a tool result is carried in an event. Tool
// output can be an entire file; the watcher wants a readable preview, and the
// full text is in the transcript. A package constant, not a knob (SD-1).
const maxToolOutput = 4000

// restartReason is what an orphaned row is failed with. Written as a sentence
// the operator reads on a card, because that card is where the question "why is
// this not running?" is actually asked.
const restartReason = "the daemon restarted while this run was in flight"

// maxResumeCause bounds how much of the previous failure is repeated to a
// resumed session. The reason is usually one line; a CLI that died noisily can
// leave twenty of stderr behind, and none of it is the task.
const maxResumeCause = 300

// Run is one coding session, at whatever point of its life it has reached.
type Run struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Title     string `json:"title,omitempty"`
	Prompt    string `json:"prompt"`
	Status    string `json:"status"`
	SessionID string `json:"session_id,omitempty"`
	Model     string `json:"model,omitempty"`
	// RequestedAccountID is the slot the operator pinned, empty for "any free
	// one". AccountID is the slot it actually ran on.
	RequestedAccountID string    `json:"requested_account_id,omitempty"`
	AccountID          string    `json:"account_id,omitempty"`
	Attachments        []string  `json:"attachments,omitempty"`
	CostUSD            float64   `json:"cost_usd,omitempty"`
	NumTurns           int       `json:"num_turns,omitempty"`
	Error              string    `json:"error,omitempty"`
	CreatedAt          time.Time `json:"created_at,omitempty"`
	QueuedAt           time.Time `json:"queued_at,omitempty"`
	StartedAt          time.Time `json:"started_at,omitempty"`
	EndedAt            time.Time `json:"ended_at,omitempty"`
}

// CreateRequest is everything a task needs to exist. One struct rather than a
// widening argument list, because Create and Start take exactly the same thing
// and differ only in whether they release it.
type CreateRequest struct {
	ProjectID string
	Title     string
	Prompt    string
	// AccountID pins the run to one credential slot. Empty means the
	// dispatcher assigns the first free one, which is what makes a second
	// account worth having.
	AccountID string
	// Model is one of config.CodingModels' IDs. Empty means the daemon's
	// default, which is what every client written before the picker sends.
	Model         string
	AttachmentIDs []string
}

// ProjectResolver hands back a directory that has passed every guard. Taking
// the interface rather than *project.Registry keeps the runner testable, but
// note the contract: an implementation MUST validate, not just look up.
type ProjectResolver interface {
	Resolve(ctx context.Context, id string) (project.Project, error)
}

// AccountRegistry is where credential slots come from. *account.Registry
// satisfies it. Taking the interface keeps this package testable without a
// keychain.
type AccountRegistry interface {
	List(ctx context.Context) ([]account.Account, error)
	Get(ctx context.Context, id string) (account.Account, error)
	Touch(ctx context.Context, id string, at time.Time) error
}

// RunStore persists run records.
type RunStore interface {
	InsertRun(ctx context.Context, r store.RunRow) error
	UpdateRunResult(ctx context.Context, r store.RunRow) error
	GetRun(ctx context.Context, id string) (store.RunRow, bool, error)
	ListRunsByProject(ctx context.Context, projectID string, limit int) ([]store.RunRow, error)
	UpdateRunStatus(ctx context.Context, id, from, to string, queuedAt, endedAt time.Time, runErr string) (bool, error)
	EditRun(ctx context.Context, id string, e store.RunEdit) (bool, error)
	RequeueRun(ctx context.Context, id string, at time.Time, keepSession bool) (bool, error)
	ListQueuedRuns(ctx context.Context, limit int) ([]store.RunRow, error)
	ClaimRun(ctx context.Context, id, accountID string, at time.Time) (bool, error)
	ReconcileRunningRuns(ctx context.Context, reason string, at time.Time) (int, error)
	DeleteRun(ctx context.Context, id string) error
	ReferencedAttachments(ctx context.Context) ([]string, error)

	// The queue's own log, and the transition only it needs. A run parked by a
	// spent token budget goes from `running` straight back to `queued`, which
	// is neither a retry (RequeueRun starts from a finished row) nor a status
	// move (UpdateRunStatus cannot carry the session id the resume needs).
	ParkRun(ctx context.Context, id, sessionID string, at time.Time, reason string) (bool, error)
	InsertRateLimitEvent(ctx context.Context, e store.RateLimitRow) error
	ListRateLimitEvents(ctx context.Context, limit int) ([]store.RateLimitRow, error)
}

// inflight is the handle on a run this process is currently executing.
type inflight struct {
	cancel    context.CancelFunc
	accountID string
	stopped   bool
}

// Runner starts and supervises coding sessions.
type Runner struct {
	base     context.Context
	cfg      config.Config
	bus      *events.Bus
	projects ProjectResolver
	accounts AccountRegistry
	runs     RunStore
	wg       sync.WaitGroup

	// dispatch serialises pump. Capacity is not a number here: it is one run
	// per credential slot, and the slots are whatever the registry holds. A
	// counting semaphore cannot express that, so the check is "is this
	// account busy?" against inflight, under this lock.
	dispatch sync.Mutex

	mu       sync.Mutex
	inflight map[string]*inflight

	// The credential slots that have nothing left to spend, and the wake-ups
	// that end their pauses. See ratelimit.go: this is what makes a spent
	// token budget a pause the pipeline lifts by itself rather than a failure
	// somebody has to notice.
	limitsMu sync.Mutex
	held     map[string]*spentBudget
}

// New returns a Runner whose in-flight runs live until base is cancelled.
//
// base is an explicit parameter because a run must outlive the HTTP request
// that started it — deriving the run's context from the request would kill it
// the moment the response was written. Taking the lifetime from the caller is
// also what keeps this package free of a rootless context, which
// tools/lint/check_context.sh bans under internal/.
func New(base context.Context, cfg config.Config, bus *events.Bus,
	projects ProjectResolver, accounts AccountRegistry, runs RunStore) *Runner {
	return &Runner{
		base:     base,
		cfg:      cfg,
		bus:      bus,
		projects: projects,
		accounts: accounts,
		runs:     runs,
		inflight: map[string]*inflight{},
		held:     map[string]*spentBudget{},
	}
}

func newRunID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("coderunner: generating run id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// Resume reconciles the store against reality and then starts whatever the
// queue already holds.
//
// Called once, at daemon startup, before the HTTP surface is serving. The
// reconcile has to come first: a `running` row at startup is by definition
// orphaned — this process owns no subprocess yet — and dispatching before
// clearing them would let a restarted daemon report more work in flight than it
// is doing. Queued rows are the durable queue and are picked up as they are.
func (r *Runner) Resume(ctx context.Context) error {
	n, err := r.runs.ReconcileRunningRuns(ctx, restartReason, time.Now().UTC())
	if err != nil {
		return err
	}
	if n > 0 {
		slog.Warn("failed runs orphaned by a restart", "count", n)
	}

	// Before the pump, for the same reason the reconcile is: a budget spent
	// before the restart is still spent after it, and dispatching into one
	// would burn a CLI invocation rediscovering what the log already knows.
	r.restoreHolds(ctx)

	if referenced, err := r.runs.ReferencedAttachments(ctx); err == nil {
		if removed, err := r.SweepAttachments(referenced, time.Now()); err != nil {
			slog.Warn("sweeping attachments", "error", err)
		} else if removed > 0 {
			slog.Info("removed unreferenced attachments", "count", removed)
		}
	}

	r.pump()
	return nil
}

// Create records a task without running it. Nothing is spawned and no slot is
// taken: this is the operator writing something down.
func (r *Runner) Create(ctx context.Context, req CreateRequest) (Run, error) {
	row, err := r.insert(ctx, req, store.RunStatusBacklog)
	if err != nil {
		return Run{}, err
	}
	return runFromRow(row), nil
}

// Start records a task and releases it in one call — the quick-task and
// coding-runner path, where writing it down and meaning it are the same act.
func (r *Runner) Start(ctx context.Context, req CreateRequest) (Run, error) {
	row, err := r.insert(ctx, req, store.RunStatusQueued)
	if err != nil {
		return Run{}, err
	}
	r.pump()

	// Re-read rather than report the row as inserted: pump may have claimed it
	// already, and a caller told "queued" for a run that is running has to poll
	// to discover something this call already knows. If the read fails the
	// inserted row is still true — it just describes a moment ago.
	if run, err := r.Get(ctx, row.ID); err == nil {
		return run, nil
	}
	return runFromRow(row), nil
}

// insert is the shared half of Create and Start: validate, resolve, allocate
// the transcript, write the row.
func (r *Runner) insert(ctx context.Context, req CreateRequest, status string) (store.RunRow, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		return store.RunRow{}, errors.New("coderunner: prompt is empty")
	}

	// Resolve, never Get: this re-runs every path guard against the filesystem
	// as it is now. It is the only way this package learns a directory.
	proj, err := r.projects.Resolve(ctx, req.ProjectID)
	if err != nil {
		return store.RunRow{}, err
	}

	if err := r.requireSlot(ctx, req.AccountID); err != nil {
		return store.RunRow{}, err
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = r.cfg.CodingModel
	} else if !r.cfg.HasCodingModel(model) {
		return store.RunRow{}, fmt.Errorf("%w: %s", ErrUnknownModel, model)
	}

	runID, err := newRunID()
	if err != nil {
		return store.RunRow{}, err
	}

	transcriptPath := filepath.Join(r.cfg.TranscriptDir, runID+".jsonl")
	if err := os.MkdirAll(r.cfg.TranscriptDir, 0o755); err != nil {
		return store.RunRow{}, fmt.Errorf("coderunner: creating transcript directory: %w", err)
	}

	attachments, err := encodeAttachmentIDs(req.AttachmentIDs)
	if err != nil {
		return store.RunRow{}, err
	}

	now := time.Now().UTC()
	row := store.RunRow{
		ID:                 runID,
		ProjectID:          proj.ID,
		Title:              strings.TrimSpace(req.Title),
		Prompt:             req.Prompt,
		Status:             status,
		Model:              model,
		RequestedAccountID: req.AccountID,
		TranscriptPath:     transcriptPath,
		Attachments:        attachments,
		CreatedAt:          now,
	}
	if status == store.RunStatusQueued {
		row.QueuedAt = now
	}
	// Recorded before anything is dispatched, so a run is never in flight
	// without a row to find it by.
	if err := r.runs.InsertRun(ctx, row); err != nil {
		return store.RunRow{}, err
	}
	return row, nil
}

// EditRequest is a change to a card, field by field. A nil field is one the
// operator did not touch, which is what makes this a patch rather than a
// replacement — a client that only renames a card must not have to send the
// prompt back, and a client that never learned about a field must not blank it.
type EditRequest struct {
	Title         *string
	Prompt        *string
	Model         *string
	AttachmentIDs *[]string
}

// Edit rewrites what a card asks for.
//
// Only while nothing has been spent on it — see store.EditableStatuses. The
// guard is a compare-and-swap in the store rather than a check here, because
// the dispatcher can claim a queued run between the read and the write, and an
// edit that landed on a run already talking to the CLI would change the prompt
// out from under it.
//
// Validation is the same as a create's, deliberately: an edited card is a card,
// and a prompt emptied by an edit or a model that is no longer on the offer
// list would fail at dispatch time instead of here, in front of the operator.
func (r *Runner) Edit(ctx context.Context, runID string, req EditRequest) (Run, error) {
	row, err := r.row(ctx, runID)
	if err != nil {
		return Run{}, err
	}

	edit := store.RunEdit{
		Title:       row.Title,
		Prompt:      row.Prompt,
		Model:       row.Model,
		Attachments: row.Attachments,
	}
	if req.Title != nil {
		edit.Title = strings.TrimSpace(*req.Title)
	}
	if req.Prompt != nil {
		edit.Prompt = strings.TrimSpace(*req.Prompt)
	}
	if edit.Prompt == "" {
		return Run{}, errors.New("coderunner: prompt is empty")
	}
	if req.Model != nil {
		model := strings.TrimSpace(*req.Model)
		if model == "" {
			model = r.cfg.CodingModel
		} else if !r.cfg.HasCodingModel(model) {
			return Run{}, fmt.Errorf("%w: %s", ErrUnknownModel, model)
		}
		edit.Model = model
	}
	if req.AttachmentIDs != nil {
		// Every id is resolved before the row is written: an edit that
		// attached an image the daemon does not have would leave a card
		// promising a picture the CLI is never handed.
		for _, id := range *req.AttachmentIDs {
			if _, err := r.statAttachment(id); err != nil {
				return Run{}, err
			}
		}
		encoded, err := encodeAttachmentIDs(*req.AttachmentIDs)
		if err != nil {
			return Run{}, err
		}
		edit.Attachments = encoded
	}

	moved, err := r.runs.EditRun(ctx, runID, edit)
	if err != nil {
		return Run{}, err
	}
	if !moved {
		return Run{}, fmt.Errorf("%w: it is %s", ErrNotEditable, row.Status)
	}

	// An image dropped by an edit is an image nothing points at any more, and
	// the sweep only runs at startup — so it is collected here, the same way
	// Delete collects a card's own.
	if req.AttachmentIDs != nil {
		r.sweepDropped(ctx, decodeAttachmentIDs(row.Attachments))
	}
	return r.Get(ctx, runID)
}

// sweepDropped removes uploads from a set that no run refers to any more.
func (r *Runner) sweepDropped(ctx context.Context, candidates []string) {
	keep := map[string]struct{}{}
	referenced, err := r.runs.ReferencedAttachments(ctx)
	if err != nil {
		// Not knowing what is still referenced is a reason to keep the files,
		// not to delete them: an orphaned image costs disk, a deleted one that
		// something still points at costs the operator their picture.
		return
	}
	for _, raw := range referenced {
		for _, id := range decodeAttachmentIDs(raw) {
			keep[id] = struct{}{}
		}
	}
	r.removeAttachments(candidates, keep)
}

// requireSlot refuses to release work there is no identity to spend.
//
// Mimir spends one Claude account and signs it out when the app closes, so
// "nothing connected" is a normal state rather than a broken one — and a task
// released into it would sit in the queue with nothing able to claim it. Every
// path that puts a run in the queue asks this first, while the operator is
// still looking at the thing they pressed: a card that silently waits forever
// is the one failure this package must not produce.
//
// A pin to an account that is not there is refused for the same reason — the
// dispatcher would never find a slot matching it.
func (r *Runner) requireSlot(ctx context.Context, accountID string) error {
	if r.accounts == nil {
		if accountID != "" {
			return fmt.Errorf("%w: %s", account.ErrAccountNotFound, accountID)
		}
		return nil
	}
	connected, err := r.accounts.List(ctx)
	if err != nil {
		return err
	}
	if len(connected) == 0 {
		return account.ErrNotConnected
	}
	if accountID != "" {
		if _, err := r.accounts.Get(ctx, accountID); err != nil {
			return err
		}
	}
	return nil
}

// Kick asks the dispatcher to look at the queue again.
//
// Nothing else calls pump when an account *appears*: the queue is pumped when
// work is released and when a run frees its slot, so a queue that filled up
// while nothing was connected would keep waiting after the login that could
// drain it. This is that missing edge — the daemon calls it when a login lands,
// and the board offers it as a button on a card that has been queued too long.
//
// The connection check is the point as much as the pump is: an operator asking
// why nothing starts deserves "no account is connected" rather than silence.
func (r *Runner) Kick(ctx context.Context) error {
	if err := r.requireSlot(ctx, ""); err != nil {
		return err
	}
	r.pump()

	// Pumped first, then answered: the pump is what the caller asked for, and
	// a pause that expired a second ago is lifted by it. What is left is a
	// queue that genuinely cannot move, and saying so — with the time it moves
	// again — is the same courtesy the connection check above pays.
	now := time.Now().UTC()
	slots := r.slots(ctx)
	if held, soonest := r.heldSlots(slots, now); len(held) > 0 && len(held) == len(slots) {
		return fmt.Errorf("%w — the queue restarts by itself at %s",
			ErrBudgetSpent, soonest.Local().Format("15:04"))
	}
	return nil
}

// Enqueue releases a backlog task to the dispatcher.
//
// The from-state guard is in the store, so two clicks on the same card resolve
// to one enqueue and one "not in the backlog any more" rather than two runs.
func (r *Runner) Enqueue(ctx context.Context, runID string) (Run, error) {
	row, err := r.row(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	if err := r.requireSlot(ctx, ""); err != nil {
		return Run{}, err
	}
	moved, err := r.runs.UpdateRunStatus(ctx, runID,
		store.RunStatusBacklog, store.RunStatusQueued,
		time.Now().UTC(), time.Time{}, "")
	if err != nil {
		return Run{}, err
	}
	if !moved {
		return Run{}, fmt.Errorf("coderunner: run %s is %s, not in the backlog", runID, row.Status)
	}
	r.pump()
	return r.Get(ctx, runID)
}

// Retry puts a finished run back in the queue so the work can carry on.
//
// Only a `failed` or `stopped` run: a completed one has nothing left to pick
// up, and a backlog card is Enqueue's business. The row keeps its session id,
// so when the dispatcher claims it again the CLI is resumed rather than started
// — the difference between continuing a task and doing it twice.
//
// fresh drops that session instead. It is not a preference but an escape: a
// session the CLI can no longer resume (its config directory was reset, say)
// would otherwise fail on every retry, with the same card and the same reason.
func (r *Runner) Retry(ctx context.Context, runID string, fresh bool) (Run, error) {
	row, err := r.row(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	// Asked before the row moves. A retry with nothing connected used to land
	// the card in Queued and leave it there, which reads as the retry button
	// being broken rather than as a missing login.
	if err := r.requireSlot(ctx, ""); err != nil {
		return Run{}, err
	}
	moved, err := r.runs.RequeueRun(ctx, runID, time.Now().UTC(), !fresh)
	if err != nil {
		return Run{}, err
	}
	if !moved {
		return Run{}, fmt.Errorf("%w: it is %s", ErrNotRetryable, row.Status)
	}
	r.pump()
	return r.Get(ctx, runID)
}

// Stop cancels a run, whatever stage it has reached.
//
// A queued run goes back to the backlog: it never started, so failing it would
// invent a failure. A running one is interrupted — see execute, which gives the
// CLI CodingStopGrace to write its own result line before the kill, so the cost
// and turn count of the work already done are not thrown away.
func (r *Runner) Stop(ctx context.Context, runID string) (Run, error) {
	row, err := r.row(ctx, runID)
	if err != nil {
		return Run{}, err
	}

	switch row.Status {
	case store.RunStatusQueued:
		moved, err := r.runs.UpdateRunStatus(ctx, runID,
			store.RunStatusQueued, store.RunStatusBacklog,
			time.Time{}, time.Time{}, "")
		if err != nil {
			return Run{}, err
		}
		if !moved {
			// It was claimed between the read and the write. Ask again, and
			// the running branch below will take it on the retry.
			return r.Stop(ctx, runID)
		}
		return r.Get(ctx, runID)

	case store.RunStatusRunning:
		r.mu.Lock()
		h, owned := r.inflight[runID]
		if owned {
			h.stopped = true
		}
		r.mu.Unlock()

		if owned {
			// The goroutine that owns the run records the outcome; cancelling
			// is the whole of the request.
			h.cancel()
			return r.Get(ctx, runID)
		}
		// Marked running but nothing here owns it. Reconcile should have
		// cleared this at startup; treat it as the stale row it is rather than
		// leaving the operator with a card they cannot act on.
		if _, err := r.runs.UpdateRunStatus(ctx, runID,
			store.RunStatusRunning, store.RunStatusStopped,
			row.QueuedAt, time.Now().UTC(), restartReason); err != nil {
			return Run{}, err
		}
		return r.Get(ctx, runID)

	default:
		return Run{}, fmt.Errorf("%w: it is %s", ErrNotStoppable, row.Status)
	}
}

// Delete removes a task and the files it owns. Only a task that is not going
// anywhere: stopping first is the operator's decision to make, not a side
// effect of deleting.
func (r *Runner) Delete(ctx context.Context, runID string) error {
	row, err := r.row(ctx, runID)
	if err != nil {
		return err
	}
	if row.Status == store.RunStatusRunning || row.Status == store.RunStatusQueued {
		return fmt.Errorf("%w: it is %s", ErrNotDeletable, row.Status)
	}

	if err := r.runs.DeleteRun(ctx, runID); err != nil {
		return err
	}
	if row.TranscriptPath != "" {
		_ = os.Remove(row.TranscriptPath)
	}

	// Whatever another run still points at survives; the row is gone, so that
	// listing no longer includes it.
	r.sweepDropped(ctx, decodeAttachmentIDs(row.Attachments))
	return nil
}

// pump starts every queued run whose account is free, and is the only place a
// run begins.
//
// It is called rather than looped: on release (Start, Enqueue), at startup
// (Resume), and by each run as it finishes and frees its slot. A loop would
// need a goroutine of its own with a lifetime to manage; this has neither and
// cannot be left running after the daemon is gone.
//
// Capacity is one run per credential slot, and Mimir has one — so one run at a
// time. That is not a tuning choice: two runs sharing one Claude Code identity
// share its rate limit and its session state, so the second is not throughput,
// it is contention.
func (r *Runner) pump() {
	// Serialised so two callers cannot both see the same slot free and both
	// claim against it. The claim itself is still a compare-and-swap in the
	// store, which is what makes this safe across a restart as well.
	r.dispatch.Lock()
	defer r.dispatch.Unlock()

	ctx, cancel := context.WithTimeout(r.base, 15*time.Second)
	defer cancel()

	for {
		if !r.dispatchOne(ctx) {
			return
		}
	}
}

// slots returns the credential slots runs may be dispatched to: the connected
// account, or nothing.
//
// Nothing is the honest answer when no account is connected. Falling back to
// the CLI's own slot here would mean a queue quietly draining through the
// operator's terminal login — the identity Mimir deliberately does not touch —
// and it would do so right after a reset had signed Mimir's own slot out.
func (r *Runner) slots(ctx context.Context) []account.Account {
	if r.accounts == nil {
		return []account.Account{{ID: defaultAccountID, Label: "Default"}}
	}
	accounts, err := r.accounts.List(ctx)
	if err != nil {
		slog.Warn("listing accounts", "error", err)
		return nil
	}
	return accounts
}

// busyAccounts is which slots this process is currently spending.
func (r *Runner) busyAccounts() map[string]struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()

	busy := make(map[string]struct{}, len(r.inflight))
	for _, h := range r.inflight {
		busy[h.accountID] = struct{}{}
	}
	return busy
}

// dispatchOne finds one queued run that can start now and starts it, reporting
// whether it did.
//
// The queue is walked rather than popped: a run pinned to a busy account must
// be stepped over, or one operator's long task would hold up every other
// account's work behind it.
func (r *Runner) dispatchOne(ctx context.Context) bool {
	slots := r.slots(ctx)
	if len(slots) == 0 {
		return false
	}
	busy := r.busyAccounts()
	now := time.Now().UTC()

	free := make([]account.Account, 0, len(slots))
	for _, a := range slots {
		if _, taken := busy[a.ID]; taken {
			continue
		}
		// A slot with no tokens left is not free. Claiming a run against one
		// would spend a CLI invocation to be told again what the last one was
		// told, and would land the operator a second failed-looking card. The
		// pause lifts itself — see releaseSlot.
		if _, out := r.heldUntil(a.ID, now); out {
			continue
		}
		free = append(free, a)
	}
	if len(free) == 0 {
		if held, _ := r.heldSlots(slots, now); len(held) > 0 {
			r.noteHeld(ctx, held)
		}
		return false
	}

	queued, err := r.runs.ListQueuedRuns(ctx, 100)
	if err != nil {
		if r.base.Err() == nil {
			slog.Warn("reading the queue", "error", err)
		}
		return false
	}

	for _, row := range queued {
		// A pin to a slot that no longer exists is not a pin. The account is
		// signed out on quit and comes back with a new id, so every pin
		// written before the last launch names something gone — honouring it
		// would strand the run in the queue for the life of the row.
		requested := row.RequestedAccountID
		if requested != "" && !known(slots, requested) {
			requested = ""
		}
		target, ok := pick(free, requested)
		if !ok {
			continue // pinned to a slot that is busy right now
		}
		if r.launch(ctx, row, target) {
			return true
		}
	}
	return false
}

// known reports whether a slot with that id exists at all, which is what
// separates "pinned to a busy account" from "pinned to an account that is gone".
func known(slots []account.Account, id string) bool {
	for _, a := range slots {
		if a.ID == id {
			return true
		}
	}
	return false
}

// pick chooses the slot a queued run may take: the one it is pinned to, or the
// first free one when it is not pinned.
func pick(free []account.Account, requested string) (account.Account, bool) {
	if requested == "" {
		return free[0], true
	}
	for _, a := range free {
		if a.ID == requested {
			return a, true
		}
	}
	return account.Account{}, false
}

// launch claims a run for a slot and starts it, reporting whether the claim
// won. Losing is ordinary: another dispatcher took it first.
func (r *Runner) launch(ctx context.Context, row store.RunRow, on account.Account) bool {
	proj, err := r.projects.Resolve(ctx, row.ProjectID)
	if err != nil {
		// The folder moved or was withdrawn between queueing and now. The run
		// is over before it started, and the reason is the useful part — but
		// only if we are the one that claimed it.
		if claimed, cerr := r.runs.ClaimRun(ctx, row.ID, on.ID, time.Now().UTC()); cerr != nil || !claimed {
			return false
		}
		row.AccountID = on.ID
		r.finish(row, newState(row.ID), err, nil)
		return true
	}

	claimed, err := r.runs.ClaimRun(ctx, row.ID, on.ID, time.Now().UTC())
	if err != nil {
		if r.base.Err() == nil {
			slog.Warn("claiming a queued run", "run_id", row.ID, "error", err)
		}
		return false
	}
	if !claimed {
		return false
	}

	row.Status = store.RunStatusRunning
	row.AccountID = on.ID
	row.StartedAt = time.Now().UTC()

	if r.accounts != nil && on.ID != defaultAccountID {
		if err := r.accounts.Touch(ctx, on.ID, row.StartedAt); err != nil {
			slog.Warn("touching an account", "account_id", on.ID, "error", err)
		}
	}

	// The slot is marked busy here, before the goroutine exists — not inside
	// execute. busyAccounts reads this map, and pump keeps calling dispatchOne
	// until it runs out of free slots: if the marking happened in the child,
	// the very next iteration would still see this account free and claim a
	// second run against the same identity. The dispatch mutex does not close
	// that window, because the window is between launch returning and the
	// child being scheduled.
	ctx, cancel := context.WithTimeout(r.base, r.cfg.CodingRunTimeout)
	h := &inflight{cancel: cancel, accountID: on.ID}
	r.mu.Lock()
	r.inflight[row.ID] = h
	r.mu.Unlock()

	r.wg.Add(1)
	go func() {
		// Ordered so the slot is released and the next run dispatched before
		// the wait group drops: Wait must not be able to return between one
		// run ending and the next being registered.
		defer r.wg.Done()
		defer r.pump()
		r.execute(ctx, h, row, proj.Path, on.ConfigDir)
	}()
	return true
}

// Wait blocks until every in-flight run has finished. For graceful shutdown,
// and what lets tests satisfy goleak.
//
// Deliberately not the place the pending rate-limit wake-ups are disarmed:
// Wait is also how callers join the runs in flight at an arbitrary moment, and
// cancelling the timer that restarts a paused queue there would make a
// synchronisation point silently change what the pipeline does next. What
// makes a late wake-up harmless is base — see releaseSlot.
func (r *Runner) Wait() {
	r.wg.Wait()
}

func (r *Runner) row(ctx context.Context, runID string) (store.RunRow, error) {
	row, found, err := r.runs.GetRun(ctx, runID)
	if err != nil {
		return store.RunRow{}, err
	}
	if !found {
		return store.RunRow{}, fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}
	return row, nil
}

func runFromRow(row store.RunRow) Run {
	return Run{
		ID:                 row.ID,
		ProjectID:          row.ProjectID,
		RequestedAccountID: row.RequestedAccountID,
		AccountID:          row.AccountID,
		Title:              row.Title,
		Prompt:             row.Prompt,
		Status:             row.Status,
		SessionID:          row.SessionID,
		Model:              row.Model,
		Attachments:        decodeAttachmentIDs(row.Attachments),
		CostUSD:            row.CostUSD,
		NumTurns:           row.NumTurns,
		Error:              row.Error,
		CreatedAt:          row.CreatedAt,
		QueuedAt:           row.QueuedAt,
		StartedAt:          row.StartedAt,
		EndedAt:            row.EndedAt,
	}
}

// Get returns a recorded run.
func (r *Runner) Get(ctx context.Context, runID string) (Run, error) {
	row, err := r.row(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	return runFromRow(row), nil
}

// List returns a project's runs, most recent first — what the desktop app's
// board renders. limit <= 0 defers to the store's own default.
func (r *Runner) List(ctx context.Context, projectID string, limit int) ([]Run, error) {
	rows, err := r.runs.ListRunsByProject(ctx, projectID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Run, len(rows))
	for i, row := range rows {
		out[i] = runFromRow(row)
	}
	return out, nil
}

// args builds the CLI invocation. cmd.Dir and the first --add-dir must always
// name the same resolved project path — one without the other is a scoping
// hole.
//
// The model comes from the row, not from config: it was decided when the task
// was written down, and a card that has been sitting in the backlog must run
// as the model the operator picked then.
//
// sessionID is empty for every first attempt: a row only carries one once the
// CLI has reported it, so a session id here means this run has already spoken
// and is being picked up again. That is precisely when `--resume` is right, and
// it is why the flag needs no separate "is this a retry" flag to sit beside it.
func (r *Runner) args(model, sessionID string) []string {
	if model == "" {
		model = r.cfg.CodingModel
	}
	args := []string{
		"-p",
		"--model", model,
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", r.cfg.CodingPermissionMode,
	}
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}
	return args
}

// buildPrompt puts the attachment paths where the agent will look for them.
//
// The images are handed over as paths rather than inlined bytes because the run
// is a headless `claude -p` reading a text prompt on stdin: a path it can Read
// is the one form of "here is a picture" that invocation understands.
func buildPrompt(prompt string, paths []string) string {
	if len(paths) == 0 {
		return prompt
	}
	var b strings.Builder
	b.WriteString("[attached files]\n")
	for _, p := range paths {
		b.WriteString(p)
		b.WriteString("\n")
	}
	b.WriteString("\nRead the paths above with the Read tool before answering.\n\n")
	b.WriteString(prompt)
	return b.String()
}

// continuation is what a resumed run is given: the task again, plus the one
// thing the replayed conversation cannot tell the session — that it was cut
// off, and why.
//
// The prompt is restated rather than replaced with "carry on" because
// `--resume` replays a transcript that may be long and may end mid-tool-call.
// Restating the goal is what keeps the session from picking up the wrong
// thread; telling it to check its own work first is what keeps it from
// repeating the half it already finished.
func continuation(prompt, cause string) string {
	cause = strings.TrimSpace(cause)
	if i := strings.IndexByte(cause, '\n'); i >= 0 {
		cause = cause[:i]
	}
	if len(cause) > maxResumeCause {
		cause = cause[:maxResumeCause] + "…"
	}

	var b strings.Builder
	b.WriteString("[this session was interrupted before it finished")
	if cause != "" {
		b.WriteString(": ")
		b.WriteString(cause)
	}
	b.WriteString("]\nCarry on from where you stopped. Check what you already did before redoing any of it.\n\n")
	b.WriteString("The task, unchanged:\n\n")
	b.WriteString(prompt)
	return b.String()
}

// execute owns one run from spawn to terminal event. It never returns an
// error: every failure becomes a terminal event and a recorded row, because
// there is no caller left to return to.
// The context and the handle are made by launch, which registers the handle
// before this goroutine exists so the slot is never briefly free; releasing
// both is still this function's job, because it is the one that knows when the
// run is over.
func (r *Runner) execute(ctx context.Context, h *inflight, row store.RunRow, projectPath, configDir string) {
	defer h.cancel()
	defer r.bus.CloseRun(row.ID)
	defer func() {
		r.mu.Lock()
		delete(r.inflight, row.ID)
		r.mu.Unlock()
	}()

	st := newState(row.ID)

	// The directory is created when the task is written down, but a run can be
	// claimed long afterwards — and from a queue that survived a restart, in a
	// process that has not created it yet.
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

	// Every failure below has to ask whether it was really a stop. Cancelling
	// the run's context is how a stop is delivered, and a cancelled context
	// makes the spawn — or the wait — fail in ways that read exactly like a
	// broken CLI. Reporting one of those would send the operator to fix
	// something they had just chosen to do.
	stopped := func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return h.stopped
	}
	fail := func(cause error) {
		if stopped() {
			r.stop(row, st, nil, transcript)
			return
		}
		// A budget that ran out is not a failure, and the difference is not
		// cosmetic: this run goes back in the queue with its session, and the
		// slot is held until the window rolls over. See park.
		if until, spent := st.budgetSpent(cause.Error(), time.Now().UTC(), r.cfg.CodingLimitRecheck); spent {
			r.park(row, st, cause.Error(), until, transcript)
			return
		}
		r.finish(row, st, cause, transcript)
	}

	attachments := r.attachmentPaths(decodeAttachmentIDs(row.Attachments))

	args := append(r.args(row.Model, row.SessionID), "--add-dir", projectPath)
	if len(attachments) > 0 {
		// The images live beside the store, outside the project, so the run
		// needs a second readable root to reach them at all.
		args = append(args, "--add-dir", r.cfg.AttachmentDir)
	}

	prompt := buildPrompt(row.Prompt, attachments)
	if row.SessionID != "" {
		// The row still carries the error the previous attempt ended with:
		// RequeueRun leaves it for ClaimRun to clear, so this goroutine's copy
		// is the last thing that went wrong rather than an empty string.
		prompt = continuation(prompt, row.Error)
	}

	cmd := exec.CommandContext(ctx, r.cfg.ClaudeCLIPath, args...)
	cmd.Dir = projectPath
	cmd.Stdin = strings.NewReader(prompt)
	// The credential slot is chosen here and nowhere else. account.Environ
	// also strips the session variables of whatever launched the daemon: a
	// child that inherits CLAUDECODE=1 and a session id believes it is
	// resuming somebody else's session.
	cmd.Env = account.Environ(os.Environ(), configDir)
	// A stop asks before it insists: SIGINT gives the CLI the chance to print
	// its own result line, which carries the cost and turns of the work already
	// done. The insisting is the watchdog below.
	// Its own process group, so a stop reaches the whole tree. `claude` spawns
	// children that inherit the stdout pipe: signalling the parent alone leaves
	// them holding it open, and the run would sit there "stopping" until they
	// finished on their own.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return signalGroup(cmd.Process.Pid, syscall.SIGINT) }

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fail(fmt.Errorf("%w: %v", ErrClaudeUnavailable, err))
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		fail(fmt.Errorf("%w: %v", ErrClaudeUnavailable, err))
		return
	}

	if err := cmd.Start(); err != nil {
		fail(fmt.Errorf("%w: `%s` could not be started (%v) — check it is on PATH and run `claude login`",
			ErrClaudeUnavailable, r.cfg.ClaudeCLIPath, err))
		return
	}

	// The watchdog, not cmd.WaitDelay, is what makes a stop take effect.
	// WaitDelay's kill only runs inside Wait, and Wait cannot be called until
	// consume has drained the pipes — which a child ignoring SIGINT never lets
	// happen. So the escalation has to live beside the read, not after it.
	done := make(chan struct{})
	var watchdog sync.WaitGroup
	watchdog.Add(1)
	go func() {
		defer watchdog.Done()
		select {
		case <-done:
			return
		case <-ctx.Done():
		}
		select {
		case <-done:
		case <-time.After(r.cfg.CodingStopGrace):
			_ = signalGroup(cmd.Process.Pid, syscall.SIGKILL)
		}
	}()

	terminal, tail := r.consume(stdout, stderr, st, transcript)

	// Wait must run even if consume stopped early, or the process is a zombie.
	waitErr := cmd.Wait()
	close(done)
	watchdog.Wait()

	// A stop is decided here, not by the exit status: an interrupted CLI exits
	// like a killed one, and only this flag knows the difference between a
	// cancellation and a crash.
	if stopped() {
		r.stop(row, st, terminal, transcript)
		return
	}
	if terminal != nil {
		// The CLI reported its own outcome; that is authoritative — except on
		// what to do about it, which for a spent budget is to wait rather than
		// to fail. consume has already declined to announce that failure, so
		// park owns the whole of what this run's watchers hear next.
		if terminal.Kind == events.KindRunFailed {
			if until, spent := st.budgetSpent(terminal.Error, time.Now().UTC(), r.cfg.CodingLimitRecheck); spent {
				r.park(row, st, terminal.Error, until, transcript)
				return
			}
		}
		r.record(row, *terminal)
		return
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		fail(fmt.Errorf("the run exceeded its %s limit", r.cfg.CodingRunTimeout))
		return
	}
	if waitErr != nil {
		fail(fmt.Errorf("the claude CLI exited with an error: %v: %s", waitErr, tail))
		return
	}
	// Exited cleanly without a result line — treat as a failure rather than
	// silently reporting success for a run that produced no outcome.
	fail(errors.New("the claude CLI exited without reporting a result"))
}

// signalGroup sends sig to the process group led by pid. A negative pid is how
// the kernel is told "the group, not the process".
func signalGroup(pid int, sig syscall.Signal) error {
	return syscall.Kill(-pid, sig)
}

// outLine is one line read from the child, and which stream it came from.
type outLine struct {
	text   []byte
	stderr bool
}

// scanInto reads one stream into the shared channel. Both streams are drained
// to EOF whatever happens downstream: a child whose stderr nobody reads will
// eventually block on a full pipe.
func scanInto(src io.Reader, isStderr bool, out chan<- outLine, wg *sync.WaitGroup) {
	defer wg.Done()

	scanner := bufio.NewScanner(src)
	// Tool results can be large; the default 64KB line limit is not enough.
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := make([]byte, len(scanner.Bytes()))
		copy(line, scanner.Bytes())
		out <- outLine{text: line, stderr: isStderr}
	}
}

// consume merges the CLI's two output streams into one sequenced event stream,
// publishing and persisting each event as it arrives. It returns the terminal
// event if the CLI reported one, and the tail of stderr for a failure message.
//
// The merge is a single consumer on purpose: Event.Seq is what stitches the
// transcript and the bus back together, and a second goroutine stamping
// sequence numbers would make that ordering a race.
func (r *Runner) consume(stdout, stderr io.Reader, st *state, transcript *os.File) (*events.Event, string) {
	lines := make(chan outLine, 64)

	var scanners sync.WaitGroup
	scanners.Add(2)
	go scanInto(stdout, false, lines, &scanners)
	go scanInto(stderr, true, lines, &scanners)
	go func() {
		scanners.Wait()
		close(lines)
	}()

	var (
		terminal    *events.Event
		stderrBytes int
		tail        []string
	)

	emit := func(ev events.Event) {
		ev = clampEvent(ev)
		// Transcript first: it is the complete record, and the bus is
		// explicitly allowed to drop (see internal/events/AGENTS.md).
		writeTranscript(transcript, ev)
		// Recorded but not announced: the budget ran out rather than the run
		// going wrong, and execute is about to put this run back in the queue.
		// A watcher told "failed" would be told something that stops being
		// true a moment later; park publishes the pause instead, and that
		// carries the time the work resumes.
		if ev.Kind != events.KindRunFailed || !st.rejected {
			r.bus.Publish(ev)
		}
		if ev.Terminal() {
			t := ev
			terminal = &t
		}
	}

	for line := range lines {
		if !line.stderr {
			for _, ev := range parseLine(line.text, st, time.Now().UTC()) {
				emit(ev)
			}
			continue
		}

		text := strings.TrimRight(string(line.text), "\r")
		// Kept for the failure message even once the live budget is spent: the
		// last thing a broken CLI says is usually the reason it is broken.
		tail = append(tail, text)
		if len(tail) > 20 {
			tail = tail[len(tail)-20:]
		}
		if stderrBytes >= r.cfg.CodingStderrMaxBytes {
			continue
		}
		stderrBytes += len(text)
		ev := st.next(events.KindStderr, time.Now().UTC())
		ev.Text = text
		emit(ev)
	}

	return terminal, strings.TrimSpace(strings.Join(tail, "\n"))
}

// clampEvent bounds the per-event payload a watcher receives. The transcript
// keeps whatever the CLI actually said.
func clampEvent(ev events.Event) events.Event {
	if len(ev.Output) > maxToolOutput {
		ev.Output = ev.Output[:maxToolOutput] + "\n… (truncated, see transcript)"
	}
	// Only stderr: a text delta is a suffix that a watcher appends, so
	// truncating one would corrupt the message rather than shorten it.
	if ev.Kind == events.KindStderr && len(ev.Text) > maxToolOutput {
		ev.Text = ev.Text[:maxToolOutput] + "… (truncated, see transcript)"
	}
	return ev
}

func writeTranscript(f *os.File, ev events.Event) {
	if f == nil {
		return
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return
	}
	_, _ = f.Write(append(line, '\n'))
}

// persist writes an outcome under a context the caller's cancellation cannot
// reach.
//
// The run's context is cancelled by the time this is called, and so, during
// shutdown, is base. Deriving from base directly meant every run finishing as
// the daemon exited failed to write its row and stayed `running` forever —
// which is exactly the state Resume now has to clean up.
func (r *Runner) persist(row store.RunRow) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.base), 5*time.Second)
	defer cancel()

	if err := r.runs.UpdateRunResult(ctx, row); err != nil {
		slog.Error("recording a run result", "run_id", row.ID, "error", err)
	}
}

// record persists a terminal event's outcome.
func (r *Runner) record(row store.RunRow, ev events.Event) {
	switch ev.Kind {
	case events.KindRunFailed:
		row.Status = store.RunStatusFailed
		row.Error = ev.Error
	case events.KindRunStopped:
		row.Status = store.RunStatusStopped
		row.Error = ev.Error
	default:
		row.Status = store.RunStatusCompleted
	}
	if ev.SessionID != "" {
		row.SessionID = ev.SessionID
	}
	if ev.Model != "" {
		row.Model = ev.Model
	}
	if ev.CostUSD != 0 {
		row.CostUSD = ev.CostUSD
	}
	if ev.NumTurns != 0 {
		row.NumTurns = ev.NumTurns
	}
	row.EndedAt = time.Now().UTC()

	r.persist(row)
}

// finish emits a run.failed for a failure the CLI never got to report, then
// records it.
func (r *Runner) finish(row store.RunRow, st *state, cause error, transcript *os.File) {
	ev := st.next(events.KindRunFailed, time.Now().UTC())
	ev.Error = cause.Error()
	// Whatever the CLI announced before it broke. A run that dies after `init`
	// has a session even though it never reported a result, and that session is
	// the only thing a retry can pick up — losing it here would make every
	// crash restart the task from nothing.
	ev.SessionID = st.sessionID
	ev.Model = st.model

	writeTranscript(transcript, ev)
	r.bus.Publish(ev)
	r.record(row, ev)
}

// stop ends a cancelled run, keeping whatever the CLI managed to report about
// the work it had already done.
func (r *Runner) stop(row store.RunRow, st *state, reported *events.Event, transcript *os.File) {
	ev := st.next(events.KindRunStopped, time.Now().UTC())
	ev.Error = "stopped by the operator"
	// Same reason as finish: an interrupted run is the one most likely to be
	// picked up again, and it is usually killed before it reports anything.
	ev.SessionID = st.sessionID
	ev.Model = st.model
	if reported != nil {
		ev.SessionID = firstNonEmpty(reported.SessionID, ev.SessionID)
		ev.Model = firstNonEmpty(reported.Model, ev.Model)
		ev.CostUSD = reported.CostUSD
		ev.NumTurns = reported.NumTurns
	}

	writeTranscript(transcript, ev)
	r.bus.Publish(ev)
	r.record(row, ev)
}
