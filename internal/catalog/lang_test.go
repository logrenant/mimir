package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/llm"
)

// The source spelling is what every column map an operator already saved
// holds. If it changed, those maps would stop parsing and their imports would
// fall back to the manual mapping form with the work already done.
func TestLangField_TheSourceSpellingIsTheBareFieldName(t *testing.T) {
	for _, f := range Fields() {
		lf := LangField{Field: f, Lang: LangSource}
		if got := lf.String(); got != string(f) {
			t.Errorf("%s kaynak dilde %q olarak yazıldı", f, got)
		}
		back, ok := ParseLangField(string(f))
		if !ok || back != lf {
			t.Errorf("%s geri okunamadı: %+v ok=%v", f, back, ok)
		}
	}
}

func TestParseLangField_RoundTripsAndRefusesWhatThisBinaryDoesNotCarry(t *testing.T) {
	for _, want := range LangFields(Langs()) {
		got, ok := ParseLangField(want.String())
		if !ok || got != want {
			t.Errorf("%q geri okunamadı: %+v ok=%v", want.String(), got, ok)
		}
	}
	for _, bad := range []string{"description_html@de", "renk@ar", "@ar", "price"} {
		if _, ok := ParseLangField(bad); ok {
			t.Errorf("%q kabul edildi", bad)
		}
	}
}

// A store whose export has no Arabic column must not be offered an Arabic
// pass: there would be nowhere to write the answer, and the operator would pay
// for copy that cannot be exported.
func TestFileLangs_OnlyOffersALanguageTheFileHasAColumnFor(t *testing.T) {
	for _, tc := range []struct {
		file string
		want []Lang
	}{
		{"ikas-fields.csv", []Lang{LangSource, LangAR}},
		{"shopify.csv", []Lang{LangSource}},
		{"ikas.csv", []Lang{LangSource}},
	} {
		f, _ := parseFixture(t, tc.file)
		got := f.Langs()
		if len(got) != len(tc.want) {
			t.Errorf("%s: beklenen %v, alınan %v", tc.file, tc.want, got)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: beklenen %v, alınan %v", tc.file, tc.want, got)
				break
			}
		}
	}
}

// What the file already says in a target language is read, not ignored. An
// export that overwrote it would throw away copy somebody wrote by hand.
func TestProducts_ReadsWhatTheFileAlreadySaysInEachLanguage(t *testing.T) {
	f, _ := parseFixture(t, "ikas-fields.csv")
	products := Products("imp_1", f)
	if len(products) != 2 {
		t.Fatalf("beklenen 2 ürün, alınan %d", len(products))
	}

	krem := products[0]
	if !strings.Contains(krem.Original.DescriptionHTML, "Kuru ciltler") {
		t.Errorf("kaynak dil açıklaması okunmadı: %q", krem.Original.DescriptionHTML)
	}
	ar := krem.Content(LangAR).DescriptionHTML
	if !strings.Contains(ar, "مرطب مكثف") {
		t.Errorf("Arapça açıklama okunmadı: %q", ar)
	}

	// The second row's Arabic cell is empty, which is what a half-translated
	// catalog looks like. Empty must read as empty, not as the Turkish copy.
	if got := products[1].Content(LangAR).DescriptionHTML; got != "" {
		t.Errorf("boş Arapça hücre boş okunmadı: %q", got)
	}
}

// The whole claim of this package, now with a second language in the file.
func TestExport_WritesOnlyTheApprovedLanguagesCell(t *testing.T) {
	f, _ := parseFixture(t, "ikas-fields.csv")
	products := Products("imp_1", f)
	krem := products[0]

	next := krem.Content(LangAR)
	next.DescriptionHTML = "<h3>المواصفات</h3><p>مرطب غني للبشرة شديدة الجفاف.</p>"

	out, err := Export(f, products, map[string]map[Lang]Content{
		krem.ID: {LangAR: next},
	})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	got := string(out)

	if !strings.Contains(got, "مرطب غني") {
		t.Error("onaylanan Arapça değişiklik yazılmadı")
	}
	// Everything else is copied, including the Turkish body of the same row
	// and the untranslated second product.
	if !strings.Contains(got, "Kuru ciltler için yoğun nemlendirici") {
		t.Error("aynı satırın Türkçe gövdesi bozuldu")
	}
	if !strings.Contains(got, "Yıpranmış saçlar için") {
		t.Error("onaylanmayan ürün değişti")
	}
	if strings.Contains(got, "مرطب مكثف") {
		t.Error("eski Arapça değer yerinde kaldı")
	}
}

// Approving one language must not ship another.
func TestExport_LeavesTheOtherLanguagesCellAlone(t *testing.T) {
	f, _ := parseFixture(t, "ikas-fields.csv")
	products := Products("imp_1", f)
	krem := products[0]

	next := krem.Original
	next.Title = "Yoğun Nemlendirici Krem"

	out, err := Export(f, products, map[string]map[Lang]Content{
		krem.ID: {LangSource: next},
	})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "Yoğun Nemlendirici Krem") {
		t.Error("onaylanan kaynak dil değişikliği yazılmadı")
	}
	if !strings.Contains(got, "مرطب مكثف") {
		t.Error("onaylanmamış Arapça hücreye dokunuldu")
	}
}

// A target-language column that was empty on every row has no row that
// "carried the value". Writing nowhere would make the first Arabic pass a pass
// that silently did nothing.
func TestRowsFor_AnEmptyTargetColumnFollowsTheSourceLanguagesRows(t *testing.T) {
	f, _ := parseFixture(t, "ikas-fields.csv")
	products := Products("imp_1", f)
	sampuan := products[1] // its Arabic cell is empty

	src := sampuan.rowsFor(f, LangField{Field: FieldDescriptionHTML})
	got := sampuan.rowsFor(f, LangField{Field: FieldDescriptionHTML, Lang: LangAR})
	if len(got) == 0 {
		t.Fatal("boş Arapça sütun için yazılacak satır bulunamadı")
	}
	if len(got) != len(src) || got[0] != src[0] {
		t.Errorf("Arapça yazım kaynak dilin satırlarını izlemedi: %v vs %v", got, src)
	}
}

// The profile table is code and it grows. A column added to a profile has to
// reach the imports operators already have, which is what stored.go's own
// comment claims and what its guard used to prevent.
func TestStoredImport_PicksUpAColumnAddedToAProfileAfterTheImport(t *testing.T) {
	s := New(config.Load(), newMemStore(), nil)
	ctx := context.Background()
	raw, err := os.ReadFile("testdata/ikas-fields.csv")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	imp, err := s.Import(ctx, "ikas-fields.csv", raw, llm.Selection{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	// Simulate the row as it was written before the profile named the Arabic
	// column: the dialect matched then, so it was stored bound to only the
	// columns the table knew at the time.
	stored := imp.stored()
	narrowed := imp
	narrowed.File.Dialect.Translations = nil
	stored.FileJSON = mustJSON(t, narrowed.File)

	back, err := stored.value()
	if err != nil {
		t.Fatalf("value: %v", err)
	}
	if _, ok := back.File.Dialect.Translations[LangAR]; !ok {
		t.Error("saklanmış içe aktarım profile sonradan eklenen Arapça sütunu almadı")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// The four profiles, against the four real exports they were written from.
// Ordering matters: the variant custom-fields header contains the product-level
// profile's whole signature, so a table in the wrong order silently drops the
// variant identity that file has.
func TestDetect_ReadsEveryRealIkasExport(t *testing.T) {
	for _, tc := range []struct {
		file, want string
	}{
		{"ikas.csv", "ikas"},
		{"ikas-variants.csv", "ikas"},
		{"ikas-cp1254.csv", "ikas"},
		{"ikas-fields.csv", "ikas-fields"},
		{"ikas-fields-variant.csv", "ikas-fields-variant"},
		{"ikas-ceviriler.csv", "ikas-ceviriler"},
		{"shopify.csv", "shopify"},
	} {
		f, _ := parseFixture(t, tc.file)
		if f.Dialect.Key != tc.want {
			t.Errorf("%s: beklenen %q, alınan %q", tc.file, tc.want, f.Dialect.Key)
		}
	}
}

// The variant export carries the variant's own identity. Reading it as the
// product-level profile would lose the SKU, which is the column an operator
// matches against their own system.
func TestDetect_TheVariantCustomFieldsProfileKeepsTheVariantIdentity(t *testing.T) {
	f, _ := parseFixture(t, "ikas-fields-variant.csv")
	if got := f.Dialect.Columns[FieldSKU]; got != "SKU" {
		t.Errorf("SKU sütunu bağlanmadı: %q", got)
	}
	products := Products("imp_1", f)
	if len(products) != 1 {
		t.Fatalf("iki varyant tek üründe birleşmedi: %d ürün", len(products))
	}
	if len(products[0].Rows) != 2 {
		t.Errorf("ürün iki varyant satırını taşımıyor: %v", products[0].Rows)
	}
}

// IKAS's translations export does not record which language "Çevrilecek" is.
// The operator picked it in the admin panel and the file came back without the
// answer, so this package asks rather than guessing — guessing wrong writes
// Arabic into the German column, silently, across a thousand rows.
func TestTranslationsExport_CarriesColumnsWhoseLanguageOnlyAPersonKnows(t *testing.T) {
	f, _ := parseFixture(t, "ikas-ceviriler.csv")

	if !f.PendingTarget() {
		t.Fatal("çeviri sütunları var ama dili sorulmadı")
	}
	// Until somebody answers, no target language is offered: there is a place
	// to write but nobody has said what belongs in it.
	if got := f.Langs(); len(got) != 1 || got[0] != LangSource {
		t.Errorf("dili söylenmemiş dosya için dil sunuldu: %v", got)
	}

	f.TargetLang = LangAR
	if f.PendingTarget() {
		t.Error("dil söylendikten sonra hâlâ bekliyor")
	}
	langs := f.Langs()
	if len(langs) != 2 || langs[1] != LangAR {
		t.Fatalf("dil söylendikten sonra sunulmadı: %v", langs)
	}
	if got := f.ColumnsFor(LangAR)[FieldDescriptionHTML]; got != "Çevrilecek Açıklama" {
		t.Errorf("hedef açıklama sütunu çözülmedi: %q", got)
	}
	// The slug is never a target: a handle is the product's URL, and the real
	// export has "Çevrilecek Meta Slug" empty on all 1013 rows.
	if _, ok := f.ColumnsFor(LangAR)[FieldHandle]; ok {
		t.Error("çeviri hedefine slug girdi — handle asla yazılmaz")
	}
}

// A store's export is not a fixed shape. The only honest answer to "what can be
// rewritten here" is "whatever this file has a column for".
func TestOffered_IsDerivedFromTheFileAndNotFromAConstant(t *testing.T) {
	products, _ := parseFixture(t, "ikas.csv")
	if got := products.Offered(LangSource); len(got) != 5 {
		t.Errorf("ürün export'unda beklenen 5 alan, alınan %v", got)
	}

	// The custom-fields export carries a title and one body, and its Arabic
	// surface is that body alone. Offering an Arabic SEO title here would be
	// offering a switch with nowhere to write.
	fields, _ := parseFixture(t, "ikas-fields.csv")
	if got := fields.Offered(LangAR); len(got) != 1 || got[0] != FieldDescriptionHTML {
		t.Errorf("özel alanlar export'unda Arapça yüzey yanlış: %v", got)
	}
}

// An empty configuration is "not configured", not "nothing selected". Reading
// it as nothing would make an untouched import rewrite no field at all.
func TestWrites_AnEmptyConfigurationMeansEverythingTheFileOffers(t *testing.T) {
	f, _ := parseFixture(t, "ikas.csv")
	for _, field := range f.Offered(LangSource) {
		if !f.Writes(LangField{Field: field}) {
			t.Errorf("%s yapılandırılmamış dosyada kapalı görünüyor", field)
		}
	}
	// And a field the file has no column for is never written, configured or
	// not: there is nowhere for it to go.
	if f.Writes(LangField{Field: FieldDescriptionHTML, Lang: LangAR}) {
		t.Error("sütunu olmayan alan yazılabilir görünüyor")
	}
}

func TestWrites_AConfiguredSetIsAGateAndNotADefault(t *testing.T) {
	f, _ := parseFixture(t, "ikas.csv")
	f.Write = []LangField{{Field: FieldDescriptionHTML}}

	if !f.Writes(LangField{Field: FieldDescriptionHTML}) {
		t.Error("açık bırakılan alan kapalı")
	}
	for _, off := range []Field{FieldTitle, FieldSEOTitle, FieldSEODescription, FieldTags} {
		if f.Writes(LangField{Field: off}) {
			t.Errorf("%s kapatıldığı hâlde yazılabilir", off)
		}
	}
	if got := f.WriteSet(LangSource); len(got) != 1 || got[0] != FieldDescriptionHTML {
		t.Errorf("yazılacak küme yanlış: %v", got)
	}
}

// The configuration outlives the export it was made against. Re-exporting with
// one column removed should give the operator their other switches back, not an
// error about a column they did not remove on purpose.
func TestNormalizeWrite_DropsWhatThisFileCannotCarryAndKeepsTheRest(t *testing.T) {
	f, _ := parseFixture(t, "ikas-fields.csv")
	got := normalizeWrite(f, []LangField{
		{Field: FieldTitle},
		{Field: FieldDescriptionHTML},
		{Field: FieldDescriptionHTML, Lang: LangAR},
		{Field: FieldSEOTitle},               // no column in this export
		{Field: FieldSEOTitle, Lang: LangAR}, // nor in its Arabic surface
		{Field: FieldTitle},                  // a duplicate
	})
	want := []LangField{
		{Field: FieldTitle},
		{Field: FieldDescriptionHTML},
		{Field: FieldDescriptionHTML, Lang: LangAR},
	}
	if len(got) != len(want) {
		t.Fatalf("beklenen %v, alınan %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("beklenen %v, alınan %v", want, got)
		}
	}
}

// The wire spelling is what an operator reads in a database row, and it is what
// an older binary parses the source-language half of.
func TestLangField_SerialisesAsItsWireSpelling(t *testing.T) {
	b, err := json.Marshal([]LangField{
		{Field: FieldTitle},
		{Field: FieldDescriptionHTML, Lang: LangAR},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != `["title","description_html@ar"]` {
		t.Errorf("beklenmeyen yazım: %s", b)
	}
	var back []LangField
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(back) != 2 || back[1].Lang != LangAR {
		t.Errorf("geri okunamadı: %+v", back)
	}
}

// The correction form's whole promise. A profile is a guess about somebody
// else's export: it matched this header, it does not follow that it named every
// column right. Before this, a map saved over a matched file was stored, the
// file was re-read — and every read still went through the profile, so the
// operator watched their correction do nothing and had no way to tell.
func TestColumnsFor_TheOperatorsMapOutranksTheProfileThatMatched(t *testing.T) {
	f, _ := parseFixture(t, "ikas.csv")
	if f.Dialect.IsZero() {
		t.Fatal("fikstür tanınmadı; testin öncülü yok")
	}
	if got := f.ColumnsFor(LangSource)[FieldDescriptionHTML]; got != "Açıklama" {
		t.Fatalf("profilin kendi sütunu okunmuyor: %q", got)
	}

	// The operator says: this store's real body is in the metadata column.
	f.Mapping = map[Field]string{
		FieldTitle:           "İsim",
		FieldDescriptionHTML: "Metadata Açıklama",
	}
	if got := f.ColumnsFor(LangSource)[FieldDescriptionHTML]; got != "Metadata Açıklama" {
		t.Errorf("düzeltme yok sayıldı: %q", got)
	}
	// And it replaces rather than merges: a field they cleared has to come back
	// unmapped, not silently repopulated from the profile.
	if _, ok := f.ColumnsFor(LangSource)[FieldTags]; ok {
		t.Error("formda boşaltılan sütun profilden geri geldi")
	}

	// The profile's other languages are untouched — a source-language
	// correction is not a statement about the Arabic column.
	fields, _ := parseFixture(t, "ikas-fields.csv")
	fields.Mapping = map[Field]string{FieldDescriptionHTML: "Html:Detay"}
	if got := fields.ColumnsFor(LangAR)[FieldDescriptionHTML]; got != "Html:Detay-AR" {
		t.Errorf("kaynak dil düzeltmesi Arapça sütunu düşürdü: %q", got)
	}
}

// The round trip is the tool's licence to exist: the operator hands the result
// back to the same admin panel that produced it. Every fixture here has a real
// export's header row, so "every export imports back in exactly the shape it
// left" is asserted against all seven shapes rather than against the three it
// used to cover.
func TestExport_EveryRealExportShapeComesBackByteForByte(t *testing.T) {
	for _, name := range []string{
		"shopify.csv", "ikas.csv", "ikas-variants.csv", "ikas-cp1254.csv",
		"ikas-fields.csv", "ikas-fields-variant.csv", "ikas-ceviriler.csv",
	} {
		f, raw := parseFixture(t, name)
		got, err := Export(f, Products("imp_test", f), nil)
		if err != nil {
			t.Fatalf("Export(%s): %v", name, err)
		}
		if !bytes.Equal(raw, got) {
			t.Errorf("%s bayt bayt aynı değil (%d → %d bayt)", name, len(raw), len(got))
		}
	}
}

// "sku ve ürün idleri kesinlikle değiştirilmemeli". Field.Writable() is the
// gate and Export filters on it, but the claim is worth a test of its own: a
// draft that arrived carrying an SKU — from a model, from a client with a bug,
// from a future field added to Content — must not reach the file. An SKU is
// what the merchant matches against their own stock system, and a group id is
// what joins the variant rows of one product.
func TestExport_NeverWritesIdentity(t *testing.T) {
	f, raw := parseFixture(t, "ikas-variants.csv")
	products := Products("imp_test", f)
	if len(products) == 0 {
		t.Fatal("fikstürde ürün yok")
	}

	approved := map[string]map[Lang]Content{}
	for _, p := range products {
		approved[p.ID] = map[Lang]Content{LangSource: {
			Title:           "yeni başlık",
			DescriptionHTML: "<p>yeni gövde</p>",
		}}
	}
	got, err := Export(f, products, approved)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	before, err := ParseCSV(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ParseCSV(kaynak): %v", err)
	}
	after, err := ParseCSV(bytes.NewReader(got))
	if err != nil {
		t.Fatalf("ParseCSV(çıktı): %v", err)
	}
	if !reflect.DeepEqual(before.Header, after.Header) {
		t.Fatalf("başlık satırı değişti:\n%v\n%v", before.Header, after.Header)
	}
	if len(before.Rows) != len(after.Rows) {
		t.Fatalf("satır sayısı değişti: %d → %d", len(before.Rows), len(after.Rows))
	}
	for _, field := range []Field{FieldSKU, FieldGroupID, FieldHandle} {
		i := before.headerIndex(before.ColumnsFor(LangSource)[field])
		if i < 0 {
			continue
		}
		for r := range before.Rows {
			was, now := before.Rows[r][i], after.Rows[r][i]
			if was != now {
				t.Errorf("%s sütunu %d. satırda değişti: %q → %q", field, r, was, now)
			}
		}
	}
	// And the change the operator did approve landed, so the test is not
	// passing because nothing was written at all.
	if !bytes.Contains(got, []byte("yeni gövde")) {
		t.Error("onaylanan gövde dosyaya yazılmadı")
	}
}
