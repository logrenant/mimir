# task-111 — Katalog: gerçek IKAS profilleri, alan yapılandırması ve dili söylenmemiş çeviri sütunları

- **Status:** done
- **Owner agent:** Coder
- **Prerequisites:** task-103, task-105, task-107
- **Primary paths:** `internal/catalog/**`, `internal/api/catalog.go`, `desktop/src/**`
- **Roadmap bucket:** Katalog — ürün içeriği stüdyosu

## Context

Operatör dört gerçek IKAS dışa aktarımını verdi (`ikas-urunler`,
`ikas-urun-ozel-alanlar`, `ikas-varyantlı-urun-ozel-alanlar`, `ikas-ceviriler`).
Üç şey ortaya çıktı:

1. **Çeviriler dışa aktarımı** task-109'un beklediği uzun format değil, **geniş**
   format: `İsim, Açıklama, Meta Başlığı, Meta Açıklaması, Meta Slug` yanında
   `Çevrilecek …` sütunları. 1013 ürün, 1009'unda çeviri dolu. Ek koda ihtiyaç
   duymadı — ama dosya **hangi dile** çevrildiğini yazmıyor.
2. **Varyant düzeyinde özel alanlar** ayrı bir dışa aktarım ve başlığı
   `ikas-fields` imzasını kapsıyor; sıralama olmadan yanlış profile düşüyordu.
3. **Alan kümesi sabit değil.** Bu mağazanın ürün export'unda `SKU` tamamen boş,
   `Satış Kanalı:meletiorient` mağazaya özel, `Html:Detay` ve `Html:Detay-AR`
   sütunları var ama hiç dolu değil. Sabit bir alan listesi varsaymak yanlış.

## Scope (do exactly this)

1. `testdata/`: `ikas-ceviriler.csv` ve `ikas-fields-variant.csv` — başlıklar
   gerçek dosyalardan birebir.
2. İki yeni profil. `ikas-fields-variant`, `ikas-fields`'tan **önce**.
3. `Dialect.TargetColumns` + `File.TargetLang` + `File.PendingTarget()` +
   `SetTargetLang`. Dil tahmin edilmez, sorulur.
4. `File.Write []LangField` + `Offered` / `Writes` / `WriteSet` /
   `normalizeWrite` + `SetWrite`. `File` üzerinde, `Mapping`'in yanında —
   aynı dosya hakkında aynı türden operatör cevabı, ve migration gerektirmiyor
   çünkü `file_json` zaten opak.
5. `Rewrite`, yapılandırmayı **kesişim** olarak uygular ve atladığını söyler.
6. `CheckLanguage(..., want []Field)` — boşluk kontrolü yalnızca yazılması
   istenen alanlara bakar; Arapça yüzeyi tek sütun olan dosyada "başlık boş"
   demek her taslağı reddederdi.
7. `PUT /catalog/imports/{id}/fields`, `PUT …/target-lang`; içe aktarım yanıtında
   dil başına `fields[]` ve `pending_target`.
8. Masaüstü: `ui/switch.tsx`, `FieldsPanel`, `TargetLangLine`, kart düğmesinin
   üstünde ne yazılacağının özeti.

## Definition of Done

- [x] Dört gerçek dosya doğru profile düşüyor; dışa aktarım **dördünde de**
      bayt bayt aynı (8.7 MB'lık çeviri dosyası dahil)
- [x] Çeviriler dosyası dilini soruyor; söylenmeden hiçbir hedef dil sunulmuyor
- [x] Dil söylendikten sonra 1009/1013 üründe hedef dil içeriği okunuyor
- [x] Yapılandırma saklanıyor ve `Reread`'den sonra da duruyor
- [x] Kapatılan alan, kart onu istese bile yazılmıyor; atlanan alan not olarak
      söyleniyor
- [x] Hepsi kapalı bir yapılandırma reddediliyor (boş = "hepsi" demek olurdu)
- [x] `make check`, `make e2e`, `npm run typecheck && npm test` yeşil

## Notes for the reviewer (Opus)

- Yapılandırma neden `File`'da: `Mapping` zaten orada ve aynı türden bir şey —
  bu dosya hakkında operatörün cevabı. `file_json` opak saklandığı için yeni bir
  JSON anahtarı geriye dönük uyumlu ve migration gerekmiyor.
- Boş kümenin "hepsi" demesi bilinçli: yapılandırılmamış bir içe aktarım bugüne
  kadar her alanı yazıyordu ve öyle kalmalı. Bu yüzden `SetWrite` boşa
  normalleşen bir seçimi **reddediyor** — saklasaydı her anahtarı geri açardı.
- Toggle'lar anında uygulanmıyor ve bu, anahtarların olağan kuralını çiğniyor.
  Sebep: yapılandırma tek bir küme ve yarım uygulanmış bir küme, kimsenin
  istemediği bir sütuna yazan bir yeniden yazım demek. Bedeli, birinin
  değişikliğin geçtiğini sanıp gitmesi — panel bu yüzden kaydedilmemiş durumu
  **yazıyla** söylüyor, rengiyle değil.
