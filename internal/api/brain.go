package api

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/logrenant/mimir/internal/brain"
	"github.com/logrenant/mimir/internal/llm"
	"github.com/logrenant/mimir/internal/store"
)

// BrainScanner is the daemon's resident scan, as this package needs it.
// *brain.Supervisor satisfies it.
type BrainScanner interface {
	Status() brain.ScanStatus
	Events(after int64, limit int) ([]brain.ScanEvent, int64)
	Pause()
	Resume()
	ScanNow(sel llm.Selection) bool
}

// BrainReader is the node core behind node detail. *brain.Core satisfies it.
type BrainReader interface {
	Related(ctx context.Context, nodeID string, limit int) (brain.NodeView, error)
}

// BrainGraphStore is the three reads a force-directed view needs, and nothing
// else. *store.Store satisfies it.
type BrainGraphStore interface {
	BrainGraphIDs(ctx context.Context, projectPath string, limit int) ([]store.BrainNodeDegree, error)
	BrainNodesByIDs(ctx context.Context, ids []string) ([]store.BrainNodeRow, error)
	BrainEdgesAmong(ctx context.Context, ids []string, limit int) ([]store.BrainEdgeRow, error)
	BrainProjects(ctx context.Context) ([]store.BrainProjectCount, error)
	BrainNodeVersions(ctx context.Context, nodeID string, limit int) ([]store.BrainNodeVersion, error)
}

// --- scan control ------------------------------------------------------------

type scanStatusResponse struct {
	Scan brain.ScanStatus `json:"scan"`
}

func (s *Server) handleBrainScanStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, scanStatusResponse{Scan: s.deps.BrainScan.Status()})
}

// Pause and Resume take no body. That is not an omission: there is nothing to
// configure about stopping (SD-1), and decoding a body there would 400 every
// click — decodeJSON's DisallowUnknownFields turns an empty body into an EOF.
// Scan-now is the exception, and an optional one: see below.
func (s *Server) handleBrainScanPause(w http.ResponseWriter, _ *http.Request) {
	s.deps.BrainScan.Pause()
	writeJSON(w, http.StatusOK, scanStatusResponse{Scan: s.deps.BrainScan.Status()})
}

func (s *Server) handleBrainScanResume(w http.ResponseWriter, _ *http.Request) {
	s.deps.BrainScan.Resume()
	writeJSON(w, http.StatusOK, scanStatusResponse{Scan: s.deps.BrainScan.Status()})
}

// scanNowRequest routes one hand-started sweep.
//
// Both fields are optional and an absent body is the whole of the old contract:
// a client written before this — or a click that never opened the picker —
// sends nothing and gets the configured distil routing, exactly as before.
type scanNowRequest struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

// handleBrainScanNow wakes the resident loop for one sweep.
//
// The selection it carries is for that sweep only. A first mount is where an
// operator reaches for a provider whose free pool will actually cover the
// repository, and that is a decision about this scan — writing it into the
// daemon's routing would quietly change every sweep that follows, including the
// ones nobody is watching.
func (s *Server) handleBrainScanNow(w http.ResponseWriter, r *http.Request) {
	var req scanNowRequest
	if !decodeOptionalJSON(w, r, &req) {
		return
	}
	sel, ok := s.llmSelection(w, req.Provider, req.Model)
	if !ok {
		return
	}

	if !s.deps.BrainScan.ScanNow(sel) {
		// The operator stopped this on purpose and the button they pressed was
		// drawn before that. 409 says so; starting anyway would be a button
		// undoing a decision quietly.
		writeError(w, http.StatusConflict, codeConflict, "the scan is paused; resume it first")
		return
	}
	writeJSON(w, http.StatusAccepted, scanStatusResponse{Scan: s.deps.BrainScan.Status()})
}

type scanLogResponse struct {
	Events []brain.ScanEvent `json:"events"`
	Seq    int64             `json:"seq"`
}

// handleBrainScanLog is the scan's console.
//
// `after` is the sequence the caller last saw, so a tab that has been open for
// an hour asks for the handful of lines it is missing rather than the whole
// buffer. A caller that has fallen further behind than the buffer is deep gets
// the tail: a console that missed a hundred lines wants the recent ones, not an
// error about the ninety-nine it cannot have.
func (s *Server) handleBrainScanLog(w http.ResponseWriter, r *http.Request) {
	after, err := parseSeq(r.URL.Query().Get("after"))
	if err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, err.Error())
		return
	}
	limit, err := graphLimit(r.URL.Query().Get("limit"), 200, 600)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, err.Error())
		return
	}

	events, seq := s.deps.BrainScan.Events(after, limit)
	writeJSON(w, http.StatusOK, scanLogResponse{Events: append([]brain.ScanEvent{}, events...), Seq: seq})
}

func parseSeq(raw string) (int64, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, errBadSeq
	}
	return n, nil
}

// --- the graph ---------------------------------------------------------------

type graphNode struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Title     string   `json:"title"`
	Project   string   `json:"project,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Degree    int      `json:"degree"`
	UpdatedAt int64    `json:"updated_at"`
}

// source/target rather than src/dst: that is what a force layout reads, and the
// store's own names stay in the store.
type graphEdge struct {
	Source string  `json:"source"`
	Target string  `json:"target"`
	Kind   string  `json:"kind"`
	Weight float64 `json:"weight"`
}

type graphResponse struct {
	Nodes      []graphNode `json:"nodes"`
	Edges      []graphEdge `json:"edges"`
	Project    string      `json:"project,omitempty"`
	TotalNodes int         `json:"total_nodes"`
	Truncated  bool        `json:"truncated"`
}

type brainProject struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Path    string `json:"path"`
	Nodes   int    `json:"nodes"`
	Files   int    `json:"files"`
	Updated int64  `json:"updated_at"`
}

type brainProjectsResponse struct {
	Projects []brainProject `json:"projects"`
}

type brainNodeResponse struct {
	Node brain.NodeView `json:"node"`
	// Versions is the node's history, newest first — bounded here so node
	// detail stays one response. The full list is its own route.
	Versions []brainVersionView `json:"versions,omitempty"`
}

// brainVersionView is one entry in a node's history. It carries no file
// content: the source is still on disk, and what this row keeps is the reading
// of it, which is the part nothing else has.
type brainVersionView struct {
	Hash       string   `json:"content_hash"`
	SeenAt     int64    `json:"seen_at"`
	SizeBytes  int64    `json:"size_bytes,omitempty"`
	ModifiedAt int64    `json:"modified_at,omitempty"`
	Title      string   `json:"title,omitempty"`
	Assessment string   `json:"assessment,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Model      string   `json:"model,omitempty"`
}

func brainVersionViews(rows []store.BrainNodeVersion) []brainVersionView {
	out := make([]brainVersionView, 0, len(rows))
	for _, v := range rows {
		view := brainVersionView{
			Hash:       v.ContentHash,
			SeenAt:     v.SeenAt.Unix(),
			SizeBytes:  v.SizeBytes,
			Title:      v.Title,
			Assessment: v.Assessment,
			Tags:       v.Tags,
			Model:      v.Model,
		}
		if !v.ModifiedAt.IsZero() {
			view.ModifiedAt = v.ModifiedAt.Unix()
		}
		out = append(out, view)
	}
	return out
}

// handleBrainNodeVersions serves a node's whole history.
func (s *Server) handleBrainNodeVersions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "node id is required")
		return
	}
	limit, err := graphLimit(r.URL.Query().Get("limit"), s.cfg.BrainVersionsPerNode, s.cfg.BrainVersionsPerNode)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, "limit must be a positive integer")
		return
	}

	rows, err := s.deps.BrainGraph.BrainNodeVersions(r.Context(), id, limit)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": brainVersionViews(rows)})
}

// handleBrainProjects lists what Brain knows, by project.
//
// The id is opaque and the path is returned but never accepted — see
// resolveBrainProject.
func (s *Server) handleBrainProjects(w http.ResponseWriter, r *http.Request) {
	rows, err := s.deps.BrainGraph.BrainProjects(r.Context())
	if err != nil {
		writeDomainError(w, r, err)
		return
	}

	out := brainProjectsResponse{Projects: make([]brainProject, 0, len(rows))}
	for _, row := range rows {
		out.Projects = append(out.Projects, brainProject{
			ID:      brain.ProjectID(row.ProjectPath),
			Label:   projectLabel(row.ProjectPath),
			Path:    row.ProjectPath,
			Nodes:   row.Nodes,
			Files:   row.Files,
			Updated: row.UpdatedAt.Unix(),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleBrainGraph returns the picture: the most connected nodes and the edges
// among them.
func (s *Server) handleBrainGraph(w http.ResponseWriter, r *http.Request) {
	limit, err := graphLimit(r.URL.Query().Get("limit"), s.cfg.BrainGraphDefaultNodes, s.cfg.BrainGraphMaxNodes)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, err.Error())
		return
	}

	project, err := s.resolveBrainProject(r.Context(), r.URL.Query().Get("project"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}

	ranked, err := s.deps.BrainGraph.BrainGraphIDs(r.Context(), project, limit)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}

	ids := make([]string, 0, len(ranked))
	degree := make(map[string]int, len(ranked))
	for _, d := range ranked {
		ids = append(ids, d.ID)
		degree[d.ID] = d.Degree
	}

	rows, err := s.deps.BrainGraph.BrainNodesByIDs(r.Context(), ids)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}

	out := graphResponse{
		Nodes:      make([]graphNode, 0, len(rows)),
		Edges:      []graphEdge{},
		Project:    project,
		TotalNodes: len(ranked),
		Truncated:  len(ranked) == limit,
	}

	present := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		present[row.ID] = struct{}{}
		out.Nodes = append(out.Nodes, graphNode{
			ID:        row.ID,
			Kind:      row.Kind,
			Title:     row.Title,
			Project:   row.ProjectPath,
			Tags:      row.Tags,
			Degree:    degree[row.ID],
			UpdatedAt: row.UpdatedAt.Unix(),
		})
	}

	edges, err := s.deps.BrainGraph.BrainEdgesAmong(r.Context(), ids, s.cfg.BrainGraphMaxNodes*4)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	for _, e := range edges {
		// The store guarantees this; the check is here anyway because the thing
		// that breaks on a dangling endpoint is a canvas in the desktop app,
		// where the failure is a blank screen with no message.
		if _, ok := present[e.Src]; !ok {
			continue
		}
		if _, ok := present[e.Dst]; !ok {
			continue
		}
		out.Edges = append(out.Edges, graphEdge{Source: e.Src, Target: e.Dst, Kind: e.Kind, Weight: e.Weight})
	}

	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleBrainNode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "node id is required")
		return
	}
	node, err := s.deps.Brain.Related(r.Context(), id, 0)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	resp := brainNodeResponse{Node: node}
	// History rides node detail rather than needing a second call, because a
	// node with one version is the common case and the panel would otherwise
	// fetch an empty list for every file on the machine. The graph store is
	// optional here on purpose: node detail works without it.
	if s.deps.BrainGraph != nil {
		if rows, err := s.deps.BrainGraph.BrainNodeVersions(r.Context(), id, s.cfg.BrainNodeVersionsInline); err == nil && len(rows) > 1 {
			resp.Versions = brainVersionViews(rows)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// resolveBrainProject turns the opaque id the client was given back into a
// path, and refuses a path outright.
//
// This is the rule from internal/api/AGENTS.md held in one function: a
// filesystem path is accepted at exactly two routes, and this is not one of
// them. Returning a path is fine — GET /projects already does — accepting one
// is what opens the hole.
func (s *Server) resolveBrainProject(ctx context.Context, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", nil
	}
	if strings.ContainsAny(id, "/\\") || strings.HasPrefix(id, "~") {
		return "", errProjectIsAnID
	}

	rows, err := s.deps.BrainGraph.BrainProjects(ctx)
	if err != nil {
		return "", err
	}
	for _, row := range rows {
		if brain.ProjectID(row.ProjectPath) == id {
			return row.ProjectPath, nil
		}
	}
	return "", errUnknownProject
}

// projectLabel is what the tab prints. The path is in the payload for anyone
// who wants it; a sidebar full of absolute paths is unreadable.
func projectLabel(path string) string {
	if path == "" {
		return "global"
	}
	return filepath.Base(path)
}

func graphLimit(raw string, fallback, max int) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, errBadLimit
	}
	if n > max {
		n = max
	}
	return n, nil
}
