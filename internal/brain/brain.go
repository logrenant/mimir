// Package brain is the node core: the part of the memory that is not tied to
// one project's transcripts.
//
// The project memory (internal/memory) answers "what happened in this
// checkout". Brain answers "what do we know about this thing" — a repository, a
// piece of research, a decision, a file — and how it connects to everything
// else we know. Both live in the same store, and Brain is deliberately the
// derived half: nothing here is a second source of truth for what memory
// already records.
//
// # Why rows, and why no node ever rewrites another
//
// The first version of this layer kept one Markdown file per node and had the
// clustering engine rewrite its neighbours in place on every ingest. That is
// the shape docs/ROADMAP.md §B.9 exists to forbid: goat v1 kept its memory as a
// document a model rewrote, one rewrite degenerated, and because the document
// *was* the memory the damage was permanent and is still on disk.
//
// So: a node is a row. An ingest writes exactly one node and a set of edges,
// never another node's content. A distil that fails or is rejected costs that
// node's assessment and nothing else — the node is still stored, still
// findable, still linkable. Degrade, never corrupt (SD-6).
//
// # Why there is no vector index
//
// Semantic neighbours come from one relation pass over the candidates FTS5
// already found, and semantic *recall* comes from alias terms the distil writes
// into the index at ingest time. Both ride the provider that is already there.
// ROADMAP §A.4 keeps local embeddings parked, and this layer did not need to
// unpark them.
package brain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/llm"
	"github.com/logrenant/mimir/internal/store"
)

// ErrNoStore means Brain was asked to work without somewhere to remember.
// Callers treat it the way the memory tools treat a missing store: the feature
// is absent, not broken.
var ErrNoStore = errors.New("brain: no store")

// Store is the slice of *store.Store this package uses. It is an interface so
// the core can be tested against a fake, the same way internal/memory does it.
//
// Implementations must be safe for concurrent use: Scan runs Ingest on several
// files at once, and every one of those calls reads and writes through here.
// *store.Store is, because database/sql is; a fake that is not will be found by
// `make race` rather than in production, which is the point of saying so.
type Store interface {
	UpsertBrainNode(ctx context.Context, n store.BrainNodeRow) error
	BrainNode(ctx context.Context, id string) (store.BrainNodeRow, bool, error)
	BrainNodesByIDs(ctx context.Context, ids []string) ([]store.BrainNodeRow, error)
	SearchBrainNodes(ctx context.Context, projectPath, query string, limit int) ([]store.BrainNodeRow, error)
	UpsertBrainEdges(ctx context.Context, edges []store.BrainEdgeRow) error
	BrainNeighbors(ctx context.Context, id string, limit int) ([]store.BrainEdgeRow, error)
}

// Completer is the model call this package makes. Satisfied by *llm.Router.
//
// Like Store, implementations must be safe for concurrent use: Scan distils
// several files at once. *llm.Router is — its routing tables are read-only
// after construction and each call spawns its own subprocess.
type Completer interface {
	Complete(ctx context.Context, c llm.Class, req llm.Request) (llm.Response, error)
}

// Core is the handle both binaries build once and share.
type Core struct {
	cfg   config.Config
	store Store
	llm   Completer

	// The PDF extractor is resolved once per process, not once per file: a
	// machine without poppler would otherwise pay a failed PATH lookup for
	// every PDF in every sweep, and say so every time.
	pdfOnce sync.Once
	pdfPath string
	pdfErr  error
}

// New builds the core. A nil store is a usable argument on purpose — the caller
// gets a Core whose methods return ErrNoStore, so the decision about whether to
// register the tools stays in one place (tools.RegisterAll).
func New(cfg config.Config, st Store, router Completer) *Core {
	return &Core{cfg: cfg, store: st, llm: router}
}

// Available reports whether this core can do anything at all.
func (c *Core) Available() bool { return c != nil && c.store != nil }

// Kind values. These are the node types the system produces; the set is closed
// so a typo cannot mint a category nothing will ever query.
const (
	KindNote     = "note"
	KindResearch = "research"
	KindSession  = "session"
	KindRepo     = "repo"
	KindFile     = "file"
	KindCommit   = "commit"
	KindDecision = "decision"
)

var validKinds = map[string]struct{}{
	KindNote: {}, KindResearch: {}, KindSession: {}, KindRepo: {},
	KindFile: {}, KindCommit: {}, KindDecision: {},
}

// NodeView is what leaves this package. It is not store.BrainNodeRow: the body
// stays behind, because a consumer that wanted the body would be better served
// reading the source the node names.
type NodeView struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	Source     string         `json:"source"`
	Title      string         `json:"title,omitempty"`
	Assessment string         `json:"assessment,omitempty"`
	Tags       []string       `json:"tags,omitempty"`
	Project    string         `json:"project,omitempty"`
	UpdatedAt  time.Time      `json:"updated_at"`
	Neighbors  []NeighborView `json:"neighbors,omitempty"`
}

// NeighborView is one end of an edge, already resolved to something readable.
type NeighborView struct {
	ID     string  `json:"id"`
	Title  string  `json:"title,omitempty"`
	Kind   string  `json:"kind"`
	Via    string  `json:"via"`
	Weight float64 `json:"weight"`
}

// IngestResult is what an ingest reports back. Distilled is false when the
// model call failed or its output was rejected — a normal outcome that costs
// the assessment and keeps the node.
type IngestResult struct {
	Node      NodeView `json:"node"`
	Distilled bool     `json:"distilled"`
	Note      string   `json:"note,omitempty"`
	Linked    int      `json:"linked"`
}

// Input is one thing to remember.
type Input struct {
	Source      string
	Kind        string
	Content     string
	ProjectPath string

	// ContentHash is the digest of the source, when it has stable content. It
	// is stored so a later pass can tell "unchanged" from "not seen yet"
	// without re-reading the body — the whole reason a repository scan can be
	// run twice without paying twice. Empty is normal.
	ContentHash string
}

// NodeID is the node's identity: the project, the kind and the source key.
//
// It is deliberately content-free. The previous implementation salted the hash
// with UnixNano, which meant ingesting one repository twice produced two
// unrelated nodes that then linked to each other and to everything sharing a
// tag. Re-ingesting a source must update one row.
func NodeID(projectPath, kind, sourceKey string) string {
	sum := sha256.Sum256([]byte(projectPath + "|" + kind + "|" + sourceKey))
	return "node-" + hex.EncodeToString(sum[:])[:16]
}

// ProjectID is a project path's opaque handle.
//
// It exists so the graph can be filtered by project without a filesystem path
// ever being a request parameter: internal/api/AGENTS.md allows exactly two
// routes to accept a path, both of which mint an opaque id on the spot, and a
// third would be the beginning of the hole that rule exists to keep shut.
// Constructed like NodeID, for the same reason — identity that carries no
// content and does not change.
func ProjectID(projectPath string) string {
	sum := sha256.Sum256([]byte("project|" + projectPath))
	return "proj-" + hex.EncodeToString(sum[:])[:16]
}

// ErrNodeNotFound is an id nothing answers to. It is a sentinel rather than a
// formatted string because the API turns it into a 404: without it, a mistyped
// id is a 500 and reads like a broken daemon.
var ErrNodeNotFound = errors.New("brain: no such node")

// Ingest stores one node and links it.
//
// The order is load-bearing. The node is written before it is linked, so a
// failure in the relation pass leaves a stored, searchable node with fewer
// edges rather than losing the ingest — the same invariant the first version
// stated in its commit message and then broke by returning early when the
// summarizer failed.
func (c *Core) Ingest(ctx context.Context, in Input) (IngestResult, error) {
	if !c.Available() {
		return IngestResult{}, ErrNoStore
	}

	source := strings.TrimSpace(in.Source)
	if source == "" {
		return IngestResult{}, errors.New("brain: source is required")
	}
	body := strings.TrimSpace(in.Content)
	if body == "" {
		return IngestResult{}, errors.New("brain: content is required")
	}

	kind := strings.TrimSpace(in.Kind)
	if kind == "" {
		kind = KindNote
	}
	if _, ok := validKinds[kind]; !ok {
		return IngestResult{}, fmt.Errorf("brain: unknown kind %q", kind)
	}

	body = sanitizeBody(body, c.cfg.BrainBodyMaxChars)

	row := store.BrainNodeRow{
		ID:            NodeID(in.ProjectPath, kind, source),
		ProjectPath:   in.ProjectPath,
		Kind:          kind,
		SourceKey:     source,
		Body:          body,
		ContentHash:   in.ContentHash,
		PromptVersion: c.cfg.BrainPromptVersion,
	}

	// The timestamps are set here rather than left to the store, because the
	// view returned to the caller is built from this row and would otherwise
	// report a zero time for a node that was just written.
	//
	// A node keeps the timestamp it was first seen at, so re-scanning a source
	// does not make it look like new work.
	row.UpdatedAt = time.Now().UTC()
	row.CreatedAt = row.UpdatedAt
	if existing, ok, err := c.store.BrainNode(ctx, row.ID); err == nil && ok && !existing.CreatedAt.IsZero() {
		row.CreatedAt = existing.CreatedAt
	}

	result := IngestResult{}
	distilled, err := c.distil(ctx, source, kind, body)
	if err != nil {
		// The node still gets a title, because a node nothing can name is a
		// node nothing will ever pick out of a result list.
		row.Title = fallbackTitle(source, body)
		result.Note = distilFailureNote(err)
		// The content hash means "this content has been read at this prompt
		// version", and a node with no assessment has not been. Keeping it
		// would make a scan skip the file forever on the strength of one
		// provider hiccup — the failure would be permanent, and silent.
		row.ContentHash = ""
	} else {
		row.Title = distilled.Title
		row.Assessment = distilled.Assessment
		row.Tags = distilled.Tags
		row.Aliases = distilled.Aliases
		row.Provider = distilled.Provider
		row.Model = distilled.Model
		result.Distilled = true
	}

	if err := c.store.UpsertBrainNode(ctx, row); err != nil {
		return IngestResult{}, fmt.Errorf("storing the node: %w", err)
	}

	linked, relateNote := c.relate(ctx, row)
	result.Linked = linked
	// When the distil already failed there is nothing new to say: both halves
	// went down for the same reason, and repeating it makes the note read like
	// two separate problems.
	if relateNote != "" && result.Distilled {
		result.Note = strings.TrimSpace(result.Note + " " + relateNote)
	}

	view := toView(row)
	view.Neighbors = c.neighbors(ctx, row.ID)
	result.Node = view
	return result, nil
}

// Search answers "what do we know about X", best match first.
func (c *Core) Search(ctx context.Context, projectPath, query string, limit int) ([]NodeView, error) {
	if !c.Available() {
		return nil, ErrNoStore
	}
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("brain: query is required")
	}
	if limit <= 0 || limit > 20 {
		limit = c.cfg.BrainSearchLimit
	}

	rows, err := c.store.SearchBrainNodes(ctx, projectPath, query, limit)
	if err != nil {
		return nil, err
	}

	out := make([]NodeView, 0, len(rows))
	for _, r := range rows {
		v := toView(r)
		v.Neighbors = c.neighbors(ctx, r.ID)
		out = append(out, v)
	}
	return fitToBudget(out, c.cfg.BrainSearchMaxTokens), nil
}

// fitToBudget shrinks a result list until it fits.
//
// This is not politeness, it is the difference between an answer and an error.
// internal/mcp's choke-point *rejects* an over-budget response rather than
// truncating it, so a result set that grew past its ceiling would not arrive
// shortened — it would not arrive at all, and the tool would look broken on
// exactly the queries that matched the most. Eight nodes with real assessments
// and their neighbours clear the ceiling comfortably, so without this the
// default limit alone is enough to break it.
//
// Least valuable material goes first: neighbours are an enrichment of a node
// that was already found, then the worst-ranked nodes, then assessments. The
// best match keeps its assessment to the end, because a result list that is all
// titles has stopped answering the question.
func fitToBudget(nodes []NodeView, budget int) []NodeView {
	if budget <= 0 || len(nodes) == 0 {
		return nodes
	}
	// A margin, because the tool wraps this in a couple of fields of its own.
	target := budget - budget/10

	for estimateTokens(nodes) > target {
		switch {
		case dropLastNeighbors(nodes):
		case len(nodes) > 1:
			nodes = nodes[:len(nodes)-1]
		case len(nodes[0].Assessment) > 0:
			nodes[0].Assessment = ""
		default:
			// One node with a title and its tags is the floor; below that the
			// response has no content to defend.
			return nodes
		}
	}
	return nodes
}

// dropLastNeighbors removes the neighbour list from the worst-ranked node that
// still has one, and reports whether it found any.
func dropLastNeighbors(nodes []NodeView) bool {
	for i := len(nodes) - 1; i >= 0; i-- {
		if len(nodes[i].Neighbors) > 0 {
			nodes[i].Neighbors = nil
			return true
		}
	}
	return false
}

// estimateTokens must keep matching internal/mcp/finalize.go's len(json)/4.
func estimateTokens(v any) int {
	b, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return len(b) / 4
}

// Related walks one node's edges.
func (c *Core) Related(ctx context.Context, nodeID string, limit int) (NodeView, error) {
	if !c.Available() {
		return NodeView{}, ErrNoStore
	}
	row, ok, err := c.store.BrainNode(ctx, nodeID)
	if err != nil {
		return NodeView{}, err
	}
	if !ok {
		return NodeView{}, fmt.Errorf("%w: %s", ErrNodeNotFound, nodeID)
	}
	if limit <= 0 || limit > 40 {
		limit = c.cfg.BrainNeighborCap
	}

	v := toView(row)
	v.Neighbors = c.neighborsN(ctx, nodeID, limit)

	// Forty neighbours plus an assessment sits close enough to the ceiling that
	// it is not worth reasoning about per call.
	fitted := fitToBudget([]NodeView{v}, c.cfg.BrainSearchMaxTokens)
	return fitted[0], nil
}

func (c *Core) neighbors(ctx context.Context, id string) []NeighborView {
	return c.neighborsN(ctx, id, c.cfg.BrainNeighborCap)
}

// neighborsN resolves an edge list into something readable.
//
// A failure here returns nothing rather than an error: neighbours are an
// enrichment of a node that was already found, and losing them must not lose
// the answer.
func (c *Core) neighborsN(ctx context.Context, id string, limit int) []NeighborView {
	edges, err := c.store.BrainNeighbors(ctx, id, limit)
	if err != nil || len(edges) == 0 {
		return nil
	}

	ids := make([]string, 0, len(edges))
	for _, e := range edges {
		ids = append(ids, e.Dst)
	}
	rows, err := c.store.BrainNodesByIDs(ctx, ids)
	if err != nil {
		return nil
	}
	byID := make(map[string]store.BrainNodeRow, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}

	out := make([]NeighborView, 0, len(edges))
	for _, e := range edges {
		r, ok := byID[e.Dst]
		if !ok {
			continue
		}
		out = append(out, NeighborView{
			ID: r.ID, Title: r.Title, Kind: r.Kind, Via: e.Kind, Weight: e.Weight,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Weight > out[j].Weight })
	return out
}

func toView(r store.BrainNodeRow) NodeView {
	return NodeView{
		ID:         r.ID,
		Kind:       r.Kind,
		Source:     r.SourceKey,
		Title:      r.Title,
		Assessment: r.Assessment,
		Tags:       r.Tags,
		Project:    r.ProjectPath,
		UpdatedAt:  r.UpdatedAt,
	}
}

// fallbackTitle names a node the distiller could not name. It is the source
// plus the first line of the body, which is almost always a heading.
func fallbackTitle(source, body string) string {
	first := body
	if i := strings.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	first = strings.TrimSpace(strings.TrimLeft(first, "#> -*"))
	title := source
	if first != "" && !strings.EqualFold(first, source) {
		title = source + " — " + first
	}
	return truncateRunes(title, 120)
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max]))
}

func distilFailureNote(err error) string {
	if errors.Is(err, llm.ErrProviderUnavailable) {
		return "stored without an assessment: no model was reachable."
	}
	return "stored without an assessment: " + err.Error()
}
