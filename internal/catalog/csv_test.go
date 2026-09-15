package catalog

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("fikstür okunamadı: %v", err)
	}
	return b
}

func parseFixture(t *testing.T, name string) (File, []byte) {
	t.Helper()
	raw := readFixture(t, name)
	f, err := ParseCSV(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ParseCSV(%s): %v", name, err)
	}
	return f, raw
}

// The contract the whole tool rests on. The operator hands this file back to
// the same admin panel that produced it; a changed delimiter, a changed
// encoding or a re-quoted cell is a rejected import and a tool nobody uses.
func TestExport_IsByteIdenticalWhenNothingWasApproved(t *testing.T) {
	for _, name := range []string{"shopify.csv", "ikas.csv", "ikas-cp1254.csv"} {
		f, raw := parseFixture(t, name)
		products := Products("imp_test", f)

		got, err := Export(f, products, nil)
		if err != nil {
			t.Fatalf("Export(%s): %v", name, err)
		}
		if !bytes.Equal(raw, got) {
			t.Errorf("%s bayt bayt aynı değil\n--- beklenen (%d bayt) ---\n%q\n--- alınan (%d bayt) ---\n%q",
				name, len(raw), raw, len(got), got)
		}
	}
}

func TestParseCSV_ReadsTheFramingOfEachDialect(t *testing.T) {
	shopify, _ := parseFixture(t, "shopify.csv")
	if shopify.Delimiter != ',' || shopify.Quoting != QuoteMinimal ||
		shopify.Encoding != EncodingUTF8 || shopify.HasBOM || !shopify.CRLF {
		t.Errorf("shopify çerçevesi yanlış okundu: %+v", framing(shopify))
	}
	if shopify.Dialect.Key != "shopify" {
		t.Errorf("shopify lehçesi algılanmadı: %q", shopify.Dialect.Key)
	}

	// The framing of a real IKAS export, down to the BOM: comma-delimited
	// UTF-8 with CRLF line endings and every cell quoted, filled or not.
	ikas, _ := parseFixture(t, "ikas.csv")
	if ikas.Delimiter != ',' || ikas.Quoting != QuoteAll ||
		ikas.Encoding != EncodingUTF8 || !ikas.HasBOM || !ikas.CRLF {
		t.Errorf("ikas çerçevesi yanlış okundu: %+v", framing(ikas))
	}
	if ikas.Dialect.Key != "ikas" {
		t.Errorf("ikas lehçesi algılanmadı: %q", ikas.Dialect.Key)
	}
}

// A Turkish-locale Excel writes Windows-1254. Reading it as UTF-8 turns every
// ş and ğ into a replacement character, and the operator finds out when the
// product page is live.
func TestParseCSV_ReadsWindows1254(t *testing.T) {
	f, _ := parseFixture(t, "ikas-cp1254.csv")
	if f.Encoding != EncodingCP1254 {
		t.Fatalf("beklenen windows-1254, alınan %q", f.Encoding)
	}
	products := Products("imp", f)
	if len(products) != 1 {
		t.Fatalf("beklenen 1 ürün, alınan %d", len(products))
	}
	if got := products[0].Original.Title; got != "Eşarp" {
		t.Errorf("Türkçe harfler bozuldu: %q", got)
	}
}

// A rewrite that introduces a character the source encoding cannot carry must
// say so. Dropping it silently hands back a file whose text is not what the
// operator approved.
func TestExport_RefusesContentTheSourceEncodingCannotCarry(t *testing.T) {
	f, _ := parseFixture(t, "ikas-cp1254.csv")
	products := Products("imp", f)
	approved := map[string]Content{
		products[0].ID: {Title: "Eşarp 中文", DescriptionHTML: products[0].Original.DescriptionHTML},
	}
	if _, err := Export(f, products, approvedSource(approved)); err == nil {
		t.Fatal("windows-1254'e sığmayan içerik sessizce yazıldı")
	}
}

func TestExport_TouchesOnlyTheApprovedCells(t *testing.T) {
	f, raw := parseFixture(t, "shopify.csv")
	products := Products("imp", f)

	var tisort Product
	for _, p := range products {
		if p.Key == "pamuklu-tisort" {
			tisort = p
		}
	}
	if tisort.ID == "" {
		t.Fatal("ürün bulunamadı")
	}

	next := tisort.Original
	next.SEOTitle = "Pamuklu Tişört — Yeni"
	got, err := Export(f, products, approvedSource(map[string]Content{tisort.ID: next}))
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	if !bytes.Contains(got, []byte("Pamuklu Tişört — Yeni")) {
		t.Error("onaylanan değişiklik yazılmamış")
	}
	// Everything else, including the variant rows and the other product, is
	// the file that came in.
	for _, untouched := range []string{"TS-M", "TS-L", "Deri Çanta", "El yapımı"} {
		if !bytes.Contains(got, []byte(untouched)) {
			t.Errorf("dokunulmaması gereken %q kaybolmuş", untouched)
		}
	}
	if bytes.Count(got, []byte("\r\n")) != bytes.Count(raw, []byte("\r\n")) {
		t.Error("satır sayısı ya da satır sonu değişmiş")
	}
}

func TestParseCSV_EmptyInputIsATypedError(t *testing.T) {
	if _, err := ParseCSV(bytes.NewReader(nil)); err == nil {
		t.Fatal("boş girdi hata vermedi")
	}
}

func TestExportFileName_IsSafeAndDated(t *testing.T) {
	at := time.Date(2026, 9, 6, 21, 4, 5, 0, time.UTC)
	got := exportFileName("ürün listesi (son).csv", at)
	if filepath.Base(got) != got {
		t.Errorf("dosya adı bir yol içeriyor: %q", got)
	}
	if got != "ürün-listesi-son-mimir-20260906-210405.csv" {
		t.Errorf("beklenmeyen ad: %q", got)
	}
}

func framing(f File) map[string]any {
	return map[string]any{
		"delimiter": string(f.Delimiter), "quoting": f.Quoting,
		"encoding": f.Encoding, "bom": f.HasBOM, "crlf": f.CRLF,
		"trailing_newline": f.TrailingNewline,
	}
}
