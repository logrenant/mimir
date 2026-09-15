package brain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/logrenant/mimir/internal/project"
	"github.com/logrenant/mimir/internal/store"
)

// Moving a project, and forgetting one.
//
// The absence of these was a bug with a shape. A repository renamed or moved on
// disk left its old self in the knowledge base as a separate project — nodes,
// sessions, commits, all filed under a path that no longer held anything — and
// the capture loop went on re-recording it every five minutes from transcripts
// that still named the old directory. There was no way to say "that is the same
// project" and no way to say "forget it": before this file, no code in this
// repository deleted a brain node.
//
// The identity rule is why moving is not an UPDATE. A node's id is
// sha256(project_path|kind|source_key) — NodeID, below the store's reach — so
// every id in a moved project changes, and so does every edge that named one.
// This package computes the rule; internal/store applies it in one transaction.

// ErrCannotMove is returned when the destination is not somewhere a project may
// live. It is a typed sentinel so the API can answer 400 rather than 500.
var ErrCannotMove = errors.New("brain: that is not a project directory")

// MoveProject re-files everything recorded under `from` as being under `to`.
//
// The destination goes through the same guard a scan root and a coding task's
// folder go through (internal/project.Canonicalize): a move is not a chance to
// name the home directory or a path that resolves somewhere else.
//
// The source is deliberately *not* checked for existence. The whole reason this
// exists is a directory that is gone.
func (c *Core) MoveProject(ctx context.Context, from, to string) (store.MoveResult, error) {
	if !c.Available() {
		return store.MoveResult{}, ErrNoStore
	}

	from = strings.TrimSpace(from)
	resolved, err := project.Canonicalize(to)
	if err != nil {
		return store.MoveResult{}, fmt.Errorf("%w: %s", ErrCannotMove, err)
	}
	if from == "" {
		return store.MoveResult{}, fmt.Errorf("%w: no source", ErrCannotMove)
	}
	if from == resolved {
		return store.MoveResult{}, fmt.Errorf("%w: the project is already there", ErrCannotMove)
	}

	return c.store.MoveBrainProject(ctx, from, resolved, NodeID)
}

// ForgetProject removes a project and everything filed under it.
//
// Irreversible, and it takes the path rather than resolving it: the point is to
// be able to forget something that is not on disk any more, and canonicalising
// it would refuse exactly the case this is for.
func (c *Core) ForgetProject(ctx context.Context, path string) (int, error) {
	if !c.Available() {
		return 0, ErrNoStore
	}
	if strings.TrimSpace(path) == "" {
		return 0, errors.New("brain: forgetting needs a project path")
	}
	return c.store.ForgetBrainProject(ctx, path)
}

// OnDisk reports whether a project path still holds anything.
//
// The same question discover.go asks before scanning, asked in the one other
// place that needed it: the capture loop, which was reading transcripts for
// directories that had been deleted and writing their contents back into the
// graph on every pass. An empty husk — the directory left behind by a move —
// counts as gone, because a project with no files is not one.
func OnDisk(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if e.IsDir() {
			// One level is enough to tell a husk from a project: a directory
			// holding only empty directories is what a move leaves behind.
			if sub, err := os.ReadDir(fmt.Sprintf("%s/%s", path, e.Name())); err == nil && len(sub) > 0 {
				return true
			}
			continue
		}
		return true
	}
	return false
}
