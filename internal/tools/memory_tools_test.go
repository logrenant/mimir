package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/memory"
	"github.com/logrenant/mimir/internal/project"
	"github.com/logrenant/mimir/internal/store"
)

func memoryFixture(t *testing.T) (config.Config, *memory.Memory, *store.Store, string) {
	t.Helper()

	// Canonicalized up front: the tools resolve every path through
	// project.Canonicalize, which runs EvalSymlinks, and on macOS a temp dir
	// under /var resolves to /private/var. Seeding the store with the
	// unresolved spelling would key rows the tools then never find.
	projectPath, err := project.Canonicalize(t.TempDir())
	if err != nil {
		t.Fatalf("canonicalize temp project: %v", err)
	}

	cfg := config.Load()
	cfg.StorePath = filepath.Join(t.TempDir(), "mimir.db")
	cfg.ClaudeProjectsDir = t.TempDir() // Empty: these tests seed the store directly.

	db, err := store.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return cfg, memory.New(cfg, db, nil, nil), db, projectPath
}

// finalizeLike reproduces internal/mcp/finalize.go's checks. The real one is
// unexported, and a copy that drifts is worse than none — so this asserts the
// same three things the choke-point asserts, and TestEveryToolResponseIsGateable
// covers the marker interface separately.
func finalizeLike(t *testing.T, v any, budget int) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)

	for _, sig := range []string{"<html", `<html`, "<script", `<script`} {
		if strings.Contains(s, sig) {
			t.Fatalf("response carries %q and would be rejected as an isolation violation:\n%s", sig, s)
		}
	}
	if strings.Contains(s, "data:image/") && strings.Contains(s, ";base64,") {
		t.Fatalf("response carries an image payload and would be rejected")
	}
	if got := len(b) / 4; budget > 0 && got > budget {
		t.Fatalf("response is ~%d tokens, over its %d budget", got, budget)
	}
}

func TestProjectContext_ReturnsAGateableBrief(t *testing.T) {
	cfg, mem, _, projectPath := memoryFixture(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("A demo service.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := mem.Remember(ctx, memory.Project{Path: projectPath}, "decision", "Places API is primary."); err != nil {
		t.Fatal(err)
	}

	tool := NewProjectContext(cfg, mem, nil)
	args, _ := json.Marshal(map[string]string{"project_path": projectPath})

	got, err := tool.Handle(ctx, args)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	resp, ok := got.(projectContextResponse)
	if !ok {
		t.Fatalf("unexpected response type %T", got)
	}
	if !resp.IsRefined() {
		t.Error("the brief must be marked refined or the choke-point rejects it")
	}
	if len(resp.Notes) != 1 {
		t.Errorf("pinned note missing: %+v", resp.Notes)
	}
	finalizeLike(t, resp, resp.SizeBudgetTokens())
}

// A single episode quoting markup — from any session that scraped a page —
// would otherwise make every future call to this tool fail closed, taking the
// whole memory dark for a reason nothing in the error would explain.
func TestProjectContext_MarkupInStoredContentDoesNotDisableTheTool(t *testing.T) {
	cfg, mem, db, projectPath := memoryFixture(t)
	ctx := context.Background()

	if err := db.PutEpisode(ctx, store.EpisodeRow{
		Key:         "k1",
		ProjectPath: projectPath,
		SourceKind:  "claude_code",
		FactsJSON:   `{"prompt":"why does <html><script>alert(1)</script> come back raw?","files":["internal/crawl/client.go"]}`,
	}); err != nil {
		t.Fatal(err)
	}
	// And a pinned note doing the same, which a consumer can write directly.
	if _, err := mem.Remember(ctx, memory.Project{Path: projectPath}, "trap",
		"Crawl4AI returns <html> when the page is JS-only; see data:image/png;base64, payloads too."); err != nil {
		t.Fatal(err)
	}

	tool := NewProjectContext(cfg, mem, nil)
	args, _ := json.Marshal(map[string]string{"project_path": projectPath})
	got, err := tool.Handle(ctx, args)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	resp := got.(projectContextResponse)
	finalizeLike(t, resp, resp.SizeBudgetTokens())

	// The fact must survive in readable form — scrubbing is not deletion.
	blob, _ := json.Marshal(resp)
	if !strings.Contains(string(blob), "Crawl4AI returns") {
		t.Errorf("the note was lost rather than neutralized: %s", blob)
	}
}

func TestContextRecall_RequiresAQuery(t *testing.T) {
	cfg, mem, _, projectPath := memoryFixture(t)
	tool := NewContextRecall(cfg, mem, nil)

	args, _ := json.Marshal(map[string]string{"query": "   ", "project_path": projectPath})
	if _, err := tool.Handle(context.Background(), args); err == nil {
		t.Fatal("want an error for a blank query")
	}
}

func TestContextRecall_ReturnsGateableHits(t *testing.T) {
	cfg, mem, db, projectPath := memoryFixture(t)
	ctx := context.Background()

	if err := db.PutEpisode(ctx, store.EpisodeRow{
		Key: "k1", ProjectPath: projectPath, SourceKind: "claude_code",
		FilesText: "internal/crawl/client.go", Significance: 5,
		FactsJSON: `{"prompt":"add a retry to the crawl client","files":["internal/crawl/client.go"]}`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateRecap(ctx, "k1", "Retry in the crawl client", "- chose a fixed delay", "v1"); err != nil {
		t.Fatal(err)
	}

	tool := NewContextRecall(cfg, mem, nil)
	args, _ := json.Marshal(map[string]any{"query": "crawl retry", "project_path": projectPath})
	got, err := tool.Handle(ctx, args)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	resp := got.(contextRecallResponse)
	if len(resp.Hits) != 1 || resp.Hits[0].Title != "Retry in the crawl client" {
		t.Fatalf("unexpected hits: %+v", resp.Hits)
	}
	finalizeLike(t, resp, resp.SizeBudgetTokens())
}

func TestContextRemember_StoresAndIsMetadataOnly(t *testing.T) {
	cfg, mem, _, projectPath := memoryFixture(t)
	tool := NewContextRemember(cfg, mem, nil)
	ctx := context.Background()

	args, _ := json.Marshal(map[string]string{
		"text": "Places API is primary; scraping is the fallback.",
		"kind": "decision", "project_path": projectPath,
	})
	got, err := tool.Handle(ctx, args)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	resp := got.(contextRememberResponse)
	if !resp.MetadataOnly() || resp.Stored.Kind != "decision" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	finalizeLike(t, resp, resp.SizeBudgetTokens())

	args, _ = json.Marshal(map[string]string{"text": "x", "kind": "nonsense", "project_path": projectPath})
	if _, err := tool.Handle(ctx, args); err == nil {
		t.Fatal("want a rejection for an unknown kind")
	}
}

// Reading a project's history must not create the registration that would let
// a coding run start in it. The picker is the only thing that mints that.
func TestMemoryTools_RejectDeniedDirectories(t *testing.T) {
	cfg, mem, _, _ := memoryFixture(t)
	tool := NewProjectContext(cfg, mem, nil)

	for _, path := range []string{"/", "/etc", "/System"} {
		args, _ := json.Marshal(map[string]string{"project_path": path})
		if _, err := tool.Handle(context.Background(), args); err == nil {
			t.Errorf("%q was accepted as a project directory", path)
		}
	}
}

// An empty memory is a normal state on a project that has never been ingested.
// It must answer, not fail: a tool that errors on a cold start looks broken.
func TestProjectContext_EmptyMemoryStillAnswers(t *testing.T) {
	cfg, mem, _, projectPath := memoryFixture(t)
	tool := NewProjectContext(cfg, mem, nil)

	args, _ := json.Marshal(map[string]string{"project_path": projectPath})
	got, err := tool.Handle(context.Background(), args)
	if err != nil {
		t.Fatalf("Handle on an empty memory: %v", err)
	}
	resp := got.(projectContextResponse)
	if resp.Guidance == "" {
		t.Error("even an empty brief carries its guidance")
	}
	if resp.Coverage.Episodes != 0 {
		t.Errorf("coverage should report an empty memory honestly: %+v", resp.Coverage)
	}
	finalizeLike(t, resp, resp.SizeBudgetTokens())
}

// diagnostics is a health check: it must report on the memory without ever
// failing because of it.
func TestDiagnostics_ReportsMemoryWhenPresent(t *testing.T) {
	cfg, mem, _, _ := memoryFixture(t)

	tool := NewDiagnostics(cfg, nil, nil, nil, mem)
	resp, ok := tool.(*diagnosticsTool)
	if !ok {
		t.Fatalf("unexpected tool type %T", tool)
	}

	// The working directory during a test is the package dir, which is inside
	// the repo — a real, allowed path, so the stats block should be present.
	got := resp.memoryStats(context.Background())
	if got == nil {
		t.Fatal("want a memory block for the working directory")
	}
	if got.Project == "" {
		t.Errorf("memory block should name the project: %+v", got)
	}

	// And with no memory at all the block is omitted rather than zeroed:
	// absent and empty are different answers.
	none := (&diagnosticsTool{}).memoryStats(context.Background())
	if none != nil {
		t.Errorf("want no memory block when there is no memory, got %+v", none)
	}
	_ = mem
}
