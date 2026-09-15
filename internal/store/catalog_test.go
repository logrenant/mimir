package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/catalog"
)

func seedImport(t *testing.T, s *Store, id string, products ...catalog.StoredProduct) {
	t.Helper()
	ctx := context.Background()
	imp := catalog.StoredImport{
		ID: id, Filename: "urunler.csv", Dialect: "ikas",
		ProductCount: len(products), CreatedAt: time.Now(),
		FileJSON: []byte(`{"Header":["a"]}`), BrandJSON: []byte(`{"version":"brand-v1:abcd"}`),
	}
	if err := s.PutCatalogImport(ctx, imp); err != nil {
		t.Fatalf("PutCatalogImport: %v", err)
	}
	if err := s.PutCatalogProducts(ctx, id, products); err != nil {
		t.Fatalf("PutCatalogProducts: %v", err)
	}
}

func product(id, key string) catalog.StoredProduct {
	p := catalog.Product{ID: id, Key: key, Category: "Cilt Bakımı"}
	p.Original.Title = key
	return catalog.StoredProduct{Product: p, Status: catalog.StatusPending}
}

func TestCatalogImport_RoundTrips(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	seedImport(t, s, "imp_1", product("prd_a", "krem"), product("prd_b", "sampuan"))

	got, ok, err := s.GetCatalogImport(ctx, "imp_1")
	if err != nil || !ok {
		t.Fatalf("GetCatalogImport: %v ok=%v", err, ok)
	}
	if got.Filename != "urunler.csv" || got.Dialect != "ikas" || got.ProductCount != 2 {
		t.Errorf("import bozuk döndü: %+v", got)
	}
	if string(got.FileJSON) == "" {
		t.Error("dosya gövdesi kaybolmuş — kayıpsız dışa aktarım imkânsız hâle gelir")
	}

	list, err := s.ListCatalogImports(ctx, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListCatalogImports: %v %d", err, len(list))
	}
	// A listing is a menu; carrying every file body would make opening the
	// screen cost as much as opening every import at once.
	if len(list[0].FileJSON) != 0 {
		t.Error("liste dosya gövdesini de taşıyor")
	}
}

func TestCatalogImport_MissIsNotAnError(t *testing.T) {
	_, ok, err := openTestStore(t).GetCatalogImport(context.Background(), "yok")
	if err != nil || ok {
		t.Fatalf("olmayan import hata verdi: %v ok=%v", err, ok)
	}
}

// A re-upload of a corrected export must not throw away the approvals the
// operator already gave. The ids are derived rather than random for exactly
// this, and the upsert has to honour it.
func TestPutCatalogProducts_PreservesStatusOnReimport(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	seedImport(t, s, "imp_1", product("prd_a", "krem"))

	if err := s.SetCatalogProductStatus(ctx, "prd_a", catalog.LangSource, catalog.StatusApproved, "", time.Now()); err != nil {
		t.Fatalf("SetCatalogProductStatus: %v", err)
	}
	// The same file again.
	if err := s.PutCatalogProducts(ctx, "imp_1", []catalog.StoredProduct{product("prd_a", "krem")}); err != nil {
		t.Fatalf("yeniden import: %v", err)
	}

	got, ok, err := s.GetCatalogProduct(ctx, "prd_a", catalog.LangSource)
	if err != nil || !ok {
		t.Fatalf("GetCatalogProduct: %v ok=%v", err, ok)
	}
	if got.Status != catalog.StatusApproved {
		t.Errorf("yeniden import onayı sıfırladı: %q", got.Status)
	}
}

func TestListCatalogProducts_FiltersAndKeepsFileOrder(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	seedImport(t, s, "imp_1",
		product("prd_a", "bir"), product("prd_b", "iki"), product("prd_c", "uc"))

	if err := s.SetCatalogProductStatus(ctx, "prd_b", catalog.LangSource, catalog.StatusApproved, "", time.Now()); err != nil {
		t.Fatalf("status: %v", err)
	}

	all, err := s.ListCatalogProducts(ctx, catalog.ProductFilter{ImportID: "imp_1"})
	if err != nil || len(all) != 3 {
		t.Fatalf("ListCatalogProducts: %v %d", err, len(all))
	}
	if all[0].Key != "bir" || all[2].Key != "uc" {
		t.Errorf("dosya sırası korunmadı: %s %s %s", all[0].Key, all[1].Key, all[2].Key)
	}

	approved, err := s.ListCatalogProducts(ctx, catalog.ProductFilter{
		ImportID: "imp_1", Status: catalog.StatusApproved,
	})
	if err != nil || len(approved) != 1 || approved[0].Key != "iki" {
		t.Fatalf("durum filtresi çalışmadı: %v %+v", err, approved)
	}
}

func TestSetCatalogProductStatus_RefusesAnUnknownStatus(t *testing.T) {
	s := openTestStore(t)
	seedImport(t, s, "imp_1", product("prd_a", "krem"))
	if err := s.SetCatalogProductStatus(context.Background(), "prd_a", catalog.LangSource, "yayinlandi", "", time.Now()); err == nil {
		t.Fatal("kapalı küme dışında bir durum kabul edildi")
	}
}

// The guard that makes a re-run safe. Without it a bulk pass quietly throws
// away an afternoon of hand corrections.
func TestPutCatalogDraft_IsNotOverwrittenAfterTheOperatorEditedIt(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	seedImport(t, s, "imp_1", product("prd_a", "krem"))

	const version = "content-v1@agy/x:brand-v1:ab"

	machine := catalog.StoredDraft{
		ProductID: "prd_a", Version: version,
		Content: catalog.Content{Title: "Makine yazdı"},
	}
	if err := s.PutCatalogDraft(ctx, machine); err != nil {
		t.Fatalf("ilk yazım: %v", err)
	}

	edited := machine
	edited.Content.Title = "Operatör düzeltti"
	edited.EditedByOperator = true
	if err := s.PutCatalogDraft(ctx, edited); err != nil {
		t.Fatalf("operatör yazımı: %v", err)
	}

	// A later pass over the same selection.
	rerun := machine
	rerun.Content.Title = "Makine tekrar yazdı"
	err := s.PutCatalogDraft(ctx, rerun)
	if !errors.Is(err, catalog.ErrDraftLocked) {
		t.Fatalf("beklenen catalog.ErrDraftLocked, alınan %v", err)
	}

	got, ok, err := s.GetCatalogDraft(ctx, "prd_a", version)
	if err != nil || !ok {
		t.Fatalf("GetCatalogDraft: %v ok=%v", err, ok)
	}
	if got.Content.Title != "Operatör düzeltti" {
		t.Errorf("elle düzeltme ezildi: %q", got.Content.Title)
	}
}

// The guard stops a machine, not a person. Without this a second save locked
// the operator out of their own draft and the screen answered with a refusal it
// could not explain.
func TestPutCatalogDraft_LetsTheOperatorEditTheirOwnDraftAgain(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	seedImport(t, s, "imp_1", product("prd_a", "krem"))

	const version = "content-v1@agy/x:brand-v1:ab"
	first := catalog.StoredDraft{
		ProductID: "prd_a", Version: version,
		Content:          catalog.Content{Title: "İlk düzeltme"},
		EditedByOperator: true,
	}
	if err := s.PutCatalogDraft(ctx, first); err != nil {
		t.Fatalf("ilk düzeltme: %v", err)
	}

	second := first
	second.Content.Title = "İkinci düzeltme"
	if err := s.PutCatalogDraft(ctx, second); err != nil {
		t.Fatalf("operatör kendi taslağını ikinci kez kaydedemedi: %v", err)
	}

	got, _, err := s.GetCatalogDraft(ctx, "prd_a", version)
	if err != nil {
		t.Fatalf("GetCatalogDraft: %v", err)
	}
	if got.Content.Title != "İkinci düzeltme" {
		t.Errorf("ikinci düzeltme yazılmadı: %q", got.Content.Title)
	}
}

// The read a bulk run makes before it spends anything: one query for the whole
// selection. A per-product read would put a round trip in front of every cache
// hit and make resuming cost more than the run it resumes.
func TestGetCatalogDrafts_ReadsAWholeSelectionAtOnce(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	seedImport(t, s, "imp_1", product("prd_a", "a"), product("prd_b", "b"), product("prd_c", "c"))

	const version = "content-v1@agy/x:brand-v1:ab"
	for _, id := range []string{"prd_a", "prd_c"} {
		if err := s.PutCatalogDraft(ctx, catalog.StoredDraft{
			ProductID: id, Version: version, Content: catalog.Content{Title: id},
		}); err != nil {
			t.Fatalf("PutCatalogDraft(%s): %v", id, err)
		}
	}

	got, err := s.GetCatalogDrafts(ctx, []string{"prd_a", "prd_b", "prd_c"}, version)
	if err != nil {
		t.Fatalf("GetCatalogDrafts: %v", err)
	}
	if len(got) != 2 || got["prd_a"].Content.Title != "prd_a" {
		t.Errorf("beklenen 2 taslak, alınan %d: %+v", len(got), got)
	}
	if _, present := got["prd_b"]; present {
		t.Error("yazılmamış ürün için taslak döndü")
	}
}

// A different version is a different answer. If it were not, editing the brand
// voice would serve copy written under a voice that no longer exists.
func TestGetCatalogDraft_IsScopedToItsVersion(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	seedImport(t, s, "imp_1", product("prd_a", "a"))

	if err := s.PutCatalogDraft(ctx, catalog.StoredDraft{
		ProductID: "prd_a", Version: "v1", Content: catalog.Content{Title: "eski"},
	}); err != nil {
		t.Fatalf("PutCatalogDraft: %v", err)
	}
	if _, ok, _ := s.GetCatalogDraft(ctx, "prd_a", "v2"); ok {
		t.Error("başka bir sürümün taslağı servis edildi")
	}
}

func TestDeleteCatalogImport_TakesEverythingDerivedFromIt(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	seedImport(t, s, "imp_1", product("prd_a", "a"))
	if err := s.PutCatalogDraft(ctx, catalog.StoredDraft{
		ProductID: "prd_a", Version: "v1", Content: catalog.Content{Title: "x"},
	}); err != nil {
		t.Fatalf("PutCatalogDraft: %v", err)
	}

	if err := s.DeleteCatalogImport(ctx, "imp_1"); err != nil {
		t.Fatalf("DeleteCatalogImport: %v", err)
	}
	if _, ok, _ := s.GetCatalogImport(ctx, "imp_1"); ok {
		t.Error("import silinmedi")
	}
	if _, ok, _ := s.GetCatalogProduct(ctx, "prd_a", catalog.LangSource); ok {
		t.Error("ürün arkada kaldı")
	}
	if _, ok, _ := s.GetCatalogDraft(ctx, "prd_a", "v1"); ok {
		t.Error("taslak arkada kaldı — hiçbir ürünün adresleyemeyeceği bir satır")
	}
}

// The store contract everywhere in this package: a daemon with no database
// reports empty rather than refusing to start (SD-6).
func TestCatalogReads_TolerateANilStore(t *testing.T) {
	var s *Store
	ctx := context.Background()
	if _, ok, err := s.GetCatalogImport(ctx, "x"); err != nil || ok {
		t.Errorf("nil store hata verdi: %v", err)
	}
	if err := s.PutCatalogDraft(ctx, catalog.StoredDraft{ProductID: "a", Version: "v"}); err != nil {
		t.Errorf("nil store yazımı hata verdi: %v", err)
	}
	if got, err := s.ListCatalogProducts(ctx, catalog.ProductFilter{ImportID: "x"}); err != nil || got != nil {
		t.Errorf("nil store listesi: %v %v", err, got)
	}
}
