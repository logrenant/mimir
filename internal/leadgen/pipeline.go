package leadgen

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/maps"
)

// RegionSearcher is the primary data source (Google Places). *maps.Client
// satisfies it.
type RegionSearcher interface {
	SearchText(ctx context.Context, q maps.Query) ([]maps.Company, error)
}

// RegionScraper is the fallback data source (the Playwright sidecar).
// *mapscrape.Client satisfies it. It may be nil — then a Places failure is a
// hard failure, with nothing to fall back to.
type RegionScraper interface {
	Search(ctx context.Context, q maps.Query) ([]maps.Company, error)
}

// RegionStore is the region-search cache. *store.Store satisfies it. A nil
// RegionStore means every region search is a billed request (SD-6).
type RegionStore interface {
	GetRegionSearch(ctx context.Context, regionKey string, ttl time.Duration) ([]maps.Company, bool, error)
	PutRegionSearch(ctx context.Context, regionKey, query string, cs []maps.Company) error
}

// ErrNoData means neither the Places API nor the scrape fallback could produce
// a company list — the pipeline has nothing to work with.
var ErrNoData = errors.New("leadgen: region search produced no data from any source")

// CompanyLead is one company carried through every stage that ran. Its company
// fields mirror the maps_search tool's wire shape (snake_case, no free-text
// Google types) so a client sees one company representation across both
// surfaces.
type CompanyLead struct {
	PlaceID        string  `json:"place_id"`
	Name           string  `json:"name"`
	Address        string  `json:"address,omitempty"`
	Latitude       float64 `json:"latitude,omitempty"`
	Longitude      float64 `json:"longitude,omitempty"`
	Rating         float64 `json:"rating,omitempty"`
	ReviewCount    int     `json:"review_count,omitempty"`
	Website        string  `json:"website,omitempty"`
	Phone          string  `json:"phone,omitempty"`
	PrimaryType    string  `json:"primary_type,omitempty"`
	BusinessStatus string  `json:"business_status,omitempty"`
	Source         string  `json:"source,omitempty"`

	Category       Category `json:"category"`
	CategoryMethod string   `json:"category_method,omitempty"`
	Email          string   `json:"email,omitempty"`
	EmailStatus    string   `json:"email_status,omitempty"`
	EmailMethod    string   `json:"email_method,omitempty"`
}

func leadFrom(c maps.Company) CompanyLead {
	return CompanyLead{
		PlaceID:        c.PlaceID,
		Name:           c.Name,
		Address:        c.FormattedAddress,
		Latitude:       c.Latitude,
		Longitude:      c.Longitude,
		Rating:         c.Rating,
		ReviewCount:    c.ReviewCount,
		Website:        c.Website,
		Phone:          c.Phone,
		PrimaryType:    c.PrimaryType,
		BusinessStatus: c.BusinessStatus,
		Source:         c.Source,
		Category:       CategoryUnknown,
	}
}

// CategoryReport is one category's gap analysis plus how many companies fell
// into it.
type CategoryReport struct {
	Category     Category `json:"category"`
	CompanyCount int      `json:"company_count"`
	GapAnalysis  string   `json:"gap_analysis,omitempty"`
	GapMethod    string   `json:"gap_method,omitempty"`
	Truncated    bool     `json:"truncated,omitempty"`
}

// Report is the whole pipeline result. Notes collects the diagnostic gaps from
// every stage — none of them is a reason to fail (SD-6).
type Report struct {
	Region         string           `json:"region"`
	Query          string           `json:"query"`
	FromCache      bool             `json:"from_cache"`
	RanCategorize  bool             `json:"ran_categorize"`
	RanGapAnalysis bool             `json:"ran_gap_analysis"`
	RanEmails      bool             `json:"ran_emails"`
	Companies      []CompanyLead    `json:"companies"`
	Categories     []CategoryReport `json:"categories"`
	Notes          []string         `json:"notes,omitempty"`
}

// RunRequest is one full-pipeline invocation.
type RunRequest struct {
	// Query is the region search, as typed into Google Maps.
	Query maps.Query
	// Region is the human label for the area, used as the gap-analysis and
	// email cache-key region. Empty falls back to Query.Text.
	Region string
	// WithGapAnalysis runs stage 3. WithEmails implies it.
	WithGapAnalysis bool
	// WithEmails runs stage 4.
	WithEmails bool
}

// Pipeline threads region search → categorize → per-category gap analysis →
// per-company outreach email, keyed end to end by place_id. Stages 1–2 are
// near-zero-token; stages 3–4 are opt-in per request.
type Pipeline struct {
	cfg         config.Config
	searcher    RegionSearcher
	scraper     RegionScraper
	regionStore RegionStore
	categorizer *Categorizer
	gaps        *GapAnalyzerRunner
	emails      *EmailRunner
}

// NewPipeline wires the orchestrator. scraper, regionStore, categorizer, gaps
// and emails may each be nil; the pipeline degrades the corresponding stage
// rather than failing.
func NewPipeline(cfg config.Config, searcher RegionSearcher, scraper RegionScraper, rs RegionStore, cat *Categorizer, gaps *GapAnalyzerRunner, emails *EmailRunner) *Pipeline {
	return &Pipeline{
		cfg:         cfg,
		searcher:    searcher,
		scraper:     scraper,
		regionStore: rs,
		categorizer: cat,
		gaps:        gaps,
		emails:      emails,
	}
}

// Run executes the pipeline. It returns an error only when it can produce no
// company list at all (ErrNoData or a cancelled context); every other problem
// is a note on the Report.
func (p *Pipeline) Run(ctx context.Context, req RunRequest) (Report, error) {
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	if p.searcher == nil {
		return Report{}, errors.New("leadgen: pipeline has no region searcher")
	}

	region := req.Region
	if region == "" {
		region = req.Query.Text
	}

	rep := Report{Region: region, Query: req.Query.Text}

	companies, fromCache, notes, err := p.regionSearch(ctx, req.Query)
	rep.Notes = append(rep.Notes, notes...)
	if err != nil {
		return Report{}, err
	}
	rep.FromCache = fromCache

	leads := make([]CompanyLead, len(companies))
	for i, c := range companies {
		leads[i] = leadFrom(c)
	}

	// Stage 2 — categorize. Near-zero-token; always runs when wired.
	if p.categorizer != nil {
		results, catGaps, err := p.categorizer.Categorize(ctx, companies)
		if err != nil {
			return Report{}, err
		}
		rep.RanCategorize = true
		rep.Notes = append(rep.Notes, catGaps...)
		for i := range leads {
			if i < len(results) {
				leads[i].Category = results[i].Category
				leads[i].CategoryMethod = results[i].Method
			}
		}
	}

	byCategory := groupByCategory(leads)

	// Stage 3 — gap analysis, one call per real category, bounded fan-out.
	gapText := map[Category]string{}
	wantGaps := (req.WithGapAnalysis || req.WithEmails) && p.gaps != nil
	if wantGaps {
		rep.RanGapAnalysis = true
		reports, gapNotes := p.analyzeGaps(ctx, region, companies, byCategory)
		rep.Notes = append(rep.Notes, gapNotes...)
		rep.Categories = reports
		for _, cr := range reports {
			if cr.GapAnalysis != "" {
				gapText[cr.Category] = cr.GapAnalysis
			}
		}
	} else {
		rep.Categories = categoryCounts(byCategory)
	}

	// Stage 4 — one email per company that has a gap analysis for its category.
	if req.WithEmails && p.emails != nil {
		rep.RanEmails = true
		emailNotes := p.draftEmails(ctx, companies, leads, gapText)
		rep.Notes = append(rep.Notes, emailNotes...)
	}

	rep.Companies = leads
	return rep, nil
}

// regionSearch resolves the company list: cache, then Places, then the scrape
// fallback. A hit is a hit; only "no source produced anything" is an error.
func (p *Pipeline) regionSearch(ctx context.Context, q maps.Query) ([]maps.Company, bool, []string, error) {
	var notes []string
	key := q.Key()

	if p.regionStore != nil {
		cs, ok, err := p.regionStore.GetRegionSearch(ctx, key, p.cfg.LeadgenRegionTTL)
		if err != nil {
			notes = append(notes, "region cache unavailable: "+err.Error())
		} else if ok {
			return cs, true, notes, nil
		}
	}

	cs, err := p.searcher.SearchText(ctx, q)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, false, notes, ctxErr
		}
		if p.scraper == nil {
			return nil, false, notes, fmt.Errorf("%w: places api failed and no scrape fallback is configured: %v", ErrNoData, err)
		}
		notes = append(notes, "places api failed, using scrape fallback: "+err.Error())
		cs, err = p.scraper.Search(ctx, q)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, false, notes, ctxErr
			}
			return nil, false, notes, fmt.Errorf("%w: both places api and scrape fallback failed: %v", ErrNoData, err)
		}
	}

	if p.regionStore != nil {
		if err := p.regionStore.PutRegionSearch(ctx, key, q.Text, cs); err != nil {
			notes = append(notes, "caching region search failed: "+err.Error())
		}
	}
	return cs, false, notes, nil
}

// analyzeGaps runs stage 3 for every real category with at least one company,
// in bounded-concurrency batches. Output order is the sorted category order,
// regardless of completion order.
func (p *Pipeline) analyzeGaps(ctx context.Context, region string, companies []maps.Company, byCategory map[Category][]int) ([]CategoryReport, []string) {
	cats := sortedCategories(byCategory)

	reports := make([]CategoryReport, len(cats))
	noteSlices := make([][]string, len(cats))

	limit := p.cfg.MaxConcurrentRefines
	if limit <= 0 {
		limit = 1
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(limit)

	for i, cat := range cats {
		idxs := byCategory[cat]
		reports[i] = CategoryReport{Category: cat, CompanyCount: len(idxs)}

		// A gap analysis of "unknown" companies is meaningless — they share
		// nothing but the fact that we could not classify them.
		if cat == CategoryUnknown {
			continue
		}

		batch := make([]maps.Company, 0, len(idxs))
		for _, idx := range idxs {
			batch = append(batch, companies[idx])
		}

		g.Go(func() error {
			if err := gctx.Err(); err != nil {
				return err
			}
			res, gaps, err := p.gaps.AnalyzeCategory(gctx, region, cat, batch)
			if err != nil {
				return err
			}
			reports[i].GapAnalysis = res.Analysis
			reports[i].GapMethod = res.Method
			reports[i].Truncated = res.Truncated
			noteSlices[i] = gaps
			return nil
		})
	}

	var notes []string
	if err := g.Wait(); err != nil {
		notes = append(notes, "gap analysis cancelled: "+err.Error())
	}
	for _, ns := range noteSlices {
		notes = append(notes, ns...)
	}
	return reports, notes
}

// draftEmails runs stage 4 for every company whose category produced a gap
// analysis, in bounded-concurrency batches. Writes results back into leads
// (each goroutine touches a distinct index, so the slice needs no lock).
func (p *Pipeline) draftEmails(ctx context.Context, companies []maps.Company, leads []CompanyLead, gapText map[Category]string) []string {
	limit := p.cfg.MaxConcurrentRefines
	if limit <= 0 {
		limit = 1
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(limit)

	noteSlices := make([][]string, len(leads))

	for i := range leads {
		gap := gapText[leads[i].Category]
		if gap == "" {
			continue
		}
		g.Go(func() error {
			if err := gctx.Err(); err != nil {
				return err
			}
			res, gaps, err := p.emails.DraftFor(gctx, companies[i], leads[i].Category, gap)
			if err != nil {
				return err
			}
			leads[i].Email = res.Email
			leads[i].EmailStatus = res.Status
			leads[i].EmailMethod = res.Method
			noteSlices[i] = gaps
			return nil
		})
	}

	var notes []string
	if err := g.Wait(); err != nil {
		notes = append(notes, "email drafting cancelled: "+err.Error())
	}
	for _, ns := range noteSlices {
		notes = append(notes, ns...)
	}
	return notes
}

func groupByCategory(leads []CompanyLead) map[Category][]int {
	out := map[Category][]int{}
	for i, l := range leads {
		out[l.Category] = append(out[l.Category], i)
	}
	return out
}

func sortedCategories(byCategory map[Category][]int) []Category {
	cats := make([]Category, 0, len(byCategory))
	for c := range byCategory {
		cats = append(cats, c)
	}
	sort.Slice(cats, func(i, j int) bool { return cats[i] < cats[j] })
	return cats
}

func categoryCounts(byCategory map[Category][]int) []CategoryReport {
	cats := sortedCategories(byCategory)
	out := make([]CategoryReport, len(cats))
	for i, c := range cats {
		out[i] = CategoryReport{Category: c, CompanyCount: len(byCategory[c])}
	}
	return out
}
