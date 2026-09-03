package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/store"
)

type fakeChat struct {
	sessions []store.ChatSessionRow
	turns    []store.ChatTurnRow
	found    bool
	query    string
	project  string
	limit    int
}

func (f *fakeChat) ListChatSessions(_ context.Context, filter store.ChatFilter) ([]store.ChatSessionRow, error) {
	f.project = filter.ProjectPath
	f.limit = filter.Limit
	return f.sessions, nil
}

func (f *fakeChat) ChatSession(_ context.Context, id string) (store.ChatSessionRow, bool, error) {
	if !f.found {
		return store.ChatSessionRow{}, false, nil
	}
	return store.ChatSessionRow{ID: id, Title: "arşiv", TurnCount: len(f.turns)}, true, nil
}

func (f *fakeChat) ChatTurns(_ context.Context, _ string, limit, _ int) ([]store.ChatTurnRow, error) {
	f.limit = limit
	return f.turns, nil
}

func (f *fakeChat) SearchChatTurns(_ context.Context, query, projectPath string, limit int) ([]store.ChatTurnRow, error) {
	f.query = query
	f.project = projectPath
	f.limit = limit
	return f.turns, nil
}

func archivedTurn() store.ChatTurnRow {
	now := time.Unix(1_700_000_000, 0).UTC()
	return store.ChatTurnRow{
		EpisodeKey:    "ep-1",
		SourceKind:    "claude_code",
		ProjectPath:   "/repo",
		StartedAt:     now,
		EndedAt:       now,
		UserPrompt:    "migration nasıl eklenir",
		AssistantText: "append-only",
		FilesJSON:     `["internal/store/chat.go"]`,
		CommandsJSON:  `["make check"]`,
		ToolCallsJSON: `[{"Name":"Bash"},{"Name":"Edit"}]`,
	}
}

func TestChatSessions_ListsConversations(t *testing.T) {
	ch := &fakeChat{sessions: []store.ChatSessionRow{{ID: "c1", SourceKind: "mimir_run", Title: "koşu", TurnCount: 3}}}
	h := New(testConfig(), Deps{Chat: ch}).Handler()

	w := do(h, http.MethodGet, "/chat/sessions", testToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "mimir_run") {
		t.Errorf("the Terminals history must be listed too: %s", w.Body.String())
	}
	if ch.limit != testConfig().ChatSessionsMax {
		t.Errorf("want the configured page, got %d", ch.limit)
	}
}

func TestChatSession_ServesTheTurnsVerbatim(t *testing.T) {
	ch := &fakeChat{found: true, turns: []store.ChatTurnRow{archivedTurn()}}
	h := New(testConfig(), Deps{Chat: ch}).Handler()

	w := do(h, http.MethodGet, "/chat/sessions/c1", testToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (%s)", w.Code, w.Body.String())
	}

	var got struct {
		Turns []chatTurnView `json:"turns"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Turns) != 1 || got.Turns[0].Prompt != "migration nasıl eklenir" {
		t.Fatalf("the conversation did not survive the wire: %+v", got.Turns)
	}
	if got.Turns[0].Tools != 2 || len(got.Turns[0].Files) != 1 {
		t.Errorf("the JSON columns were not decoded: %+v", got.Turns[0])
	}
}

func TestChatSession_UnknownIdIs404(t *testing.T) {
	h := New(testConfig(), Deps{Chat: &fakeChat{}}).Handler()

	if w := do(h, http.MethodGet, "/chat/sessions/nope", testToken, ""); w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", w.Code)
	}
}

func TestChatSearch_RequiresAQuery(t *testing.T) {
	ch := &fakeChat{}
	h := New(testConfig(), Deps{Chat: ch}).Handler()

	if w := do(h, http.MethodGet, "/chat/search", testToken, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
	if ch.query != "" {
		t.Error("an empty query must not reach the archive")
	}
}

func TestChatSearch_PassesTheQueryThrough(t *testing.T) {
	ch := &fakeChat{turns: []store.ChatTurnRow{archivedTurn()}}
	h := New(testConfig(), Deps{Chat: ch}).Handler()

	w := do(h, http.MethodGet, "/chat/search?q=migration", testToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if ch.query != "migration" {
		t.Errorf("query lost: %q", ch.query)
	}
	if ch.limit != testConfig().ChatSearchDefault {
		t.Errorf("want the configured default, got %d", ch.limit)
	}
}

// A filter is not a place to reintroduce a path the client chose — the same
// rule /brain/graph applies.
func TestChat_RefusesARawProjectPath(t *testing.T) {
	h := New(testConfig(), Deps{Chat: &fakeChat{}}).Handler()

	if w := do(h, http.MethodGet, "/chat/sessions?project=/Users/x/repo", testToken, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400 (%s)", w.Code, w.Body.String())
	}
}

func TestChatRoutes_NotRegisteredWithoutTheArchive(t *testing.T) {
	h := New(testConfig(), Deps{}).Handler()

	if w := do(h, http.MethodGet, "/chat/sessions", testToken, ""); w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", w.Code)
	}
}
