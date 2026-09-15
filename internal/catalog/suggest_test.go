package catalog

import (
	"strings"
	"testing"
)

// The header these two tests read is the one a real IKAS store downloads. The
// profile that stood here before task-91 was guessed from memory — "Ürün Adı",
// "Stok Kodu", "Kategori" — and matched nothing anyone actually exports, so an
// operator's own catalogue came up TANINMADI in front of them.
func TestDetect_ReadsARealIkasProductExport(t *testing.T) {
	f, _ := parseFixture(t, "ikas.csv")
	if f.Dialect.Key != "ikas" {
		t.Fatalf("ikas lehçesi algılanmadı: %q", f.Dialect.Key)
	}
	want := map[Field]string{
		FieldGroupID:         "Ürün Grup ID",
		FieldHandle:          "Slug",
		FieldSKU:             "SKU",
		FieldTitle:           "İsim",
		FieldDescriptionHTML: "Açıklama",
		FieldSEOTitle:        "Metadata Başlık",
		FieldSEODescription:  "Metadata Açıklama",
		FieldTags:            "Etiketler",
		FieldCategory:        "Kategoriler",
	}
	for field, col := range want {
		if got := f.Dialect.Columns[field]; got != col {
			t.Errorf("%s sütunu %q bekleniyordu, %q geldi", field, col, got)
		}
	}

	products := Products("imp", f)
	if len(products) != 2 {
		t.Fatalf("beklenen 2 ürün, alınan %d", len(products))
	}
	if products[0].Original.Title != "Nemlendirici Krem" {
		t.Errorf("başlık okunmadı: %q", products[0].Original.Title)
	}
	if products[0].Original.SEODescription == "" {
		t.Error("Metadata Açıklama okunmadı")
	}
}

// IKAS offers a second export — custom fields — where a store keeps the long
// rich-text detail. It is one title and one HTML body, and it is content this
// tool has something to say about.
func TestDetect_ReadsTheIkasCustomFieldsExport(t *testing.T) {
	f, _ := parseFixture(t, "ikas-fields.csv")
	if f.Dialect.Key != "ikas-fields" {
		t.Fatalf("özel alanlar lehçesi algılanmadı: %q", f.Dialect.Key)
	}
	products := Products("imp", f)
	if len(products) != 2 {
		t.Fatalf("beklenen 2 ürün, alınan %d", len(products))
	}
	if !strings.Contains(products[0].Original.DescriptionHTML, "<h3>Özellikler</h3>") {
		t.Errorf("Html:Detay okunmadı: %q", products[0].Original.DescriptionHTML)
	}
	// The products export and the custom-fields export share Ürün Grup ID and
	// İsim; only one of them has Açıklama and SKU. Detection must not read the
	// narrower file as the wider one and then find nine empty columns.
	if f.Dialect.Columns[FieldSKU] != "" {
		t.Error("özel alanlar dosyasında olmayan bir SKU sütunu bağlandı")
	}
}

// A header no profile matched is not the end of the road: the guess costs
// nothing and turns eight empty dropdowns into eight the operator only has to
// check. It is a suggestion — nothing here writes a mapping.
func TestSuggest_MapsAnUnknownHeaderWithNoModelCall(t *testing.T) {
	got := Suggest([]string{
		"id", "Product Name", "Description", "Meta Title", "Meta Description",
		"Tags", "Categories", "Barcode", "permalink",
	})
	want := map[Field]string{
		FieldTitle:           "Product Name",
		FieldDescriptionHTML: "Description",
		FieldSEOTitle:        "Meta Title",
		FieldSEODescription:  "Meta Description",
		FieldTags:            "Tags",
		FieldCategory:        "Categories",
		FieldSKU:             "Barcode",
		FieldHandle:          "permalink",
	}
	for field, col := range want {
		if got[field] != col {
			t.Errorf("%s için %q bekleniyordu, %q geldi", field, col, got[field])
		}
	}
}

// "Açıklama" and "Metadata Açıklama" both mean description to a loose reader.
// Mapping the same column to two fields would silently make the SEO
// description a copy of the body, so the first field to claim a column keeps
// it and the other looks further down its own list.
func TestSuggest_NeverGivesOneColumnToTwoFields(t *testing.T) {
	got := Suggest([]string{"İsim", "Açıklama", "Metadata Açıklama", "Metadata Başlık"})
	if got[FieldDescriptionHTML] != "Açıklama" {
		t.Errorf("açıklama %q", got[FieldDescriptionHTML])
	}
	if got[FieldSEODescription] != "Metadata Açıklama" {
		t.Errorf("SEO açıklama %q", got[FieldSEODescription])
	}
	seen := map[string]Field{}
	for f, col := range got {
		if prev, dup := seen[col]; dup {
			t.Errorf("%q sütunu hem %s hem %s alanına verildi", col, prev, f)
		}
		seen[col] = f
	}
}

// The gate the screen reads. A mapped file has no dialect and is perfectly
// readable; reading the gate off the dialect is what left an operator staring
// at the mapping form they had just saved.
func TestReadable_IsTrueForAMappedFileWithNoDialect(t *testing.T) {
	f := File{Header: []string{"a", "b"}}
	if f.Readable() {
		t.Error("eşlemesiz, lehçesiz dosya okunabilir sayıldı")
	}
	f.Mapping = map[Field]string{FieldSKU: "a"}
	if f.Readable() {
		t.Error("yalnız SKU eşlenmiş dosya okunabilir sayıldı")
	}
	f.Mapping[FieldTitle] = "b"
	if !f.Readable() {
		t.Error("başlığı eşlenmiş dosya okunabilir sayılmadı")
	}
}

// A column in a thirty-seven column export is told apart by what is in it. The
// cut is on rune boundaries: a byte cut through "ş" would put mojibake in the
// dropdown of the very screen that exists to make a Turkish export legible.
func TestSample_TrimsOnRuneBoundaries(t *testing.T) {
	f, _ := parseFixture(t, "ikas.csv")
	s := f.Sample(20)
	if s["İsim"] != "Nemlendirici Krem" {
		t.Errorf("örnek değer %q", s["İsim"])
	}
	desc := s["Açıklama"]
	if !strings.HasSuffix(desc, "…") {
		t.Errorf("uzun değer kırpılmadı: %q", desc)
	}
	if strings.ContainsRune(desc, '�') {
		t.Errorf("kırpma bir rune'u böldü: %q", desc)
	}
	if _, ok := s["Desi"]; ok {
		t.Error("boş sütun için örnek gönderildi")
	}
}

// A profile added later applies to files imported before it. Detection is a
// pure function of the header and the profile table is code, so an operator
// whose export came up unrecognised in task-85 does not have to re-upload a
// thousand products to benefit from the profile that now reads it.
func TestStoredImport_ReDetectsADialectAddedAfterTheUpload(t *testing.T) {
	f, _ := parseFixture(t, "ikas.csv")
	// The row as the old binary would have written it: a real IKAS header,
	// and no dialect, because no profile matched at the time.
	f.Dialect = Dialect{}
	stored := Import{ID: "imp_1", Filename: "ikas.csv", File: f}.stored()

	back, err := stored.value()
	if err != nil {
		t.Fatalf("value: %v", err)
	}
	if back.File.Dialect.Key != "ikas" {
		t.Errorf("sonradan eklenen profil eski satıra uygulanmadı: %q", back.File.Dialect.Key)
	}

	// But an operator's own mapping is not ours to discard: it exists because
	// we failed them once.
	f.Mapping = map[Field]string{FieldTitle: "İsim"}
	stored = Import{ID: "imp_2", Filename: "ikas.csv", File: f}.stored()
	back, err = stored.value()
	if err != nil {
		t.Fatalf("value: %v", err)
	}
	if !back.File.Dialect.IsZero() {
		t.Error("operatörün eşlemesi bir profille ezildi")
	}
	if back.File.Mapping[FieldTitle] != "İsim" {
		t.Errorf("eşleme kayboldu: %+v", back.File.Mapping)
	}
}
