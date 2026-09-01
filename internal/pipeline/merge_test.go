package pipeline

import (
	"testing"
)

func TestMergeResults_Dedup(t *testing.T) {
	pages := []RefinedPage{
		{URL: "u1", Title: "t1", Markdown: "- the goat is a farm animal\n- goats eat grass"},
		{URL: "u2", Title: "t2", Markdown: "- The Goat is a farm animal\n- they climb mountains\n* GOATS EAT GRASS"},
	}

	brief := mergeResults(pages, nil, 1000)

	// we expect exactly 3 points
	if len(brief.KeyPoints) != 3 {
		t.Errorf("expected 3 keypoints after dedup, got %d: %v", len(brief.KeyPoints), brief.KeyPoints)
	}

	// first occurrence preserved
	if brief.KeyPoints[0] != "- the goat is a farm animal" {
		t.Errorf("unexpected point 0: %q", brief.KeyPoints[0])
	}
}

func TestMergeResults_Truncate(t *testing.T) {
	pages := []RefinedPage{
		{URL: "u1", Title: "t1", Markdown: "- point 1\n- point 2 is extremely long and will exceed the budget"},
	}

	// budget of 5 tokens => 20 chars
	brief := mergeResults(pages, nil, 5)

	if !brief.Truncated {
		t.Errorf("expected truncated to be true")
	}
	if len(brief.KeyPoints) != 1 {
		t.Errorf("expected 1 point, got %d", len(brief.KeyPoints))
	}
	if brief.KeyPoints[0] != "- point 1" {
		t.Errorf("unexpected point: %q", brief.KeyPoints[0])
	}
}
