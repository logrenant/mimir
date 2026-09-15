# task-104 — Masaüstü: dil sekmeleri, RTL düzenleme ve dil başına sütun eşleme

- **Status:** done
- **Owner agent:** Coder
- **Prerequisites:** task-102, task-103, task-105
- **Primary paths:** `desktop/src/lib/{daemon,catalog}.ts`, `desktop/src/components/RichText.tsx`, `desktop/src/screens/Catalog.tsx`
- **Roadmap bucket:** Katalog — ürün içeriği stüdyosu

## Context

Daemon çok dilli okuyor, yazıyor ve dışa aktarıyor; ekran hâlâ tek dil biliyor.

## Scope (do exactly this)

1. `daemon.ts`: `CatalogLanguage`, `CatalogImportView.languages`,
   `CatalogProduct.translations`; `lang` parametresi ürün listesi, tek ürün,
   taslak kaydı ve yeniden yazma kartında.
2. `catalog.ts`: `languagesOf`, `isMultilingual`, `directionOf`, `contentFor`,
   `langKey`, `mappableFor`; `previewDocument(html, lang)` kendi `dir`'ini yazar
   ve yerleşimi `padding-inline-start` / `text-align: start` ile yöne bağlar.
3. `Catalog.tsx`: dil sekmeleri (yalnız birden fazla dil varsa çizilir),
   dil değişiminde ürünlerin yeniden yüklenmesi, "şimdiki" değerin **o dilin**
   kendi hücresi olması, kart düğmesinin dili adlandırması, sütun formunda dil
   sekmesi.
4. `RichText.tsx`: `RichPreview` ve `RichEditor` `dir` alır. Yön **yazma
   yüzeyinde**, araç çubuğunda değil.

## Out of scope (do NOT do here)

- Uygulama arayüzünün kendisinin çevrilmesi. Belge Türkçe kalır; yön yalnızca
  düzenlenen içeriğe ait.

## Definition of Done

- [x] Tek dilli bir katalog bugünkü ekranla birebir aynı görünüyor
- [x] Arapça sekmesinde önizleme ve editör sağdan sola
- [x] Arapça sekmesinde "şimdiki", Türkçe gövde değil Arapça hücre
- [x] Sütun formu, profilin adlandırmadığı bir dil sütununu seçtirebiliyor
- [x] `npm run typecheck && npm test` yeşil

## Notes for the reviewer (Opus)

- `contentFor` hedef dilde kaynak dile **düşmüyor**: düşseydi "zaten çevrilmiş"
  ile "henüz çevrilmemiş" aynı görünürdü, ki operatörün sekmeyi açma sebebi tam
  olarak bu ayrım.
- Sütun formundaki dil listesi `view.languages` **değil**: o "bu dosya bugün ne
  taşıyor" sorusunu cevaplıyor, form ise bir tane daha taşımasını sağlamak için
  var. Yalnız çözülenleri sunmak, profilin adlandırmadığı her sütunu erişilmez
  yapardı.
- `useDraft` dil değişiminde de sıfırlanıyor: yarım kalmış bir Türkçe düzenleme
  operatörün peşinden Arapça sekmesine gitmemeli.
