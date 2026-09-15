# task-114 — Katalog: içe aktarımlar arası çıktı listesi

- **Status:** done
- **Owner agent:** Coder
- **Prerequisites:** task-113
- **Primary paths:** `internal/store/catalog.go`, `internal/catalog/{studio,stored,ops}.go`, `internal/api/**`
- **Roadmap bucket:** Katalog — ürün içeriği stüdyosu

## Context

Üretilmiş içerik yalnızca tek ürün tezgâhında, geldiği dosyanın ekranında
okunabiliyordu. "Arapçada beni ne bekliyor" ve "çeviri export'ları gerçekten
yazıldı mı" soruları dosyaları tek tek açıp saymakla cevaplanıyordu — ve dosya
profiline göre süzmek, profil dosyanın özelliği olduğu için ancak hepsinin
üstünden anlam kazanıyor.

Zor kısım: bir taslak **tek** bir (içe aktarım, dil) çifti için güncel ve sürüm
dizesi satırdan türetilemiyor. `catalog_drafts`'ta `import_id` yok ve olmamalı —
satırın hangi içe aktarıma ait olduğunun otoritesi `catalog_products` ve
`productID` zaten import id'sini hash'liyor.

## Scope (do exactly this)

1. `catalog.DraftKey{ImportID, Version, Lang}` + `DraftFilter{Keys, Status, Limit, Offset}`
   + `OutputFilter{Langs, Dialect, Status, Limit, Offset}` + `OutputPage` + `DraftRow`.
2. `Store.ListCatalogDrafts`: anahtarlar satır içi bir tablo olarak join edilir;
   `AND p.import_id = k.import_id` **çift** kısıtı; toplam sıralama;
   `LIMIT ?+1` ile `has_more`.
3. `Studio.Outputs`: tam iki depo okuması. Sürüm bestelemesi bellekte saf Go.
   Profil süzgeci import pasosunda, anahtarlar bestelenmeden önce.
   `Changed` burada hesaplanır (`changedNames`, taslağın kendi dilindeki hücreye
   karşı). Boş sayfa **boş dilim**, asla nil.
4. `OutputFilter.Langs` boş = her dil (kaynak dil değil).
5. `GET /catalog/outputs?lang=&dialect=&status=&limit=&offset=`.
6. `config.CatalogOutputsImportMax` (sabit, knob değil).

## Out of scope (do NOT do here)

- MCP aracı. Bu bir ekran, bir oturumun sorduğu soru değil.
- `total`. İkinci bir `COUNT` aynı join üzerinde, kimsenin karar vermediği bir
  sayı için. Tam sayfa kendini sayar, kesik sayfa "şu kadar ve dahası" der.
- "changed" üzerinden süzme. `LIMIT`'ten sonra hesaplanan bir yüklem sessizce
  kısa sayfa döndürür.

## Definition of Done

- [x] Bayat marka altındaki taslak listeye girmiyor (çift kısıtı testle çivili)
- [x] Satır, taslağın kendi dilinin durumunu taşıyor
- [x] Sayfalama toplam sıralama üzerinde: tekrar yok, düşen yok
- [x] Boş sayfa nil değil (tel üzerinde `null` ekranı düşürüyordu)
- [x] Import başına ürün okuması yok — sayan store ile çivili
- [x] Bilinmeyen dil ve bilinmeyen durum 400
- [x] `make check` yeşil

## Notes for the reviewer (Opus)

- Sürüm neden depoda bestelenmiyor: bir sürüm bu paketin önbellek anahtarı ve
  formatın ikinci bir uygulaması bu depoda bir kez zaten sürüklendi.
- Profil süzgeci neden `catalog_imports.dialect` üzerinden güvenli: bir taslak
  ancak dosyanın okunduğu profil altında var olabilir — ürünler yalnızca
  `Studio.save`'den yazılıyor ve o aynı çağrıda import satırını da yazıyor.
