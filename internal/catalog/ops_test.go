package catalog

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/llm"
)

// memStore is the engine's persistence, in memory. The engine is worth testing
// without a database: what it enforces is a contract about content, and a
// contract about content should not need a schema to demonstrate.
type memStore struct {
	imports  map[string]StoredImport
	products map[string]StoredProduct
	// langs is the decision table, keyed product then language. The source
	// language is deliberately absent from it, exactly as it is from
	// catalog_product_langs: a memStore that held it in both places would pass
	// a test the database would fail.
	langs    map[string]map[Lang]StoredProduct
	drafts   map[string]StoredDraft
	research map[string]Findings
	order    []string
	// reads counts store reads that cost a query, so "this pass reads the
	// imports once and the drafts once" is a claim something checks.
	reads struct{ imports, drafts, products, langs int }
}

func newMemStore() *memStore {
	return &memStore{
		imports:  map[string]StoredImport{},
		products: map[string]StoredProduct{},
		langs:    map[string]map[Lang]StoredProduct{},
		drafts:   map[string]StoredDraft{},
		research: map[string]Findings{},
	}
}

func draftKey(id, v string) string { return id + "\x00" + v }

func (m *memStore) PutCatalogImport(_ context.Context, i StoredImport) error {
	m.imports[i.ID] = i
	return nil
}

func (m *memStore) GetCatalogImport(_ context.Context, id string) (StoredImport, bool, error) {
	i, ok := m.imports[id]
	return i, ok, nil
}

func (m *memStore) ListCatalogImports(_ context.Context, _ int) ([]StoredImport, error) {
	m.reads.imports++
	out := make([]StoredImport, 0, len(m.imports))
	for _, i := range m.imports {
		// Without the file body, exactly as the real store lists them: a
		// listing that carried every file would cost as much to open as every
		// import at once. A fake that handed it over would let a caller read
		// columns production does not have — which is the whole reason the
		// dialect survives a listing as its own column.
		i.FileJSON = nil
		out = append(out, i)
	}
	return out, nil
}

func (m *memStore) DeleteCatalogImport(_ context.Context, id string) error {
	delete(m.imports, id)
	return nil
}

func (m *memStore) PutCatalogProducts(_ context.Context, _ string, rows []StoredProduct) error {
	m.order = nil
	for _, r := range rows {
		if old, ok := m.products[r.ID]; ok {
			r.Status = old.Status
		}
		m.products[r.ID] = r
		m.order = append(m.order, r.ID)
	}
	return nil
}

func (m *memStore) CatalogStatusCounts(context.Context) (map[string]map[Lang]map[string]int, error) {
	out := map[string]map[Lang]map[string]int{}
	bump := func(importID string, l Lang, status string) {
		if out[importID] == nil {
			out[importID] = map[Lang]map[string]int{}
		}
		if out[importID][l] == nil {
			out[importID][l] = map[string]int{}
		}
		out[importID][l][status]++
	}
	for _, p := range m.products {
		bump(p.ImportID, LangSource, p.Status)
		for l, d := range m.langs[p.ID] {
			bump(p.ImportID, l, d.Status)
		}
	}
	return out, nil
}

func (m *memStore) ListCatalogProducts(_ context.Context, f ProductFilter) ([]StoredProduct, error) {
	m.reads.products++
	var out []StoredProduct
	for _, id := range m.order {
		p := m.inLang(m.products[id], f.Lang)
		if p.ImportID != f.ImportID {
			continue
		}
		if f.Status != "" && p.Status != f.Status {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

func (m *memStore) GetCatalogProduct(_ context.Context, id string, lang Lang) (StoredProduct, bool, error) {
	p, ok := m.products[id]
	if !ok {
		return StoredProduct{}, false, nil
	}
	return m.inLang(p, lang), true, nil
}

// inLang is the database's LEFT JOIN and COALESCE, in memory: a language with
// no decision reads as pending rather than as missing.
func (m *memStore) inLang(p StoredProduct, lang Lang) StoredProduct {
	if lang == LangSource {
		return p
	}
	out := p
	out.Lang = lang
	out.SourceStatus = p.Status
	out.Status, out.Reason = StatusPending, ""
	if d, ok := m.langs[p.ID][lang]; ok {
		out.Status, out.Reason, out.UpdatedAt = d.Status, d.Reason, d.UpdatedAt
	}
	return out
}

// CatalogProductLangStatuses is the decision table read straight: the source
// language is not in it, exactly as it is not in catalog_product_langs.
func (m *memStore) CatalogProductLangStatuses(
	_ context.Context, productIDs []string,
) (map[string]map[Lang]string, error) {
	m.reads.langs++
	out := map[string]map[Lang]string{}
	for _, id := range productIDs {
		for lang, d := range m.langs[id] {
			if out[id] == nil {
				out[id] = map[Lang]string{}
			}
			out[id][lang] = d.Status
		}
	}
	return out, nil
}

func (m *memStore) SetCatalogProductStatus(
	_ context.Context, id string, lang Lang, status, reason string, at time.Time,
) error {
	p, ok := m.products[id]
	if !ok {
		return errors.New("no rows")
	}
	if lang == LangSource {
		p.Status, p.Reason, p.UpdatedAt = status, reason, at
		m.products[id] = p
		return nil
	}
	if m.langs[id] == nil {
		m.langs[id] = map[Lang]StoredProduct{}
	}
	m.langs[id][lang] = StoredProduct{Status: status, Reason: reason, UpdatedAt: at}
	return nil
}

func (m *memStore) GetCatalogResearch(_ context.Context, id, v string) (Findings, bool, error) {
	f, ok := m.research[draftKey(id, v)]
	return f, ok, nil
}

func (m *memStore) PutCatalogResearch(_ context.Context, id, v string, f Findings) error {
	m.research[draftKey(id, v)] = f
	return nil
}

func (m *memStore) GetCatalogDraft(_ context.Context, id, v string) (StoredDraft, bool, error) {
	d, ok := m.drafts[draftKey(id, v)]
	return d, ok, nil
}

func (m *memStore) GetCatalogDrafts(_ context.Context, ids []string, v string) (map[string]StoredDraft, error) {
	out := map[string]StoredDraft{}
	for _, id := range ids {
		if d, ok := m.drafts[draftKey(id, v)]; ok {
			out[id] = d
		}
	}
	return out, nil
}

// ListCatalogDrafts is the inline-key join, in memory. The pair constraint is
// the point of the loop: a version is matched only against the import it was
// composed for.
func (m *memStore) ListCatalogDrafts(_ context.Context, f DraftFilter) ([]DraftRow, error) {
	m.reads.drafts++
	var out []DraftRow
	for _, k := range f.Keys {
		for _, id := range m.order {
			p := m.products[id]
			if p.ImportID != k.ImportID {
				continue
			}
			d, ok := m.drafts[draftKey(id, k.Version)]
			if !ok {
				continue
			}
			row := m.inLang(p, k.Lang)
			if f.Status != "" && row.Status != f.Status {
				continue
			}
			out = append(out, DraftRow{
				ImportID: p.ImportID, Filename: m.imports[p.ImportID].Filename,
				Dialect:   m.imports[p.ImportID].Dialect,
				ProductID: id, Handle: p.Handle,
				Lang: k.Lang, Status: row.Status,
				Fields: d.Fields, Provider: d.Provider, Model: d.Model,
				EditedByOperator: d.EditedByOperator,
				CreatedAt:        d.CreatedAt, UpdatedAt: d.CreatedAt,
				Product: p.Product, Content: d.Content,
			})
		}
	}
	if f.Offset > 0 {
		if f.Offset >= len(out) {
			return nil, nil
		}
		out = out[f.Offset:]
	}
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

func (m *memStore) PutCatalogDraft(_ context.Context, d StoredDraft) error {
	// The same guard the real store writes in SQL:
	//   WHERE catalog_drafts.edited_by_operator = 0 OR excluded.edited_by_operator = 1
	// A bulk pass cannot overwrite a hand-edited draft; the operator can. This
	// fake refused both for a while, which made "save my correction twice" a
	// failure here and a success against the database.
	if old, ok := m.drafts[draftKey(d.ProductID, d.Version)]; ok &&
		old.EditedByOperator && !d.EditedByOperator {
		return errors.New("store: this draft was edited by the operator")
	}
	m.drafts[draftKey(d.ProductID, d.Version)] = d
	return nil
}

func importedStudio(t *testing.T, fixture string) (*Studio, *memStore, Import) {
	t.Helper()
	raw := readFixture(t, fixture)
	ms := newMemStore()
	s := New(config.Load(), ms, &fakeCompleter{err: llm.ErrProviderUnavailable})
	s.UseClock(func() time.Time { return time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC) })

	imp, err := s.Import(context.Background(), fixture, raw, llm.Selection{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	return s, ms, imp
}

func TestImport_StoresTheFileAndItsProducts(t *testing.T) {
	s, ms, imp := importedStudio(t, "ikas.csv")
	if imp.ProductCount != 2 {
		t.Fatalf("beklenen 2 ürün, alınan %d", imp.ProductCount)
	}
	if len(ms.products) != 2 {
		t.Errorf("ürünler kaydedilmedi: %d", len(ms.products))
	}
	// The whole file rides along, or lossless export is impossible later.
	back, err := s.Get(context.Background(), imp.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(back.File.Rows) != 2 || back.File.Delimiter != ',' || !back.File.HasBOM {
		t.Errorf("dosya çerçevesi saklanmadı: %+v", framing(back.File))
	}
}

func TestImport_SaysSoWhenNoPlatformMatched(t *testing.T) {
	ms := newMemStore()
	s := New(config.Load(), ms, nil)
	imp, err := s.Import(context.Background(), "garip.csv",
		[]byte("kod,ad,metin\nA1,Ürün,<p>x</p>\n"), llm.Selection{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if imp.Note == "" {
		t.Error("tanınmayan başlık sessizce kabul edildi")
	}
}

func TestImport_RefusesAFileLargerThanAnOperatorReviews(t *testing.T) {
	cfg := config.Load()
	cfg.CatalogMaxProductsPerImport = 1
	s := New(cfg, newMemStore(), nil)
	_, err := s.Import(context.Background(), "x.csv",
		readFixture(t, "ikas.csv"), llm.Selection{})
	if !errors.Is(err, ErrTooManyProducts) {
		t.Fatalf("beklenen ErrTooManyProducts, alınan %v", err)
	}
}

func TestSetMapping_RereadsTheFileUnderIt(t *testing.T) {
	ms := newMemStore()
	s := New(config.Load(), ms, nil)
	ctx := context.Background()
	imp, err := s.Import(ctx, "garip.csv",
		[]byte("kod,ad,metin\nA1,Krem,<p>Nemlendirici</p>\n"), llm.Selection{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	mapped, err := s.SetMapping(ctx, imp.ID, map[LangField]string{
		{Field: FieldSKU}: "kod", {Field: FieldTitle}: "ad", {Field: FieldDescriptionHTML}: "metin",
	}, llm.Selection{})
	if err != nil {
		t.Fatalf("SetMapping: %v", err)
	}
	if mapped.ProductCount != 1 {
		t.Fatalf("beklenen 1 ürün, alınan %d", mapped.ProductCount)
	}
	if mapped.Brand.Vocab.IsEmpty() {
		t.Error("eşlemeden sonra sözlük yeniden çıkarılmadı")
	}
	products, _ := s.Products(ctx, ProductFilter{ImportID: imp.ID}, "v")
	if len(products) != 1 || products[0].Original.Title != "Krem" {
		t.Errorf("eşleme sonrası başlık okunmadı: %+v", products)
	}
}

// The bug an operator hit with their own catalogue: they filled the mapping
// form, pressed save, and the form came back. SetMapping does not invent a
// dialect — it saves a mapping — so a screen gated on the dialect never opens.
// Readable is that gate, and this is it being true.
func TestSetMapping_MakesTheImportReadable(t *testing.T) {
	ms := newMemStore()
	s := New(config.Load(), ms, nil)
	ctx := context.Background()
	imp, err := s.Import(ctx, "garip.csv",
		[]byte("kod,ad,metin\nA1,Krem,<p>Nemlendirici</p>\n"), llm.Selection{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if imp.File.Readable() {
		t.Fatal("eşlemesiz dosya okunabilir sayıldı")
	}

	mapped, err := s.SetMapping(ctx, imp.ID,
		map[LangField]string{{Field: FieldTitle}: "ad", {Field: FieldDescriptionHTML}: "metin"}, llm.Selection{})
	if err != nil {
		t.Fatalf("SetMapping: %v", err)
	}
	if !mapped.File.Dialect.IsZero() {
		t.Error("eşleme bir lehçe uydurdu")
	}
	if !mapped.File.Readable() {
		t.Fatal("eşleme kaydedildi ama dosya hâlâ okunamaz sayılıyor")
	}
	// And it survives the round trip through the store, which is where the
	// screen reads it from.
	back, err := s.Get(ctx, imp.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !back.File.Readable() {
		t.Error("eşleme saklanmadı")
	}
}

// A mapping with neither a title nor a description builds an import full of
// empty products and no explanation. The refusal is the explanation.
func TestSetMapping_RefusesAMappingWithNeitherTitleNorDescription(t *testing.T) {
	ms := newMemStore()
	s := New(config.Load(), ms, nil)
	ctx := context.Background()
	imp, err := s.Import(ctx, "garip.csv",
		[]byte("kod,ad,metin\nA1,Krem,<p>Nemlendirici</p>\n"), llm.Selection{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	_, err = s.SetMapping(ctx, imp.ID, map[LangField]string{{Field: FieldSKU}: "kod"}, llm.Selection{})
	if !errors.Is(err, ErrMappingIncomplete) {
		t.Fatalf("beklenen ErrMappingIncomplete, alınan %v", err)
	}
}

func TestSetMapping_RefusesAColumnTheFileDoesNotHave(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas.csv")
	_, err := s.SetMapping(context.Background(), imp.ID,
		map[LangField]string{{Field: FieldTitle}: "yok böyle bir sütun"}, llm.Selection{})
	if err == nil {
		t.Fatal("olmayan sütun kabul edildi")
	}
}

// The vocabulary is derived from the operator's own past HTML and it is the
// gate every rewrite is measured against. An edit is not the place to widen it.
func TestSetBrand_TakesTheVoiceAndNotTheVocabulary(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas.csv")
	before := len(imp.Brand.Vocab.Tags)

	after, err := s.SetBrand(context.Background(), imp.ID, Voice{
		Address: "siz", Tone: "sade", Lexicon: []string{"nemlendirici"},
	})
	if err != nil {
		t.Fatalf("SetBrand: %v", err)
	}
	if after.Brand.Voice.Tone != "sade" {
		t.Errorf("ses kaydedilmedi: %+v", after.Brand.Voice)
	}
	if len(after.Brand.Vocab.Tags) != before {
		t.Error("düzenleme sözlüğü değiştirdi")
	}
	if after.Brand.Version == imp.Brand.Version {
		t.Error("ses düzenlendi ama marka sürümü aynı kaldı")
	}
	if after.Brand.VoiceNote != "" {
		t.Error("elle yazılmış sesin yanında hâlâ 'çıkarılamadı' notu var")
	}
}

// The editor on the other end is a convenience; the server is the authority.
func TestSaveDraft_SanitizesWhateverTheClientPosted(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas.csv")
	ctx := context.Background()

	products, err := s.Products(ctx, ProductFilter{ImportID: imp.ID}, "v1")
	if err != nil || len(products) == 0 {
		t.Fatalf("Products: %v", err)
	}
	p := products[0]

	d, err := s.SaveDraft(ctx, p.ID, "v1", LangSource, Content{
		DescriptionHTML: `<script>alert(1)</script><h1 onclick="x">Uydurma</h1><p>Gerçek</p>`,
		SEOTitle:        strings.Repeat("uzun ", 40),
	}, []Field{FieldDescriptionHTML, FieldSEOTitle})
	if err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	for _, forbidden := range []string{"<script", "onclick", "<h1"} {
		if strings.Contains(d.Content.DescriptionHTML, forbidden) {
			t.Errorf("%q sunucuda süzülmedi: %s", forbidden, d.Content.DescriptionHTML)
		}
	}
	if !strings.Contains(d.Content.DescriptionHTML, "Gerçek") {
		t.Errorf("meşru metin de kayboldu: %s", d.Content.DescriptionHTML)
	}
	if n := len([]rune(d.Content.SEOTitle)); n > 60 {
		t.Errorf("SEO başlık kırpılmadı: %d karakter", n)
	}
	if len(d.Notes) == 0 {
		t.Error("yapılan sadeleştirmeler operatöre söylenmiyor")
	}
	if !d.EditedByOperator {
		t.Error("elle kayıt operatör düzenlemesi olarak işaretlenmedi")
	}
}

func TestSaveDraft_RefusesToWriteAnIdentityField(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas.csv")
	ctx := context.Background()
	products, _ := s.Products(ctx, ProductFilter{ImportID: imp.ID}, "v1")

	_, err := s.SaveDraft(ctx, products[0].ID, "v1", LangSource, Content{Title: "x"}, []Field{FieldHandle})
	if !errors.Is(err, ErrNotWritable) {
		t.Fatalf("beklenen ErrNotWritable, alınan %v", err)
	}
}

// A drafted-but-unreviewed product is a suggestion. A tool that shipped
// suggestions to a live storefront because somebody clicked export is a tool
// nobody could leave running.
func TestExport_AppliesApprovedDraftsAndIgnoresUnreviewedOnes(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas.csv")
	ctx := context.Background()
	s.cfg.ExportDir = t.TempDir()

	// The version the export will look under is the studio's own, so the
	// drafts have to be written under it too — that agreement is the thing
	// task-101 fixed and this test would not notice if it used a literal.
	version, err := s.CurrentDraftVersion(ctx, imp.ID, LangSource)
	if err != nil {
		t.Fatalf("CurrentDraftVersion: %v", err)
	}
	products, _ := s.Products(ctx, ProductFilter{ImportID: imp.ID}, version)
	approved, pending := products[0], products[1]

	for _, p := range []ProductView{approved, pending} {
		next := p.Original
		next.Title = "YENİ " + p.Original.Title
		if _, err := s.SaveDraft(ctx, p.ID, version, LangSource, next, []Field{FieldTitle}); err != nil {
			t.Fatalf("SaveDraft: %v", err)
		}
	}
	if err := s.SetStatus(ctx, approved.ID, LangSource, StatusApproved, ""); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	res, err := s.Export(ctx, imp.ID)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if res.Changed != 1 {
		t.Errorf("beklenen 1 değişiklik, alınan %d", res.Changed)
	}

	data := readFile(t, res.Path)
	if !strings.Contains(data, "YENİ "+approved.Original.Title) {
		t.Error("onaylanan değişiklik yazılmamış")
	}
	if strings.Contains(data, "YENİ "+pending.Original.Title) {
		t.Error("incelenmemiş taslak canlı dosyaya yazıldı")
	}
}

func TestSetStatus_RefusesAStatusOutsideTheClosedSet(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas.csv")
	ctx := context.Background()
	products, _ := s.Products(ctx, ProductFilter{ImportID: imp.ID}, "v1")
	if err := s.SetStatus(ctx, products[0].ID, LangSource, "yayinlandi", ""); err == nil {
		t.Fatal("kapalı küme dışında bir durum kabul edildi")
	}
}

func TestGet_UnknownImportIsATypedError(t *testing.T) {
	s := New(config.Load(), newMemStore(), nil)
	if _, err := s.Get(context.Background(), "yok"); !errors.Is(err, ErrUnknownImport) {
		t.Fatalf("beklenen ErrUnknownImport, alınan %v", err)
	}
}

// Change any of the four and the stored copy is an answer to a question nobody
// is asking any more.
func TestDraftVersion_CoversPromptModelBrandAndSkill(t *testing.T) {
	s := New(config.Load(), nil, nil)
	kit := BrandKit{Version: "brand-v1:aaaa"}
	base := s.DraftVersion(kit, llm.Selection{}, "skill1", LangSource)

	other := kit
	other.Version = "brand-v1:bbbb"
	for name, got := range map[string]string{
		"model": s.DraftVersion(kit, llm.Selection{Provider: "claude", Model: "x"}, "skill1", LangSource),
		"marka": s.DraftVersion(other, llm.Selection{}, "skill1", LangSource),
		"skill": s.DraftVersion(kit, llm.Selection{}, "skill2", LangSource),
	} {
		if got == base {
			t.Errorf("%s değişti ama taslak anahtarı değişmedi", name)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("dışa aktarılan dosya okunamadı: %v", err)
	}
	return string(b)
}

// A person typed that link because they meant to. Refusing it would make the
// editor's link button a button that silently does nothing — the exact failure
// this package says it exists to prevent. A model's invented URL is a different
// thing entirely, and task-87's rewrite path keeps the strict allow-list.
func TestSaveDraft_KeepsALinkTheOperatorTypedThemselves(t *testing.T) {
	ms := newMemStore()
	s := New(config.Load(), ms, nil)
	ctx := context.Background()

	imp, err := s.Import(ctx, "x.csv",
		[]byte("Ürün Grup ID,SKU,İsim,Açıklama\ng1,A1,Krem,\"<p>Eski <a href=&quot;https://eski.example/a&quot;>bağ</a></p>\"\n"),
		llm.Selection{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	products, err := s.Products(ctx, ProductFilter{ImportID: imp.ID}, "v1")
	if err != nil || len(products) == 0 {
		t.Fatalf("Products: %v", err)
	}

	d, err := s.SaveDraft(ctx, products[0].ID, "v1", LangSource, Content{
		DescriptionHTML: `<p>Yeni <a href="https://yeni.example/b">bağ</a> ve <a href="https://eski.example/a">eski</a></p>`,
	}, []Field{FieldDescriptionHTML})
	if err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	for _, want := range []string{"yeni.example/b", "eski.example/a"} {
		if !strings.Contains(d.Content.DescriptionHTML, want) {
			t.Errorf("operatörün bağlantısı %q düşürüldü: %s", want, d.Content.DescriptionHTML)
		}
	}
}

// The strict policy, pinned before its caller exists.
//
// task-87's rewrite path is what will pass `sourceURLs`, and this is the
// contract it must not loosen: a URL a model produced that was not in the
// product's own description was invented, and an invented URL on a product page
// is a broken link the merchant ships to customers. The operator path is
// deliberately the other way round, and the test above says so.
func TestSanitize_UnderTheSourcePolicyRefusesAURLTheProductNeverHad(t *testing.T) {
	s := New(config.Load(), nil, nil)

	p := Product{}
	p.Original.DescriptionHTML = `<p>Eski <a href="https://vardi.example/a">bağ</a></p>`
	src, err := ParseHTML(p.Original.DescriptionHTML)
	if err != nil {
		t.Fatalf("ParseHTML: %v", err)
	}
	kit := BrandKit{Vocab: NewVocabulary()}
	kit.Vocab.Add(src)

	in := Content{
		DescriptionHTML: `<p>Yeni <a href="https://uydurma.example/b">bağ</a> ve <a href="https://vardi.example/a">eski</a></p>`,
	}

	strict, _, err := s.sanitize(in, p, kit, LangSource, sourceURLs)
	if err != nil {
		t.Fatalf("sanitize: %v", err)
	}
	if strings.Contains(strict.DescriptionHTML, "uydurma.example") {
		t.Errorf("uydurulmuş URL katı politikadan geçti: %s", strict.DescriptionHTML)
	}
	if !strings.Contains(strict.DescriptionHTML, "vardi.example/a") {
		t.Errorf("ürünün kendi bağlantısı düşürüldü: %s", strict.DescriptionHTML)
	}

	loose, _, err := s.sanitize(in, p, kit, LangSource, operatorURLs)
	if err != nil {
		t.Fatalf("sanitize: %v", err)
	}
	if !strings.Contains(loose.DescriptionHTML, "uydurma.example") {
		t.Errorf("operatörün kendi yazdığı bağlantı reddedildi: %s", loose.DescriptionHTML)
	}
}

// The bug this pins: the board card wrote drafts under a key that included the
// catalog agent's skill version, every HTTP read composed the same key without
// it, and store.GetCatalogDraft matches a version byte for byte. Every draft a
// bulk rewrite paid for was therefore stored where nothing looked, and the
// products screen reported no drafts at all.
//
// The test spans both sides on purpose. Either half alone passes while the
// feature is broken — that is exactly how this shipped.
func TestCurrentDraftVersion_FindsWhatARewritePassWrote(t *testing.T) {
	ms := newMemStore()
	s := New(config.Load(), ms, nil)
	ctx := context.Background()

	imp, err := s.Import(ctx, "shopify.csv", []byte(
		"Handle,Title,Body (HTML),Variant SKU\n"+
			"krem,Krem,<p>Nemlendirici</p>,A1\n"), llm.Selection{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	sel := llm.Selection{Provider: "claude", Model: "opus-5"}
	s.UseDraftKey(
		func() llm.Selection { return sel },
		func() string { return "product-content:11aa22bb" },
	)

	// What the card does: compose the key from what the run actually ran under.
	written := s.DraftVersion(imp.Brand, sel, "product-content:11aa22bb", LangSource)
	products, err := s.Products(ctx, ProductFilter{ImportID: imp.ID}, written)
	if err != nil || len(products) != 1 {
		t.Fatalf("Products: %v (%d ürün)", err, len(products))
	}
	if err := ms.PutCatalogDraft(ctx, StoredDraft{
		ProductID: products[0].ID, Version: written,
		Content: Content{Title: "Yeniden yazılmış krem"},
	}); err != nil {
		t.Fatalf("PutCatalogDraft: %v", err)
	}

	// What every read does: ask the studio.
	read, err := s.CurrentDraftVersion(ctx, imp.ID, LangSource)
	if err != nil {
		t.Fatalf("CurrentDraftVersion: %v", err)
	}
	if read != written {
		t.Fatalf("okuma ve yazma farklı anahtar kurdu:\n  yazılan: %s\n  okunan:  %s", written, read)
	}

	views, err := s.Products(ctx, ProductFilter{ImportID: imp.ID}, read)
	if err != nil {
		t.Fatalf("Products: %v", err)
	}
	if views[0].Draft == nil {
		t.Fatal("kartın yazdığı taslak okumada görünmedi")
	}
	if got := views[0].Draft.Content.Title; got != "Yeniden yazılmış krem" {
		t.Errorf("beklenen yeniden yazılmış başlık, alınan %q", got)
	}
}

// An unwired studio composes the key every draft written before the wiring
// existed is already stored under, rather than a third shape.
func TestCurrentDraftVersion_WithoutWiringIsTheBareKey(t *testing.T) {
	ms := newMemStore()
	s := New(config.Load(), ms, nil)
	ctx := context.Background()
	imp, err := s.Import(ctx, "shopify.csv", []byte(
		"Handle,Title,Body (HTML),Variant SKU\nkrem,Krem,<p>x</p>,A1\n"), llm.Selection{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	got, err := s.CurrentDraftVersion(ctx, imp.ID, LangSource)
	if err != nil {
		t.Fatalf("CurrentDraftVersion: %v", err)
	}
	if want := s.DraftVersion(imp.Brand, llm.Selection{}, "", LangSource); got != want {
		t.Errorf("beklenen %q, alınan %q", want, got)
	}
}

// Detection answers "which platform wrote this file", not "which platform is
// this store on". An operator who can see it is an IKAS export must be able to
// say so without retyping a map the profile table already holds.
func TestSetDialect_OverridesDetectionAndBindsToTheFilesOwnSpelling(t *testing.T) {
	ms := newMemStore()
	s := New(config.Load(), ms, nil)
	ctx := context.Background()

	// A header no signature matches: IKAS's product export with the group id
	// column renamed, which is what a store that reorganised its export has.
	raw := "\"Grup\",\"İsim\",\"Açıklama\",\"SKU\"\n\"g1\",\"Krem\",\"<p>Nemlendirici</p>\",\"A1\"\n"
	imp, err := s.Import(ctx, "ikas.csv", []byte(raw), llm.Selection{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if !imp.File.Dialect.IsZero() {
		t.Fatalf("bu başlık tanınmamalıydı, tanınan: %s", imp.File.Dialect.Key)
	}

	picked, err := s.SetDialect(ctx, imp.ID, "ikas", llm.Selection{})
	if err != nil {
		t.Fatalf("SetDialect: %v", err)
	}
	if picked.File.Dialect.Key != "ikas" {
		t.Fatalf("beklenen ikas profili, alınan %q", picked.File.Dialect.Key)
	}
	// "Ürün Grup ID" is not in this file, so the profile binds without it
	// rather than binding to a column name the file never had.
	if _, ok := picked.File.Dialect.Columns[FieldGroupID]; ok {
		t.Error("dosyada olmayan sütun profile bağlandı; dışa aktarım onu yazardı")
	}
	if got := picked.File.Dialect.Columns[FieldTitle]; got != "İsim" {
		t.Errorf("başlık sütunu dosyanın kendi yazımına bağlanmadı: %q", got)
	}
	products, _ := s.Products(ctx, ProductFilter{ImportID: imp.ID}, "v")
	if len(products) != 1 || products[0].Original.Title != "Krem" {
		t.Errorf("profil seçildikten sonra dosya yeniden okunmadı: %+v", products)
	}
}

func TestSetDialect_RefusesAProfileThisBinaryDoesNotShip(t *testing.T) {
	s := New(config.Load(), newMemStore(), nil)
	ctx := context.Background()
	imp, err := s.Import(ctx, "shopify.csv", []byte(
		"Handle,Title,Body (HTML),Variant SKU\nkrem,Krem,<p>x</p>,A1\n"), llm.Selection{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, err := s.SetDialect(ctx, imp.ID, "woocommerce", llm.Selection{}); !errors.Is(err, ErrUnknownDialect) {
		t.Fatalf("beklenen ErrUnknownDialect, alınan %v", err)
	}
}

// Undoing a wrong pick must not need a re-upload.
func TestSetDialect_AnEmptyKeyReturnsTheFileToDetection(t *testing.T) {
	s := New(config.Load(), newMemStore(), nil)
	ctx := context.Background()
	imp, err := s.Import(ctx, "shopify.csv", []byte(
		"Handle,Title,Body (HTML),Variant SKU\nkrem,Krem,<p>x</p>,A1\n"), llm.Selection{})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, err := s.SetDialect(ctx, imp.ID, "ikas-fields", llm.Selection{}); err == nil {
		t.Fatal("bu dosyanın hiçbir sütununu okumayan profil kabul edildi")
	}
	back, err := s.SetDialect(ctx, imp.ID, "", llm.Selection{})
	if err != nil {
		t.Fatalf("SetDialect(\"\"): %v", err)
	}
	if back.File.Dialect.Key != "shopify" {
		t.Errorf("boş anahtar algılamaya dönmedi: %q", back.File.Dialect.Key)
	}
}

// The configuration is the operator's answer about their own export, and it has
// to survive being written down. A panel whose switches reset on reload is a
// panel nobody configures twice.
func TestSetWrite_IsStoredAndSurvivesEveryLaterOperation(t *testing.T) {
	s, ms, imp := importedStudio(t, "ikas.csv")
	ctx := context.Background()

	want := []LangField{{Field: FieldDescriptionHTML}, {Field: FieldSEODescription}}
	saved, err := s.SetWrite(ctx, imp.ID, want)
	if err != nil {
		t.Fatalf("SetWrite: %v", err)
	}
	if got := saved.File.WriteSet(LangSource); len(got) != 2 {
		t.Fatalf("kaydedilen küme yanlış: %v", got)
	}

	// Read back from the store, not from the value we were handed.
	back, err := s.Get(ctx, imp.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := back.File.WriteSet(LangSource); len(got) != 2 || got[0] != FieldDescriptionHTML {
		t.Fatalf("yapılandırma saklanmadı: %v", got)
	}
	if back.File.Writes(LangField{Field: FieldTitle}) {
		t.Error("kapatılan alan geri açıldı")
	}

	// And it survives a re-read, which rebuilds products and the brand kit.
	if _, err := s.Reread(ctx, imp.ID, llm.Selection{}); err != nil {
		t.Fatalf("Reread: %v", err)
	}
	after, _ := s.Get(ctx, imp.ID)
	if after.File.Writes(LangField{Field: FieldTitle}) {
		t.Error("yeniden okuma yapılandırmayı sıfırladı")
	}
	if len(ms.imports) != 1 {
		t.Errorf("beklenmeyen import sayısı: %d", len(ms.imports))
	}
}

// Selecting only fields this file has no column for would store an empty set,
// and an empty set means "everything" — so it would silently turn every switch
// back on. Refused instead, with a sentence.
func TestSetWrite_RefusesASelectionThisFileCannotHonourAtAll(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas-fields.csv")
	ctx := context.Background()

	_, err := s.SetWrite(ctx, imp.ID, []LangField{{Field: FieldSEOTitle}})
	if !errors.Is(err, ErrNothingToWrite) {
		t.Fatalf("beklenen ErrNothingToWrite, alınan %v", err)
	}
	if _, err := s.SetWrite(ctx, imp.ID, []LangField{{Field: FieldHandle}}); !errors.Is(err, ErrNotWritable) {
		t.Fatalf("kimlik alanı için beklenen ErrNotWritable, alınan %v", err)
	}
}

// The file cannot say which language its translation columns hold, so a person
// does — and only then does the surface become readable.
func TestSetTargetLang_MakesTheTranslationColumnsReadable(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas-ceviriler.csv")
	ctx := context.Background()

	if !imp.File.PendingTarget() {
		t.Fatal("çeviri dosyası dilini sormuyor")
	}
	named, err := s.SetTargetLang(ctx, imp.ID, LangAR, llm.Selection{})
	if err != nil {
		t.Fatalf("SetTargetLang: %v", err)
	}
	if named.File.PendingTarget() {
		t.Error("dil verildikten sonra hâlâ bekliyor")
	}

	products, err := s.Products(ctx, ProductFilter{ImportID: imp.ID}, "v")
	if err != nil {
		t.Fatalf("Products: %v", err)
	}
	if len(products) == 0 {
		t.Fatal("ürün okunmadı")
	}
	// The translated copy the file already carried is now read, which is what
	// makes it possible to leave it alone rather than overwrite it.
	if products[0].Original.Title == "" {
		t.Error("kaynak dil başlığı okunmadı")
	}
	if products[0].Translations[LangAR].Title == "" {
		t.Error("hedef dil başlığı okunmadı")
	}
}

func TestSetTargetLang_RefusesAFileWithNoTranslationColumns(t *testing.T) {
	s, _, imp := importedStudio(t, "ikas.csv")
	_, err := s.SetTargetLang(context.Background(), imp.ID, LangAR, llm.Selection{})
	if !errors.Is(err, ErrNoTargetColumns) {
		t.Fatalf("beklenen ErrNoTargetColumns, alınan %v", err)
	}
}
