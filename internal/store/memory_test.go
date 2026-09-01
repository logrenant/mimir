package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func testEpisode(key string) EpisodeRow {
	return EpisodeRow{
		Key:          key,
		ProjectPath:  "/work/demo",
		SourceKind:   "claude_code",
		SourcePath:   "/transcripts/a.jsonl",
		SessionID:    "sess-1",
		GitBranch:    "main",
		StartedAt:    time.Unix(1700000000, 0).UTC(),
		EndedAt:      time.Unix(1700000600, 0).UTC(),
		FactsJSON:    `{"files":["internal/mcp/finalize.go"]}`,
		FilesText:    "internal/mcp/finalize.go",
		CommandsText: "make check",
		InputTokens:  1200,
		OutputTokens: 300,
		CostUSD:      0.004,
		Significance: 5,
	}
}

func TestEpisode_RoundTrip(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	want := testEpisode("k1")

	if err := s.PutEpisode(ctx, want); err != nil {
		t.Fatalf("PutEpisode: %v", err)
	}

	got, err := s.RecentEpisodes(ctx, want.ProjectPath, 10)
	if err != nil {
		t.Fatalf("RecentEpisodes: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 episode, got %d", len(got))
	}
	if got[0].Key != want.Key || got[0].FilesText != want.FilesText ||
		got[0].InputTokens != want.InputTokens || got[0].Significance != want.Significance {
		t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", got[0], want)
	}
	if !got[0].StartedAt.Equal(want.StartedAt) {
		t.Errorf("StartedAt: got %v, want %v", got[0].StartedAt, want.StartedAt)
	}
}

// Phase 1 of an ingest re-derives every episode it sees. If that overwrote the
// refined half, every pass would throw away the model calls the previous pass
// paid for and the backfill would never converge.
func TestPutEpisode_DoesNotClobberAnExistingRecap(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	e := testEpisode("k1")

	if err := s.PutEpisode(ctx, e); err != nil {
		t.Fatalf("PutEpisode: %v", err)
	}
	if err := s.UpdateRecap(ctx, e.Key, "chokepoint work", "added the isolation gate", "v1"); err != nil {
		t.Fatalf("UpdateRecap: %v", err)
	}

	// A later ingest pass sees the same episode again with fresh facts.
	e.CommandsText = "make race"
	if err := s.PutEpisode(ctx, e); err != nil {
		t.Fatalf("PutEpisode (second pass): %v", err)
	}

	got, err := s.RecentEpisodes(ctx, e.ProjectPath, 10)
	if err != nil {
		t.Fatalf("RecentEpisodes: %v", err)
	}
	if got[0].Summary != "added the isolation gate" {
		t.Errorf("summary was clobbered by a re-ingest: %q", got[0].Summary)
	}
	if got[0].Title != "chokepoint work" {
		t.Errorf("title was clobbered by a re-ingest: %q", got[0].Title)
	}
	if got[0].CommandsText != "make race" {
		t.Errorf("deterministic facts were not refreshed: %q", got[0].CommandsText)
	}
	if got[0].RecapAttempts != 1 {
		t.Errorf("RecapAttempts: got %d, want 1", got[0].RecapAttempts)
	}
}

func TestPendingRecap_Selection(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	low := testEpisode("low")
	low.Significance = 1
	high := testEpisode("high")
	high.Significance = 9
	trivial := testEpisode("trivial")
	trivial.Significance = 0 // filtered out before ever reaching a model
	for _, e := range []EpisodeRow{low, high, trivial} {
		if err := s.PutEpisode(ctx, e); err != nil {
			t.Fatalf("PutEpisode %s: %v", e.Key, err)
		}
	}

	got, err := s.PendingRecap(ctx, "/work/demo", "v1", 2, 10)
	if err != nil {
		t.Fatalf("PendingRecap: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 pending (the insignificant one excluded), got %d", len(got))
	}
	if got[0].Key != "high" {
		t.Errorf("want highest significance first, got %q", got[0].Key)
	}

	if err := s.UpdateRecap(ctx, "high", "t", "s", "v1"); err != nil {
		t.Fatalf("UpdateRecap: %v", err)
	}
	got, _ = s.PendingRecap(ctx, "/work/demo", "v1", 2, 10)
	if len(got) != 1 || got[0].Key != "low" {
		t.Fatalf("a recapped episode should drop out of the pending set, got %+v", got)
	}

	// Bumping the prompt version makes every stored recap eligible again.
	got, _ = s.PendingRecap(ctx, "/work/demo", "v2", 2, 10)
	if len(got) != 2 {
		t.Fatalf("a prompt-version bump must re-open recapped episodes, got %d", len(got))
	}
}

// A recap that keeps getting rejected must stop costing model calls.
func TestPendingRecap_RespectsAttemptCeiling(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.PutEpisode(ctx, testEpisode("k1")); err != nil {
		t.Fatalf("PutEpisode: %v", err)
	}

	for i := range 2 {
		got, _ := s.PendingRecap(ctx, "/work/demo", "v1", 2, 10)
		if len(got) != 1 {
			t.Fatalf("attempt %d: want the episode still pending, got %d", i, len(got))
		}
		// A rejected recap writes empty text but still counts as an attempt.
		if err := s.UpdateRecap(ctx, "k1", "", "", "v1"); err != nil {
			t.Fatalf("UpdateRecap: %v", err)
		}
	}

	got, _ := s.PendingRecap(ctx, "/work/demo", "v1", 2, 10)
	if len(got) != 0 {
		t.Fatalf("want the episode retired after 2 attempts, got %d", len(got))
	}
	n, err := s.CountPendingRecap(ctx, "/work/demo", "v1", 2)
	if err != nil || n != 0 {
		t.Fatalf("CountPendingRecap: got %d err=%v, want 0", n, err)
	}
}

func TestSearchEpisodes_FindsAndRanks(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	a := testEpisode("a")
	a.FilesText = "internal/mcp/finalize.go"
	b := testEpisode("b")
	b.FilesText = "internal/leadgen/gap.go"
	other := testEpisode("other")
	other.ProjectPath = "/work/somethingelse"
	other.FilesText = "internal/mcp/finalize.go"
	for _, e := range []EpisodeRow{a, b, other} {
		if err := s.PutEpisode(ctx, e); err != nil {
			t.Fatalf("PutEpisode: %v", err)
		}
	}
	if err := s.UpdateRecap(ctx, "a", "chokepoint", "added the isolation gate", "v1"); err != nil {
		t.Fatalf("UpdateRecap: %v", err)
	}

	got, err := s.SearchEpisodes(ctx, "/work/demo", "isolation gate", 10)
	if err != nil {
		t.Fatalf("SearchEpisodes: %v", err)
	}
	if len(got) != 1 || got[0].Key != "a" {
		t.Fatalf("want only episode a, got %+v", got)
	}

	// Another project's episode indexes the same path and must not leak.
	got, _ = s.SearchEpisodes(ctx, "/work/demo", "finalize", 10)
	for _, e := range got {
		if e.ProjectPath != "/work/demo" {
			t.Fatalf("search leaked across projects: %+v", e)
		}
	}
}

// The external-content FTS index keeps no copy of its own, so an upsert that
// did not fire the delete/re-insert pair would leave the old terms matching
// forever — a memory that answers with text it no longer holds.
func TestSearchEpisodes_ReindexesOnUpdate(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.PutEpisode(ctx, testEpisode("k1")); err != nil {
		t.Fatalf("PutEpisode: %v", err)
	}
	if err := s.UpdateRecap(ctx, "k1", "first", "places api is the primary source", "v1"); err != nil {
		t.Fatalf("UpdateRecap: %v", err)
	}
	if got, _ := s.SearchEpisodes(ctx, "/work/demo", "primary", 10); len(got) != 1 {
		t.Fatalf("want a hit before the rewrite, got %d", len(got))
	}

	if err := s.UpdateRecap(ctx, "k1", "second", "scraping is the fallback", "v1"); err != nil {
		t.Fatalf("UpdateRecap: %v", err)
	}
	if got, _ := s.SearchEpisodes(ctx, "/work/demo", "primary", 10); len(got) != 0 {
		t.Errorf("stale term still matches after a rewrite: %d hits", len(got))
	}
	if got, _ := s.SearchEpisodes(ctx, "/work/demo", "fallback", 10); len(got) != 1 {
		t.Errorf("new term does not match after a rewrite")
	}
}

// FTS5's query language is a syntax. A natural-language question containing
// its operators must degrade to a miss, never to a SQL error.
func TestSearchEpisodes_HostileQueriesDoNotError(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	if err := s.PutEpisode(ctx, testEpisode("k1")); err != nil {
		t.Fatalf("PutEpisode: %v", err)
	}

	for _, q := range []string{
		`"`, `*`, `OR`, `NEAR(a b)`, `foo AND (bar`, `-x`, `a:b`, `^`, ``, `   `,
		`why did we pick "places" OR scraping? -- see internal/maps/*`,
	} {
		if _, err := s.SearchEpisodes(ctx, "/work/demo", q, 10); err != nil {
			t.Errorf("SearchEpisodes(%q): %v, want a miss not an error", q, err)
		}
	}
}

func TestFTSQuery_Shape(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain words", "isolation gate", `"isolation" OR "gate"`},
		{"operators stripped", `places OR "scraping"*`, `"places" OR "or" OR "scraping"`},
		{"single chars dropped", "a bc d ef", `"bc" OR "ef"`},
		{"duplicates collapsed", "gate gate GATE", `"gate"`},
		{"punctuation only", "?! -- ***", ``},
		{"paths split into terms", "internal/mcp/finalize.go", `"internal" OR "mcp" OR "finalize" OR "go"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ftsQuery(tc.in); got != tc.want {
				t.Errorf("ftsQuery(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNotes_RoundTrip(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	older := NoteRow{ID: "n1", ProjectPath: "/work/demo", Kind: "decision",
		Text: "Places API is primary; scraping is fallback only.", CreatedAt: time.Unix(1700000000, 0)}
	newer := NoteRow{ID: "n2", ProjectPath: "/work/demo", Kind: "trap",
		Text: "modernc sqlite needs unaliased FTS tables for MATCH.", CreatedAt: time.Unix(1700009999, 0)}
	for _, n := range []NoteRow{older, newer} {
		if err := s.PutNote(ctx, n); err != nil {
			t.Fatalf("PutNote: %v", err)
		}
	}
	// Blank text is not a note.
	if err := s.PutNote(ctx, NoteRow{ID: "n3", ProjectPath: "/work/demo", Text: "   "}); err != nil {
		t.Fatalf("PutNote(blank): %v", err)
	}

	got, err := s.ListNotes(ctx, "/work/demo", 10)
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 notes, got %d", len(got))
	}
	if got[0].ID != "n2" {
		t.Errorf("want newest first, got %q", got[0].ID)
	}
}

func TestIngestState_RoundTripAndMiss(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if _, ok, err := s.GetIngestState(ctx, "/nope.jsonl"); err != nil || ok {
		t.Fatalf("unknown file: ok=%v err=%v, want a miss and no error", ok, err)
	}

	want := IngestState{SourcePath: "/t/a.jsonl", ProjectPath: "/work/demo", ByteOffset: 4096, SizeSeen: 8192}
	if err := s.PutIngestState(ctx, want); err != nil {
		t.Fatalf("PutIngestState: %v", err)
	}
	got, ok, err := s.GetIngestState(ctx, want.SourcePath)
	if err != nil || !ok {
		t.Fatalf("GetIngestState: ok=%v err=%v", ok, err)
	}
	if got.ByteOffset != 4096 || got.SizeSeen != 8192 || got.ProjectPath != want.ProjectPath {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	want.ByteOffset = 9000
	if err := s.PutIngestState(ctx, want); err != nil {
		t.Fatalf("PutIngestState (advance): %v", err)
	}
	got, _, _ = s.GetIngestState(ctx, want.SourcePath)
	if got.ByteOffset != 9000 {
		t.Errorf("offset did not advance: %d", got.ByteOffset)
	}
}

func TestMemoryStats(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	a := testEpisode("a")
	b := testEpisode("b")
	b.StartedAt = time.Unix(1700009999, 0).UTC()
	for _, e := range []EpisodeRow{a, b} {
		if err := s.PutEpisode(ctx, e); err != nil {
			t.Fatalf("PutEpisode: %v", err)
		}
	}
	if err := s.UpdateRecap(ctx, "a", "t", "s", "v1"); err != nil {
		t.Fatalf("UpdateRecap: %v", err)
	}
	if err := s.PutNote(ctx, NoteRow{ID: "n1", ProjectPath: "/work/demo", Kind: "decision", Text: "x"}); err != nil {
		t.Fatalf("PutNote: %v", err)
	}

	st, err := s.MemoryStats(ctx, "/work/demo")
	if err != nil {
		t.Fatalf("MemoryStats: %v", err)
	}
	if st.Episodes != 2 || st.WithSummary != 1 || st.Notes != 1 {
		t.Fatalf("unexpected rollup: %+v", st)
	}
	if st.OldestEpisode != 1700000000 || st.NewestEpisode != 1700009999 {
		t.Errorf("time bounds: %+v", st)
	}
}

// An empty project has no memory, which is a zero rollup, not an error.
func TestMemoryStats_EmptyProject(t *testing.T) {
	s := openTestStore(t)
	st, err := s.MemoryStats(context.Background(), "/work/nothing")
	if err != nil {
		t.Fatalf("MemoryStats: %v", err)
	}
	if st.Episodes != 0 || st.OldestEpisode != 0 {
		t.Fatalf("want a zero rollup, got %+v", st)
	}
}

func TestNilStore_MemoryAccessorsAreSafe(t *testing.T) {
	var s *Store
	ctx := context.Background()

	if err := s.PutEpisode(ctx, testEpisode("k1")); err != nil {
		t.Fatalf("PutEpisode on nil store: %v", err)
	}
	if err := s.UpdateRecap(ctx, "k1", "t", "s", "v1"); err != nil {
		t.Fatalf("UpdateRecap on nil store: %v", err)
	}
	if got, err := s.PendingRecap(ctx, "/p", "v1", 2, 10); err != nil || got != nil {
		t.Fatalf("PendingRecap on nil store: %v / %v", got, err)
	}
	if got, err := s.CountPendingRecap(ctx, "/p", "v1", 2); err != nil || got != 0 {
		t.Fatalf("CountPendingRecap on nil store: %v / %v", got, err)
	}
	if got, err := s.RecentEpisodes(ctx, "/p", 10); err != nil || got != nil {
		t.Fatalf("RecentEpisodes on nil store: %v / %v", got, err)
	}
	if got, err := s.SearchEpisodes(ctx, "/p", "anything", 10); err != nil || got != nil {
		t.Fatalf("SearchEpisodes on nil store: %v / %v", got, err)
	}
	if err := s.PutNote(ctx, NoteRow{ID: "n", ProjectPath: "/p", Text: "t"}); err != nil {
		t.Fatalf("PutNote on nil store: %v", err)
	}
	if got, err := s.ListNotes(ctx, "/p", 10); err != nil || got != nil {
		t.Fatalf("ListNotes on nil store: %v / %v", got, err)
	}
	if _, ok, err := s.GetIngestState(ctx, "/t/a.jsonl"); err != nil || ok {
		t.Fatalf("GetIngestState on nil store: ok=%v err=%v", ok, err)
	}
	if err := s.PutIngestState(ctx, IngestState{SourcePath: "/t/a.jsonl"}); err != nil {
		t.Fatalf("PutIngestState on nil store: %v", err)
	}
	if st, err := s.MemoryStats(ctx, "/p"); err != nil || st.Episodes != 0 {
		t.Fatalf("MemoryStats on nil store: %+v / %v", st, err)
	}
}

// Migrations are append-only and Open is idempotent; 0008 adds triggers and a
// virtual table, which are the two things most likely to break a re-open.
func TestOpen_IsIdempotentWithMemorySchema(t *testing.T) {
	cfg := testConfig(t)
	ctx := context.Background()

	s, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.PutEpisode(ctx, testEpisode("k1")); err != nil {
		t.Fatalf("PutEpisode: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })

	got, err := s2.RecentEpisodes(ctx, "/work/demo", 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("episode did not survive a reopen: %d rows, err=%v", len(got), err)
	}
	if hits, _ := s2.SearchEpisodes(ctx, "/work/demo", "finalize", 10); len(hits) != 1 {
		t.Fatalf("fts index did not survive a reopen: %d hits", len(hits))
	}
}

func TestPrefixed(t *testing.T) {
	got := prefixed("key, project_path,\n\tsource_kind", "e")
	want := "e.key, e.project_path, e.source_kind"
	if got != want {
		t.Errorf("prefixed() = %q, want %q", got, want)
	}
	// Every column in the shared list must be qualified, or the join is ambiguous.
	if strings.Count(prefixed(episodeColumns, "e"), "e.") != 19 {
		t.Errorf("episodeColumns qualification count changed: %q", prefixed(episodeColumns, "e"))
	}
}

// A memory needs a way to forget: an episode the parser has since learned to
// recognise as noise, or one a person wants gone. The index must go with it —
// a half-forgotten episode that still matched a search would be worse than one
// that was never stored.
func TestDeleteEpisode_RemovesTheIndexEntryToo(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	e := testEpisode("k1")
	if err := s.PutEpisode(ctx, e); err != nil {
		t.Fatalf("PutEpisode: %v", err)
	}
	if err := s.UpdateRecap(ctx, e.Key, "chokepoint", "added the isolation gate", "v1"); err != nil {
		t.Fatalf("UpdateRecap: %v", err)
	}
	if hits, _ := s.SearchEpisodes(ctx, e.ProjectPath, "isolation", 10); len(hits) != 1 {
		t.Fatal("precondition: the episode should be findable")
	}

	if err := s.DeleteEpisode(ctx, e.Key); err != nil {
		t.Fatalf("DeleteEpisode: %v", err)
	}
	if rows, _ := s.RecentEpisodes(ctx, e.ProjectPath, 10); len(rows) != 0 {
		t.Errorf("row survived deletion: %+v", rows)
	}
	if hits, _ := s.SearchEpisodes(ctx, e.ProjectPath, "isolation", 10); len(hits) != 0 {
		t.Errorf("the index still matches a deleted episode: %d hits", len(hits))
	}

	// Deleting what is not there is not an error.
	if err := s.DeleteEpisode(ctx, "never-existed"); err != nil {
		t.Errorf("deleting an unknown key: %v", err)
	}
}

// A presentation fix must not cost a re-recap, and must not look like an
// attempt: only UpdateRecap records those.
func TestSetEpisodeTitle_DoesNotTouchTheSummaryOrTheAttemptCount(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	e := testEpisode("k1")
	if err := s.PutEpisode(ctx, e); err != nil {
		t.Fatalf("PutEpisode: %v", err)
	}
	if err := s.UpdateRecap(ctx, e.Key, "**bold**", "- a summary", "v1"); err != nil {
		t.Fatalf("UpdateRecap: %v", err)
	}

	if err := s.SetEpisodeTitle(ctx, e.Key, "plain"); err != nil {
		t.Fatalf("SetEpisodeTitle: %v", err)
	}
	rows, _ := s.RecentEpisodes(ctx, e.ProjectPath, 10)
	if rows[0].Title != "plain" {
		t.Errorf("Title = %q", rows[0].Title)
	}
	if rows[0].Summary != "- a summary" {
		t.Errorf("the summary was disturbed: %q", rows[0].Summary)
	}
	if rows[0].RecapAttempts != 1 {
		t.Errorf("RecapAttempts = %d, want 1 (a retitle is not an attempt)", rows[0].RecapAttempts)
	}
	// The new title must be searchable, the old one must not.
	if hits, _ := s.SearchEpisodes(ctx, e.ProjectPath, "plain", 10); len(hits) != 1 {
		t.Errorf("the retitled episode is not findable by its new title")
	}
}
