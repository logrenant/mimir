package leadgen

import (
	"slices"
	"testing"
)

func TestCategoryForTypes_PrimaryTypeWins(t *testing.T) {
	// Google lists the most specific type first and repeats it as
	// primary_type; when the two disagree, primary_type is the answer.
	got, ok := CategoryForTypes("dentist", []string{"restaurant", "point_of_interest"})
	if !ok || got != CategoryHealth {
		t.Errorf("got %q, %v; want health, true", got, ok)
	}
}

func TestCategoryForTypes_UsesTypesInOrder(t *testing.T) {
	got, ok := CategoryForTypes("", []string{"establishment", "hair_care", "spa"})
	if !ok || got != CategoryBeauty {
		t.Errorf("got %q, %v; want beauty, true", got, ok)
	}
}

// The generic types every Places row carries must fall through to the model
// tier. Mapping them would turn "we do not know" into a confident wrong answer.
func TestCategoryForTypes_GenericTypesFallThrough(t *testing.T) {
	for _, generic := range []string{"point_of_interest", "establishment", "store", "food", "premise"} {
		if got, ok := CategoryForTypes(generic, []string{generic}); ok {
			t.Errorf("generic type %q resolved to %q; it must reach the model tier", generic, got)
		}
	}
}

func TestCategoryForTypes_UnknownIsAMiss(t *testing.T) {
	got, ok := CategoryForTypes("interdimensional_portal", nil)
	if ok {
		t.Errorf("got %q, true; want a miss", got)
	}
	if got != CategoryUnknown {
		t.Errorf("miss returned %q, want unknown", got)
	}
}

// Every rule must point at a real category, and none may point at unknown:
// "unknown" is what the absence of a rule already means.
func TestTypeRulesPointAtRealCategories(t *testing.T) {
	for gtype, cat := range typeRules {
		if !Valid(string(cat)) {
			t.Errorf("rule %q → %q is not in the vocabulary", gtype, cat)
		}
		if cat == CategoryUnknown {
			t.Errorf("rule %q maps to unknown; delete the rule instead", gtype)
		}
	}
}

func TestCategoriesAreStableAndClosed(t *testing.T) {
	first := Categories()
	for range 5 {
		if !slices.Equal(first, Categories()) {
			t.Fatal("Categories() order is not stable")
		}
	}
	if !slices.Contains(first, CategoryUnknown) {
		t.Error("unknown must be offered to the model: it is a real answer")
	}
	if len(CategoryStrings()) != len(first) {
		t.Error("CategoryStrings and Categories disagree")
	}

	seen := map[Category]bool{}
	for _, c := range first {
		if seen[c] {
			t.Errorf("duplicate category %q", c)
		}
		seen[c] = true
	}
}

func TestValid(t *testing.T) {
	if !Valid("health") {
		t.Error("health must be valid")
	}
	if Valid("crypto_startup") {
		t.Error("a category we never defined must not validate")
	}
	if Valid("") {
		t.Error("empty must not validate")
	}
}
