package store

import (
	"context"
	"testing"
	"time"
)

func TestCodingRunStats(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	// A nil store, and an empty table, are both a zero rollup — never an error.
	var nilStore *Store
	if _, err := nilStore.CodingRunStats(ctx); err != nil {
		t.Fatalf("nil Store: %v", err)
	}
	if st, err := s.CodingRunStats(ctx); err != nil || st.Total != 0 {
		t.Fatalf("empty table: %+v err=%v", st, err)
	}

	insert := func(id, status string, cost float64) {
		if err := s.InsertRun(ctx, RunRow{
			ID: id, ProjectID: "p", Prompt: "x", Status: status,
			CostUSD: cost, StartedAt: time.Now(),
		}); err != nil {
			t.Fatalf("InsertRun %s: %v", id, err)
		}
	}
	insert("r1", RunStatusCompleted, 0.12)
	insert("r2", RunStatusFailed, 0.03)
	insert("r3", RunStatusRunning, 0)

	st, err := s.CodingRunStats(ctx)
	if err != nil {
		t.Fatalf("CodingRunStats: %v", err)
	}
	if st.Total != 3 {
		t.Errorf("total = %d, want 3", st.Total)
	}
	if st.Running != 1 {
		t.Errorf("running = %d, want 1", st.Running)
	}
	if st.Failed != 1 {
		t.Errorf("failed = %d, want 1", st.Failed)
	}
	if st.CostUSD < 0.149 || st.CostUSD > 0.151 {
		t.Errorf("cost_usd = %v, want ~0.15", st.CostUSD)
	}
}
