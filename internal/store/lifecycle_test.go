package store

import (
	"context"
	"sync"
	"testing"
	"time"
)

func insertRun(t *testing.T, s *Store, id, status string, queuedAt time.Time) {
	t.Helper()

	ctx := context.Background()
	row := RunRow{
		ID: id, ProjectID: "p", Prompt: "x", Status: status,
		CreatedAt: time.Now().UTC(), QueuedAt: queuedAt,
	}
	if status == RunStatusRunning {
		row.StartedAt = time.Now().UTC()
	}
	if err := s.InsertRun(ctx, row); err != nil {
		t.Fatalf("InsertRun %s: %v", id, err)
	}
}

// A row still marked running at startup belongs to a daemon that is gone. The
// queue beside it is the durable half and must survive untouched.
func TestReconcileRunningRuns_FailsOrphansAndKeepsTheQueue(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	insertRun(t, s, "orphan", RunStatusRunning, time.Time{})
	insertRun(t, s, "waiting", RunStatusQueued, time.Now().UTC())
	insertRun(t, s, "finished", RunStatusCompleted, time.Time{})

	n, err := s.ReconcileRunningRuns(ctx, "the daemon restarted", time.Now().UTC())
	if err != nil {
		t.Fatalf("ReconcileRunningRuns: %v", err)
	}
	if n != 1 {
		t.Errorf("moved %d rows, want 1", n)
	}

	statusOf := func(id string) string {
		row, found, err := s.GetRun(ctx, id)
		if err != nil || !found {
			t.Fatalf("GetRun %s: found=%v err=%v", id, found, err)
		}
		return row.Status
	}
	if got := statusOf("orphan"); got != RunStatusFailed {
		t.Errorf("orphan: got %q, want failed", got)
	}
	if got := statusOf("waiting"); got != RunStatusQueued {
		t.Errorf("queued run was disturbed: got %q", got)
	}
	if got := statusOf("finished"); got != RunStatusCompleted {
		t.Errorf("finished run was disturbed: got %q", got)
	}
}

func TestListQueuedRuns_IsTheQueueOldestFirst(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	base := time.Now().UTC()
	insertRun(t, s, "second", RunStatusQueued, base.Add(2*time.Minute))
	insertRun(t, s, "first", RunStatusQueued, base)
	// The backlog is not the queue: nothing may reach into it.
	insertRun(t, s, "parked", RunStatusBacklog, time.Time{})

	rows, err := s.ListQueuedRuns(ctx, 0)
	if err != nil {
		t.Fatalf("ListQueuedRuns: %v", err)
	}
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	if len(ids) != 2 || ids[0] != "first" || ids[1] != "second" {
		t.Fatalf("queue: got %v, want [first second]", ids)
	}
}

func TestClaimRun_RecordsTheAccountItRanOn(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	insertRun(t, s, "first", RunStatusQueued, time.Now().UTC())

	claimed, err := s.ClaimRun(ctx, "first", "acct-1", time.Now().UTC())
	if err != nil || !claimed {
		t.Fatalf("claim: claimed=%v err=%v", claimed, err)
	}
	row, _, err := s.GetRun(ctx, "first")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if row.Status != RunStatusRunning || row.StartedAt.IsZero() {
		t.Errorf("a claimed run must be running and started: %+v", row)
	}
	if row.AccountID != "acct-1" {
		t.Errorf("AccountID: got %q, want acct-1", row.AccountID)
	}

	// Claiming twice must not hand the same run out twice.
	if claimed, err := s.ClaimRun(ctx, "first", "acct-2", time.Now().UTC()); err != nil || claimed {
		t.Fatalf("second claim: claimed=%v err=%v", claimed, err)
	}
}

// The dispatcher claims from several goroutines as slots free up; two of them
// taking the same row would run one task twice.
func TestClaimRun_NeverHandsOutTheSameRunTwice(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	const runs = 12
	var ids []string
	for i := range runs {
		id := "r" + string(rune('a'+i))
		ids = append(ids, id)
		insertRun(t, s, id, RunStatusQueued, time.Now().UTC())
	}

	var (
		mu     sync.Mutex
		claims = map[string]int{}
		wg     sync.WaitGroup
	)
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, id := range ids {
				won, err := s.ClaimRun(ctx, id, "acct", time.Now().UTC())
				if err != nil || !won {
					continue
				}
				mu.Lock()
				claims[id]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(claims) != runs {
		t.Errorf("claimed %d distinct runs, want %d", len(claims), runs)
	}
	for id, n := range claims {
		if n != 1 {
			t.Errorf("run %s was claimed %d times", id, n)
		}
	}
}

// Two clicks on the same card must resolve to one winner.
func TestUpdateRunStatus_OnlyMovesFromTheExpectedState(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	insertRun(t, s, "card", RunStatusBacklog, time.Time{})

	moved, err := s.UpdateRunStatus(ctx, "card",
		RunStatusBacklog, RunStatusQueued, time.Now().UTC(), time.Time{}, "")
	if err != nil || !moved {
		t.Fatalf("first move: moved=%v err=%v", moved, err)
	}

	moved, err = s.UpdateRunStatus(ctx, "card",
		RunStatusBacklog, RunStatusQueued, time.Now().UTC(), time.Time{}, "")
	if err != nil {
		t.Fatalf("second move: %v", err)
	}
	if moved {
		t.Error("a second enqueue of the same card must not move it again")
	}
}

// The new columns and scanRun are positional; a round trip is what catches one
// of them drifting from the other.
func TestRunRow_CarriesTheBoardColumns(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	created := time.Now().UTC().Truncate(time.Second)
	want := RunRow{
		ID: "full", ProjectID: "p", Title: "fix the board",
		Prompt: "do it", Status: RunStatusBacklog,
		Attachments: `["a1","a2"]`, CreatedAt: created,
	}
	if err := s.InsertRun(ctx, want); err != nil {
		t.Fatalf("InsertRun: %v", err)
	}

	got, found, err := s.GetRun(ctx, "full")
	if err != nil || !found {
		t.Fatalf("GetRun: found=%v err=%v", found, err)
	}
	if got.Title != want.Title || got.Attachments != want.Attachments {
		t.Errorf("title/attachments did not round-trip: %+v", got)
	}
	if !got.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt: got %v, want %v", got.CreatedAt, created)
	}
	if !got.StartedAt.IsZero() || !got.QueuedAt.IsZero() {
		t.Errorf("a backlog task has neither queued nor started: %+v", got)
	}

	refs, err := s.ReferencedAttachments(ctx)
	if err != nil {
		t.Fatalf("ReferencedAttachments: %v", err)
	}
	if len(refs) != 1 || refs[0] != want.Attachments {
		t.Errorf("ReferencedAttachments: got %v", refs)
	}

	if err := s.DeleteRun(ctx, "full"); err != nil {
		t.Fatalf("DeleteRun: %v", err)
	}
	if _, found, _ := s.GetRun(ctx, "full"); found {
		t.Error("the row survived DeleteRun")
	}
}

// A backlog task has never started, so ordering by started_at alone would bury
// every one of them under the oldest finished run.
func TestListRunsByProject_SurfacesBacklogTasks(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	old := time.Now().UTC().Add(-time.Hour)
	if err := s.InsertRun(ctx, RunRow{
		ID: "done", ProjectID: "p", Prompt: "x", Status: RunStatusCompleted,
		CreatedAt: old, StartedAt: old, EndedAt: old,
	}); err != nil {
		t.Fatalf("InsertRun: %v", err)
	}
	insertRun(t, s, "fresh", RunStatusBacklog, time.Time{})

	rows, err := s.ListRunsByProject(ctx, "p", 0)
	if err != nil {
		t.Fatalf("ListRunsByProject: %v", err)
	}
	if len(rows) != 2 || rows[0].ID != "fresh" {
		t.Fatalf("a new backlog task must come first, got %+v", rows)
	}
}
