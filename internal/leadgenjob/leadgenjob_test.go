package leadgenjob

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/logrenant/mimir/internal/coderunner"
	"github.com/logrenant/mimir/internal/leadgen"
	"github.com/logrenant/mimir/internal/store"
)

// recordingSink captures what a job said, which is the only way to assert the
// narration without a daemon behind it.
type recordingSink struct {
	steps   []string
	results []string
	said    []string
}

func (s *recordingSink) Started(string, string) {}
func (s *recordingSink) Say(text string)        { s.said = append(s.said, text) }
func (s *recordingSink) Stderr(string)          {}
func (s *recordingSink) Step(name string, _ any) func(bool, string) {
	s.steps = append(s.steps, name)
	return func(ok bool, out string) {
		mark := "ok"
		if !ok {
			mark = "fail"
		}
		s.results = append(s.results, name+":"+mark+":"+out)
	}
}

type fakePipeline struct {
	report leadgen.Report
	err    error
	got    leadgen.RunRequest
}

func (f *fakePipeline) Run(_ context.Context, req leadgen.RunRequest) (leadgen.Report, error) {
	f.got = req
	return f.report, f.err
}

func TestExecutor_NeedsNoAccountAndAnswersToItsAgent(t *testing.T) {
	e := New(&fakePipeline{})
	if e.Agent() != Agent {
		t.Fatalf("agent = %q, want %q", e.Agent(), Agent)
	}
	if e.Lane() != coderunner.LaneWorker {
		t.Fatal("lead-gen must be on the worker lane: it spends no credential slot")
	}
}

func TestParse_FallsBackToThePromptWhenNoParamsWereSent(t *testing.T) {
	p, err := Parse(store.RunRow{Prompt: "İstanbul'da diş kliniği"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Query != "İstanbul'da diş kliniği" {
		t.Fatalf("query = %q, want the prompt", p.Query)
	}
}

func TestParse_PrefersParamsOverThePrompt(t *testing.T) {
	p, err := Parse(store.RunRow{
		Prompt: "bir şeyler bul",
		Params: `{"query":"Kadıköy diş kliniği","region":"Kadıköy","with_emails":true}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Query != "Kadıköy diş kliniği" || p.Region != "Kadıköy" || !p.WithEmails {
		t.Fatalf("got %+v", p)
	}
}

// TestPrepare_RefusesACardWhoseParamsWillNotDecode — refused before anything is
// claimed, so the reason lands on the card instead of half a region search.
func TestPrepare_RefusesACardWhoseParamsWillNotDecode(t *testing.T) {
	e := New(&fakePipeline{})
	err := e.Prepare(context.Background(), store.RunRow{Params: `{"query":`})
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Fatalf("err = %v, want it to say the params are the problem", err)
	}
}

func TestPrepare_RefusesACardThatNamesNoRegion(t *testing.T) {
	e := New(&fakePipeline{})
	if err := e.Prepare(context.Background(), store.RunRow{}); err == nil {
		t.Fatal("a card with neither params nor a prompt has nothing to search")
	}
}

// TestRequest_ImpliesGapAnalysisFromEmails: stage 4 reads stage 3's output, so
// asking for drafts without the analysis would silently produce nothing.
func TestRequest_ImpliesGapAnalysisFromEmails(t *testing.T) {
	p := Params{Query: "x", WithEmails: true}
	if !p.request().WithGapAnalysis {
		t.Fatal("emails must imply the gap analysis they are written from")
	}
}

func TestRequest_DefaultsContactsOn(t *testing.T) {
	if !(Params{Query: "x"}).request().WithContacts {
		t.Fatal("a lead nobody can ring is not a lead")
	}
	off := false
	if (Params{Query: "x", WithContacts: &off}).request().WithContacts {
		t.Fatal("an explicit false must be honoured")
	}
}

// TestExecute_NarratesEveryStageThePipelineActuallyRan. A card on this lane has
// a terminal; what fills it is stages rather than tool calls.
func TestExecute_NarratesEveryStageThePipelineActuallyRan(t *testing.T) {
	sink := &recordingSink{}
	e := New(&fakePipeline{report: leadgen.Report{
		Region:         "İstanbul",
		Source:         "mapscrape",
		RanCategorize:  true,
		RanContacts:    true,
		RanGapAnalysis: true,
		Companies: []leadgen.CompanyLead{
			{Name: "A", Category: "dentist", Phone: "0212", Email: "a@b.c"},
			{Name: "B", Category: "dentist"},
		},
		Categories: []leadgen.CategoryReport{{}},
		Notes:      []string{"iki şirketin sitesi açılmadı"},
	}})

	out := e.Execute(context.Background(), coderunner.Job{
		Row: store.RunRow{Prompt: "İstanbul diş kliniği"},
		Out: sink,
	})
	if out.Status != store.RunStatusCompleted {
		t.Fatalf("status = %q, want completed (err=%v)", out.Status, out.Err)
	}

	for _, want := range []string{"region_search", "categorize", "contacts", "gap_analysis"} {
		if !contains(sink.steps, want) {
			t.Errorf("stage %q was never announced (got %v)", want, sink.steps)
		}
	}
	if contains(sink.steps, "outreach") {
		t.Error("a stage the pipeline did not run was announced anyway")
	}
	if len(sink.said) != 1 {
		t.Errorf("the pipeline's notes did not reach the stream: %v", sink.said)
	}
	if !strings.Contains(strings.Join(sink.results, "|"), "2 işletme") {
		t.Errorf("the search result did not say what it found: %v", sink.results)
	}
}

// TestExecute_ReportsAFailedSearchOnTheCard.
func TestExecute_ReportsAFailedSearchOnTheCard(t *testing.T) {
	sink := &recordingSink{}
	e := New(&fakePipeline{err: errors.New("no region search source")})

	out := e.Execute(context.Background(), coderunner.Job{
		Row: store.RunRow{Prompt: "İstanbul"},
		Out: sink,
	})
	if out.Status != store.RunStatusFailed || out.Err == nil {
		t.Fatalf("got %+v, want a failure carrying its reason", out)
	}
	if !strings.Contains(strings.Join(sink.results, "|"), "fail") {
		t.Errorf("the failed stage was not closed as failed: %v", sink.results)
	}
}

// TestCategorySummary_IsStableAcrossRuns — two transcripts over the same region
// should be comparable, which map iteration order would otherwise prevent.
func TestCategorySummary_IsStableAcrossRuns(t *testing.T) {
	r := leadgen.Report{Companies: []leadgen.CompanyLead{
		{Category: "zebra"}, {Category: "apple"}, {Category: "mango"}, {Category: "apple"},
	}}
	first := categorySummary(r)
	for i := 0; i < 20; i++ {
		if categorySummary(r) != first {
			t.Fatalf("summary is not stable: %q vs %q", categorySummary(r), first)
		}
	}
	if !strings.HasPrefix(first, "apple") {
		t.Fatalf("summary = %q, want it sorted", first)
	}
}

func contains(all []string, want string) bool {
	for _, v := range all {
		if v == want {
			return true
		}
	}
	return false
}
