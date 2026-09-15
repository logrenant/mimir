// Package leadgenjob makes lead-gen a sub-agent: a card on the board, in the
// same queue as every other card, running on a lane that spends no credential
// slot.
//
// It is its own package on purpose. tasks/README.md's parallelism rule says a
// new package taking a constructor argument conflicts with nothing, and the
// point here is stronger than convenience: this package imports coderunner for
// its types and leadgen for its pipeline, so neither of those has to learn
// about the other. internal/leadgen stays free of the dispatcher, and
// internal/coderunner stays free of Google Maps.
//
// POST /maps/leadgen does not go away, and that is deliberate. It is the
// Leadgen screen's own interaction — the operator types a region and watches
// the table fill — and turning it into a card would make that screen poll a
// board to see its own result. The card and the route call the same
// Pipeline.Run: the card is "queue this and tell me when it's done", the route
// is "I am looking at it now".
package leadgenjob

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/logrenant/mimir/internal/coderunner"
	"github.com/logrenant/mimir/internal/leadgen"
	"github.com/logrenant/mimir/internal/maps"
	"github.com/logrenant/mimir/internal/store"
)

// Agent is the sub-agent key this executor answers to. It matches the entry in
// internal/agents, and the dispatcher routes on nothing else.
const Agent = "leadgen"

// Params is a lead-gen card's input, as the client wrote it into the row.
//
// A struct rather than a free-form map so the failure is legible: a card whose
// params will not decode is refused at dispatch with a reason, instead of
// producing a search for an empty region.
type Params struct {
	// Query is the free-text region search, exactly as it would be typed into
	// Google Maps.
	Query string `json:"query"`
	// Region is the human label the gap analysis and the drafts are cached
	// under. Empty falls back to Query.
	Region string `json:"region,omitempty"`
	// MaxResults caps how many companies are collected. Zero means the
	// pipeline's own ceiling.
	MaxResults int `json:"max_results,omitempty"`

	// The three opt-in stages. Contacts defaults on at the API edge for the
	// same reason it does there: a lead nobody can ring is not a lead.
	WithContacts    *bool `json:"with_contacts,omitempty"`
	WithGapAnalysis bool  `json:"with_gap_analysis,omitempty"`
	WithEmails      bool  `json:"with_emails,omitempty"`
}

// Parse reads a row's params. The prompt is the fallback query, so an operator
// who wrote a card in plain words and let the router pick the agent still gets
// a search rather than a refusal.
func Parse(row store.RunRow) (Params, error) {
	var p Params
	if strings.TrimSpace(row.Params) != "" {
		if err := json.Unmarshal([]byte(row.Params), &p); err != nil {
			return Params{}, fmt.Errorf("leadgenjob: params will not decode: %w", err)
		}
	}
	if strings.TrimSpace(p.Query) == "" {
		p.Query = strings.TrimSpace(row.Prompt)
	}
	if strings.TrimSpace(p.Query) == "" {
		return Params{}, fmt.Errorf("leadgenjob: the card names no region to search")
	}
	return p, nil
}

func (p Params) request() leadgen.RunRequest {
	contacts := true
	if p.WithContacts != nil {
		contacts = *p.WithContacts
	}
	region := strings.TrimSpace(p.Region)
	if region == "" {
		region = strings.TrimSpace(p.Query)
	}
	return leadgen.RunRequest{
		Query:           maps.Query{Text: p.Query, MaxResults: p.MaxResults},
		Region:          region,
		WithContacts:    contacts,
		WithGapAnalysis: p.WithGapAnalysis || p.WithEmails,
		WithEmails:      p.WithEmails,
	}
}

// Runner is the half of leadgen.Pipeline this executor uses. An interface so a
// test can answer without a scraper, a browser or a Places key.
type Runner interface {
	Run(ctx context.Context, req leadgen.RunRequest) (leadgen.Report, error)
}

// Executor runs one lead-gen card.
type Executor struct{ pipeline Runner }

// New returns an executor over a pipeline.
func New(p Runner) *Executor { return &Executor{pipeline: p} }

func (e *Executor) Agent() string         { return Agent }
func (e *Executor) Lane() coderunner.Lane { return coderunner.LaneWorker }

// Prepare decodes the card's input before anything is claimed, so a card that
// cannot run is refused with its reason rather than failing halfway through a
// region search.
func (e *Executor) Prepare(_ context.Context, row store.RunRow) error {
	_, err := Parse(row)
	return err
}

// Execute runs the pipeline and narrates it.
//
// The narration is not decoration: a card on this lane has a terminal like
// every other card, and what fills it is the stages rather than tool calls.
// The desktop already renders all four event kinds, so nothing downstream had
// to learn a new shape.
func (e *Executor) Execute(ctx context.Context, job coderunner.Job) coderunner.Outcome {
	params, err := Parse(job.Row)
	if err != nil {
		return coderunner.Outcome{Status: store.RunStatusFailed, Err: err}
	}

	req := params.request()
	done := job.Out.Step("region_search", map[string]any{
		"region": req.Region, "query": req.Query.Text,
	})

	report, err := e.pipeline.Run(ctx, req)
	if err != nil {
		done(false, err.Error())
		return coderunner.Outcome{Status: store.RunStatusFailed, Err: err}
	}
	done(true, fmt.Sprintf("%s — %d işletme (%s)",
		report.Region, len(report.Companies), sourceLabel(report)))

	// Each stage the pipeline actually ran, reported after the fact. The
	// pipeline is one synchronous call and does not publish progress, so this
	// is an honest account rather than a live one — and saying "categorize:
	// 62" once is worth more than a spinner that claims to know when.
	if report.RanCategorize {
		closeStep := job.Out.Step("categorize", nil)
		closeStep(true, categorySummary(report))
	}
	if report.RanContacts {
		closeStep := job.Out.Step("contacts", nil)
		closeStep(true, contactSummary(report))
	}
	if report.RanGapAnalysis {
		closeStep := job.Out.Step("gap_analysis", nil)
		closeStep(true, strconv.Itoa(len(report.Categories))+" kategori")
	}
	if report.RanEmails {
		closeStep := job.Out.Step("outreach", nil)
		closeStep(true, strconv.Itoa(draftCount(report))+" taslak")
	}

	// The pipeline degrades rather than failing (SD-6), so its notes are the
	// only place a partial answer says it is partial.
	for _, note := range report.Notes {
		job.Out.Say(note)
	}

	return coderunner.Outcome{Status: store.RunStatusCompleted, NumTurns: 1}
}

func sourceLabel(r leadgen.Report) string {
	if r.FromCache {
		return r.Source + ", önbellek"
	}
	return r.Source
}

func categorySummary(r leadgen.Report) string {
	counts := map[string]int{}
	for _, c := range r.Companies {
		counts[string(c.Category)]++
	}
	parts := make([]string, 0, len(counts))
	for name, n := range counts {
		parts = append(parts, name+": "+strconv.Itoa(n))
	}
	// Sorted so two runs over the same region read the same, which is what
	// makes two transcripts comparable.
	sortStrings(parts)
	return strings.Join(parts, ", ")
}

func contactSummary(r leadgen.Report) string {
	phones, emails := 0, 0
	for _, c := range r.Companies {
		if strings.TrimSpace(c.Phone) != "" {
			phones++
		}
		if strings.TrimSpace(c.Email) != "" {
			emails++
		}
	}
	return strconv.Itoa(phones) + " telefon, " + strconv.Itoa(emails) + " e-posta"
}

func draftCount(r leadgen.Report) int {
	n := 0
	for _, c := range r.Companies {
		n += len(c.Drafts)
	}
	return n
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
