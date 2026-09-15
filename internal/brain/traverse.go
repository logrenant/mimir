package brain

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/logrenant/mimir/internal/store"
)

// Reading the graph as a graph.
//
// Everything else in this package writes: a scan distils a file, a relate pass
// links it. These five read, and none of them spends a model call — the answers
// are already facts in brain_edges, put there by the parser
// (internal/brain/structural.go), and asking a model to restate them would be
// slower and less true.
//
// The shapes are Graphify's, because Graphify had already worked out which five
// questions are worth asking of a code graph: query, path, explain, affected,
// god-nodes. What is not Graphify's is the implementation — going through its
// CLI would mean a second graph file on disk, kept in sync with this one, and a
// Python prerequisite for reading data Mimir already has. task-72 drew that
// line ("a second brain would look like two separate systems"); this keeps it.
//
// The one discipline carried over verbatim is the constrained expansion in
// Query. It is not politeness: the index matches literally, so a question
// phrased in different words than the graph's own returns nothing, and an
// answer built on nothing is worse than no answer.

// Hit is one node an answer passes through.
type Hit struct {
	NodeID string `json:"node_id"`
	Title  string `json:"title"`
	Kind   string `json:"kind"`
	// File and Location are where the thing actually is, so an answer can be
	// checked rather than believed.
	File     string `json:"file,omitempty"`
	Location string `json:"location,omitempty"`
	// Why is the edge this hit was reached through, named. A path with no
	// reasons on it is a list, not an explanation.
	Why  string `json:"why,omitempty"`
	Hops int    `json:"hops"`
}

// QueryResult is an answer plus the means to audit it.
type QueryResult struct {
	// Expanded is the vocabulary the question was actually run as. It is part
	// of the answer, not debug output: a reader who cannot see which words
	// were searched cannot tell a miss from an absence.
	Expanded []string `json:"expanded"`
	Hits     []Hit    `json:"hits"`
	// Note is the honest empty answer — "this graph has no vocabulary for
	// that" — rather than an empty list, which reads as "there is nothing
	// there".
	Note string `json:"note,omitempty"`
}

// structuralKinds are the parser-derived edges, which are the ones with a
// direction worth honouring.
func structuralKinds() []string {
	return []string{"calls", "imports", "defines", "inherits", "implements", "references", "uses", "structural"}
}

// wordRE keeps combining marks inside the word rather than treating them as
// separators. macOS hands back decomposed filenames — this store holds a node
// whose source key is `İlk-çizim.uskroki` with the cedilla as its own
// codepoint — and a mark is not a letter, so the plain class cut that name into
// `I`, `lk`, `c`, `izim`: four words nobody wrote, none of them findable.
var wordRE = regexp.MustCompile(`[\p{L}\p{N}\p{M}]+`)

// Query walks out from whatever the question matches.
//
// Step one is the expansion, and it is required rather than an optimisation.
// The index matches case-folded substrings: no stemming, no synonyms, nothing
// across languages. A question that says "authentication" against a graph that
// says "Guardian" scores zero, and a traversal from zero start nodes returns
// noise. So the question is first mapped onto tokens the graph actually
// contains — and only onto those. A concept with no token here is dropped, not
// approximated, because an approximated token is an invented one.
func (c *Core) Query(ctx context.Context, projectPath, question string, budget int) (QueryResult, error) {
	if c == nil || c.store == nil {
		return QueryResult{}, ErrNoStore
	}
	if budget <= 0 {
		budget = c.cfg.BrainSearchLimit
	}

	expanded, err := c.expand(ctx, projectPath, question)
	if err != nil {
		return QueryResult{}, err
	}
	if len(expanded) == 0 {
		return QueryResult{
			Note: "bu grafikte bu konuda kelime yok — soru grafiğin kendi " +
				"kelime dağarcığıyla eşleşmedi",
		}, nil
	}

	seeds, err := c.store.SearchBrainNodes(ctx, projectPath, strings.Join(expanded, " "), c.cfg.BrainSearchLimit)
	if err != nil {
		return QueryResult{}, err
	}
	if len(seeds) == 0 {
		return QueryResult{Expanded: expanded, Note: "kelimeler eşleşti ama düğüm bulunamadı"}, nil
	}

	hits := c.walk(ctx, seeds, budget)
	return QueryResult{Expanded: expanded, Hits: hits}, nil
}

// expand picks up to twelve words the graph actually has.
//
// The candidate vocabulary comes from the graph rather than from a dictionary,
// which is what makes "do not invent" enforceable rather than a request: a
// word is only ever kept if the index can produce a node for it.
//
// That test used to be made against the *titles* of the best matches, and it
// was too narrow by exactly the fields it left out. The index reads title,
// assessment, tags, aliases and body; a title is one of five. So a word written
// into dozens of assessments — "popover" in this repository, at the time of
// writing, in eight of them — never appeared in the reconstructed vocabulary
// and the console answered that the graph had never heard of it. The word was
// there; the check was looking in one drawer. It asks the index now, which is
// the same question the search that follows will ask.
func (c *Core) expand(ctx context.Context, projectPath, question string) ([]string, error) {
	raw := wordRE.FindAllString(question, -1)
	if len(raw) == 0 {
		return nil, nil
	}

	seen := map[string]struct{}{}
	asked := make([]string, 0, len(raw))
	for _, word := range raw {
		word = foldWord(word)
		// Two letters carry no signal and match nearly everything.
		if len([]rune(word)) < 3 {
			continue
		}
		if _, dup := seen[word]; dup {
			continue
		}
		seen[word] = struct{}{}
		asked = append(asked, word)
		if len(asked) == 12 {
			break
		}
	}
	if len(asked) == 0 {
		return nil, nil
	}

	out, err := c.store.BrainVocabulary(ctx, projectPath, asked)
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// foldWord lower-cases a word and drops its combining marks, which is what the
// index did to the same text on the way in: FTS5's unicode61 tokeniser folds
// case and strips diacritics. Comparing an unfolded question against a folded
// index is how a word present in both fails to match itself.
func foldWord(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return unicode.ToLower(r)
	}, s)
}

// walk is the breadth-first expansion shared by Query and Explain, bounded by
// a node budget rather than a depth: a hub in this repository has hundreds of
// structural edges, and a fixed depth over one of them is not a bound at all.
func (c *Core) walk(ctx context.Context, seeds []store.BrainNodeRow, budget int) []Hit {
	type queued struct {
		id   string
		hops int
	}

	seen := map[string]struct{}{}
	var frontier []queued
	var hits []Hit

	for _, row := range seeds {
		if _, dup := seen[row.ID]; dup {
			continue
		}
		seen[row.ID] = struct{}{}
		hits = append(hits, hitOf(row, "", 0))
		frontier = append(frontier, queued{id: row.ID, hops: 0})
		if len(hits) >= budget {
			return hits
		}
	}

	for len(frontier) > 0 && len(hits) < budget {
		cur := frontier[0]
		frontier = frontier[1:]

		edges, err := c.store.BrainNeighbors(ctx, cur.id, c.cfg.BrainNeighborCap)
		if err != nil {
			break
		}
		ids := make([]string, 0, len(edges))
		why := map[string]string{}
		for _, e := range edges {
			if _, dup := seen[e.Dst]; dup {
				continue
			}
			seen[e.Dst] = struct{}{}
			ids = append(ids, e.Dst)
			why[e.Dst] = e.Kind
		}
		if len(ids) == 0 {
			continue
		}
		rows, err := c.store.BrainNodesByIDs(ctx, ids)
		if err != nil {
			break
		}
		for _, row := range rows {
			hits = append(hits, hitOf(row, why[row.ID], cur.hops+1))
			frontier = append(frontier, queued{id: row.ID, hops: cur.hops + 1})
			if len(hits) >= budget {
				break
			}
		}
	}
	return hits
}

// Affected is the reverse traversal: what depends on this.
//
// This is the question BrainNeighbors cannot answer. It normalises every edge
// so the node asked about is always Src, which erases exactly the information
// "who calls me" needs — and the parser has been writing that direction down
// since task-72.
func (c *Core) Affected(ctx context.Context, id string, kinds []string, depth int) ([]Hit, error) {
	if c == nil || c.store == nil {
		return nil, ErrNoStore
	}
	if depth <= 0 {
		depth = 2
	}
	if len(kinds) == 0 {
		kinds = structuralKinds()
	}

	if _, found, err := c.store.BrainNode(ctx, id); err != nil {
		return nil, err
	} else if !found {
		return nil, ErrNodeNotFound
	}

	seen := map[string]struct{}{id: {}}
	frontier := []string{id}
	var hits []Hit

	for hop := 1; hop <= depth && len(frontier) > 0; hop++ {
		var next []string
		why := map[string]string{}
		for _, cur := range frontier {
			edges, err := c.store.BrainInboundEdges(ctx, cur, kinds, c.cfg.BrainNeighborCap)
			if err != nil {
				return nil, err
			}
			for _, e := range edges {
				if _, dup := seen[e.Src]; dup {
					continue
				}
				seen[e.Src] = struct{}{}
				next = append(next, e.Src)
				why[e.Src] = e.Kind
			}
		}
		if len(next) == 0 {
			break
		}
		rows, err := c.store.BrainNodesByIDs(ctx, next)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			hits = append(hits, hitOf(row, why[row.ID], hop))
		}
		frontier = next
	}
	return hits, nil
}

// Path is the shortest chain between two nodes.
//
// Bidirectional: a breadth-first search from one end over a graph where hubs
// have hundreds of edges visits most of it before it arrives, and meeting in
// the middle is the difference between an answer and a timeout.
func (c *Core) Path(ctx context.Context, from, to string) ([]Hit, error) {
	if c == nil || c.store == nil {
		return nil, ErrNoStore
	}
	for _, id := range []string{from, to} {
		if _, found, err := c.store.BrainNode(ctx, id); err != nil {
			return nil, err
		} else if !found {
			return nil, ErrNodeNotFound
		}
	}
	if from == to {
		return c.hitsFor(ctx, []string{from}, nil)
	}

	fromSeen := map[string]string{from: ""}
	toSeen := map[string]string{to: ""}
	fromEdge := map[string]string{}
	toEdge := map[string]string{}
	fromWave := []string{from}
	toWave := []string{to}

	for hop := 0; hop < c.cfg.BrainNeighborCap && len(fromWave) > 0 && len(toWave) > 0; hop++ {
		if meet, ok := c.step(ctx, &fromWave, fromSeen, fromEdge, toSeen); ok {
			return c.assemble(ctx, meet, fromSeen, toSeen, fromEdge, toEdge)
		}
		if meet, ok := c.step(ctx, &toWave, toSeen, toEdge, fromSeen); ok {
			return c.assemble(ctx, meet, fromSeen, toSeen, fromEdge, toEdge)
		}
	}
	return nil, nil
}

// step expands one wave, reporting the node where it met the other side.
func (c *Core) step(ctx context.Context, wave *[]string, seen, edge map[string]string, other map[string]string) (string, bool) {
	var next []string
	for _, cur := range *wave {
		edges, err := c.store.BrainNeighbors(ctx, cur, c.cfg.BrainNeighborCap)
		if err != nil {
			break
		}
		for _, e := range edges {
			if _, dup := seen[e.Dst]; dup {
				continue
			}
			seen[e.Dst] = cur
			edge[e.Dst] = e.Kind
			if _, met := other[e.Dst]; met {
				*wave = next
				return e.Dst, true
			}
			next = append(next, e.Dst)
		}
	}
	*wave = next
	return "", false
}

func (c *Core) assemble(ctx context.Context, meet string, fromSeen, toSeen, fromEdge, toEdge map[string]string) ([]Hit, error) {
	var left []string
	for node := meet; node != ""; node = fromSeen[node] {
		left = append([]string{node}, left...)
		if fromSeen[node] == "" {
			break
		}
	}
	var right []string
	for node := toSeen[meet]; node != ""; node = toSeen[node] {
		right = append(right, node)
		if toSeen[node] == "" {
			break
		}
	}

	chain := make([]string, 0, len(left)+len(right))
	chain = append(chain, left...)
	chain = append(chain, right...)
	why := map[string]string{}
	for k, v := range fromEdge {
		why[k] = v
	}
	for k, v := range toEdge {
		if _, ok := why[k]; !ok {
			why[k] = v
		}
	}
	return c.hitsFor(ctx, chain, why)
}

// Explain is one node and what it is connected to, in plain shape.
func (c *Core) Explain(ctx context.Context, id string) (QueryResult, error) {
	if c == nil || c.store == nil {
		return QueryResult{}, ErrNoStore
	}
	row, found, err := c.store.BrainNode(ctx, id)
	if err != nil {
		return QueryResult{}, err
	}
	if !found {
		return QueryResult{}, ErrNodeNotFound
	}
	return QueryResult{Hits: c.walk(ctx, []store.BrainNodeRow{row}, c.cfg.BrainNeighborCap)}, nil
}

// Hubs is the architectural centres — Graphify calls them god nodes.
//
// It reads the same degree ranking the graph view already computes, because
// "most connected" is the same question drawn and asked.
func (c *Core) Hubs(ctx context.Context, projectPath string, top int) ([]Hit, error) {
	if c == nil || c.store == nil {
		return nil, ErrNoStore
	}
	if top <= 0 {
		top = 10
	}
	degrees, err := c.store.BrainGraphIDs(ctx, projectPath, top, nil)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(degrees))
	rank := map[string]int{}
	for _, d := range degrees {
		ids = append(ids, d.ID)
		rank[d.ID] = d.Degree
	}
	rows, err := c.store.BrainNodesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	hits := make([]Hit, 0, len(rows))
	for _, row := range rows {
		h := hitOf(row, "", 0)
		h.Hops = rank[row.ID]
		hits = append(hits, h)
	}
	// By degree, because that is what was asked for; BrainNodesByIDs answers
	// in whatever order the store finds them.
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Hops > hits[j].Hops })
	return hits, nil
}

func (c *Core) hitsFor(ctx context.Context, ids []string, why map[string]string) ([]Hit, error) {
	rows, err := c.store.BrainNodesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := map[string]store.BrainNodeRow{}
	for _, row := range rows {
		byID[row.ID] = row
	}
	out := make([]Hit, 0, len(ids))
	for i, id := range ids {
		row, ok := byID[id]
		if !ok {
			continue
		}
		out = append(out, hitOf(row, why[id], i))
	}
	return out, nil
}

func hitOf(row store.BrainNodeRow, why string, hops int) Hit {
	h := Hit{
		NodeID: row.ID,
		Title:  row.Title,
		Kind:   row.Kind,
		Why:    why,
		Hops:   hops,
	}
	// A symbol's assessment is where the parser recorded the file and line.
	if row.Kind == KindSymbol {
		h.File, h.Location = splitLocation(row.Assessment)
	} else {
		h.File = row.SourceKey
	}
	return h
}

// splitLocation reads back what structural.go's `located` wrote: a path, and
// optionally a line reference after a space.
func splitLocation(s string) (file, location string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	if i := strings.LastIndexByte(s, ' '); i > 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}
