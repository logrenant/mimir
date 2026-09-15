# task-90 — Desktop: eşleme kapısı, sütun önerisi ve yan yana editör/önizleme

- **Status:** done
- **Owner agent:** Coder (Gemini)
- **Prerequisites:** task-86, task-91
- **Primary paths:** `desktop/**`
- **Roadmap bucket:** Katalog — ürün içeriği stüdyosu

## Context

Operatör kendi IKAS dosyasını attı, `TANINMADI` gördü, sekiz açılır listeyi
elle doldurdu, **"kaydet ve oku"ya bastı ve hiçbir şey olmadı.**

Sebebi tek satır: `needsMapping(view) = view.dialect === ""`. `SetMapping` bir
lehçe *uydurmaz* — eşlemeyi kaydeder, lehçe boş kalır — yani form kaydettikten
sonra da kendini gösterir. Ekran, kendi kaydettiği şeyi görmüyordu.

İkinci eksik: form boş açılıyor. 37 sütunlu bir dosyada operatörden sekiz
seçimi sıfırdan yapmasını istemek, tanıma hatasının faturasını ona kesmektir.
task-91 `suggested` gönderiyor; form onunla açılmalı.

Üçüncüsü, kullanıcının kendi cümlesi: *"aktarılan productu RTE ile yan yana
preview hâlini de görebilmemiz lazım. edit öncesi ve sonrası bunların hepsini
görebiliyor olmalıyız."* Bugün önizleme ve editör **ayrı sekmelerde** ve panel
26rem — yan yana koyacak yer yok.

## Scope (do exactly this)

1. **Kapı `readable`'a bağlanır.** `needsMapping(view)` → `!view.readable`.
   Eşleme kaydedildiği anda tablo açılır.

2. **Form önerilerle açılır.** Başlangıç durumu `view.mapping` varsa o, yoksa
   `view.suggested`. Boş bir öneri hâlâ boş formdur — uydurma yapılmaz.

3. **Her seçimin altında dosyanın kendi ilk satırından örnek değer.** 37 sütunlu
   bir dosyada "Açıklama" ile "Metadata Açıklama"yı ayıran şey addan çok
   içeriktir. `view.sample` (task-91'in gönderdiği ilk veri satırı) kırpılarak
   gösterilir.

4. **Eşleme her zaman erişilebilir.** Başlıkta "sütunlar" düğmesi: lehçe
   tanınmış olsa bile form açılır ve üstüne yazılabilir. Kullanıcının şartı:
   *"eğer uyumlu değilse kullanıcı tarafında editable olması lazım."*

5. **Kaydet düğmesi eksik eşlemede kapalı** ve nedenini söyler (`title` ya da
   `description_html` şart) — 400'ü sunucudan öğrenmek yerine.

6. **Ürün tezgâhı (workbench).** Bir üründe "aç" ekranı tam genişliğe alır:
   - solda **RTE editör**, sağda **canlı önizleme** — yan yana, aynı anda;
   - önizlemenin üstünde `önce · sonra` anahtarı: özgün HTML ve taslak;
   - `alanlar` (SEO başlık/açıklama/etiket, canlı sayaç) altta kalır;
   - "listeye dön" geri götürür, seçili ürün korunur.
   Dar paneldeki üç sekmeli hâl listede kalır — hızlı bakış için.

7. **`lib/catalog.ts`** saf yarımı taşır ve test edilir: `needsMapping`
   (readable), `initialMapping(view)`, `mappingIsComplete(m)`,
   `sampleFor(view, column)`.

## Out of scope (do NOT do here)

- `internal/**` — `suggested`, `readable`, `sample` alanları task-91'indir.
- Yeni bağımlılık. TipTap zaten kurulu; tezgâh yerleşimi CSS'tir.
- Önizlemenin sandbox kuralını gevşetmek. `PREVIEW_SANDBOX` aynen kalır.

## Interfaces / contracts

```ts
export function needsMapping(view: CatalogImportView): boolean; // !view.readable
export function initialMapping(view: CatalogImportView): Record<string, string>;
export function mappingIsComplete(m: Record<string, string>): boolean;
export function sampleFor(view: CatalogImportView, column: string): string;
```

## Definition of Done

- [x] Eşleme kaydedildikten sonra ürün tablosu açılıyor (yaşanan hata).
- [x] Form tanınmayan bir IKAS dosyasında dolu açılıyor.
- [x] Her seçimin altında dosyanın kendi örnek değeri.
- [x] Tanınmış bir dosyada da eşleme açılıp değiştirilebiliyor.
- [x] Tezgâhta editör ve önizleme yan yana; önizleme önce/sonra anahtarlı.
- [x] `lib/catalog.test.ts` yeni saf fonksiyonları kapsıyor.
- [x] `make desktop-check` yeşil.
- [x] Status `done` + changelog.

## Notes for the reviewer (Opus)

- Mantık `lib/`de, JSX'te değil (`desktop/AGENTS.md`).
- Tezgâh yeni bir *ekran* değil, aynı ekranın bir kipi — `modules.ts`'e girmez.
- Öneri bir karar değil: form her zaman üstüne yazılabilir olmalı.

## Changelog

- **Kapı `readable`'a bağlandı.** Yaşanan hatanın tamamı buydu: `needsMapping`
  `view.dialect === ""` diye soruyordu, `SetMapping` ise bir lehçe uydurmuyor —
  eşlemeyi kaydediyor. Operatör 37 sütunu elle eşledi, kaydete bastı, ve form
  geri geldi. Testi `lib/catalog.test.ts`'de: lehçesiz ama eşlenmiş bir dosya
  bir daha eşleme istemiyor.
- **Form dolu açılıyor** — kayıtlı eşleme varsa o, yoksa daemon'ın tahmini —
  ve her seçimin altında dosyanın kendi ilk satırından bir örnek değer duruyor.
- **Eşleme her zaman erişilebilir**: başlıkta "sütunlar". Tanınmış bir dosyada
  da açılıyor, çünkü bir lehçe eşleşmesi başkasının dışa aktarım biçimi
  hakkında iyi bir tahmindir, bu dosya hakkında bir olgu değil.
- **Kaydet düğmesi eksik eşlemede kapalı** ve nedenini yazıyor; 400'ü sunucudan
  öğrenmek gerekmiyor.
- **Ürün tezgâhı**: tam genişlikte, editör solda, canlı önizleme sağda,
  önizlemenin üstünde `önce · sonra`. "sonra", kaydedilmemiş düzenleme dahil
  editördeki hâl — karşılaştıran kişi az önce yazdığını karşılaştırıyor.
  Tabloda satır başına "aç", çift tık da açıyor.
- **`useDraft`** — panel ile tezgâh aynı düzenleme durumunu paylaşıyor.
  "Şu anki içerik ne, ne değişti, nasıl kaydedilir" sorusunun ikinci bir
  kopyası, birinciyle çelişebilecek bir kopyadır.
- **Eşleme formu kimlik sütunlarını da sunuyor** (`Ürün grup ID`, `SKU`,
  `Kategori`), soluk yazılmış. Sunucu bunları zaten kabul ediyordu; formda
  olmamaları, tanınmayan bir dosyanın varyant satırlarını hiç gruplayamaması ve
  kategori rayının hep boş kalması demekti.

- **"Yeniden oku" satırı.** Okunabilir ama bütün ürünleri boş olan bir import,
  daemon onu okuyamazken içe aktarılmış demektir (`needsReread`). Ekran bunu
  söylüyor ve düğmeyi veriyor; kendiliğinden bin satırı yeniden okumuyor.

### Sapmalar

- **Tezgâh `modules.ts`'e girmedi.** Yeni bir ekran değil, aynı ekranın bir
  kipi: kenar çubuğunda yeri yok, bir import içindeki bir ürün.
- **`DIALECT_LABELS`'tan `ikas-en` çıktı, `ikas-fields` girdi** — task-91'in
  profil değişikliğinin desktop tarafındaki karşılığı. Etiket tablosu
  `desktop/**` altında olduğu için burada.

`make desktop-check: 0`
