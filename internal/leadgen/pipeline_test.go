package leadgen

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/maps"
	"github.com/logrenant/mimir/internal/refine"
	"github.com/logrenant/mimir/internal/regionsearch"
	"github.com/logrenant/mimir/internal/settings"
	"github.com/logrenant/mimir/internal/store"
)

// --- fakes for the region-search stage -----------------------------------

type fakeSearcher struct {
	mu     sync.Mutex
	calls  int
	lastQ  maps.Query
	result []maps.Company
	err    error
}

func (f *fakeSearcher) SearchText(_ context.Context, q maps.Query) ([]maps.Company, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastQ = q
	return f.result, f.err
}

type fakeScraper struct {
	mu     sync.Mutex
	calls  int
	lastQ  maps.Query
	result []maps.Company
	err    error
}

func (f *fakeScraper) Search(_ context.Context, q maps.Query) ([]maps.Company, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastQ = q
	return f.result, f.err
}

type fakeRegionStore struct {
	mu       sync.Mutex
	getHit   []maps.Company
	getOK    bool
	getErr   error
	putErr   error
	putCalls int
}

func (f *fakeRegionStore) GetRegionSearch(_ context.Context, _ string, _ time.Duration) ([]maps.Company, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, false, f.getErr
	}
	return f.getHit, f.getOK, nil
}

func (f *fakeRegionStore) PutRegionSearch(_ context.Context, _, _ string, _ []maps.Company) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.putCalls++
	return f.putErr
}

// --- helpers ---------------------------------------------------------------

func pipeCompany(id, primaryType string) maps.Company {
	return maps.Company{
		PlaceID:          id,
		Name:             "Company " + id,
		PrimaryType:      primaryType,
		FormattedAddress: "1 Test Street, Kadikoy",
		Phone:            "+90 555 111 2233",
		Rating:           4.1,
		ReviewCount:      40,
	}
}

func pipeConfig() config.Config {
	cfg := testConfig()
	cfg.LeadgenRegionTTL = 24 * time.Hour
	return cfg
}

// wiredPipeline returns a pipeline with all three model stages on in-process
// fakes, plus handles to the two that cost tokens.
func wiredPipeline(t *testing.T) (*Pipeline, *fakeScraper, *fakeAnalyzer, *fakeDrafter) {
	t.Helper()
	cfg := pipeConfig()

	searcher := &fakeScraper{}
	analyzer := &fakeAnalyzer{}
	drafter := &fakeDrafter{}

	cat := New(cfg, &fakeClassifier{}, newFakeStore())
	gaps := NewGapAnalyzer(cfg, analyzer, newFakeGapStore())
	messages := NewMessageRunner(cfg, drafter, newFakeMessageStore())

	p := NewPipeline(cfg, regionsearch.Standard(regionsearch.Sources{Sidecar: searcher}), &fakeRegionStore{}, cat, gaps, messages)
	return p, searcher, analyzer, drafter
}

func hasNoteContaining(notes []string, sub string) bool {
	for _, n := range notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

// --- tests ---------------------------------------------------------------

func TestPipeline_FullRun(t *testing.T) {
	p, searcher, analyzer, drafter := wiredPipeline(t)
	searcher.result = []maps.Company{
		pipeCompany("p1", "dentist"),
		pipeCompany("p2", "dental_clinic"),
		pipeCompany("p3", "dentist"),
	}

	rep, err := p.Run(context.Background(), RunRequest{
		Query:      maps.Query{Text: "dentists in Kadikoy"},
		Region:     "Kadikoy",
		WithEmails: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !rep.RanCategorize || !rep.RanGapAnalysis || !rep.RanEmails {
		t.Fatalf("all stages should have run: %+v", rep)
	}
	if len(rep.Companies) != 3 {
		t.Fatalf("want 3 companies, got %d", len(rep.Companies))
	}
	for _, l := range rep.Companies {
		if l.Category != CategoryHealth {
			t.Fatalf("company %s categorized %q, want health", l.PlaceID, l.Category)
		}
		mail := l.draftFor(settings.ChannelEmail)
		if mail == nil || mail.Body == "" || mail.Status == "" {
			t.Fatalf("company %s has no email draft: %+v", l.PlaceID, l)
		}
		if l.draftFor(settings.ChannelWhatsApp) != nil {
			t.Fatalf("a search must not draft WhatsApp: %+v", l.Drafts)
		}
	}
	if len(rep.Categories) != 1 || rep.Categories[0].Category != CategoryHealth {
		t.Fatalf("want one health category report, got %+v", rep.Categories)
	}
	if rep.Categories[0].GapAnalysis == "" {
		t.Fatal("health category should carry a gap analysis")
	}
	if analyzer.calls != 1 {
		t.Fatalf("gap analyzer called %d times, want 1 (one per category)", analyzer.calls)
	}
	if drafter.calls != 3 {
		t.Fatalf("email drafter called %d times, want 3 (one per company)", drafter.calls)
	}
}

func TestPipeline_RegionCacheHitSkipsSearch(t *testing.T) {
	p, searcher, _, _ := wiredPipeline(t)
	rs := &fakeRegionStore{getHit: []maps.Company{pipeCompany("p1", "dentist")}, getOK: true}
	p.regionStore = rs

	rep, err := p.Run(context.Background(), RunRequest{Query: maps.Query{Text: "x"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rep.FromCache {
		t.Error("FromCache should be true on a region cache hit")
	}
	if searcher.calls != 0 {
		t.Errorf("searcher called %d times on a cache hit, want 0", searcher.calls)
	}
	if rs.putCalls != 0 {
		t.Errorf("PutRegionSearch called %d times on a cache hit, want 0", rs.putCalls)
	}
}

// The free source answers and the billed one is never touched. This is the
// whole point of the order: a machine with a Places key still spends nothing on
// a region search that the scrape can serve.
func TestPipeline_FreeSourceAnswersAndPlacesIsNotCalled(t *testing.T) {
	cfg := pipeConfig()
	scraper := &fakeScraper{result: []maps.Company{pipeCompany("p1", "restaurant")}}
	searcher := &fakeSearcher{result: []maps.Company{pipeCompany("p2", "dentist")}}

	p := NewPipeline(cfg, regionsearch.Standard(regionsearch.Sources{Sidecar: scraper, Places: searcher}), &fakeRegionStore{},
		New(cfg, &fakeClassifier{}, newFakeStore()), nil, nil)

	rep, err := p.Run(context.Background(), RunRequest{Query: maps.Query{Text: "x"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.Companies) != 1 || rep.Companies[0].Category != CategoryRestaurant {
		t.Fatalf("expected the scraped restaurant, got %+v", rep.Companies)
	}
	if searcher.calls != 0 {
		t.Errorf("the billed source was called %d times, want 0", searcher.calls)
	}
	if !hasNoteContaining(rep.Notes, "answered by "+maps.SourceScrape) {
		t.Errorf("expected a provenance note, got %v", rep.Notes)
	}
}

// And the reverse: the scrape is down, so the key earns its keep.
func TestPipeline_FallsBackToPlacesWhenTheScrapeFails(t *testing.T) {
	cfg := pipeConfig()
	scraper := &fakeScraper{err: errors.New("sidecar unavailable")}
	searcher := &fakeSearcher{result: []maps.Company{pipeCompany("p1", "restaurant")}}

	p := NewPipeline(cfg, regionsearch.Standard(regionsearch.Sources{Sidecar: scraper, Places: searcher}), &fakeRegionStore{},
		New(cfg, &fakeClassifier{}, newFakeStore()), nil, nil)

	rep, err := p.Run(context.Background(), RunRequest{Query: maps.Query{Text: "x"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.Companies) != 1 {
		t.Fatalf("expected the billed result, got %+v", rep.Companies)
	}
	if searcher.calls != 1 {
		t.Errorf("places calls = %d, want 1", searcher.calls)
	}
	if !hasNoteContaining(rep.Notes, "billed") {
		t.Errorf("a billed answer must say so, got %v", rep.Notes)
	}
}

func TestPipeline_NoDataWhenEverySourceFails(t *testing.T) {
	cfg := pipeConfig()
	scraper := &fakeScraper{err: errors.New("sidecar down")}
	searcher := &fakeSearcher{err: errors.New("places down")}

	p := NewPipeline(cfg, regionsearch.Standard(regionsearch.Sources{Sidecar: scraper, Places: searcher}), nil, nil, nil, nil)

	_, err := p.Run(context.Background(), RunRequest{Query: maps.Query{Text: "x"}})
	if !errors.Is(err, ErrNoData) {
		t.Fatalf("want ErrNoData, got %v", err)
	}
}

func TestPipeline_StagesAreOptIn(t *testing.T) {
	p, searcher, analyzer, drafter := wiredPipeline(t)
	searcher.result = []maps.Company{pipeCompany("p1", "dentist")}

	rep, err := p.Run(context.Background(), RunRequest{Query: maps.Query{Text: "x"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.RanGapAnalysis || rep.RanEmails {
		t.Fatalf("stages 3-4 should not run without opt-in: %+v", rep)
	}
	if analyzer.calls != 0 || drafter.calls != 0 {
		t.Fatalf("no model calls expected: gaps=%d emails=%d", analyzer.calls, drafter.calls)
	}
	if len(rep.Categories) != 1 || rep.Categories[0].CompanyCount != 1 {
		t.Fatalf("category counts should still be reported: %+v", rep.Categories)
	}
}

func TestPipeline_UnknownCategoryGetsNoGapAnalysis(t *testing.T) {
	p, searcher, analyzer, _ := wiredPipeline(t)
	searcher.result = []maps.Company{
		{PlaceID: "p1", Name: "Mystery 1", Types: []string{"establishment"}},
		{PlaceID: "p2", Name: "Mystery 2", Types: []string{"establishment"}},
		{PlaceID: "p3", Name: "Mystery 3", Types: []string{"establishment"}},
	}
	// Leave the model tier unable to classify: everything stays unknown.
	p.categorizer.classifier.(*fakeClassifier).answer = func(refine.ClassifyInput) (refine.ClassifyOutput, error) {
		return refine.ClassifyOutput{Assignments: map[string]string{}}, nil
	}

	rep, err := p.Run(context.Background(), RunRequest{Query: maps.Query{Text: "x"}, WithGapAnalysis: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rep.RanGapAnalysis {
		t.Fatal("RanGapAnalysis should be true — it was requested")
	}
	if analyzer.calls != 0 {
		t.Fatalf("gap analyzer called %d times for unknown-only companies, want 0", analyzer.calls)
	}
	if len(rep.Categories) != 1 || rep.Categories[0].Category != CategoryUnknown {
		t.Fatalf("want one unknown category report, got %+v", rep.Categories)
	}
	if rep.Categories[0].GapAnalysis != "" {
		t.Fatal("unknown category must carry no gap analysis")
	}
}

func TestPipeline_CancelledContext(t *testing.T) {
	p, searcher, _, _ := wiredPipeline(t)
	searcher.result = []maps.Company{pipeCompany("p1", "dentist")}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := p.Run(ctx, RunRequest{Query: maps.Query{Text: "x"}, WithEmails: true}); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

// --- the ledger ------------------------------------------------------------

type fakeLedger struct {
	mu    sync.Mutex
	runs  []store.LeadRun
	rows  [][]store.LeadRow
	err   error
	calls int
}

func (f *fakeLedger) PutLeadRun(_ context.Context, run store.LeadRun, rows []store.LeadRow) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return f.err
	}
	f.runs = append(f.runs, run)
	f.rows = append(f.rows, rows)
	return nil
}

func TestPipeline_RecordsTheRunInTheLedger(t *testing.T) {
	p, searcher, _, _ := wiredPipeline(t)
	ledger := &fakeLedger{}
	p.UseLedger(ledger)

	searcher.result = []maps.Company{pipeCompany("p1", "dentist"), pipeCompany("p2", "hair_salon")}

	rep, err := p.Run(context.Background(), RunRequest{
		Query:  maps.Query{Text: "Kadikoy dentist"},
		Region: "Kadikoy",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ledger.calls != 1 {
		t.Fatalf("want one ledger write, got %d", ledger.calls)
	}
	if got := len(ledger.rows[0]); got != 2 {
		t.Fatalf("want 2 recorded companies, got %d", got)
	}
	if ledger.runs[0].Query != "Kadikoy dentist" || ledger.runs[0].RegionLabel != "Kadikoy" {
		t.Fatalf("run not described: %+v", ledger.runs[0])
	}
	if ledger.runs[0].ID == "" {
		t.Error("a run must carry an id")
	}
	if ledger.rows[0][0].Category != string(rep.Companies[0].Category) {
		t.Error("the recorded category must be the one the report shows")
	}
}

// The ledger is a record, not a stage: failing to write it costs a note, not
// the answer (SD-6).
func TestPipeline_LedgerFailureIsANoteNotAnError(t *testing.T) {
	p, searcher, _, _ := wiredPipeline(t)
	p.UseLedger(&fakeLedger{err: errors.New("disk on fire")})

	searcher.result = []maps.Company{pipeCompany("p1", "dentist")}

	rep, err := p.Run(context.Background(), RunRequest{Query: maps.Query{Text: "Kadikoy dentist"}})
	if err != nil {
		t.Fatalf("a ledger failure must not fail the run: %v", err)
	}
	if len(rep.Companies) != 1 {
		t.Fatalf("the run must still answer, got %d companies", len(rep.Companies))
	}
	if !hasNoteContaining(rep.Notes, "lead defteri") {
		t.Errorf("want a ledger note, got %v", rep.Notes)
	}
}

// The ledger is keyed by place_id, exactly as the outreach drafts are.
func TestPipeline_LedgerSkipsCompaniesWithoutAPlaceID(t *testing.T) {
	p, searcher, _, _ := wiredPipeline(t)
	ledger := &fakeLedger{}
	p.UseLedger(ledger)

	anon := pipeCompany("", "dentist")
	searcher.result = []maps.Company{pipeCompany("p1", "dentist"), anon}

	if _, err := p.Run(context.Background(), RunRequest{Query: maps.Query{Text: "Kadikoy dentist"}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(ledger.rows[0]); got != 1 {
		t.Fatalf("want only the identified company recorded, got %d", got)
	}
}

// A pipeline with no ledger is the pipeline as it was before the ledger.
func TestPipeline_NoLedgerStillRuns(t *testing.T) {
	p, searcher, _, _ := wiredPipeline(t)
	searcher.result = []maps.Company{pipeCompany("p1", "dentist")}

	rep, err := p.Run(context.Background(), RunRequest{Query: maps.Query{Text: "Kadikoy dentist"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.Companies) != 1 {
		t.Fatalf("want 1 company, got %d", len(rep.Companies))
	}
	if hasNoteContaining(rep.Notes, "lead defteri") {
		t.Errorf("no ledger means no ledger note, got %v", rep.Notes)
	}
}
