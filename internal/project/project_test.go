package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/store"
)

// newRegistry returns a Registry backed by a real temp-file SQLite store.
// SQLite is an in-process library, not an external service, so exercising the
// real SQL is more valuable here than a hand-written fake.
func newRegistry(t *testing.T) *Registry {
	t.Helper()

	cfg := config.Load()
	cfg.StorePath = filepath.Join(t.TempDir(), "mimir.db")

	s, err := store.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	return NewRegistry(s)
}

func TestRegister_StoresAResolvedProject(t *testing.T) {
	r := newRegistry(t)
	dir := t.TempDir()

	p, err := r.Register(context.Background(), dir)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if p.ID == "" {
		t.Error("project has no id")
	}
	if !filepath.IsAbs(p.Path) {
		t.Errorf("path %q is not absolute", p.Path)
	}
	if p.DisplayName != filepath.Base(p.Path) {
		t.Errorf("DisplayName: got %q, want %q", p.DisplayName, filepath.Base(p.Path))
	}
	if p.CreatedAt.IsZero() || p.LastUsedAt.IsZero() {
		t.Error("timestamps were not set")
	}
}

// The id must carry no path information — it is what every caller passes after
// registration, and a caller must not be able to steer it.
func TestRegister_IDIsOpaque(t *testing.T) {
	r := newRegistry(t)
	dir := t.TempDir()

	p, err := r.Register(context.Background(), dir)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if filepath.IsAbs(p.ID) || len(p.ID) != 24 {
		t.Errorf("id %q should be an opaque 24-char hex string", p.ID)
	}
}

func TestRegister_RefusesABroadRoot(t *testing.T) {
	r := newRegistry(t)

	if _, err := r.Register(context.Background(), "/"); !errors.Is(err, ErrPathNotAllowed) {
		t.Fatalf("registering / must be refused, got %v", err)
	}

	// Nothing was persisted.
	list, err := r.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("a refused registration was still stored: %+v", list)
	}
}

// Two spellings of one directory are one project, not two.
func TestRegister_IsIdempotentAcrossSpellings(t *testing.T) {
	r := newRegistry(t)
	ctx := context.Background()

	base := t.TempDir()
	sub := filepath.Join(base, "project")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	link := filepath.Join(base, "alias")
	if err := os.Symlink(sub, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	first, err := r.Register(ctx, sub)
	if err != nil {
		t.Fatalf("Register(sub): %v", err)
	}
	viaLink, err := r.Register(ctx, link)
	if err != nil {
		t.Fatalf("Register(link): %v", err)
	}
	viaDotDot, err := r.Register(ctx, filepath.Join(sub, "..", "project"))
	if err != nil {
		t.Fatalf("Register(dotdot): %v", err)
	}

	if viaLink.ID != first.ID || viaDotDot.ID != first.ID {
		t.Fatalf("one directory produced multiple projects: %s / %s / %s",
			first.ID, viaLink.ID, viaDotDot.ID)
	}

	list, err := r.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 project, got %d", len(list))
	}
}

func TestGet_UnknownIDIsNotFound(t *testing.T) {
	r := newRegistry(t)

	if _, err := r.Get(context.Background(), "nope"); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("want ErrProjectNotFound, got %v", err)
	}
}

func TestResolve_ReturnsTheProjectAndMarksItUsed(t *testing.T) {
	r := newRegistry(t)
	ctx := context.Background()

	p, err := r.Register(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	got, err := r.Resolve(ctx, p.ID)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Path != p.Path {
		t.Errorf("Path: got %q, want %q", got.Path, p.Path)
	}
	if got.LastUsedAt.Before(p.LastUsedAt) {
		t.Error("Resolve did not update last_used_at")
	}
}

// A path recorded earlier may since have been deleted. Re-running the guards
// on every Resolve is what stops a stale row from becoming a way around them.
func TestResolve_FailsWhenTheDirectoryIsGone(t *testing.T) {
	r := newRegistry(t)
	ctx := context.Background()

	dir := filepath.Join(t.TempDir(), "will-be-deleted")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	p, err := r.Register(ctx, dir)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	if _, err := r.Resolve(ctx, p.ID); err == nil {
		t.Fatal("Resolve must fail for a project whose directory no longer exists")
	}
}

// Replacing the directory with a file is the same class of problem.
func TestResolve_FailsWhenTheDirectoryBecameAFile(t *testing.T) {
	r := newRegistry(t)
	ctx := context.Background()

	dir := filepath.Join(t.TempDir(), "swapped")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	p, err := r.Register(ctx, dir)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if err := os.WriteFile(dir, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := r.Resolve(ctx, p.ID); err == nil {
		t.Fatal("Resolve must fail when the project path is no longer a directory")
	}
}

func TestList_IsEmptyBeforeAnythingIsRegistered(t *testing.T) {
	r := newRegistry(t)

	list, err := r.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("want no projects by default, got %d — there must be no implicit default project", len(list))
	}
}
