package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/catalog"
)

// A decision about the Arabic copy is not a decision about the Turkish one.
// The source language stays in catalog_products.status and a target language
// gets a row of its own, which is the same argument DraftVersion makes about
// the draft key: the language is a suffix, and only for a target.
func TestSetCatalogProductStatus_ATargetLanguageDecisionDoesNotMoveTheSourceOne(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	seedImport(t, s, "imp_1", product("prd_a", "krem"))

	if err := s.SetCatalogProductStatus(ctx, "prd_a", catalog.LangAR,
		catalog.StatusApproved, "", time.Now()); err != nil {
		t.Fatalf("SetCatalogProductStatus: %v", err)
	}

	src, ok, err := s.GetCatalogProduct(ctx, "prd_a", catalog.LangSource)
	if err != nil || !ok {
		t.Fatalf("GetCatalogProduct: %v ok=%v", err, ok)
	}
	if src.Status != catalog.StatusPending {
		t.Errorf("Arapça kararı kaynak dilin durumunu değiştirdi: %q", src.Status)
	}

	ar, _, err := s.GetCatalogProduct(ctx, "prd_a", catalog.LangAR)
	if err != nil {
		t.Fatalf("GetCatalogProduct(ar): %v", err)
	}
	if ar.Status != catalog.StatusApproved {
		t.Errorf("Arapça kararı okunmadı: %q", ar.Status)
	}
	if ar.SourceStatus != catalog.StatusPending {
		t.Errorf("kaynak dilin kararı satıra gelmedi: %q", ar.SourceStatus)
	}
}

// The guard is structural, not trusted to a caller. A plain upsert would always
// affect a row — losing the sql.ErrNoRows the studio turns into
// ErrUnknownProduct, and leaving a decision about a product that is not there.
func TestSetCatalogProductStatus_RefusesADecisionAboutAProductThatIsNotThere(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	err := s.SetCatalogProductStatus(ctx, "prd_yok", catalog.LangAR,
		catalog.StatusApproved, "", time.Now())
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("beklenen sql.ErrNoRows, alınan %v", err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM catalog_product_langs`).Scan(&n); err != nil {
		t.Fatalf("sayım: %v", err)
	}
	if n != 0 {
		t.Errorf("olmayan ürün için karar satırı yazıldı: %d", n)
	}
}

// Absence is pending. A product nobody has judged in Arabic has no row, and an
// INNER JOIN would answer "hiç bekleyen yok" for a catalogue nobody has
// touched — the exact opposite of the truth.
func TestListCatalogProducts_AProductWithNoDecisionInALanguageReadsAsPending(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	seedImport(t, s, "imp_1", product("prd_a", "krem"), product("prd_b", "sampuan"))

	rows, err := s.ListCatalogProducts(ctx, catalog.ProductFilter{
		ImportID: "imp_1", Lang: catalog.LangAR, Status: catalog.StatusPending,
	})
	if err != nil {
		t.Fatalf("ListCatalogProducts: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("beklenen iki bekleyen ürün, alınan %d", len(rows))
	}
	for _, r := range rows {
		if r.Lang != catalog.LangAR {
			t.Errorf("satır dilini söylemiyor: %+v", r)
		}
	}
}

// The filter reads the requested language's decision, not the product's own.
func TestListCatalogProducts_FiltersOnTheRequestedLanguagesStatus(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	seedImport(t, s, "imp_1", product("prd_a", "krem"), product("prd_b", "sampuan"))

	if err := s.SetCatalogProductStatus(ctx, "prd_a", catalog.LangAR,
		catalog.StatusApproved, "", time.Now()); err != nil {
		t.Fatalf("SetCatalogProductStatus: %v", err)
	}
	// And the source language of the OTHER product, so a query that read the
	// wrong column would find something and look right.
	if err := s.SetCatalogProductStatus(ctx, "prd_b", catalog.LangSource,
		catalog.StatusApproved, "", time.Now()); err != nil {
		t.Fatalf("SetCatalogProductStatus: %v", err)
	}

	rows, err := s.ListCatalogProducts(ctx, catalog.ProductFilter{
		ImportID: "imp_1", Lang: catalog.LangAR, Status: catalog.StatusApproved,
	})
	if err != nil {
		t.Fatalf("ListCatalogProducts: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "prd_a" {
		t.Fatalf("Arapça onaylı küme yanlış: %+v", rows)
	}
	if rows[0].SourceStatus != catalog.StatusPending {
		t.Errorf("kaynak dilin kararı satıra gelmedi: %q", rows[0].SourceStatus)
	}
}

// The source-language read is the query it has always been, on the column it
// has always used. No migration may change what an existing screen says.
func TestListCatalogProducts_TheSourceLanguageStillReadsTheProductsOwnColumn(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	seedImport(t, s, "imp_1", product("prd_a", "krem"))

	if err := s.SetCatalogProductStatus(ctx, "prd_a", catalog.LangAR,
		catalog.StatusApproved, "", time.Now()); err != nil {
		t.Fatalf("SetCatalogProductStatus: %v", err)
	}
	rows, err := s.ListCatalogProducts(ctx, catalog.ProductFilter{
		ImportID: "imp_1", Status: catalog.StatusApproved,
	})
	if err != nil {
		t.Fatalf("ListCatalogProducts: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("Arapça onayı kaynak dilin süzgecine düştü: %+v", rows)
	}
}

func TestCatalogStatusCounts_KeepsTheSourceLanguageUnderTheEmptyKey(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	seedImport(t, s, "imp_1", product("prd_a", "krem"), product("prd_b", "sampuan"))
	if err := s.SetCatalogProductStatus(ctx, "prd_a", catalog.LangAR,
		catalog.StatusDrafted, "", time.Now()); err != nil {
		t.Fatalf("SetCatalogProductStatus: %v", err)
	}

	counts, err := s.CatalogStatusCounts(ctx)
	if err != nil {
		t.Fatalf("CatalogStatusCounts: %v", err)
	}
	if counts["imp_1"][catalog.LangSource][catalog.StatusPending] != 2 {
		t.Errorf("kaynak dilin sayımı değişti: %+v", counts["imp_1"])
	}
	if counts["imp_1"][catalog.LangAR][catalog.StatusDrafted] != 1 {
		t.Errorf("Arapça sayımı gelmedi: %+v", counts["imp_1"])
	}
}

// A language nobody has decided in is absent, not zero. The list screen does
// not read file bodies, so it cannot know which languages a file carries, and
// synthesising a pending count per language would draw "Arapça · 2 bekliyor"
// over a Shopify export with no Arabic column.
func TestCatalogStatusCounts_LeavesOutALanguageNobodyHasDecidedIn(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	seedImport(t, s, "imp_1", product("prd_a", "krem"))

	counts, err := s.CatalogStatusCounts(ctx)
	if err != nil {
		t.Fatalf("CatalogStatusCounts: %v", err)
	}
	if _, ok := counts["imp_1"][catalog.LangAR]; ok {
		t.Errorf("kimsenin karar vermediği dil sayımda göründü: %+v", counts["imp_1"])
	}
}

// A re-upload of a corrected export addresses the same product ids, so a target
// language's approval survives it for the same reason the source language's
// does. That is what deriving the ids rather than randomising them is for.
func TestPutCatalogProducts_PreservesATargetLanguageDecisionOnReimport(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	seedImport(t, s, "imp_1", product("prd_a", "krem"))
	if err := s.SetCatalogProductStatus(ctx, "prd_a", catalog.LangAR,
		catalog.StatusApproved, "", time.Now()); err != nil {
		t.Fatalf("SetCatalogProductStatus: %v", err)
	}

	if err := s.PutCatalogProducts(ctx, "imp_1", []catalog.StoredProduct{
		product("prd_a", "krem"),
	}); err != nil {
		t.Fatalf("PutCatalogProducts: %v", err)
	}

	got, _, err := s.GetCatalogProduct(ctx, "prd_a", catalog.LangAR)
	if err != nil {
		t.Fatalf("GetCatalogProduct: %v", err)
	}
	if got.Status != catalog.StatusApproved {
		t.Errorf("yeniden yükleme Arapça onayını sildi: %q", got.Status)
	}
}

// --- the cross-import outputs listing ---------------------------------------

func seedDraft(t *testing.T, s *Store, productID, version, title string, at time.Time) {
	t.Helper()
	var c catalog.Content
	c.Title = title
	if err := s.PutCatalogDraft(context.Background(), catalog.StoredDraft{
		ProductID: productID, Version: version, Content: c,
		Fields: []string{"title"}, CreatedAt: at,
	}); err != nil {
		t.Fatalf("PutCatalogDraft: %v", err)
	}
}

// The listing resolves a version per import per language, and the key is the
// PAIR. One version can be current for one import and superseded for another —
// two imports share a brand hash until one of their voices is edited — so a
// filter written as `version IN (…)` would surface the stale one as current.
func TestListCatalogDrafts_ConstrainsTheImportAndTheVersionTogether(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	seedImport(t, s, "imp_1", product("prd_a", "krem"))
	seedImport(t, s, "imp_2", product("prd_b", "sampuan"))
	now := time.Now()
	seedDraft(t, s, "prd_a", "content-v1:brand-a", "A yeni", now)
	seedDraft(t, s, "prd_b", "content-v1:brand-a", "B eski", now)

	// imp_2's voice has been edited since: its current version is brand-b, and
	// the brand-a draft it still holds is stale.
	rows, err := s.ListCatalogDrafts(ctx, catalog.DraftFilter{Keys: []catalog.DraftKey{
		{ImportID: "imp_1", Version: "content-v1:brand-a", Lang: catalog.LangSource},
		{ImportID: "imp_2", Version: "content-v1:brand-b", Lang: catalog.LangSource},
	}})
	if err != nil {
		t.Fatalf("ListCatalogDrafts: %v", err)
	}
	if len(rows) != 1 || rows[0].ProductID != "prd_a" {
		t.Fatalf("çift kısıtı tutmadı, bayat taslak listeye girdi: %+v", rows)
	}
	if rows[0].Filename != "urunler.csv" || rows[0].Dialect != "ikas" {
		t.Errorf("satır hangi dosyadan geldiğini söylemiyor: %+v", rows[0])
	}
	if rows[0].Content.Title != "A yeni" {
		t.Errorf("taslak içeriği gelmedi: %+v", rows[0].Content)
	}
}

// The row carries the status of the draft's OWN language, so an Arabic row on
// the outputs screen says whether the Arabic has been approved.
func TestListCatalogDrafts_ReadsTheStatusOfTheDraftsOwnLanguage(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	seedImport(t, s, "imp_1", product("prd_a", "krem"))
	seedDraft(t, s, "prd_a", "v1/ar", "عربي", time.Now())
	if err := s.SetCatalogProductStatus(ctx, "prd_a", catalog.LangAR,
		catalog.StatusApproved, "", time.Now()); err != nil {
		t.Fatalf("SetCatalogProductStatus: %v", err)
	}

	rows, err := s.ListCatalogDrafts(ctx, catalog.DraftFilter{Keys: []catalog.DraftKey{
		{ImportID: "imp_1", Version: "v1/ar", Lang: catalog.LangAR},
	}})
	if err != nil {
		t.Fatalf("ListCatalogDrafts: %v", err)
	}
	if len(rows) != 1 || rows[0].Status != catalog.StatusApproved {
		t.Fatalf("Arapça kararı satıra gelmedi: %+v", rows)
	}
	if rows[0].Lang != catalog.LangAR {
		t.Errorf("satır dilini söylemiyor: %q", rows[0].Lang)
	}
}

// The ORDER BY is total. A partial sort key with OFFSET paging repeats rows on
// one page and drops them from the next.
func TestListCatalogDrafts_PagesOnATotalOrder(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	products := []catalog.StoredProduct{
		product("prd_a", "a"), product("prd_b", "b"),
		product("prd_c", "c"), product("prd_d", "d"),
	}
	seedImport(t, s, "imp_1", products...)
	// The same timestamp on every draft, so only the tie-breakers order them.
	at := time.Unix(1_700_000_000, 0)
	for _, p := range products {
		seedDraft(t, s, p.ID, "v1", "yeni "+p.Key, at)
	}
	keys := []catalog.DraftKey{{ImportID: "imp_1", Version: "v1", Lang: catalog.LangSource}}

	seen := map[string]int{}
	for offset := 0; offset < 4; offset += 2 {
		rows, err := s.ListCatalogDrafts(ctx, catalog.DraftFilter{
			Keys: keys, Limit: 2, Offset: offset,
		})
		if err != nil {
			t.Fatalf("ListCatalogDrafts: %v", err)
		}
		// Limit+1 is asked for internally, so a full page comes back long.
		if len(rows) < 2 {
			t.Fatalf("sayfa %d eksik döndü: %d", offset, len(rows))
		}
		for _, r := range rows[:2] {
			seen[r.ProductID]++
		}
	}
	if len(seen) != 4 {
		t.Fatalf("sayfalama satır tekrarladı ya da düşürdü: %+v", seen)
	}
}

// The listing asks for one more row than the page so the caller can say whether
// there is another page without a second COUNT over the same join.
func TestListCatalogDrafts_ReturnsOneMoreThanTheLimit(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	seedImport(t, s, "imp_1", product("prd_a", "a"), product("prd_b", "b"))
	at := time.Now()
	seedDraft(t, s, "prd_a", "v1", "a", at)
	seedDraft(t, s, "prd_b", "v1", "b", at)

	rows, err := s.ListCatalogDrafts(ctx, catalog.DraftFilter{
		Keys:  []catalog.DraftKey{{ImportID: "imp_1", Version: "v1", Lang: catalog.LangSource}},
		Limit: 1,
	})
	if err != nil {
		t.Fatalf("ListCatalogDrafts: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("beklenen limit+1 satır, alınan %d", len(rows))
	}
}

// Everything derived from an import goes with it, decisions included.
func TestDeleteCatalogImport_TakesTheLanguageDecisionsToo(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	seedImport(t, s, "imp_1", product("prd_a", "krem"))
	if err := s.SetCatalogProductStatus(ctx, "prd_a", catalog.LangAR,
		catalog.StatusApproved, "", time.Now()); err != nil {
		t.Fatalf("SetCatalogProductStatus: %v", err)
	}
	if err := s.DeleteCatalogImport(ctx, "imp_1"); err != nil {
		t.Fatalf("DeleteCatalogImport: %v", err)
	}

	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM catalog_product_langs`).Scan(&n); err != nil {
		t.Fatalf("sayım: %v", err)
	}
	if n != 0 {
		t.Errorf("silinen import'un dil kararları kaldı: %d", n)
	}
}

func TestCatalogLangReads_TolerateANilStore(t *testing.T) {
	var s *Store
	ctx := context.Background()
	if _, _, err := s.GetCatalogProduct(ctx, "prd_a", catalog.LangAR); err != nil {
		t.Errorf("GetCatalogProduct: %v", err)
	}
	if _, err := s.ListCatalogDrafts(ctx, catalog.DraftFilter{}); err != nil {
		t.Errorf("ListCatalogDrafts: %v", err)
	}
	if err := s.SetCatalogProductStatus(ctx, "prd_a", catalog.LangAR,
		catalog.StatusApproved, "", time.Now()); err != nil {
		t.Errorf("SetCatalogProductStatus: %v", err)
	}
}

// SQLite caps a compound SELECT at 500 terms. The key list is imports ×
// languages, so at the 200-import ceiling it is 600 — and the whole outputs
// screen failed with "too many terms in compound SELECT", surfaced to the
// operator as an unavailable store on a database that was perfectly healthy.
func TestListCatalogDrafts_HandlesMoreKeysThanACompoundSelectAllows(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	seedImport(t, s, "imp_1", product("prd_a", "krem"))
	seedDraft(t, s, "prd_a", "v1", "yeni", time.Now())

	// 200 imports × 3 languages, which is what config.CatalogOutputsImportMax
	// composes on a busy store. The ids are distinct, because that is the shape
	// Studio.Outputs produces — repeating one id 200 times would put duplicate
	// rows in the CTE and return the same draft once per copy, which would be a
	// test passing for the wrong reason.
	keys := make([]catalog.DraftKey, 0, 600)
	for i := 0; i < 200; i++ {
		id := fmt.Sprintf("imp_%d", i+1)
		for _, l := range catalog.Langs() {
			// A version per language, because that is what DraftVersion
			// composes: the language is a suffix, and the source language has
			// none. Handing every language the same version would ask three
			// questions the studio never asks and match one draft three times.
			version := "v1"
			if l != catalog.LangSource {
				version += "/" + string(l)
			}
			keys = append(keys, catalog.DraftKey{ImportID: id, Version: version, Lang: l})
		}
	}
	rows, err := s.ListCatalogDrafts(ctx, catalog.DraftFilter{Keys: keys, Limit: 10})
	if err != nil {
		t.Fatalf("ListCatalogDrafts: %v", err)
	}
	// Exactly one: the single draft belongs to imp_1 in the source language, and
	// the 599 keys that name another import or another language must not match
	// it. A count here rather than a "not empty" is what makes the key table's
	// pair constraint part of this test too.
	if len(rows) != 1 {
		t.Fatalf("beklenen tek satır, alınan %d", len(rows))
	}
}

// The product table needs every language's decision in one answer, so this read
// is one query over the decision table for a whole page of products.
func TestCatalogProductLangStatuses_ReadsEveryLanguageForAPageOfProducts(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	seedImport(t, s, "imp_1", product("prd_a", "krem"), product("prd_b", "losyon"))

	set := func(id string, lang catalog.Lang, status string) {
		t.Helper()
		if err := s.SetCatalogProductStatus(ctx, id, lang, status, "", time.Now()); err != nil {
			t.Fatalf("SetCatalogProductStatus(%s,%s): %v", id, lang, err)
		}
	}
	set("prd_a", catalog.LangAR, catalog.StatusApproved)
	set("prd_a", catalog.LangEN, catalog.StatusDrafted)
	set("prd_b", catalog.LangAR, catalog.StatusRejected)

	got, err := s.CatalogProductLangStatuses(ctx, []string{"prd_a", "prd_b"})
	if err != nil {
		t.Fatalf("CatalogProductLangStatuses: %v", err)
	}
	if got["prd_a"][catalog.LangAR] != catalog.StatusApproved {
		t.Errorf("prd_a Arapça %q", got["prd_a"][catalog.LangAR])
	}
	if got["prd_a"][catalog.LangEN] != catalog.StatusDrafted {
		t.Errorf("prd_a İngilizce %q", got["prd_a"][catalog.LangEN])
	}
	if got["prd_b"][catalog.LangAR] != catalog.StatusRejected {
		t.Errorf("prd_b Arapça %q", got["prd_b"][catalog.LangAR])
	}
}

// The source language does not live in this table, and this read does not
// invent it. Composing it belongs to the one caller that knows where it lives.
func TestCatalogProductLangStatuses_DoesNotReportTheSourceLanguage(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	seedImport(t, s, "imp_1", product("prd_a", "krem"))

	if err := s.SetCatalogProductStatus(ctx, "prd_a", catalog.LangSource,
		catalog.StatusApproved, "", time.Now()); err != nil {
		t.Fatalf("SetCatalogProductStatus: %v", err)
	}

	got, err := s.CatalogProductLangStatuses(ctx, []string{"prd_a"})
	if err != nil {
		t.Fatalf("CatalogProductLangStatuses: %v", err)
	}
	if _, ok := got["prd_a"][catalog.LangSource]; ok {
		t.Error("kaynak dil karar tablosundan okundu")
	}
}

// A product nobody has judged in any target language has no entry at all, and
// the caller reads that as pending. A nil map here is the same answer.
func TestCatalogProductLangStatuses_AnUndecidedProductHasNoEntry(t *testing.T) {
	s := openTestStore(t)
	seedImport(t, s, "imp_1", product("prd_a", "krem"))

	got, err := s.CatalogProductLangStatuses(context.Background(), []string{"prd_a"})
	if err != nil {
		t.Fatalf("CatalogProductLangStatuses: %v", err)
	}
	if len(got["prd_a"]) != 0 {
		t.Errorf("karar verilmemiş ürün için kayıt üretildi: %v", got["prd_a"])
	}
}

// No ids is no query and no rows, not every decision in the database.
func TestCatalogProductLangStatuses_NoIdsReadsNothing(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	seedImport(t, s, "imp_1", product("prd_a", "krem"))
	if err := s.SetCatalogProductStatus(ctx, "prd_a", catalog.LangAR,
		catalog.StatusApproved, "", time.Now()); err != nil {
		t.Fatalf("SetCatalogProductStatus: %v", err)
	}

	got, err := s.CatalogProductLangStatuses(ctx, nil)
	if err != nil {
		t.Fatalf("CatalogProductLangStatuses: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("id verilmeden %d kayıt döndü", len(got))
	}
}
