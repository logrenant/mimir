# task-85 — Katalog çekirdeği: kayıpsız CSV, RTE zarfı, marka kimliği

- **Status:** done
- **Owner agent:** Coder
- **Prerequisites:** none
- **Primary paths:** `internal/catalog/**` (yeni), `internal/store/catalog.go` (yeni),
  `internal/store/migrations/0025_catalog.sql` (yeni), `internal/api/catalog.go` (yeni),
  `internal/api/api.go`, `internal/api/middleware.go`, `internal/config/config.go`,
  `cmd/mimir-daemon/main.go`, `go.mod`
- **Roadmap bucket:** B.11 — katalog / ürün içeriği

## Context

Shopify ve IKAS ürün kataloglarını CSV olarak dışa aktarır. Açıklama alanı düz
metin değil, mağazanın RTE'sinde girilmiş **HTML**'dir ve markanın kendi etiket,
sınıf ve inline-style sözlüğünü taşır. Bu task o CSV'yi okuyan, ondan markanın
sözlüğünü çıkaran ve **kayıpsız** geri yazan çekirdeği kurar.

Bu task'ta ürün içeriği yeniden yazılmaz. Tek model çağrısı marka **ses
profilinin** distil'idir; onun dışında her şey deterministiktir. Ayrım kasıtlı:
yeniden yazımın güvencesi (`Vocabulary`) modelden bağımsız olarak test
edilebilmeli, yoksa "çıktı markanın sözlüğünü genişletemez" iddiası bir umut
olur. `internal/graphify` aynı ayrımı yapıyor ve testini nil bir `Completer` ile
yazıyor — burada da öyle.

Kayıpsızlık bu katmanın asıl sözleşmesidir. Operatör bu CSV'yi kendi admin
paneline geri import edecek; sütun sırası, tırnaklama, ayraç, kodlama ya da
dokunulmamış bir hücre değişirse import reddedilir ve tool işe yaramaz.

## Scope (do exactly this)

1. **`internal/catalog/dialect.go`** — lehçe profilleri ve okuma.
   - `Field` kapalı kümesi: `title`, `description_html`, `seo_title`,
     `seo_description`, `tags`, `category`, `handle`, `sku`.
   - `Dialect{Key, Name, Signature, GroupBy, Columns}`; iki profil: `shopify`
     (`GroupBy: FieldHandle`) ve `ikas` (`GroupBy` boş).
   - `Detect(header []string) (Dialect, bool)` — `Signature` başlık kümesinin
     **alt kümesi** olarak eşleşir, böylece metafield sütunları olan bir mağaza
     yine çözülür.
   - `Parse(r io.Reader) (File, error)` — BOM sniff, kodlama tespiti
     (UTF-8 / UTF-8-BOM / Windows-1254), ayraç tespiti (`,` / `;`), sonra
     `encoding/csv`. `File` başlığı, sütun sırasını, ayracı, kodlamayı, BOM'u ve
     **her satırın tüm hücrelerini** olduğu gibi tutar.
   - Hiçbir profil eşleşmezse `File` yine döner, `Dialect` boş kalır; eşlemeyi
     operatör yazar.
2. **`internal/catalog/product.go`** — satırları ürüne gruplama.
   - `GroupBy` dolu ise aynı anahtarı taşıyan ardışık satırlar tek `Product`
     olur; alan değerleri o grubun **ilk dolu** hücresinden okunur (Shopify
     varyant satırlarında `Title`/`Body (HTML)` yalnız ilk satırda doludur).
   - `Product` kendi `RawRows`'unu taşır — dışa aktarım onları yazar.
3. **`internal/catalog/rte.go`** — RTE zarfı. `goquery`, sıfır model çağrısı.
   - `Elem{Tag, Attrs}`, `Block{Kind, Level, Text, Envelope []Elem}`,
     `Vocabulary{Tags, Attrs, Classes, Styles}`.
   - `ParseHTML(html) (Doc, error)`, `(Doc).Vocabulary()`,
     `(Doc).Render(blocks []Block, v Vocabulary) (string, error)`.
   - Satır içi işaretler kapalı bir sözdizimine (`**kalın**`, `_italik_`,
     `[metin](url)`) indirilir ve geri basımda markanın gerçek satır içi
     etiketlerine eşlenir.
   - `Render` **kapanarak** başarısız olur: `v` dışındaki her etiket, nitelik,
     sınıf ve stil düşer. Girdide geçmeyen bir `href` bağlantıyı düz metne indirir.
   - Kaynakta karşılığı olmayan yeni bir blok, **aynı türden en yakın bloğun**
     zarfını alır.
4. **`internal/catalog/brand.go`** — marka kimliği, iki yarım.
   - Deterministik yarım: import'un tüm açıklama HTML'i üzerinden `Vocabulary`,
     başlık hiyerarşisi, bölüm sayısı dağılımı, uzunluk dağılımı. Model yok.
   - Model yarımı: `cfg.CatalogBrandSampleSize` açıklamadan tek bir `llm.Distill`
     çağrısı, JSON şemasıyla doğrulanan `Voice{Address, Tone, Patterns,
     Banned, Lexicon}`. Reddedilirse yalnız `Voice` kaybolur; `Vocabulary` ve
     import ayakta kalır (SD-6).
   - `BrandKit` `version8(body)` hash'i taşır.
5. **`internal/catalog/export.go`** — kayıpsız geri yazım.
   - `Export(f File, products []Product, drafts map[string]Draft) ([]byte, error)`:
     ham satırları yazar, yalnız **onaylanmış** taslakların eşlenmiş hücrelerini
     değiştirir, ayracı/kodlamayı/BOM'u geri koyar.
   - `cfg.ExportDir` altına yazan `WriteExport` — leadgen workbook'larıyla aynı dizin.
6. **`internal/store/catalog.go` + `migrations/0025_catalog.sql`** — dört tablo:
   `catalog_imports`, `catalog_products`, `catalog_research`, `catalog_drafts`
   (şema planda). Bu task `research`'ü **yazmaz**, tabloyu kurar — migration'lar
   append-only olduğu için ikiye bölmek ikinci bir migration demek olurdu.
   - Her erişimci nil `*Store`'a toleranslı.
   - `PutDraft` koruması SQL'de:
     `ON CONFLICT(product_id, version) DO UPDATE ... WHERE catalog_drafts.edited_by_operator = 0`.
7. **`internal/config`** — sabitler: `CatalogCSVMaxBytes`,
   `CatalogMaxProductsPerImport`, `CatalogProductsPageMax`,
   `CatalogBrandSampleSize`, `CatalogSEOTitleMaxChars` (60),
   `CatalogSEODescMaxChars` (155), ve önbellek-sürüm sabiti `CatalogBrandVersion`.
   Hiçbirinin ortam override'ı yok (SD-1). `Validate()` boş olmadıklarını denetler.
8. **`internal/api/catalog.go`** — rotalar, `s.deps.Catalog != nil` kapısı altında:
   `POST|GET /catalog/imports`, `GET|DELETE /catalog/imports/{id}`,
   `PUT /catalog/imports/{id}/mapping`, `GET|PUT /catalog/imports/{id}/brand`,
   `POST /catalog/imports/{id}/brand/rescan`, `POST /catalog/imports/{id}/export`,
   `GET /catalog/products`, `GET /catalog/products/{id}`,
   `PUT /catalog/products/{id}/draft`, `POST /catalog/products/{id}/status`.
   - Yükleme `{filename, data_base64}` — `handleUploadAttachment`'ın gerekçesi.
   - `middleware.go`'daki `bodyLimits` tablosuna bir satır.
9. **`cmd/mimir-daemon/main.go`** — `catalog.New(...)` kurulur ve `api.Deps.Catalog`'a
   verilir. Store yoksa alan nil bırakılır (nil impl'li non-nil interface tuzağı).
10. **`go.mod`** — `golang.org/x/text` indirect'ten doğrudan bağımlılığa geçer.
    İndirilen yeni bir modül yok.
11. **`internal/catalog/AGENTS.md`** — depo şablonuna göre.

## Out of scope (do NOT do here)

- Ürün başına rakip araştırması ve içerik yeniden yazımı — task-87.
- `coderunner.Executor`, `internal/agents` kaydı, `product-content` skill'i,
  MCP araçları, limit parkı — task-87.
- Masaüstünde herhangi bir ekran, TipTap, CSP değişikliği — task-86.
- WooCommerce ya da üçüncü bir lehçe.
- Admin paneli API'siyle yazma; bu tool CSV ile çalışır.

## Interfaces / contracts

```go
// internal/catalog
type Field string
type Dialect struct{ Key, Name string; Signature []string; GroupBy Field; Columns map[Field]string }
type File struct {
    Header    []string
    Delimiter rune
    Encoding  string
    HasBOM    bool
    Rows      [][]string
    Dialect   Dialect
    Mapping   map[Field]string // Dialect boşsa operatörün yazdığı
}
func Detect(header []string) (Dialect, bool)
func Parse(r io.Reader) (File, error)

type Elem  struct{ Tag string; Attrs [][2]string }
type Block struct{ Kind BlockKind; Level int; Text string; Envelope []Elem }
type Vocabulary struct{ Tags, Attrs, Classes, Styles map[string]int }
type Doc    struct{ Blocks []Block }
func ParseHTML(html string) (Doc, error)
func (d Doc) Vocabulary() Vocabulary
func (d Doc) Render(blocks []Block, v Vocabulary) (string, error)

type Voice    struct{ Address, Tone string; Patterns, Banned, Lexicon []string }
type BrandKit struct{ Vocab Vocabulary; Voice Voice; Version string }
func (s *Studio) DeriveBrand(ctx context.Context, f File) (BrandKit, error)
```

```sql
-- 0025_catalog.sql — dört tablo, planın §2'sindeki şema
```

## Definition of Done

- [x] `TestExport_IsByteIdenticalWhenNothingWasApproved` — `testdata/` altında
      birer Shopify, IKAS ve Windows-1254 export'u; import → export bayt bayt aynı.
- [x] `TestShopifyVariantRowsCollapseIntoOneProduct`.
- [x] `TestParseCSV_ReadsWindows1254` + `TestParseCSV_ReadsTheFramingOfEachDialect`
      (BOM, noktalı virgül ve "her alan tırnaklı" dahil).
- [x] `TestRender_CannotEmitATagTheInputDidNotUse` — sözlük alt küme değişmezi.
- [x] `TestRender_DropsALinkWhoseHrefWasNotInTheInput` +
      `TestRender_DropsAnImageWhoseSourceWasInvented`.
- [x] `TestRender_OfAParsedDescriptionIsStable` — tam dönüş yerine **kararlılık**;
      gerekçe changelog'da.
- [x] `TestDeriveVocabulary_CostsNoModelCall` — nil `Completer`.
- [x] `TestDeriveBrand_KeepsTheVocabularyWhenTheVoiceIsUnavailable`.
- [x] `TestPutCatalogDraft_IsNotOverwrittenAfterTheOperatorEditedIt`.
- [x] `internal/api`: rota kapısı, `bodyLimits` girdisi, `status` geçersiz değeri 400,
      sürümün sunucuda çözülmesi.
- [x] `make check` yeşil.
- [x] Status `done` + changelog.

## Notes for the reviewer (Opus)

- **SD-1**: `Catalog*` sabitlerinden herhangi biri `os.Getenv` okuyor mu?
  `CatalogBrandVersion` bir prompt değişikliğiyle birlikte yükseldi mi?
- **SD-2**: bu task kazınmış sayfa metni taşımıyor; taşıdığı an task-87'ye
  kaymış demektir.
- **SD-6**: `Voice` reddi import'u düşürüyor mu? Düşürüyorsa yanlış.
- Kayıpsızlık testi gerçek bir export dosyası üzerinde mi, elle yazılmış üç
  satırlık bir fikstür üzerinde mi? İkincisiyse yeterli değil.
- `Render` sözlük dışını **düşürüyor** mu, yoksa geçiriyor mu? Geçiriyorsa
  bütün katmanın iddiası yok demektir.

---

## Changelog — 2026-09-06

**Yeni paket `internal/catalog`.** Bir e-ticaret ürün export'unu okuyan, ondan
markanın işaretleme ve ses sözlüğünü çıkaran, ve dosyayı geri yazan çekirdek.
Bu task'ta hiçbir ürün içeriği yeniden yazılmadı; yeniden yazımın **güvencesi**
kuruldu.

**Kendi CSV okuyucumuz ve yazıcımız — plandan sapma, gerekçesi bir hata.**
Plan dosya başına tek bir tırnak kuralı ("minimal" ya da "hepsi") öngörüyordu.
İlk kayıpsızlık testi bunu düşürdü: Shopify `Body (HTML)` sütununu gerekmese de
tırnaklıyor ve diğer sütunları yalnız gerektiğinde tırnaklıyor, yani dosya
başına tek bir kural o dosya hakkında iki yönden birden yanılıyor. Tırnaklama
artık **hücre başına** kaydediliyor (`File.Quoted`), ki bu `encoding/csv`'nin
cevaplayamadığı bir soru — okuyucu bu yüzden bizim. Yazıcı da bizim: `csv.Writer`
satır sonunu bir boolean'la, tırnaklamayı sabit bir kuralla ve kapanış satır
sonunu her zaman kendisi karar veriyor, yani gerçek bir export'u ondan geçirmek
operatörün kendi paneline geri vereceği dosyanın her satırını sessizce yeniden
yazardı. Yeni yazılan bir hücre — genişletilmiş bir satır — dosyanın kendi
kuralına düşüyor.

**Tam dönüş değil, kararlılık.** Plan `Render(Parse(x)) == x` diyordu. Keyfi
HTML üzerinde bu bir DOM ayrıştırmasından geçirilerek elde edilemez: boşluk
daraltılır, nitelik sırası normalize edilir, varlıklar yeniden kodlanır.
Sözleşme `ParseHTML(Render(...))`'ın aynı blokları vermesi ve ikinci basımın
birinciyle **bayt bayt aynı** olması olarak yazıldı. Asıl garanti zaten başka
yerde ve daha güçlü: dışa aktarım onaylanmamış bir hücreye hiç dokunmuyor, o
yüzden CSV'nin bayt eşitliği HTML normalizasyonundan bağımsız.

**`CatalogContentVersion` bu task'ta tanımlandı** — plan onu task-87'ye
koymuştu. Taslak tablosu burada kurulduğu için anahtarının şekli de burada
tanımlanmalıydı; iki task'ın iki farklı anahtar üretmesi, task-85'te elle
düzenlenmiş her taslağı task-87'de görünmez yapardı. `internal/catalog.DraftVersion`
dört şeyi birleştiriyor: prompt sabiti, model seçimi, marka hash'i, skill sürümü.
Skill sürümü task-87 gelene kadar boş ve boş bir yarım anahtarda hiç görünmüyor.

**Marka hash'i sayımları içermiyor.** Bir ürün eklendiği için bir `<p>` daha
sayılması, bir yeniden yazımın ne yazabileceğini değiştirmiyor. Hash sesi,
sözlüğün etiket **kümesini** ve yapı medyanlarını kapsıyor; testi iki yönlü,
çünkü iki yön de önemli: ses düzenlenince değişmeli (bayat metin kalmasın), ürün
eklenince değişmemeli (büyüyen bir katalog her gece her şeyi geçersiz kılmasın).

**`internal/store` bu paketi import ediyor, tersi değil.** Satır şekilleri
(`StoredImport`, `StoredProduct`, `StoredDraft`) katalog paketinde; store'un işi
onları yazmak, bir katalog ürününün ne olduğuna karar vermek değil. Motorun kendi
`Store` arayüzü var ve nil'e toleranslı.

**Elle düzenleme koruması SQL'de.** `PutCatalogDraft`'ın
`WHERE catalog_drafts.edited_by_operator = 0` koşulu `outreach_emails`'in
`WHERE status = 'draft'` koruması ile aynı biçimde: eşzamanlı bir yazım onu
atlayamıyor. Reddedilen yazım `ErrCatalogDraftLocked` olarak dönüyor, yutulmuyor —
çağıran onu bir atlama olarak anlatacak, ki öyle.

**Dokümanlar.** `docs/CAPABILITIES.md` ve `.tr.md`'ye on rotalık `/catalog/*`
bloğu, dört tablo için iki kalıcılık satırı ve "çalışması için gerekenler"e iki
satır. `CHANGELOG.md`'nin `[Yayımlanmadı]` bölümü.

`make check` yeşil (build + vet + lint + test + race).

## Sonradan bulunan ve task-86'da düzeltilen kusurlar

Ekran gerçek daemon'a karşı sürülürken dört kusur çıktı ve
[task-86](task-86-desktop-catalog.md)'nın changelog'unda gerekçeleriyle kayda
geçti: içe aktarmanın sınırsıza yakın süresi (`CatalogVoiceTimeout`),
operatörün kendi yazdığı bağlantının uydurma sayılması (`urlPolicy`),
`<script>` gövdesinin metin sayılması (`opaqueTags`), ve elle düzenlenmiş bir
taslağın sahibi tarafından bile yeniden kaydedilememesi. Dördünün de testi var.
