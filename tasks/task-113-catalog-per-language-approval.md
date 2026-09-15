# task-113 — Katalog: dil başına onay, her dilin dışa aktarımı, ve operatör yolundaki üç kırık

- **Status:** done
- **Owner agent:** Coder
- **Prerequisites:** task-103, task-105, task-107, task-111
- **Primary paths:** `internal/store/**`, `internal/catalog/**`, `internal/api/catalog.go`
- **Roadmap bucket:** Katalog — ürün içeriği stüdyosu

## Context

Motor task-105'ten beri hedef dilde **üretiyor**, task-107'den beri dil kapısından
geçiriyor, ama üretilen Arapça hiçbir zaman dosyaya yazılamadı. `Studio.Export`
tek bir `DraftVersion(..., LangSource)` çözüyordu ve `approved`'ı yalnızca
kaynak dille dolduruyordu. Kodun kendi yorumu sebebi yazıyordu: onay
`catalog_products.status` tek sütununda duruyor, yani dil başına tutulamıyor, ve
Türkçe kopyayı onaylamak kimsenin okumadığı bir Arapça kopyayı yayına
göndermemeli. Alt seviyedeki `Export()` task-103'ten beri her dil üzerinde genel
ve öyle test ediliyor — eksik olan yalnızca stüdyo teliydi.

Aynı kazıda operatör yolunda üç sessiz kırık çıktı:

1. `stripDirectionWrapper`'ın (`render.go:381`) üretimde hiçbir çağıranı yoktu.
   `Studio.sanitize` `Render` çağırıyordu, `RenderLang` değil, ve hiç
   sıyırmıyordu — operatör onaylanmış bir Arapça taslağı elle düzenleyip
   kaydettiğinde `<div dir="rtl" lang="ar">` zarfa ayrışıyor, `sanitizeAttrs`
   `dir`'i düşürüyor ve yön hiçbir şey söylemeden kayboluyordu.
2. `CheckLanguage` / `NormalizeForLang` yalnızca `rewrite.go`'dan çağrılıyordu.
   `rewrite.go:474`'ün kendi yorumu "kapı bu yolda **ve** operatörün yolunda
   çalışır, yani kapıyı atlamış bir taslağı saklamanın yolu yoktur" diyordu.
3. `rewriteOne` dil bilmeden iki kez durum yazıyordu (`rewrite.go:290`, `:307`).
   Arapça geçişte kapıya takılan bir ürün Türkçe ürünü `failed` işaretliyor,
   `reason`'ını başka bir dil hakkındaki şikâyetle eziyor ve operatörün
   `approved` süzgecinden çıkarıyordu. İlki bir öneriyi değil, verilmiş bir
   **kararı** yok ediyor.

## Scope (do exactly this)

1. `0026_catalog_langs.sql`: `catalog_product_langs(product_id, lang, status,
   reason, created_at, updated_at)`, `PRIMARY KEY (product_id, lang)`,
   `CHECK (lang <> '')`. Kaynak dil bu tabloda **değil**. `idx_catalog_drafts_version`.
2. Depo: `GetCatalogProduct(…, lang)`, `SetCatalogProductStatus(…, lang, …)`,
   `CatalogStatusCounts` → `map[import]map[Lang]map[status]int`,
   `ListCatalogProducts` `f.Lang`'e göre `LEFT JOIN` + `COALESCE`,
   `DeleteCatalogImport` kararları da siliyor. Dil dalı **yalnızca burada**.
3. `catalog`: `ProductFilter.Lang`, `StoredProduct.{Lang,SourceStatus}`,
   `SetStatus(…, lang, …)`, `SaveDraft(…, lang, …)`, `Product(…, lang, …)`,
   `sanitize(…, lang, …)`.
4. `sanitize` hedef dilde: kaynak belge `p.Content(lang)`,
   `stripDirectionWrapper` + `RenderLang`, `NormalizeForLang` + `CheckLanguage`
   — bulgular **not**, asla ret.
5. `rewriteOne` her iki durum yazımında `req.Lang`.
6. `Studio.Export` `imp.File.Langs()` üzerinde döner; dil başına bir sürüm, bir
   ürün sorgusu, bir taslak sorgusu.
7. API: `SetStatus`/`SaveDraft`/`Product`/`Products` `?lang=` taşır;
   `catalogImportRow.counts_by_lang`, `counts` ondan türetilir;
   `catalogImportResponse` `pending_target` + `target_lang` + `writable_languages`.

## Out of scope (do NOT do here)

- Dil başına araştırma. `researchVersion` dil taşımıyor ve taşımamalı: pazar
  hakkında öğrenilen şey hedef dile göre değişmiyor.
- `StatusResearched`'ı canlandırmak. Motor onu hiç yazmıyor ve dil başına bir
  "araştırıldı" kategori hatası olurdu.

## Definition of Done

- [x] `TestExport_EveryRealExportShapeComesBackByteForByte` yedi gerçek fixture'da yeşil
- [x] Onaylanan Arapça hücre yazılıyor; onaylanmayan yazılmıyor
- [x] Arapça onayı Türkçe durumunu kıpırdatmıyor, tersi de
- [x] Karar verilmemiş dil `pending` okunuyor (yokluk = bekliyor)
- [x] Arapça düzenleme `dir="rtl"` sarmalayıcısını koruyor ve ikinci kat eklemiyor
- [x] Operatörün Arapçası kapının bulgularıyla birlikte **saklanıyor**
- [x] Kaynak dil okuması bayt bayt eski şeklinde
- [x] Yeniden yüklemede hedef dil onayı korunuyor
- [x] `make check` yeşil

## Notes for the reviewer (Opus)

- Kaynak dil neden ayrı tabloda değil: taslak anahtarının kendi gerekçesi. Her
  mevcut satır kaynak dil hakkında bir karar; buraya taşımak ya backfill ya da
  liste ekranının tek `GROUP BY`'ının yeniden yazımı demekti. Karşılığında
  kaynak dil sorgusu ve indeksi kıpırdamadı.
- `CatalogStatusCounts` neden dil başına `CROSS JOIN` yapmıyor: `ListCatalogImports`
  `file_json`'ı bilerek okumuyor, yani liste hangi dosyanın hangi dili
  taşıdığını bilemez. Eksik dilleri sentezlemek, Arapça sütunu olmayan bir
  Shopify export'unun üstüne "Arapça · 40 bekliyor" çizmek olurdu.
- `SetCatalogProductStatus` neden `INSERT … SELECT`: düz upsert her zaman bir
  satır etkiler, `ErrUnknownProduct` yolunu kaybederdik ve olmayan bir ürün
  hakkında öksüz karar satırı kalırdı.
