package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/logrenant/goat-mcp/internal/store"
)

func TestBrief_AssemblesFromRows(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(h.project.Path, "README.md"),
		[]byte("# Demo\n\nA local service that does one thing well.\n\nMore prose.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h.project.Path, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.project.Path, "internal", "AGENTS.md"), []byte("rules"), 0o600); err != nil {
		t.Fatal(err)
	}

	h.writeSession(t, "a.jsonl", "add a retry", "remove the retry")
	if _, err := h.mem.Ingest(ctx, h.project, 10); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if _, err := h.mem.Remember(ctx, h.project, "decision", "Places API is primary; scraping is fallback."); err != nil {
		t.Fatalf("Remember: %v", err)
	}

	b, err := h.mem.Brief(ctx, h.project)
	if err != nil {
		t.Fatalf("Brief: %v", err)
	}

	// The summary keeps reading past the first blank line: a README's opening
	// clause is often just an introduction to what follows.
	if b.Repo == nil || b.Repo.Summary != "A local service that does one thing well. More prose." {
		t.Errorf("repo summary: %+v", b.Repo)
	}
	if b.Repo != nil && !contains(b.Repo.RuleFiles, "internal/AGENTS.md") {
		t.Errorf("rule files should point at per-package rules: %v", b.Repo.RuleFiles)
	}
	if len(b.Notes) != 1 || b.Notes[0].Kind != "decision" {
		t.Errorf("pinned note missing: %+v", b.Notes)
	}
	if len(b.Recent) != 2 {
		t.Errorf("want 2 recent entries, got %d", len(b.Recent))
	}
	if b.Coverage.Episodes != 2 || b.Coverage.Distilled != 2 || b.Coverage.Pending != 0 {
		t.Errorf("coverage: %+v", b.Coverage)
	}
	if b.Guidance == "" {
		t.Error("guidance must travel with every response")
	}
}

// The repository is the authority, so its self-description is re-read rather
// than remembered. A memory that described what a repo used to be would be
// worse than one that said nothing.
func TestBrief_RepoFactsFollowDisk(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	readme := filepath.Join(h.project.Path, "README.md")

	if err := os.WriteFile(readme, []byte("First description.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, _ := h.mem.Brief(ctx, h.project)
	if b.Repo.Summary != "First description." {
		t.Fatalf("got %q", b.Repo.Summary)
	}

	if err := os.WriteFile(readme, []byte("Second description.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b, _ = h.mem.Brief(ctx, h.project)
	if b.Repo.Summary != "Second description." {
		t.Errorf("the brief served a stale repo description: %q", b.Repo.Summary)
	}
}

// Two identical calls must produce identical output. A brief that shifted
// between calls is indistinguishable from a memory that is unstable, and a
// consumer would learn to distrust it.
func TestBrief_IsDeterministic(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.writeSession(t, "a.jsonl", "one", "two", "three")
	if _, err := h.mem.Ingest(ctx, h.project, 10); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	first, err := h.mem.Brief(ctx, h.project)
	if err != nil {
		t.Fatalf("Brief: %v", err)
	}
	want, _ := json.Marshal(first)
	for range 15 {
		got, err := h.mem.Brief(ctx, h.project)
		if err != nil {
			t.Fatalf("Brief: %v", err)
		}
		b, _ := json.Marshal(got)
		if string(b) != string(want) {
			t.Fatalf("Brief is not deterministic:\n got %s\nwant %s", b, want)
		}
	}
}

// internal/mcp's choke-point rejects an over-budget response rather than
// truncating it. A brief that outgrew its ceiling would not arrive shortened —
// it would not arrive at all, and only on the projects with the most history.
func TestBrief_AlwaysFitsItsBudget(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Far more history than a brief could ever carry.
	for i := range 60 {
		row := store.EpisodeRow{
			Key:         fmt.Sprintf("k%d", i),
			ProjectPath: h.project.Path,
			SourceKind:  "claude_code",
			StartedAt:   time.Now().UTC().Add(-time.Duration(i) * time.Hour),
			EndedAt:     time.Now().UTC(),
			Title:       strings.Repeat("a long title about the subsystem ", 4),
			Summary:     strings.Repeat("a long summary line about what changed and why ", 12),
			FactsJSON: fmt.Sprintf(`{"files":["internal/a/%d.go","internal/b/%d.go","internal/c/%d.go"],"prompt":%q}`,
				i%5, i%5, i%5, strings.Repeat("a long request ", 20)),
			Significance: 5,
		}
		if err := h.store.PutEpisode(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 12 {
		if _, err := h.mem.Remember(ctx, h.project, "decision", fmt.Sprintf("%d: %s", i, strings.Repeat("a pinned decision ", 20))); err != nil {
			t.Fatal(err)
		}
	}

	b, err := h.mem.Brief(ctx, h.project)
	if err != nil {
		t.Fatalf("Brief: %v", err)
	}
	if got, budget := estimateTokens(b), h.mem.cfg.MemoryBriefMaxTokens; got > budget {
		t.Fatalf("brief is ~%d tokens, over its %d budget", got, budget)
	}
	// It must still say something.
	if len(b.Recent) == 0 && len(b.Notes) == 0 {
		t.Fatal("the brief trimmed itself to nothing")
	}
	if b.Coverage.Episodes != 60 {
		t.Errorf("coverage must report the whole history even when the brief shows a slice: %+v", b.Coverage)
	}
}

func TestRecall_ReturnsPointersAndFitsItsBudget(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.refiner.reply = "Retry in the crawl client\n- chose a fixed delay over backoff"
	h.writeSession(t, "a.jsonl", "add a retry to the crawl client")
	if _, err := h.mem.Ingest(ctx, h.project, 10); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if _, err := h.mem.Remember(ctx, h.project, "trap", "the crawl mock closes early"); err != nil {
		t.Fatal(err)
	}

	r, err := h.mem.Recall(ctx, h.project, "crawl retry", 10)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(r.Hits) != 1 {
		t.Fatalf("want 1 hit, got %d", len(r.Hits))
	}
	if r.Hits[0].Title != "Retry in the crawl client" {
		t.Errorf("hit title: %q", r.Hits[0].Title)
	}
	if len(r.Hits[0].Files) == 0 {
		t.Error("a hit must carry file pointers; that is what saves the re-derivation")
	}
	if len(r.Notes) != 1 {
		t.Errorf("a matching note should ride along: %+v", r.Notes)
	}
	if got, budget := estimateTokens(r), h.mem.cfg.MemoryRecallMaxTokens; got > budget {
		t.Errorf("recall is ~%d tokens, over its %d budget", got, budget)
	}
}

func TestRecall_NoMatchIsAnEmptyAnswerNotAnError(t *testing.T) {
	h := newHarness(t)
	r, err := h.mem.Recall(context.Background(), h.project, "nothing here at all", 10)
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(r.Hits) != 0 {
		t.Errorf("want no hits, got %d", len(r.Hits))
	}
	// Marshals as [] rather than null, so a consumer sees "searched, found
	// nothing" instead of "this field is missing".
	b, _ := json.Marshal(r)
	if !strings.Contains(string(b), `"hits":[]`) {
		t.Errorf("empty hits should marshal as an empty list: %s", b)
	}
}

func TestRemember_ValidatesKind(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.mem.Remember(ctx, h.project, "whatever", "text"); err == nil {
		t.Error("an open kind vocabulary becomes tag soup; want a rejection")
	}
	if _, err := h.mem.Remember(ctx, h.project, "decision", "   "); err == nil {
		t.Error("a blank note is not a note")
	}
	n, err := h.mem.Remember(ctx, h.project, "  DECISION  ", "Places API is primary.")
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if n.Kind != "decision" {
		t.Errorf("kind should normalize: %q", n.Kind)
	}
}

func TestHotFiles_RanksDeterministicallyAndIgnoresOneOffs(t *testing.T) {
	now := time.Now().UTC()
	rows := []store.EpisodeRow{
		{StartedAt: now, FactsJSON: `{"files":["a.go","b.go"]}`},
		{StartedAt: now, FactsJSON: `{"files":["a.go","b.go"]}`},
		{StartedAt: now, FactsJSON: `{"files":["a.go","c.go"]}`},
		{StartedAt: now.AddDate(0, 0, -90), FactsJSON: `{"files":["ancient.go","ancient.go"]}`},
	}

	got := hotFiles(rows, 30, 10)
	if len(got) != 2 {
		t.Fatalf("want a.go and b.go only (c.go touched once, ancient.go out of window): %+v", got)
	}
	if got[0].Path != "a.go" || got[0].Touches != 3 {
		t.Errorf("ranking: %+v", got)
	}
	if got[1].Path != "b.go" || got[1].Touches != 2 {
		t.Errorf("ranking: %+v", got)
	}
}

func TestSplitRecap(t *testing.T) {
	tests := []struct {
		name, in, title, summary string
	}{
		{"title and bullets", "Retry work\n- one\n- two", "Retry work", "- one\n- two"},
		{"title only", "Retry work", "Retry work", ""},
		{"bullets only", "- one\n- two", "", "- one\n- two"},
		// Indistinguishable from bullets-only, so it is read as bullets-only:
		// never invent a title out of a bullet.
		{"leading bullet is all bullets", "- Retry work\n- one", "", "- Retry work\n- one"},
		{"empty", "   ", "", ""},
		// A model reaches for markdown out of habit; the title is rendered as
		// a field, so the decoration would arrive showing.
		{"bold title", "**Retry work**\n- one", "Retry work", "- one"},
		{"heading title", "## Retry work\n- one", "Retry work", "- one"},
		{"trailing colon", "Retry work:\n- one", "Retry work", "- one"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			title, summary := splitRecap(tc.in)
			if title != tc.title || summary != tc.summary {
				t.Errorf("splitRecap(%q) = (%q, %q), want (%q, %q)", tc.in, title, summary, tc.title, tc.summary)
			}
		})
	}
}

func TestEstimateTokens_MatchesTheChokePoint(t *testing.T) {
	// finalize.go estimates len(json)/4. If these two ever disagree, a response
	// this package considers safe can still be rejected downstream.
	v := map[string]string{"a": strings.Repeat("x", 100)}
	b, _ := json.Marshal(v)
	if got, want := estimateTokens(v), len(b)/4; got != want {
		t.Errorf("estimateTokens = %d, want %d", got, want)
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
