package leadgen

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/logrenant/mimir/internal/contacts"
	"github.com/logrenant/mimir/internal/maps"
	"github.com/xuri/excelize/v2"
)

// ContactEnricher fills in phone and email from a company's own website.
// *contacts.Enricher satisfies it; nil means the export writes what the search
// returned and nothing more.
type ContactEnricher interface {
	Enrich(ctx context.Context, companies []maps.Company) map[string]contacts.Enriched
}

// ExportRequest is one workbook to write.
type ExportRequest struct {
	// Report is what the pipeline produced. Its companies, categories and
	// provenance are the whole content of the file.
	Report Report
	// Enrich opens each company's website for contact details. Off by default
	// because it is a page fetch per company.
	Enrich bool
	// Dir is where the file is written. Empty means the configured export
	// directory.
	Dir string
}

// ExportResult names the file that was written.
type ExportResult struct {
	Path      string   `json:"path"`
	Sheets    []string `json:"sheets"`
	Companies int      `json:"companies"`
	WithPhone int      `json:"with_phone"`
	WithEmail int      `json:"with_email"`
	WithSite  int      `json:"with_website"`
	Enriched  bool     `json:"enriched"`
}

// exportColumns is the header row, and the order every sheet uses.
var exportColumns = []string{
	"Şirket", "Kategori", "Web sitesi var mı", "Web sitesi", "Telefon", "E-posta",
	"Adres", "Puan", "Yorum", "Enlem", "Boylam", "Kaynak", "İletişim yöntemi", "Not", "place_id",
}

// Export writes one workbook: a summary sheet, then one sheet per category.
//
// A sheet per category rather than one table with a category column, because
// the question this file answers is asked one category at a time — "who are the
// dentists, and which of them has no website" — and a spreadsheet that has to
// be filtered first answers it worse.
//
// Every cell is something a source said. An empty phone column means the
// lookup found nothing, and the "İletişim yöntemi" column says which tier
// looked, so the emptiness can be read rather than guessed at.
func (p *Pipeline) Export(ctx context.Context, req ExportRequest) (ExportResult, error) {
	rep := req.Report
	if len(rep.Companies) == 0 {
		return ExportResult{}, fmt.Errorf("leadgen: nothing to export")
	}

	enriched := map[string]contacts.Enriched{}
	if req.Enrich && p.contacts != nil {
		enriched = p.contacts.Enrich(ctx, companiesOf(rep))
	}

	dir := strings.TrimSpace(req.Dir)
	if dir == "" {
		dir = p.cfg.ExportDir
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ExportResult{}, fmt.Errorf("leadgen: creating the export directory: %w", err)
	}

	f := excelize.NewFile()
	defer func() { _ = f.Close() }()

	byCategory := map[Category][]CompanyLead{}
	for _, lead := range rep.Companies {
		byCategory[lead.Category] = append(byCategory[lead.Category], lead)
	}
	cats := make([]Category, 0, len(byCategory))
	for cat := range byCategory {
		cats = append(cats, cat)
	}
	sort.Slice(cats, func(i, j int) bool { return string(cats[i]) < string(cats[j]) })

	res := ExportResult{Enriched: req.Enrich && p.contacts != nil}

	// The default sheet becomes the summary, so the workbook opens on it.
	const summary = "Özet"
	if err := f.SetSheetName(f.GetSheetName(0), summary); err != nil {
		return ExportResult{}, err
	}
	res.Sheets = append(res.Sheets, summary)

	used := map[string]bool{summary: true}
	for _, cat := range cats {
		name := sheetName(string(cat), used)
		if _, err := f.NewSheet(name); err != nil {
			return ExportResult{}, err
		}
		res.Sheets = append(res.Sheets, name)

		rows := byCategory[cat]
		if err := writeHeader(f, name); err != nil {
			return ExportResult{}, err
		}
		for i, lead := range rows {
			row := buildRow(lead, enriched[lead.PlaceID])
			if row.hasPhone {
				res.WithPhone++
			}
			if row.hasEmail {
				res.WithEmail++
			}
			if row.hasSite {
				res.WithSite++
			}
			res.Companies++
			cell, err := excelize.CoordinatesToCellName(1, i+2)
			if err != nil {
				return ExportResult{}, err
			}
			if err := f.SetSheetRow(name, cell, &row.values); err != nil {
				return ExportResult{}, err
			}
		}
		if err := f.SetColWidth(name, "A", "A", 42); err != nil {
			return ExportResult{}, err
		}
		if err := f.SetColWidth(name, "D", "G", 30); err != nil {
			return ExportResult{}, err
		}
		if err := f.AutoFilter(name, "A1:O1", nil); err != nil {
			return ExportResult{}, err
		}
	}

	if err := writeSummary(f, summary, rep, res, cats, byCategory); err != nil {
		return ExportResult{}, err
	}

	path := filepath.Join(dir, exportFileName(rep, time.Now()))
	if err := f.SaveAs(path); err != nil {
		return ExportResult{}, fmt.Errorf("leadgen: writing %s: %w", path, err)
	}
	res.Path = path
	return res, nil
}

type exportRow struct {
	values                      []any
	hasPhone, hasEmail, hasSite bool
}

func buildRow(lead CompanyLead, en contacts.Enriched) exportRow {
	phone := strings.TrimSpace(lead.Phone)
	if phone == "" {
		phone = en.Phone
	}
	address := strings.TrimSpace(lead.Address)
	if address == "" {
		address = en.Address
	}
	site := strings.TrimSpace(lead.Website)

	// "Var mı" as a word rather than a formula: the column is read by a human
	// scanning for the companies to call, and "hayır" scans faster than FALSE.
	hasSite := site != ""
	siteLabel := "hayır"
	if hasSite {
		siteLabel = "evet"
	}

	return exportRow{
		hasPhone: phone != "",
		hasEmail: en.Email != "",
		hasSite:  hasSite,
		values: []any{
			lead.Name,
			string(lead.Category),
			siteLabel,
			site,
			phone,
			en.Email,
			address,
			lead.Rating,
			lead.ReviewCount,
			lead.Latitude,
			lead.Longitude,
			lead.Source,
			en.Method,
			en.Note,
			lead.PlaceID,
		},
	}
}

func writeHeader(f *excelize.File, sheet string) error {
	header := make([]any, len(exportColumns))
	for i, c := range exportColumns {
		header[i] = c
	}
	if err := f.SetSheetRow(sheet, "A1", &header); err != nil {
		return err
	}
	style, err := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	if err != nil {
		return err
	}
	return f.SetRowStyle(sheet, 1, 1, style)
}

func writeSummary(f *excelize.File, sheet string, rep Report, res ExportResult, cats []Category, byCategory map[Category][]CompanyLead) error {
	rows := [][]any{
		{"Bölge", rep.Region},
		{"Arama", rep.Query},
		{"Kaynak", sourceLabel(rep)},
		{"Şirket sayısı", res.Companies},
		{"Web sitesi olan", res.WithSite},
		{"Telefonu bulunan", res.WithPhone},
		{"E-postası bulunan", res.WithEmail},
		{"İletişim zenginleştirme", enrichedLabel(res.Enriched)},
		{"Oluşturma", time.Now().Format("2006-01-02 15:04")},
		{},
		{"Kategori", "Şirket", "Web sitesi olan"},
	}
	for _, cat := range cats {
		withSite := 0
		for _, lead := range byCategory[cat] {
			if strings.TrimSpace(lead.Website) != "" {
				withSite++
			}
		}
		rows = append(rows, []any{string(cat), len(byCategory[cat]), withSite})
	}

	for i, row := range rows {
		cell, err := excelize.CoordinatesToCellName(1, i+1)
		if err != nil {
			return err
		}
		r := row
		if err := f.SetSheetRow(sheet, cell, &r); err != nil {
			return err
		}
	}
	return f.SetColWidth(sheet, "A", "C", 26)
}

func sourceLabel(rep Report) string {
	switch {
	case rep.FromCache:
		return "önbellek"
	case rep.Source == maps.SourcePlacesAPI:
		return "Google Places API (faturalı)"
	case rep.Source == "":
		return "bilinmiyor"
	default:
		return rep.Source + " (ücretsiz)"
	}
}

func enrichedLabel(on bool) string {
	if on {
		return "açık — web siteleri tarandı"
	}
	return "kapalı — yalnızca aramadan gelen alanlar"
}

func companiesOf(rep Report) []maps.Company {
	out := make([]maps.Company, 0, len(rep.Companies))
	for _, lead := range rep.Companies {
		out = append(out, maps.Company{
			PlaceID:          lead.PlaceID,
			Name:             lead.Name,
			FormattedAddress: lead.Address,
			Website:          lead.Website,
			Phone:            lead.Phone,
		})
	}
	return out
}

// sheetNameUnsafe are the characters Excel forbids in a sheet name.
var sheetNameUnsafe = regexp.MustCompile(`[:\\/?*\[\]]`)

// sheetName makes a category into a legal, unique sheet name. Excel's limit is
// 31 characters and its forbidden set is small; a collision after truncation is
// resolved with a counter rather than by dropping a category.
func sheetName(cat string, used map[string]bool) string {
	name := strings.TrimSpace(sheetNameUnsafe.ReplaceAllString(cat, "-"))
	if name == "" {
		name = "kategori"
	}
	if len([]rune(name)) > 31 {
		name = string([]rune(name)[:31])
	}
	base := name
	for i := 2; used[name]; i++ {
		suffix := fmt.Sprintf(" %d", i)
		trimmed := base
		if len([]rune(trimmed))+len(suffix) > 31 {
			trimmed = string([]rune(trimmed)[:31-len(suffix)])
		}
		name = trimmed + suffix
	}
	used[name] = true
	return name
}

// exportFileName is region and timestamp, so two exports of the same region
// never overwrite each other.
func exportFileName(rep Report, now time.Time) string {
	base := rep.Region
	if base == "" {
		base = rep.Query
	}
	base = sheetNameUnsafe.ReplaceAllString(base, "-")
	base = strings.Join(strings.Fields(base), "-")
	if base == "" {
		base = "leadgen"
	}
	if len([]rune(base)) > 40 {
		base = string([]rune(base)[:40])
	}
	return fmt.Sprintf("%s-%s.xlsx", base, now.Format("20060102-150405"))
}
