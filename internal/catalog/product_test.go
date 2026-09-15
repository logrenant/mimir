package catalog

import "testing"

// Shopify writes one row per variant and fills Title and Body (HTML) only on
// the first. Reading row by row would produce one real product followed by two
// empty ones, and a bulk rewrite would then spend three model calls to write
// two blank descriptions.
func TestShopifyVariantRowsCollapseIntoOneProduct(t *testing.T) {
	f, _ := parseFixture(t, "shopify.csv")
	products := Products("imp", f)

	if len(products) != 2 {
		t.Fatalf("beklenen 2 ürün, alınan %d", len(products))
	}

	var tisort Product
	for _, p := range products {
		if p.Key == "pamuklu-tisort" {
			tisort = p
		}
	}
	if len(tisort.Rows) != 3 {
		t.Errorf("beklenen 3 satır, alınan %d", len(tisort.Rows))
	}
	if tisort.Original.Title != "Pamuklu Tişört" {
		t.Errorf("başlık ilk satırdan okunmadı: %q", tisort.Original.Title)
	}
	if tisort.Original.DescriptionHTML == "" {
		t.Error("açıklama ilk satırdan okunmadı")
	}
	if tisort.SKU != "TS-S" {
		t.Errorf("beklenen ilk varyantın SKU'su, alınan %q", tisort.SKU)
	}
}

func TestProducts_OneRowPerProductWhenTheDialectDoesNotGroup(t *testing.T) {
	f, _ := parseFixture(t, "ikas.csv")
	products := Products("imp", f)
	if len(products) != 2 {
		t.Fatalf("beklenen 2 ürün, alınan %d", len(products))
	}
	for _, p := range products {
		if len(p.Rows) != 1 {
			t.Errorf("%s: beklenen 1 satır, alınan %d", p.SKU, len(p.Rows))
		}
	}
}

// A stable id is what makes a stored draft survive a re-import of a corrected
// export. A random one would orphan every draft the operator had approved.
func TestProductID_IsStableAcrossReimportsOfTheSameFile(t *testing.T) {
	f, _ := parseFixture(t, "ikas.csv")
	first := Products("imp_a", f)
	again := Products("imp_a", f)
	other := Products("imp_b", f)

	for i := range first {
		if first[i].ID != again[i].ID {
			t.Errorf("aynı import iki kez farklı id üretti: %s vs %s", first[i].ID, again[i].ID)
		}
		if first[i].ID == other[i].ID {
			t.Error("iki farklı import aynı id'yi paylaşıyor")
		}
	}
}

// Two products with no grouping key must not collapse into one.
func TestProducts_BlankKeysDoNotCollapse(t *testing.T) {
	f := File{
		Header:  []string{"Handle", "Title"},
		Rows:    [][]string{{"", "Bir"}, {"", "İki"}},
		Dialect: Dialect{Key: "x", GroupBy: FieldHandle, Columns: map[Field]string{FieldHandle: "Handle", FieldTitle: "Title"}},
	}
	if got := len(Products("imp", f)); got != 2 {
		t.Errorf("beklenen 2 ürün, alınan %d", got)
	}
}

func TestField_HandleAndSKUAreNotWritable(t *testing.T) {
	// A handle is the product's URL. Rewriting it turns every link and every
	// ranking that pointed at the old one into a 404.
	for _, f := range []Field{FieldHandle, FieldSKU, FieldCategory} {
		if f.Writable() {
			t.Errorf("%s yazılabilir işaretlenmiş", f)
		}
	}
	for _, f := range []Field{FieldTitle, FieldDescriptionHTML, FieldSEOTitle, FieldSEODescription, FieldTags} {
		if !f.Writable() {
			t.Errorf("%s yazılabilir değil", f)
		}
	}
}

func TestDetect_MatchesASignatureAsASubsetOfTheHeader(t *testing.T) {
	// A real store has metafield columns the profile never heard of.
	header := []string{
		"Handle", "Title", "Body (HTML)", "Variant SKU",
		"Metafield: custom.material [single_line_text_field]", "Cost per item",
	}
	d, ok := Detect(header)
	if !ok || d.Key != "shopify" {
		t.Fatalf("ek sütunlar algılamayı bozdu: %q %v", d.Key, ok)
	}
	// An optional column the store never filled in resolves to nothing rather
	// than to a guess.
	if _, present := d.Columns[FieldSEOTitle]; present {
		t.Error("dosyada olmayan SEO sütunu eşlenmiş")
	}
}

func TestDetect_UnknownHeaderIsNotAnError(t *testing.T) {
	if _, ok := Detect([]string{"foo", "bar"}); ok {
		t.Error("tanınmayan başlık bir lehçeye eşlendi")
	}
}

// With no dialect, the operator's own mapping is what the file is read through.
func TestFile_UsesTheOperatorMappingWhenNoDialectMatched(t *testing.T) {
	f := File{
		Header:  []string{"kod", "ad", "metin"},
		Rows:    [][]string{{"A1", "Ürün", "<p>x</p>"}},
		Mapping: map[Field]string{FieldSKU: "kod", FieldTitle: "ad", FieldDescriptionHTML: "metin"},
	}
	products := Products("imp", f)
	if len(products) != 1 || products[0].Original.Title != "Ürün" {
		t.Fatalf("elle eşleme okunmadı: %+v", products)
	}
}
