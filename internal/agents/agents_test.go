package agents

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/logrenant/mimir/internal/llm"
	"github.com/logrenant/mimir/internal/skills"
)

// fakeCompleter answers with whatever the test hands it, and counts calls so a
// test can assert that no model was spent.
type fakeCompleter struct {
	text  string
	err   error
	calls int
}

func (f *fakeCompleter) Complete(_ context.Context, _ llm.Class, _ llm.Request) (llm.Response, error) {
	f.calls++
	if f.err != nil {
		return llm.Response{}, f.err
	}
	return llm.Response{Structured: json.RawMessage(f.text)}, nil
}

// TestRegistry_EveryAgentDeclaresASkill is the mandate at its source. An agent
// with no required skill would make the dispatcher's gate a no-op for it, and
// the mandate would quietly become advisory for whichever agent forgot.
func TestRegistry_EveryAgentDeclaresASkill(t *testing.T) {
	for _, a := range Registry() {
		if len(a.RequiredSkills) == 0 {
			t.Errorf("agent %q declares no skill", a.Key)
		}
		for _, id := range a.RequiredSkills {
			if !skills.Known(id) {
				t.Errorf("agent %q requires %q, which this binary does not ship", a.Key, id)
			}
		}
	}
}

func TestRegistry_HasADefaultAgent(t *testing.T) {
	if _, ok := Lookup(Default); !ok {
		t.Fatalf("the default agent %q is not in the registry", Default)
	}
}

func TestRegistry_KeysAreUnique(t *testing.T) {
	seen := map[string]struct{}{}
	for _, k := range Keys() {
		if _, dup := seen[k]; dup {
			t.Fatalf("duplicate agent key %q", k)
		}
		seen[k] = struct{}{}
	}
}

// TestRoute_RefusesAnAgentTheRegistryDoesNotOffer: an invented key would name
// an executor the dispatcher has no entry for, so the failure would surface at
// dispatch instead of here.
func TestRoute_RefusesAnAgentTheRegistryDoesNotOffer(t *testing.T) {
	f := &fakeCompleter{text: `{"agent":"seo","why":"uydurma"}`}
	got := Route(context.Background(), f, "landing page metni", "pazarlama mesajı yaz")

	if got.Source == SourceModel {
		t.Fatal("an invented key must not be taken as the model's answer")
	}
	if _, ok := Lookup(got.Agent); !ok {
		t.Fatalf("routed to %q, which is not in the registry", got.Agent)
	}
}

func TestRoute_TakesTheModelsAnswerWhenItIsInTheRegistry(t *testing.T) {
	f := &fakeCompleter{text: `{"agent":"review","why":"bir diff inceleniyor"}`}
	got := Route(context.Background(), f, "şunu incele", "")

	if got.Agent != "review" || got.Source != SourceModel {
		t.Fatalf("got %+v, want the review agent from the model", got)
	}
}

// TestRoute_FallsBackToTheRuleTableWhenTheModelIsDown: the router must never be
// the reason a card cannot be written down (SD-6).
func TestRoute_FallsBackToTheRuleTableWhenTheModelIsDown(t *testing.T) {
	f := &fakeCompleter{err: errors.New("signed out")}
	got := Route(context.Background(), f, "İstanbul'da diş kliniği işletmeleri bul", "")

	if got.Source != SourceRules {
		t.Fatalf("source %q, want the keyword table", got.Source)
	}
	if got.Agent != "leadgen" {
		t.Fatalf("routed to %q, want leadgen", got.Agent)
	}
}

func TestRoute_FallsBackToCodingWhenNothingMatches(t *testing.T) {
	f := &fakeCompleter{err: errors.New("signed out")}
	got := Route(context.Background(), f, "zzz qqq", "")

	if got.Agent != Default || got.Source != SourceDefault {
		t.Fatalf("got %+v, want the default agent", got)
	}
}

func TestRoute_SurvivesAModelThatWrapsItsJSONInProse(t *testing.T) {
	// Structured empty, Text carrying prose: exercise the Text path.
	resp := &proseCompleter{text: "Sure! Here you go:\n{\"agent\":\"graph\"}\nHope that helps."}
	got := Route(context.Background(), resp, "bu fonksiyonu kim çağırıyor", "")

	if got.Agent != "graph" || got.Source != SourceModel {
		t.Fatalf("got %+v, want graph from the model", got)
	}
}

type proseCompleter struct{ text string }

func (p *proseCompleter) Complete(_ context.Context, _ llm.Class, _ llm.Request) (llm.Response, error) {
	return llm.Response{Text: p.text}, nil
}

// TestResolve_MakesNoModelCallWhenTheOperatorAlreadyChose is the cost rule: a
// card whose agent was picked by hand must not spend a call to confirm it.
func TestResolve_MakesNoModelCallWhenTheOperatorAlreadyChose(t *testing.T) {
	got := Resolve("marketing")
	if got.Agent != "marketing" || got.Source != SourceOperator {
		t.Fatalf("got %+v, want the operator's own choice", got)
	}
}

func TestResolve_FallsBackRatherThanFailingOnAKeyThisBinaryDoesNotKnow(t *testing.T) {
	got := Resolve("seo")
	if got.Agent != Default {
		t.Fatalf("got %q, want the default: a key from a newer client is version skew, not a lost card", got.Agent)
	}
}

// TestRoute_SkillsComeFromTheRegistryNotTheDecision: whatever picked the agent,
// the skills are the agent's contract. Letting a routing answer widen or narrow
// them would make the mandate advisory.
func TestRoute_SkillsComeFromTheRegistryNotTheDecision(t *testing.T) {
	f := &fakeCompleter{text: `{"agent":"marketing"}`}
	got := Route(context.Background(), f, "kampanya", "")

	a, _ := Lookup("marketing")
	if len(got.Skills) != len(a.RequiredSkills) {
		t.Fatalf("got skills %v, want the registry's %v", got.Skills, a.RequiredSkills)
	}
	for i := range got.Skills {
		if got.Skills[i] != a.RequiredSkills[i] {
			t.Fatalf("got skills %v, want %v", got.Skills, a.RequiredSkills)
		}
	}
}

func TestRoute_MakesNoCallForAnEmptyCard(t *testing.T) {
	f := &fakeCompleter{text: `{"agent":"review"}`}
	got := Route(context.Background(), f, "  ", "  ")

	if f.calls != 0 {
		t.Fatalf("spent %d model calls on an empty card", f.calls)
	}
	if got.Agent != Default {
		t.Fatalf("got %q, want the default", got.Agent)
	}
}

// The composed version of an agent's skills is half a draft's cache key.
// Declaring the language files unconditionally would change the string a
// source-language pass runs under, and every draft an operator has already
// approved would stop being found.
func TestCatalogSkills_TheSourceLanguageSetIsUnchanged(t *testing.T) {
	got := CatalogSkills("")
	if len(got) != 1 || got[0] != skills.ProductContent {
		t.Fatalf("kaynak dil beceri kümesi değişti: %v", got)
	}
	a, ok := Lookup(KeyCatalog)
	if !ok {
		t.Fatal("katalog ajanı kayıtta yok")
	}
	if len(a.RequiredSkills) != 1 || a.RequiredSkills[0] != skills.ProductContent {
		t.Errorf("ajanın varsayılan beceri kümesi değişti: %v", a.RequiredSkills)
	}
}

// A target language adds its own file and never drops the method.
func TestCatalogSkills_ATargetLanguageAddsItsOwnFileToTheMethod(t *testing.T) {
	for lang, want := range map[string]string{
		"ar": skills.ProductContentAR,
		"en": skills.ProductContentEN,
	} {
		got := CatalogSkills(lang)
		if len(got) != 2 || got[0] != skills.ProductContent || got[1] != want {
			t.Errorf("%s: beklenen [%s %s], alınan %v", lang, skills.ProductContent, want, got)
		}
		for _, id := range got {
			if !skills.Known(id) {
				t.Errorf("%s: bu ikili sevk edilmeyen bir beceri adlandırıyor: %s", lang, id)
			}
		}
	}
	// An unknown language is the source language, not a missing skill: a card
	// from a newer client is version skew, and refusing it would lose the run.
	if got := CatalogSkills("de"); len(got) != 1 {
		t.Errorf("bilinmeyen dil kaynak dile düşmedi: %v", got)
	}
}
