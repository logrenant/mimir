package leadgen

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/logrenant/mimir/internal/maps"
	"github.com/logrenant/mimir/internal/refine"
	"github.com/logrenant/mimir/internal/settings"
	"github.com/logrenant/mimir/internal/store"
)

var (
	_ MessageDrafter = (*refine.Client)(nil)
	_ MessageStore   = (*store.Store)(nil)
	_ RuleSource     = (*settings.Store)(nil)
)

type fakeDrafter struct {
	mu     sync.Mutex
	calls  int
	inputs []refine.MessageInput

	answer func(in refine.MessageInput) (refine.Output, error)
}

func (f *fakeDrafter) DraftMessage(_ context.Context, in refine.MessageInput) (refine.Output, error) {
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

func (p panicDrafter) DraftMessage(context.Context, refine.MessageInput) (refine.Output, error) {
	p.t.Helper()
	p.t.Fatal("the model tier was invoked when a cache hit or a guard should have answered")
	return refine.Output{}, nil
}

// fakeRules is a rule file that does not touch the filesystem. The version is
// the caller's, so a test can say "the operator edited the rules" in one line.
type fakeRules struct {
	body    string
	version string
}

func (f fakeRules) RuleBody(settings.Channel) (string, string) { return f.body, f.version }

type fakeMessageStore struct {
	mu     sync.Mutex
	rows   map[string]store.OutreachMessage
	getErr error
	putErr error
	writes int
}

func newFakeMessageStore() *fakeMessageStore {
	return &fakeMessageStore{rows: map[string]store.OutreachMessage{}}
}

func (f *fakeMessageStore) key(placeID, version string) string { return placeID + "|" + version }

func (f *fakeMessageStore) GetOutreachMessage(_ context.Context, placeID, version string) (store.OutreachMessage, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return store.OutreachMessage{}, false, f.getErr
	}
	om, ok := f.rows[f.key(placeID, version)]
	return om, ok, nil
}

func (f *fakeMessageStore) PutOutreachMessage(_ context.Context, placeID, channel, version, body string, truncated bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.putErr != nil {
		return f.putErr
	}
	// Mirror the real store: a sent/skipped row is not overwritten by a re-run.
	if existing, ok := f.rows[f.key(placeID, version)]; ok && existing.Status != store.OutreachStatusDraft {
		return nil
	}
	f.writes++
	f.rows[f.key(placeID, version)] = store.OutreachMessage{
		Channel:   channel,
		Body:      body,
		Status:    store.OutreachStatusDraft,
		Truncated: truncated,
	}
	return nil
}

func (f *fakeMessageStore) seed(placeID, version string, om store.OutreachMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[f.key(placeID, version)] = om
}

func draftCompany(id string) maps.Company {
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
	s := newFakeMessageStore()
	r := NewMessageRunner(testConfig(), d, s)

	res, gaps, err := r.DraftFor(ctx, draftCompany("p1"), CategoryBeauty, sampleGap, settings.ChannelEmail)
	if err != nil {
		t.Fatalf("DraftFor: %v", err)
	}
	if len(gaps) != 0 {
		t.Fatalf("gaps = %v, want none", gaps)
	}
	if res.Method != MethodModel || res.Status != store.OutreachStatusDraft || res.Body == "" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if s.writes != 1 {
		t.Fatalf("cache writes = %d, want 1", s.writes)
	}

	res2, _, err := r.DraftFor(ctx, draftCompany("p1"), CategoryBeauty, sampleGap, settings.ChannelEmail)
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

// The two channels are two questions. Drafting the WhatsApp message must not be
// answered from the email's cache entry, or the operator gets a letter in a
// message box.
func TestDraftFor_ChannelIsPartOfTheCacheKey(t *testing.T) {
	ctx := context.Background()
	d := &fakeDrafter{}
	r := NewMessageRunner(testConfig(), d, newFakeMessageStore())

	if _, _, err := r.DraftFor(ctx, draftCompany("p1"), CategoryBeauty, sampleGap, settings.ChannelEmail); err != nil {
		t.Fatalf("DraftFor(email): %v", err)
	}
	res, _, err := r.DraftFor(ctx, draftCompany("p1"), CategoryBeauty, sampleGap, settings.ChannelWhatsApp)
	if err != nil {
		t.Fatalf("DraftFor(whatsapp): %v", err)
	}
	if res.Method != MethodModel {
		t.Fatalf("whatsapp answered from the email cache: %+v", res)
	}
	if d.calls != 2 {
		t.Fatalf("drafter calls = %d, want 2 — one per channel", d.calls)
	}
	if d.inputs[1].Channel != settings.ChannelWhatsApp {
		t.Fatalf("the channel did not reach the prompt: %+v", d.inputs[1])
	}
}

// Editing a rule file changes the prompt, so it has to change the key too —
// otherwise the operator edits the rules and is served the old draft.
func TestDraftFor_RuleEditInvalidatesTheCache(t *testing.T) {
	ctx := context.Background()
	d := &fakeDrafter{}
	s := newFakeMessageStore()

	r := NewMessageRunner(testConfig(), d, s)
	r.UseRules(fakeRules{body: "kisa yaz", version: "aaaa1111"})
	if _, _, err := r.DraftFor(ctx, draftCompany("p1"), CategoryBeauty, sampleGap, settings.ChannelEmail); err != nil {
		t.Fatalf("DraftFor: %v", err)
	}
	if d.inputs[0].Rules != "kisa yaz" {
		t.Fatalf("the rule file did not reach the prompt: %q", d.inputs[0].Rules)
	}

	// The operator rewrites the rules.
	r.UseRules(fakeRules{body: "uzun yaz", version: "bbbb2222"})
	res, _, err := r.DraftFor(ctx, draftCompany("p1"), CategoryBeauty, sampleGap, settings.ChannelEmail)
	if err != nil {
		t.Fatalf("DraftFor (2): %v", err)
	}
	if res.Method != MethodCache && d.calls != 2 {
		t.Fatalf("a rule edit must re-draft, got method=%q calls=%d", res.Method, d.calls)
	}
	if d.calls != 2 {
		t.Fatalf("drafter calls = %d, want 2 after the rules changed", d.calls)
	}
}

// A sent message is replayed from the cache and never regenerated.
func TestDraftFor_SentMessageNotRegenerated(t *testing.T) {
	s := newFakeMessageStore()
	r := NewMessageRunner(testConfig(), panicDrafter{t}, s)
	s.seed("p1", r.version(settings.ChannelEmail), store.OutreachMessage{
		Channel: string(settings.ChannelEmail),
		Body:    "the email a human already sent",
		Status:  store.OutreachStatusSent,
	})

	res, gaps, err := r.DraftFor(context.Background(), draftCompany("p1"), CategoryBeauty, sampleGap, settings.ChannelEmail)
	if err != nil {
		t.Fatalf("DraftFor: %v", err)
	}
	if len(gaps) != 0 {
		t.Fatalf("gaps = %v, want none", gaps)
	}
	if res.Method != MethodCache || res.Status != store.OutreachStatusSent {
		t.Fatalf("a sent message should replay as-is: %+v", res)
	}
	if res.Body != "the email a human already sent" {
		t.Fatalf("body changed: %q", res.Body)
	}
}

func TestDraftFor_NoPlaceIDIsSkipped(t *testing.T) {
	r := NewMessageRunner(testConfig(), panicDrafter{t}, newFakeMessageStore())

	c := draftCompany("")
	res, gaps, err := r.DraftFor(context.Background(), c, CategoryBeauty, sampleGap, settings.ChannelEmail)
	if err != nil {
		t.Fatalf("DraftFor: %v", err)
	}
	if res.Method != MethodUnresolved || res.Body != "" {
		t.Fatalf("want an unresolved empty result, got %+v", res)
	}
	if len(gaps) != 1 || !strings.Contains(gaps[0], "no place_id") {
		t.Fatalf("want one 'no place_id' gap, got %v", gaps)
	}
}

func TestDraftFor_EmptyGapAnalysisIsSkipped(t *testing.T) {
	r := NewMessageRunner(testConfig(), panicDrafter{t}, newFakeMessageStore())

	res, gaps, err := r.DraftFor(context.Background(), draftCompany("p1"), CategoryBeauty, "  ", settings.ChannelEmail)
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
	s := newFakeMessageStore()
	s.getErr = errors.New("disk gone")
	r := NewMessageRunner(testConfig(), d, s)

	res, gaps, err := r.DraftFor(context.Background(), draftCompany("p1"), CategoryBeauty, sampleGap, settings.ChannelEmail)
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
	d := &fakeDrafter{answer: func(refine.MessageInput) (refine.Output, error) {
		return refine.Output{}, errors.New("claude unavailable")
	}}
	r := NewMessageRunner(testConfig(), d, newFakeMessageStore())

	res, gaps, err := r.DraftFor(context.Background(), draftCompany("p1"), CategoryBeauty, sampleGap, settings.ChannelEmail)
	if err != nil {
		t.Fatalf("DraftFor: %v", err)
	}
	if res.Method != MethodUnresolved || res.Body != "" {
		t.Fatalf("want an unresolved result, got %+v", res)
	}
	if len(gaps) != 1 || !strings.Contains(gaps[0], "failed") {
		t.Fatalf("want one 'failed' gap, got %v", gaps)
	}
}

func TestDraftFor_UnusableModelAnswerNotCached(t *testing.T) {
	d := &fakeDrafter{answer: func(refine.MessageInput) (refine.Output, error) {
		return refine.Output{Text: "", Refined: false}, nil
	}}
	s := newFakeMessageStore()
	r := NewMessageRunner(testConfig(), d, s)

	res, gaps, err := r.DraftFor(context.Background(), draftCompany("p1"), CategoryBeauty, sampleGap, settings.ChannelEmail)
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

	r := NewMessageRunner(testConfig(), &fakeDrafter{}, newFakeMessageStore())
	if _, _, err := r.DraftFor(ctx, draftCompany("p1"), CategoryBeauty, sampleGap, settings.ChannelEmail); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

// The drafter is fed the company's own facts and the category gap analysis —
// derived values, no raw page text.
func TestDraftFor_InputIsDerived(t *testing.T) {
	d := &fakeDrafter{}
	r := NewMessageRunner(testConfig(), d, newFakeMessageStore())

	c := draftCompany("p1")
	c.Website = "https://example.com"

	if _, _, err := r.DraftFor(context.Background(), c, CategoryBeauty, sampleGap, settings.ChannelEmail); err != nil {
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
