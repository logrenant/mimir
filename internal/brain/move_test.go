package brain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/store"
)

// The husk is the case this was written for: `goat-remastered` after the
// repository moved was a directory containing one empty directory and nothing
// else — 0 bytes — and the capture loop went on reading its transcripts and
// writing its contents back into the graph every five minutes.
func TestOnDisk_TellsAHuskFromAProject(t *testing.T) {
	real := t.TempDir()
	if err := os.WriteFile(filepath.Join(real, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	empty := t.TempDir()

	husk := t.TempDir()
	if err := os.MkdirAll(filepath.Join(husk, "deploy"), 0o755); err != nil {
		t.Fatal(err)
	}

	nested := t.TempDir()
	if err := os.MkdirAll(filepath.Join(nested, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "internal", "a.go"), []byte("package a"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := map[string]struct {
		path string
		want bool
	}{
		"a project with a file":        {real, true},
		"a project with a nested file": {nested, true},
		"an empty directory":           {empty, false},
		"a husk of empty directories":  {husk, false},
		"a path that is not there":     {filepath.Join(real, "nope"), false},
		"a file rather than a folder":  {filepath.Join(real, "main.go"), false},
	}
	for name, c := range cases {
		if got := OnDisk(c.path); got != c.want {
			t.Errorf("%s: OnDisk = %v, want %v", name, got, c.want)
		}
	}
}

// A dotfile-only directory is still a husk: `.git` alone is a repository whose
// working tree is gone, and there is nothing in it for the scan to read.
func TestOnDisk_IgnoresDotDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if OnDisk(dir) {
		t.Error("a directory holding only dotfiles reads as a project")
	}
}

func moveCore(t *testing.T) (*Core, *fakeStore) {
	t.Helper()
	st := newFakeStore()
	return New(config.Load(), st, nil), st
}

func TestMoveProject_ReFilesEverythingUnderTheNewPath(t *testing.T) {
	c, st := moveCore(t)
	ctx := context.Background()

	dest := t.TempDir()
	// What comes back is the *resolved* path: on macOS a temp dir is under
	// /var, which is a symlink to /private/var, and MoveProject canonicalises
	// before judging — the same guard a scan root goes through.
	resolved, err := filepath.EvalSymlinks(dest)
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{"a.go", "b.go"} {
		id := NodeID("/old", KindFile, src)
		if err := st.UpsertBrainNode(ctx, store.BrainNodeRow{
			ID: id, ProjectPath: "/old", Kind: KindFile, SourceKey: src,
		}); err != nil {
			t.Fatal(err)
		}
	}

	res, err := c.MoveProject(ctx, "/old", dest)
	if err != nil {
		t.Fatal(err)
	}
	if res.Nodes != 2 {
		t.Fatalf("moved %d nodes, want 2", res.Nodes)
	}
	// The identity is recomputed from the destination, which is the whole
	// reason a move is not an UPDATE.
	if _, ok := st.nodes[NodeID(resolved, KindFile, "a.go")]; !ok {
		t.Error("the moved node does not carry its destination identity")
	}
}

// The destination goes through the same guard a scan root does: a move is not a
// way to name the home directory.
func TestMoveProject_RefusesADestinationTheGuardRejects(t *testing.T) {
	c, _ := moveCore(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory on this machine")
	}

	for _, bad := range []string{home, "/", "relative/path", ""} {
		if _, err := c.MoveProject(context.Background(), "/old", bad); !errors.Is(err, ErrCannotMove) {
			t.Errorf("destination %q: err = %v, want ErrCannotMove", bad, err)
		}
	}
}

// The source is deliberately not checked for existence — the whole reason this
// exists is a directory that is gone.
func TestMoveProject_DoesNotRequireTheSourceToExist(t *testing.T) {
	c, _ := moveCore(t)
	if _, err := c.MoveProject(context.Background(), "/gone/for/good", t.TempDir()); err != nil {
		t.Fatalf("a missing source must be movable: %v", err)
	}
}

func TestMoveProject_RefusesAMoveToWhereItAlreadyIs(t *testing.T) {
	c, _ := moveCore(t)
	dir := t.TempDir()
	// Canonicalize resolves symlinks, so the comparison is made after it.
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.MoveProject(context.Background(), resolved, dir); !errors.Is(err, ErrCannotMove) {
		t.Errorf("err = %v, want ErrCannotMove", err)
	}
}

func TestForgetProject_RemovesWhatWasFiledUnderThePath(t *testing.T) {
	c, st := moveCore(t)
	ctx := context.Background()

	id := NodeID("/gone", KindFile, "a.go")
	if err := st.UpsertBrainNode(ctx, store.BrainNodeRow{
		ID: id, ProjectPath: "/gone", Kind: KindFile, SourceKey: "a.go",
	}); err != nil {
		t.Fatal(err)
	}

	removed, err := c.ForgetProject(ctx, "/gone")
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, ok := st.nodes[id]; ok {
		t.Error("the node survived being forgotten")
	}
}

// Forgetting takes the path rather than resolving it: canonicalising would
// refuse exactly the case this is for.
func TestForgetProject_NeedsAPathButNotADirectory(t *testing.T) {
	c, _ := moveCore(t)
	if _, err := c.ForgetProject(context.Background(), ""); err == nil {
		t.Fatal("want an error for an empty path")
	}
	if _, err := c.ForgetProject(context.Background(), "/not/on/disk"); err != nil {
		t.Fatalf("a path that is gone must be forgettable: %v", err)
	}
}
