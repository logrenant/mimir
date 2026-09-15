package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/catalog"
	"github.com/logrenant/mimir/internal/catalogjob"
	"github.com/logrenant/mimir/internal/coderunner"
	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/llm"
	"github.com/logrenant/mimir/internal/store"
)

// fakeCatalog records what the door handed through, which is all a door test
// can honestly assert.
type fakeCatalog struct {
	imports      []catalog.Import
	counts       map[string]map[catalog.Lang]map[string]int
	lang         catalog.Lang
	outputs      catalog.OutputPage
	outputFilter catalog.OutputFilter
	imported     []byte
	filename     string
	mapping      map[catalog.LangField]string
	reread       string
	savedFields  []catalog.Field
	status       string
	exported     string
	version      string
	dialect      string
	imp          catalog.Import
	write        []catalog.LangField
	targetLang   catalog.Lang
	scannedURL   string
	err          error
}

func (f *fakeCatalog) Import(_ context.Context, name string, data []byte, _ llm.Selection) (catalog.Import, error) {
	f.filename, f.imported = name, data
	return catalog.Import{ID: "imp_1", Filename: name, ProductCount: 2}, f.err
}

func (f *fakeCatalog) Get(_ context.Context, id string) (catalog.Import, error) {
	if f.err != nil {
		return catalog.Import{}, f.err
	}
	if f.imp.ID != "" {
		return f.imp, nil
	}
	return catalog.Import{ID: id, Brand: catalog.BrandKit{Version: "brand-v1:ab"}}, nil
}

func (f *fakeCatalog) List(context.Context, int) ([]catalog.Import, error) {
	return f.imports, f.err
}

func (f *fakeCatalog) StatusCounts(context.Context) map[string]map[catalog.Lang]map[string]int {
	return f.counts
}
func (f *fakeCatalog) Delete(context.Context, string) error { return f.err }

func (f *fakeCatalog) SetMapping(_ context.Context, id string, m map[catalog.LangField]string, _ llm.Selection) (catalog.Import, error) {
	f.mapping = m
	return catalog.Import{ID: id}, f.err
}

func (f *fakeCatalog) Reread(_ context.Context, id string, _ llm.Selection) (catalog.Import, error) {
	f.reread = id
	return catalog.Import{ID: id}, f.err
}

func (f *fakeCatalog) SetBrand(_ context.Context, id string, v catalog.Voice) (catalog.Import, error) {
	return catalog.Import{ID: id, Brand: catalog.BrandKit{Voice: v}}, f.err
}

func (f *fakeCatalog) RescanBrand(_ context.Context, id string, _ llm.Selection) (catalog.Import, error) {
	return catalog.Import{ID: id}, f.err
}

func (f *fakeCatalog) ScanSite(_ context.Context, _, url string) (catalog.SiteScan, error) {
	f.scannedURL = url
	return catalog.SiteScan{URL: url, Theme: catalog.SiteTheme{
		Background: "rgb(255, 255, 255)", Text: "rgb(17, 17, 17)",
	}}, f.err
}

func (f *fakeCatalog) Products(_ context.Context, _ catalog.ProductFilter, v string) ([]catalog.ProductView, error) {
	f.version = v
	return []catalog.ProductView{}, f.err
}

func (f *fakeCatalog) Product(_ context.Context, id string, lang catalog.Lang, v string) (catalog.ProductView, error) {
	if f.err != nil {
		return catalog.ProductView{}, f.err
	}
	f.lang = lang
	pv := catalog.ProductView{}
	pv.ID, pv.ImportID = id, "imp_1"
	return pv, nil
}

func (f *fakeCatalog) SaveDraft(_ context.Context, id, v string, lang catalog.Lang, c catalog.Content, fields []catalog.Field) (catalog.StoredDraft, error) {
	f.savedFields = fields
	f.lang = lang
	return catalog.StoredDraft{ProductID: id, Version: v, Content: c}, f.err
}

func (f *fakeCatalog) SetStatus(_ context.Context, _ string, lang catalog.Lang, status, _ string) error {
	f.status, f.lang = status, lang
	return f.err
}

func (f *fakeCatalog) Outputs(_ context.Context, filter catalog.OutputFilter) (catalog.OutputPage, error) {
	f.outputFilter = filter
	return f.outputs, f.err
}

func (f *fakeCatalog) Export(_ context.Context, id string) (catalog.ExportResult, error) {
	f.exported = id
	return catalog.ExportResult{Path: "/tmp/x.csv", Products: 2, Changed: 1}, f.err
}

func (f *fakeCatalog) DraftVersion(kit catalog.BrandKit, sel llm.Selection, skill string, lang catalog.Lang) string {
	if lang != catalog.LangSource {
		return "content-v1:" + kit.Version + "/" + string(lang)
	}
	return "content-v1:" + kit.Version
}

func (f *fakeCatalog) SetDialect(ctx context.Context, id, key string, _ llm.Selection) (catalog.Import, error) {
	f.dialect = key
	return f.Get(ctx, id)
}

func (f *fakeCatalog) SetWrite(ctx context.Context, id string, want []catalog.LangField) (catalog.Import, error) {
	f.write = want
	return f.Get(ctx, id)
}

func (f *fakeCatalog) SetTargetLang(ctx context.Context, id string, lang catalog.Lang, _ llm.Selection) (catalog.Import, error) {
	f.targetLang = lang
	return f.Get(ctx, id)
}

func (f *fakeCatalog) CurrentDraftVersion(ctx context.Context, importID string, lang catalog.Lang) (string, error) {
	imp, err := f.Get(ctx, importID)
	if err != nil {
		return "", err
	}
	return f.DraftVersion(imp.Brand, llm.Selection{}, "", lang), nil
}

func catalogHandler(t *testing.T, c CatalogStudio) http.Handler {
	t.Helper()
	return New(testConfig(), Deps{Catalog: c}).Handler()
}

func catalogDo(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return do(h, method, path, testToken, body)
}

// A route is registered only when its dependency is present. Without the
// studio, /catalog/* is not a route that answers badly — it is not a route.
func TestCatalogRoutes_AreAbsentWithoutTheStudio(t *testing.T) {
	h := New(testConfig(), Deps{}).Handler()

	if resp := catalogDo(t, h, http.MethodGet, "/catalog/imports", ""); resp.Code != http.StatusNotFound {
		t.Errorf("beklenen 404, alınan %d", resp.Code)
	}
}

func TestCatalogImport_DecodesBase64AndPassesTheBytesThrough(t *testing.T) {
	fc := &fakeCatalog{}
	h := catalogHandler(t, fc)

	raw := "Handle,Title\r\na,Bir\r\n"
	body, _ := json.Marshal(catalogImportRequest{
		Filename: "urunler.csv", DataBase64: base64.StdEncoding.EncodeToString([]byte(raw)),
	})
	resp := catalogDo(t, h, http.MethodPost, "/catalog/imports", string(body))
	if resp.Code != http.StatusCreated {
		t.Fatalf("beklenen 201, alınan %d", resp.Code)
	}
	// The bytes must arrive undisturbed — the encoding sniff downstream reads
	// them, and a mangled upload is a mangled catalog.
	if string(fc.imported) != raw {
		t.Errorf("baytlar bozuldu: %q", fc.imported)
	}
	if fc.filename != "urunler.csv" {
		t.Errorf("dosya adı geçmedi: %q", fc.filename)
	}
}

func TestCatalogImport_RejectsSomethingThatIsNotBase64(t *testing.T) {
	h := catalogHandler(t, &fakeCatalog{})
	resp := catalogDo(t, h, http.MethodPost, "/catalog/imports",
		`{"filename":"x.csv","data_base64":"!!!"}`)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("beklenen 400, alınan %d", resp.Code)
	}
}

func TestCatalogImport_RejectsAnEmptyFile(t *testing.T) {
	h := catalogHandler(t, &fakeCatalog{})
	resp := catalogDo(t, h, http.MethodPost, "/catalog/imports",
		`{"filename":"x.csv","data_base64":""}`)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("beklenen 400, alınan %d", resp.Code)
	}
}

// The import route is the one payload here measured in megabytes, and it has
// its own entry rather than a raised cap for the whole surface.
func TestBodyLimits_CoversTheCatalogImportRouteAndNothingElseNew(t *testing.T) {
	cfg := config.Load()
	f, ok := bodyLimits["POST /catalog/imports"]
	if !ok {
		t.Fatal("katalog import rotası gövde sınırı tablosunda yok")
	}
	if got := f(cfg); got != cfg.CatalogCSVMaxBytes {
		t.Errorf("beklenen CatalogCSVMaxBytes, alınan %d", got)
	}
	if len(bodyLimits) != 2 {
		t.Errorf("gövde sınırı tablosu beklenmedik biçimde büyüdü: %d satır", len(bodyLimits))
	}
}

func TestCatalogStatus_RefusesAStatusOutsideTheClosedSet(t *testing.T) {
	fc := &fakeCatalog{}
	h := catalogHandler(t, fc)

	resp := catalogDo(t, h, http.MethodPost, "/catalog/products/prd_a/status", `{"status":"yayinlandi"}`)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("beklenen 400, alınan %d", resp.Code)
	}
	if fc.status != "" {
		t.Errorf("geçersiz durum motora kadar gitti: %q", fc.status)
	}

	resp = catalogDo(t, h, http.MethodPost, "/catalog/products/prd_a/status", `{"status":"approved"}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("beklenen 200, alınan %d", resp.Code)
	}
	if fc.status != catalog.StatusApproved {
		t.Errorf("durum geçmedi: %q", fc.status)
	}
}

// The draft version is a cache key. A client that could name one could serve
// itself copy written under a brand voice that no longer exists, and would have
// no way of knowing.
func TestCatalogProducts_ResolveTheVersionOnTheServer(t *testing.T) {
	fc := &fakeCatalog{}
	h := catalogHandler(t, fc)

	resp := catalogDo(t, h, http.MethodGet, "/catalog/products?import_id=imp_1&version=uydurma", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("beklenen 200, alınan %d", resp.Code)
	}
	if fc.version != "content-v1:brand-v1:ab" {
		t.Errorf("sürüm sunucuda çözülmedi: %q", fc.version)
	}
}

func TestCatalogProducts_RequireAnImport(t *testing.T) {
	h := catalogHandler(t, &fakeCatalog{})
	if resp := catalogDo(t, h, http.MethodGet, "/catalog/products", ""); resp.Code != http.StatusBadRequest {
		t.Fatalf("beklenen 400, alınan %d", resp.Code)
	}
}

func TestCatalogProducts_RejectANegativeLimit(t *testing.T) {
	h := catalogHandler(t, &fakeCatalog{})
	resp := catalogDo(t, h, http.MethodGet, "/catalog/products?import_id=imp_1&limit=-3", "")
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("beklenen 400, alınan %d", resp.Code)
	}
}

func TestCatalogDraft_PassesTheNamedFieldsThrough(t *testing.T) {
	fc := &fakeCatalog{}
	h := catalogHandler(t, fc)

	resp := catalogDo(t, h, http.MethodPut, "/catalog/products/prd_a/draft",
		`{"content":{"title":"Yeni"},"fields":["title","seo_title"]}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("beklenen 200, alınan %d", resp.Code)
	}
	if len(fc.savedFields) != 2 || fc.savedFields[0] != catalog.FieldTitle {
		t.Errorf("alanlar geçmedi: %v", fc.savedFields)
	}
}

// A client sending "seoTitle" instead of "seo_title" is told, not handed a
// confusing validation error later.
func TestCatalogDraft_RefusesAnUnknownField(t *testing.T) {
	h := catalogHandler(t, &fakeCatalog{})
	resp := catalogDo(t, h, http.MethodPut, "/catalog/products/prd_a/draft",
		`{"content":{"title":"x"},"fieldz":["title"]}`)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("beklenen 400, alınan %d", resp.Code)
	}
}

func TestCatalogExport_ReturnsThePathItWrote(t *testing.T) {
	fc := &fakeCatalog{}
	h := catalogHandler(t, fc)

	resp := catalogDo(t, h, http.MethodPost, "/catalog/imports/imp_1/export", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("beklenen 200, alınan %d", resp.Code)
	}
	if fc.exported != "imp_1" {
		t.Errorf("import id geçmedi: %q", fc.exported)
	}
	var res catalog.ExportResult
	if err := json.Unmarshal(resp.Body.Bytes(), &res); err != nil {
		t.Fatalf("cevap çözülemedi: %v", err)
	}
	if res.Path == "" || res.Changed != 1 {
		t.Errorf("dışa aktarım sonucu eksik: %+v", res)
	}
}

func TestCatalogImportView_ReportsTheFramingBackToTheOperator(t *testing.T) {
	// An operator who exported semicolon-delimited Windows-1254 needs to see
	// those two words back before they trust anything else on the screen.
	imp := catalog.Import{ID: "imp_1"}
	imp.File.Delimiter = ';'
	imp.File.Encoding = catalog.EncodingCP1254
	imp.File.HasBOM = true

	view := catalogImportView(imp, 120)
	if view.Framing.Delimiter != ";" || view.Framing.Encoding != "windows-1254" || !view.Framing.HasBOM {
		t.Errorf("çerçeve rapor edilmedi: %+v", view.Framing)
	}
	for _, f := range view.Fields {
		if f == string(catalog.FieldHandle) {
			t.Error("handle yazılabilir alan olarak sunuldu")
		}
	}
}

// The screen's gate. A file whose header no profile matched, but whose columns
// the operator has mapped by hand, is readable — and the view has to say so,
// because saying only "dialect: ”" is what left an operator pressing save on a
// form that then showed itself again.
func TestCatalogImportView_SaysAMappedFileIsReadable(t *testing.T) {
	imp := catalog.Import{ID: "imp_1"}
	imp.File.Header = []string{"kod", "ad", "metin"}
	imp.File.Rows = [][]string{{"A1", "Krem", "<p>Nemlendirici</p>"}}

	view := catalogImportView(imp, 120)
	if view.Readable {
		t.Error("eşlemesiz dosya okunabilir bildirildi")
	}
	if view.Sample["ad"] != "Krem" {
		t.Errorf("örnek değer gönderilmedi: %+v", view.Sample)
	}

	imp.File.Mapping = map[catalog.Field]string{catalog.FieldTitle: "ad"}
	view = catalogImportView(imp, 120)
	if !view.Readable {
		t.Error("eşlenmiş dosya okunamaz bildirildi")
	}
	if view.Mapping["title"] != "ad" {
		t.Errorf("kayıtlı eşleme gönderilmedi: %+v", view.Mapping)
	}
}

// A guess beside a matched profile would suggest the profile is one.
func TestCatalogImportView_OffersASuggestionOnlyWhenNothingMatched(t *testing.T) {
	imp := catalog.Import{ID: "imp_1"}
	imp.File.Header = []string{"Product Name", "Description"}
	if got := catalogImportView(imp, 120).Suggested["title"]; got != "Product Name" {
		t.Errorf("öneri gönderilmedi: %q", got)
	}

	imp.File.Dialect = catalog.Dialect{Key: "shopify"}
	if s := catalogImportView(imp, 120).Suggested; len(s) != 0 {
		t.Errorf("lehçe tanınmışken öneri gönderildi: %+v", s)
	}
}

// parseFixtureFile reads one of internal/catalog's real-export fixtures. The
// header rows there are copied verbatim from files a store actually downloads,
// which is what makes a claim about "five fields" mean anything.
func parseFixtureFile(t *testing.T, name string) catalog.File {
	t.Helper()
	raw, err := os.ReadFile("../catalog/testdata/" + name)
	if err != nil {
		t.Fatalf("fikstür okunamadı: %v", err)
	}
	f, err := catalog.ParseCSV(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ParseCSV(%s): %v", name, err)
	}
	return f
}

// The label an operator read for every import they ever opened: "hiçbir alan
// yazılmayacak", over a file with five perfectly good columns. `catalogLangView`
// had no field list at all, so the screen had nothing to read, the panel that
// would have let anybody fix it said "bu dilde yazılabilecek bir sütun yok",
// and the pass then queued behind that label wrote all five fields anyway.
func TestCatalogImportView_SendsTheFieldsAPassWouldActuallyWrite(t *testing.T) {
	f := parseFixtureFile(t, "ikas.csv")
	imp := catalog.Import{ID: "imp_1", File: f}

	langs := catalogImportView(imp, 120).Languages
	if len(langs) != 1 {
		t.Fatalf("beklenen tek dil, alınan %d", len(langs))
	}
	// Nothing has been configured, and "not configured" means everything the
	// file offers — the rule internal/catalog enforces and this layer contradicted.
	if len(langs[0].Fields) != 5 {
		t.Fatalf("beklenen 5 alan, alınan %d: %+v", len(langs[0].Fields), langs[0].Fields)
	}
	for _, got := range langs[0].Fields {
		if !got.Write {
			t.Errorf("yapılandırılmamış dosyada %q kapalı bildirildi", got.Key)
		}
		if got.Key != got.Field || got.Lang != "" {
			t.Errorf("kaynak dilin anahtarı çıplak alan adı olmalı: %+v", got)
		}
	}
}

// And the configuration is a gate the view reports honestly: two switched off
// stay off, the rest stay on. Reading it as "nothing" in either direction is
// how a screen ends up describing a pass that does the opposite.
func TestCatalogImportView_ReportsTheOperatorsFieldConfiguration(t *testing.T) {
	f := parseFixtureFile(t, "ikas.csv")
	f.Write = []catalog.LangField{
		{Field: catalog.FieldTitle},
		{Field: catalog.FieldSEOTitle},
		{Field: catalog.FieldTags},
	}
	imp := catalog.Import{ID: "imp_1", File: f}

	on := map[string]bool{}
	for _, got := range catalogImportView(imp, 120).Languages[0].Fields {
		on[got.Key] = got.Write
	}
	for _, want := range []string{"title", "seo_title", "tags"} {
		if !on[want] {
			t.Errorf("açık bırakılan %q kapalı bildirildi", want)
		}
	}
	for _, want := range []string{"description_html", "seo_description"} {
		if on[want] {
			t.Errorf("kapatılan %q açık bildirildi", want)
		}
	}
}

// A target language gets its own composite key, so a client posts back exactly
// what it was handed and a map saved before languages existed still parses.
func TestCatalogImportView_NamesATargetLanguagesFieldsUnderTheirOwnKey(t *testing.T) {
	f := parseFixtureFile(t, "ikas-fields.csv")
	imp := catalog.Import{ID: "imp_1", File: f}

	langs := catalogImportView(imp, 120).Languages
	if len(langs) != 2 {
		t.Fatalf("beklenen iki dil, alınan %d", len(langs))
	}
	if len(langs[1].Fields) != 1 || langs[1].Fields[0].Key != "description_html@ar" {
		t.Errorf("Arapça alan anahtarı yanlış: %+v", langs[1].Fields)
	}
}

// --- POST /catalog/rewrite ---------------------------------------------------

// The queue is the package's own fakeRunner: it already records what a card was
// written with, which is all a door test can honestly assert.

func rewriteHandler(t *testing.T, c CatalogStudio, r CodeRunner) http.Handler {
	t.Helper()
	return New(testConfig(), Deps{Catalog: c, Runner: r}).Handler()
}

// The rule handleDraftOutreach established, and it matters more here: a filter
// would let one short string spend a whole catalog's worth of searches, crawls
// and model calls, and the operator would find out from the bill rather than
// from the board.
func TestCatalogRewrite_TakesAnExplicitListAndNeverAFilter(t *testing.T) {
	fr := &fakeRunner{run: coderunner.Run{ID: "run_1"}}
	h := rewriteHandler(t, &fakeCatalog{}, fr)

	// A body that names a filter instead of products is refused.
	resp := catalogDo(t, h, http.MethodPost, "/catalog/rewrite",
		`{"import_id":"imp_1","product_ids":[]}`)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("beklenen 400, alınan %d", resp.Code)
	}
	if !strings.Contains(resp.Body.String(), "never a filter") {
		t.Errorf("ret sebebi kuralı söylemiyor: %s", resp.Body.String())
	}
	if fr.lastCreate.Agent != "" {
		t.Error("boş seçim kuyruğa kadar gitti")
	}
}

func TestCatalogRewrite_QueuesACardOnTheCatalogAgent(t *testing.T) {
	fr := &fakeRunner{run: coderunner.Run{ID: "run_1"}}
	h := rewriteHandler(t, &fakeCatalog{}, fr)

	resp := catalogDo(t, h, http.MethodPost, "/catalog/rewrite",
		`{"import_id":"imp_1","product_ids":["prd_a","prd_b"],"fields":["title"]}`)
	if resp.Code != http.StatusCreated {
		t.Fatalf("beklenen 201, alınan %d (%s)", resp.Code, resp.Body.String())
	}
	if fr.lastCreate.Agent != catalogjob.Agent {
		t.Errorf("kart yanlış ajana yazıldı: %q", fr.lastCreate.Agent)
	}
	if fr.enqueued != "run_1" {
		t.Error("kart oluşturuldu ama kuyruğa alınmadı")
	}

	// The params the executor will parse, and the door's only real job.
	p, err := catalogjob.Parse(store.RunRow{Params: fr.lastCreate.Params})
	if err != nil {
		t.Fatalf("executor kartın params'ını çözemiyor: %v", err)
	}
	if p.ImportID != "imp_1" || len(p.ProductIDs) != 2 {
		t.Errorf("params eksik geçti: %+v", p)
	}
	// A card with no project: a catalog pass has no folder to be scoped to.
	if fr.lastCreate.ProjectID != "" {
		t.Errorf("katalog kartına proje kondu: %q", fr.lastCreate.ProjectID)
	}
}

func TestCatalogRewrite_CapsTheSelection(t *testing.T) {
	cfg := testConfig()
	ids := make([]string, cfg.CatalogProductsPageMax+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("\"prd_%d\"", i)
	}
	body := `{"import_id":"imp_1","product_ids":[` + strings.Join(ids, ",") + `]}`

	fr := &fakeRunner{run: coderunner.Run{ID: "run_1"}}
	resp := catalogDo(t, rewriteHandler(t, &fakeCatalog{}, fr), http.MethodPost,
		"/catalog/rewrite", body)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("beklenen 400, alınan %d", resp.Code)
	}
	if fr.lastCreate.Agent != "" {
		t.Error("tavanı aşan seçim kuyruğa gitti")
	}
}

func TestCatalogRewrite_RefusesAnIdentityField(t *testing.T) {
	resp := catalogDo(t, rewriteHandler(t, &fakeCatalog{}, &fakeRunner{run: coderunner.Run{ID: "run_1"}}),
		http.MethodPost, "/catalog/rewrite",
		`{"import_id":"imp_1","product_ids":["a"],"fields":["handle"]}`)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("beklenen 400, alınan %d", resp.Code)
	}
}

// Every other /catalog/* route answers on a daemon with no queue: importing a
// file, reading what the brand kit made of it and writing the file back out all
// need no runner at all.
func TestCatalogRoutes_TheReadsAnswerWithoutAQueue(t *testing.T) {
	h := catalogHandler(t, &fakeCatalog{}) // no Runner in Deps

	if resp := catalogDo(t, h, http.MethodGet, "/catalog/imports", ""); resp.Code != http.StatusOK {
		t.Errorf("kuyruksuz daemon'da liste %d döndü", resp.Code)
	}
	if resp := catalogDo(t, h, http.MethodPost, "/catalog/rewrite",
		`{"import_id":"imp_1","product_ids":["a"]}`); resp.Code != http.StatusNotFound {
		t.Errorf("kuyruksuz daemon toplu yeniden yazımı sundu: %d", resp.Code)
	}
}

// The desktop kept its own copy of the profile table and it went stale: it
// still offered a profile task-91 deleted, and it would have missed every
// profile added since. A closed set that lives in Go is answered from Go.
func TestCatalogProfiles_AnswersWithTheProfilesThisBinaryShips(t *testing.T) {
	h := catalogHandler(t, &fakeCatalog{})

	resp := catalogDo(t, h, http.MethodGet, "/catalog/profiles", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("beklenen 200, alınan %d", resp.Code)
	}
	var got struct {
		Profiles []catalogProfileView `json:"profiles"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
		t.Fatalf("çözümlenemedi: %v", err)
	}

	want := catalog.Dialects()
	if len(got.Profiles) != len(want) {
		t.Fatalf("beklenen %d profil, alınan %d", len(want), len(got.Profiles))
	}
	// The columns matter as much as the names: a client draws the mapping form
	// from them, and a profile that answered with no columns would look like a
	// profile that reads nothing.
	byKey := map[string]catalogProfileView{}
	for _, p := range got.Profiles {
		byKey[p.Key] = p
	}
	ikas, ok := byKey["ikas"]
	if !ok {
		t.Fatalf("ikas profili yanıtta yok: %+v", got.Profiles)
	}
	if ikas.Columns[string(catalog.FieldTitle)] != "İsim" {
		t.Errorf("ikas başlık sütunu eksik ya da yanlış: %+v", ikas.Columns)
	}
	if ikas.GroupBy != string(catalog.FieldGroupID) {
		t.Errorf("ikas group_by yanlış: %q", ikas.GroupBy)
	}
}

func TestCatalogProfiles_IsAbsentWithoutTheStudio(t *testing.T) {
	h := New(testConfig(), Deps{}).Handler()
	if resp := catalogDo(t, h, http.MethodGet, "/catalog/profiles", ""); resp.Code != http.StatusNotFound {
		t.Errorf("beklenen 404, alınan %d", resp.Code)
	}
}

func TestSetCatalogDialect_PassesTheOperatorsPickThrough(t *testing.T) {
	fc := &fakeCatalog{}
	h := catalogHandler(t, fc)

	resp := catalogDo(t, h, http.MethodPut, "/catalog/imports/imp_1/dialect", `{"key":"ikas-fields"}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("beklenen 200, alınan %d: %s", resp.Code, resp.Body.String())
	}
	if fc.dialect != "ikas-fields" {
		t.Errorf("seçilen profil stüdyoya ulaşmadı: %q", fc.dialect)
	}
}

// An empty key is a request, not a missing field: it returns the file to
// detection. A handler that rejected it would leave a wrong pick permanent.
func TestSetCatalogDialect_AnEmptyKeyReachesTheStudio(t *testing.T) {
	fc := &fakeCatalog{dialect: "ikas"}
	h := catalogHandler(t, fc)

	resp := catalogDo(t, h, http.MethodPut, "/catalog/imports/imp_1/dialect", `{"key":""}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("beklenen 200, alınan %d", resp.Code)
	}
	if fc.dialect != "" {
		t.Errorf("boş anahtar stüdyoya iletilmedi: %q", fc.dialect)
	}
}

// This layer must not compose the draft key itself. It did, and it composed a
// different one from the board card's, which made every bulk-written draft
// invisible. Asking the studio is the contract now.
func TestCatalogProducts_AsksTheStudioForTheDraftKey(t *testing.T) {
	fc := &fakeCatalog{}
	h := catalogHandler(t, fc)

	resp := catalogDo(t, h, http.MethodGet, "/catalog/products?import_id=imp_1", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("beklenen 200, alınan %d", resp.Code)
	}
	if fc.version != "content-v1:brand-v1:ab" {
		t.Errorf("stüdyonun kurduğu anahtar kullanılmadı: %q", fc.version)
	}
}

// A client draws its language tabs from the import, not from a constant: the
// answer depends on the operator's own export. Offering Arabic for a file with
// no Arabic column would let somebody pay for copy that cannot be written.
func TestCatalogImportView_ReportsOnlyTheLanguagesTheFileCanCarry(t *testing.T) {
	raw, err := os.ReadFile("../catalog/testdata/ikas-fields.csv")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	f, err := catalog.ParseCSV(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}

	got := catalogImportView(catalog.Import{File: f}, 120)
	if len(got.Languages) != 2 {
		t.Fatalf("beklenen 2 dil, alınan %d: %+v", len(got.Languages), got.Languages)
	}
	if got.Languages[0].Lang != "" || got.Languages[0].Dir != "ltr" {
		t.Errorf("ilk dil kaynak dil olmalı: %+v", got.Languages[0])
	}
	ar := got.Languages[1]
	if ar.Lang != "ar" || ar.Dir != "rtl" {
		t.Errorf("Arapça yönüyle birlikte bildirilmedi: %+v", ar)
	}
	if ar.Columns[string(catalog.FieldDescriptionHTML)] != "Html:Detay-AR" {
		t.Errorf("Arapça açıklama sütunu bildirilmedi: %+v", ar.Columns)
	}
}

// The wire key for the file's own language is the bare field name, so every
// column map an operator saved before languages existed still posts and parses.
func TestSetCatalogMapping_KeepsTheOldWireAndAcceptsALanguageSuffix(t *testing.T) {
	fc := &fakeCatalog{}
	h := catalogHandler(t, fc)

	body := `{"mapping":{"title":"İsim","description_html":"Html:Detay","description_html@ar":"Html:Detay-AR"}}`
	resp := catalogDo(t, h, http.MethodPut, "/catalog/imports/imp_1/mapping", body)
	if resp.Code != http.StatusOK {
		t.Fatalf("beklenen 200, alınan %d: %s", resp.Code, resp.Body.String())
	}
	if got := fc.mapping[catalog.LangField{Field: catalog.FieldTitle}]; got != "İsim" {
		t.Errorf("kaynak dil anahtarı çözülmedi: %q", got)
	}
	want := catalog.LangField{Field: catalog.FieldDescriptionHTML, Lang: catalog.LangAR}
	if got := fc.mapping[want]; got != "Html:Detay-AR" {
		t.Errorf("dil ekli anahtar çözülmedi: %q (%+v)", got, fc.mapping)
	}
}

func TestSetCatalogMapping_RefusesALanguageThisBinaryDoesNotCarry(t *testing.T) {
	h := catalogHandler(t, &fakeCatalog{})
	resp := catalogDo(t, h, http.MethodPut, "/catalog/imports/imp_1/mapping",
		`{"mapping":{"description_html@de":"Beschreibung"}}`)
	if resp.Code != http.StatusBadRequest {
		t.Errorf("beklenen 400, alınan %d", resp.Code)
	}
}

// A client asking for a language this binary does not carry must not be handed
// the source language's copy with no way to tell.
func TestCatalogProducts_RefusesAnUnknownLanguage(t *testing.T) {
	h := catalogHandler(t, &fakeCatalog{})
	resp := catalogDo(t, h, http.MethodGet, "/catalog/products?import_id=imp_1&lang=de", "")
	if resp.Code != http.StatusBadRequest {
		t.Errorf("beklenen 400, alınan %d", resp.Code)
	}
}

func TestCatalogProducts_AsksForTheRequestedLanguagesDraftKey(t *testing.T) {
	fc := &fakeCatalog{}
	h := catalogHandler(t, fc)

	resp := catalogDo(t, h, http.MethodGet, "/catalog/products?import_id=imp_1&lang=ar", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("beklenen 200, alınan %d", resp.Code)
	}
	if fc.version != "content-v1:brand-v1:ab/ar" {
		t.Errorf("Arapça taslak anahtarı istenmedi: %q", fc.version)
	}
}

// A language this file has no column for is refused before it costs anything.
// A pass whose output has nowhere to be written is a pass the operator paid for
// and cannot export.
func TestRewriteCatalog_RefusesALanguageTheFileHasNoColumnFor(t *testing.T) {
	raw, err := os.ReadFile("../catalog/testdata/shopify.csv")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	f, err := catalog.ParseCSV(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	fc := &fakeCatalog{imp: catalog.Import{ID: "imp_1", File: f}}
	h := rewriteHandler(t, fc, &fakeRunner{run: coderunner.Run{ID: "run_1"}})

	resp := catalogDo(t, h, http.MethodPost, "/catalog/rewrite",
		`{"import_id":"imp_1","product_ids":["p1"],"lang":"ar"}`)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("beklenen 400, alınan %d: %s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "sütun yok") {
		t.Errorf("sebep söylenmedi: %s", resp.Body.String())
	}
}

func TestRewriteCatalog_AcceptsALanguageTheFileCarries(t *testing.T) {
	raw, err := os.ReadFile("../catalog/testdata/ikas-fields.csv")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	f, err := catalog.ParseCSV(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ParseCSV: %v", err)
	}
	fc := &fakeCatalog{imp: catalog.Import{ID: "imp_1", File: f}}
	h := rewriteHandler(t, fc, &fakeRunner{run: coderunner.Run{ID: "run_1"}})

	resp := catalogDo(t, h, http.MethodPost, "/catalog/rewrite",
		`{"import_id":"imp_1","product_ids":["p1"],"lang":"ar"}`)
	if resp.Code != http.StatusCreated {
		t.Fatalf("beklenen 201, alınan %d: %s", resp.Code, resp.Body.String())
	}
}

func TestRewriteCatalog_RefusesALanguageThisBinaryDoesNotCarry(t *testing.T) {
	h := rewriteHandler(t, &fakeCatalog{}, &fakeRunner{run: coderunner.Run{ID: "run_1"}})
	resp := catalogDo(t, h, http.MethodPost, "/catalog/rewrite",
		`{"import_id":"imp_1","product_ids":["p1"],"lang":"de"}`)
	if resp.Code != http.StatusBadRequest {
		t.Errorf("beklenen 400, alınan %d", resp.Code)
	}
}

// --- the card's own model ----------------------------------------------------

// fakeCapabilities is a router that answers the one question the rewrite gate
// asks, and answers it differently per provider — which is the whole point: a
// card naming a provider that can serve a schema must not be refused because
// the machine default cannot.
type fakeCapabilities struct{ structured map[string]bool }

func (fakeCapabilities) Discover(context.Context, bool, time.Duration) []llm.Availability {
	return nil
}

func (fakeCapabilities) ProbeOne(context.Context, string, time.Duration) (llm.Availability, bool) {
	return llm.Availability{}, false
}

func (f fakeCapabilities) Capabilities(_ llm.Class, sel llm.Selection) (llm.Capabilities, error) {
	return llm.Capabilities{StructuredOutput: f.structured[sel.Provider]}, nil
}

// The fix for "I changed the model and nothing changed": the pair the operator
// picked is written onto the card, where the executor reads it, instead of
// being re-read from the settings file at every call.
func TestCatalogRewrite_PinsThePickedModelToTheCard(t *testing.T) {
	cfg := testConfig()
	if len(cfg.LLMProviders) == 0 {
		t.Skip("bu derlemede sağlayıcı listesi boş")
	}
	picked := cfg.LLMProviders[0]

	fr := &fakeRunner{run: coderunner.Run{ID: "run_1"}}
	h := New(cfg, Deps{Catalog: &fakeCatalog{}, Runner: fr}).Handler()

	body := fmt.Sprintf(
		`{"import_id":"imp_1","product_ids":["prd_a"],"provider":%q,"model":%q}`,
		picked.ID, picked.DefaultModel)
	if resp := catalogDo(t, h, http.MethodPost, "/catalog/rewrite", body); resp.Code != http.StatusCreated {
		t.Fatalf("beklenen 201, alınan %d (%s)", resp.Code, resp.Body.String())
	}

	p, err := catalogjob.Parse(store.RunRow{Params: fr.lastCreate.Params})
	if err != nil {
		t.Fatalf("executor kartın params'ını çözemiyor: %v", err)
	}
	if p.Selection().Provider != picked.ID || p.Selection().Model != picked.DefaultModel {
		t.Errorf("seçilen model karta yazılmadı: %+v", p.Selection())
	}
	// And on the column every screen already draws, so the board stops
	// labelling a catalog pass with a coding model it never spends.
	if fr.lastCreate.Model != picked.DefaultModel {
		t.Errorf("kartın model sütunu yanlış: %q", fr.lastCreate.Model)
	}
}

// A card that names nothing is every card written before the field existed. It
// stays empty rather than freezing today's saved choice, so a card sitting in
// the backlog follows the setting the operator changes tomorrow.
func TestCatalogRewrite_ACardThatNamesNoModelPinsNothing(t *testing.T) {
	fr := &fakeRunner{run: coderunner.Run{ID: "run_1"}}
	h := New(testConfig(), Deps{Catalog: &fakeCatalog{}, Runner: fr}).Handler()

	resp := catalogDo(t, h, http.MethodPost, "/catalog/rewrite",
		`{"import_id":"imp_1","product_ids":["prd_a"]}`)
	if resp.Code != http.StatusCreated {
		t.Fatalf("beklenen 201, alınan %d (%s)", resp.Code, resp.Body.String())
	}
	p, _ := catalogjob.Parse(store.RunRow{Params: fr.lastCreate.Params})
	if !p.Selection().IsZero() {
		t.Errorf("seçilmeyen model karta yazıldı: %+v", p.Selection())
	}
	if fr.lastCreate.Model != "" {
		t.Errorf("model sütununa uydurma bir değer kondu: %q", fr.lastCreate.Model)
	}
}

// Both names become argv to a subprocess, so the pair goes through the same
// allow-list every other per-run selection does.
func TestCatalogRewrite_RefusesAPairTheDaemonWillNotRun(t *testing.T) {
	fr := &fakeRunner{run: coderunner.Run{ID: "run_1"}}
	h := New(testConfig(), Deps{Catalog: &fakeCatalog{}, Runner: fr}).Handler()

	for name, body := range map[string]string{
		"bilinmeyen sağlayıcı": `{"import_id":"imp_1","product_ids":["a"],"provider":"uydurma","model":"yok"}`,
		"sağlayıcısız model":   `{"import_id":"imp_1","product_ids":["a"],"model":"qwen3:8b"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if resp := catalogDo(t, h, http.MethodPost, "/catalog/rewrite", body); resp.Code != http.StatusBadRequest {
				t.Fatalf("beklenen 400, alınan %d (%s)", resp.Code, resp.Body.String())
			}
		})
	}
	if fr.lastCreate.Agent != "" {
		t.Error("izin listesi dışındaki çift kuyruğa gitti")
	}
}

// The wall this whole change exists to remove: the schema gate is asked about
// the card's own provider, not about the machine default the operator has just
// picked their way around.
func TestCatalogRewrite_TheSchemaGateAsksAboutTheCardsProvider(t *testing.T) {
	cfg := testConfig()
	if len(cfg.LLMProviders) < 2 {
		t.Skip("bu derlemede tek sağlayıcı var")
	}
	blocked, fine := cfg.LLMProviders[0], cfg.LLMProviders[1]

	fr := &fakeRunner{run: coderunner.Run{ID: "run_1"}}
	h := New(cfg, Deps{
		Catalog: &fakeCatalog{},
		Runner:  fr,
		LLM:     fakeCapabilities{structured: map[string]bool{fine.ID: true}},
	}).Handler()

	ok := fmt.Sprintf(`{"import_id":"imp_1","product_ids":["a"],"provider":%q,"model":%q}`,
		fine.ID, fine.DefaultModel)
	if resp := catalogDo(t, h, http.MethodPost, "/catalog/rewrite", ok); resp.Code != http.StatusCreated {
		t.Fatalf("şema verebilen sağlayıcı reddedildi: %d (%s)", resp.Code, resp.Body.String())
	}

	no := fmt.Sprintf(`{"import_id":"imp_1","product_ids":["a"],"provider":%q,"model":%q}`,
		blocked.ID, blocked.DefaultModel)
	resp := catalogDo(t, h, http.MethodPost, "/catalog/rewrite", no)
	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("şema veremeyen sağlayıcı geçti: %d (%s)", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "yapılandırılmış çıktı") {
		t.Errorf("ret sebebi neyin eksik olduğunu söylemiyor: %s", resp.Body.String())
	}
}

// --- the catalogs list ------------------------------------------------------

// The list screen answers "which file do I open", and a filename does not
// answer it: what state the file is in — how many products are drafted,
// approved, still waiting — used to be reachable only by opening it.
func TestCatalogImports_RowsCarryTheProfileAndStatusCounts(t *testing.T) {
	f := parseFixtureFile(t, "ikas-fields.csv")
	fc := &fakeCatalog{
		imports: []catalog.Import{{ID: "imp_1", Filename: "ikas-urunler.csv", File: f}},
		counts: map[string]map[catalog.Lang]map[string]int{
			"imp_1": {
				catalog.LangSource: {"pending": 50, "drafted": 1},
				catalog.LangAR:     {"drafted": 3},
			},
		},
	}

	resp := catalogDo(t, catalogHandler(t, fc), http.MethodGet, "/catalog/imports", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("beklenen 200, alınan %d (%s)", resp.Code, resp.Body.String())
	}

	var body struct {
		Imports []struct {
			ID           string                    `json:"id"`
			Dialect      string                    `json:"dialect"`
			Counts       map[string]int            `json:"counts"`
			CountsByLang map[string]map[string]int `json:"counts_by_lang"`
		} `json:"imports"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("liste çözülemedi: %v", err)
	}
	if len(body.Imports) != 1 {
		t.Fatalf("beklenen bir satır, alınan %d", len(body.Imports))
	}
	row := body.Imports[0]
	if row.Counts["pending"] != 50 || row.Counts["drafted"] != 1 {
		t.Errorf("durum sayıları satıra gelmedi: %+v", row.Counts)
	}
	// `counts` keeps meaning the source language, byte for byte, because a
	// client that suddenly found an object of objects under it would render
	// nothing at all.
	if row.CountsByLang["ar"]["drafted"] != 3 {
		t.Errorf("dil başına sayım satıra gelmedi: %+v", row.CountsByLang)
	}
	// The profile survives a listing even though the file body does not.
	if row.Dialect == "" {
		t.Errorf("profil satıra gelmedi: %q", row.Dialect)
	}
}

// A store that cannot answer leaves the rows without their summary rather than
// failing the screen (SD-6) — the list is still a list without the counts.
func TestCatalogImports_ASummaryThatCannotBeReadDoesNotFailTheList(t *testing.T) {
	f := parseFixtureFile(t, "ikas-fields.csv")
	fc := &fakeCatalog{imports: []catalog.Import{{ID: "imp_1", Filename: "a.csv", File: f}}}

	resp := catalogDo(t, catalogHandler(t, fc), http.MethodGet, "/catalog/imports", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("beklenen 200, alınan %d", resp.Code)
	}
	if strings.Contains(resp.Body.String(), `"counts"`) {
		t.Errorf("bilinmeyen sayılar boş harita olarak yazıldı: %s", resp.Body.String())
	}
}

// The shop an operator types reaches the studio unchanged. The guard that
// refuses a private address lives in the domain, not here — this door only has
// to not lose or rewrite what was typed.
func TestScanCatalogSite_PassesTheURLThrough(t *testing.T) {
	fc := &fakeCatalog{}
	h := catalogHandler(t, fc)

	resp := catalogDo(t, h, http.MethodPost, "/catalog/imports/imp_1/site/scan",
		`{"url":"https://shop.example.com"}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("beklenen 200, alınan %d: %s", resp.Code, resp.Body.String())
	}
	if fc.scannedURL != "https://shop.example.com" {
		t.Errorf("URL değişerek geçti: %q", fc.scannedURL)
	}
	var got struct {
		Site catalog.SiteScan `json:"site"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
		t.Fatalf("yanıt okunamadı: %v", err)
	}
	if got.Site.Theme.Background != "rgb(255, 255, 255)" {
		t.Errorf("tema yanıtta yok: %+v", got.Site)
	}
}

// A refusal from the domain — a private address, an unreachable shop — is an
// answer the operator can act on, so it must not arrive as a 500.
func TestScanCatalogSite_ReportsTheDomainsRefusal(t *testing.T) {
	h := catalogHandler(t, &fakeCatalog{err: catalog.ErrUnknownImport})
	resp := catalogDo(t, h, http.MethodPost, "/catalog/imports/imp_1/site/scan",
		`{"url":"https://shop.example.com"}`)
	if resp.Code == http.StatusOK {
		t.Fatalf("hata 200 olarak döndü: %s", resp.Body.String())
	}
}
