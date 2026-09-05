package store

import (
	"context"
	"testing"
	"time"
)

func TestRateLimitLog_NewestFirst(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	rows := []RateLimitRow{
		{At: now.Add(-2 * time.Hour), Phase: RateLimitPhaseRun,
			AccountID: "acct", RunID: "run-1", ResetsAt: now, Detail: "usage limit"},
		{At: now.Add(-time.Hour), Phase: RateLimitPhaseDispatch,
			AccountID: "acct", RunID: "run-2", ResetsAt: now},
		{At: now, Phase: RateLimitPhaseResumed, AccountID: "acct"},
	}
	for _, row := range rows {
		if err := s.InsertRateLimitEvent(ctx, row); err != nil {
			t.Fatalf("InsertRateLimitEvent: %v", err)
		}
	}

	got, err := s.ListRateLimitEvents(ctx, 10)
	if err != nil {
		t.Fatalf("ListRateLimitEvents: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("rows: got %d, want 3", len(got))
	}
	// Newest first is what restoreHolds relies on: the first row it sees for
	// an account is the one that decides whether the pause is still in force.
	if got[0].Phase != RateLimitPhaseResumed || got[2].Phase != RateLimitPhaseRun {
		t.Errorf("order: got %s … %s", got[0].Phase, got[2].Phase)
	}
	if got[2].RunID != "run-1" || got[2].Detail != "usage limit" {
		t.Errorf("row not round-tripped: %+v", got[2])
	}
	if !got[2].ResetsAt.Equal(now) {
		t.Errorf("resets at: got %v, want %v", got[2].ResetsAt, now)
	}
}

func TestParkRun_OnlyFromRunning(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	seed := func(t *testing.T, id, status string) {
		t.Helper()
		if err := s.InsertRun(ctx, RunRow{
			ID: id, ProjectID: "proj", Prompt: "do it", Status: status,
			CreatedAt: now, QueuedAt: now, StartedAt: now,
		}); err != nil {
			t.Fatalf("InsertRun: %v", err)
		}
	}

	seed(t, "run-running", RunStatusRunning)
	moved, err := s.ParkRun(ctx, "run-running", "sess-1", now, "the budget ran out")
	if err != nil {
		t.Fatalf("ParkRun: %v", err)
	}
	if !moved {
		t.Fatal("a running run was not parked")
	}
	got, _, err := s.GetRun(ctx, "run-running")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.Status != RunStatusQueued {
		t.Errorf("status: got %q, want %q", got.Status, RunStatusQueued)
	}
	// The session is what makes the resume a continuation; the row had never
	// seen it, because a run only learns it from the CLI's first line.
	if got.SessionID != "sess-1" {
		t.Errorf("session id: got %q, want sess-1", got.SessionID)
	}
	if got.Error != "the budget ran out" {
		t.Errorf("reason not left on the card: %q", got.Error)
	}
	if !got.EndedAt.IsZero() {
		t.Errorf("a parked run has not ended: %v", got.EndedAt)
	}

	// An empty session must not erase the one the row already had.
	if _, err := s.ParkRun(ctx, "run-running", "", now, "again"); err != nil {
		t.Fatalf("ParkRun: %v", err)
	}
	if again, _, _ := s.GetRun(ctx, "run-running"); again.SessionID != "sess-1" {
		t.Errorf("an empty session id erased the row's own: %q", again.SessionID)
	}

	// Every other state is somebody else's transition. A completed run must
	// not be dragged back into the queue by a late report.
	for _, status := range []string{RunStatusCompleted, RunStatusQueued, RunStatusBacklog} {
		id := "run-" + status
		seed(t, id, status)
		moved, err := s.ParkRun(ctx, id, "sess-2", now, "nope")
		if err != nil {
			t.Fatalf("ParkRun(%s): %v", status, err)
		}
		if moved {
			t.Errorf("a %s run was parked", status)
		}
	}
}
