package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/logrenant/mimir/internal/catalog"
)

// A decision is per language on the wire too. Without this the screen could
// only ever approve the file's own language, which is the state the whole
// per-language change exists to leave.
func TestCatalogStatus_RecordsTheDecisionAgainstTheRequestedLanguage(t *testing.T) {
	fc := &fakeCatalog{}
	resp := catalogDo(t, catalogHandler(t, fc), http.MethodPost,
		"/catalog/products/prd_a/status?lang=ar", `{"status":"approved"}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("beklenen 200, alınan %d (%s)", resp.Code, resp.Body.String())
	}
	if fc.lang != catalog.LangAR {
		t.Errorf("karar dili stüdyoya geçmedi: %q", fc.lang)
	}
}

// An unknown language is a 400, never a silent downgrade to the source one: a
// client handed the Turkish copy under an Arabic tab has no way to tell.
func TestCatalogStatus_RefusesALanguageThisBinaryDoesNotCarry(t *testing.T) {
	resp := catalogDo(t, catalogHandler(t, &fakeCatalog{}), http.MethodPost,
		"/catalog/products/prd_a/status?lang=de", `{"status":"approved"}`)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("beklenen 400, alınan %d (%s)", resp.Code, resp.Body.String())
	}
}

func TestCatalogDraft_SavesAgainstTheRequestedLanguage(t *testing.T) {
	fc := &fakeCatalog{}
	resp := catalogDo(t, catalogHandler(t, fc), http.MethodPut,
		"/catalog/products/prd_a/draft?lang=ar",
		`{"content":{"title":"عنوان"},"fields":["title"]}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("beklenen 200, alınan %d (%s)", resp.Code, resp.Body.String())
	}
	if fc.lang != catalog.LangAR {
		t.Errorf("taslak dili stüdyoya geçmedi: %q", fc.lang)
	}
}

// --- the outputs listing ----------------------------------------------------

func TestCatalogOutputs_PassesTheFiltersThroughAndReturnsThePage(t *testing.T) {
	fc := &fakeCatalog{outputs: catalog.OutputPage{
		Outputs: []catalog.DraftRow{{
			ImportID: "imp_1", Filename: "ceviriler.csv", Dialect: "ikas-ceviriler",
			ProductID: "prd_a", Title: "شامبو", Lang: catalog.LangAR,
			Status: catalog.StatusDrafted, Changed: []string{"description_html"},
		}},
		Limit: 50, HasMore: true,
	}}

	resp := catalogDo(t, catalogHandler(t, fc), http.MethodGet,
		"/catalog/outputs?lang=ar&dialect=ikas-ceviriler&status=drafted&limit=50", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("beklenen 200, alınan %d (%s)", resp.Code, resp.Body.String())
	}
	if got := fc.outputFilter; len(got.Langs) != 1 || got.Langs[0] != catalog.LangAR ||
		got.Dialect != "ikas-ceviriler" || got.Status != catalog.StatusDrafted || got.Limit != 50 {
		t.Errorf("süzgeçler stüdyoya geçmedi: %+v", got)
	}

	var body catalog.OutputPage
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("yanıt çözülemedi: %v", err)
	}
	if len(body.Outputs) != 1 || body.Outputs[0].Lang != catalog.LangAR {
		t.Fatalf("satır gelmedi: %+v", body.Outputs)
	}
	if !body.HasMore {
		t.Error("bir sonraki sayfa olduğu söylenmedi")
	}
}

// Absent means every language HERE and the source language everywhere else,
// which is the one place that asymmetry exists: "" is a real answer, so there
// is no value left over to spell "unset" with.
func TestCatalogOutputs_AnAbsentLanguageAsksForEveryLanguage(t *testing.T) {
	fc := &fakeCatalog{}
	resp := catalogDo(t, catalogHandler(t, fc), http.MethodGet, "/catalog/outputs", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("beklenen 200, alınan %d (%s)", resp.Code, resp.Body.String())
	}
	if fc.outputFilter.Langs != nil {
		t.Errorf("dil verilmediğinde tek dile daraltıldı: %+v", fc.outputFilter.Langs)
	}
}

func TestCatalogOutputs_RefusesALanguageThisBinaryDoesNotCarry(t *testing.T) {
	resp := catalogDo(t, catalogHandler(t, &fakeCatalog{}), http.MethodGet,
		"/catalog/outputs?lang=de", "")
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("beklenen 400, alınan %d (%s)", resp.Code, resp.Body.String())
	}
}

func TestCatalogOutputs_RefusesAStatusOutsideTheClosedSet(t *testing.T) {
	resp := catalogDo(t, catalogHandler(t, &fakeCatalog{}), http.MethodGet,
		"/catalog/outputs?status=yayinlandi", "")
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("beklenen 400, alınan %d (%s)", resp.Code, resp.Body.String())
	}
}

func TestCatalogOutputs_IsAbsentWithoutTheStudio(t *testing.T) {
	h := New(testConfig(), Deps{}).Handler()
	if resp := catalogDo(t, h, http.MethodGet, "/catalog/outputs", ""); resp.Code != http.StatusNotFound {
		t.Errorf("beklenen 404, alınan %d", resp.Code)
	}
}

// --- the target language question -------------------------------------------

// IKAS's Çeviriler export says "Çevrilecek İsim" and nothing in it says what it
// was translated into. The screen that asks has been in the desktop app all
// along, gated on a field the daemon never sent — so on the one file that most
// needed it, the question could not be asked and the language could not be set.
func TestCatalogImportView_SaysWhenNobodyHasNamedTheTargetLanguage(t *testing.T) {
	f := parseFixtureFile(t, "ikas-ceviriler.csv")
	fc := &fakeCatalog{imp: catalog.Import{ID: "imp_1", Filename: "ceviriler.csv", File: f}}

	resp := catalogDo(t, catalogHandler(t, fc), http.MethodGet, "/catalog/imports/imp_1", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("beklenen 200, alınan %d (%s)", resp.Code, resp.Body.String())
	}
	var body struct {
		PendingTarget bool   `json:"pending_target"`
		TargetLang    string `json:"target_lang"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("yanıt çözülemedi: %v", err)
	}
	if !body.PendingTarget {
		t.Fatal("dili söylenmemiş çeviri yüzeyi tele çıkmadı")
	}
	if body.TargetLang != "" {
		t.Errorf("kimse söylemediği hâlde bir hedef dil bildirildi: %q", body.TargetLang)
	}
}

// The mapping form exists precisely so an operator can point at a column no
// profile names — the `Html:Detay-EN` their own store created. Offering only
// the languages that already resolve made that column unreachable.
func TestCatalogImportView_OffersEveryLanguageThisBinaryCanWrite(t *testing.T) {
	f := parseFixtureFile(t, "ikas.csv")
	fc := &fakeCatalog{imp: catalog.Import{ID: "imp_1", Filename: "urunler.csv", File: f}}

	resp := catalogDo(t, catalogHandler(t, fc), http.MethodGet, "/catalog/imports/imp_1", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("beklenen 200, alınan %d (%s)", resp.Code, resp.Body.String())
	}
	var body struct {
		Languages []struct {
			Lang string `json:"lang"`
		} `json:"languages"`
		WritableLanguages []struct {
			Lang  string `json:"lang"`
			Label string `json:"label"`
		} `json:"writable_languages"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("yanıt çözülemedi: %v", err)
	}
	// This fixture carries only its own language…
	if len(body.Languages) != 1 {
		t.Fatalf("dosyanın taşıdığı dil sayısı değişti: %+v", body.Languages)
	}
	// …and the form is still offered every language a column could be named for.
	if len(body.WritableLanguages) != len(catalog.Langs()) {
		t.Fatalf("yazılabilir dil listesi eksik: %+v", body.WritableLanguages)
	}
	for _, l := range body.WritableLanguages {
		if l.Label == "" {
			t.Errorf("dil adsız geldi: %+v", l)
		}
	}
}
