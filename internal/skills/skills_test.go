package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bodyCeilingTokens mirrors config.SkillBodyMaxTokens. Duplicated as a literal
// rather than imported, because internal/config importing this package would
// be the wrong direction and this test must not be the reason it does.
const bodyCeilingTokens = 600

// TestShippedSkills_FitTheirCeiling guards the MCP gate: a skill body is
// attached to a tool response and counted against that tool's budget, so a
// skill somebody grew by a page would start failing tool calls that used to
// work — at the choke-point, far from the edit that caused it.
func TestShippedSkills_FitTheirCeiling(t *testing.T) {
	for _, id := range IDs() {
		body, err := Default(id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if tokens := len(body) / 4; tokens > bodyCeilingTokens {
			t.Errorf("%s is ~%d tokens, ceiling is %d", id, tokens, bodyCeilingTokens)
		}
		if strings.TrimSpace(body) == "" {
			t.Errorf("%s ships an empty body", id)
		}
	}
}

func TestDefault_RefusesASkillThatWasNeverShipped(t *testing.T) {
	if _, err := Default("seo"); err == nil {
		t.Fatal("expected an error for an unshipped skill")
	}
}

func TestSkill_SeedsTheFileOnFirstRead(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	sk, err := s.Skill(CodeReview)
	if err != nil {
		t.Fatal(err)
	}
	if !sk.IsDefault {
		t.Error("a freshly seeded skill should be the default")
	}
	if _, err := os.Stat(filepath.Join(dir, "code-review.md")); err != nil {
		t.Errorf("reading a skill should have written it to disk: %v", err)
	}
}

func TestPutSkill_TreatsAnEmptyBodyAsAReset(t *testing.T) {
	s := New(t.TempDir())
	if _, err := s.PutSkill(Marketing, "yalnız bunu yaz"); err != nil {
		t.Fatal(err)
	}
	sk, err := s.PutSkill(Marketing, "   \n  ")
	if err != nil {
		t.Fatal(err)
	}
	if !sk.IsDefault {
		t.Error("clearing the box should restore the shipped default, not write an empty skill")
	}
}

func TestVersion_ChangesWithTheBody(t *testing.T) {
	s := New(t.TempDir())
	before, err := s.Skill(GraphQuery)
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.PutSkill(GraphQuery, before.Body+"\n\nEk kural: yönü kontrol et.")
	if err != nil {
		t.Fatal(err)
	}
	if before.Version == after.Version {
		t.Fatal("editing a skill must change its version, or everything cached under it goes stale silently")
	}
}

// TestBody_FallsBackToTheShippedDefaultWhenTheFileIsUnreadable is the split the
// package doc describes: an unreadable operator file still runs the job under
// the shipped instructions; only an unknown skill returns empty.
func TestBody_FallsBackToTheShippedDefaultWhenTheFileIsUnreadable(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if _, err := s.Skill(CodeReview); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "code-review.md")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	body, version := s.Body(CodeReview)
	if strings.TrimSpace(body) == "" || version == "" {
		t.Fatal("an empty file should fall back to the shipped body, not leave the job unguided")
	}
}

func TestBody_IsEmptyForASkillThatDoesNotExist(t *testing.T) {
	s := New(t.TempDir())
	if body, _ := s.Body("seo"); body != "" {
		t.Fatal("an unknown skill must return empty, which is what the mandate gates on")
	}
}

// TestCompose_RefusesWhenAnyRequestedSkillIsUnknown is the mandate itself: the
// composition either carries every skill asked for or it carries none, so a
// caller cannot half-satisfy a contract and run anyway.
func TestCompose_RefusesWhenAnyRequestedSkillIsUnknown(t *testing.T) {
	s := New(t.TempDir())
	if _, _, ok := s.Compose([]string{CodeReview, "seo"}); ok {
		t.Fatal("an unknown skill in the set must refuse the whole composition")
	}
	if _, _, ok := s.Compose(nil); ok {
		t.Fatal("an empty set must refuse: no skills is not a satisfied mandate")
	}
}

func TestCompose_IsOrderIndependent(t *testing.T) {
	s := New(t.TempDir())
	a, va, ok := s.Compose([]string{CodeReview, Marketing})
	if !ok {
		t.Fatal("expected a composition")
	}
	b, vb, ok := s.Compose([]string{Marketing, CodeReview})
	if !ok {
		t.Fatal("expected a composition")
	}
	if a != b || va != vb {
		t.Fatal("two cards naming the same skills must hash the same, whatever order they listed them in")
	}
}

// TestStore_NilIsReadableSoAMisconfiguredDaemonStillKnowsItsSkills mirrors the
// store package's rule that a nil receiver is a miss, not a panic.
func TestStore_NilIsReadableSoAMisconfiguredDaemonStillKnowsItsSkills(t *testing.T) {
	var s *Store
	sk, err := s.Skill(LeadOutreach)
	if err != nil {
		t.Fatalf("a nil store should still serve the shipped default: %v", err)
	}
	if !sk.IsDefault {
		t.Error("expected the shipped body")
	}
}
