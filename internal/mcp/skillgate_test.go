package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// skilledTool is a metadata-only tool that declares a skill.
type skilledTool struct {
	name       string
	skills     []string
	budget     int
	skillAllow int
}

func (t skilledTool) Name() string        { return t.name }
func (t skilledTool) Description() string { return "test" }
func (t skilledTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object"}`)
}
func (t skilledTool) Handle(context.Context, json.RawMessage) (any, error) { return nil, nil }
func (t skilledTool) Skills() []string                                     { return t.skills }
func (t skilledTool) SkillBudgetTokens() int                               { return t.skillAllow }

type skilledResponse struct {
	Answer string `json:"answer"`
	budget int
}

func (r skilledResponse) MetadataOnly() bool    { return true }
func (r skilledResponse) SizeBudgetTokens() int { return r.budget }

// fakeSkills answers with whatever the test set up.
type fakeSkills struct {
	bodies   map[string]string
	versions map[string]string
}

func (f fakeSkills) Body(id string) (string, string) {
	return f.bodies[id], f.versions[id]
}

// TestFinalize_AttachesASkillBodyOnceAndThenOnlyItsVersion is the whole point
// of the gate being per process: the consumer reads the instructions once, and
// every later call pays only for a version string.
func TestFinalize_AttachesASkillBodyOnceAndThenOnlyItsVersion(t *testing.T) {
	gate := newSkillGate(fakeSkills{
		bodies:   map[string]string{"graph-query": "grafiğin kendi kelimeleriyle sor"},
		versions: map[string]string{"graph-query": "aaaa1111"},
	})
	tool := skilledTool{name: "graph_query", skills: []string{"graph-query"}, budget: 500, skillAllow: 600}

	first, err := finalizeWithSkills(tool, skilledResponse{Answer: "x", budget: 500}, gate)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(t, first), "grafiğin kendi kelimeleriyle sor") {
		t.Fatal("the first response must carry the skill body")
	}

	second, err := finalizeWithSkills(tool, skilledResponse{Answer: "y", budget: 500}, gate)
	if err != nil {
		t.Fatal(err)
	}
	body := mustJSON(t, second)
	if strings.Contains(body, "grafiğin kendi kelimeleriyle sor") {
		t.Fatal("the second response repeated the body instead of only its version")
	}
	if !strings.Contains(body, "aaaa1111") {
		t.Fatal("the second response should still name the version the answer was produced under")
	}
}

// TestFinalize_ResendsTheBodyWhenTheOperatorEditedItMidSession: the version is
// the handle on "the instructions changed", and a consumer still holding the
// old text would be following instructions nobody has any more.
func TestFinalize_ResendsTheBodyWhenTheOperatorEditedItMidSession(t *testing.T) {
	src := &editableSkills{body: "ilk hâli", version: "aaaa1111"}
	gate := newSkillGate(src)
	tool := skilledTool{name: "graph_query", skills: []string{"graph-query"}, budget: 500, skillAllow: 600}

	if _, err := finalizeWithSkills(tool, skilledResponse{budget: 500}, gate); err != nil {
		t.Fatal(err)
	}
	src.body, src.version = "ikinci hâli", "bbbb2222"

	after, err := finalizeWithSkills(tool, skilledResponse{budget: 500}, gate)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mustJSON(t, after), "ikinci hâli") {
		t.Fatal("an edited skill must be sent again")
	}
}

type editableSkills struct{ body, version string }

func (e *editableSkills) Body(string) (string, string) { return e.body, e.version }

// TestFinalize_FailsClosedWhenADeclaredSkillIsMissing is the mandate: an answer
// produced without the instructions it was supposed to follow is worse than no
// answer, because nothing downstream can tell the difference.
func TestFinalize_FailsClosedWhenADeclaredSkillIsMissing(t *testing.T) {
	gate := newSkillGate(fakeSkills{bodies: map[string]string{}, versions: map[string]string{}})
	tool := skilledTool{name: "graph_query", skills: []string{"graph-query"}, budget: 500, skillAllow: 600}

	if _, err := finalizeWithSkills(tool, skilledResponse{budget: 500}, gate); !errors.Is(err, ErrSkillUnavailable) {
		t.Fatalf("err = %v, want ErrSkillUnavailable", err)
	}
}

// TestFinalize_FailsClosedWhenTheProcessHasNoSkillSource: a wiring bug must not
// read as a process where the mandate happens not to apply.
func TestFinalize_FailsClosedWhenTheProcessHasNoSkillSource(t *testing.T) {
	tool := skilledTool{name: "graph_query", skills: []string{"graph-query"}, budget: 500, skillAllow: 600}

	if _, err := finalizeWithSkills(tool, skilledResponse{budget: 500}, nil); !errors.Is(err, ErrSkillUnavailable) {
		t.Fatalf("err = %v, want ErrSkillUnavailable", err)
	}
}

// TestFinalize_SkillBodyIsAddedToTheBudgetNotHiddenFromIt: the attachment is
// measured. A skill must neither shrink the answer it rides on (so the
// allowance is added) nor be unbounded (so it is still counted).
func TestFinalize_SkillBodyIsAddedToTheBudgetNotHiddenFromIt(t *testing.T) {
	big := strings.Repeat("k", 4000) // ~1000 tokens
	gate := newSkillGate(fakeSkills{
		bodies:   map[string]string{"graph-query": big},
		versions: map[string]string{"graph-query": "aaaa1111"},
	})

	// An allowance smaller than the body: the call must fail rather than pass
	// a skill nobody budgeted for.
	tight := skilledTool{name: "graph_query", skills: []string{"graph-query"}, budget: 100, skillAllow: 200}
	if _, err := finalizeWithSkills(tight, skilledResponse{budget: 100}, gate); !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("err = %v, want ErrResponseTooLarge", err)
	}

	// A generous allowance: the same body passes, and the answer's own 100
	// tokens were not eaten to make room for it.
	gate2 := newSkillGate(fakeSkills{
		bodies:   map[string]string{"graph-query": big},
		versions: map[string]string{"graph-query": "aaaa1111"},
	})
	roomy := skilledTool{name: "graph_query", skills: []string{"graph-query"}, budget: 100, skillAllow: 1200}
	if _, err := finalizeWithSkills(roomy, skilledResponse{budget: 100}, gate2); err != nil {
		t.Fatalf("a budgeted skill should pass: %v", err)
	}
}

// TestFinalize_LeavesAnUnskilledToolAlone — the gate is opt-in per tool.
func TestFinalize_LeavesAnUnskilledToolAlone(t *testing.T) {
	gate := newSkillGate(fakeSkills{})
	v, err := finalizeWithSkills(fakeGateTool{name: "web_search"}, skilledResponse{Answer: "x", budget: 500}, gate)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(mustJSON(t, v), `"skill"`) {
		t.Fatal("a tool that declares no skill must not grow a skill field")
	}
}

// TestFinalize_StillRefusesAnUnrefinedPayloadThatCarriesASkill: the skill gate
// is an addition to the isolation choke-point, never a way around it.
func TestFinalize_StillRefusesAnUnrefinedPayloadThatCarriesASkill(t *testing.T) {
	gate := newSkillGate(fakeSkills{
		bodies:   map[string]string{"graph-query": "kural"},
		versions: map[string]string{"graph-query": "aaaa1111"},
	})
	tool := skilledTool{name: "graph_query", skills: []string{"graph-query"}, budget: 500, skillAllow: 600}

	if _, err := finalizeWithSkills(tool, struct{ X string }{X: "raw"}, gate); !errors.Is(err, ErrIsolationViolation) {
		t.Fatalf("err = %v, want ErrIsolationViolation", err)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
