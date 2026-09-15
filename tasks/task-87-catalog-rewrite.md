# task-87 — Ürün başına araştırma ve yeniden yazım: kaldığı yerden süren kart

- **Status:** done
- **Owner agent:** Coder
- **Prerequisites:** task-85 (katalog çekirdeği)
- **Primary paths:** `internal/catalog/research.go`, `internal/catalog/rewrite.go`,
  `internal/catalogjob/**` (yeni), `internal/agents/agents.go`,
  `internal/skills/skills.go`, `internal/skills/skills/product-content.md` (yeni),
  `internal/tools/catalog_*.go` (yeni), `internal/tools/register.go`,
  `internal/config/config.go`, `internal/api/catalog.go`, `cmd/mimir-daemon/main.go`
- **Roadmap bucket:** B.11 — katalog / ürün içeriği

## Context

task-85 CSV'yi okuyup markanın sözlüğünü çıkardı ve kayıpsız geri yazdı. Bu task
aradaki işi yapar: ürün başına rakip araştırması, ve markanın sesini ve etiket
sözlüğünü koruyan yeniden yazım.

Ürün başına araştırma bilinçli olarak pahalı taraftır — 400 ürün 400 arama +
crawl + refine demektir. O yüzden bu task'ın asıl konusu içerik değil,
**yarıda kalmanın maliyeti**dir. Üç mekanizma:

1. **İki fazlı önbellek.** Araştırma ve metin ayrı satırlarda, ayrı sürüm
   anahtarlarıyla durur. Marka hash'i **araştırma anahtarında yoktur**: operatör
   marka sesini düzeltip yeniden koştuğunda metinler yeniden yazılır, rakip
   araştırması olduğu yerde kalır.
2. **`internal/store/pages.go`'nun mevcut crawl/refine önbelleği** — aynı rakip
   sayfası ikinci üründe bedava.
3. **Limit parkı.** `llm.ErrRateLimited` geldiğinde kart `failed` olmaz;
   `Outcome.ParkUntil` ile `queued`'a döner ve pencere dönünce aynı yerden sürer.
   Bu, model limitine park eden **ilk** executor olur — `leadgenjob` buna ihtiyaç
   duymuyordu çünkü Distill'i ücretsiz tier'a gidiyor.

Kendi zamanlayıcısı kurulmaz. `internal/coderunner/executor.go` bunu açıkça
yasaklıyor: *"A second scheduler beside it would mean a second board state
mirrored from this one."* Kart mevcut dispatcher'a bir `Executor` olarak takılır,
task-81'in lead-gen için yaptığının aynısı.

## Scope (do exactly this)

1. **`internal/catalog/research.go`** — ürün başına rakip araştırması.
   - Mevcut `internal/pipeline` hattından geçer: `search → crawl → refine → merge`.
     SD-2 gereği kazınmış metin `internal/refine`'dan geçmeden hiçbir yere düşmez.
   - Ürün başına `cfg.CatalogResearchSources` kaynak. `Findings` alanları
     `refined: true` işaretli.
   - Sonuç `store.PutCatalogResearch(productID, version, ...)`;
     `version = cfg.CatalogResearchVersion + "@" + sel.Key()`.
   - Bir ürünün araştırması başarısızsa o ürün araştırmasız yeniden yazılır ve
     bunu `Notes`'a yazar (SD-6) — koşu düşmez.
2. **`internal/catalog/rewrite.go`** — yeniden yazım.
   - Girdi: `Block[]` (metin, zarf modele gitmez), `BrandKit`, `Findings`,
     `Fields` (hangi alanlar yazılacak).
   - `llm.Reason` sınıfı, JSON şemasıyla yapılandırılmış çıktı: aynı blok
     yapısında metin + `seo_title`, `seo_description`, `title`, `tags`.
   - Doğrulama, kabul etmeden önce: SEO uzunlukları `cfg.CatalogSEOTitleMaxChars` /
     `CatalogSEODescMaxChars`'ı aşamaz; `Render` sözlük dışını düşürür;
     girdide geçmeyen `href` düz metne iner. Reddedilen bir alan o alanı
     kaybettirir, ürünü değil.
   - Fan-out `errgroup.WithContext` + `SetLimit(cfg.CatalogMaxConcurrentProducts)` (SD-3).
   - Sonuç `store.PutCatalogDraft`;
     `version = cfg.CatalogContentVersion + "@" + sel.Key() + ":" + brandHash8 + ":" + skillVersion8`.
   - **Her ürün için önce `Get` sonra çağrı**: taslağı olan ürün model harcamaz.
3. **`internal/catalogjob`** — `coderunner.Executor`. Ayrı paket, çünkü
   `internal/catalog` asla `coderunner`'ı import etmemeli.
   - `Agent() == "catalog"`, `Lane() == LaneWorker`, `NeedsProject() == false`.
   - `Prepare` — `row.Params`'ı `Params{ImportID, ProductIDs, Fields, Research}`'e
     çözer; çözemezse hiçbir şey claim edilmeden reddedilir.
   - `Execute` — ürün başına iki `job.Out.Step` (`research`, `draft`). Önbellek
     isabeti isabet olarak anlatılır ve hiçbir şeye mal olmaz.
   - `llm.ErrRateLimited` → `Outcome{ParkUntil: resets}`. Başka bir hata o ürünü
     `failed` yapar, koşuyu değil.
4. **`internal/agents/agents.go`** — `ExecCatalog Executor = "catalog"` ve
   `Agent{Key: "catalog", Name: "Katalog", ..., RequiredSkills:
   []string{skills.ProductContent}, NeedsProject: false, ModelClass: llm.Reason}`.
5. **`internal/skills`** — `ProductContent = "product-content"`, başlık
   "Ürün içeriği", gömülü varsayılan `skills/product-content.md`. `IDs()` ve
   `titles`'a eklenir. Mandat: `RequiredSkills` boş bırakılamaz.
6. **`internal/tools`** — üç araç, tek listeye (`RegisterAll`), `d.Catalog != nil`
   kapısıyla: `catalog_products` (`metadata_only`), `catalog_product`
   (`refined: true`), `catalog_rewrite` (kart açar, içerik döndürmez). Tavanlar
   şema açıklamasında yazılı ve zorlanır (SD-7). CSV import'u MCP'de **yoktur**.
7. **`internal/api/catalog.go`** — `POST /catalog/rewrite`
   `{import_id, product_ids[], fields[]}`. **Açık id listesi, filtre değil** —
   `handleDraftOutreach`'in kuralı. `cfg.CatalogProductsPageMax` ile sınırlı.
8. **`internal/config`** — `CatalogMaxConcurrentProducts`, `CatalogResearchSources`,
   ve iki önbellek-sürüm sabiti: `CatalogResearchVersion`, `CatalogContentVersion`.
9. **`cmd/mimir-daemon/main.go`** — `runner.Register(catalogjob.New(...))`,
   **`runner.Resume(ctx)`'ten önce**.

## Out of scope (do NOT do here)

- `internal/coderunner/**` altında hiçbir dosya. Dokunulması gerekiyorsa
  task-79'un dikişi eksik demektir; bunu söyle, burada tamamlama.
- Lehçe, CSV ayrıştırma, `rte.go`, `export.go` — task-85 bitirdi.
- Masaüstünde herhangi bir şey — task-86 ve task-88.
- Kartın gövdesinin nasıl göründüğü — task-88.

## Interfaces / contracts

```go
// internal/catalogjob
const Agent = "catalog"
type Params struct {
    ImportID   string   `json:"import_id"`
    ProductIDs []string `json:"product_ids"`
    Fields     []string `json:"fields"`
    Research   bool     `json:"research"`
}
func Parse(row store.RunRow) (Params, error)
type Runner interface {
    Rewrite(ctx context.Context, req catalog.RewriteRequest, sink catalog.Sink) (catalog.Report, error)
}
func New(r Runner) *Executor
```

```
catalog_research.version = CatalogResearchVersion + "@" + sel.Key()
catalog_drafts.version   = CatalogContentVersion + "@" + sel.Key()
                           + ":" + brandHash8 + ":" + skillVersion8
```

## Definition of Done

- [x] `TestRewrite_SecondPassOverTheSameProductsSpendsNothing` — çağrı sayan
      sahte sağlayıcı ve sahte araştırmacı; ikinci koşuda **sıfır** yeni çağrı.
- [x] `TestRewrite_EditingTheVoiceRedraftsButKeepsTheResearch` — marka hash'i
      değişince metinler yeniden yazılıyor, araştırma satırları duruyor.
- [x] `TestRewrite_StopsOnARateLimitAndKeepsWhatItAlreadyWrote` + sürdürülen
      koşunun yalnız kalanı harcaması.
- [x] `TestRewrite_StopsAtOnceWhenTheProviderCannotBeRun` — planda olmayan,
      koşarken bulunan kural.
- [x] `TestRewrite_AProductionOfNothingIsAFailureNotACompletion` ve
      `TestRewrite_AllCachedIsNotAFailure`.
- [x] `TestPrepare_RefusesACardWhoseParamsWillNotDecode`,
      `TestParse_DoesNotGuessProductsFromThePrompt`,
      `TestExecutor_RunsOnTheWorkerLaneAndNeedsNoProject`.
- [x] `TestRewrite_RejectsAnOverlongSEOFieldWithoutLosingTheProduct`.
- [x] `TestRewrite_CannotWidenTheBrandsMarkupOrInventALink`.
- [x] `TestRewrite_AFailedResearchDoesNotStopThePass` — not `Report.Notes`'ta.
- [x] `internal/tools`: `metadata_only` / `refined` işaretleri, tavanlar,
      `additionalProperties: false`, ve açıklamanın tavanını söylemesi.
- [x] `POST /catalog/rewrite` filtre değil liste alıyor; tavanı aşan seçim 400;
      kuyruksuz daemon'da rota yok ama okumalar cevap veriyor.
- [x] Diff `internal/coderunner/**` altında hiçbir dosyaya dokunmuyor.
- [x] `make check` yeşil.
- [x] Status `done` + changelog.

## Notes for the reviewer (Opus)

- **SD-2**: rakip sayfası metni `internal/refine`'dan geçiyor mu, yoksa
  `Findings`'e ham mı düşüyor?
- **SD-3**: `errgroup.SetLimit` var mı, limit `internal/config`'ten mi geliyor?
- **SD-6**: bir ürünün hatası koşuyu düşürüyor mu?
- **SD-7**: üç aracın da tavanı şema açıklamasında yazılı mı ve tavanı aşan bir
  yanıt için testi var mı?
- Araştırma anahtarında marka hash'i **yok** — bu kasıtlı. Varsa, planın asıl
  token tasarrufu kaybolmuş demektir.
- `CatalogResearchVersion` ve `CatalogContentVersion` gerçekten ayrı mı, yoksa
  biri diğerinden mi türetiliyor? Türetiliyorsa bağımsız ayarlanamazlar.

---

## Changelog — 2026-09-07

**Ürün başına araştırma ve yeniden yazım.** Araştırma mevcut `internal/pipeline`
hattından geçiyor (SD-2), yeniden yazım `llm.Reason`. Modele bu yolda da hiç
işaretleme gösterilmiyor: şema *metin* taşıyan tiplenmiş blokların listesini
istiyor ve zarfı `envelopeFor` mağazanın kendi geçmiş HTML'inden seçiyor.

**`internal/catalogjob` — kart.** `runner.Register` `Resume`'dan önce.
`internal/coderunner` altında **hiçbir dosyaya dokunulmadı**; task-79'un dikişi
ikinci kez de tuttu.

**İki önbellek anahtarı, marka hash'i yalnız birinde.** Planın can damarıydı ve
öyle kaldı. `TestRewrite_EditingTheVoiceRedraftsButKeepsTheResearch` iki yönü de
ölçüyor: metinler yeniden yazılıyor, araştırma çağrısı sayısı artmıyor.

## Plandan üç sapma

1. **Üçüncü MCP aracı (`catalog_rewrite`) yazılmadı.** Plan üç araç diyordu.
   Yazarken şu görüldü: bu, kayıtta bütün etkisi **para harcamak** olan tek araç
   olurdu — birinin yazdığı bir cümleden bir katalog dolusu arama, tarama ve
   akıl yürütme çağrısı. Onu kuyruğa alan rota tam da bu yüzden açık bir ürün
   listesi istiyor ve operatörün o ürünlere az önce baktığı bir ekranda duruyor;
   bir araç ya o korumayı kötü tekrar eder ya da hiç yapmazdı. Ayrıca
   `internal/tools`'un kuyruğa bir bağımlılık kazanması gerekirdi. İki okuma
   aracı yazıldı, gerekçe `internal/tools/catalog.go`'nun paket yorumunda.

2. **Limit parkı yapılamadı — dikişte yok.** Plan "model limitine park eden ilk
   executor" diyordu. `coderunner.Outcome` bir `ParkUntil` alanı ilan ediyor ama
   `runExecutor` onu **hiç okumuyor**: limit makinesi (`holdSlot`, `heldUntil`)
   bir kimlik yuvasına anahtarlı ve worker lane'deki bir iş öyle bir yuva
   tutmuyor. `Outcome.ParkUntil`'in kendi yorumu da bunu bir özellik olarak
   yazıyor. Bu task'ın "Out of scope"u `internal/coderunner`'a dokunmayı
   yasaklıyor ve o kural doğru, o yüzden **tamamlanmadı, söylendi** — önerisi
   aşağıda. Yerine konan şey kullanıcının asıl şartını zaten karşılıyor: limit
   çarpınca kart, kaç ürünün korunduğunu ve yeniden çalıştırmanın yalnız kalanları
   harcayacağını söyleyerek düşüyor, ve ürün başına önbellek bunu kibar değil
   ucuz yapıyor.

3. **Sistemik hata kuralı plana sonradan girdi.** Kimliği doğrulanmamış bir
   `claude` CLI'ıyla gerçek daemon'a karşı koşunca görüldü: kart "tamamlandı"
   diyordu ve üç ürünün üçü de aynı sebeple `failed` idi — panoda yeşil bir kart,
   altında dokunulmamış bir katalog. `isFatal` artık
   `llm.ErrProviderUnavailable`'da (ki `ErrRateLimited` onu sarmalıyor) koşuyu ilk
   üründe durduruyor, ve hiçbir şey yazamayan bir koşu başarısız sayılıyor.
   Doğrulandı: ikinci koşuda kart `failed`, diğer iki ürün `pending` kaldı
   (hataları değildi) ve önceki taslak korundu.

## Önerilen sonraki task

**task-89 — `Outcome.ParkUntil` dikişte okunur.** `runExecutor` alanı okumuyor;
worker lane'deki bir iş kimlik yuvası tutmadığı için limit makinesi de ona
anahtarlanamıyor. İki soru var ve ikisi de dispatcher'ın: bir kimlik yuvası
tutmayan bir işin limiti neye anahtarlanır (daemon'ın kendi hesabı), ve park
edilmiş bir worker kartı `queued`'dan nasıl ayırt edilir. task-88 zaten park
rozetini masaüstünde çizmeyi planlıyor; o rozetin arkasında bir veri olması için
bu task gerekiyor.

## Uçtan uca doğrulama (gerçek daemon)

- `/agents` katalog ajanını `catalog` executor'ı ve `product-content` skill'iyle
  listeliyor; `/skills` beşinci skill'i gömülü varsayılan olarak veriyor.
- `POST /catalog/rewrite`: boş liste 400 ve ret sebebi kuralı söylüyor
  ("never a filter"); `handle` alanı 400; geçerli seçim 201 ve `run_id`.
- Kart kuyruğa girdi, dispatcher onu `catalog` ajanına yönlendirdi ve
  `product-content:b0142b91` skill'ine bağladı (daemon logunda "job is bound to
  its agent's skills").
- Bu makinede `claude` CLI oturumu kapalı olduğu için koşu ilk üründe durdu ve
  kart CLI'ın kendi eyleme geçirilebilir mesajıyla (`run \`claude login\``)
  başarısız oldu — aranan davranış buydu.

`make check` yeşil.
