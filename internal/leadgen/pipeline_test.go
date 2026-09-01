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
	calls  int
	result []maps.Company
	err    error
}

func (f *fakeScraper) Search(_ context.Context, _ maps.Query) ([]maps.Company, error) {
	f.calls++
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
func wiredPipeline(t *testing.T) (*Pipeline, *fakeSearcher, *fakeAnalyzer, *fakeDrafter) {
	t.Helper()
	cfg := pipeConfig()

	searcher := &fakeSearcher{}
	analyzer := &fakeAnalyzer{}
	drafter := &fakeDrafter{}

	cat := New(cfg, &fakeClassifier{}, newFakeStore())
	gaps := NewGapAnalyzer(cfg, analyzer, newFakeGapStore())
	emails := NewEmailRunner(cfg, drafter, newFakeEmailStore())

	p := NewPipeline(cfg, searcher, nil, &fakeRegionStore{}, cat, gaps, emails)
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
		if l.Email == "" || l.EmailStatus == "" {
			t.Fatalf("company %s has no email: %+v", l.PlaceID, l)
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

func TestPipeline_ScrapeFallback(t *testing.T) {
	cfg := pipeConfig()
	searcher := &fakeSearcher{err: errors.New("places 503")}
	scraper := &fakeScraper{result: []maps.Company{pipeCompany("p1", "restaurant")}}

	p := NewPipeline(cfg, searcher, scraper, &fakeRegionStore{},
		New(cfg, &fakeClassifier{}, newFakeStore()), nil, nil)

	rep, err := p.Run(context.Background(), RunRequest{Query: maps.Query{Text: "x"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.Companies) != 1 || rep.Companies[0].Category != CategoryRestaurant {
		t.Fatalf("expected the scraped restaurant, got %+v", rep.Companies)
	}
	if scraper.calls != 1 {
		t.Errorf("scraper calls = %d, want 1", scraper.calls)
	}
	if !hasNoteContaining(rep.Notes, "scrape fallback") {
		t.Errorf("expected a fallback note, got %v", rep.Notes)
	}
}

func TestPipeline_NoDataWhenBothSourcesFail(t *testing.T) {
	cfg := pipeConfig()
	searcher := &fakeSearcher{err: errors.New("places down")}

	p := NewPipeline(cfg, searcher, nil, nil, nil, nil, nil)

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
