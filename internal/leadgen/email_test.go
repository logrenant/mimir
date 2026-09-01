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

var (
	_ EmailDrafter = (*refine.Client)(nil)
	_ EmailStore   = (*store.Store)(nil)
)

type fakeDrafter struct {
	mu     sync.Mutex
	calls  int
	inputs []refine.EmailInput

	answer func(in refine.EmailInput) (refine.Output, error)
}

func (f *fakeDrafter) DraftEmail(_ context.Context, in refine.EmailInput) (refine.Output, error) {
	f.mu.Lock()
	f.calls++
	f.inputs = append(f.inputs, in)
	answer := f.answer
	f.mu.Unlock()

	if answer != nil {
		return answer(in)
	}
	return refine.Output{Text: "Hi " + in.BusinessName + ", let's talk.", Refined: true}, nil
}

type panicDrafter struct{ t *testing.T }

func (p panicDrafter) DraftEmail(context.Context, refine.EmailInput) (refine.Output, error) {
	p.t.Helper()
	p.t.Fatal("the model tier was invoked when a cache hit or a guard should have answered")
	return refine.Output{}, nil
}

type fakeEmailStore struct {
	mu     sync.Mutex
	rows   map[string]store.OutreachEmail
	getErr error
	putErr error
	writes int
}

func newFakeEmailStore() *fakeEmailStore {
	return &fakeEmailStore{rows: map[string]store.OutreachEmail{}}
}

func (f *fakeEmailStore) key(placeID, version string) string { return placeID + "|" + version }

func (f *fakeEmailStore) GetOutreachEmail(_ context.Context, placeID, version string) (store.OutreachEmail, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return store.OutreachEmail{}, false, f.getErr
	}
	oe, ok := f.rows[f.key(placeID, version)]
	return oe, ok, nil
}

func (f *fakeEmailStore) PutOutreachEmail(_ context.Context, placeID, version, email string, truncated bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.putErr != nil {
		return f.putErr
	}
	// Mirror the real store: a sent/skipped row is not overwritten by a re-run.
	if existing, ok := f.rows[f.key(placeID, version)]; ok && existing.Status != store.EmailStatusDraft {
		return nil
	}
	f.writes++
	f.rows[f.key(placeID, version)] = store.OutreachEmail{Email: email, Status: store.EmailStatusDraft, Truncated: truncated}
	return nil
}

func (f *fakeEmailStore) seed(placeID, version string, oe store.OutreachEmail) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[f.key(placeID, version)] = oe
}

func emailCompany(id string) maps.Company {
	return maps.Company{
		PlaceID:          id,
		Name:             "Company " + id,
		FormattedAddress: "1 Test Street, Kadikoy",
		Website:          "",
		Rating:           4.2,
		ReviewCount:      33,
	}
}

const sampleGap = "- Few have a website\n- Thin review counts"

func TestDraftFor_ModelThenCache(t *testing.T) {
	ctx := context.Background()
	d := &fakeDrafter{}
	s := newFakeEmailStore()
	r := NewEmailRunner(testConfig(), d, s)

	res, gaps, err := r.DraftFor(ctx, emailCompany("p1"), CategoryBeauty, sampleGap)
	if err != nil {
		t.Fatalf("DraftFor: %v", err)
	}
	if len(gaps) != 0 {
		t.Fatalf("gaps = %v, want none", gaps)
	}
	if res.Method != MethodModel || res.Status != store.EmailStatusDraft || res.Email == "" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if s.writes != 1 {
		t.Fatalf("cache writes = %d, want 1", s.writes)
	}

	res2, _, err := r.DraftFor(ctx, emailCompany("p1"), CategoryBeauty, sampleGap)
	if err != nil {
		t.Fatalf("DraftFor (2): %v", err)
	}
	if res2.Method != MethodCache {
		t.Fatalf("method = %q, want cache on the repeat run", res2.Method)
	}
	if d.calls != 1 {
		t.Fatalf("drafter calls = %d, want 1 — the second run must be free", d.calls)
	}
}

// A sent email is replayed from the cache and never regenerated.
func TestDraftFor_SentEmailNotRegenerated(t *testing.T) {
	s := newFakeEmailStore()
	s.seed("p1", testConfig().LeadgenEmailVersion, store.OutreachEmail{
		Email:  "the email a human already sent",
		Status: store.EmailStatusSent,
	})

	r := NewEmailRunner(testConfig(), panicDrafter{t}, s)

	res, gaps, err := r.DraftFor(context.Background(), emailCompany("p1"), CategoryBeauty, sampleGap)
	if err != nil {
		t.Fatalf("DraftFor: %v", err)
	}
	if len(gaps) != 0 {
		t.Fatalf("gaps = %v, want none", gaps)
	}
	if res.Method != MethodCache || res.Status != store.EmailStatusSent {
		t.Fatalf("a sent email should replay as-is: %+v", res)
	}
	if res.Email != "the email a human already sent" {
		t.Fatalf("body changed: %q", res.Email)
	}
}

func TestDraftFor_NoPlaceIDIsSkipped(t *testing.T) {
	r := NewEmailRunner(testConfig(), panicDrafter{t}, newFakeEmailStore())

	c := emailCompany("")
	res, gaps, err := r.DraftFor(context.Background(), c, CategoryBeauty, sampleGap)
	if err != nil {
		t.Fatalf("DraftFor: %v", err)
	}
	if res.Method != MethodUnresolved || res.Email != "" {
		t.Fatalf("want an unresolved empty result, got %+v", res)
	}
	if len(gaps) != 1 || !strings.Contains(gaps[0], "no place_id") {
		t.Fatalf("want one 'no place_id' gap, got %v", gaps)
	}
}

func TestDraftFor_EmptyGapAnalysisIsSkipped(t *testing.T) {
	r := NewEmailRunner(testConfig(), panicDrafter{t}, newFakeEmailStore())

	res, gaps, err := r.DraftFor(context.Background(), emailCompany("p1"), CategoryBeauty, "  ")
	if err != nil {
		t.Fatalf("DraftFor: %v", err)
	}
	if res.Method != MethodUnresolved {
		t.Fatalf("want unresolved, got %+v", res)
	}
	if len(gaps) != 1 || !strings.Contains(gaps[0], "no gap analysis") {
		t.Fatalf("want one 'no gap analysis' gap, got %v", gaps)
	}
}

func TestDraftFor_CacheReadFailureDegrades(t *testing.T) {
	d := &fakeDrafter{}
	s := newFakeEmailStore()
	s.getErr = errors.New("disk gone")
	r := NewEmailRunner(testConfig(), d, s)

	res, gaps, err := r.DraftFor(context.Background(), emailCompany("p1"), CategoryBeauty, sampleGap)
	if err != nil {
		t.Fatalf("DraftFor: %v", err)
	}
	if res.Method != MethodModel {
		t.Fatalf("method = %q, want model after a cache miss-by-error", res.Method)
	}
	if len(gaps) != 1 || !strings.Contains(gaps[0], "cache unavailable") {
		t.Fatalf("want one 'cache unavailable' gap, got %v", gaps)
	}
}

func TestDraftFor_ModelFailureDegrades(t *testing.T) {
	d := &fakeDrafter{answer: func(refine.EmailInput) (refine.Output, error) {
		return refine.Output{}, errors.New("claude unavailable")
	}}
	r := NewEmailRunner(testConfig(), d, newFakeEmailStore())

	res, gaps, err := r.DraftFor(context.Background(), emailCompany("p1"), CategoryBeauty, sampleGap)
	if err != nil {
		t.Fatalf("DraftFor: %v", err)
	}
	if res.Method != MethodUnresolved || res.Email != "" {
		t.Fatalf("want an unresolved result, got %+v", res)
	}
	if len(gaps) != 1 || !strings.Contains(gaps[0], "failed") {
		t.Fatalf("want one 'failed' gap, got %v", gaps)
	}
}

func TestDraftFor_UnusableModelAnswerNotCached(t *testing.T) {
	d := &fakeDrafter{answer: func(refine.EmailInput) (refine.Output, error) {
		return refine.Output{Text: "", Refined: false}, nil
	}}
	s := newFakeEmailStore()
	r := NewEmailRunner(testConfig(), d, s)

	res, gaps, err := r.DraftFor(context.Background(), emailCompany("p1"), CategoryBeauty, sampleGap)
	if err != nil {
		t.Fatalf("DraftFor: %v", err)
	}
	if res.Method != MethodUnresolved {
		t.Fatalf("method = %q, want unresolved", res.Method)
	}
	if s.writes != 0 {
		t.Fatalf("cache writes = %d, want 0", s.writes)
	}
	if len(gaps) != 1 {
		t.Fatalf("want one gap, got %v", gaps)
	}
}

func TestDraftFor_CancelledContextReturnsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r := NewEmailRunner(testConfig(), &fakeDrafter{}, newFakeEmailStore())
	if _, _, err := r.DraftFor(ctx, emailCompany("p1"), CategoryBeauty, sampleGap); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

// The drafter is fed the company's own facts and the category gap analysis —
// derived values, no raw page text.
func TestDraftFor_InputIsDerived(t *testing.T) {
	d := &fakeDrafter{}
	r := NewEmailRunner(testConfig(), d, newFakeEmailStore())

	c := emailCompany("p1")
	c.Website = "https://example.com"

	if _, _, err := r.DraftFor(context.Background(), c, CategoryBeauty, sampleGap); err != nil {
		t.Fatalf("DraftFor: %v", err)
	}
	if len(d.inputs) != 1 {
		t.Fatalf("want one drafter call, got %d", len(d.inputs))
	}
	got := d.inputs[0]
	if got.BusinessName != "Company p1" || got.Category != string(CategoryBeauty) {
		t.Fatalf("company/category not threaded: %+v", got)
	}
	if !got.HasWebsite {
		t.Error("HasWebsite should reflect the Website field")
	}
	if got.GapAnalysis != sampleGap {
		t.Errorf("gap analysis not passed through: %q", got.GapAnalysis)
	}
}
