package api

import (
	"net/http"
	"strings"

	"github.com/logrenant/mimir/internal/llm"
	"github.com/logrenant/mimir/internal/project"
	"github.com/logrenant/mimir/internal/settings"
)

// The scan permission surface: which folders Brain may read, and what inside
// them it may not.
//
// These routes live beside the scan controls rather than with the rest of
// /settings because that is the question they answer. An operator looking for
// "what is Mimir reading?" goes to the tab where the scan is running, not to a
// model picker — and the answer has to be editable from the same screen that
// shows the sweep, or the two drift in the operator's head.
//
// Storage is internal/settings all the same: this is the operator's decision,
// not the machine's, and it is the same kind of thing the model default is.

type scanPolicyResponse struct {
	Roots    []string `json:"roots"`
	Excludes []string `json:"excludes"`
	// Configured says whether the operator has ever saved a policy. False means
	// Roots below are the shipped default, which the screen says out loud —
	// "these are the folders Mimir picked" is a different sentence from "these
	// are the folders you chose", and only one of them invites a look.
	Configured bool `json:"configured"`
	// DefaultRoots is what a reset would restore. Sent so the screen can offer
	// that without holding its own copy of the default, the same reason
	// GET /coding-models exists.
	DefaultRoots []string `json:"default_roots"`
}

type scanPolicyRequest struct {
	Roots    []string `json:"roots"`
	Excludes []string `json:"excludes"`
}

func (s *Server) scanPolicyView() scanPolicyResponse {
	_, configured, err := s.deps.Settings.ScanPolicy()
	effective := s.deps.Settings.EffectiveScanPolicy(s.cfg.BrainScanRoots)
	return scanPolicyResponse{
		// Never nil, for the reason brain.list gives: neither field carries
		// omitempty, so a nil slice reaches the screen as `null` rather than
		// as the empty list it means.
		Roots:        append(make([]string, 0, len(effective.Roots)), effective.Roots...),
		Excludes:     append(make([]string, 0, len(effective.Excludes)), effective.Excludes...),
		Configured:   configured && err == nil,
		DefaultRoots: append(make([]string, 0, len(s.cfg.BrainScanRoots)), s.cfg.BrainScanRoots...),
	}
}

// handleGetScanPolicy reports what the scan is allowed to read.
func (s *Server) handleGetScanPolicy(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.scanPolicyView())
}

// handleSaveScanPolicy replaces the whole policy.
//
// A PUT of both lists rather than add/remove routes, because the screen holds
// the list and the operator edits it there: a DELETE per row would make the
// server the arbiter of an ordering the client already has, and two rows
// removed quickly would race. The whole document is one decision.
//
// Roots go through project.Canonicalize — the same guard a coding task's folder
// goes through. That is not ceremony: it resolves symlinks *before* judging the
// path, so `~/shortcut -> /` cannot be added as a scan root, and it refuses the
// roots that would turn a sweep into a walk of the whole machine (`/`, `/Users`,
// the home directory itself).
//
// Exclusions get none of that. An exclusion only ever removes permission, so
// there is nothing to guard against; it may name a file rather than a
// directory, and it may name something that does not exist yet — excluding a
// path you are about to create is a perfectly sensible thing to want.
func (s *Server) handleSaveScanPolicy(w http.ResponseWriter, r *http.Request) {
	var req scanPolicyRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	roots := make([]string, 0, len(req.Roots))
	for _, raw := range req.Roots {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		resolved, err := project.Canonicalize(raw)
		if err != nil {
			// Echoed rather than logged: the path guards in internal/project
			// say what is wrong and what to do instead, which is exactly what
			// the operator who just picked a folder needs to read.
			writeDomainError(w, r, err)
			return
		}
		roots = append(roots, resolved)
	}

	excludes := make([]string, 0, len(req.Excludes))
	for _, raw := range req.Excludes {
		if raw = strings.TrimSpace(raw); raw != "" {
			excludes = append(excludes, raw)
		}
	}

	if _, err := s.deps.Settings.PutScanPolicy(settings.ScanPolicy{
		Roots: roots, Excludes: excludes,
	}); err != nil {
		writeDomainError(w, r, err)
		return
	}

	// The next sweep would pick this up on its own, but "on its own" is up to
	// BrainScanIdleInterval away. An operator who has just excluded a folder
	// wants the scan to stop reading it now, and a wake-up is the difference
	// between a control and a suggestion. Paused stays paused: ScanNow reports
	// that and it is not this route's decision to override.
	if s.deps.BrainScan != nil {
		s.deps.BrainScan.ScanNow(llm.Selection{})
	}

	writeJSON(w, http.StatusOK, s.scanPolicyView())
}

// handleResetScanPolicy forgets the operator's list and restores the default.
//
// A route rather than "PUT the defaults back", for the reason handleResetRule
// gives: the client would then hold its own copy of what the default is, and
// the two would drift the first time the shipped list changed.
func (s *Server) handleResetScanPolicy(w http.ResponseWriter, r *http.Request) {
	if _, err := s.deps.Settings.PutScanPolicy(settings.ScanPolicy{
		Roots: append([]string(nil), s.cfg.BrainScanRoots...),
	}); err != nil {
		writeDomainError(w, r, err)
		return
	}
	if s.deps.BrainScan != nil {
		s.deps.BrainScan.ScanNow(llm.Selection{})
	}
	writeJSON(w, http.StatusOK, s.scanPolicyView())
}
