package catalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/logrenant/mimir/internal/llm"
)

// ResearchRequest is one product's question to the market.
type ResearchRequest struct {
	Query string
	TopN  int
}

// Researcher is the market research one product gets.
//
// It is stated in this package's own types rather than internal/pipeline's,
// and that is not stylistic: internal/store imports this package for its row
// shapes, internal/pipeline imports internal/store for its cache, so importing
// the pipeline here is a cycle the compiler refuses. The adapter that satisfies
// this from a *pipeline.Pipeline lives in internal/catalogjob, where the two
// already meet.
//
// Whatever satisfies it must have passed scraped page text through
// internal/refine first. That is SD-2 and it is not this interface's to relax —
// the pipeline is the only implementation precisely because it is the one that
// cannot skip the step.
type Researcher interface {
	Research(ctx context.Context, req ResearchRequest) (Findings, error)
}

// Source is one competitor page the research read.
type Source struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// Findings is what a product's research produced.
//
// Refined is carried rather than assumed: it is the assertion the MCP
// choke-point checks, and a Findings that lost it on the way through the store
// would fail closed at the boundary rather than leak.
type Findings struct {
	Summary   string   `json:"summary"`
	KeyPoints []string `json:"key_points"`
	Gaps      []string `json:"gaps"`
	Sources   []Source `json:"sources"`
	Refined   bool     `json:"refined"`
}

// IsEmpty reports whether the research found nothing worth writing from.
func (f Findings) IsEmpty() bool {
	return strings.TrimSpace(f.Summary) == "" && len(f.KeyPoints) == 0
}

// researchVersion is the cache key for a product's market research.
//
// The brand hash is deliberately not in it. What a competitor's page says about
// a category does not change because this store decided to address its readers
// as "siz", and throwing it away when the voice is edited would make correcting
// the voice the most expensive thing an operator can do.
func (s *Studio) researchVersion(sel llm.Selection) string {
	v := s.cfg.CatalogResearchVersion
	if k := sel.Key(); k != "" {
		v += "@" + k
	}
	return v
}

// researchFor returns a product's findings, from the cache when they exist.
//
// The read comes first and it is the whole point: a bulk run that stopped at
// product 300 has already paid for 299 pieces of research, and a resumed run
// that re-bought them would cost more than the run it is resuming.
func (s *Studio) researchFor(ctx context.Context, p Product, sel llm.Selection) (Findings, bool, error) {
	version := s.researchVersion(sel)

	if s.store != nil {
		if f, ok, err := s.store.GetCatalogResearch(ctx, p.ID, version); err == nil && ok {
			return f, true, nil
		} else if err != nil {
			return Findings{}, false, err
		}
	}
	if s.research == nil {
		return Findings{}, false, fmt.Errorf("catalog: no researcher configured")
	}

	f, err := s.research.Research(ctx, ResearchRequest{
		Query: researchQuery(p),
		TopN:  s.cfg.CatalogResearchSources,
	})
	if err != nil {
		return Findings{}, false, err
	}

	if s.store != nil {
		if err := s.store.PutCatalogResearch(ctx, p.ID, version, f); err != nil {
			// The research was paid for and is usable; failing the product over
			// a write that will simply be repeated next run would be the
			// expensive answer to the cheap problem.
			return f, false, nil
		}
	}
	return f, false, nil
}

// researchQuery is what the market is asked about.
//
// The product's own title and category, and nothing from its description. A
// description is this store's marketing copy: feeding it back into the search
// that is meant to find what *other* stores say would return this store's own
// phrasing and call it the market.
func researchQuery(p Product) string {
	parts := make([]string, 0, 2)
	if t := strings.TrimSpace(p.Original.Title); t != "" {
		parts = append(parts, t)
	}
	if c := strings.TrimSpace(p.Category); c != "" {
		parts = append(parts, c)
	}
	if len(parts) == 0 {
		parts = append(parts, strings.TrimSpace(p.Key))
	}
	return strings.Join(parts, " ")
}
