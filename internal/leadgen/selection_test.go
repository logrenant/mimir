package leadgen

import (
	"context"
	"testing"

	"github.com/logrenant/mimir/internal/llm"
	"github.com/logrenant/mimir/internal/maps"
	"github.com/logrenant/mimir/internal/refine"
)

// The selection must reach the model call, or the picker is decoration.
func TestCategorizer_WithSendsTheSelectionToTheClassifier(t *testing.T) {
	var seen llm.Selection
	classifier := &fakeClassifier{answer: func(in refine.ClassifyInput) (refine.ClassifyOutput, error) {
		seen = in.Selection
		out := refine.ClassifyOutput{Assignments: map[string]string{}}
		for _, item := range in.Items {
			out.Assignments[item.ID] = string(CategoryRetail)
		}
		return out, nil
	}}

	sel := llm.Selection{Provider: "claude", Model: "claude-opus-5"}
	c := New(testConfig(), classifier, nil).With(sel)

	if _, _, err := c.Categorize(context.Background(), []maps.Company{company("p1", "", "establishment")}); err != nil {
		t.Fatalf("Categorize: %v", err)
	}
	if seen != sel {
		t.Errorf("classifier saw %+v, want %+v", seen, sel)
	}
}

// A cached answer belongs to the model that wrote it. Serving it to a run that
// chose a different one would make the picker look like it did nothing.
func TestCategorizer_ADifferentModelDoesNotReadTheDefaultsCache(t *testing.T) {
	s := newFakeStore()
	classifier := &fakeClassifier{}
	base := New(testConfig(), classifier, s)

	cs := []maps.Company{company("p1", "", "establishment")}
	if _, _, err := base.Categorize(context.Background(), cs); err != nil {
		t.Fatalf("Categorize: %v", err)
	}
	if classifier.calls != 1 {
		t.Fatalf("model called %d times on the first run, want 1", classifier.calls)
	}

	// Same company, same version, different model: a miss, and a second call.
	chosen := base.With(llm.Selection{Provider: "agy", Model: "gemini-3.1-pro-high"})
	if _, _, err := chosen.Categorize(context.Background(), cs); err != nil {
		t.Fatalf("Categorize: %v", err)
	}
	if classifier.calls != 2 {
		t.Errorf("model called %d times, want 2 — the selected model must not read the default's cache", classifier.calls)
	}

	// And the default keeps reading its own entries, so nothing cached before
	// the picker existed was invalidated by it.
	if _, _, err := base.Categorize(context.Background(), cs); err != nil {
		t.Fatalf("Categorize: %v", err)
	}
	if classifier.calls != 2 {
		t.Errorf("model called %d times, want the routed run to still hit its cache", classifier.calls)
	}
}

// With(zero) must be the identity, not a copy with an empty selection: the
// zero value is how every caller that offers no picker calls this.
func TestWith_TheZeroSelectionChangesNothing(t *testing.T) {
	c := New(testConfig(), &fakeClassifier{}, nil)
	if c.With(llm.Selection{}) != c {
		t.Error("Categorizer.With(zero) should return the same stage")
	}

	g := NewGapAnalyzer(testConfig(), nil, nil)
	if g.With(llm.Selection{}) != g {
		t.Error("GapAnalyzerRunner.With(zero) should return the same stage")
	}

	e := NewEmailRunner(testConfig(), nil, nil)
	if e.With(llm.Selection{}) != e {
		t.Error("EmailRunner.With(zero) should return the same stage")
	}
}

// A nil stage is a legal pipeline — the stage simply does not run — so binding
// a selection to one must not panic before the nil check downstream.
func TestWith_TolerateANilStage(t *testing.T) {
	var c *Categorizer
	var g *GapAnalyzerRunner
	var e *EmailRunner

	sel := llm.Selection{Provider: "agy", Model: "gemini-3.8-flash-low"}
	if c.With(sel) != nil || g.With(sel) != nil || e.With(sel) != nil {
		t.Error("binding a selection to a nil stage should stay nil")
	}
}
