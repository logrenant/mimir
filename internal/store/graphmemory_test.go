package store

import (
	"context"
	"testing"
	"time"
)

func TestRecordGraphResult_RefusesAnOutcomeItDoesNotKnow(t *testing.T) {
	s := openTestStore(t)
	err := s.RecordGraphResult(context.Background(), GraphResult{
		Question: "x", Outcome: "maybe",
	})
	if err == nil {
		t.Fatal("an unknown outcome must be refused, not stored as a third meaning")
	}
}

// TestGraphLessons_PrefersNodesCorroboratedMoreThanOnce.
//
// One enthusiastic session is not evidence. A node needs agreement from more
// than one question before the aggregate is willing to recommend it.
func TestGraphLessons_PrefersNodesCorroboratedMoreThanOnce(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	now := time.Now().UTC()
	record := func(node, outcome string) {
		t.Helper()
		if err := s.RecordGraphResult(ctx, GraphResult{
			ProjectPath: "/repo", Question: "q", Nodes: []string{node},
			Outcome: outcome, At: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	record("solid", GraphUseful)
	record("solid", GraphUseful)
	record("once", GraphUseful)

	got, err := s.GraphLessons(ctx, "/repo", 30, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].NodeID != "solid" {
		t.Fatalf("lessons = %+v, want only the corroborated node", got)
	}
	if got[0].Useful != 2 {
		t.Fatalf("useful = %d, want the raw count under the score", got[0].Useful)
	}
}

// TestGraphLessons_DecaysOldSignal — a node that answered well last year must
// not outrank one that answered well last week.
func TestGraphLessons_DecaysOldSignal(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	now := time.Now().UTC()
	for i := 0; i < 2; i++ {
		if err := s.RecordGraphResult(ctx, GraphResult{
			ProjectPath: "/repo", Nodes: []string{"old"}, Question: "q",
			Outcome: GraphUseful, At: now.AddDate(0, 0, -365),
		}); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordGraphResult(ctx, GraphResult{
			ProjectPath: "/repo", Nodes: []string{"recent"}, Question: "q",
			Outcome: GraphUseful, At: now,
		}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.GraphLessons(ctx, "/repo", 30, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 2 || got[0].NodeID != "recent" {
		t.Fatalf("lessons = %+v, want the recent node first", got)
	}
	if got[0].Score <= got[1].Score {
		t.Fatalf("a year-old signal scored %v against this week's %v", got[1].Score, got[0].Score)
	}
}

// TestGraphLessons_DeadEndsCountAgainst.
func TestGraphLessons_DeadEndsCountAgainst(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	now := time.Now().UTC()

	for _, outcome := range []string{GraphUseful, GraphDeadEnd, GraphDeadEnd} {
		if err := s.RecordGraphResult(ctx, GraphResult{
			ProjectPath: "/repo", Nodes: []string{"misleading"}, Question: "q",
			Outcome: outcome, At: now,
		}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.GraphLessons(ctx, "/repo", 30, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Score >= 0 {
		t.Fatalf("lessons = %+v, want a negative score for a node that mostly misled", got)
	}
}

// TestGraphLessons_KeepsACorrectionEvenWithoutCorroboration — the correction is
// the most valuable row in the table and the one nobody writes twice.
func TestGraphLessons_KeepsACorrectionEvenWithoutCorroboration(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.RecordGraphResult(ctx, GraphResult{
		ProjectPath: "/repo", Nodes: []string{"wrong"}, Question: "q",
		Outcome: GraphCorrected, Correction: "aslında dispatchOne çağırıyor",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.GraphLessons(ctx, "/repo", 30, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Corrections) != 1 {
		t.Fatalf("lessons = %+v, want the correction kept", got)
	}
	// And it is not scored as a failure: a well-corrected node should not be
	// punished for having been wrong once.
	if got[0].Score != 0 {
		t.Fatalf("score = %v, want a correction to be neither a vote for nor against", got[0].Score)
	}
}

// TestGraphLessons_ScopesToTheProject.
func TestGraphLessons_ScopesToTheProject(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	now := time.Now().UTC()

	for i := 0; i < 2; i++ {
		if err := s.RecordGraphResult(ctx, GraphResult{
			ProjectPath: "/other", Nodes: []string{"elsewhere"}, Question: "q",
			Outcome: GraphUseful, At: now,
		}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.GraphLessons(ctx, "/repo", 30, 2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("lessons = %+v, want another project's history left out", got)
	}
}
