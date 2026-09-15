package catalog

import (
	"context"
	"strings"
	"testing"

	"github.com/logrenant/mimir/internal/config"
)

// The claim this whole change exists for: an operator who approves the Arabic
// copy gets the Arabic cell written, and an operator who approves the Turkish
// one does not.
//
// Before this, Studio.Export resolved exactly one version — the source
// language's — so an Arabic draft could be written, reviewed and approved and
// still had no way of reaching the file. The lower-level Export has been
// general over languages since task-103; this pins the wiring.
func TestExport_WritesEveryLanguageTheFileCarries(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas-fields.csv")
	ctx := context.Background()

	if got := imp.File.Langs(); len(got) < 2 {
		t.Fatalf("fixture Arapça sütunu taşımıyor: %v", got)
	}
	arVersion, err := s.CurrentDraftVersion(ctx, imp.ID, LangAR)
	if err != nil {
		t.Fatalf("CurrentDraftVersion: %v", err)
	}
	products, err := s.Products(ctx, ProductFilter{ImportID: imp.ID, Lang: LangAR}, arVersion)
	if err != nil {
		t.Fatalf("Products: %v", err)
	}
	if len(products) == 0 {
		t.Fatal("fixture ürün vermedi")
	}
	p := products[0]

	arabic := Content{DescriptionHTML: "<p>وصف عربي جديد للمنتج.</p>"}
	if _, err := s.SaveDraft(ctx, p.ID, arVersion, LangAR, arabic,
		[]Field{FieldDescriptionHTML}); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	if err := s.SetStatus(ctx, p.ID, LangAR, StatusApproved, ""); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	res, err := s.Export(ctx, imp.ID)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	data := readFile(t, res.Path)
	if !strings.Contains(data, "وصف عربي جديد للمنتج") {
		t.Error("onaylanan Arapça taslak dosyaya yazılmadı")
	}
}

// The guarantee task-103 chose on purpose, and the one this change could most
// easily have lost: an approval is per language, so approving the Turkish copy
// must not ship an Arabic draft nobody read.
func TestExport_ApprovingTheSourceLanguageDoesNotShipAnUnapprovedArabicCell(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas-fields.csv")
	ctx := context.Background()

	arVersion, _ := s.CurrentDraftVersion(ctx, imp.ID, LangAR)
	srcVersion, _ := s.CurrentDraftVersion(ctx, imp.ID, LangSource)
	products, _ := s.Products(ctx, ProductFilter{ImportID: imp.ID}, srcVersion)
	p := products[0]

	if _, err := s.SaveDraft(ctx, p.ID, arVersion, LangAR,
		Content{DescriptionHTML: "<p>نص عربي لم يقرأه أحد.</p>"},
		[]Field{FieldDescriptionHTML}); err != nil {
		t.Fatalf("SaveDraft(ar): %v", err)
	}
	// Only the source language is approved.
	if err := s.SetStatus(ctx, p.ID, LangSource, StatusApproved, ""); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	res, err := s.Export(ctx, imp.ID)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if data := readFile(t, res.Path); strings.Contains(data, "نص عربي لم يقرأه أحد") {
		t.Error("onaylanmamış Arapça kopya canlı dosyaya yazıldı")
	}
}

// Two queries per language, never one per product. A read per product is the
// fan-out this package keeps removing, and on a thousand-product translation
// export it is the difference between a screen and a stall.
func TestExport_ResolvesAVersionPerLanguageAndNotOnePerProduct(t *testing.T) {
	s, ms, imp := importedStudio(t, "ikas-fields.csv")
	ctx := context.Background()

	ms.reads.products = 0
	if _, err := s.Export(ctx, imp.ID); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if want := len(imp.File.Langs()); ms.reads.products != want {
		t.Errorf("beklenen %d ürün okuması (dil başına bir), alınan %d", want, ms.reads.products)
	}
}

// A decision in one language is not a decision in another. It is the whole
// reason catalog_product_langs exists, and the reason the source language
// deliberately stays in the product's own column.
func TestSetStatus_ApprovingArabicDoesNotApproveTheSourceLanguage(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas-fields.csv")
	ctx := context.Background()
	products, _ := s.Products(ctx, ProductFilter{ImportID: imp.ID}, "v1")
	id := products[0].ID

	if err := s.SetStatus(ctx, id, LangAR, StatusApproved, ""); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	ar, err := s.Product(ctx, id, LangAR, "v1")
	if err != nil {
		t.Fatalf("Product(ar): %v", err)
	}
	if ar.Status != StatusApproved {
		t.Errorf("Arapça kararı okunmadı: %q", ar.Status)
	}
	if ar.SourceStatus != StatusPending {
		t.Errorf("kaynak dilin durumu Arapça kararıyla değişti: %q", ar.SourceStatus)
	}

	src, err := s.Product(ctx, id, LangSource, "v1")
	if err != nil {
		t.Fatalf("Product(source): %v", err)
	}
	if src.Status != StatusPending {
		t.Errorf("kaynak dil Arapça onayıyla onaylandı: %q", src.Status)
	}
	// A source-language read carries neither field, which is what keeps its
	// JSON byte-identical to what every client already parses.
	if src.Lang != LangSource || src.SourceStatus != "" {
		t.Errorf("kaynak dil okuması şeklini değiştirdi: %+v", src.StoredProduct)
	}
}

// A language with no decision reads as pending, not as missing. Absence is the
// answer, which is why every read is a LEFT JOIN.
func TestProducts_ALanguageNobodyHasDecidedInReadsAsPending(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas-fields.csv")
	ctx := context.Background()

	rows, err := s.Products(ctx, ProductFilter{
		ImportID: imp.ID, Lang: LangAR, Status: StatusPending,
	}, "v1")
	if err != nil {
		t.Fatalf("Products: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("hiç karar verilmemiş bir dilde bekleyen ürün bulunamadı")
	}
}

// An unknown language is refused rather than quietly read as the source one: a
// caller handed the Turkish copy under an Arabic tab has no way to tell.
func TestProducts_RefusesALanguageThisBinaryDoesNotCarry(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas-fields.csv")
	_, err := s.Products(context.Background(), ProductFilter{ImportID: imp.ID, Lang: "de"}, "v1")
	if err == nil {
		t.Fatal("bilinmeyen dil kabul edildi")
	}
}

// SaveDraft marks the language it wrote and no other. An operator fixing the
// Arabic must not move the Turkish copy out of whatever the reviewer left it in.
func TestSaveDraft_MarksOnlyTheLanguageItWrote(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas-fields.csv")
	ctx := context.Background()
	products, _ := s.Products(ctx, ProductFilter{ImportID: imp.ID}, "v1")
	id := products[0].ID

	if err := s.SetStatus(ctx, id, LangSource, StatusApproved, ""); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if _, err := s.SaveDraft(ctx, id, "v1@ar", LangAR,
		Content{DescriptionHTML: "<p>نص.</p>"}, []Field{FieldDescriptionHTML}); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	src, _ := s.Product(ctx, id, LangSource, "v1")
	if src.Status != StatusApproved {
		t.Errorf("Arapça taslak kaydı Türkçe onayını düşürdü: %q", src.Status)
	}
}

// stripDirectionWrapper had no production caller and this is the failure that
// was hiding behind that: an operator's edit of an Arabic draft lost its
// direction silently, because sanitize rendered through Render rather than
// RenderLang and the vocabulary drops `dir`.
func TestSaveDraft_KeepsTheDirectionWrapperOnAnArabicEdit(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas-fields.csv")
	ctx := context.Background()
	products, _ := s.Products(ctx, ProductFilter{ImportID: imp.ID, Lang: LangAR}, "v1")
	id := products[0].ID

	d, err := s.SaveDraft(ctx, id, "v1@ar", LangAR,
		Content{DescriptionHTML: `<div dir="rtl" lang="ar"><p>نص عربي.</p></div>`},
		[]Field{FieldDescriptionHTML})
	if err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	if !strings.Contains(d.Content.DescriptionHTML, `dir="rtl"`) {
		t.Fatalf("yön kayboldu: %s", d.Content.DescriptionHTML)
	}

	// And saving it again does not nest a second wrapper. Without the strip,
	// every save goes one layer deeper — and it only shows up on a brand whose
	// own HTML contains a div, which most do.
	again, err := s.SaveDraft(ctx, id, "v1@ar", LangAR, d.Content, []Field{FieldDescriptionHTML})
	if err != nil {
		t.Fatalf("SaveDraft (ikinci): %v", err)
	}
	if n := strings.Count(again.Content.DescriptionHTML, `dir="rtl"`); n != 1 {
		t.Errorf("beklenen tek yön sarmalayıcısı, alınan %d: %s", n, again.Content.DescriptionHTML)
	}
}

// rewrite.go's own comment says the language gate "runs on this path and on the
// operator's, so there is no way to store a draft that skipped it". It did not.
// The findings are notes and never a refusal: refusing a person's Arabic
// because a ratio heuristic disagreed would make the save button silently do
// nothing.
func TestSaveDraft_StoresAnOperatorsArabicWithTheGatesFindingsAsNotes(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas-fields.csv")
	ctx := context.Background()
	products, _ := s.Products(ctx, ProductFilter{ImportID: imp.ID, Lang: LangAR}, "v1")
	id := products[0].ID

	// Turkish letters in text claiming to be Arabic — the gate's own
	// turkish_leak rule, and something an operator can only have meant.
	d, err := s.SaveDraft(ctx, id, "v1@ar", LangAR,
		Content{Title: "Saç Dökülmesi Şampuanı"}, []Field{FieldTitle})
	if err != nil {
		t.Fatalf("kapı operatörün kaydını reddetti: %v", err)
	}
	if len(d.Notes) == 0 {
		t.Error("dil kapısı operatör yolunda hiçbir şey söylemedi")
	}
}

// A source-language save goes through byte for byte. RenderLang returns
// Render's exact output for a left-to-right language and this is what keeps
// that true through sanitize as well.
func TestSaveDraft_TheSourceLanguagePathIsUnmoved(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas.csv")
	ctx := context.Background()
	products, _ := s.Products(ctx, ProductFilter{ImportID: imp.ID}, "v1")
	p := products[0]

	in := p.Original
	in.Title = "Yeni başlık"
	d, err := s.SaveDraft(ctx, p.ID, "v1", LangSource, in, []Field{FieldTitle})
	if err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	if strings.Contains(d.Content.DescriptionHTML, "dir=") {
		t.Errorf("kaynak dile yön niteliği eklendi: %s", d.Content.DescriptionHTML)
	}
	for _, n := range d.Notes {
		if strings.Contains(n, "Arap") {
			t.Errorf("kaynak dil hedef dil kapısından geçti: %q", n)
		}
	}
}

// The outputs listing reads the imports once and the drafts once, whatever the
// catalogue looks like. A read per import is the fan-out; a read per row is
// worse.
func TestOutputs_ReadsTheImportsOnceAndTheDraftsOnce(t *testing.T) {
	s, ms, imp := importedStudio(t, "ikas-fields.csv")
	ctx := context.Background()
	products, _ := s.Products(ctx, ProductFilter{ImportID: imp.ID}, "v1")
	version, _ := s.CurrentDraftVersion(ctx, imp.ID, LangSource)
	if _, err := s.SaveDraft(ctx, products[0].ID, version, LangSource,
		Content{Title: "Yeni"}, []Field{FieldTitle}); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	ms.reads.imports, ms.reads.drafts, ms.reads.products = 0, 0, 0
	page, err := s.Outputs(ctx, OutputFilter{})
	if err != nil {
		t.Fatalf("Outputs: %v", err)
	}
	if len(page.Outputs) == 0 {
		t.Fatal("çıktı listesi boş döndü")
	}
	if ms.reads.imports != 1 || ms.reads.drafts != 1 {
		t.Errorf("beklenen bir import ve bir taslak okuması, alınan %d ve %d",
			ms.reads.imports, ms.reads.drafts)
	}
	if ms.reads.products != 0 {
		t.Errorf("çıktı listesi import başına ürün okudu: %d", ms.reads.products)
	}
}

// Changed is the diff against the cell this draft would be exported INTO, which
// for a target language is that language's own cell. A client diffing against
// the product's visible content would invert it on every translation row, which
// is Export's own rule: an Arabic cell equal to the Turkish one is a change.
func TestOutputs_ComparesAgainstTheDraftsOwnLanguage(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas-fields.csv")
	ctx := context.Background()
	products, _ := s.Products(ctx, ProductFilter{ImportID: imp.ID}, "v1")
	p := products[0]
	arVersion, _ := s.CurrentDraftVersion(ctx, imp.ID, LangAR)

	// The Turkish title, written into the Arabic draft. Against the Turkish
	// cell that is "no change"; against the empty Arabic cell it is a change,
	// and the second reading is the one the export acts on.
	if _, err := s.SaveDraft(ctx, p.ID, arVersion, LangAR,
		Content{Title: p.Original.Title}, []Field{FieldTitle}); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	page, err := s.Outputs(ctx, OutputFilter{Langs: []Lang{LangAR}})
	if err != nil {
		t.Fatalf("Outputs: %v", err)
	}
	if len(page.Outputs) != 1 {
		t.Fatalf("beklenen bir satır, alınan %d", len(page.Outputs))
	}
	row := page.Outputs[0]
	if row.Lang != LangAR {
		t.Errorf("satır dilini söylemiyor: %q", row.Lang)
	}
	if strings.Join(row.Changed, ",") == "değişiklik yok" {
		t.Error("Arapça taslak Türkçe hücreyle karşılaştırıldı")
	}
}

// Empty means every language, not the source language. It is the one filter
// where the zero Lang cannot double as "unset", because "" is a real answer.
func TestOutputs_AnEmptyLanguageFilterMeansEveryLanguage(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas-fields.csv")
	ctx := context.Background()
	products, _ := s.Products(ctx, ProductFilter{ImportID: imp.ID}, "v1")
	p := products[0]

	srcVersion, _ := s.CurrentDraftVersion(ctx, imp.ID, LangSource)
	arVersion, _ := s.CurrentDraftVersion(ctx, imp.ID, LangAR)
	if _, err := s.SaveDraft(ctx, p.ID, srcVersion, LangSource,
		Content{Title: "Türkçe"}, []Field{FieldTitle}); err != nil {
		t.Fatalf("SaveDraft(tr): %v", err)
	}
	if _, err := s.SaveDraft(ctx, p.ID, arVersion, LangAR,
		Content{Title: "عربي"}, []Field{FieldTitle}); err != nil {
		t.Fatalf("SaveDraft(ar): %v", err)
	}

	page, err := s.Outputs(ctx, OutputFilter{})
	if err != nil {
		t.Fatalf("Outputs: %v", err)
	}
	seen := map[Lang]bool{}
	for _, r := range page.Outputs {
		seen[r.Lang] = true
	}
	if !seen[LangSource] || !seen[LangAR] {
		t.Errorf("boş dil süzgeci her dili getirmedi: %v", seen)
	}
}

// The profile filter narrows the imports before any key is composed, and it
// reads the dialect the file was actually read with.
func TestOutputs_FiltersOnTheProfileTheFileWasReadWith(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas-fields.csv")
	ctx := context.Background()
	products, _ := s.Products(ctx, ProductFilter{ImportID: imp.ID}, "v1")
	version, _ := s.CurrentDraftVersion(ctx, imp.ID, LangSource)
	if _, err := s.SaveDraft(ctx, products[0].ID, version, LangSource,
		Content{Title: "Yeni"}, []Field{FieldTitle}); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	hit, err := s.Outputs(ctx, OutputFilter{Dialect: imp.File.Dialect.Key})
	if err != nil {
		t.Fatalf("Outputs: %v", err)
	}
	if len(hit.Outputs) == 0 {
		t.Error("kendi profiliyle süzülen liste boş döndü")
	}
	miss, err := s.Outputs(ctx, OutputFilter{Dialect: "shopify"})
	if err != nil {
		t.Fatalf("Outputs: %v", err)
	}
	if len(miss.Outputs) != 0 {
		t.Errorf("başka bir profilin süzgeci bu dosyanın satırlarını getirdi: %d", len(miss.Outputs))
	}
}

func TestOutputs_RefusesALanguageThisBinaryDoesNotCarry(t *testing.T) {
	s := New(config.Load(), newMemStore(), nil)
	if _, err := s.Outputs(context.Background(), OutputFilter{Langs: []Lang{"de"}}); err == nil {
		t.Fatal("bilinmeyen dil kabul edildi")
	}
}

// A nil store degrades rather than panics, which is the store contract
// everywhere else in this repo.
func TestOutputs_ToleratesANilStore(t *testing.T) {
	s := New(config.Load(), nil, nil)
	page, err := s.Outputs(context.Background(), OutputFilter{})
	if err != nil {
		t.Fatalf("Outputs: %v", err)
	}
	if len(page.Outputs) != 0 {
		t.Errorf("depo yokken satır döndü: %d", len(page.Outputs))
	}
}

// A list is a list even when it is empty. A nil slice serialises as `null`,
// and the screen that was promised an array reads `.length` off it and dies —
// which is exactly how this was found.
func TestOutputs_AnEmptyPageIsAnEmptyListAndNotNull(t *testing.T) {
	// An import with keys to compose but nothing written yet: the path that
	// reaches the store and comes back with no rows, which is where the nil
	// was. A filter that matches no import short-circuits earlier and would
	// pass this even unfixed.
	s, _, _ := importedStudio(t, "ikas-fields.csv")
	page, err := s.Outputs(context.Background(), OutputFilter{})
	if err != nil {
		t.Fatalf("Outputs: %v", err)
	}
	if page.Outputs == nil {
		t.Fatal("boş sayfa nil dilim döndürdü — tel üzerinde null olur")
	}
}

// RenderLang writes a direction wrapper for an RTL language and nothing for any
// other, so stripping one unconditionally is a one-way door. A brand whose own
// descriptions are wrapped in <div dir="ltr"> has both the tag and the
// attribute in its vocabulary — they are counted from its own HTML — so that
// wrapper survives Render today, and an operator saving such a description
// unchanged would have watched it disappear.
func TestSanitize_KeepsABrandsOwnDirectionWrapperInTheSourceLanguage(t *testing.T) {
	s, ms, imp := importedStudio(t, "ikas.csv")
	ctx := context.Background()
	products, _ := s.Products(ctx, ProductFilter{ImportID: imp.ID}, "v1")
	p := products[0]

	// Teach the brand this wrapper by putting it in the product's own HTML,
	// the way DeriveVocabulary would have.
	body := `<div dir="ltr"><p>Yoğun bakım formülü.</p></div>`
	row := ms.products[p.ID]
	row.Original.DescriptionHTML = body
	ms.products[p.ID] = row

	kit := imp.Brand
	kit.Vocab.Tags["div"]++
	kit.Vocab.Attrs["dir"]++

	out, _, err := s.sanitize(
		Content{DescriptionHTML: body}, row.Product, kit, LangSource, operatorURLs,
	)
	if err != nil {
		t.Fatalf("sanitize: %v", err)
	}
	if !strings.Contains(out.DescriptionHTML, `dir="ltr"`) {
		t.Errorf("markanın kendi yön sarmalayıcısı düştü: %s", out.DescriptionHTML)
	}
}

// The product table draws one status column per language, so it needs every
// language's decision in one answer. Asking per language is what made the
// language a mode on that screen: switching it re-read the same rows and, for a
// language nobody had written yet, changed nothing anybody could see.
func TestProducts_CarriesEveryLanguagesDecisionInOneRead(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas-fields.csv")
	ctx := context.Background()

	rows, _ := s.Products(ctx, ProductFilter{ImportID: imp.ID}, "v1")
	if len(rows) == 0 {
		t.Fatal("fixture ürün vermedi")
	}
	p := rows[0]
	if err := s.SetStatus(ctx, p.ID, LangAR, StatusApproved, ""); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if err := s.SetStatus(ctx, p.ID, LangSource, StatusRejected, ""); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	again, err := s.Products(ctx, ProductFilter{ImportID: imp.ID}, "v1")
	if err != nil {
		t.Fatalf("Products: %v", err)
	}
	got := again[0].Statuses
	if got[LangSource] != StatusRejected {
		t.Errorf("kaynak dil kararı %q, beklenen %q", got[LangSource], StatusRejected)
	}
	if got[LangAR] != StatusApproved {
		t.Errorf("Arapça kararı %q, beklenen %q", got[LangAR], StatusApproved)
	}
}

// Absence is pending, and it stays absent here rather than being filled in: the
// map says what has been decided, and a language with no entry has had nothing
// decided in it. Synthesising "pending" for every language the binary can write
// would draw an Arabic column over a file with no Arabic column.
func TestProducts_ALanguageNobodyHasTouchedHasNoEntry(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas-fields.csv")
	ctx := context.Background()

	rows, _ := s.Products(ctx, ProductFilter{ImportID: imp.ID}, "v1")
	got := rows[0].Statuses
	if _, ok := got[LangAR]; ok {
		t.Error("hiç karar verilmemiş dil için kayıt üretildi")
	}
	if got[LangSource] == "" {
		t.Error("kaynak dilin kararı her zaman olmalı")
	}
}

// One read for the products and one for the decisions, whatever the page holds.
// A read per product is the 1+N fan-out this package removed from the board.
func TestProducts_ReadsTheDecisionTableOnce(t *testing.T) {
	s, store, imp := importedStudio(t, "ikas-fields.csv")
	ctx := context.Background()

	store.reads.langs = 0
	if _, err := s.Products(ctx, ProductFilter{ImportID: imp.ID}, "v1"); err != nil {
		t.Fatalf("Products: %v", err)
	}
	if store.reads.langs != 1 {
		t.Errorf("karar tablosu %d kez okundu, beklenen 1", store.reads.langs)
	}
}

// The other half of statusesOf, which the tests above do not reach: on a
// target-language read the row's own Status is that target's decision and the
// source language's rides along in SourceStatus. Composing the map from Status
// in both cases would report the Arabic decision as the Turkish one, on every
// row, and every test that reads in the source language would still pass.
func TestProducts_ATargetLanguageReadStillReportsTheSourceDecision(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas-fields.csv")
	ctx := context.Background()

	rows, _ := s.Products(ctx, ProductFilter{ImportID: imp.ID}, "v1")
	p := rows[0]
	if err := s.SetStatus(ctx, p.ID, LangSource, StatusApproved, ""); err != nil {
		t.Fatalf("SetStatus(source): %v", err)
	}
	if err := s.SetStatus(ctx, p.ID, LangAR, StatusRejected, ""); err != nil {
		t.Fatalf("SetStatus(ar): %v", err)
	}

	read, err := s.Products(ctx, ProductFilter{ImportID: imp.ID, Lang: LangAR}, "v1")
	if err != nil {
		t.Fatalf("Products: %v", err)
	}
	got := read[0].Statuses
	if got[LangSource] != StatusApproved {
		t.Errorf("Arapça okumasında kaynak dil %q, beklenen %q", got[LangSource], StatusApproved)
	}
	if got[LangAR] != StatusRejected {
		t.Errorf("Arapça kararı %q, beklenen %q", got[LangAR], StatusRejected)
	}
}
