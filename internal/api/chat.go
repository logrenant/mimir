package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/logrenant/mimir/internal/store"
)

// ChatArchiveReader is the verbatim conversation archive behind the /chat
// routes. *store.Store satisfies it.
//
// Read-only on purpose: nothing about a stored conversation is a decision a
// client gets to change. The archive is written by the ingest loop and by
// nothing else.
type ChatArchiveReader interface {
	ListChatSessions(ctx context.Context, f store.ChatFilter) ([]store.ChatSessionRow, error)
	ChatSession(ctx context.Context, id string) (store.ChatSessionRow, bool, error)
	ChatTurns(ctx context.Context, sessionRowID string, limit, offset int) ([]store.ChatTurnRow, error)
	SearchChatTurns(ctx context.Context, query, projectPath string, limit int) ([]store.ChatTurnRow, error)
}

type chatSessionView struct {
	ID        string `json:"id"`
	Source    string `json:"source_kind"`
	SessionID string `json:"session_id,omitempty"`
	Project   string `json:"project,omitempty"`
	Branch    string `json:"git_branch,omitempty"`
	Title     string `json:"title,omitempty"`
	StartedAt int64  `json:"started_at"`
	EndedAt   int64  `json:"ended_at"`
	Turns     int    `json:"turn_count"`
}

type chatTurnView struct {
	Key       string   `json:"key"`
	SessionID string   `json:"session_id,omitempty"`
	Source    string   `json:"source_kind,omitempty"`
	Project   string   `json:"project,omitempty"`
	StartedAt int64    `json:"started_at"`
	EndedAt   int64    `json:"ended_at,omitempty"`
	Prompt    string   `json:"user_prompt,omitempty"`
	Assistant string   `json:"assistant_text,omitempty"`
	Files     []string `json:"files,omitempty"`
	Commands  []string `json:"commands,omitempty"`
	Tools     int      `json:"tool_calls,omitempty"`
}

// chatProject turns the opaque `project` parameter into a path. A raw path is
// refused rather than used — the same rule /brain/graph applies, and for the
// same reason: a filter is not a place to reintroduce a path the client chose.
func (s *Server) chatProject(ctx context.Context, raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if id == "" {
		return "", nil
	}
	if strings.ContainsAny(id, "/\\") || strings.HasPrefix(id, "~") {
		return "", errProjectIsAnID
	}
	if s.deps.Projects == nil {
		return "", errUnknownProject
	}
	rows, err := s.deps.Projects.List(ctx)
	if err != nil {
		return "", err
	}
	for _, p := range rows {
		if p.ID == id {
			return p.Path, nil
		}
	}
	return "", errUnknownProject
}

// handleListChatSessions serves the conversation list, most recently ended
// first.
func (s *Server) handleListChatSessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	limit, err := graphLimit(q.Get("limit"), s.cfg.ChatSessionsMax, s.cfg.ChatSessionsMax)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, "limit must be a positive integer")
		return
	}
	project, err := s.chatProject(r.Context(), q.Get("project"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}

	rows, err := s.deps.Chat.ListChatSessions(r.Context(), store.ChatFilter{
		ProjectPath: project,
		SourceKind:  strings.TrimSpace(q.Get("source_kind")),
		Limit:       limit,
	})
	if err != nil {
		writeDomainError(w, r, err)
		return
	}

	out := make([]chatSessionView, 0, len(rows))
	for _, c := range rows {
		out = append(out, chatSessionView{
			ID:        c.ID,
			Source:    c.SourceKind,
			SessionID: c.SessionID,
			Project:   projectLabel(c.ProjectPath),
			Branch:    c.GitBranch,
			Title:     c.Title,
			StartedAt: c.StartedAt.Unix(),
			EndedAt:   c.EndedAt.Unix(),
			Turns:     c.TurnCount,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

// handleChatSession serves one conversation: its header and a page of turns.
func (s *Server) handleChatSession(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "id is required")
		return
	}

	limit, err := graphLimit(r.URL.Query().Get("limit"), s.cfg.ChatTurnsPage, s.cfg.ChatTurnsMax)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, "limit must be a positive integer")
		return
	}
	offset := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, codeBadRequest, "offset must be a non-negative integer")
			return
		}
		offset = n
	}

	session, ok, err := s.deps.Chat.ChatSession(r.Context(), id)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "no such conversation")
		return
	}

	turns, err := s.deps.Chat.ChatTurns(r.Context(), id, limit, offset)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"session": chatSessionView{
			ID:        session.ID,
			Source:    session.SourceKind,
			SessionID: session.SessionID,
			Project:   projectLabel(session.ProjectPath),
			Branch:    session.GitBranch,
			Title:     session.Title,
			StartedAt: session.StartedAt.Unix(),
			EndedAt:   session.EndedAt.Unix(),
			Turns:     session.TurnCount,
		},
		"turns":  chatTurnViews(turns),
		"limit":  limit,
		"offset": offset,
	})
}

// handleSearchChat is full-text over what was actually said.
func (s *Server) handleSearchChat(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	query := strings.TrimSpace(q.Get("q"))
	if query == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "q is required")
		return
	}
	limit, err := graphLimit(q.Get("limit"), s.cfg.ChatSearchDefault, s.cfg.ChatSearchMax)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, "limit must be a positive integer")
		return
	}
	project, err := s.chatProject(r.Context(), q.Get("project"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}

	turns, err := s.deps.Chat.SearchChatTurns(r.Context(), query, project, limit)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"turns": chatTurnViews(turns)})
}

func chatTurnViews(rows []store.ChatTurnRow) []chatTurnView {
	out := make([]chatTurnView, 0, len(rows))
	for _, t := range rows {
		v := chatTurnView{
			Key:       t.EpisodeKey,
			SessionID: t.SessionID,
			Source:    t.SourceKind,
			Project:   projectLabel(t.ProjectPath),
			StartedAt: t.StartedAt.Unix(),
			Prompt:    t.UserPrompt,
			Assistant: t.AssistantText,
		}
		if !t.EndedAt.Equal(t.StartedAt) {
			v.EndedAt = t.EndedAt.Unix()
		}
		// The three JSON columns are decoded here rather than passed through as
		// strings: a client should not have to parse a field twice, and a row
		// written by a future writer with unparseable JSON is still a usable
		// turn — it loses a list, not the conversation.
		_ = json.Unmarshal([]byte(t.FilesJSON), &v.Files)
		_ = json.Unmarshal([]byte(t.CommandsJSON), &v.Commands)
		var calls []struct{}
		if json.Unmarshal([]byte(t.ToolCallsJSON), &calls) == nil {
			v.Tools = len(calls)
		}
		out = append(out, v)
	}
	return out
}
