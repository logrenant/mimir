package tools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/logrenant/mimir/internal/catalog"
	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/tools"
)

type fakeCatalogReader struct {
	imports  []catalog.Import
	products []catalog.ProductView
	product  catalog.ProductView
	version  string
	err      error
}

func (f *fakeCatalogReader) Get(_ context.Context, id string) (catalog.Import, error) {
	if f.err != nil {
		return catalog.Import{}, f.err
	}
	return catalog.Import{ID: id, Brand: catalog.BrandKit{Version: "brand-v1:ab"}}, nil
}

func (f *fakeCatalogReader) List(context.Context, int) ([]catalog.Import, error) {
	return f.imports, f.err
}

func (f *fakeCatalogReader) Products(_ context.Context, _ catalog.ProductFilter, v string) ([]catalog.ProductView, error) {
	f.version = v
	return f.products, f.err
}

func (f *fakeCatalogReader) Product(_ context.Context, _ string, _ catalog.Lang, v string) (catalog.ProductView, error) {
	if f.err != nil {
		return catalog.ProductView{}, f.err
	}
	f.version = v
	return f.product, nil
}

func (f *fakeCatalogReader) CurrentDraftVersion(ctx context.Context, importID string, _ catalog.Lang) (string, error) {
	imp, err := f.Get(ctx, importID)
	if err != nil {
		return "", err
	}
	return "content-v1:" + imp.Brand.Version, nil
}

func productView(id, title, html string) catalog.ProductView {
	var v catalog.ProductView
	v.ID, v.ImportID, v.Status = id, "imp_1", catalog.StatusDrafted
	v.Original.Title = title
	v.Original.DescriptionHTML = html
	return v
}

func handle(t *testing.T, tool interface {
	Handle(context.Context, json.RawMessage) (any, error)
}, args string) any {
	t.Helper()
	out, err := tool.Handle(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	return out
}

// A title and an SKU are the operator's own spreadsheet, and the description
// text this tool deliberately does not carry is what would have needed
// refining. So it is metadata, and it says so at the choke-point.
func TestCatalogProducts_IsMetadataOnlyAndCarriesNoDescription(t *testing.T) {
	fc := &fakeCatalogReader{products: []catalog.ProductView{
		productView("prd_a", "Nemlendirici Krem", "<p>gizli kalmalı</p>"),
	}}
	tool := tools.NewCatalogProducts(config.Load(), fc)

	out := handle(t, tool, `{"import_id":"imp_1"}`)
	meta, ok := out.(interface{ MetadataOnly() bool })
	if !ok || !meta.MetadataOnly() {
		t.Fatal("yanıt metadata_only işaretlenmemiş — choke-point'te kapanır")
	}
	budget, ok := out.(interface{ SizeBudgetTokens() int })
	if !ok || budget.SizeBudgetTokens() <= 0 {
		t.Fatal("yanıtın bir tavanı yok (SD-7)")
	}

	body, _ := json.Marshal(out)
	if strings.Contains(string(body), "gizli kalmalı") {
		t.Errorf("liste açıklama metnini taşıyor: %s", body)
	}
	if !strings.Contains(string(body), "Nemlendirici Krem") {
		t.Errorf("başlık yok: %s", body)
	}
}

// The version is a cache key and the daemon owns it. A caller that could name
// one could serve itself copy written under a brand voice that no longer
// exists, and would have no way of knowing.
func TestCatalogProducts_ResolvesTheVersionItself(t *testing.T) {
	fc := &fakeCatalogReader{}
	tool := tools.NewCatalogProducts(config.Load(), fc)
	handle(t, tool, `{"import_id":"imp_1"}`)
	if fc.version != "content-v1:brand-v1:ab" {
		t.Errorf("sürüm araç tarafında çözülmedi: %q", fc.version)
	}
}

func TestCatalogProducts_WithNoImportListsTheImports(t *testing.T) {
	fc := &fakeCatalogReader{imports: []catalog.Import{
		{ID: "imp_1", Filename: "urunler.csv", ProductCount: 12},
	}}
	body, _ := json.Marshal(handle(t, tools.NewCatalogProducts(config.Load(), fc), `{}`))
	if !strings.Contains(string(body), "urunler.csv") {
		t.Errorf("import listesi dönmedi: %s", body)
	}
}

// The description leaves as prose, never as the merchant's HTML: the blocks
// internal/catalog already parsed it into, bounded, and marked refined.
func TestCatalogProduct_ReturnsProseAndNeverMarkup(t *testing.T) {
	fc := &fakeCatalogReader{product: productView("prd_a", "Krem",
		`<div class="rte"><h2>Başlık</h2><p>Gövde <b>kalın</b>.</p></div>`)}
	out := handle(t, tools.NewCatalogProduct(config.Load(), fc), `{"product_id":"prd_a"}`)

	refined, ok := out.(interface{ IsRefined() bool })
	if !ok || !refined.IsRefined() {
		t.Fatal("yanıt refined işaretlenmemiş — choke-point'te kapanır")
	}
	body, _ := json.Marshal(out)
	for _, markup := range []string{"<div", "<h2", "class=", "<b>"} {
		if strings.Contains(string(body), markup) {
			t.Errorf("%q işaretlemesi tüketiciye sızdı: %s", markup, body)
		}
	}
	if !strings.Contains(string(body), "Başlık") || !strings.Contains(string(body), "Gövde") {
		t.Errorf("metin kayboldu: %s", body)
	}
}

func TestCatalogProduct_RequiresAProductID(t *testing.T) {
	tool := tools.NewCatalogProduct(config.Load(), &fakeCatalogReader{})
	if _, err := tool.Handle(context.Background(), json.RawMessage(`{"product_id":"  "}`)); err == nil {
		t.Fatal("boş id kabul edildi")
	}
}

// An error a consumer can act on, not a sentinel it has never heard of.
func TestCatalogTools_TranslateAMissIntoSomethingActionable(t *testing.T) {
	fc := &fakeCatalogReader{err: catalog.ErrUnknownProduct}
	_, err := tools.NewCatalogProduct(config.Load(), fc).
		Handle(context.Background(), json.RawMessage(`{"product_id":"yok"}`))
	if err == nil || !strings.Contains(err.Error(), "catalog_products") {
		t.Fatalf("hata mesajı bir sonraki adımı söylemiyor: %v", err)
	}
}

func TestCatalogTools_SchemasRefuseUnknownArguments(t *testing.T) {
	cfg := config.Load()
	for _, schema := range []json.RawMessage{
		tools.NewCatalogProducts(cfg, &fakeCatalogReader{}).InputSchema(),
		tools.NewCatalogProduct(cfg, &fakeCatalogReader{}).InputSchema(),
	} {
		var parsed map[string]any
		if err := json.Unmarshal(schema, &parsed); err != nil {
			t.Fatalf("şema geçersiz JSON: %v", err)
		}
		if parsed["additionalProperties"] != false {
			t.Error("şema bilinmeyen argümanı reddetmiyor")
		}
	}
}

// The description names the ceiling, which is what SD-7 asks of every tool.
func TestCatalogTools_DescriptionsNameTheirCeiling(t *testing.T) {
	cfg := config.Load()
	for _, d := range []string{
		tools.NewCatalogProducts(cfg, &fakeCatalogReader{}).Description(),
		tools.NewCatalogProduct(cfg, &fakeCatalogReader{}).Description(),
	} {
		if !strings.Contains(d, "tokens") {
			t.Errorf("açıklama tavanını söylemiyor: %s", d)
		}
	}
}
