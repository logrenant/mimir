# task-115 — Masaüstü: eksik dil yüzeyleri, Çıktılar sekmesi ve tarayıcı harness'ı

- **Status:** done
- **Owner agent:** Coder
- **Prerequisites:** task-113, task-114
- **Primary paths:** `desktop/src/lib/{daemon,catalog,outputs}.ts`, `desktop/src/screens/catalog/**`, `desktop/harness/**`
- **Roadmap bucket:** Katalog — ürün içeriği stüdyosu

## Context

Motor çok dilliydi, ekran değildi. Somut olarak: ürün tablosu Arapça sekmesinde
Türkçe başlığı yazıyordu ve arama da Türkçe başlıkta arıyordu; tezgâhın başlığı
aynı şekilde; `Setup.tsx`'in "bu build'in yazabildiği her dil" diye belgelenmiş
dil listesi aslında yalnızca dosyanın **zaten çözdüğü** dilleri sunuyordu, yani
mağazanın kendi açtığı `Html:Detay-EN` sütunu hiçbir zaman eşlenemiyordu; dil
şeridi tek dilli dosyada hiç çizilmiyordu, yani operatör modülün başka bir dil
yazabildiğini hiç öğrenmiyordu; ve onay/ret çağrıları dil taşımıyordu.

Ayrıca hiçbir görsel iddia ölçülemiyordu: uygulama tarayıcıda açılmıyor
(`main.tsx` modül seviyesinde `getCurrentWindow()` çağırıyor, her REST çağrısı
Rust `daemon_request` üzerinden gidiyor), yani `design-review` inceleyecek bir
sayfa bulamıyordu ve her bulgu bilgili tahmin olarak kalıyordu.

## Scope (do exactly this)

1. `daemon.ts`: `CatalogProduct.{lang,source_status}`,
   `CatalogImport.counts_by_lang`, `CatalogImportView.{writable_languages,target_lang}`,
   `setCatalogStatus(…, lang)`, `catalogOutputs(filter)`, `CatalogOutput`/`CatalogOutputPage`.
2. `catalog.ts`: `langLabel` (tek sözlük; `shared.tsx`'teki ikinci kopya kalktı),
   `framingFacts` (orta noktalı dizenin yerine), `statusIsDecisive`.
3. `outputs.ts` + `outputs.test.ts`: `filterOutputs`, `langChoices`,
   `dialectChoices`, `langDir`, `outputChangedSummary`, `outputCount`.
4. `Products.tsx` / `Bench.tsx`: başlık, arama ve durum o dilin; kaynak dilin
   durumu yanında sessiz ikinci işaret olarak.
5. `Setup.tsx`: `mappableLangs` artık `writable_languages`; framing olguları
   etiketli satır olarak buraya indi.
6. `shared.tsx`: `decide` dil taşıyor; `LanguageBar` tek dilli dosyada da
   çiziliyor ve çıkış yolunu gösteriyor; `DraftActions`'ta onay birincil.
7. `index.tsx`: modül seviyesinde `ui/tabs` (Kataloglar / Çıktılar);
   `Outputs.tsx` yeni ekran.
8. `desktop/harness/**` + `vite.harness.config.ts` + `npm run harness`: dev'e ait
   ikinci bir Vite yapılandırması, `@tauri-apps/api` alias shim'i ve `/daemon`
   proxy'si. Üretim paketi, `vite.config.ts` ve `src-tauri` değişmiyor.

## Out of scope (do NOT do here)

- Uygulama arayüzünün çevrilmesi. Belge Türkçe kalır.
- Tip ölçeğini modül çapında yeniden ölçeklemek. 11px bu uygulamanın her
  ekranında yoğun kullanılıyor (bileşenlerde 51, Dashboard'da 21, Lead-gen'de
  20 kez); yalnız katalogda değiştirmek, düzeltmesi beklenen tutarsızlığı
  yaratırdı.

## Definition of Done

- [x] Arapça sekmesinde satır, arama ve tezgâh başlığı Arapça ve sağdan sola
- [x] Onay o dile yazılıyor; satır kaynak dilin durumunu da söylüyor
- [x] Eşleme formu dosyanın taşımadığı dili de sunuyor
- [x] Tek dilli dosyada dil şeridi sebebini ve çıkışını söylüyor
- [x] Çıktılar sekmesi dile, profile ve duruma göre süzüyor; satır tıklaması
      dosyayı o dilde ve o üründe açıyor
- [x] `npm run harness` gerçek daemon'a karşı tarayıcıda açılıyor; token
      tarayıcıya girmiyor (proxy Node tarafında)
- [x] `npm run typecheck && npm test` yeşil

## Notes for the reviewer (Opus)

- Harness neden ayrı bir giriş noktası değil: Vite'ın `root`'unu `harness/`'a
  almak Tailwind v4'ün kaynak taramasını da oraya aldı ve `src/` altındaki her
  utility üretilmeden kaldı — sayfa tek bir 1670px logo olarak render edildi.
  Alias'larla uygulamanın kendi `index.html`'i ve `main.tsx`'i olduğu gibi
  çalışıyor.
- Proxy neden shim'de `fetch` değil: daemon bilerek hiçbir CORS başlığı
  göndermiyor (`lib/daemon.ts`'in kendi gerekçesi), yani tarayıcıdan doğrudan
  `fetch` preflight'ta 401 alır. Node tarafındaki proxy hem bunu çözüyor hem de
  token'ı tarayıcının dışında tutuyor — gerçek uygulamanın sahip olduğu özellik.
- `Picker`'ın `width` prop'u menüyü boyutlandırıyor, tetikleyiciyi değil;
  genişlik sarmalayıcıda. Ölçümden önce iki süzgeç satırı kaplıyordu.
