package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/logrenant/mimir/internal/graphify"
	"github.com/logrenant/mimir/internal/settings"
)

// The structural layer, from the screen's side.
//
// Two questions, and they are not the same one: *may* Mimir use the parser
// (the operator's switch, stored) and *can* it (is Graphify installed on this
// machine). The tab needs both, because "off" and "not installed" want
// different sentences under them — one offers a toggle, the other offers a pip
// command — and a single boolean would make the screen guess which.

type structuralResponse struct {
	// Enabled is the operator's answer.
	Enabled bool `json:"enabled"`
	// Python is the interpreter they named. Empty means the default.
	Python string `json:"python,omitempty"`

	// Installed is this machine's answer. False with Enabled true is the
	// normal state on a machine that has never installed Graphify, and is not
	// an error.
	Installed bool `json:"installed"`
	// Version and Interpreter are what was actually found, so an operator with
	// three Pythons can see which one answered.
	Version     string `json:"version,omitempty"`
	Interpreter string `json:"interpreter,omitempty"`

	// Install is the command that would change the answer. Sent rather than
	// hardcoded in the screen for the reason GET /coding-models exists: the
	// package name is this side's fact, and two copies of it would drift.
	Install string `json:"install"`
	// Looked is where Detect looked, so an operator whose install went
	// somewhere else can see that rather than guess at it.
	Looked []string `json:"looked,omitempty"`
}

// installCommand is what to type, and it is not `pip install graphifyy`.
//
// Three things are wrong with the obvious command on a current Mac. `pip` is
// usually not on PATH at all — Homebrew ships `pip3`. Where it is, a Homebrew
// or system Python either refuses the install outright (PEP 668) or accepts it
// into a directory the next `brew upgrade` will replace. And the newest Python
// is often ahead of the tree-sitter wheels Graphify needs, so the interpreter
// that happens to be first on PATH is not necessarily the one that can install
// it at all.
//
// A dedicated environment answers all three, and it is the one Detect looks in
// first — so this command is the whole of what an operator has to do. The
// package name carries two y's, which is the other detail everybody gets wrong.
var installCommand = "python3 -m venv " + graphify.VenvPath +
	" && " + graphify.VenvPath + "/bin/pip install graphifyy"

func (s *Server) structuralView(r *http.Request) structuralResponse {
	st := s.deps.Settings.EffectiveStructural()
	out := structuralResponse{
		Enabled: st.Enabled,
		Python:  st.Python,
		Install: installCommand,
	}

	// Not probed while it is switched off: looking for an interpreter the
	// operator has told us not to use would spend a subprocess to fill in a
	// field the screen greys out.
	if !st.Enabled || s.deps.Structural == nil {
		return out
	}

	// Empty is meaningful and is passed through: it means "look in the usual
	// places", which is what graphify.Candidates does.
	python := st.Python
	if python == "" {
		python = s.cfg.BrainStructuralPython
	}

	if info, ok := s.deps.Structural.Look(r.Context(), python); ok {
		out.Installed = true
		out.Version = info.Version
		out.Interpreter = info.Python
		return out
	}
	// Only when nothing was found: a list of paths under a green tick would be
	// noise, and under a missing one it is the answer to "but I installed it".
	out.Looked = graphify.Candidates(st.Python)
	return out
}

func (s *Server) handleGetStructural(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.structuralView(r))
}

type structuralRequest struct {
	Enabled bool   `json:"enabled"`
	Python  string `json:"python"`
}

func (s *Server) handleSaveStructural(w http.ResponseWriter, r *http.Request) {
	var req structuralRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, "body must be a JSON object")
		return
	}

	if _, err := s.deps.Settings.PutStructural(settings.Structural{
		Enabled: req.Enabled,
		Python:  strings.TrimSpace(req.Python),
	}); err != nil {
		writeDomainError(w, r, err)
		return
	}

	// The cached answer is about the interpreter that was just replaced, so it
	// is dropped rather than served: an operator who names a different Python
	// is asking to be told about that one.
	if s.deps.Structural != nil {
		s.deps.Structural.Forget()
	}

	writeJSON(w, http.StatusOK, s.structuralView(r))
}
