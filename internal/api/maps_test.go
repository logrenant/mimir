package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/logrenant/mimir/internal/leadgen"
	"github.com/logrenant/mimir/internal/store"
)

type fakeLeadGen struct {
	calls int
	req   leadgen.RunRequest
	rep   leadgen.Report
	err   error
}

func (f *fakeLeadGen) Run(_ context.Context, req leadgen.RunRequest) (leadgen.Report, error) {
	f.calls++
	f.req = req
	return f.rep, f.err
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
