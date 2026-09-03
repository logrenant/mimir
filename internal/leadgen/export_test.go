package leadgen

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/contacts"
	"github.com/logrenant/mimir/internal/maps"
	"github.com/xuri/excelize/v2"
)

type fakeEnricher struct {
	calls int
	rows  map[string]contacts.Enriched
}

func (f *fakeEnricher) Enrich(_ context.Context, _ []maps.Company) map[string]contacts.Enriched {
	f.calls++
	return f.rows
}

func exportPipeline(t *testing.T, enricher ContactEnricher) *Pipeline {
	t.Helper()
	cfg := pipeConfig()
	cfg.ExportDir = t.TempDir()

	p := NewPipeline(cfg, nil, nil, nil, nil, nil)
	if enricher != nil {
		p.UseContacts(enricher)
	}
	return p
}

func exportReport() Report {
	return Report{
		Region: "Kadıköy",
		Query:  "Kadıköy diş kliniği",
		Source: maps.SourceScrape,
		Companies: []CompanyLead{
			{PlaceID: "mapscrape:1", Name: "A Diş", Category: CategoryHealth, Website: "https://a.example", Rating: 4.9},
			{PlaceID: "mapscrape:2", Name: "B Diş", Category: CategoryHealth},
			{PlaceID: "mapscrape:3", Name: "C Lokanta", Category: CategoryRestaurant, Website: "https://c.example"},
		},
	}
}

// One sheet per category, plus the summary the workbook opens on.
func TestExport_OneSheetPerCategory(t *testing.T) {
	p := exportPipeline(t, nil)

	res, err := p.Export(context.Background(), ExportRequest{Report: exportReport()})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if res.Companies != 3 || res.WithSite != 2 {
		t.Errorf("counts: %+v", res)
	}

	f, err := excelize.OpenFile(res.Path)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer func() { _ = f.Close() }()

	sheets := f.GetSheetList()
	if len(sheets) != 3 || sheets[0] != "Özet" {
		t.Fatalf("sheets: %v, want the summary first and one per category", sheets)
	}

	rows, err := f.GetRows(string(CategoryHealth))
	if err != nil {
		t.Fatalf("GetRows: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("health sheet has %d rows, want a header and two companies", len(rows))
	}
	if rows[0][0] != "Şirket" || rows[0][2] != "Web sitesi var mı" {
		t.Errorf("header: %v", rows[0])
	}
	// The website column is a word, because a human scans this column looking
	// for the companies to call.
	if rows[1][2] != "evet" || rows[2][2] != "hayır" {
		t.Errorf("website column: %q / %q", rows[1][2], rows[2][2])
	}
}

// Enrichment fills the columns the search could not, and records which tier
// answered so an empty cell can be read as "looked, found nothing".
func TestExport_EnrichmentFillsContactColumns(t *testing.T) {
	enricher := &fakeEnricher{rows: map[string]contacts.Enriched{
		"mapscrape:1": {PlaceID: "mapscrape:1", Phone: "+902161234567", Email: "info@a.example", Method: contacts.MethodPattern},
		"mapscrape:2": {PlaceID: "mapscrape:2", Method: contacts.MethodNoSite, Note: "no website on the listing"},
	}}
	p := exportPipeline(t, enricher)

	res, err := p.Export(context.Background(), ExportRequest{Report: exportReport(), Enrich: true})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if enricher.calls != 1 {
		t.Fatalf("the enricher ran %d times, want 1", enricher.calls)
	}
	if res.WithPhone != 1 || res.WithEmail != 1 || !res.Enriched {
		t.Errorf("counts: %+v", res)
	}

	f, err := excelize.OpenFile(res.Path)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	defer func() { _ = f.Close() }()

	rows, err := f.GetRows(string(CategoryHealth))
	if err != nil {
		t.Fatalf("GetRows: %v", err)
	}
	if rows[1][4] != "+902161234567" || rows[1][5] != "info@a.example" {
		t.Errorf("contact columns: %v", rows[1])
	}
	if rows[2][4] != "" || rows[2][12] != contacts.MethodNoSite {
		t.Errorf("a company with no website must be empty and say why: %v", rows[2])
	}
}

// Without an enricher the export still writes — it just carries what the
// search returned.
func TestExport_WithoutAnEnricher(t *testing.T) {
	p := exportPipeline(t, nil)

	res, err := p.Export(context.Background(), ExportRequest{Report: exportReport(), Enrich: true})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if res.Enriched {
		t.Errorf("an export with no enricher must not claim it enriched: %+v", res)
	}
}

func TestExport_EmptyReportIsAnError(t *testing.T) {
	p := exportPipeline(t, nil)
	if _, err := p.Export(context.Background(), ExportRequest{Report: Report{Region: "x"}}); err == nil {
		t.Fatal("an empty report must not produce a file")
	}
}

// Excel's own limits: 31 characters, no :\/?*[], and no two sheets with one
// name. A category that violates them must not cost the export a sheet.
func TestSheetName_ObeysExcelsLimits(t *testing.T) {
	used := map[string]bool{}
	long := strings.Repeat("kategori", 6)

	first := sheetName(long, used)
	if len([]rune(first)) != 31 {
		t.Errorf("a long name must be cut to 31 runes: %q", first)
	}
	second := sheetName(long, used)
	if second == first {
		t.Errorf("a collision must be resolved, both were %q", first)
	}
	if len([]rune(second)) > 31 {
		t.Errorf("the counter must not push it past 31: %q", second)
	}
	if got := sheetName("a/b:c[d]", used); strings.ContainsAny(got, `:\/?*[]`) {
		t.Errorf("forbidden characters survived: %q", got)
	}
}

// Two exports of one region must not overwrite each other.
func TestExportFileName_IsUniquePerRun(t *testing.T) {
	rep := exportReport()
	at := time.Date(2026, 9, 2, 21, 4, 5, 0, time.UTC)

	name := exportFileName(rep, at)
	if !strings.HasPrefix(name, "Kadıköy-20260902-") || filepath.Ext(name) != ".xlsx" {
		t.Errorf("file name: %q", name)
	}
	if other := exportFileName(rep, at.Add(time.Second)); other == name {
		t.Errorf("two runs produced the same file name: %q", name)
	}
}
