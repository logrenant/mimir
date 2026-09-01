package leadgen

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/logrenant/goat-mcp/internal/maps"
	"github.com/logrenant/goat-mcp/internal/refine"
	"github.com/logrenant/goat-mcp/internal/store"
)

// The runtime types must satisfy what stage 3 asks of them.
var (
	_ GapAnalyzer = (*refine.Client)(nil)
	_ GapStore    = (*store.Store)(nil)
)

// fakeAnalyzer records every call and returns a canned answer.
type fakeAnalyzer struct {
	mu     sync.Mutex
	calls  int
	inputs []refine.GapInput

	answer func(in refine.GapInput) (refine.Output, error)
}

func (f *fakeAnalyzer) AnalyzeGaps(_ context.Context, in refine.GapInput) (refine.Output, error) {
	f.mu.Lock()
	f.calls++
	f.inputs = append(f.inputs, in)
	answer := f.answer
	f.mu.Unlock()

	if answer != nil {
		return answer(in)
	}
	return refine.Output{Text: "- common gap one\n- common gap two", Refined: true}, nil
}

// panicAnalyzer fails the test if the model tier is reached at all.
type panicAnalyzer struct{ t *testing.T }

func (p panicAnalyzer) AnalyzeGaps(context.Context, refine.GapInput) (refine.Output, error) {
	p.t.Helper()
	p.t.Fatal("the model tier was invoked when a cache hit or a guard should have answered")
	return refine.Output{}, nil
}

type fakeGapStore struct {
	mu     sync.Mutex
	rows   map[string]store.GapAnalysis
	getErr error
	putErr error
	writes int
}

func newFakeGapStore() *fakeGapStore {
	return &fakeGapStore{rows: map[string]store.GapAnalysis{}}
}

func gapRowKey(k store.GapAnalysisKey) string {
	return strings.Join([]string{k.Region, k.Category, k.PromptVersion, k.CompanySetHash}, "|")
}

func (f *fakeGapStore) GetGapAnalysis(_ context.Context, key store.GapAnalysisKey) (store.GapAnalysis, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return store.GapAnalysis{}, false, f.getErr
	}
	ga, ok := f.rows[gapRowKey(key)]
	return ga, ok, nil
}

func (f *fakeGapStore) PutGapAnalysis(_ context.Context, key store.GapAnalysisKey, ga store.GapAnalysis) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.putErr != nil {
		return f.putErr
	}
	f.writes++
	f.rows[gapRowKey(key)] = ga
	return nil
}

func gapCompanies(n int) []maps.Company {
	out := make([]maps.Company, 0, n)
	for i := range n {
		id := string(rune('a' + i))
		out = append(out, maps.Company{
			PlaceID: "place-" + id,
			Name:    "Company " + id,
			Website: "",
			Phone:   "+90 555 000 0000",
			Rating:  4.0,
		})
	}
	return out
}

func TestAnalyzeCategory_ModelThenCache(t *testing.T) {
	ctx := context.Background()
	an := &fakeAnalyzer{}
	s := newFakeGapStore()
	r := NewGapAnalyzer(testConfig(), an, s)

	cs := gapCompanies(5)

	res, gaps, err := r.AnalyzeCategory(ctx, "Kadikoy", CategoryBeauty, cs)
	if err != nil {
		t.Fatalf("AnalyzeCategory: %v", err)
	}
	if len(gaps) != 0 {
		t.Fatalf("gaps = %v, want none", gaps)
	}
	if res.Method != MethodModel {
		t.Fatalf("method = %q, want model", res.Method)
	}
	if res.CompanyCount != 5 || res.Analysis == "" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if s.writes != 1 {
		t.Fatalf("cache writes = %d, want 1", s.writes)
	}

	// Second call, same set, different order: a cache hit, no second model call.
	shuffled := make([]maps.Company, 0, len(cs))
	shuffled = append(shuffled, cs[2:]...)
	shuffled = append(shuffled, cs[:2]...)
	res2, gaps2, err := r.AnalyzeCategory(ctx, "Kadikoy", CategoryBeauty, shuffled)
	if err != nil {
		t.Fatalf("AnalyzeCategory (2): %v", err)
	}
	if len(gaps2) != 0 {
		t.Fatalf("gaps = %v, want none", gaps2)
	}
	if res2.Method != MethodCache {
		t.Fatalf("method = %q, want cache on the repeat run", res2.Method)
	}
	if an.calls != 1 {
		t.Fatalf("analyzer calls = %d, want 1 — the second run must be free", an.calls)
	}
	if res2.Analysis != res.Analysis {
		t.Fatalf("cache replayed different text:\n%q\n%q", res2.Analysis, res.Analysis)
	}
}

// A category set below the floor is diagnostic, not a subprocess.
func TestAnalyzeCategory_TooFewCompaniesSkipsModel(t *testing.T) {
	r := NewGapAnalyzer(testConfig(), panicAnalyzer{t}, newFakeGapStore())

	res, gaps, err := r.AnalyzeCategory(context.Background(), "Kadikoy", CategoryBeauty, gapCompanies(2))
	if err != nil {
		t.Fatalf("AnalyzeCategory: %v", err)
	}
	if res.Method != MethodUnresolved || res.Analysis != "" {
		t.Fatalf("want an unresolved empty result, got %+v", res)
	}
	if len(gaps) != 1 || !strings.Contains(gaps[0], "need at least") {
		t.Fatalf("want one 'need at least' gap, got %v", gaps)
	}
}

// A different set of companies is a different analysis: the hash is in the key.
func TestAnalyzeCategory_CompanySetChangeMissesCache(t *testing.T) {
	ctx := context.Background()
	an := &fakeAnalyzer{}
	r := NewGapAnalyzer(testConfig(), an, newFakeGapStore())

	if _, _, err := r.AnalyzeCategory(ctx, "Kadikoy", CategoryBeauty, gapCompanies(4)); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, _, err := r.AnalyzeCategory(ctx, "Kadikoy", CategoryBeauty, gapCompanies(5)); err != nil {
		t.Fatalf("second: %v", err)
	}
	if an.calls != 2 {
		t.Fatalf("analyzer calls = %d, want 2 — a changed company set must re-synthesize", an.calls)
	}
}

// SD-6: a failing cache read degrades to a model call plus a gap, never an error.
func TestAnalyzeCategory_CacheReadFailureDegrades(t *testing.T) {
	an := &fakeAnalyzer{}
	s := newFakeGapStore()
	s.getErr = errors.New("disk gone")
	r := NewGapAnalyzer(testConfig(), an, s)

	res, gaps, err := r.AnalyzeCategory(context.Background(), "Kadikoy", CategoryBeauty, gapCompanies(4))
	if err != nil {
		t.Fatalf("AnalyzeCategory: %v", err)
	}
	if res.Method != MethodModel {
		t.Fatalf("method = %q, want model after a cache miss-by-error", res.Method)
	}
	if len(gaps) != 1 || !strings.Contains(gaps[0], "cache unavailable") {
		t.Fatalf("want one 'cache unavailable' gap, got %v", gaps)
	}
}

// SD-6: a failing model call degrades to an unresolved result plus a gap.
func TestAnalyzeCategory_ModelFailureDegrades(t *testing.T) {
	an := &fakeAnalyzer{answer: func(refine.GapInput) (refine.Output, error) {
		return refine.Output{}, errors.New("claude unavailable")
	}}
	r := NewGapAnalyzer(testConfig(), an, newFakeGapStore())

	res, gaps, err := r.AnalyzeCategory(context.Background(), "Kadikoy", CategoryBeauty, gapCompanies(4))
	if err != nil {
		t.Fatalf("AnalyzeCategory: %v", err)
	}
	if res.Method != MethodUnresolved || res.Analysis != "" {
		t.Fatalf("want an unresolved result, got %+v", res)
	}
	if len(gaps) != 1 || !strings.Contains(gaps[0], "failed") {
		t.Fatalf("want one 'failed' gap, got %v", gaps)
	}
}

// An unrefined or empty model answer is not cached and not returned as content.
func TestAnalyzeCategory_UnusableModelAnswerNotCached(t *testing.T) {
	an := &fakeAnalyzer{answer: func(refine.GapInput) (refine.Output, error) {
		return refine.Output{Text: "", Refined: false}, nil
	}}
	s := newFakeGapStore()
	r := NewGapAnalyzer(testConfig(), an, s)

	res, gaps, err := r.AnalyzeCategory(context.Background(), "Kadikoy", CategoryBeauty, gapCompanies(4))
	if err != nil {
		t.Fatalf("AnalyzeCategory: %v", err)
	}
	if res.Method != MethodUnresolved {
		t.Fatalf("method = %q, want unresolved", res.Method)
	}
	if s.writes != 0 {
		t.Fatalf("cache writes = %d, want 0 — an unusable answer must not be stored", s.writes)
	}
	if len(gaps) != 1 {
		t.Fatalf("want one gap, got %v", gaps)
	}
}

// A cancelled caller is the one failure that surfaces as an error.
func TestAnalyzeCategory_CancelledContextReturnsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r := NewGapAnalyzer(testConfig(), &fakeAnalyzer{}, newFakeGapStore())
	_, _, err := r.AnalyzeCategory(ctx, "Kadikoy", CategoryBeauty, gapCompanies(4))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

// The facts handed to the model are computed locally: has_website reflects the
// Website field, and no raw text is passed.
func TestAnalyzeCategory_FactsAreDerivedNotRaw(t *testing.T) {
	an := &fakeAnalyzer{}
	r := NewGapAnalyzer(testConfig(), an, newFakeGapStore())

	cs := gapCompanies(3)
	cs[0].Website = "https://example.com"
	cs[1].Phone = ""

	if _, _, err := r.AnalyzeCategory(context.Background(), "Kadikoy", CategoryBeauty, cs); err != nil {
		t.Fatalf("AnalyzeCategory: %v", err)
	}
	if len(an.inputs) != 1 {
		t.Fatalf("want one analyzer call, got %d", len(an.inputs))
	}

	got := an.inputs[0]
	if got.Region != "Kadikoy" || got.Category != string(CategoryBeauty) {
		t.Fatalf("region/category not threaded: %+v", got)
	}
	if len(got.Companies) != 3 {
		t.Fatalf("want 3 fact rows, got %d", len(got.Companies))
	}

	// Companies are sorted by place_id, so index maps back to cs order here.
	byName := map[string]refine.GapCompany{}
	for _, f := range got.Companies {
		byName[f.Name] = f
	}
	if !byName["Company a"].HasWebsite {
		t.Error("Company a has a website; has_website should be true")
	}
	if byName["Company b"].HasPhone {
		t.Error("Company b has no phone; has_phone should be false")
	}
	if !byName["Company c"].HasPhone {
		t.Error("Company c has a phone; has_phone should be true")
	}
}
