package api

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/logrenant/mimir/internal/brain"
	"github.com/logrenant/mimir/internal/store"
)

// BrainScanner is the daemon's resident scan, as this package needs it.
// *brain.Supervisor satisfies it.
type BrainScanner interface {
	Status() brain.ScanStatus
	Pause()
	Resume()
	ScanNow() bool
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
}

// --- scan control ------------------------------------------------------------

type scanStatusResponse struct {
	Scan brain.ScanStatus `json:"scan"`
}

func (s *Server) handleBrainScanStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, scanStatusResponse{Scan: s.deps.BrainScan.Status()})
}

// The three controls take no body. That is not an omission: there is nothing to
// configure about a scan (SD-1), and decoding a body here would 400 every click
// — decodeJSON's DisallowUnknownFields turns an empty body into an EOF.
func (s *Server) handleBrainScanPause(w http.ResponseWriter, _ *http.Request) {
	s.deps.BrainScan.Pause()
	writeJSON(w, http.StatusOK, scanStatusResponse{Scan: s.deps.BrainScan.Status()})
}

func (s *Server) handleBrainScanResume(w http.ResponseWriter, _ *http.Request) {
	s.deps.BrainScan.Resume()
	writeJSON(w, http.StatusOK, scanStatusResponse{Scan: s.deps.BrainScan.Status()})
}

func (s *Server) handleBrainScanNow(w http.ResponseWriter, _ *http.Request) {
	if !s.deps.BrainScan.ScanNow() {
		// The operator stopped this on purpose and the button they pressed was
		// drawn before that. 409 says so; starting anyway would be a button
		// undoing a decision quietly.
		writeError(w, http.StatusConflict, codeConflict, "the scan is paused; resume it first")
		return
	}
	writeJSON(w, http.StatusAccepted, scanStatusResponse{Scan: s.deps.BrainScan.Status()})
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
	writeJSON(w, http.StatusOK, brainNodeResponse{Node: node})
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
