package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/logrenant/mimir/internal/brain"
)

// The graph reads.
//
// Gated on their own dependency rather than on BrainReader, which is the
// package's own idiom: a route exists when the thing behind it does, and these
// three answer over the parser's edges, which a daemon can perfectly well be
// missing while node detail still works.
//
// They are the same five reads the graph_* MCP tools serve, over the same core.
// One engine, two transports — the desktop asks over HTTP because the WebView
// has no MCP client, not because the answers differ.

// GraphReader is the traversal half of the knowledge base. *brain.Core
// satisfies it.
type GraphReader interface {
	Query(ctx context.Context, projectPath, question string, budget int) (brain.QueryResult, error)
	Affected(ctx context.Context, id string, kinds []string, depth int) ([]brain.Hit, error)
	Hubs(ctx context.Context, projectPath string, top int) ([]brain.Hit, error)
}

type graphQueryRequest struct {
	Question    string `json:"question"`
	ProjectPath string `json:"project_path"`
	Budget      int    `json:"budget"`
}

func (s *Server) handleBrainQuery(w http.ResponseWriter, r *http.Request) {
	var req graphQueryRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Question) == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "question is required")
		return
	}
	res, err := s.deps.GraphRead.Query(r.Context(), req.ProjectPath, req.Question, req.Budget)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	// Never a nil slice: the screen renders "no results" differently from a
	// field that is absent, and JSON null would be the second.
	if res.Hits == nil {
		res.Hits = []brain.Hit{}
	}
	if res.Expanded == nil {
		res.Expanded = []string{}
	}
	writeJSON(w, http.StatusOK, res)
}

type graphAffectedRequest struct {
	NodeID string   `json:"node_id"`
	Depth  int      `json:"depth"`
	Kinds  []string `json:"kinds"`
}

func (s *Server) handleBrainAffected(w http.ResponseWriter, r *http.Request) {
	var req graphAffectedRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.NodeID) == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "node_id is required")
		return
	}
	hits, err := s.deps.GraphRead.Affected(r.Context(), req.NodeID, req.Kinds, req.Depth)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hits": nonNilHits(hits)})
}

func (s *Server) handleBrainHubs(w http.ResponseWriter, r *http.Request) {
	top := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("top")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, codeBadRequest, "top must be a positive integer")
			return
		}
		top = n
	}
	hits, err := s.deps.GraphRead.Hubs(r.Context(), r.URL.Query().Get("project_path"), top)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hits": nonNilHits(hits)})
}

func nonNilHits(hits []brain.Hit) []brain.Hit {
	if hits == nil {
		return []brain.Hit{}
	}
	return hits
}
