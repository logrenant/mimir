package brain

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// machine builds the shape this feature exists for: a root holding a checkout,
// a directory that never became one but has files of its own, a checkout nested
// inside that directory, and a directory that is only a container.
func machine(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// A repository.
	write("repo/main.go", "package main\n")
	if err := os.MkdirAll(filepath.Join(root, "repo", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	// A container with nothing of its own, holding a loose project which in
	// turn holds a checkout.
	write("personal/loose/README.md", "# loose\n")
	write("personal/loose/src/app.ts", "export const x = 1\n")
	write("personal/loose/nested/main.swift", "print(1)\n")
	if err := os.MkdirAll(filepath.Join(root, "personal", "loose", "nested", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Nothing here is worth calling a project: a lock file and an empty file.
	write("junk/pnpm-lock.yaml", "lockfileVersion: 6\n")
	write("junk/empty.md", "")

	return root
}

func TestDiscoverProjects(t *testing.T) {
	root := machine(t)

	got, err := DiscoverProjects(root, 6)
	if err != nil {
		t.Fatalf("DiscoverProjects: %v", err)
	}

	rel := make([]string, 0, len(got))
	for _, p := range got {
		r, err := filepath.Rel(root, p)
		if err != nil {
			t.Fatal(err)
		}
		rel = append(rel, r)
	}
	joined := strings.Join(rel, ",")

	want := []string{"repo", "personal/loose", "personal/loose/nested"}
	for _, w := range want {
		if !strings.Contains(joined, w) {
			t.Errorf("missing project %q: %v", w, rel)
		}
	}
	// A container is not a project, and neither is a directory whose only files
	// are generated or empty.
	for _, unwanted := range []string{"personal,", "junk"} {
		if strings.Contains(joined+",", unwanted) {
			t.Errorf("%q should not be a project: %v", unwanted, rel)
		}
	}
	// The loose directory claims its own subtree: src/ is not a second project,
	// or its files would be scanned twice under two project paths.
	if strings.Contains(joined, "loose/src") {
		t.Errorf("a subdirectory of a claimed project became its own project: %v", rel)
	}
	if len(rel) != 3 {
		t.Errorf("found %d projects, want 3: %v", len(rel), rel)
	}
}

// The depth bound is what keeps a mis-typed root from becoming a walk of the
// whole disk, so it has to actually stop the descent.
func TestDiscoverProjects_StopsAtDepth(t *testing.T) {
	root := machine(t)

	got, err := DiscoverProjects(root, 1)
	if err != nil {
		t.Fatalf("DiscoverProjects: %v", err)
	}
	for _, p := range got {
		if strings.Contains(p, "loose") {
			t.Errorf("depth 1 reached %q", p)
		}
	}
}

func TestDiscoverProjects_RejectsAFile(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "a.md")
	if err := os.WriteFile(file, []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverProjects(file, 3); err == nil {
		t.Fatal("a file is not a root")
	}
}

// --- listing -----------------------------------------------------------------

// A file being uncommitted says nothing about whether it belongs to the work.
// What .gitignore names is still excluded, because that judgement was made on
// purpose.
func TestListScannable_IncludesUntrackedButNotIgnored(t *testing.T) {
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v (%s)", err, out)
		}
	}

	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write(".gitignore", "ignored.md\n")
	write("committed.md", "# committed\n")
	run("init", "-q")
	run("add", ".")
	run("commit", "-q", "-m", "first")

	write("untracked.md", "# untracked\n")
	write("ignored.md", "# ignored\n")

	got, err := listScannable(t.Context(), dir)
	if err != nil {
		t.Fatalf("listScannable: %v", err)
	}
	joined := strings.Join(got, ",")

	for _, want := range []string{"committed.md", "untracked.md"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q: %v", want, got)
		}
	}
	if strings.Contains(joined, "ignored.md") {
		t.Errorf("an ignored file was listed: %v", got)
	}
}

// A checkout nested under a non-checkout is its own project, with its own
// .gitignore. The walk must leave it alone or its files are read twice, under
// a project path they do not belong to.
func TestListScannable_WalkSkipsANestedCheckout(t *testing.T) {
	dir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(dir, "nested", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, content := range map[string]string{
		"own.md":         "# mine\n",
		"nested/main.go": "package main\n",
	} {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := listScannable(t.Context(), dir)
	if err != nil {
		t.Fatalf("listScannable: %v", err)
	}
	joined := strings.Join(got, ",")
	if !strings.Contains(joined, "own.md") {
		t.Errorf("the directory's own file is missing: %v", got)
	}
	if strings.Contains(joined, "nested/main.go") {
		t.Errorf("the walk went into a nested checkout: %v", got)
	}
}
