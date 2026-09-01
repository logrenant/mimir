package leadgen

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/maps"
	"github.com/logrenant/goat-mcp/internal/refine"
	"github.com/logrenant/goat-mcp/internal/store"
)

// The runtime types must actually satisfy what this package asks of them.
var (
	_ Classifier = (*refine.Client)(nil)
	_ Store      = (*store.Store)(nil)
)

func testConfig() config.Config {
	return config.Config{
		LeadgenCategoryVersion:   "leadgen-test",
		LeadgenBatchSize:         2,
		LeadgenClassifyMaxTokens: 800,
		MaxConcurrentRefines:     2,
		LeadgenGapVersion:        "gap-test",
		LeadgenGapMaxTokens:      700,
		LeadgenGapMinCompanies:   3,
		LeadgenEmailVersion:      "email-test",
		LeadgenEmailMaxTokens:    600,
	}
}

// fakeClassifier records every batch it is asked to classify.
type fakeClassifier struct {
	mu         sync.Mutex
	batchSizes []int
	calls      int

	answer func(in refine.ClassifyInput) (refine.ClassifyOutput, error)
}

func (f *fakeClassifier) Classify(_ context.Context, in refine.ClassifyInput) (refine.ClassifyOutput, error) {
	f.mu.Lock()
	f.calls++
	f.batchSizes = append(f.batchSizes, len(in.Items))
	answer := f.answer
	f.mu.Unlock()

	if answer != nil {
		return answer(in)
	}
	out := refine.ClassifyOutput{Assignments: map[string]string{}}
	for _, item := range in.Items {
		out.Assignments[item.ID] = string(CategoryRetail)
	}
	return out, nil
}

// panicClassifier fails the test if the model tier is reached at all — the
// zero-token path is asserted by execution, not by counting afterwards.
type panicClassifier struct{ t *testing.T }

func (p panicClassifier) Classify(context.Context, refine.ClassifyInput) (refine.ClassifyOutput, error) {
	p.t.Helper()
	p.t.Fatal("the model tier was invoked for companies the cheaper tiers had already answered")
	return refine.ClassifyOutput{}, nil
}

type fakeStore struct {
	mu     sync.Mutex
	rows   map[string]string // placeID|version -> category
	method map[string]string
	getErr error
	putErr error
	writes int
}

func newFakeStore() *fakeStore {
	return &fakeStore{rows: map[string]string{}, method: map[string]string{}}
}

func (f *fakeStore) GetCategorizations(_ context.Context, placeIDs []string, version string) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	out := map[string]string{}
	for _, id := range placeIDs {
		if cat, ok := f.rows[id+"|"+version]; ok {
			out[id] = cat
		}
	}
	return out, nil
}

func (f *fakeStore) PutCategorization(_ context.Context, placeID, version, category, method string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.putErr != nil {
		return f.putErr
	}
	f.writes++
	f.rows[placeID+"|"+version] = category
	f.method[placeID] = method
	return nil
}

func company(id, primaryType string, types ...string) maps.Company {
	return maps.Company{PlaceID: id, Name: "Company " + id, PrimaryType: primaryType, Types: types}
}

func TestCategorize_RuleTierCostsNothing(t *testing.T) {
	s := newFakeStore()
	c := New(testConfig(), panicClassifier{t}, s)

	got, gaps, err := c.Categorize(context.Background(), []maps.Company{
		company("p1", "dentist"),
		company("p2", "", "establishment", "hair_care"),
		company("p3", "car_repair"),
	})
	if err != nil {
		t.Fatalf("Categorize: %v", err)
	}
	if len(gaps) != 0 {
		t.Errorf("gaps = %v, want none", gaps)
	}

	want := []Category{CategoryHealth, CategoryBeauty, CategoryAutomotive}
	for i, res := range got {
		if res.Category != want[i] || res.Method != MethodRule {
			t.Errorf("result %d = %+v, want %s/rule", i, res, want[i])
		}
	}
	if s.writes != 3 {
		t.Errorf("cache writes = %d, want 3", s.writes)
	}
}

// The same claim as above, one level lower: with a real *refine.Client wired to
// a fake `claude`, a rule-answerable region must not spawn the subprocess at
// all. The stub classifier proves the call is not made; this proves the wiring
// between the two packages does not make it anyway.
func TestCategorize_RuleTierSpawnsNoSubprocess(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "was-run")
	cli := filepath.Join(dir, "fake-claude.sh")
	script := "#!/bin/sh\ntouch " + marker + "\nexit 1\n"
	if err := os.WriteFile(cli, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig()
	cfg.ClaudeCLIPath = cli
	cfg.ClaudeModel = "claude-haiku-4-5-20251001"
	cfg.RefineTimeout = 5 * time.Second

	c := New(cfg, refine.New(cfg), newFakeStore())

	got, gaps, err := c.Categorize(context.Background(), []maps.Company{
		company("p1", "dentist"),
		company("p2", "restaurant"),
	})
	if err != nil {
		t.Fatalf("Categorize: %v", err)
	}
	if len(gaps) != 0 {
		t.Errorf("gaps = %v, want none", gaps)
	}
	if got[0].Category != CategoryHealth || got[1].Category != CategoryRestaurant {
		t.Errorf("results = %+v, want health and restaurant from the rule table", got)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the `claude` CLI was executed for companies the rule table answered")
	}
}

func TestCategorize_CachedCompaniesSkipEveryTier(t *testing.T) {
	s := newFakeStore()
	s.rows["p1|leadgen-test"] = string(CategoryEducation)
	c := New(testConfig(), panicClassifier{t}, s)

	got, _, err := c.Categorize(context.Background(), []maps.Company{company("p1", "dentist")})
	if err != nil {
		t.Fatalf("Categorize: %v", err)
	}
	// The cached answer wins over the rule table on purpose: a re-categorized
	// region must be reproducible, not quietly re-derived.
	if got[0].Category != CategoryEducation || got[0].Method != MethodCache {
		t.Errorf("result = %+v, want education/cache", got[0])
	}
	if s.writes != 0 {
		t.Errorf("cache writes = %d, want 0 on a full hit", s.writes)
	}
}

func TestCategorize_OnlyResidualReachesTheModel(t *testing.T) {
	s := newFakeStore()
	fc := &fakeClassifier{}
	c := New(testConfig(), fc, s) // LeadgenBatchSize = 2

	companies := []maps.Company{
		company("p1", "dentist"),                 // rule
		company("p2", "", "establishment"),       // residual
		company("p3", "", "point_of_interest"),   // residual
		company("p4", "hair_care"),               // rule
		company("p5", "", "establishment"),       // residual
		company("p6", "interdimensional_portal"), // residual
		company("p7", "", "unmapped_thing"),      // residual
	}

	got, gaps, err := c.Categorize(context.Background(), companies)
	if err != nil {
		t.Fatalf("Categorize: %v", err)
	}
	if len(gaps) != 0 {
		t.Errorf("gaps = %v, want none", gaps)
	}

	fc.mu.Lock()
	sizes := append([]int(nil), fc.batchSizes...)
	fc.mu.Unlock()

	total := 0
	for _, n := range sizes {
		if n > testConfig().LeadgenBatchSize {
			t.Errorf("batch of %d exceeds LeadgenBatchSize %d", n, testConfig().LeadgenBatchSize)
		}
		total += n
	}
	if total != 5 {
		t.Errorf("model saw %d companies, want the 5 residual ones", total)
	}

	if got[0].Method != MethodRule || got[3].Method != MethodRule {
		t.Error("rule-matched companies must not be re-answered by the model")
	}
	for _, i := range []int{1, 2, 4, 5, 6} {
		if got[i].Method != MethodModel || got[i].Category != CategoryRetail {
			t.Errorf("result %d = %+v, want retail/model", i, got[i])
		}
	}
	if s.method["p2"] != MethodModel {
		t.Errorf("model answers must be cached with their method, got %q", s.method["p2"])
	}
}

// An answer outside the vocabulary, or for an id we never sent, leaves the
// company unresolved — and crucially caches nothing, so one bad batch does not
// become a permanent fact for this taxonomy version.
func TestCategorize_UnusableAnswersCacheNothing(t *testing.T) {
	s := newFakeStore()
	fc := &fakeClassifier{answer: func(in refine.ClassifyInput) (refine.ClassifyOutput, error) {
		return refine.ClassifyOutput{Assignments: map[string]string{
			in.Items[0].ID: "crypto_startup",
			"p-never-sent": string(CategoryRetail),
		}}, nil
	}}
	c := New(testConfig(), fc, s)

	got, _, err := c.Categorize(context.Background(), []maps.Company{company("p1", "", "establishment")})
	if err != nil {
		t.Fatalf("Categorize: %v", err)
	}
	if got[0].Category != CategoryUnknown || got[0].Method != MethodUnresolved {
		t.Errorf("result = %+v, want unknown/unresolved", got[0])
	}
	if s.writes != 0 {
		t.Errorf("cache writes = %d, want 0 for an unusable answer", s.writes)
	}
}

// An explicit "unknown" is a real answer and is worth caching: re-asking would
// spend the same tokens for the same shrug.
func TestCategorize_ExplicitUnknownIsCached(t *testing.T) {
	s := newFakeStore()
	fc := &fakeClassifier{answer: func(in refine.ClassifyInput) (refine.ClassifyOutput, error) {
		return refine.ClassifyOutput{Assignments: map[string]string{
			in.Items[0].ID: string(CategoryUnknown),
		}}, nil
	}}
	c := New(testConfig(), fc, s)

	got, _, err := c.Categorize(context.Background(), []maps.Company{company("p1", "", "establishment")})
	if err != nil {
		t.Fatalf("Categorize: %v", err)
	}
	if got[0].Category != CategoryUnknown || got[0].Method != MethodModel {
		t.Errorf("result = %+v, want unknown/model", got[0])
	}
	if s.writes != 1 {
		t.Errorf("cache writes = %d, want 1", s.writes)
	}
}

func TestCategorize_ClassifierFailureIsAGapNotAnError(t *testing.T) {
	s := newFakeStore()
	fc := &fakeClassifier{answer: func(refine.ClassifyInput) (refine.ClassifyOutput, error) {
		return refine.ClassifyOutput{}, errors.New("claude CLI unavailable")
	}}
	c := New(testConfig(), fc, s)

	got, gaps, err := c.Categorize(context.Background(), []maps.Company{company("p1", "", "establishment")})
	if err != nil {
		t.Fatalf("Categorize returned an error for a degraded tier: %v", err)
	}
	if len(gaps) == 0 {
		t.Error("a failed batch must be reported as a gap")
	}
	if got[0].Category != CategoryUnknown || got[0].Method != MethodUnresolved {
		t.Errorf("result = %+v, want unknown/unresolved", got[0])
	}
	if s.writes != 0 {
		t.Errorf("cache writes = %d, want 0 after a subprocess failure", s.writes)
	}
}

// A broken cache is a slower run, never a failed one — but it is worth saying
// out loud, because every company then costs tokens.
func TestCategorize_CacheReadFailureDegrades(t *testing.T) {
	s := newFakeStore()
	s.getErr = errors.New("database is locked")
	c := New(testConfig(), panicClassifier{t}, s)

	got, gaps, err := c.Categorize(context.Background(), []maps.Company{company("p1", "dentist")})
	if err != nil {
		t.Fatalf("Categorize: %v", err)
	}
	if len(gaps) == 0 {
		t.Error("an unavailable cache must be reported as a gap")
	}
	if got[0].Category != CategoryHealth {
		t.Errorf("result = %+v, want the rule tier to still answer", got[0])
	}
}

// A cache that cannot be written is a gap too — the answer in hand is still
// correct, it just will not be free next time.
func TestCategorize_CacheWriteFailureDegrades(t *testing.T) {
	s := newFakeStore()
	s.putErr = errors.New("disk is full")
	c := New(testConfig(), panicClassifier{t}, s)

	got, gaps, err := c.Categorize(context.Background(), []maps.Company{company("p1", "dentist")})
	if err != nil {
		t.Fatalf("Categorize: %v", err)
	}
	if len(gaps) == 0 {
		t.Error("a failed cache write must be reported as a gap")
	}
	if got[0].Category != CategoryHealth || got[0].Method != MethodRule {
		t.Errorf("result = %+v, want health/rule regardless of the cache", got[0])
	}
}

func TestCategorize_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := New(testConfig(), &fakeClassifier{}, newFakeStore())
	if _, _, err := c.Categorize(ctx, []maps.Company{company("p1", "dentist")}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestCategorize_NilStoreAndNoClassifier(t *testing.T) {
	c := New(testConfig(), nil, nil)

	got, gaps, err := c.Categorize(context.Background(), []maps.Company{
		company("p1", "dentist"),
		company("p2", "", "establishment"),
	})
	if err != nil {
		t.Fatalf("Categorize: %v", err)
	}
	if got[0].Category != CategoryHealth || got[0].Method != MethodRule {
		t.Errorf("result 0 = %+v, want health/rule without a store", got[0])
	}
	if got[1].Method != MethodUnresolved {
		t.Errorf("result 1 = %+v, want unresolved without a classifier", got[1])
	}
	if len(gaps) == 0 {
		t.Error("companies left unresolved because no classifier is configured must be a gap")
	}
}

// A region can list the same business twice; it must be paid for once and
// still answered twice, in input order.
func TestCategorize_DuplicatesClassifiedOnce(t *testing.T) {
	fc := &fakeClassifier{}
	c := New(testConfig(), fc, newFakeStore())

	got, _, err := c.Categorize(context.Background(), []maps.Company{
		company("p1", "", "establishment"),
		company("p1", "", "establishment"),
	})
	if err != nil {
		t.Fatalf("Categorize: %v", err)
	}
	fc.mu.Lock()
	sizes := append([]int(nil), fc.batchSizes...)
	fc.mu.Unlock()

	if len(sizes) != 1 || sizes[0] != 1 {
		t.Errorf("batches = %v, want one batch of one", sizes)
	}
	if got[0].Category != CategoryRetail || got[1].Category != CategoryRetail {
		t.Errorf("both copies must get the answer, got %+v", got)
	}
}

// A company with no place_id can still be classified by the rule table; it just
// cannot be cached, and must never be sent as an item with an empty id.
func TestCategorize_CompanyWithoutPlaceID(t *testing.T) {
	s := newFakeStore()
	fc := &fakeClassifier{}
	c := New(testConfig(), fc, s)

	got, _, err := c.Categorize(context.Background(), []maps.Company{
		{Name: "No id, but a dentist", PrimaryType: "dentist"},
		{Name: "No id, no types"},
	})
	if err != nil {
		t.Fatalf("Categorize: %v", err)
	}
	if got[0].Category != CategoryHealth || got[0].Method != MethodRule {
		t.Errorf("result 0 = %+v, want health/rule", got[0])
	}
	if got[1].Method != MethodUnresolved {
		t.Errorf("result 1 = %+v, want unresolved", got[1])
	}
	if fc.calls != 0 {
		t.Error("a company with no id cannot be addressed in an answer; do not send it")
	}
	if s.writes != 0 {
		t.Errorf("cache writes = %d, want 0 without a place_id", s.writes)
	}
}

func TestCategorize_EmptyInput(t *testing.T) {
	c := New(testConfig(), panicClassifier{t}, newFakeStore())

	got, gaps, err := c.Categorize(context.Background(), nil)
	if err != nil || len(got) != 0 || len(gaps) != 0 {
		t.Errorf("got %v, %v, %v; want empty and no error", got, gaps, err)
	}
}

// Bounded fan-out over many batches, exercised under -race.
func TestCategorize_ManyBatches(t *testing.T) {
	fc := &fakeClassifier{}
	c := New(testConfig(), fc, newFakeStore())

	companies := make([]maps.Company, 0, 21)
	for i := range 21 {
		companies = append(companies, company("p"+strconv.Itoa(i), "", "establishment"))
	}

	got, gaps, err := c.Categorize(context.Background(), companies)
	if err != nil || len(gaps) != 0 {
		t.Fatalf("Categorize: %v, gaps %v", err, gaps)
	}
	for i, res := range got {
		if res.Category != CategoryRetail {
			t.Fatalf("result %d = %+v, want retail", i, res)
		}
	}
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if fc.calls != 11 { // 21 companies, batch size 2
		t.Errorf("calls = %d, want 11", fc.calls)
	}
}
