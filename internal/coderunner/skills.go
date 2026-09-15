package coderunner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/logrenant/mimir/internal/agents"
	"github.com/logrenant/mimir/internal/skills"
)

// ErrSkillUnavailable means a run's sub-agent declares a skill this process
// could not load, so the run is refused rather than started.
//
// The refusal is the whole point. A skill is not advice a run can do without:
// it is what makes the output reviewable afterwards, and a run that quietly
// went ahead without it produces work nobody downstream can tell apart from
// work that followed the rules. Failing here — while the operator is still
// looking at the thing they pressed — is the only place this can be said
// usefully.
var ErrSkillUnavailable = errors.New("coderunner: required skill could not be loaded")

// SkillSource is the narrow half of skills.Store this package needs. An alias
// rather than a second declaration of the same method set: the composition that
// turns these bodies into a run's standing instructions lives in
// skills.Require, and this package and internal/api both call it so the version
// they agree on cannot drift again.
type SkillSource = skills.BodySource

// SetSkills gives the runner the source its agents' skills come from. Called
// once at wiring time.
//
// A runner with no source is not a runner with the mandate switched off: the
// fallback below is the bodies compiled into this binary, which are always
// present. The only thing that can actually fail is a skill the binary does
// not ship — an agent registry that drifted — and that is a refusal.
func (r *Runner) SetSkills(src SkillSource) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skills = src
}

func (r *Runner) skillSource() SkillSource {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.skills != nil {
		return r.skills
	}
	// A nil *skills.Store serves the shipped defaults rather than nothing,
	// which is why this is a usable fallback and not a hidden failure.
	return (*skills.Store)(nil)
}

// agentFor resolves a row's sub-agent. An empty or unknown name is the default
// agent, because every row written before agents existed was that agent and a
// name from a newer client is version skew rather than a lost card.
func agentFor(name string) agents.Agent {
	if a, ok := agents.Lookup(name); ok {
		return a
	}
	a, _ := agents.Lookup(agents.Default)
	return a
}

// requireSkills is the gate. It resolves everything the agent cannot work
// without and returns the composed body, so the caller that passes the gate is
// holding exactly what the caller that failed it was missing.
func (r *Runner) requireSkills(agentName string) (body, version string, err error) {
	a := agentFor(agentName)
	if len(a.RequiredSkills) == 0 {
		// The registry forbids this and a test enforces it. Refusing here too
		// means a future agent that slipped through cannot run unguided.
		return "", "", fmt.Errorf("%w: agent %q declares none", ErrSkillUnavailable, a.Key)
	}

	src := r.skillSource()
	body, version, ok := skills.Require(src, a.RequiredSkills)
	if !ok {
		// Name the one that is missing rather than the set: the operator's next
		// move is to open that file, and a list does not tell them which.
		for _, id := range a.RequiredSkills {
			if b, _ := src.Body(id); b == "" {
				return "", "", fmt.Errorf("%w: %s (agent %q)", ErrSkillUnavailable, id, a.Key)
			}
		}
		return "", "", fmt.Errorf("%w: agent %q", ErrSkillUnavailable, a.Key)
	}
	return body, version, nil
}

// writeSkillFile puts the composed body where the CLI can read it.
//
// A file rather than an argument because the composition is a page or more of
// Markdown and argv is not where that belongs, and because
// --append-system-prompt-file is the flag that says "these are the standing
// instructions", as opposed to the prompt, which is the job.
//
// It lives beside the transcript so a run's inputs and its record are in one
// place, and it is removed when the run ends — the durable copy is the
// operator's file in SkillDir, not this one.
func (r *Runner) writeSkillFile(runID, body string) (path string, cleanup func(), err error) {
	dir := filepath.Join(r.cfg.TranscriptDir, "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", nil, fmt.Errorf("coderunner: creating %s: %w", dir, err)
	}
	path = filepath.Join(dir, runID+".md")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return "", nil, fmt.Errorf("coderunner: writing %s: %w", path, err)
	}
	return path, func() { _ = os.Remove(path) }, nil
}
