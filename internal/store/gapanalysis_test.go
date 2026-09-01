package store

import (
	"context"
	"testing"
)

func sampleGapKey() GapAnalysisKey {
	return GapAnalysisKey{
		Region:         "Kadikoy",
		Category:       "beauty",
		PromptVersion:  "gap-v1",
		CompanySetHash: "abc123",
	}
}

func TestGapAnalysisRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	key := sampleGapKey()
	want := GapAnalysis{Analysis: "- few have websites\n- thin review counts", CompanyCount: 7, Truncated: true}

	if err := s.PutGapAnalysis(ctx, key, want); err != nil {
		t.Fatalf("PutGapAnalysis: %v", err)
	}

	got, ok, err := s.GetGapAnalysis(ctx, key)
	if err != nil {
		t.Fatalf("GetGapAnalysis: %v", err)
	}
	if !ok {
		t.Fatal("want a hit for the key just written")
	}
	if got != want {
		t.Fatalf("round-trip mismatch: got %+v want %+v", got, want)
	}
}

func TestGapAnalysisMissForEachKeyComponent(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	base := sampleGapKey()
	if err := s.PutGapAnalysis(ctx, base, GapAnalysis{Analysis: "x", CompanyCount: 1}); err != nil {
		t.Fatalf("PutGapAnalysis: %v", err)
	}

	for name, mutate := range map[string]func(k *GapAnalysisKey){
		"region":           func(k *GapAnalysisKey) { k.Region = "Besiktas" },
		"category":         func(k *GapAnalysisKey) { k.Category = "health" },
		"prompt_version":   func(k *GapAnalysisKey) { k.PromptVersion = "gap-v2" },
		"company_set_hash": func(k *GapAnalysisKey) { k.CompanySetHash = "different" },
	} {
		k := base
		mutate(&k)
		_, ok, err := s.GetGapAnalysis(ctx, k)
		if err != nil {
			t.Fatalf("%s: GetGapAnalysis: %v", name, err)
		}
		if ok {
			t.Errorf("%s: changing this key component should have missed", name)
		}
	}
}

func TestGapAnalysisOverwritesSameKey(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	key := sampleGapKey()
	if err := s.PutGapAnalysis(ctx, key, GapAnalysis{Analysis: "first", CompanyCount: 1}); err != nil {
		t.Fatalf("PutGapAnalysis first: %v", err)
	}
	if err := s.PutGapAnalysis(ctx, key, GapAnalysis{Analysis: "second", CompanyCount: 2}); err != nil {
		t.Fatalf("PutGapAnalysis second: %v", err)
	}

	got, ok, err := s.GetGapAnalysis(ctx, key)
	if err != nil || !ok {
		t.Fatalf("GetGapAnalysis: ok=%v err=%v", ok, err)
	}
	if got.Analysis != "second" || got.CompanyCount != 2 {
		t.Fatalf("re-write did not replace: %+v", got)
	}
}

// SD-6: a nil Store is a slower run, never a failed one.
func TestGapAnalysisNilStoreTolerant(t *testing.T) {
	ctx := context.Background()
	var s *Store

	if err := s.PutGapAnalysis(ctx, sampleGapKey(), GapAnalysis{Analysis: "x"}); err != nil {
		t.Errorf("PutGapAnalysis on nil Store: %v", err)
	}
	_, ok, err := s.GetGapAnalysis(ctx, sampleGapKey())
	if err != nil {
		t.Errorf("GetGapAnalysis on nil Store: %v", err)
	}
	if ok {
		t.Error("nil Store must always miss")
	}
}

// An incomplete key and an empty analysis are both no-ops, not rows.
func TestGapAnalysisRejectsIncompleteInput(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	partial := GapAnalysisKey{Region: "Kadikoy", Category: "beauty"} // no version, no hash
	if err := s.PutGapAnalysis(ctx, partial, GapAnalysis{Analysis: "x"}); err != nil {
		t.Fatalf("PutGapAnalysis with a partial key should be a silent no-op: %v", err)
	}
	if _, ok, _ := s.GetGapAnalysis(ctx, partial); ok {
		t.Error("a partial key must never read as a hit")
	}

	full := sampleGapKey()
	if err := s.PutGapAnalysis(ctx, full, GapAnalysis{Analysis: ""}); err != nil {
		t.Fatalf("PutGapAnalysis with empty analysis: %v", err)
	}
	if _, ok, _ := s.GetGapAnalysis(ctx, full); ok {
		t.Error("an empty analysis must not be stored")
	}
}
