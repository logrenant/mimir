package catalogjob

import (
	"context"

	"github.com/logrenant/mimir/internal/catalog"
	"github.com/logrenant/mimir/internal/pipeline"
)

// The adapter between the research pipeline and the studio.
//
// It lives here rather than in internal/catalog because that package cannot
// import internal/pipeline: internal/store imports the catalog for its row
// shapes, the pipeline imports the store for its cache, and the compiler
// refuses the loop. This package already depends on both, so the translation
// costs a file and no new edge in the graph.

// Briefer is internal/pipeline's research half, narrowed to what a catalog
// needs. Naming it here rather than taking *pipeline.Pipeline keeps the studio
// testable against something that spends nothing.
type Briefer interface {
	Research(ctx context.Context, q pipeline.Query) (pipeline.Brief, error)
}

// Research adapts a pipeline into the studio's Researcher.
//
// The pipeline is the only implementation on purpose: it is the one that cannot
// skip internal/refine, and SD-2 is not a property this adapter could restore
// if something else were passed here.
func Research(b Briefer) catalog.Researcher { return &briefResearcher{b: b} }

type briefResearcher struct{ b Briefer }

func (r *briefResearcher) Research(ctx context.Context, req catalog.ResearchRequest) (catalog.Findings, error) {
	brief, err := r.b.Research(ctx, pipeline.Query{Text: req.Query, TopN: req.TopN})
	if err != nil {
		return catalog.Findings{}, err
	}
	f := catalog.Findings{
		Summary:   brief.Summary,
		KeyPoints: brief.KeyPoints,
		Gaps:      brief.Gaps,
		// Carried, never assumed. It is the assertion the MCP choke-point
		// checks, and a findings value that invented it would fail closed at
		// the boundary rather than leak — but it would also be a lie in the
		// database, which nothing downstream could detect.
		Refined: brief.Refined,
	}
	for _, s := range brief.Sources {
		f.Sources = append(f.Sources, catalog.Source{Title: s.Title, URL: s.URL})
	}
	return f, nil
}
