package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/logrenant/mimir/internal/leadgen"
	"github.com/logrenant/mimir/internal/store"
)

type fakeLeadGen struct {
	calls     int
	req       leadgen.RunRequest
	rep       leadgen.Report
	err       error
	exportReq leadgen.ExportRequest
	exportRes leadgen.ExportResult
	exportErr error
}

func (f *fakeLeadGen) Run(_ context.Context, req leadgen.RunRequest) (leadgen.Report, error) {
	f.calls++
	f.req = req
	return f.rep, f.err
}

func (f *fakeLeadGen) Export(_ context.Context, req leadgen.ExportRequest) (leadgen.ExportResult, error) {
	f.exportReq = req
	return f.exportRes, f.exportErr
}

// The export route is a run plus a file: the same search validation, then the
// workbook. Enrichment is opt-in because it is a page fetch per company.
func TestLeadgenExport_RunsTheSearchThenWrites(t *testing.T) {
	lg := &fakeLeadGen{
		rep:       leadgen.Report{Region: "Kadıköy", Companies: []leadgen.CompanyLead{{Name: "A"}}},
		exportRes: leadgen.ExportResult{Path: "/tmp/Kadikoy.xlsx", Companies: 1},
	}
	h := New(testConfig(), Deps{LeadGen: lg, Emails: &fakeEmailStatus{}}).Handler()

	w := do(h, http.MethodPost, "/maps/leadgen/export", testToken,
		`{"query":"Kadıköy diş kliniği","region":"Kadıköy","enrich":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if lg.calls != 1 || lg.req.Region != "Kadıköy" {
		t.Errorf("the export did not run the search: %+v", lg.req)
	}
	if !lg.exportReq.Enrich || lg.exportReq.Report.Region != "Kadıköy" {
		t.Errorf("the export request lost its fields: %+v", lg.exportReq)
	}
	if !strings.Contains(w.Body.String(), "Kadikoy.xlsx") {
		t.Errorf("the response must name the file: %s", w.Body.String())
	}
}

// An empty query is refused before anything runs — the same rule the run route
// applies, because both share one validator.
func TestLeadgenExport_EmptyQueryIs400(t *testing.T) {
	lg := &fakeLeadGen{}
	h := New(testConfig(), Deps{LeadGen: lg, Emails: &fakeEmailStatus{}}).Handler()

	if w := do(h, http.MethodPost, "/maps/leadgen/export", testToken, `{"query":"  "}`); w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", w.Code)
	}
	if lg.calls != 0 {
		t.Errorf("the pipeline ran on an empty query")
	}
}

type fakeEmailStatus struct {
	calls                              int
	lastPlace, lastVersion, lastStatus string
	err                                error
}

func (f *fakeEmailStatus) SetOutreachEmailStatus(_ context.Context, placeID, version, status string) error {
	f.calls++
	f.lastPlace, f.lastVersion, f.lastStatus = placeID, version, status
	return f.err
}

func TestMaps_RoutesUnregisteredWithoutLeadGen(t *testing.T) {
	h := New(testConfig(), Deps{}).Handler()

	for _, path := range []string{"/maps/leadgen", "/maps/emails/status"} {
		w := do(h, http.MethodPost, path, testToken, `{}`)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s without a LeadGen dep = %d, want 404", path, w.Code)
		}
	}
}

func TestLeadgen_HappyPathThreadsTheRequest(t *testing.T) {
	lg := &fakeLeadGen{rep: leadgen.Report{Region: "Kadikoy", RanCategorize: true}}
	h := New(testConfig(), Deps{LeadGen: lg, Emails: &fakeEmailStatus{}}).Handler()

	body := `{"query":"dentists in Kadikoy","region":"Kadikoy","count":10,"gap_analysis":true,"emails":true}`
	w := do(h, http.MethodPost, "/maps/leadgen", testToken, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if lg.calls != 1 {
		t.Fatalf("LeadGen.Run called %d times, want 1", lg.calls)
	}
	if lg.req.Query.Text != "dentists in Kadikoy" || lg.req.Region != "Kadikoy" {
		t.Errorf("query/region not threaded: %+v", lg.req)
	}
	if lg.req.Query.MaxResults != 10 {
		t.Errorf("count not threaded: %d", lg.req.Query.MaxResults)
	}
	if !lg.req.WithGapAnalysis || !lg.req.WithEmails {
		t.Errorf("stage flags not threaded: %+v", lg.req)
	}

	var got leadgen.Report
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("response is not a Report: %v", err)
	}
	if got.Region != "Kadikoy" {
		t.Errorf("report not passed through: %+v", got)
	}
}

func TestLeadgen_Validation(t *testing.T) {
	h := New(testConfig(), Deps{LeadGen: &fakeLeadGen{}, Emails: &fakeEmailStatus{}}).Handler()

	cases := map[string]string{
		"empty query":      `{"query":"  "}`,
		"near radius zero": `{"query":"x","near":{"latitude":1,"longitude":2,"radius_meters":0}}`,
		"unknown field":    `{"query":"x","bogus":true}`,
		"not json":         `{`,
	}
	for name, body := range cases {
		w := do(h, http.MethodPost, "/maps/leadgen", testToken, body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (%s)", name, w.Code, w.Body.String())
		}
	}
}

func TestLeadgen_NoDataIsBadGateway(t *testing.T) {
	lg := &fakeLeadGen{err: leadgen.ErrNoData}
	h := New(testConfig(), Deps{LeadGen: lg, Emails: &fakeEmailStatus{}}).Handler()

	w := do(h, http.MethodPost, "/maps/leadgen", testToken, `{"query":"x"}`)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
}

func TestLeadgen_UnknownErrorIsInternal(t *testing.T) {
	lg := &fakeLeadGen{err: context.DeadlineExceeded}
	h := New(testConfig(), Deps{LeadGen: lg, Emails: &fakeEmailStatus{}}).Handler()

	w := do(h, http.MethodPost, "/maps/leadgen", testToken, `{"query":"x"}`)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}

func TestSetEmailStatus_HappyPath(t *testing.T) {
	es := &fakeEmailStatus{}
	cfg := testConfig()
	h := New(cfg, Deps{LeadGen: &fakeLeadGen{}, Emails: es}).Handler()

	w := do(h, http.MethodPost, "/maps/emails/status", testToken, `{"place_id":"place-1","status":"sent"}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if es.calls != 1 || es.lastPlace != "place-1" || es.lastStatus != "sent" {
		t.Fatalf("SetOutreachEmailStatus args wrong: %+v", es)
	}
	if es.lastVersion != cfg.LeadgenEmailVersion {
		t.Errorf("version = %q, want the server constant %q", es.lastVersion, cfg.LeadgenEmailVersion)
	}
}

func TestSetEmailStatus_ErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"invalid status", store.ErrEmailStatusInvalid, http.StatusBadRequest},
		{"not found", store.ErrEmailNotFound, http.StatusNotFound},
	}
	for _, tc := range cases {
		es := &fakeEmailStatus{err: tc.err}
		h := New(testConfig(), Deps{LeadGen: &fakeLeadGen{}, Emails: es}).Handler()
		w := do(h, http.MethodPost, "/maps/emails/status", testToken, `{"place_id":"p","status":"sent"}`)
		if w.Code != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.name, w.Code, tc.want)
		}
	}
}

func TestSetEmailStatus_MissingPlaceID(t *testing.T) {
	h := New(testConfig(), Deps{LeadGen: &fakeLeadGen{}, Emails: &fakeEmailStatus{}}).Handler()
	w := do(h, http.MethodPost, "/maps/emails/status", testToken, `{"status":"sent"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestMaps_RoutesRequireToken(t *testing.T) {
	h := New(testConfig(), Deps{LeadGen: &fakeLeadGen{}, Emails: &fakeEmailStatus{}}).Handler()
	w := do(h, http.MethodPost, "/maps/leadgen", "", `{"query":"x"}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

// --- the ledger routes -----------------------------------------------------

type fakeLedger struct {
	regions []store.LeadRegion
	rows    []store.LeadRow
	filter  store.LeadFilter
	counts  []store.CategoryCount
	runs    []store.LeadRun
	drafts  map[string]store.OutreachEmail
	listErr error
}

func (f *fakeLedger) ListLeads(_ context.Context, filter store.LeadFilter) ([]store.LeadRow, error) {
	f.filter = filter
	return f.rows, f.listErr
}

func (f *fakeLedger) LeadCategoryCounts(_ context.Context, filter store.LeadFilter) ([]store.CategoryCount, error) {
	f.filter = filter
	return f.counts, nil
}

func (f *fakeLedger) ListLeadRuns(_ context.Context, _ int) ([]store.LeadRun, error) {
	return f.runs, nil
}

func (f *fakeLedger) ListLeadRegions(_ context.Context) ([]store.LeadRegion, error) {
	return f.regions, nil
}

func (f *fakeLedger) OutreachEmailsFor(_ context.Context, _ []string, _ string) (map[string]store.OutreachEmail, error) {
	return f.drafts, nil
}

func TestListLeads_ServesTheLedgerWithDraftStatus(t *testing.T) {
	led := &fakeLedger{
		rows: []store.LeadRow{{PlaceID: "p1", Name: "Alfa", Category: "health"}},
		drafts: map[string]store.OutreachEmail{
			"p1": {Email: "merhaba", Status: store.EmailStatusSent},
		},
	}
	h := New(testConfig(), Deps{Leads: led}).Handler()

	w := do(h, http.MethodGet, "/maps/leads?category=health&q=alf&without_website=1", testToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (%s)", w.Code, w.Body.String())
	}

	var got savedLeadsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Companies) != 1 || got.Companies[0].EmailStatus != store.EmailStatusSent {
		t.Fatalf("the draft status must ride the lead: %+v", got.Companies)
	}
	if led.filter.Category != "health" || led.filter.Text != "alf" || !led.filter.WithoutWebsite {
		t.Errorf("the query string was not read into the filter: %+v", led.filter)
	}
	if led.filter.Limit != testConfig().LeadsPageDefault {
		t.Errorf("want the configured default page, got %d", led.filter.Limit)
	}
}

// The page bound is the server's, not the client's (SD-1).
func TestListLeads_LimitIsClampedToTheMax(t *testing.T) {
	led := &fakeLedger{}
	cfg := testConfig()
	h := New(cfg, Deps{Leads: led}).Handler()

	if w := do(h, http.MethodGet, "/maps/leads?limit=999999", testToken, ""); w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	if led.filter.Limit != cfg.LeadsPageMax {
		t.Errorf("want the limit clamped to %d, got %d", cfg.LeadsPageMax, led.filter.Limit)
	}
	if w := do(h, http.MethodGet, "/maps/leads?limit=-3", testToken, ""); w.Code != http.StatusBadRequest {
		t.Errorf("a negative limit must be 400, got %d", w.Code)
	}
}

// The rail shows the categories the operator is not looking at.
func TestLeadCategories_DropsTheSelectedCategory(t *testing.T) {
	led := &fakeLedger{counts: []store.CategoryCount{{Category: "health", Companies: 2, WithoutWebsite: 1}}}
	h := New(testConfig(), Deps{Leads: led}).Handler()

	w := do(h, http.MethodGet, "/maps/leads/categories?category=health", testToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if led.filter.Category != "" {
		t.Errorf("the rail must not filter by the selected category: %q", led.filter.Category)
	}
	if !strings.Contains(w.Body.String(), `"without_website":1`) {
		t.Errorf("the rail lost its counts: %s", w.Body.String())
	}
}

func TestLeadRuns_ServesTheHistory(t *testing.T) {
	led := &fakeLedger{runs: []store.LeadRun{{ID: "run-1", Query: "Kadıköy diş kliniği", CompanyCount: 12}}}
	h := New(testConfig(), Deps{Leads: led}).Handler()

	w := do(h, http.MethodGet, "/maps/leads/runs", testToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "run-1") {
		t.Errorf("the history lost its run: %s", w.Body.String())
	}
}

// The ledger routes are gated on their own dep: a daemon with no region source
// still serves what earlier runs found.
func TestLeadRoutes_NotRegisteredWithoutTheLedger(t *testing.T) {
	h := New(testConfig(), Deps{LeadGen: &fakeLeadGen{}, Emails: &fakeEmailStatus{}}).Handler()

	if w := do(h, http.MethodGet, "/maps/leads", testToken, ""); w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", w.Code)
	}
}

// --- the model selection ------------------------------------------------------

func TestLeadgen_ThreadsThePublishedProviderAndModel(t *testing.T) {
	lg := &fakeLeadGen{rep: leadgen.Report{Region: "Kadikoy"}}
	h := New(testConfig(), Deps{LeadGen: lg, Emails: &fakeEmailStatus{}}).Handler()

	body := `{"query":"x","provider":"claude","model":"claude-opus-5"}`
	if w := do(h, http.MethodPost, "/maps/leadgen", testToken, body); w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if lg.req.Selection.Provider != "claude" || lg.req.Selection.Model != "claude-opus-5" {
		t.Errorf("selection not threaded: %+v", lg.req.Selection)
	}
}

// A provider with no model is "that provider's default", and the daemon
// resolves it here rather than leaving an empty string to be interpreted three
// layers down — the cache key is derived from it.
func TestLeadgen_AProviderWithoutAModelResolvesToItsDefault(t *testing.T) {
	cfg := testConfig()
	lg := &fakeLeadGen{rep: leadgen.Report{Region: "Kadikoy"}}
	h := New(cfg, Deps{LeadGen: lg, Emails: &fakeEmailStatus{}}).Handler()

	if w := do(h, http.MethodPost, "/maps/leadgen", testToken, `{"query":"x","provider":"agy"}`); w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if want := cfg.LLMDefaultModel("agy"); lg.req.Selection.Model != want {
		t.Errorf("model = %q, want the provider default %q", lg.req.Selection.Model, want)
	}
}

// No selection is the routed default, and it must stay the zero value: that is
// what keeps every cache entry written before the picker existed a hit.
func TestLeadgen_NoSelectionRoutesByClass(t *testing.T) {
	lg := &fakeLeadGen{rep: leadgen.Report{Region: "Kadikoy"}}
	h := New(testConfig(), Deps{LeadGen: lg, Emails: &fakeEmailStatus{}}).Handler()

	if w := do(h, http.MethodPost, "/maps/leadgen", testToken, `{"query":"x"}`); w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !lg.req.Selection.IsZero() {
		t.Errorf("Selection = %+v, want the zero value", lg.req.Selection)
	}
}

// Both names become argv to a subprocess, so anything outside the published
// table is refused rather than quietly replaced with the default.
func TestLeadgen_RejectsUnpublishedSelections(t *testing.T) {
	lg := &fakeLeadGen{rep: leadgen.Report{Region: "Kadikoy"}}
	h := New(testConfig(), Deps{LeadGen: lg, Emails: &fakeEmailStatus{}}).Handler()

	cases := map[string]string{
		"unknown provider":       `{"query":"x","provider":"gpt","model":"gpt-4"}`,
		"wrong CLI's model":      `{"query":"x","provider":"claude","model":"gemini-3.8-flash-high"}`,
		"model with no provider": `{"query":"x","model":"claude-opus-5"}`,
	}
	for name, body := range cases {
		for _, path := range []string{"/maps/leadgen", "/maps/leadgen/export"} {
			w := do(h, http.MethodPost, path, testToken, body)
			if w.Code != http.StatusBadRequest {
				t.Errorf("%s on %s: status = %d, want 400 (%s)", name, path, w.Code, w.Body.String())
			}
		}
	}
	if lg.calls != 0 {
		t.Errorf("LeadGen.Run called %d times, want 0 — a rejected selection must not run", lg.calls)
	}
}

// The picker reads its whole vocabulary from here, so it answers on a daemon
// with no lead-gen deps at all: an app that cannot reach this route shows no
// picker, which is worse than a picker on a daemon that cannot yet search.
func TestLLMProviders_IsPublishedWithoutLeadgenDeps(t *testing.T) {
	cfg := testConfig()
	h := New(cfg, Deps{}).Handler()

	w := do(h, http.MethodGet, "/llm/providers", testToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var got llmProviderListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("response is not a provider list: %v", err)
	}
	if len(got.Providers) != len(cfg.LLMProviders) {
		t.Errorf("published %d providers, want %d", len(got.Providers), len(cfg.LLMProviders))
	}
	if got.Routed.Provider != cfg.DistillProvider || got.Routed.Model != cfg.DistillModel {
		t.Errorf("routed default = %+v, want the class routing's answer", got.Routed)
	}
	// Every published pair must be one llmSelection accepts, or the picker
	// offers combinations the run route then rejects.
	for _, p := range got.Providers {
		for _, m := range p.Models {
			if !cfg.HasLLMModel(p.ID, m.ID) {
				t.Errorf("published %s/%s, which the daemon would reject", p.ID, m.ID)
			}
		}
	}
}
