package store

import (
	"context"
	"strconv"
	"testing"
)

func TestCategorizationRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.PutCategorization(ctx, "place-1", "leadgen-v1", "health", "rule"); err != nil {
		t.Fatalf("PutCategorization: %v", err)
	}

	got, err := s.GetCategorizations(ctx, []string{"place-1", "place-missing"}, "leadgen-v1")
	if err != nil {
		t.Fatalf("GetCategorizations: %v", err)
	}
	if got["place-1"] != "health" {
		t.Errorf("category = %q, want health", got["place-1"])
	}
	if _, ok := got["place-missing"]; ok {
		t.Error("a company with no row must be absent, not blank")
	}
}

// A version bump is the only invalidation this table has, so it must read as a
// miss while leaving the previous taxonomy's answer intact.
func TestCategorizationVersionIsolatesRows(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.PutCategorization(ctx, "place-1", "leadgen-v1", "health", "rule"); err != nil {
		t.Fatalf("PutCategorization: %v", err)
	}

	got, err := s.GetCategorizations(ctx, []string{"place-1"}, "leadgen-v2")
	if err != nil {
		t.Fatalf("GetCategorizations: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("v2 read returned %v, want a miss", got)
	}

	if err := s.PutCategorization(ctx, "place-1", "leadgen-v2", "beauty", "model"); err != nil {
		t.Fatalf("PutCategorization: %v", err)
	}

	old, err := s.GetCategorizations(ctx, []string{"place-1"}, "leadgen-v1")
	if err != nil {
		t.Fatalf("GetCategorizations: %v", err)
	}
	if old["place-1"] != "health" {
		t.Errorf("v1 row = %q after writing v2, want it untouched", old["place-1"])
	}
}

func TestCategorizationOverwritesSameVersion(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.PutCategorization(ctx, "place-1", "leadgen-v1", "retail", "model"); err != nil {
		t.Fatalf("PutCategorization: %v", err)
	}
	if err := s.PutCategorization(ctx, "place-1", "leadgen-v1", "health", "rule"); err != nil {
		t.Fatalf("PutCategorization: %v", err)
	}

	got, err := s.GetCategorizations(ctx, []string{"place-1"}, "leadgen-v1")
	if err != nil {
		t.Fatalf("GetCategorizations: %v", err)
	}
	if got["place-1"] != "health" {
		t.Errorf("category = %q, want the rewritten value health", got["place-1"])
	}
}

// The whole point of the batch read is one query for a region, so it must
// survive a list longer than the chunk size.
func TestCategorizationBatchReadIsChunked(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	ids := make([]string, 0, 1200)
	for i := range 1200 {
		id := "place-" + strconv.Itoa(i)
		ids = append(ids, id)
		if i%2 == 0 {
			if err := s.PutCategorization(ctx, id, "leadgen-v1", "retail", "rule"); err != nil {
				t.Fatalf("PutCategorization: %v", err)
			}
		}
	}

	got, err := s.GetCategorizations(ctx, ids, "leadgen-v1")
	if err != nil {
		t.Fatalf("GetCategorizations: %v", err)
	}
	if len(got) != 600 {
		t.Errorf("got %d rows, want 600", len(got))
	}
}

func TestCategorizationNilStore(t *testing.T) {
	ctx := context.Background()
	var s *Store

	if err := s.PutCategorization(ctx, "place-1", "leadgen-v1", "health", "rule"); err != nil {
		t.Errorf("PutCategorization on nil store: %v", err)
	}
	got, err := s.GetCategorizations(ctx, []string{"place-1"}, "leadgen-v1")
	if err != nil {
		t.Errorf("GetCategorizations on nil store: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("nil store returned %v, want empty", got)
	}
}
