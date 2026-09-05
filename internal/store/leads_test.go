package store

import (
	"context"
	"testing"
	"time"
)

func lead(placeID, name, category string) LeadRow {
	return LeadRow{
		PlaceID:  placeID,
		Name:     name,
		Address:  name + " sokak",
		Website:  "https://" + placeID + ".example",
		Phone:    "+90 555 000 0000",
		Source:   "mapscrape",
		Category: category,
	}
}

func TestPutLeadRun_RoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	run := LeadRun{ID: "run-1", Query: "Kadıköy diş kliniği", RegionLabel: "Kadıköy", Source: "mapscrape", CompanyCount: 2}
	rows := []LeadRow{lead("p1", "Alfa", "health"), lead("p2", "Beta", "beauty")}
	if err := s.PutLeadRun(ctx, run, rows); err != nil {
		t.Fatalf("PutLeadRun: %v", err)
	}

	got, err := s.ListLeads(ctx, LeadFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListLeads: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 leads, got %d", len(got))
	}
	byID := map[string]LeadRow{}
	for _, g := range got {
		byID[g.PlaceID] = g
	}
	if byID["p1"].Name != "Alfa" || byID["p1"].Category != "health" {
		t.Fatalf("round-trip mismatch: %+v", byID["p1"])
	}
	if byID["p1"].FirstSeenAt.IsZero() || byID["p1"].LastSeenAt.IsZero() {
		t.Error("timestamps must be set")
	}

	runs, err := s.ListLeadRuns(ctx, 10)
	if err != nil || len(runs) != 1 {
		t.Fatalf("ListLeadRuns: %d runs, err=%v", len(runs), err)
	}
	if runs[0].Query != "Kadıköy diş kliniği" || runs[0].CompanyCount != 2 {
		t.Fatalf("run mismatch: %+v", runs[0])
	}
}

// The ledger is a record, not a cache: running the same search twice must add a
// run and no businesses.
func TestPutLeadRun_SecondRunAddsNoLeads(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	rows := []LeadRow{lead("p1", "Alfa", "health")}
	if err := s.PutLeadRun(ctx, LeadRun{ID: "run-1", CompanyCount: 1}, rows); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := s.PutLeadRun(ctx, LeadRun{ID: "run-2", CompanyCount: 1}, rows); err != nil {
		t.Fatalf("second run: %v", err)
	}

	got, _ := s.ListLeads(ctx, LeadFilter{Limit: 10})
	if len(got) != 1 {
		t.Fatalf("want 1 lead after two runs, got %d", len(got))
	}
	runs, _ := s.ListLeadRuns(ctx, 10)
	if len(runs) != 2 {
		t.Fatalf("want 2 runs, got %d", len(runs))
	}
}

// The free scrape returns no phone where the billed API did. A re-run through
// the cheaper provider must not erase a number already known.
func TestPutLeadRun_EmptyDoesNotOverwritePopulated(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	full := lead("p1", "Alfa", "health")
	full.Phone = "+90 216 111 1111"
	full.Website = "https://alfa.example"
	if err := s.PutLeadRun(ctx, LeadRun{ID: "run-1"}, []LeadRow{full}); err != nil {
		t.Fatalf("first run: %v", err)
	}

	thin := LeadRow{PlaceID: "p1", Name: "Alfa", Source: "mapscrape"}
	if err := s.PutLeadRun(ctx, LeadRun{ID: "run-2"}, []LeadRow{thin}); err != nil {
		t.Fatalf("second run: %v", err)
	}

	got, _ := s.ListLeads(ctx, LeadFilter{Limit: 10})
	if len(got) != 1 {
		t.Fatalf("want 1 lead, got %d", len(got))
	}
	if got[0].Phone != "+90 216 111 1111" {
		t.Errorf("phone was erased by a thinner provider: %q", got[0].Phone)
	}
	if got[0].Website != "https://alfa.example" {
		t.Errorf("website was erased: %q", got[0].Website)
	}
	if got[0].Category != "health" {
		t.Errorf("category was erased: %q", got[0].Category)
	}
}

func TestListLeads_FilterComponents(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	noSite := lead("p3", "Gama", "health")
	noSite.Website = ""
	rows := []LeadRow{lead("p1", "Alfa", "health"), lead("p2", "Beta", "beauty"), noSite}
	if err := s.PutLeadRun(ctx, LeadRun{ID: "run-1"}, rows); err != nil {
		t.Fatalf("PutLeadRun: %v", err)
	}
	if err := s.PutLeadRun(ctx, LeadRun{ID: "run-2"}, []LeadRow{lead("p2", "Beta", "beauty")}); err != nil {
		t.Fatalf("PutLeadRun: %v", err)
	}

	cases := []struct {
		name string
		f    LeadFilter
		want int
	}{
		{"category", LeadFilter{Category: "health", Limit: 10}, 2},
		{"unknown category", LeadFilter{Category: "nope", Limit: 10}, 0},
		{"run", LeadFilter{RunID: "run-2", Limit: 10}, 1},
		{"text", LeadFilter{Text: "alf", Limit: 10}, 1},
		{"text misses", LeadFilter{Text: "zeta", Limit: 10}, 0},
		{"without website", LeadFilter{WithoutWebsite: true, Limit: 10}, 1},
		{"offset", LeadFilter{Limit: 10, Offset: 2}, 1},
	}
	for _, tc := range cases {
		got, err := s.ListLeads(ctx, tc.f)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(got) != tc.want {
			t.Errorf("%s: want %d, got %d", tc.name, tc.want, len(got))
		}
	}
}

// The rail counts per category under whatever filter it is handed.
func TestLeadCategoryCounts_GroupsAndCountsWebsiteGaps(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	noSite := lead("p3", "Gama", "health")
	noSite.Website = ""
	rows := []LeadRow{lead("p1", "Alfa", "health"), lead("p2", "Beta", "beauty"), noSite}
	if err := s.PutLeadRun(ctx, LeadRun{ID: "run-1"}, rows); err != nil {
		t.Fatalf("PutLeadRun: %v", err)
	}

	counts, err := s.LeadCategoryCounts(ctx, LeadFilter{})
	if err != nil {
		t.Fatalf("LeadCategoryCounts: %v", err)
	}
	if len(counts) != 2 {
		t.Fatalf("want both categories in the rail, got %d", len(counts))
	}
	if one, _ := s.LeadCategoryCounts(ctx, LeadFilter{Category: "beauty"}); len(one) != 1 {
		t.Fatalf("the filter it is given must be honoured, got %d rows", len(one))
	}
	if counts[0].Category != "health" || counts[0].Companies != 2 || counts[0].WithoutWebsite != 1 {
		t.Fatalf("health count mismatch: %+v", counts[0])
	}
}

func TestOutreachMessagesFor_BatchLookup(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.PutOutreachMessage(ctx, "p1", "email", "email-v1#email", "merhaba", false); err != nil {
		t.Fatalf("PutOutreachMessage: %v", err)
	}
	if err := s.SetOutreachStatus(ctx, "p1", "email", OutreachStatusSent); err != nil {
		t.Fatalf("SetOutreachStatus: %v", err)
	}
	if err := s.PutOutreachMessage(ctx, "p1", "whatsapp", "email-v1#whatsapp", "selam", false); err != nil {
		t.Fatalf("PutOutreachMessage(whatsapp): %v", err)
	}

	got, err := s.OutreachMessagesFor(ctx, []string{"p1", "p2"})
	if err != nil {
		t.Fatalf("OutreachMessagesFor: %v", err)
	}
	if len(got) != 1 || len(got["p1"]) != 2 {
		t.Fatalf("want both of p1's channels and nothing for p2, got %+v", got)
	}

	byChannel := map[string]OutreachMessage{}
	for _, m := range got["p1"] {
		byChannel[m.Channel] = m
	}
	if byChannel["email"].Status != OutreachStatusSent {
		t.Fatalf("email status = %q, want sent", byChannel["email"].Status)
	}
	if byChannel["whatsapp"].Status != OutreachStatusDraft {
		t.Fatalf("whatsapp status = %q, want draft", byChannel["whatsapp"].Status)
	}
}

// The selection route reads through this: the operator's own rows, in the order
// they picked them, and nothing they did not pick.
func TestLeadsByPlaceID_KeepsCallerOrder(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	rows := []LeadRow{lead("p1", "Alfa", "health"), lead("p2", "Beta", "beauty"), lead("p3", "Gama", "retail")}
	if err := s.PutLeadRun(ctx, LeadRun{ID: "run-1", RanAt: time.Now()}, rows); err != nil {
		t.Fatalf("PutLeadRun: %v", err)
	}

	got, err := s.LeadsByPlaceID(ctx, []string{"p3", "p1", "missing", "p3"})
	if err != nil {
		t.Fatalf("LeadsByPlaceID: %v", err)
	}
	if len(got) != 2 || got[0].PlaceID != "p3" || got[1].PlaceID != "p1" {
		t.Fatalf("want p3 then p1, deduplicated, got %+v", got)
	}
	if empty, _ := s.LeadsByPlaceID(ctx, nil); len(empty) != 0 {
		t.Error("no ids means no rows, not every row")
	}
}

// A run whose leads all lack a place_id must still record the run without
// writing a nameless lead row.
func TestPutLeadRun_SkipsRowsWithoutPlaceID(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.PutLeadRun(ctx, LeadRun{ID: "run-1", RanAt: time.Now()}, []LeadRow{{Name: "Nameless"}}); err != nil {
		t.Fatalf("PutLeadRun: %v", err)
	}
	got, _ := s.ListLeads(ctx, LeadFilter{Limit: 10})
	if len(got) != 0 {
		t.Fatalf("want no leads, got %d", len(got))
	}
	runs, _ := s.ListLeadRuns(ctx, 10)
	if len(runs) != 1 {
		t.Fatalf("the run itself must still be recorded, got %d", len(runs))
	}
}

func TestLeadLedger_NilStoreTolerated(t *testing.T) {
	ctx := context.Background()
	var s *Store

	if err := s.PutLeadRun(ctx, LeadRun{ID: "run-1"}, []LeadRow{lead("p1", "Alfa", "health")}); err != nil {
		t.Errorf("PutLeadRun on a nil store: %v", err)
	}
	if rows, err := s.ListLeads(ctx, LeadFilter{Limit: 10}); err != nil || rows != nil {
		t.Errorf("ListLeads on a nil store: %v %v", rows, err)
	}
	if c, err := s.LeadCategoryCounts(ctx, LeadFilter{}); err != nil || c != nil {
		t.Errorf("LeadCategoryCounts on a nil store: %v %v", c, err)
	}
	if r, err := s.ListLeadRuns(ctx, 10); err != nil || r != nil {
		t.Errorf("ListLeadRuns on a nil store: %v %v", r, err)
	}
	if m, err := s.OutreachMessagesFor(ctx, []string{"p1"}); err != nil || m != nil {
		t.Errorf("OutreachMessagesFor on a nil store: %v %v", m, err)
	}
	if rows, err := s.LeadsByPlaceID(ctx, []string{"p1"}); err != nil || rows != nil {
		t.Errorf("LeadsByPlaceID on a nil store: %v %v", rows, err)
	}
}

// Open is idempotent, migration 0016 included.
func TestLeadLedger_MigrationIdempotent(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)

	first, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := first.PutLeadRun(ctx, LeadRun{ID: "run-1"}, []LeadRow{lead("p1", "Alfa", "health")}); err != nil {
		t.Fatalf("PutLeadRun: %v", err)
	}
	_ = first.Close()

	second, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer func() { _ = second.Close() }()

	got, err := second.ListLeads(ctx, LeadFilter{Limit: 10})
	if err != nil || len(got) != 1 {
		t.Fatalf("the ledger must survive a reopen: %d rows, err=%v", len(got), err)
	}
}
