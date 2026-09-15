package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/logrenant/mimir/internal/brain"
	"github.com/logrenant/mimir/internal/project"
	"github.com/logrenant/mimir/internal/store"
)

// Moving a project, and forgetting one.
//
// Both are writes that remove, which is why they are the only two routes here
// that need saying out loud: a repository renamed on disk left its old self in
// the knowledge base with no way to say "that is the same project", and the
// capture loop kept re-recording it from transcripts that still named the old
// directory. Until this, nothing in the daemon could delete a brain node.
//
// The id on the wire stays opaque (brain.ProjectID) and a path is never
// accepted as one — resolveBrainProject enforces that, and it matters more here
// than on a read: a route that took a path would let a caller name a directory
// it was never shown.

// BrainKeeper is the write half of the knowledge base's project surface.
// *brain.Core satisfies it.
type BrainKeeper interface {
	MoveProject(ctx context.Context, from, to string) (store.MoveResult, error)
	ForgetProject(ctx context.Context, path string) (int, error)
}

// ProjectKeeper is the registry's half. *project.Registry satisfies it.
//
// Separate from BrainKeeper because they are separate decisions and can fail
// separately: a folder can be unregistered while its history is kept, and a
// history can be forgotten for a folder that was never registered at all.
type ProjectKeeper interface {
	Find(ctx context.Context, path string) (project.Project, bool, error)
	Repoint(ctx context.Context, id, path string) (project.Project, error)
	Forget(ctx context.Context, id string) error
}

type moveProjectRequest struct {
	// To is a path, not an id: the destination is a directory on disk that the
	// knowledge base has very likely never heard of, so there is no id for it.
	To string `json:"to"`
}

type moveProjectResponse struct {
	From string `json:"from"`
	To   string `json:"to"`
	// ID changes, because it is derived from the path.
	ID     string           `json:"id"`
	Result store.MoveResult `json:"result"`
}

// handleMoveBrainProject re-files a project under a new path.
func (s *Server) handleMoveBrainProject(w http.ResponseWriter, r *http.Request) {
	from, err := s.resolveBrainProject(r.Context(), r.PathValue("id"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	if from == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "a project id is required")
		return
	}

	var req moveProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, "body must be a JSON object with a `to` path")
		return
	}

	result, err := s.deps.BrainKeep.MoveProject(r.Context(), from, req.To)
	if err != nil {
		if errors.Is(err, brain.ErrCannotMove) {
			writeError(w, http.StatusBadRequest, codeBadRequest, err.Error())
			return
		}
		writeDomainError(w, r, err)
		return
	}

	// The registry follows the graph. Leaving it naming the old directory would
	// mean the next capture pass skipped the project entirely — the folder is
	// gone, which is the whole reason for the move.
	to := s.repointRegistration(r.Context(), from, req.To)

	writeJSON(w, http.StatusOK, moveProjectResponse{
		From:   from,
		To:     to,
		ID:     brain.ProjectID(to),
		Result: result,
	})
}

type forgetProjectResponse struct {
	Path  string `json:"path"`
	Nodes int    `json:"nodes_removed"`
}

// handleForgetBrainProject removes a project and everything filed under it.
func (s *Server) handleForgetBrainProject(w http.ResponseWriter, r *http.Request) {
	path, err := s.resolveBrainProject(r.Context(), r.PathValue("id"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	if path == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "a project id is required")
		return
	}

	removed, err := s.deps.BrainKeep.ForgetProject(r.Context(), path)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}

	// And the registration, if there was one. A folder whose history has been
	// thrown away should not still be offered in the picker.
	if s.deps.ProjectKeep != nil {
		if p, ok, findErr := s.deps.ProjectKeep.Find(r.Context(), path); findErr == nil && ok {
			_ = s.deps.ProjectKeep.Forget(r.Context(), p.ID)
		}
	}

	writeJSON(w, http.StatusOK, forgetProjectResponse{Path: path, Nodes: removed})
}

// repointRegistration moves the registry row too, and answers with the path
// that is now in force.
//
// Failures here are not the move's failure: the graph has already moved, and a
// registry that could not be updated is a smaller problem than a caller told
// the whole thing failed and retrying it.
func (s *Server) repointRegistration(ctx context.Context, from, to string) string {
	resolved := strings.TrimSpace(to)
	if s.deps.ProjectKeep == nil {
		return resolved
	}
	p, ok, err := s.deps.ProjectKeep.Find(ctx, from)
	if err != nil || !ok {
		return resolved
	}
	moved, err := s.deps.ProjectKeep.Repoint(ctx, p.ID, to)
	if err != nil {
		return resolved
	}
	return moved.Path
}
