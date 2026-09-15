package api

import (
	"net/http"

	"github.com/logrenant/mimir/internal/agents"
	"github.com/logrenant/mimir/internal/skills"
)

// The sub-agent and skill routes.
//
// Two different kinds of thing, deliberately next to each other. `GET /agents`
// publishes a constant this binary ships — which sub-agents exist, what each
// needs — and so it is registered unconditionally, like `GET /coding-models`:
// it costs nothing, it cannot fail, and a client that can read it before the
// rest of the daemon is wired draws a picker that is right rather than empty.
// The skill routes read and write files the operator owns, so they are gated on
// the store that holds them.
//
// The split is the same one internal/settings draws: the wiring that makes a
// skill mandatory is the machine's (SD-1), the words inside it are not.

// agentView is one sub-agent as a picker sees it.
type agentView struct {
	Key            string   `json:"key"`
	Name           string   `json:"name"`
	Desc           string   `json:"desc"`
	Executor       string   `json:"executor"`
	RequiredSkills []string `json:"required_skills"`
	NeedsProject   bool     `json:"needs_project"`
}

type agentsView struct {
	Agents []agentView `json:"agents"`
	// Default is what a card lands on when nothing else decides, so a client
	// can preselect it instead of guessing at the list's first entry.
	Default string `json:"default"`
}

func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	reg := agents.Registry()
	out := agentsView{Agents: make([]agentView, 0, len(reg)), Default: agents.Default}
	for _, a := range reg {
		out.Agents = append(out.Agents, agentView{
			Key:            a.Key,
			Name:           a.Name,
			Desc:           a.Desc,
			Executor:       string(a.Exec),
			RequiredSkills: a.RequiredSkills,
			NeedsProject:   a.NeedsProject,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// skillView is one skill file as the settings screen sees it. The body is
// included because the screen is an editor: a client that had to fetch each
// body separately would draw the list before the text and look broken for the
// difference — the same reason settingsView is one response.
type skillView struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Path      string `json:"path"`
	Body      string `json:"body"`
	IsDefault bool   `json:"is_default"`
	// Version is the handle on "these instructions changed". It is shown
	// because a run records the version it ran under, and an operator matching
	// the two needs to see both.
	Version   string `json:"version"`
	UpdatedAt int64  `json:"updated_at,omitempty"`
}

func skillViewOf(sk skills.Skill) skillView {
	v := skillView{
		ID:        sk.ID,
		Title:     sk.Title,
		Path:      sk.Path,
		Body:      sk.Body,
		IsDefault: sk.IsDefault,
		Version:   sk.Version,
	}
	if !sk.UpdatedAt.IsZero() {
		v.UpdatedAt = sk.UpdatedAt.Unix()
	}
	return v
}

func (s *Server) handleListSkills(w http.ResponseWriter, r *http.Request) {
	all, err := s.deps.Skills.Skills()
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	out := make([]skillView, 0, len(all))
	for _, sk := range all {
		out = append(out, skillViewOf(sk))
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": out})
}

type putSkillRequest struct {
	// Body may be empty, and empty is a reset rather than an empty
	// instruction — see skills.PutSkill for why "I cleared the box" is read
	// that way.
	Body string `json:"body"`
}

func (s *Server) handleSaveSkill(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req putSkillRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	sk, err := s.deps.Skills.PutSkill(id, req.Body)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, skillViewOf(sk))
}

func (s *Server) handleResetSkill(w http.ResponseWriter, r *http.Request) {
	sk, err := s.deps.Skills.ResetSkill(r.PathValue("id"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, skillViewOf(sk))
}
