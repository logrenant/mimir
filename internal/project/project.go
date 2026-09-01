// Package project owns which directories an agent run is allowed to touch.
//
// This is a security boundary, not bookkeeping. The coding-task runner starts
// a `claude` session with file-editing tools enabled; the only thing between
// that and the whole disk is the directory it is pointed at, and this package
// decides what that may be.
//
// goat v1 shipped `"tools": {"roots": ["/"]}` as its default and its own README
// conceded the result was a complete exfiltration primitive. The structural fix
// here is that there is no default: a run cannot start until a directory has
// been explicitly registered, and callers thereafter pass an opaque id rather
// than a path.
package project

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/logrenant/mimir/internal/store"
)

var (
	// ErrInvalidPath means the path is empty, relative, missing, or not a
	// directory.
	ErrInvalidPath = errors.New("project: path is not a usable directory")

	// ErrPathNotAllowed means the path resolves to a directory too broad to
	// scope an agent to — the root, a home directory itself, or a system tree.
	ErrPathNotAllowed = errors.New("project: path is too broad to be a project root")

	// ErrProjectNotFound means no project has that id.
	ErrProjectNotFound = errors.New("project: no such project")
)

// Project is a registered directory an agent may run inside.
type Project struct {
	ID          string    `json:"id"`
	Path        string    `json:"path"`
	DisplayName string    `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
	LastUsedAt  time.Time `json:"last_used_at"`
}

// Store is the persistence the registry needs. *store.Store satisfies it.
type Store interface {
	InsertProject(ctx context.Context, p store.ProjectRow) error
	GetProject(ctx context.Context, id string) (store.ProjectRow, bool, error)
	GetProjectByPath(ctx context.Context, path string) (store.ProjectRow, bool, error)
	ListProjects(ctx context.Context) ([]store.ProjectRow, error)
	TouchProject(ctx context.Context, id string, at time.Time) error
}

// Registry is the only way to turn a directory into something runnable.
type Registry struct {
	store Store
}

func NewRegistry(s Store) *Registry {
	return &Registry{store: s}
}

func fromRow(r store.ProjectRow) Project {
	return Project{
		ID:          r.ID,
		Path:        r.Path,
		DisplayName: r.DisplayName,
		CreatedAt:   r.CreatedAt,
		LastUsedAt:  r.LastUsedAt,
	}
}

// newID returns an opaque project id. Opaque on purpose: it is what every
// caller passes after registration, so it must carry no path information a
// caller could try to edit.
func newID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("project: generating id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// Register validates path and records it, returning the project.
//
// Registration is idempotent: two paths that resolve to the same directory
// (via symlinks, `..`, or case) are one project, looked up by resolved path.
func (r *Registry) Register(ctx context.Context, path string) (Project, error) {
	resolved, err := canonicalize(path)
	if err != nil {
		return Project{}, err
	}

	existing, found, err := r.store.GetProjectByPath(ctx, resolved)
	if err != nil {
		return Project{}, err
	}
	if found {
		return fromRow(existing), nil
	}

	id, err := newID()
	if err != nil {
		return Project{}, err
	}

	now := time.Now().UTC()
	row := store.ProjectRow{
		ID:          id,
		Path:        resolved,
		DisplayName: filepath.Base(resolved),
		CreatedAt:   now,
		LastUsedAt:  now,
	}
	if err := r.store.InsertProject(ctx, row); err != nil {
		return Project{}, err
	}
	return fromRow(row), nil
}

// Get returns the recorded project without re-checking its directory. Use
// Resolve before acting on one.
func (r *Registry) Get(ctx context.Context, id string) (Project, error) {
	row, found, err := r.store.GetProject(ctx, id)
	if err != nil {
		return Project{}, err
	}
	if !found {
		return Project{}, fmt.Errorf("%w: %s", ErrProjectNotFound, id)
	}
	return fromRow(row), nil
}

// List returns every registered project, most recently used first.
func (r *Registry) List(ctx context.Context) ([]Project, error) {
	rows, err := r.store.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Project, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromRow(row))
	}
	return out, nil
}

// Resolve returns the project for id and re-validates that its path is still a
// directory that passes every guard, then marks it used.
//
// Every consumer about to act on a project calls this, never Get. A path
// recorded weeks ago may since have been deleted, replaced by a file, or
// re-pointed by a symlink at something far broader — re-running the guards is
// what stops a stale row from becoming a way around them.
func (r *Registry) Resolve(ctx context.Context, id string) (Project, error) {
	p, err := r.Get(ctx, id)
	if err != nil {
		return Project{}, err
	}

	resolved, err := canonicalize(p.Path)
	if err != nil {
		return Project{}, fmt.Errorf("project %s is no longer usable: %w", id, err)
	}
	if resolved != p.Path {
		return Project{}, fmt.Errorf("%w: %s now resolves to %q, not the registered %q — re-register it",
			ErrPathNotAllowed, id, resolved, p.Path)
	}

	now := time.Now().UTC()
	if err := r.store.TouchProject(ctx, id, now); err != nil {
		return Project{}, err
	}
	p.LastUsedAt = now
	return p, nil
}

// Find returns the registration for a path if one already exists.
//
// It never creates one. Callers that only read — the project memory joining its
// episodes to this daemon's own coding runs, for instance — use it to pick up a
// project id when the picker has already produced one, and carry on without it
// when it has not. A missing registration is a normal answer, not an error.
func (r *Registry) Find(ctx context.Context, path string) (Project, bool, error) {
	resolved, err := Canonicalize(path)
	if err != nil {
		return Project{}, false, err
	}
	row, ok, err := r.store.GetProjectByPath(ctx, resolved)
	if err != nil || !ok {
		return Project{}, false, err
	}
	return fromRow(row), true, nil
}
