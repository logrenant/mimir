// Package coderunner runs a `claude` coding session inside one registered
// project directory and reports what it does as it happens.
//
// This is a second, deliberately different invocation profile from
// internal/refine. The refiner is headless, tool-less and --restricted because
// it handles untrusted scraped text; this runner has file tools enabled
// because it does work in the operator's own repo. What keeps that safe is not
// the flags but the directory: internal/project decided it, and both cmd.Dir
// and --add-dir point at exactly that path.
package coderunner

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/events"
	"github.com/logrenant/goat-mcp/internal/project"
	"github.com/logrenant/goat-mcp/internal/store"
)

// ErrClaudeUnavailable means the `claude` CLI could not be started at all —
// missing, not on PATH, or not authenticated. Distinct from a run that started
// and then failed, because the fix is different (SD-6).
var ErrClaudeUnavailable = errors.New("coderunner: claude CLI unavailable")

// ErrRunNotFound means no run has that id. A sentinel because callers across a
// transport boundary have to tell "you asked for something that does not
// exist" (404) apart from "the store broke" (500).
var ErrRunNotFound = errors.New("coderunner: no such run")

// maxToolOutput caps how much of a tool result is carried in an event. Tool
// output can be an entire file; the watcher wants a readable preview, and the
// full text is in the transcript. A package constant, not a knob (SD-1).
const maxToolOutput = 4000

// Run is one coding session.
type Run struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Prompt    string    `json:"prompt"`
	Status    string    `json:"status"`
	SessionID string    `json:"session_id,omitempty"`
	Model     string    `json:"model,omitempty"`
	CostUSD   float64   `json:"cost_usd,omitempty"`
	NumTurns  int       `json:"num_turns,omitempty"`
	Error     string    `json:"error,omitempty"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
}

// ProjectResolver hands back a directory that has passed every guard. Taking
// the interface rather than *project.Registry keeps the runner testable, but
// note the contract: an implementation MUST validate, not just look up.
type ProjectResolver interface {
	Resolve(ctx context.Context, id string) (project.Project, error)
}

// RunStore persists run records.
type RunStore interface {
	InsertRun(ctx context.Context, r store.RunRow) error
	UpdateRunResult(ctx context.Context, r store.RunRow) error
	GetRun(ctx context.Context, id string) (store.RunRow, bool, error)
	ListRunsByProject(ctx context.Context, projectID string, limit int) ([]store.RunRow, error)
}

// Runner starts and supervises coding sessions.
type Runner struct {
	base     context.Context
	cfg      config.Config
	bus      *events.Bus
	projects ProjectResolver
	runs     RunStore
	wg       sync.WaitGroup
}

// New returns a Runner whose in-flight runs live until base is cancelled.
//
// base is an explicit parameter because a run must outlive the HTTP request
// that started it — deriving the run's context from the request would kill it
// the moment the response was written. Taking the lifetime from the caller is
// also what keeps this package free of a rootless context, which
// tools/lint/check_context.sh bans under internal/.
func New(base context.Context, cfg config.Config, bus *events.Bus,
	projects ProjectResolver, runs RunStore) *Runner {
	return &Runner{
		base:     base,
		cfg:      cfg,
		bus:      bus,
		projects: projects,
		runs:     runs,
	}
}

func newRunID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("coderunner: generating run id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// Start validates the project, records the run, spawns the CLI, and returns
// immediately. Progress arrives on the bus; the complete record is the
// transcript on disk.
//
// ctx bounds only this setup work, not the run.
func (r *Runner) Start(ctx context.Context, projectID, prompt string) (Run, error) {
	if strings.TrimSpace(prompt) == "" {
		return Run{}, errors.New("coderunner: prompt is empty")
	}

	// Resolve, never Get: this re-runs every path guard against the filesystem
	// as it is now. It is the only way this package learns a directory.
	proj, err := r.projects.Resolve(ctx, projectID)
	if err != nil {
		return Run{}, err
	}

	runID, err := newRunID()
	if err != nil {
		return Run{}, err
	}

	transcriptPath := filepath.Join(r.cfg.TranscriptDir, runID+".jsonl")
	if err := os.MkdirAll(r.cfg.TranscriptDir, 0o755); err != nil {
		return Run{}, fmt.Errorf("coderunner: creating transcript directory: %w", err)
	}

	now := time.Now().UTC()
	row := store.RunRow{
		ID:             runID,
		ProjectID:      proj.ID,
		Prompt:         prompt,
		Status:         store.RunStatusRunning,
		Model:          r.cfg.CodingModel,
		TranscriptPath: transcriptPath,
		StartedAt:      now,
	}
	// Recorded before the CLI is spawned, so a run is never in flight without
	// a row to find it by.
	if err := r.runs.InsertRun(ctx, row); err != nil {
		return Run{}, err
	}

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.execute(row, proj.Path)
	}()

	return Run{
		ID:        runID,
		ProjectID: proj.ID,
		Prompt:    prompt,
		Status:    store.RunStatusRunning,
		Model:     r.cfg.CodingModel,
		StartedAt: now,
	}, nil
}

// Wait blocks until every in-flight run has finished. For graceful shutdown,
// and what lets tests satisfy goleak.
func (r *Runner) Wait() {
	r.wg.Wait()
}

// Get returns a recorded run.
func (r *Runner) Get(ctx context.Context, runID string) (Run, error) {
	row, found, err := r.runs.GetRun(ctx, runID)
	if err != nil {
		return Run{}, err
	}
	if !found {
		return Run{}, fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}
	return Run{
		ID:        row.ID,
		ProjectID: row.ProjectID,
		Prompt:    row.Prompt,
		Status:    row.Status,
		SessionID: row.SessionID,
		Model:     row.Model,
		CostUSD:   row.CostUSD,
		NumTurns:  row.NumTurns,
		Error:     row.Error,
		StartedAt: row.StartedAt,
		EndedAt:   row.EndedAt,
	}, nil
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
		out[i] = Run{
			ID:        row.ID,
			ProjectID: row.ProjectID,
			Prompt:    row.Prompt,
			Status:    row.Status,
			SessionID: row.SessionID,
			Model:     row.Model,
			CostUSD:   row.CostUSD,
			NumTurns:  row.NumTurns,
			Error:     row.Error,
			StartedAt: row.StartedAt,
			EndedAt:   row.EndedAt,
		}
	}
	return out, nil
}

// args builds the CLI invocation. cmd.Dir and --add-dir must always name the
// same resolved project path — one without the other is a scoping hole.
func (r *Runner) args() []string {
	return []string{
		"-p",
		"--model", r.cfg.CodingModel,
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", r.cfg.CodingPermissionMode,
	}
}

// execute owns one run from spawn to terminal event. It never returns an
// error: every failure becomes a run.failed event and a failed row, because
// there is no caller left to return to.
func (r *Runner) execute(row store.RunRow, projectPath string) {
	ctx, cancel := context.WithTimeout(r.base, r.cfg.CodingRunTimeout)
	defer cancel()
	defer r.bus.CloseRun(row.ID)

	st := newState(row.ID)

	transcript, err := os.OpenFile(row.TranscriptPath,
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		r.finish(row, st, fmt.Errorf("opening transcript: %w", err), nil)
		return
	}
	defer func() { _ = transcript.Close() }()

	args := append(r.args(), "--add-dir", projectPath)
	cmd := exec.CommandContext(ctx, r.cfg.ClaudeCLIPath, args...)
	cmd.Dir = projectPath
	cmd.Stdin = strings.NewReader(row.Prompt)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		r.finish(row, st, fmt.Errorf("%w: %v", ErrClaudeUnavailable, err), transcript)
		return
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		r.finish(row, st, fmt.Errorf("%w: `%s` could not be started (%v) — check it is on PATH and run `claude login`",
			ErrClaudeUnavailable, r.cfg.ClaudeCLIPath, err), transcript)
		return
	}

	terminal := r.consume(stdout, st, transcript)

	// Wait must run even if consume stopped early, or the process is a zombie.
	waitErr := cmd.Wait()

	if terminal != nil {
		// The CLI reported its own outcome; that is authoritative.
		r.record(row, *terminal)
		return
	}
	if waitErr != nil {
		r.finish(row, st, fmt.Errorf("the claude CLI exited with an error: %v: %s",
			waitErr, strings.TrimSpace(stderr.String())), transcript)
		return
	}
	// Exited cleanly without a result line — treat as a failure rather than
	// silently reporting success for a run that produced no outcome.
	r.finish(row, st, errors.New("the claude CLI exited without reporting a result"), transcript)
}

// consume reads the CLI's stdout line by line, publishing and persisting each
// translated event as it arrives. It returns the terminal event if the CLI
// reported one.
//
// bufio.Scanner is deliberate over reading to completion: the whole point is
// that a watcher sees the run as it happens.
func (r *Runner) consume(stdout interface{ Read([]byte) (int, error) },
	st *state, transcript *os.File) *events.Event {

	var terminal *events.Event

	scanner := bufio.NewScanner(stdout)
	// Tool results can be large; the default 64KB line limit is not enough.
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	for scanner.Scan() {
		for _, ev := range parseLine(scanner.Bytes(), st, time.Now().UTC()) {
			ev = clampEvent(ev)

			// Transcript first: it is the complete record, and the bus is
			// explicitly allowed to drop (see internal/events/AGENTS.md).
			writeTranscript(transcript, ev)
			r.bus.Publish(ev)

			if ev.Terminal() {
				t := ev
				terminal = &t
			}
		}
	}
	// A scanner error (including an over-long line) ends the stream; the exit
	// status and the missing terminal event are what the caller acts on.
	return terminal
}

// clampEvent bounds the per-event payload a watcher receives. The transcript
// keeps whatever the CLI actually said.
func clampEvent(ev events.Event) events.Event {
	if len(ev.Output) > maxToolOutput {
		ev.Output = ev.Output[:maxToolOutput] + "\n… (truncated, see transcript)"
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

// record persists a terminal event's outcome.
func (r *Runner) record(row store.RunRow, ev events.Event) {
	row.Status = store.RunStatusCompleted
	if ev.Kind == events.KindRunFailed {
		row.Status = store.RunStatusFailed
		row.Error = ev.Error
	}
	row.SessionID = ev.SessionID
	if ev.Model != "" {
		row.Model = ev.Model
	}
	row.CostUSD = ev.CostUSD
	row.NumTurns = ev.NumTurns
	row.EndedAt = time.Now().UTC()

	// The run's own context may already be cancelled; persist under base so a
	// timed-out run still records why.
	_ = r.runs.UpdateRunResult(r.base, row)
}

// finish emits a run.failed for a failure the CLI never got to report, then
// records it.
func (r *Runner) finish(row store.RunRow, st *state, cause error, transcript *os.File) {
	ev := st.next(events.KindRunFailed, time.Now().UTC())
	ev.Error = cause.Error()

	writeTranscript(transcript, ev)
	r.bus.Publish(ev)
	r.record(row, ev)
}
