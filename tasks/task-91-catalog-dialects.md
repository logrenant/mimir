# task-91 — Gerçek IKAS lehçeleri, sütun önerisi ve varyant satırlarına yazım

- **Status:** done
- **Owner agent:** Coder (Gemini)
- **Prerequisites:** task-85, task-87
- **Primary paths:** `internal/catalog/**`, `internal/api/catalog.go`
- **Roadmap bucket:** Katalog — ürün içeriği stüdyosu

## Context

task-85'in IKAS profili **uydurmaydı.** Gerçek bir IKAS ürün dışa aktarımının
başlığı `Ürün Adı / Açıklama / Stok Kodu` değil; 37 sütunlu ve şöyle:

```
Ürün Grup ID, Varyant ID, İsim, Açıklama, Satış Fiyatı, …, SKU, …,
Kategoriler, Etiketler, Resim URL, Metadata Başlık, Metadata Açıklama, Slug, …
```

Sonuç: operatör kendi mağazasının dosyasını attı, ekran `TANINMADI` dedi, elle
eşleme formuna düştü. Bu, bir tanıma hatasının bedelinin ne olduğunu gösteriyor
— dosya okunabilirdi, profil yanlış yerde arıyordu.

İkinci bir gerçek dışa aktarım daha var: **özel alanlar** (`Ürün Grup ID, İsim,
Resim:Ana Resim, Html:Detay, Html:Detay-AR, …`). RTE HTML'i `Html:Detay`'da
duruyor, yani bu da düzenlenecek bir içerik yüzeyi.

Üçüncüsü: IKAS varyant başına bir satır yazar ve `Ürün Grup ID` onları
birleştirir. `Export` bugün alanı **tek bir satıra** yazıyor (Shopify'ın
gerekçesi: yalnız ilk satır dolu). IKAS'ta aynı açıklama her varyant satırında
tekrar eder; tek satıra yazmak dosyada birbiriyle çelişen iki açıklama bırakır.

## Scope (do exactly this)

1. **`FieldGroupID`** kapalı kümeye eklenir (`group_id`): varyant satırlarını
   birleştiren kimlik. `Writable() == false`. `Fields()` sırasına girer.
   `FieldHandle` anlamını korur — ürünün URL'i, IKAS'ta `Slug`.

2. **`dialects` gerçek başlıklardan yeniden yazılır:**
   - `ikas` — Signature `{Ürün Grup ID, İsim, Açıklama, SKU}`, `GroupBy:
     FieldGroupID`. Sütunlar: `group_id→Ürün Grup ID`, `handle→Slug`,
     `sku→SKU`, `title→İsim`, `description_html→Açıklama`,
     `seo_title→Metadata Başlık`, `seo_description→Metadata Açıklama`,
     `tags→Etiketler`, `category→Kategoriler`.
   - `ikas-fields` — özel alanlar dışa aktarımı. Signature
     `{Ürün Grup ID, İsim, Html:Detay}`, `GroupBy: FieldGroupID`,
     `title→İsim`, `description_html→Html:Detay`.
   - `shopify` değişmez.
   - **`ikas-en` silinir.** O da uydurmaydı ve elimde doğrulayacak gerçek bir
     İngilizce dışa aktarım yok. Hiç eşleşmeyen bir profil, listede duran bir
     iddiadır; yerini 3. maddedeki öneri motoru alır.

3. **`suggest.go` — `Suggest(header []string) map[Field]string`.** Tanınmayan
   bir başlık için en iyi tahmin: önce her profilin kendi sütun adları, sonra
   alan başına bir eşanlamlı tablosu (TR + EN, `normalizeHeader` ile). **Sıfır
   model çağrısı**, deterministik, aynı sütun iki alana atanmaz. Bu, "tanınmadı"
   ile "elle 8 açılır liste doldur" arasındaki mesafeyi kapatır.

4. **`SetMapping` eksik eşlemeyi reddeder.** `ErrMappingIncomplete`: `title` ya
   da `description_html` olmadan bir ürün diye bir şey yoktur. API 400 ile
   cümleyi geri verir. Ayrıca eşleme kaydeden bir import artık **okunabilir**
   sayılır (bkz. 5).

5. **Görünüm eşlemeyi taşır.** `catalogImportResponse`'a üç alan:
   `mapping map[string]string` (kayıtlı), `suggested map[string]string`
   (öneri; yalnız lehçe yokken doldurulur) ve `readable bool` (lehçe **ya da**
   yeterli eşleme). `readable`, ekranın kapısıdır — bugün ekran `dialect == ""`
   diye kapalı kalıyor ve kaydedilmiş bir eşleme onu **hiç açmıyor**.
   Dördüncü alan `sample map[string]string`: dosyanın ilk veri satırından
   sütun başına kırpılmış bir örnek değer — 37 sütunlu bir dosyada bir sütunu
   adından çok içeriği tanıtır.

6. **`Export` alanı taşıyan her satıra yazar.** `rowFor` → `rowsFor`: alanın
   özgün hâlinin dolu olduğu **tüm** satırlar; hiçbiri dolu değilse ürünün ilk
   satırı. Shopify'da davranış birebir aynı kalır (yalnız ilk satır doludur).

7. **Testdata**: `testdata/ikas-products.csv` ve `testdata/ikas-fields.csv` —
   başlık satırı gerçek dışa aktarımdan **birebir**, veri satırları uydurma.
   Eski `testdata/ikas.csv` uydurma başlığıyla birlikte gider.

## Out of scope (do NOT do here)

- `desktop/**` — eşleme formu, öneri gösterimi ve editör/önizleme yerleşimi
  task-90'ındır.
- Yeni platform (WooCommerce, Trendyol). Bu task yalnız elde gerçek dışa
  aktarımı olan lehçeleri ekler.
- Lehçeyi modele tahmin ettirmek. Öneri deterministiktir.

## Interfaces / contracts

```go
const FieldGroupID Field = "group_id"

// Suggest is a best guess at an unknown header's column map. Zero model calls.
func Suggest(header []string) map[Field]string

var ErrMappingIncomplete = errors.New("catalog: a mapping needs at least a title or a description column")

// rowsFor is every row this product's value for a field lives on.
func (p Product) rowsFor(f File, field Field) []int
```

```jsonc
// GET /catalog/imports/{id}
{ "dialect": "", "readable": true,
  "mapping":   {"title": "İsim", "description_html": "Açıklama"},
  "suggested": {"title": "İsim", "description_html": "Açıklama"} }
```

## Definition of Done

- [x] Gerçek IKAS ürün dışa aktarımı ve özel alanlar dışa aktarımı `Detect` ile
      tanınıyor; testleri gerçek başlıkla.
- [x] `Suggest` nil `Completer` ile koşan bir testte tanınmayan bir başlığı
      doğru eşliyor.
- [x] Eşleme kaydeden import `readable: true` dönüyor.
- [x] Eksik eşleme 400 ve okunabilir bir cümle.
- [x] Varyant satırlı bir üründe export alanı taşıyan her satıra yazıyor;
      Shopify altın dosya testi hâlâ bayt bayt aynı.
- [x] `make check` yeşil.
- [x] Status `done` + changelog.

## Notes for the reviewer (Opus)

- **SD-1**: eşanlamlı tablosu sabittir, ayar değildir.
- **SD-6**: `Suggest` yanlış tahmin edebilir; bu yüzden öneridir, karar değil —
  operatör her zaman üstüne yazar.
- **SD-8**: her yeni lehçenin bir fikstürü var; başlık gerçek, satır uydurma.
- Kayıpsızlık sözleşmesi: `rowsFor` değişikliği altın dosya testini bozmamalı.

## Changelog

- **`ikas` profili gerçek bir dışa aktarımdan yeniden yazıldı.** task-85'inki
  hafızadan yazılmıştı (`Ürün Adı`, `Stok Kodu`, `Kategori`) ve kimsenin
  indirmediği bir dosyayı tarif ediyordu. Gerçek başlık 37 sütunlu:
  `Ürün Grup ID / Varyant ID / İsim / Açıklama / … / SKU / … / Kategoriler /
  Etiketler / … / Metadata Başlık / Metadata Açıklama / Slug`.
- **`ikas-fields`** eklendi: IKAS'ın özel alanlar dışa aktarımı (`Html:Detay`).
- **`ikas-en` silindi.** O da uydurmaydı ve doğrulayacak bir dosya yoktu. Hiç
  eşleşmeyen bir profil, listede duran bir iddiadır.
- **`FieldGroupID`** kapalı kümeye girdi. IKAS varyantları `Ürün Grup ID` ile
  birleşir; `FieldHandle` anlamını korudu (ürünün URL'i — IKAS'ta `Slug`).
  İkisi de yazılamaz.
- **`Suggest`** — tanınmayan bir başlık için deterministik, model çağrısız sütun
  tahmini. Bir sütun iki alana verilmez: `Açıklama` gövdeye, `Metadata Açıklama`
  SEO'ya gider ve ikisi yer değiştirmez.
- **`readable`** görünüme girdi ve `ErrMappingIncomplete` eklendi. Yaşanan hata
  buradaydı: `SetMapping` bir lehçe *uydurmaz*, eşlemeyi kaydeder — `dialect`'e
  bakan bir kapı hiç açılmaz. Ayrıca `mapping`, `suggested` ve sütun başına
  `sample` gönderiliyor.
- **`rowFor` → `rowsFor`.** Alan, özgün hâlinin dolu olduğu her satıra yazılıyor.
  Shopify'da bu bir satır (yalnız ilki dolu) ve davranış birebir aynı; IKAS'ta
  açıklama her varyant satırında tekrar ettiği için tek satıra yazmak dosyayı
  kendisiyle çelişir hâle getiriyordu.
- **Fikstürler**: `ikas.csv` gerçek çerçeveye alındı (virgül, UTF-8 BOM, CRLF,
  her hücre tırnaklı — eskiden noktalı virgüldü), `ikas-variants.csv` ve
  `ikas-fields.csv` eklendi. `internal/catalog/AGENTS.md`'ye kural yazıldı:
  **bir lehçe profili yalnız eldeki gerçek bir dışa aktarımdan eklenir** —
  veri satırları uydurma olabilir, sütun adları olamaz.

- **`StoredImport.value` lehçeyi yeniden algılıyor, `POST /catalog/imports/{id}/reread`
  ürünleri yeniden kuruyor.** Bir lehçe profili koddur; sonradan eklenen bir
  profil, ondan önce yüklenmiş bir dosyayı da doğru okur. Yalnız rozeti
  düzeltmek yetmezdi — yükleme anında yazılmış ürünler sütunsuz kurulmuştu ve
  ekran "IKAS · 1013 ürün" deyip boş satır gösterirdi. Operatörün kendi
  eşlemesi varsa profil onu ezmez: o eşleme zaten bir kez başarısız olduğumuz
  için var.

### Sapmalar

- **`CatalogSampleChars` bu task'ta `internal/config`'e eklendi**, oysa
  "Primary paths" yalnız `internal/catalog` ve `internal/api` diyordu. Kırpma
  uzunluğu bir sunum kararı ama yine de bir sabit, ve SD-1 onun yeri konusunda
  net; `internal/api` içinde tanımlanmış bir sayı, ayarların iki yerde olması
  demekti.
- **`Reread` ve rotası planda yoktu.** Retroaktif algılamayı eklerken ortaya
  çıktı: yarısı (rozet doğru, tablo boş) hiç eklememekten kötüydü.
- **Eşleme formunun kimlik alanlarını (`group_id`, `sku`, `category`) sunması
  task-90'a düştü** — planda ayrıca yazılmamıştı. Sunucu bu sütunları zaten
  kabul ediyordu; onları formda göstermemek, tanınmayan bir dosyanın varyant
  satırlarını hiç gruplayamaması demekti.

### Gerçek dosyalarla doğrulama

Operatörün kendi üç dosyası, geçici bir testle:

| Dosya | Lehçe | Ürün | Bayt bayt dönüş |
|---|---|---|---|
| `05_ikas_import_AR.csv` | `ikas` | 1 | aynı |
| `ikas-urunler.csv` | `ikas` | 51 | aynı |
| `ikas-urun-ozel-alanlar.csv` | `ikas-fields` | 1013 | aynı |

`make check: 0`
