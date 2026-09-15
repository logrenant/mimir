package brain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/logrenant/mimir/internal/graphify"
	"github.com/logrenant/mimir/internal/store"
)

// The structural layer: what the code *is*, as opposed to what it is about.
//
// Every other node in this package is the result of a model reading something.
// These are not. They come from Graphify's tree-sitter pass (internal/graphify)
// and they are written here without a single model call — which is the whole
// point of them. An import edge is a fact; asking a model whether two files are
// related, which is what relate.go does, is the best answer available when
// there is no parser, and a worse one when there is.
//
// The two layers are one graph rather than two. A symbol is joined to the
// `file` node the resident scan already made for the file it lives in, so
// "what is this file about" (a model's sentence) and "what does it contain"
// (the parser's facts) hang off the same node and a reader moves between them
// without knowing there were two passes.

// A symbol's source key is `path/to/file.go::Name`, not Graphify's own node id:
// that id is a slug meaningful only inside one extraction, and keying on it
// would mint a new node every time the tool renumbered. The kind itself is
// declared with the others in brain.go, because the set is closed.

// StructuralResult is what one project's structural pass wrote.
type StructuralResult struct {
	Symbols int
	Edges   int
	// Dropped is how many symbols were past the cap. Reported rather than
	// hidden: a project that loses half its symbols to a ceiling should say so
	// on the screen that shows the ceiling.
	Dropped int
}

// IngestStructural writes one project's symbols and their relations.
//
// `version` identifies what produced them — the Graphify version — and lands in
// prompt_version, so a later change to this mapping or to the tool can be told
// apart from a file that changed. Nothing here calls a model, so provider and
// model stay empty, and that emptiness is readable: a node with no model behind
// it is a node nothing could have imagined.
func (c *Core) IngestStructural(
	ctx context.Context,
	projectPath string,
	ex graphify.Extraction,
	version string,
) (StructuralResult, error) {
	if !c.Available() {
		return StructuralResult{}, ErrNoStore
	}

	// Graphify's ids resolve to ours here, once, so the edge pass below is a
	// lookup rather than a second decision about what a node is.
	ids := make(map[string]string, len(ex.Nodes))
	symbols := make([]graphify.Node, 0, len(ex.Nodes))

	for _, n := range ex.Nodes {
		if n.SourceFile == "" || strings.TrimSpace(n.Label) == "" {
			continue
		}
		if n.IsFile() {
			// The file node belongs to the resident scan, which gives it a
			// title and an assessment. Claiming it here would overwrite a
			// model's sentence with a filename.
			ids[n.ID] = NodeID(projectPath, KindFile, n.SourceFile)
			continue
		}
		if n.FileType != "code" {
			// Documents and images are the semantic scan's territory; a
			// concept node from Graphify would be a second, unreconciled
			// opinion about a file this package already reads.
			continue
		}
		ids[n.ID] = NodeID(projectPath, KindSymbol, symbolKey(n))
		symbols = append(symbols, n)
	}

	// Most connected first, so a cap cuts the leaves rather than the hubs.
	degree := symbolDegree(ex)
	sort.SliceStable(symbols, func(i, j int) bool {
		return degree[symbols[i].ID] > degree[symbols[j].ID]
	})

	result := StructuralResult{}
	limit := c.cfg.BrainSymbolsPerProject
	if limit > 0 && len(symbols) > limit {
		for _, dropped := range symbols[limit:] {
			delete(ids, dropped.ID)
		}
		result.Dropped = len(symbols) - limit
		symbols = symbols[:limit]
	}

	for _, n := range symbols {
		row := store.BrainNodeRow{
			ID:          ids[n.ID],
			ProjectPath: projectPath,
			Kind:        KindSymbol,
			SourceKey:   symbolKey(n),
			Title:       truncateRunes(strings.TrimSpace(n.Label), 120),
			// Where it is, and nothing else. This is the one node kind whose
			// assessment is not a reading: writing a sentence here would be
			// inventing one, and an empty assessment is a documented state
			// (0013_brain.sql) rather than a gap.
			Assessment: located(n),
			Tags:       symbolTags(n),
			// Not a prompt, but the same job: the string that says a re-read
			// is due when what produced this changes.
			PromptVersion: "graphify" + versionSuffix(version),
			ContentHash:   symbolHash(n),
		}
		if err := c.store.UpsertBrainNode(ctx, row); err != nil {
			return result, err
		}
		result.Symbols++
	}

	edges := make([]store.BrainEdgeRow, 0, len(ex.Edges))
	for _, e := range ex.Edges {
		src, okSrc := ids[e.Source]
		dst, okDst := ids[e.Target]
		if !okSrc || !okDst || src == dst {
			continue
		}
		edges = append(edges, store.BrainEdgeRow{
			Src:    src,
			Dst:    dst,
			Kind:   relationKind(e.Relation),
			Weight: edgeWeight(e),
		})
	}
	if err := c.store.UpsertBrainEdges(ctx, edges); err != nil {
		return result, err
	}
	result.Edges = len(edges)

	return result, nil
}

// symbolKey is a symbol's identity across runs: where it lives and what it is
// called. Not the line number — a symbol that moved down a file is the same
// symbol, and keying on the line would mint a new node on every edit above it.
func symbolKey(n graphify.Node) string {
	return n.SourceFile + "::" + strings.TrimSpace(n.Label)
}

// located is the assessment: the file and the line, as Graphify reported them.
func located(n graphify.Node) string {
	if n.SourceLocation == "" {
		return n.SourceFile
	}
	return n.SourceFile + " " + n.SourceLocation
}

// symbolTags gives the index something to match on beyond the title. The
// language and the file's own name are what a person actually types when they
// are looking for a function they half remember.
func symbolTags(n graphify.Node) []string {
	tags := []string{"symbol"}
	if ext := extensionOf(n.SourceFile); ext != "" {
		tags = append(tags, ext)
	}
	if base := baseName(n.SourceFile); base != "" {
		tags = append(tags, base)
	}
	return tags
}

func extensionOf(path string) string {
	i := strings.LastIndexByte(path, '.')
	if i < 0 || i == len(path)-1 {
		return ""
	}
	return strings.ToLower(path[i+1:])
}

func baseName(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

// symbolHash changes when the symbol's own facts change, and not otherwise, so
// re-running the pass over an unchanged project appends no history.
func symbolHash(n graphify.Node) string {
	sum := sha256.Sum256([]byte(n.SourceFile + "|" + n.Label + "|" + n.SourceLocation))
	return hex.EncodeToString(sum[:])
}

func versionSuffix(version string) string {
	if strings.TrimSpace(version) == "" {
		return ""
	}
	return "@" + strings.TrimSpace(version)
}

// symbolDegree counts a symbol's relations, for the cap's ordering.
func symbolDegree(ex graphify.Extraction) map[string]int {
	out := make(map[string]int, len(ex.Nodes))
	for _, e := range ex.Edges {
		out[e.Source]++
		out[e.Target]++
	}
	return out
}

// relationKind keeps Graphify's word wherever it is one a reader would
// recognise, because the desktop shows the edge kind as "via" under a
// neighbour: "calls" is worth more there than "structural".
//
// The closed set is the point. An unknown relation becomes "structural" rather
// than minting a category from a tool that ships most days.
func relationKind(relation string) string {
	switch strings.ToLower(strings.TrimSpace(relation)) {
	// An indirect call is still a call, and collapsing it loses nothing a
	// reader of the graph would act on differently.
	case "calls", "indirect_call":
		return "calls"
	case "imports", "imports_from", "dynamic_import":
		return "imports"
	// `contains` is a file holding a symbol, `method` is a type holding one:
	// from the graph's side both are "this is where that lives".
	case "contains", "method":
		return "defines"
	// Go has no inheritance and embedding is what stands in for it; a reader
	// looking at either wants the same thing, which is what this type is built
	// out of.
	case "inherits", "embeds":
		return "inherits"
	case "implements":
		return "implements"
	// The largest category in a real Go corpus after `calls`: a type named in
	// a signature, a constant read. Weaker than a call and worth saying so
	// rather than burying under a generic word.
	case "references":
		return "references"
	case "uses":
		return "uses"
	default:
		return "structural"
	}
}

// edgeWeight trusts Graphify's own number when it has one, and falls back to
// its confidence when it does not.
//
// The confidences mean different things and must not weigh the same: EXTRACTED
// is the parser saying it saw the call, INFERRED is a cross-file resolution,
// and AMBIGUOUS is a guess. BrainEdgesAmong orders by weight, so this is what
// decides which relations survive into a crowded picture.
func edgeWeight(e graphify.Edge) float64 {
	w := e.Weight
	if w <= 0 {
		switch strings.ToUpper(strings.TrimSpace(e.Confidence)) {
		case "EXTRACTED":
			w = 1
		case "INFERRED":
			w = 0.6
		default:
			w = 0.3
		}
	}
	if w > 1 {
		w = 1
	}
	return w
}

// StructuralFiles picks the files worth handing to the parser.
//
// It starts from the same list the semantic scan reads — which is what enforces
// the operator's scan policy — and keeps only what Graphify's AST pass
// understands. Mimir choosing the files, rather than pointing Graphify at the
// directory, is the whole of how task-69's consent survives this feature: an
// excluded path is never named, so it is never opened.
func StructuralFiles(ctx context.Context, projectPath string, exclude Excluder) ([]string, error) {
	rels, err := listScannable(ctx, projectPath)
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(rels))
	for _, rel := range rels {
		if !graphify.Parses(rel) {
			continue
		}
		if exclude.Excludes(fullPath(projectPath, rel)) {
			continue
		}
		out = append(out, rel)
	}
	return out, nil
}

func fullPath(projectPath, rel string) string {
	return fmt.Sprintf("%s/%s", strings.TrimRight(projectPath, "/"), rel)
}

// StructuralDigest is what a project's parseable files look like right now.
//
// The parser is deterministic, so parsing an unchanged project produces rows
// the store already has: the same symbols, the same edges, the same content
// hashes. It is pure cost. Without this, every sweep re-parsed every file of
// every project for ever — three hundred and sixteen files in this repository
// alone, eighteen projects on this machine, on a loop.
//
// The path, size and modification time, in the order StructuralFiles returns
// them. Not the contents: hashing what the parser is about to read would cost
// most of what it saves, and a file whose bytes changed without its size or
// mtime moving is a file somebody went out of their way to forge.
func StructuralDigest(projectPath string, rels []string) string {
	h := sha256.New()
	for _, rel := range rels {
		// A hash never fails a write, so the errors are dropped deliberately
		// rather than turned into a return this function has no use for.
		_, _ = fmt.Fprintf(h, "%s\x00", rel)
		if info, err := os.Stat(fullPath(projectPath, rel)); err == nil {
			_, _ = fmt.Fprintf(h, "%d\x00%d\x00", info.Size(), info.ModTime().UnixNano())
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// StructuralCursorKey names the digest in the cursor store, in the shape
// brain_capture_state expects: '<kind>:<project_path>'.
func StructuralCursorKey(projectPath string) string { return "structural:" + projectPath }
