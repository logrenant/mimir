package brain

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/store"
)

// --- fakes -------------------------------------------------------------------

type fakeEpisodes struct{ rows []store.EpisodeRow }

func (f *fakeEpisodes) RecentEpisodes(_ context.Context, _ string, limit int) ([]store.EpisodeRow, error) {
	if limit > 0 && len(f.rows) > limit {
		return f.rows[:limit], nil
	}
	return f.rows, nil
}

type fakeCursors struct{ m map[string]string }

func newFakeCursors() *fakeCursors { return &fakeCursors{m: map[string]string{}} }

func (f *fakeCursors) BrainCursor(_ context.Context, key string) (string, error) {
	return f.m[key], nil
}

func (f *fakeCursors) SetBrainCursor(_ context.Context, key, _, cursor string) error {
	f.m[key] = cursor
	return nil
}

func episodeRow(key, title, summary string, files, commands []string) store.EpisodeRow {
	f, _ := json.Marshal(episodeFacts{Files: files, Commands: commands, Prompt: "do the thing"})
	return store.EpisodeRow{
		Key: key, ProjectPath: "/repo", Title: title, Summary: summary,
		FactsJSON: string(f), StartedAt: time.Now().Add(-time.Hour).UTC(),
		PromptVersion: "v1",
	}
}

// captureCore builds a Core with **no model behind it at all**. That is the
// assertion, not a convenience: promotion and commit capture must cost nothing,
// and the moment either grows a model call these tests panic on a nil
// Completer rather than quietly starting to spend quota.
func captureCore(t *testing.T, st Store) *Core {
	t.Helper()
	return New(config.Load(), st, nil)
}

// --- promotion ---------------------------------------------------------------

func TestPromote_MakesNodesAndFileLinksWithoutAModelCall(t *testing.T) {
	st := newFakeStore()
	eps := &fakeEpisodes{rows: []store.EpisodeRow{
		episodeRow("ep-1", "Rebuilt the store layer", "WAL and FTS5, decided here.",
			[]string{"/repo/internal/store/brain.go", "/repo/internal/store/memory.go"},
			[]string{"Run full make check"}),
	}}

	c := captureCore(t, st)
	got, err := c.Promote(context.Background(), "/repo", eps, 10)
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if got.Sessions != 1 || got.Files != 2 || got.Edges != 2 {
		t.Fatalf("stats = %+v, want 1 session, 2 files, 2 edges", got)
	}

	session := st.nodes[NodeID("/repo", KindSession, "ep-1")]
	if session.Title != "Rebuilt the store layer" || session.Assessment == "" {
		t.Errorf("session node = %+v", session)
	}
	// The episode's own recap is reused rather than a second one being bought.
	if session.Provider != "memory" {
		t.Errorf("provider = %q, want the memory it came from", session.Provider)
	}

	// File nodes are keyed relative to the project: an absolute key would be a
	// different node on a different machine.
	if _, ok := st.nodes[NodeID("/repo", KindFile, "internal/store/brain.go")]; !ok {
		t.Error("no file node for internal/store/brain.go")
	}
	if got := strings.Join(session.Tags, ","); !strings.Contains(got, "internal-store") {
		t.Errorf("tags = %q, want the directory prefix", got)
	}
	// Commands are prose descriptions, so their first word is a verb. Tagging
	// on them put `check`, `find` and `read` on nearly every session.
	for _, bad := range []string{"run", "check", "read", "find"} {
		for _, tag := range session.Tags {
			if tag == bad {
				t.Errorf("verb %q leaked into the tags: %v", bad, session.Tags)
			}
		}
	}
}

func TestPromote_SkipsEpisodesWithNoRecap(t *testing.T) {
	st := newFakeStore()
	eps := &fakeEpisodes{rows: []store.EpisodeRow{
		episodeRow("ep-1", "", "", []string{"/repo/a.go"}, nil),
		episodeRow("ep-2", "Has a title", "", []string{"/repo/b.go"}, nil),
	}}

	got, err := captureCore(t, st).Promote(context.Background(), "/repo", eps, 10)
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if got.Sessions != 0 {
		t.Errorf("promoted %d undistilled episodes, want 0", got.Sessions)
	}
}

// Promotion re-reads a window every pass rather than keeping a cursor, so it
// has to be safe to run repeatedly.
func TestPromote_IsIdempotent(t *testing.T) {
	st := newFakeStore()
	eps := &fakeEpisodes{rows: []store.EpisodeRow{
		episodeRow("ep-1", "Title", "Summary", []string{"/repo/a.go"}, nil),
	}}
	c := captureCore(t, st)

	for i := 0; i < 3; i++ {
		if _, err := c.Promote(context.Background(), "/repo", eps, 10); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
	}
	if len(st.nodes) != 2 {
		t.Fatalf("three passes left %d nodes, want 2 (one session, one file)", len(st.nodes))
	}
	if len(st.edges) != 3 {
		// The fake appends; the real store's primary key collapses them. What
		// matters here is that the same edge is written, not a new one.
		for _, e := range st.edges {
			if e.Kind != "provenance" {
				t.Fatalf("unexpected edge kind %q", e.Kind)
			}
		}
	}
}

// A scan (task-47) gives a file node a real assessment. A later session
// promotion must not wipe that out.
func TestPromote_DoesNotDowngradeAnExistingFileNode(t *testing.T) {
	st := newFakeStore()
	id := NodeID("/repo", KindFile, "a.go")
	st.nodes[id] = store.BrainNodeRow{
		ID: id, ProjectPath: "/repo", Kind: KindFile, SourceKey: "a.go",
		Title: "The A module", Assessment: "Distilled by a scan.",
		ContentHash: "deadbeef", Tags: []string{"scanned"},
	}

	eps := &fakeEpisodes{rows: []store.EpisodeRow{
		episodeRow("ep-1", "Title", "Summary", []string{"/repo/a.go"}, nil),
	}}
	if _, err := captureCore(t, st).Promote(context.Background(), "/repo", eps, 10); err != nil {
		t.Fatalf("Promote: %v", err)
	}

	got := st.nodes[id]
	if got.Assessment != "Distilled by a scan." || got.ContentHash != "deadbeef" {
		t.Errorf("the scan's work was overwritten: %+v", got)
	}
	if !strings.Contains(strings.Join(got.Tags, ","), "scanned") {
		t.Errorf("existing tags were dropped: %v", got.Tags)
	}
}

func TestRelativeTo(t *testing.T) {
	cases := map[string]string{
		"/repo/internal/a.go": "internal/a.go",
		"/repo/a.go":          "a.go",
		"internal/a.go":       "internal/a.go",
		"/elsewhere/a.go":     "",
		"":                    "",
	}
	for in, want := range cases {
		if got := relativeTo("/repo", in); got != want {
			t.Errorf("relativeTo(/repo, %q) = %q, want %q", in, got, want)
		}
	}
}

func TestDeriveTags(t *testing.T) {
	got := deriveTags([]string{"internal/brain/relate.go", "internal/brain/testdata/deep/x.json", "README.md"})
	joined := strings.Join(got, ",")
	for _, want := range []string{"internal-brain", "go", "json", "md"} {
		if !strings.Contains(joined, want) {
			t.Errorf("tags %q missing %q", joined, want)
		}
	}
	// Two levels is where the signal is; deeper only narrows it into noise.
	if strings.Contains(joined, "testdata") {
		t.Errorf("tags went deeper than two directory levels: %q", joined)
	}
}

// --- commits -----------------------------------------------------------------

// gitRepo builds a throwaway repository with two commits.
func gitRepo(t *testing.T) string {
	t.Helper()
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

	run("init", "-q")
	if err := os.MkdirAll(dir+"/internal/brain", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/internal/brain/a.go", []byte("package brain\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "First commit", "-m", "The body explains why.")

	if err := os.WriteFile(dir+"/README.md", []byte("# hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "Second commit")
	return dir
}

func TestCaptureCommits_StoresAndThenStopsAtTheCursor(t *testing.T) {
	dir := gitRepo(t)
	st := newFakeStore()
	cursors := newFakeCursors()
	c := captureCore(t, st)

	n, err := c.CaptureCommits(context.Background(), dir, cursors, 50)
	if err != nil {
		t.Fatalf("CaptureCommits: %v", err)
	}
	if n != 2 {
		t.Fatalf("captured %d commits, want 2", n)
	}

	var subjects []string
	for _, node := range st.nodes {
		if node.Kind == KindCommit {
			subjects = append(subjects, node.Title)
			if node.Provider != "git" {
				t.Errorf("provider = %q, want git", node.Provider)
			}
		}
	}
	if len(subjects) != 2 {
		t.Fatalf("stored %d commit nodes, want 2", len(subjects))
	}

	// A second pass has nothing new to say.
	again, err := c.CaptureCommits(context.Background(), dir, cursors, 50)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if again != 0 {
		t.Errorf("second pass captured %d commits, want 0", again)
	}
}

// A commit must not mint file nodes: history is full of paths that no longer
// exist, and every one of them would become a node nothing ever looks at.
func TestCaptureCommits_LinksOnlyToExistingFileNodes(t *testing.T) {
	dir := gitRepo(t)
	st := newFakeStore()
	fileID := NodeID(dir, KindFile, "README.md")
	st.nodes[fileID] = store.BrainNodeRow{ID: fileID, Kind: KindFile, SourceKey: "README.md"}

	if _, err := captureCore(t, st).CaptureCommits(context.Background(), dir, newFakeCursors(), 50); err != nil {
		t.Fatalf("CaptureCommits: %v", err)
	}

	for _, e := range st.edges {
		if e.Dst != fileID {
			t.Errorf("edge to a file node that was never created: %+v", e)
		}
	}
	for _, n := range st.nodes {
		if n.Kind == KindFile && n.SourceKey != "README.md" {
			t.Errorf("a commit minted a file node: %+v", n)
		}
	}
}

func TestCaptureCommits_NotARepositoryIsNotAnError(t *testing.T) {
	n, err := captureCore(t, newFakeStore()).CaptureCommits(
		context.Background(), t.TempDir(), newFakeCursors(), 10)
	if err != nil {
		t.Fatalf("a plain directory must not be an error: %v", err)
	}
	if n != 0 {
		t.Errorf("captured %d commits from a non-repository", n)
	}
}

// --- the pass ----------------------------------------------------------------

func TestCaptureProject_DegradesWithoutAStore(t *testing.T) {
	c := New(config.Load(), nil, nil)
	got, err := c.CaptureProject(context.Background(), "/repo", CaptureDeps{})
	if err != nil {
		t.Fatalf("no store must not be an error here: %v", err)
	}
	if got != (CaptureStats{}) {
		t.Errorf("stats = %+v, want zero", got)
	}
}
