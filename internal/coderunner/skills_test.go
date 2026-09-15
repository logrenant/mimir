package coderunner

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/agents"
	"github.com/logrenant/mimir/internal/skills"
)

// noSkills is an operator whose skill files have gone: the source answers, and
// what it answers with is nothing.
type noSkills struct{}

func (noSkills) Body(string) (string, string) { return "", "" }

// TestCreate_RefusesACardWhoseSkillWillNotLoad is the mandate at the door. The
// operator is still looking at the thing they pressed, which is the only place
// this can be said usefully.
func TestCreate_RefusesACardWhoseSkillWillNotLoad(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0))
	h.runner.SetSkills(noSkills{})

	_, err := h.runner.Create(context.Background(), CreateRequest{
		ProjectID: h.projectID,
		Prompt:    "do the thing",
	})
	if !errors.Is(err, ErrSkillUnavailable) {
		t.Fatalf("err = %v, want ErrSkillUnavailable", err)
	}
}

// TestEnqueue_RefusesACardWhoseSkillWentMissingAfterItWasWritten: the card was
// legal when it was created and is not any more.
func TestEnqueue_RefusesACardWhoseSkillWentMissingAfterItWasWritten(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0))

	run, err := h.runner.Create(context.Background(), CreateRequest{
		ProjectID: h.projectID,
		Prompt:    "do the thing",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	h.runner.SetSkills(noSkills{})
	if _, err := h.runner.Enqueue(context.Background(), run.ID); !errors.Is(err, ErrSkillUnavailable) {
		t.Fatalf("err = %v, want ErrSkillUnavailable", err)
	}

	if got := statusOf(t, h, run.ID); got != "backlog" {
		t.Fatalf("status = %q, want the card left where it was", got)
	}
}

// TestDispatch_EndsACardWhoseSkillDiesBetweenReleaseAndClaim.
//
// Stepping over it would be worse than failing it: the row would be picked up
// again on every pump, for ever, and the operator would watch a card that
// never starts and never says why.
func TestDispatch_EndsACardWhoseSkillDiesBetweenReleaseAndClaim(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0))

	run, err := h.runner.Create(context.Background(), CreateRequest{
		ProjectID: h.projectID,
		Prompt:    "do the thing",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Break the skill first, then put the row in the queue through the store.
	// Enqueue would refuse it — that is the previous test — and refusing is
	// exactly what makes this path otherwise unreachable.
	h.runner.SetSkills(noSkills{})
	moved, err := h.store.UpdateRunStatus(context.Background(), run.ID,
		"backlog", "queued", time.Now().UTC(), time.Time{}, "")
	if err != nil || !moved {
		t.Fatalf("queueing through the store: moved=%v err=%v", moved, err)
	}
	_ = h.runner.Kick(context.Background())

	waitFor(t, "the card to be refused rather than retried for ever", func() bool {
		return statusOf(t, h, run.ID) == "failed"
	})

	got, err := h.runner.Get(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.Contains(got.Error, "skill") {
		t.Fatalf("error = %q, want it to name the missing skill", got.Error)
	}
	h.runner.Wait()
}

// TestRun_IsGivenItsAgentsSkillsAsStandingInstructions: the composed body
// reaches the CLI as a file behind --append-system-prompt-file, not as part of
// the prompt. The prompt is the job; the skill is what holds throughout.
func TestRun_IsGivenItsAgentsSkillsAsStandingInstructions(t *testing.T) {
	// The stand-in records its own argv, which is the only way to see the
	// flag without spending a token.
	argvFile := t.TempDir() + "/argv"
	claude := writeScriptedClaude(t,
		"cat >/dev/null\nprintf '%s\\n' \"$@\" > "+argvFile+"\necho '"+lineResult+"'\n")
	h := newHarness(t, claude)

	run, err := h.runner.Start(context.Background(), CreateRequest{
		ProjectID: h.projectID,
		Prompt:    "do the thing",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "the run to finish", func() bool { return statusOf(t, h, run.ID) == "completed" })
	h.runner.Wait()

	argv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("reading argv: %v", err)
	}
	if !strings.Contains(string(argv), "--append-system-prompt-file") {
		t.Fatalf("argv = %q, want the skill file flag", string(argv))
	}
}

// TestSkillFile_IsRemovedWhenTheRunEnds — the durable copy is the operator's
// file in SkillDir, not the per-run one beside the transcript.
func TestSkillFile_IsRemovedWhenTheRunEnds(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineResult))

	run, err := h.runner.Start(context.Background(), CreateRequest{
		ProjectID: h.projectID,
		Prompt:    "do the thing",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "the run to finish", func() bool { return statusOf(t, h, run.ID) == "completed" })
	h.runner.Wait()

	if _, err := os.Stat(h.cfg.TranscriptDir + "/skills/" + run.ID + ".md"); !os.IsNotExist(err) {
		t.Fatalf("the per-run skill file outlived the run: %v", err)
	}
}

// TestRequireSkills_FallsBackToTheShippedBodiesWithNoSourceWired: a runner
// nobody gave a source is not a runner with the mandate switched off. The
// bodies are compiled into this binary and are always there.
func TestRequireSkills_FallsBackToTheShippedBodiesWithNoSourceWired(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0))

	body, version, err := h.runner.requireSkills(agents.Default)
	if err != nil {
		t.Fatalf("requireSkills: %v", err)
	}
	if strings.TrimSpace(body) == "" || version == "" {
		t.Fatal("expected the shipped body")
	}
	if !strings.Contains(version, skills.CodeReview) {
		t.Fatalf("version = %q, want it to name the skill it came from", version)
	}
}

// TestRequireSkills_RefusesAnAgentThatDeclaresNone. The registry forbids this
// and a test in internal/agents enforces it; refusing here too means a future
// agent that slipped through cannot run unguided.
func TestRequireSkills_RefusesAnAgentThatDeclaresNone(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0))
	h.runner.SetSkills(noSkills{})

	if _, _, err := h.runner.requireSkills("coding"); !errors.Is(err, ErrSkillUnavailable) {
		t.Fatalf("err = %v, want ErrSkillUnavailable", err)
	}
}
