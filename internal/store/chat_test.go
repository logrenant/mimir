package store

import (
	"context"
	"testing"
	"time"
)

func turn(key, prompt, assistant string) ChatTurnRow {
	now := time.Unix(1_700_000_000, 0).UTC()
	return ChatTurnRow{
		EpisodeKey:    key,
		SessionID:     "sess-1",
		ProjectPath:   "/repo",
		SourceKind:    "claude_code",
		SourcePath:    "/transcripts/sess-1.jsonl",
		StartedAt:     now,
		EndedAt:       now.Add(time.Minute),
		UserPrompt:    prompt,
		AssistantText: assistant,
		FilesJSON:     `["internal/store/chat.go"]`,
		CommandsJSON:  `["make check"]`,
		ToolCallsJSON: `[{"Name":"Bash"}]`,
	}
}

func TestPutChatTurn_RoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	tr := turn("ep-1", "arşivi nasıl kuralım", "şöyle kurarız")
	if err := s.PutChatTurn(ctx, tr); err != nil {
		t.Fatalf("PutChatTurn: %v", err)
	}

	id := ChatSessionID(tr.SourceKind, tr.SourcePath, tr.SessionID)
	turns, err := s.ChatTurns(ctx, id, 10, 0)
	if err != nil || len(turns) != 1 {
		t.Fatalf("ChatTurns: %d turns, err=%v", len(turns), err)
	}
	if turns[0].UserPrompt != "arşivi nasıl kuralım" || turns[0].AssistantText != "şöyle kurarız" {
		t.Fatalf("the conversation did not survive: %+v", turns[0])
	}

	sessions, err := s.ListChatSessions(ctx, ChatFilter{Limit: 10})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("ListChatSessions: %d, err=%v", len(sessions), err)
	}
	if sessions[0].TurnCount != 1 || sessions[0].Title != "arşivi nasıl kuralım" {
		t.Fatalf("session header mismatch: %+v", sessions[0])
	}
}

// A transcript still being written is re-read from its offset, so its trailing
// turn arrives again. It must update, not duplicate — and the session's count
// must not drift upward on every pass.
func TestPutChatTurn_ReReadUpdatesRatherThanDuplicates(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.PutChatTurn(ctx, turn("ep-1", "soru", "kısmi cevap")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := s.PutChatTurn(ctx, turn("ep-1", "soru", "tamamlanmış cevap")); err != nil {
		t.Fatalf("second write: %v", err)
	}

	id := ChatSessionID("claude_code", "/transcripts/sess-1.jsonl", "sess-1")
	turns, _ := s.ChatTurns(ctx, id, 10, 0)
	if len(turns) != 1 {
		t.Fatalf("want 1 turn after a re-read, got %d", len(turns))
	}
	if turns[0].AssistantText != "tamamlanmış cevap" {
		t.Errorf("the re-read must win: %q", turns[0].AssistantText)
	}
	sessions, _ := s.ListChatSessions(ctx, ChatFilter{Limit: 10})
	if sessions[0].TurnCount != 1 {
		t.Errorf("turn count drifted to %d", sessions[0].TurnCount)
	}
}

func TestSearchChatTurns_FindsWhatWasSaid(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.PutChatTurn(ctx, turn("ep-1", "migration nasıl eklenir", "append-only, sıradaki numarayı ekle")); err != nil {
		t.Fatalf("PutChatTurn: %v", err)
	}
	if err := s.PutChatTurn(ctx, turn("ep-2", "hava nasıl", "bilmiyorum")); err != nil {
		t.Fatalf("PutChatTurn: %v", err)
	}

	got, err := s.SearchChatTurns(ctx, "migration", "/repo", 10)
	if err != nil {
		t.Fatalf("SearchChatTurns: %v", err)
	}
	if len(got) != 1 || got[0].EpisodeKey != "ep-1" {
		t.Fatalf("want ep-1, got %+v", got)
	}
	if other, _ := s.SearchChatTurns(ctx, "migration", "/elsewhere", 10); len(other) != 0 {
		t.Error("another project must not match")
	}
	// An unparseable question is a miss, never an error — the same contract the
	// memory search has.
	if noisy, err := s.SearchChatTurns(ctx, `" OR *`, "", 10); err != nil || len(noisy) != 0 {
		t.Errorf("FTS syntax from a human must be a miss: %d rows, err=%v", len(noisy), err)
	}
}

// External-content FTS5 keeps no copy of its own. Without the 'delete' row
// before each re-insert the index answers with words the archive no longer
// holds.
func TestSearchChatTurns_ReindexesOnUpdate(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.PutChatTurn(ctx, turn("ep-1", "kadmiyum hakkında", "ilk cevap")); err != nil {
		t.Fatalf("PutChatTurn: %v", err)
	}
	if err := s.PutChatTurn(ctx, turn("ep-1", "vanadyum hakkında", "ikinci cevap")); err != nil {
		t.Fatalf("PutChatTurn: %v", err)
	}

	if stale, _ := s.SearchChatTurns(ctx, "kadmiyum", "", 10); len(stale) != 0 {
		t.Errorf("the old text still matches: %+v", stale)
	}
	if fresh, _ := s.SearchChatTurns(ctx, "vanadyum", "", 10); len(fresh) != 1 {
		t.Errorf("the new text does not match: %d rows", len(fresh))
	}
}

func TestPutChatTurn_DropsAnEmptyExchange(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.PutChatTurn(ctx, turn("ep-1", "", "")); err != nil {
		t.Fatalf("PutChatTurn: %v", err)
	}
	if sessions, _ := s.ListChatSessions(ctx, ChatFilter{Limit: 10}); len(sessions) != 0 {
		t.Fatalf("an empty exchange must not make a session: %+v", sessions)
	}
}

func TestChatSession_MissAndFilters(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.PutChatTurn(ctx, turn("ep-1", "soru", "cevap")); err != nil {
		t.Fatalf("PutChatTurn: %v", err)
	}
	run := turn("ep-2", "koşu", "çıktı")
	run.SourceKind = "mimir_run"
	run.SourcePath = "/transcripts/run-1.jsonl"
	run.SessionID = "run-1"
	if err := s.PutChatTurn(ctx, run); err != nil {
		t.Fatalf("PutChatTurn: %v", err)
	}

	if _, ok, err := s.ChatSession(ctx, "no-such-session"); ok || err != nil {
		t.Errorf("an unknown id must miss: ok=%v err=%v", ok, err)
	}
	if all, _ := s.ListChatSessions(ctx, ChatFilter{Limit: 10}); len(all) != 2 {
		t.Fatalf("want both conversations, got %d", len(all))
	}
	runs, _ := s.ListChatSessions(ctx, ChatFilter{SourceKind: "mimir_run", Limit: 10})
	if len(runs) != 1 || runs[0].SessionID != "run-1" {
		t.Fatalf("source_kind filter: %+v", runs)
	}
	if none, _ := s.ListChatSessions(ctx, ChatFilter{ProjectPath: "/elsewhere", Limit: 10}); len(none) != 0 {
		t.Error("another project must not match")
	}
}

func TestChatArchive_NilStoreTolerated(t *testing.T) {
	ctx := context.Background()
	var s *Store

	if err := s.PutChatTurn(ctx, turn("ep-1", "soru", "cevap")); err != nil {
		t.Errorf("PutChatTurn on a nil store: %v", err)
	}
	if rows, err := s.ListChatSessions(ctx, ChatFilter{Limit: 10}); err != nil || rows != nil {
		t.Errorf("ListChatSessions on a nil store: %v %v", rows, err)
	}
	if _, ok, err := s.ChatSession(ctx, "x"); ok || err != nil {
		t.Errorf("ChatSession on a nil store: %v %v", ok, err)
	}
	if rows, err := s.ChatTurns(ctx, "x", 10, 0); err != nil || rows != nil {
		t.Errorf("ChatTurns on a nil store: %v %v", rows, err)
	}
	if rows, err := s.SearchChatTurns(ctx, "q", "", 10); err != nil || rows != nil {
		t.Errorf("SearchChatTurns on a nil store: %v %v", rows, err)
	}
}

func TestChatArchive_MigrationIdempotent(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)

	first, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := first.PutChatTurn(ctx, turn("ep-1", "kalıcı mı", "evet")); err != nil {
		t.Fatalf("PutChatTurn: %v", err)
	}
	_ = first.Close()

	second, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer func() { _ = second.Close() }()

	got, err := second.SearchChatTurns(ctx, "kalıcı", "", 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("the archive and its index must survive a reopen: %d rows, err=%v", len(got), err)
	}
}
