package leadgen

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/llm"
	"github.com/logrenant/mimir/internal/maps"
	"github.com/logrenant/mimir/internal/store"
)

// RegionSource answers "which companies are in this region", from whichever
// provider is available. *regionsearch.Router satisfies it, and it owns the
// order: the free scrape first, the billed Places API as the fallback.
//
// One seam rather than two because the order is a policy that belongs in one
// place — this pipeline used to hold half of it, and the maps_search tool's
// registration held the other half implicitly.
type RegionSource interface {
	Search(ctx context.Context, q maps.Query) ([]maps.Company, string, []string, error)
	Available() bool
}

// RegionStore is the region-search cache. *store.Store satisfies it. A nil
// RegionStore means every region search is a billed request (SD-6).
type RegionStore interface {
	GetRegionSearch(ctx context.Context, regionKey string, ttl time.Duration) ([]maps.Company, bool, error)
	PutRegionSearch(ctx context.Context, regionKey, query string, cs []maps.Company) error
}

// LedgerStore is the durable lead record — not a cache. *store.Store satisfies
// it, and a nil LedgerStore is a working pipeline: the run still answers, it is
// just not remembered (SD-6).
type LedgerStore interface {
	PutLeadRun(ctx context.Context, run store.LeadRun, rows []store.LeadRow) error
}

// ErrNoData means no configured source could produce a company list — the
// pipeline has nothing to work with.
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
	Region string `json:"region"`
	Query  string `json:"query"`
	// Source is which provider answered — "mapscrape" or "places_api", empty
	// on a cache hit. The two differ in field coverage, so a client reading an
	// empty phone column needs to know which it is looking at.
	Source         string           `json:"source,omitempty"`
	FromCache      bool             `json:"from_cache"`
	RanContacts    bool             `json:"ran_contacts"`
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
	// WithContacts runs stage 1b: open each company's site (and, when it has
	// none, go and find one) for a phone number and an email address. On by
	// default at the API edge, because a lead nobody can ring is not a lead.
	WithContacts bool
	// WithGapAnalysis runs stage 3. WithEmails implies it.
	WithGapAnalysis bool
	// WithEmails runs stage 4.
	WithEmails bool
	// Selection is the operator's choice of provider and model for the model
	// stages of this run — 2, 3 and 4. The zero value routes by class, which
	// is what every caller that does not offer the choice sends.
	//
	// It does not reach the region search. That stage's model fallback
	// (internal/mapsllm, used when the scrape's selectors fail) is wired at
	// construction and shared by every caller of maps_search, so a per-run
	// override there would need a seam this pipeline does not own.
	Selection llm.Selection
}

// Pipeline threads region search → categorize → per-category gap analysis →
// per-company outreach email, keyed end to end by place_id. Stages 1–2 are
// near-zero-token; stages 3–4 are opt-in per request.
type Pipeline struct {
	cfg         config.Config
	source      RegionSource
	regionStore RegionStore
	categorizer *Categorizer
	gaps        *GapAnalyzerRunner
	emails      *EmailRunner
	// contacts fills phone and email from a company's own website. Nil is a
	// working pipeline: the export then writes what the search returned.
	contacts ContactEnricher
	// ledger is the durable record of what a run found. Nil forgets the run.
	ledger LedgerStore
}

// UseContacts installs the contact enricher used by Export.
//
// Set after construction rather than taken by NewPipeline because it is only
// ever used by the export path — a lead-gen run that nobody exports must not
// fetch sixty websites — and because the enricher needs the same crawl client
// this package does not otherwise know about.
func (p *Pipeline) UseContacts(e ContactEnricher) { p.contacts = e }

// UseLedger installs the durable lead record.
//
// Set after construction for the same reason UseContacts is: NewPipeline's
// parameters are the stages, and the ledger is not a stage — it is what happens
// to a finished run. A pipeline without one behaves exactly as it did before
// the ledger existed.
func (p *Pipeline) UseLedger(l LedgerStore) { p.ledger = l }

// NewPipeline wires the orchestrator. regionStore, categorizer, gaps and emails
// may each be nil; the pipeline degrades the corresponding stage rather than
// failing. The source may not: with nothing to search there is no pipeline.
func NewPipeline(cfg config.Config, source RegionSource, rs RegionStore, cat *Categorizer, gaps *GapAnalyzerRunner, emails *EmailRunner) *Pipeline {
	return &Pipeline{
		cfg:         cfg,
		source:      source,
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
	if p.source == nil || !p.source.Available() {
		return Report{}, errors.New("leadgen: pipeline has no region search source")
	}

	region := req.Region
	if region == "" {
		region = req.Query.Text
	}

	rep := Report{Region: region, Query: req.Query.Text}

	// Bind the three model stages to this run's selection. Done once here
	// rather than at each call site so a run cannot end up half on one model
	// and half on another.
	categorizer := p.categorizer.With(req.Selection)
	gapsRunner := p.gaps.With(req.Selection)
	emailRunner := p.emails.With(req.Selection)

	companies, source, fromCache, notes, err := p.regionSearch(ctx, req.Query)
	rep.Notes = append(rep.Notes, notes...)
	if err != nil {
		return Report{}, err
	}
	rep.FromCache = fromCache
	rep.Source = source

	leads := make([]CompanyLead, len(companies))
	for i, c := range companies {
		leads[i] = leadFrom(c)
	}

	// Stage 1b — contacts. Before categorize, because the reachability rule
	// below needs to know whether a company can be contacted at all, and that
	// is only settled once the enricher has looked.
	//
	// This used to run only in Export, and its answers were never written to
	// the ledger — which is why a ledger of seventy companies held zero phone
	// numbers. A lead nobody can ring is not a lead, so finding the number is
	// part of building the list, not part of formatting it.
	if p.contacts != nil && req.WithContacts {
		found := p.contacts.Enrich(ctx, companies)
		filled, withSite := 0, 0
		for i := range leads {
			e, ok := found[leads[i].PlaceID]
			if !ok {
				continue
			}
			if e.Phone != "" {
				leads[i].Phone = e.Phone
				filled++
			}
			if e.Email != "" {
				leads[i].Email = e.Email
			}
			// A site the enricher had to go and find is still the company's
			// site, and both the categorizer and the reachability rule below
			// should see it.
			if leads[i].Website == "" && e.Website != "" {
				leads[i].Website = e.Website
				companies[i].Website = e.Website
				withSite++
			}
			if leads[i].Address == "" && e.Address != "" {
				leads[i].Address = e.Address
			}
		}
		rep.RanContacts = true
		rep.Notes = append(rep.Notes,
			fmt.Sprintf("contacts: %d phone numbers, %d websites recovered", filled, withSite))
	}

	// Stage 2 — categorize. Near-zero-token; always runs when wired.
	if categorizer != nil {
		results, catGaps, err := categorizer.Categorize(ctx, companies)
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

	// A company with neither a phone number nor a website cannot be contacted,
	// and an outreach list is a list of companies you can reach. Filing it
	// under its trade would put it in a sheet an operator works through and
	// then discovers is a dead end, so it is filed as unknown — the same word
	// the categorizer uses for "no answer", because that is what this is.
	for i := range leads {
		if leads[i].Phone == "" && leads[i].Website == "" {
			leads[i].Category = CategoryUnknown
			leads[i].CategoryMethod = MethodUnreachable
		}
	}

	byCategory := groupByCategory(leads)

	// Stage 3 — gap analysis, one call per real category, bounded fan-out.
	gapText := map[Category]string{}
	wantGaps := (req.WithGapAnalysis || req.WithEmails) && gapsRunner != nil
	if wantGaps {
		rep.RanGapAnalysis = true
		reports, gapNotes := p.analyzeGaps(ctx, gapsRunner, region, companies, byCategory)
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
	if req.WithEmails && emailRunner != nil {
		rep.RanEmails = true
		emailNotes := p.draftEmails(ctx, emailRunner, companies, leads, gapText)
		rep.Notes = append(rep.Notes, emailNotes...)
	}

	rep.Companies = leads

	// Last, and never fatal: the ledger is what makes a run outlive its
	// response, but a run that answered is a run that succeeded.
	if note := p.record(ctx, req, rep, leads); note != "" {
		rep.Notes = append(rep.Notes, note)
	}

	return rep, nil
}

// record writes the run and its companies to the ledger. It returns a note
// rather than an error: every other stage degrades this way, and losing the
// record costs the operator a row in a table, not the answer on the screen.
//
// A lead with no place_id is skipped — the ledger is keyed by it, exactly as
// the outreach drafts are.
func (p *Pipeline) record(ctx context.Context, req RunRequest, rep Report, leads []CompanyLead) string {
	if p.ledger == nil || len(leads) == 0 {
		return ""
	}

	rows := make([]store.LeadRow, 0, len(leads))
	for _, l := range leads {
		if l.PlaceID == "" {
			continue
		}
		rows = append(rows, store.LeadRow{
			PlaceID:        l.PlaceID,
			Name:           l.Name,
			Address:        l.Address,
			Latitude:       l.Latitude,
			Longitude:      l.Longitude,
			Rating:         l.Rating,
			ReviewCount:    l.ReviewCount,
			Website:        l.Website,
			Phone:          l.Phone,
			PrimaryType:    l.PrimaryType,
			BusinessStatus: l.BusinessStatus,
			Source:         l.Source,
			Category:       string(l.Category),
			CategoryMethod: l.CategoryMethod,
		})
	}
	if len(rows) == 0 {
		return "lead defteri: hiçbir şirketin place_id'si yok, koşu kaydedilmedi"
	}

	runID, err := newRunID()
	if err != nil {
		return "lead defteri: koşu kimliği üretilemedi: " + err.Error()
	}

	run := store.LeadRun{
		ID:           runID,
		RegionKey:    req.Query.Key(),
		Query:        rep.Query,
		RegionLabel:  rep.Region,
		Source:       rep.Source,
		CompanyCount: len(rows),
		WithGaps:     rep.RanGapAnalysis,
		WithEmails:   rep.RanEmails,
		RanAt:        time.Now(),
	}
	if err := p.ledger.PutLeadRun(ctx, run, rows); err != nil {
		return "lead defteri yazılamadı: " + err.Error()
	}
	return ""
}

// newRunID returns an opaque id for one lead-gen run. Random rather than
// derived from the query: the same search run twice is two runs, and that is
// the whole point of keeping a history.
func newRunID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("leadgen: generating run id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// regionSearch resolves the company list: the cache, then whichever sources the
// router has, in its order. A hit is a hit; only "no source produced anything"
// is an error.
//
// The order itself is not decided here — it is internal/regionsearch's, and it
// puts the free scrape ahead of the billed API. This function owns the cache
// and nothing else about provenance.
func (p *Pipeline) regionSearch(ctx context.Context, q maps.Query) ([]maps.Company, string, bool, []string, error) {
	var notes []string
	key := q.Key()

	if p.regionStore != nil {
		cs, ok, err := p.regionStore.GetRegionSearch(ctx, key, p.cfg.LeadgenRegionTTL)
		if err != nil {
			notes = append(notes, "region cache unavailable: "+err.Error())
		} else if ok {
			return cs, "", true, notes, nil
		}
	}

	cs, source, searchNotes, err := p.source.Search(ctx, q)
	notes = append(notes, searchNotes...)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, "", false, notes, ctxErr
		}
		return nil, "", false, notes, fmt.Errorf("%w: %v", ErrNoData, err)
	}
	notes = append(notes, "region search answered by "+source)

	if p.regionStore != nil {
		if err := p.regionStore.PutRegionSearch(ctx, key, q.Text, cs); err != nil {
			notes = append(notes, "caching region search failed: "+err.Error())
		}
	}
	return cs, source, false, notes, nil
}

// analyzeGaps runs stage 3 for every real category with at least one company,
// in bounded-concurrency batches. Output order is the sorted category order,
// regardless of completion order.
func (p *Pipeline) analyzeGaps(ctx context.Context, runner *GapAnalyzerRunner, region string, companies []maps.Company, byCategory map[Category][]int) ([]CategoryReport, []string) {
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
			res, gaps, err := runner.AnalyzeCategory(gctx, region, cat, batch)
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
func (p *Pipeline) draftEmails(ctx context.Context, runner *EmailRunner, companies []maps.Company, leads []CompanyLead, gapText map[Category]string) []string {
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
			res, gaps, err := runner.DraftFor(gctx, companies[i], leads[i].Category, gap)
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
