# task-109 — Katalog: Shopify Translate & Adapt uzun-format lehçesi

- **Status:** superseded by task-111 for IKAS; still blocked for Shopify
- **Owner agent:** Coder
- **Prerequisites:** task-103 — **ve elde gerçek bir Translate & Adapt dışa aktarımı**
- **Primary paths:** `internal/catalog/{dialect,product,export}.go`, `internal/catalog/testdata/`
- **Roadmap bucket:** Katalog — ürün içeriği stüdyosu

## Context

Shopify'ın **ürün** CSV'sinde çeviri sütunu yok. Çeviriler ayrı bir dosyadan,
ve **uzun (tall)** formatta çıkıyor: her satır bir kaynak + bir alan + bir dil.

Araştırmadan çıkan sütun sırası — **doğrulanmadı, profil olarak yazılmadı**:

```
Type,Identification,Field,Locale,Market,Status,Default content,Translated content
```

Kaynaklar: Shopify Yardım Merkezi'nin "Translate & Adapt" sayfası (dışa/içe
aktarımın varlığını doğruluyor, sütunları yazmıyor) ve Crowdin'in bu dosyayı
tüketen entegrasyonunun belgeleri. İkisi de bir dosyanın kendisi değil.

## Neden blocked

`internal/catalog/AGENTS.md`: *"A dialect profile is only ever added from an
export somebody has in front of them — the data rows may be invented, the column
names may not."* Bu kural task-91'de, task-85'in hafızadan yazılmış IKAS
profilinin hiçbir gerçek dosyayla eşleşmemesi üzerine yazıldı.

Gerçek dosya olmadan bilinemeyecek olanlar:

- Başlıkların yazımı ve büyük/küçük hâli (`Default content` mı `Default Content` mı).
- Tek pazarlı bir mağazanın çıktısında `Market` sütunu var mı.
- `Type` sözlüğü: `PRODUCT` / `Product` / `product`.
- `Field` sözlüğü: `title`, `body_html`, `meta_title`, `meta_description`, …
  ve bunlardan hangilerini T&A gerçekten üretiyor.
- Yerel ad yazımı: `ar` mı `ar-SA` mı `ar_SA` mı.

## Scope (dosya geldiğinde)

1. `Dialect.Shape` (`wide` / `tall`) ve satırdan alan/dil okuyan `Tall` betimi.
   Amaç, profilin bir **veri girdisi** olması — kod yolu değil.
2. `Products()` için `resolver` dikişi: geniş formatta alan bir sütun, uzun
   formatta alanın **adı** bir hücre.
3. `rowsFor`, uzun formatta yalnızca **var olan** satıra yazar. Olmayan bir
   `(handle, body_html, ar)` satırı **eklenmez**: eklemek `Type`, `Status` ve
   `Market` değerlerini tahmin etmek olurdu. Taslağa not düşülür ve `Skipped`
   sayılır.
4. `testdata/` altına gerçek başlık satırı.

## Notes for the reviewer (Opus)

- Byte-identity iddiası uzun formatta daha da kolay korunuyor: `File.Rows` yine
  her ham hücreyi tutuyor ve `Export` yalnızca onaylanmış ve değişmiş hedef
  hücreyi yazıyor.
- **ikas Çeviriler** dışa aktarımı geldi ve tahmin doğru çıktı: geniş format,
  ek kod gerekmedi. task-111'de `ikas-ceviriler` profili olarak girdi. Bu
  task'ta geriye yalnızca Shopify'ın uzun formatı kaldı ve o hâlâ gerçek bir
  dosya bekliyor.
