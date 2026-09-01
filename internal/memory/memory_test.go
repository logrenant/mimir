package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/refine"
	"github.com/logrenant/mimir/internal/store"
)

// fakeRefiner stands in for the claude CLI. The real subprocess is covered in
// internal/refine; what matters here is how this package reacts to each of its
// three possible answers.
type fakeRefiner struct {
	calls    atomic.Int32
	reply    string
	rejectOn func(facts string) bool
	hardErr  error
}

func (f *fakeRefiner) Recap(_ context.Context, in refine.RecapInput) (refine.Output, error) {
	f.calls.Add(1)
	if f.hardErr != nil {
		return refine.Output{}, f.hardErr
	}
	if f.rejectOn != nil && f.rejectOn(in.Facts) {
		return refine.Output{}, fmt.Errorf("%w: degenerate", refine.ErrRefineRejected)
	}
	reply := f.reply
	if reply == "" {
		reply = "A title line\n- did the thing"
	}
	return refine.Output{Text: reply, Refined: true}, nil
}

type harness struct {
	mem      *Memory
	store    *store.Store
	refiner  *fakeRefiner
	project  Project
	sessions string
}

// newHarness builds a memory over a real SQLite store and a fixture transcript
// tree. SQLite is an in-process library, not an external service, so it is not
// mocked here any more than it is in internal/store.
func newHarness(t *testing.T) *harness {
	t.Helper()

	projectPath := t.TempDir()
	claudeRoot := t.TempDir()
	sessions := filepath.Join(claudeRoot, slugHint(projectPath))
	if err := os.MkdirAll(sessions, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := config.Load()
	cfg.StorePath = filepath.Join(t.TempDir(), "mimir.db")
	cfg.ClaudeProjectsDir = claudeRoot

	s, err := store.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	r := &fakeRefiner{}
	return &harness{
		mem:      New(cfg, s, r, nil),
		store:    s,
		refiner:  r,
		project:  Project{Path: projectPath},
		sessions: sessions,
	}
}

// writeSession drops a transcript into the fixture tree.
func (h *harness) writeSession(t *testing.T, name string, prompts ...string) string {
	t.Helper()
	return h.writeSessionFor(t, name, h.project.Path, prompts...)
}

func (h *harness) writeSessionFor(t *testing.T, name, cwd string, prompts ...string) string {
	t.Helper()
	var b strings.Builder
	for i, p := range prompts {
		ts := time.Date(2026, 1, 2, 10, i, 0, 0, time.UTC).Format(time.RFC3339Nano)
		fmt.Fprintf(&b, `{"type":"user","uuid":"u%d","sessionId":"s","timestamp":%q,"cwd":%q,"gitBranch":"main","origin":{"kind":"human"},"promptSource":"typed","message":{"role":"user","content":%q}}`+"\n", i, ts, cwd, p)
		fmt.Fprintf(&b, `{"type":"assistant","uuid":"a%d","timestamp":%q,"message":{"id":"m%d","role":"assistant","usage":{"output_tokens":10},"content":[{"type":"tool_use","id":"t%d","name":"Edit","input":{"file_path":%q}}]}}`+"\n", i, ts, i, i, filepath.Join(cwd, "internal/thing.go"))
		fmt.Fprintf(&b, `{"type":"assistant","uuid":"b%d","timestamp":%q,"message":{"id":"m%d","role":"assistant","usage":{"output_tokens":10},"content":[{"type":"text","text":"Did %s"}]}}`+"\n", i, ts, i, p)
	}

	path := filepath.Join(h.sessions, name)
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	// Age the file past the open-episode grace so its trailing episode is
	// eligible for a recap; a fresh mtime means "a session is running".
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestIngest_StoresEpisodesAndRecapsThem(t *testing.T) {
	h := newHarness(t)
	h.writeSession(t, "a.jsonl", "add a retry", "remove the retry")

	stats, err := h.mem.Ingest(context.Background(), h.project, 10)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if stats.Sources != 1 || stats.Episodes != 2 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	if stats.Recapped != 2 || stats.Remaining != 0 {
		t.Fatalf("both episodes should be recapped and nothing left: %+v", stats)
	}

	rows, err := h.store.RecentEpisodes(context.Background(), h.project.Path, 10)
	if err != nil {
		t.Fatalf("RecentEpisodes: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	if rows[0].Title != "A title line" || rows[0].Summary != "- did the thing" {
		t.Errorf("recap was not split into title and summary: %+v", rows[0])
	}
}

// A second pass must not duplicate work or re-pay for recaps it already has.
// Without this the backfill would never converge.
func TestIngest_IsIdempotent(t *testing.T) {
	h := newHarness(t)
	h.writeSession(t, "a.jsonl", "add a retry", "remove the retry")
	ctx := context.Background()

	if _, err := h.mem.Ingest(ctx, h.project, 10); err != nil {
		t.Fatalf("first Ingest: %v", err)
	}
	firstCalls := h.refiner.calls.Load()

	stats, err := h.mem.Ingest(ctx, h.project, 10)
	if err != nil {
		t.Fatalf("second Ingest: %v", err)
	}
	// At most the trailing episode is re-read, by design: it has no following
	// prompt to close it, so each pass refreshes it in place. Anything beyond
	// that means the resume offset did not stick.
	if stats.Episodes > 1 {
		t.Errorf("a second pass re-read %d episodes; the offset did not stick", stats.Episodes)
	}
	if got := h.refiner.calls.Load(); got != firstCalls {
		t.Errorf("a second pass spent %d extra model calls", got-firstCalls)
	}

	rows, _ := h.store.RecentEpisodes(ctx, h.project.Path, 10)
	if len(rows) != 2 {
		t.Fatalf("episodes multiplied across passes: %d", len(rows))
	}
}

// Phase 1 is free and complete on its own. This is what makes the memory useful
// before a single model call has been paid for.
func TestIngest_PhaseOneWorksWithNoRefiner(t *testing.T) {
	h := newHarness(t)
	h.mem = New(h.mem.cfg, h.store, nil, nil)
	h.writeSession(t, "a.jsonl", "wire up the crawl retry")
	ctx := context.Background()

	stats, err := h.mem.Ingest(ctx, h.project, 10)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if stats.Episodes != 1 || stats.Recapped != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	hits, err := h.store.SearchEpisodes(ctx, h.project.Path, "thing.go", 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("an un-recapped episode must still be searchable: %d hits, err=%v", len(hits), err)
	}

	// And the brief must show the request rather than an empty line.
	b, err := h.mem.Brief(ctx, h.project)
	if err != nil {
		t.Fatalf("Brief: %v", err)
	}
	if len(b.Recent) != 1 || b.Recent[0].Distilled {
		t.Fatalf("want one undistilled entry, got %+v", b.Recent)
	}
	if b.Recent[0].Summary != "wire up the crawl retry" {
		t.Errorf("undistilled entry should fall back to the request: %q", b.Recent[0].Summary)
	}
}

// The whole failure posture in one test: a refusal costs the recap, never the
// episode, and never the pass.
func TestIngest_RejectedRecapKeepsTheEpisode(t *testing.T) {
	h := newHarness(t)
	h.refiner.rejectOn = func(string) bool { return true }
	h.writeSession(t, "a.jsonl", "add a retry")
	ctx := context.Background()

	stats, err := h.mem.Ingest(ctx, h.project, 10)
	if err != nil {
		t.Fatalf("a rejected recap must not fail the ingest: %v", err)
	}
	if stats.Episodes != 1 || stats.Rejected != 1 || stats.Recapped != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	rows, _ := h.store.RecentEpisodes(ctx, h.project.Path, 10)
	if len(rows) != 1 {
		t.Fatalf("the episode was lost with its recap: %d rows", len(rows))
	}
	if rows[0].Summary != "" || rows[0].RecapAttempts != 1 {
		t.Errorf("expected an empty summary and a recorded attempt: %+v", rows[0])
	}
	if hits, _ := h.store.SearchEpisodes(ctx, h.project.Path, "thing.go", 10); len(hits) != 1 {
		t.Errorf("a rejected episode must stay searchable")
	}
}

// An episode that keeps being refused must stop costing model calls, or a
// single pathological transcript would drain every future pass's budget.
func TestIngest_HopelessEpisodeRetiresAfterTheAttemptCeiling(t *testing.T) {
	h := newHarness(t)
	h.refiner.rejectOn = func(string) bool { return true }
	h.writeSession(t, "a.jsonl", "add a retry")
	ctx := context.Background()

	for range 4 {
		if _, err := h.mem.Ingest(ctx, h.project, 10); err != nil {
			t.Fatalf("Ingest: %v", err)
		}
	}
	if got := h.refiner.calls.Load(); got != int32(h.mem.cfg.MemoryRecapMaxAttempts) {
		t.Errorf("spent %d model calls on a hopeless episode, want %d", got, h.mem.cfg.MemoryRecapMaxAttempts)
	}
}

// An unauthenticated CLI is not any single episode's fault. Absorbing it as a
// per-episode rejection would burn every episode's attempt budget and leave the
// memory permanently blank once the CLI came back.
func TestIngest_CLIOutageDoesNotConsumeAttempts(t *testing.T) {
	h := newHarness(t)
	h.refiner.hardErr = refine.ErrClaudeUnavailable
	h.writeSession(t, "a.jsonl", "add a retry")
	ctx := context.Background()

	if _, err := h.mem.Ingest(ctx, h.project, 10); !errors.Is(err, refine.ErrClaudeUnavailable) {
		t.Fatalf("want the outage surfaced, got %v", err)
	}
	rows, _ := h.store.RecentEpisodes(ctx, h.project.Path, 10)
	if len(rows) != 1 {
		t.Fatalf("phase 1 should still have landed: %d rows", len(rows))
	}
	if rows[0].RecapAttempts != 0 {
		t.Errorf("an outage consumed an attempt: %d", rows[0].RecapAttempts)
	}

	// Once the CLI is back the episode is still eligible.
	h.refiner.hardErr = nil
	if _, err := h.mem.Ingest(ctx, h.project, 10); err != nil {
		t.Fatalf("recovery Ingest: %v", err)
	}
	rows, _ = h.store.RecentEpisodes(ctx, h.project.Path, 10)
	if rows[0].Summary == "" {
		t.Errorf("the episode was never recapped after recovery")
	}
}

// The directory name is a hint; the cwd inside the file is the decision.
func TestIngest_IgnoresAnotherProjectsTranscript(t *testing.T) {
	h := newHarness(t)
	h.writeSessionFor(t, "other.jsonl", "/some/other/project", "not my work")
	h.writeSession(t, "mine.jsonl", "my work")

	stats, err := h.mem.Ingest(context.Background(), h.project, 10)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if stats.Sources != 1 || stats.Episodes != 1 {
		t.Fatalf("another project's transcript was ingested: %+v", stats)
	}
}

// A transcript written to seconds ago is the session running right now. Its
// trailing episode is mid-task, so it is stored but held out of the recap queue
// until the file goes quiet.
func TestIngest_HotTranscriptIsStoredButNotRecapped(t *testing.T) {
	h := newHarness(t)
	path := h.writeSession(t, "a.jsonl", "still working on this")
	now := time.Now()
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	stats, err := h.mem.Ingest(ctx, h.project, 10)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if stats.Episodes != 1 {
		t.Fatalf("the in-flight episode should still be stored: %+v", stats)
	}
	if stats.Recapped != 0 || h.refiner.calls.Load() != 0 {
		t.Errorf("paid for a recap of work that is still in flight: %+v", stats)
	}

	// Once the file goes quiet the same episode becomes eligible, in place.
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := h.mem.Ingest(ctx, h.project, 10); err != nil {
		t.Fatalf("second Ingest: %v", err)
	}
	rows, _ := h.store.RecentEpisodes(ctx, h.project.Path, 10)
	if len(rows) != 1 {
		t.Fatalf("the cooled episode duplicated: %d rows", len(rows))
	}
	if rows[0].Summary == "" {
		t.Errorf("the cooled episode was never recapped")
	}
}

// A transcript that shrank was replaced, not appended to, and a stored offset
// now points into the middle of a different file.
func TestIngest_HandlesATruncatedTranscript(t *testing.T) {
	h := newHarness(t)
	path := h.writeSession(t, "a.jsonl", "first", "second", "third")
	ctx := context.Background()

	if _, err := h.mem.Ingest(ctx, h.project, 10); err != nil {
		t.Fatalf("first Ingest: %v", err)
	}

	h.writeSession(t, "a.jsonl", "replaced")
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = st

	stats, err := h.mem.Ingest(ctx, h.project, 10)
	if err != nil {
		t.Fatalf("Ingest after truncation: %v", err)
	}
	if stats.Episodes == 0 {
		t.Errorf("a replaced transcript was skipped entirely: %+v", stats)
	}
}

func TestIngest_BoundsModelCallsPerPass(t *testing.T) {
	h := newHarness(t)
	prompts := make([]string, 10)
	for i := range prompts {
		prompts[i] = fmt.Sprintf("task number %d please", i)
	}
	h.writeSession(t, "a.jsonl", prompts...)

	stats, err := h.mem.Ingest(context.Background(), h.project, 3)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if stats.Recapped != 3 {
		t.Errorf("want exactly 3 recaps in one pass, got %d", stats.Recapped)
	}
	if stats.Remaining != 7 {
		t.Errorf("Remaining = %d, want 7 so a backfill loop knows to continue", stats.Remaining)
	}
}

func TestIngest_RequiresAResolvedPath(t *testing.T) {
	h := newHarness(t)
	if _, err := h.mem.Ingest(context.Background(), Project{}, 1); err == nil {
		t.Fatal("want an error for an empty project path")
	}
}
