package coderunner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/logrenant/mimir/internal/account"
	"github.com/logrenant/mimir/internal/events"
	"github.com/logrenant/mimir/internal/store"
)

// ErrBudgetSpent means every credential slot has run out of tokens for now.
//
// A conflict rather than a failure, and unlike the others in this package it is
// one nobody can act on: the work is fine, the account simply has nothing left
// until the window rolls over. It carries that time in its message because the
// only useful answer to "why is nothing running?" here is "at 19:40, by
// itself".
var ErrBudgetSpent = errors.New("coderunner: the token budget is spent")

// holdFloor is the shortest pause worth arming a timer for. A reset time that
// has already passed by the time it is read would otherwise schedule a wake-up
// in the past, and the pump it triggers would race the run that reported it.
const holdFloor = time.Second

// maxLogDetail bounds what a log row keeps of the CLI's own words. The reason a
// budget ran out is one sentence; a CLI that died noisily can leave a stack
// trace behind, and none of it is the reason.
const maxLogDetail = 300

// spentBudget is one credential slot the dispatcher is holding work back for.
//
// timer is the whole of the "restart by itself" promise: the pipeline is not
// polled and nothing loops waiting for the window: one wake-up is armed for the
// moment the budget returns, and it calls the same pump every other release
// calls.
type spentBudget struct {
	until  time.Time
	since  time.Time
	reason string
	// noted is whether a `dispatch` row has already been written for this
	// pause. The queue is pumped on every release and every finished run, so
	// without this the log would fill with one row per pump and bury the
	// moment that actually happened.
	noted bool
	timer *time.Timer
}

// Hold is a slot the queue is currently waiting on, as an API client sees it.
type Hold struct {
	AccountID string    `json:"account_id,omitempty"`
	Since     time.Time `json:"since"`
	ResetsAt  time.Time `json:"resets_at"`
	Reason    string    `json:"reason,omitempty"`
}

// LimitReport is the whole answer to "why is nothing running, and when will it
// be?" — what the queue is waiting on now, and the log of how it got there.
type LimitReport struct {
	Holds []Hold               `json:"holds"`
	Log   []store.RateLimitRow `json:"log"`
}

// Limits reports the pauses in force and the recent log.
//
// Two sources on purpose: the holds are this process's live state and the log
// is durable, so a daemon that has just restarted answers from rows while one
// that has been up for hours answers from both.
func (r *Runner) Limits(ctx context.Context, limit int) (LimitReport, error) {
	now := time.Now().UTC()

	r.limitsMu.Lock()
	holds := make([]Hold, 0, len(r.held))
	for id, b := range r.held {
		if !b.until.After(now) {
			continue
		}
		holds = append(holds, Hold{
			AccountID: id, Since: b.since, ResetsAt: b.until, Reason: b.reason,
		})
	}
	r.limitsMu.Unlock()

	log, err := r.runs.ListRateLimitEvents(ctx, limit)
	if err != nil {
		return LimitReport{}, err
	}
	return LimitReport{Holds: holds, Log: log}, nil
}

// heldUntil reports whether a slot is out of tokens, and until when.
//
// Expired holds are dropped as they are read rather than only by their timer,
// because a process that was asleep or paused past a reset would otherwise keep
// stepping over a slot that came back while nothing was looking.
func (r *Runner) heldUntil(accountID string, now time.Time) (time.Time, bool) {
	r.limitsMu.Lock()
	defer r.limitsMu.Unlock()

	b, ok := r.held[accountID]
	if !ok {
		return time.Time{}, false
	}
	if !b.until.After(now) {
		if b.timer != nil {
			b.timer.Stop()
		}
		delete(r.held, accountID)
		return time.Time{}, false
	}
	return b.until, true
}

// holdSlot parks one slot until `until` and arms the wake-up that ends it.
//
// It writes no log row, which is what lets restoreHolds reuse it: rebuilding a
// pause that is already recorded must not record it a second time.
func (r *Runner) holdSlot(accountID, reason string, until, now time.Time) {
	r.limitsMu.Lock()
	defer r.limitsMu.Unlock()

	if prev, ok := r.held[accountID]; ok && prev.timer != nil {
		// A second run can report the same spent window before the first
		// pause has expired. One timer per slot, or the earlier one would
		// pump while the later hold is still in force.
		prev.timer.Stop()
	}

	wait := until.Sub(now)
	if wait < holdFloor {
		wait = holdFloor
	}
	id := accountID
	r.held[accountID] = &spentBudget{
		until:  until,
		since:  now,
		reason: reason,
		timer:  time.AfterFunc(wait, func() { r.releaseSlot(id) }),
	}
}

// hold records a spent budget and pauses the slot it belongs to.
func (r *Runner) hold(accountID, runID, reason string, until time.Time) {
	now := time.Now().UTC()
	r.holdSlot(accountID, reason, until, now)

	slog.Warn("the token budget is spent; the coding queue is paused",
		"account_id", accountID, "run_id", runID,
		"resets_at", until.Format(time.RFC3339), "reason", reason)

	r.logLimit(store.RateLimitRow{
		At:        now,
		Phase:     store.RateLimitPhaseRun,
		AccountID: accountID,
		RunID:     runID,
		ResetsAt:  until,
		Detail:    clampDetail(reason),
	})
}

// releaseSlot is the wake-up: the window has rolled over, so the pause is
// lifted, the fact is written down, and the queue is pumped exactly as any
// other release pumps it.
//
// This is the "restarts by itself" half of the feature. Nothing else has to
// happen — the runs parked by the pause are ordinary queued rows, and pump
// claims them the way it claims any other.
func (r *Runner) releaseSlot(accountID string) {
	r.limitsMu.Lock()
	b, ok := r.held[accountID]
	delete(r.held, accountID)
	r.limitsMu.Unlock()

	if !ok {
		return
	}
	// The daemon is going away. Pumping now would start a run the shutdown is
	// about to cancel, and writing a row would race the store being closed.
	// This guard is the whole of the timers' lifetime management: a wake-up
	// that outlives the runner finds a cancelled base and does nothing, which
	// is why Wait does not have to disarm them.
	if r.base.Err() != nil {
		return
	}

	slog.Info("the token budget has reset; the coding queue is restarting",
		"account_id", accountID, "paused_for", time.Since(b.since).Round(time.Second))

	r.logLimit(store.RateLimitRow{
		At:        time.Now().UTC(),
		Phase:     store.RateLimitPhaseResumed,
		AccountID: accountID,
		ResetsAt:  b.until,
		Detail:    "the token budget reset; the queue restarted",
	})
	r.pump()
}

// noteHeld writes the "a queued task could not start" half of the log.
//
// Only when work is actually waiting: a pause with an empty queue happened to
// nobody. And only once per pause — the queue is pumped on every release, so a
// row per attempt would be a row per unrelated event elsewhere in the daemon.
func (r *Runner) noteHeld(ctx context.Context, held []string) {
	queued, err := r.runs.ListQueuedRuns(ctx, 1)
	if err != nil || len(queued) == 0 {
		return
	}

	for _, id := range held {
		r.limitsMu.Lock()
		b, ok := r.held[id]
		first := ok && !b.noted
		if first {
			b.noted = true
		}
		r.limitsMu.Unlock()
		if !first {
			continue
		}

		slog.Warn("a queued coding task is waiting for the token budget",
			"account_id", id, "run_id", queued[0].ID,
			"resets_at", b.until.Format(time.RFC3339))

		r.logLimit(store.RateLimitRow{
			At:        time.Now().UTC(),
			Phase:     store.RateLimitPhaseDispatch,
			AccountID: id,
			RunID:     queued[0].ID,
			ResetsAt:  b.until,
			Detail:    "the queue is held: this slot has no tokens left to spend",
		})
	}
}

// restoreHolds rebuilds the pauses in force from the log, at startup.
//
// The holds themselves live in this process's memory and die with it, but the
// budget they describe belongs to the account and outlives any number of
// restarts. Without this, a daemon restarted inside a spent window would pump
// immediately, spend a CLI invocation discovering what the log already said,
// and park the run again — once per restart.
//
// Newest row per account wins: a `resumed` row means the pause is over, and
// anything else means it lasts until its reset time.
func (r *Runner) restoreHolds(ctx context.Context) {
	rows, err := r.runs.ListRateLimitEvents(ctx, 50)
	if err != nil {
		slog.Warn("reading the rate-limit log", "error", err)
		return
	}

	now := time.Now().UTC()
	seen := map[string]struct{}{}
	for _, row := range rows { // newest first
		if _, done := seen[row.AccountID]; done {
			continue
		}
		seen[row.AccountID] = struct{}{}

		if row.Phase == store.RateLimitPhaseResumed || !row.ResetsAt.After(now) {
			continue
		}
		r.holdSlot(row.AccountID, row.Detail, row.ResetsAt, now)
		slog.Info("the coding queue is still held by a spent token budget",
			"account_id", row.AccountID, "resets_at", row.ResetsAt.Format(time.RFC3339))
	}
}

// logLimit appends one moment to the durable log.
//
// Under WithoutCancel for the same reason persist is: the caller is usually a
// run whose context has just been cancelled, and during shutdown so is base.
// A failure here is logged, never returned — losing the row must not change
// what happens to the run.
func (r *Runner) logLimit(row store.RateLimitRow) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.base), 5*time.Second)
	defer cancel()

	if err := r.runs.InsertRateLimitEvent(ctx, row); err != nil {
		slog.Error("recording a rate-limit event",
			"phase", row.Phase, "account_id", row.AccountID, "error", err)
	}
}

// park puts a run the token budget cut off back in the queue, and holds its
// slot until the window rolls over.
//
// Requeued rather than failed, and that distinction is the point: nothing went
// wrong, so nothing should be handed to the operator to diagnose. The session
// id goes back on the row, so when the pause lifts the CLI is resumed and the
// task carries on from where it stopped instead of starting over.
func (r *Runner) park(row store.RunRow, st *state, reason string, until time.Time, transcript *os.File) {
	now := time.Now().UTC()

	// The pause is the run's own last event. consume deliberately did not
	// publish the CLI's `run.failed` for this case, so this is the single
	// announcement a watcher gets — and it carries the time the work resumes,
	// which "failed" never could.
	ev := st.next(events.KindRateLimit, now)
	ev.ResetsAt = until.Unix()
	ev.SessionID = st.sessionID
	ev.Model = st.model
	ev.Error = reason
	writeTranscript(transcript, ev)
	r.bus.Publish(ev)

	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.base), 5*time.Second)
	parked, err := r.runs.ParkRun(ctx, row.ID, st.sessionID, now, waitingReason(reason, until))
	cancel()

	if err != nil {
		slog.Error("parking a rate-limited run", "run_id", row.ID, "error", err)
	}
	if err != nil || !parked {
		// The row moved under us, or the store is unreachable. Either way it
		// must not be left saying `running` with nothing owning it, so the
		// attempt is recorded as the failure the CLI reported.
		r.finish(row, st, fmt.Errorf("%w: %s", ErrBudgetSpent, reason), transcript)
	}

	r.hold(row.AccountID, row.ID, reason, until)
}

// waitingReason is what the parked card says while it waits. It stays on the
// row until ClaimRun clears it, so it is read twice: by the operator looking at
// the board, and by the resumed session, which is told what cut it off.
func waitingReason(reason string, until time.Time) string {
	return fmt.Sprintf("the token budget ran out (%s) — this task resumes by itself at %s",
		clampDetail(reason), until.Local().Format("15:04"))
}

func clampDetail(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > maxLogDetail {
		s = s[:maxLogDetail] + "…"
	}
	if s == "" {
		return "no reason reported"
	}
	return s
}

// budgetSpent reports whether what ended this attempt was the token budget
// rather than anything wrong, and when the work may carry on.
//
// Two sources, because the CLI announces a spent budget in two ways: a
// `rate_limit_event` whose window it has rejected, which the parser latched on
// the state, and a sentence in the error it exits with. The second is the one
// that survives a crash before any structured line was written, so both are
// asked.
//
// The reset time is the CLI's own whenever it gave one — from the rejected
// window, or from the `…|<unix>` suffix its usage-limit message carries. Only
// when it gave neither does this fall back to a recheck interval, which is a
// guess and is treated as one: it pauses the queue for a while rather than
// claiming to know when the budget returns.
func (s *state) budgetSpent(detail string, now time.Time, recheck time.Duration) (time.Time, bool) {
	if !s.rejected && !looksSpent(detail) {
		return time.Time{}, false
	}

	if at := time.Unix(s.resetsAt, 0).UTC(); s.resetsAt > 0 && at.After(now) {
		return at, true
	}
	if at, ok := resetFromMessage(detail); ok && at.After(now) {
		return at, true
	}
	return now.Add(recheck), true
}

// spentPhrases are the CLI's own words for a budget that has run out. Matched
// as substrings of the CLI's error text only — never of a run's output — so
// they can be this plain without catching a task that merely talks about rate
// limits.
var spentPhrases = []string{
	"usage limit",
	"rate limit",
	"rate_limit",
	"rate-limited",
	"too many requests",
	"quota",
}

func looksSpent(detail string) bool {
	lower := strings.ToLower(detail)
	for _, p := range spentPhrases {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// resetFromMessage reads the reset time out of the CLI's usage-limit sentence,
// which carries it as a unix timestamp after a pipe:
// `Claude AI usage limit reached|1788104400`.
//
// Read rather than assumed, because it is the difference between resuming when
// the budget actually returns and resuming on a guess.
func resetFromMessage(detail string) (time.Time, bool) {
	_, after, ok := strings.Cut(detail, "|")
	if !ok {
		return time.Time{}, false
	}
	field := strings.TrimSpace(after)
	if i := strings.IndexFunc(field, func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
		field = field[:i]
	}
	secs, err := strconv.ParseInt(field, 10, 64)
	if err != nil || secs <= 0 {
		return time.Time{}, false
	}
	return time.Unix(secs, 0).UTC(), true
}

// heldSlots reports which of these slots have nothing left to spend, and the
// soonest moment any of them does.
func (r *Runner) heldSlots(slots []account.Account, now time.Time) ([]string, time.Time) {
	var (
		held    []string
		soonest time.Time
	)
	for _, a := range slots {
		until, ok := r.heldUntil(a.ID, now)
		if !ok {
			continue
		}
		held = append(held, a.ID)
		if soonest.IsZero() || until.Before(soonest) {
			soonest = until
		}
	}
	return held, soonest
}
