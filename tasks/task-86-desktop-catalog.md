# task-86 — Desktop: Katalog ekranı, önizleme ve marka sözlüğüne kısıtlı editör

- **Status:** done
- **Owner agent:** Coder
- **Prerequisites:** task-85 (katalog çekirdeği)
- **Primary paths:** `desktop/src/screens/Catalog.tsx` (yeni),
  `desktop/src/lib/catalog.ts` + `catalog.test.ts` (yeni),
  `desktop/src/components/RichText.tsx` (yeni), `desktop/src/lib/daemon.ts`,
  `desktop/src/lib/modules.ts`, `desktop/src/screens/Dashboard.tsx`,
  `desktop/package.json`, `desktop/src-tauri/tauri.conf.json`
- **Roadmap bucket:** B.11 — katalog / ürün içeriği

## Context

task-85 katalogu daemon'a koydu ama görünür bir yüzü yok. Bu task o yüzü yapar:
CSV'yi içe aktarma, ürün tablosu, ve **operatörün son çıktıyı görebilmesi** —
eski ve yeni içerik yan yana, markanın stilleriyle render edilmiş.

Ekran task-87'den önce de anlamlıdır: içe aktarma, marka kitinin ne çıkardığını
görme, ve hiçbir şey değiştirmeden dışa aktarıp `diff`in boş olduğunu görme
kayıpsızlık sözleşmesinin operatör tarafından doğrulanmasıdır.

## Scope (do exactly this)

1. **`desktop/src/lib/daemon.ts`** — tel tipleri (`CatalogImport`,
   `CatalogProduct`, `CatalogDraft`, `BrandKit`, `Vocabulary`) ve `api.*`
   metotları. Başka hiçbir modül `daemon_request` çağırmaz.
2. **`desktop/src/lib/catalog.ts` + testleri** — saf mantık, JSX'in dışında:
   durum kovaları, kategori rayı sayımları, SEO uzunluk doğrulaması (uyarı
   eşikleri dahil), eski/yeni diff özeti, seçim kümesi.
3. **`desktop/src/lib/modules.ts`** — `ModuleDef["key"]` birleşimine `"catalog"`
   ve bir girdi. `Dashboard.tsx`'in `ModuleScreen`'ine tek satır.
4. **`desktop/src/screens/Catalog.tsx`**:
   - **İçe aktarma paneli** — CSV sürükle → base64 → `POST /catalog/imports`.
     Algılanan lehçe, ürün sayısı, kodlama ve ayraç görünür. Lehçe
     algılanmadıysa sütun eşleme formu (`Field` → başlık).
   - **Marka paneli** — çıkarılan sözlük (etiketler, sınıflar) salt-okunur;
     ses profili (hitap, ton, yasaklı kalıplar) düzenlenebilir. Kaydetme
     `PUT /catalog/imports/{id}/brand`.
   - **Ürün tablosu** — `ui/checkbox` ile çoklu seçim, leadgen'in `CategoryRail`
     şekli, durum sütunu. "Seçilenleri yeniden yaz" düğmesi task-87 gelene kadar
     devre dışı ve nedenini söyler.
   - **Detay, üç sekme**: *Önizleme* (eski/yeni yan yana), *Düzenle* (zengin
     metin), *Alanlar* (SEO başlık/açıklama/etiket, canlı karakter sayacı).
   - **Dışa aktar** → `POST /catalog/imports/{id}/export`, sonra
     `src-tauri/src/exports.rs`'in kapsamlı reveal'i.
5. **`desktop/src/components/RichText.tsx`** — iki bileşen:
   - `<RichPreview html>` — `sandbox` özellikli `<iframe srcdoc>`. Script
     çalışmaz: iframe ebeveynin CSP'sini devralır **ve** `sandbox`'ta
     `allow-scripts` yoktur.
   - `<RichEditor html vocab onChange>` — TipTap, şeması marka `Vocabulary`'sinden
     kurulur. StarterKit sözlükteki node/mark'lara indirilir.
6. **`desktop/package.json`** — `@tiptap/react`, `@tiptap/core`, `@tiptap/pm`,
   `@tiptap/starter-kit`, dördü de **tam sürüme pinlenmiş** (SD-5, `^`/`~` yok).
7. **`desktop/src-tauri/tauri.conf.json`** — `img-src 'self' data:` →
   `'self' data: https:`, ki ürün fotoğrafları önizlemede görünsün.
   **`script-src 'self'` değişmez.**

## Out of scope (do NOT do here)

- `internal/**` ya da `cmd/**` altında hiçbir dosya.
- Bulk kartın gövdesi, ilerleme göstergesi, park/sürdürme rozeti — task-88.
- Yeni bir `ui/` primitifi icat etmek: varsa genişlet, yoksa `ui/`'ye ekle,
  ekrana gömme (task-76).

## Interfaces / contracts

```ts
export interface CatalogImport {
  id: string; filename: string; dialect: string; delimiter: string;
  encoding: string; has_bom: boolean; product_count: number;
  mapping: Record<string, string> | null; created_at: string;
}
export interface CatalogProduct {
  id: string; import_id: string; handle: string; sku: string;
  title: string; category: string; status: CatalogStatus;
  original: CatalogFields; draft: CatalogFields | null;
}
export type CatalogStatus =
  "pending" | "researched" | "drafted" | "approved" | "rejected" | "failed";
```

## Definition of Done

- [x] `lib/catalog.test.ts` — 33 test: durum kovaları, SEO uzunlukları (kod
      noktası sayımıyla), ray sayımları, diff özeti, seçim kümesi, çerçeve
      özeti, ve bellek içi süzme.
- [x] `starterKitOptions` sözlük dışında hiçbir node teklif etmiyor; kod, kural,
      üstü çizili ve altı çizili mağaza kullanıyor olsa bile hiç sunulmuyor.
- [x] `PREVIEW_SANDBOX` boş dize ve `allow-scripts` içermiyor — testi var.
- [x] Arayüz Türkçe; dört renk; `.focus-ring`; boş durum `ui/empty`.
- [x] Dört TipTap paketi de `package.json`'da tam sürüme pinli (`3.31.3`).
- [x] `make desktop-check` yeşil.
- [x] Elle, gerçek daemon'a karşı: IKAS export'u içe aktarılıyor, dokunulmadan
      dışa aktarılıyor ve `cmp` bayt eşitliği veriyor.

## Notes for the reviewer (Opus)

- CSP genişlemesi yalnız `img-src` mi? `script-src` ya da `connect-src`'ye
  dokunulduysa `CHANGES REQUIRED`.
- Mantık gerçekten `lib/catalog.ts`'de mi, yoksa JSX'in içinde mi?
  (`desktop/AGENTS.md`)
- Editörün şeması marka sözlüğünden mi kuruluyor, yoksa sabit bir StarterKit mi?
  Sabitse bağımsızlığın argümanı çürür ve bağımlılık gerekçesiz kalır.
- TipTap sürümleri `^` taşıyor mu? Taşıyorsa SD-5.

---

## Changelog — 2026-09-06

**Katalog ekranı.** İçe aktarma, marka paneli, sütun eşleme, durum ve kategori
rayı, ürün tablosu, ve üç sekmeli detay (önizleme · düzenle · alanlar). Mantık
`lib/catalog.ts`'te ve testli; ekran onu çiziyor.

**Yedinci bağımlılık — ve gerekçesi "bir editör lazımdı" değil.** TipTap dört
paket olarak geldi, dördü de `3.31.3`'e pinli. Argüman şu: **şemaya dayalı bir
editör, daemon'ın süzeceği bir etiketi üretemeyecek olan tek editör türüdür.**
Bir `contenteditable` üretebilir. Şema markanın sözlüğünden kuruluyor ve sözlükte
olmayan her şey gizlenmiyor **kapatılıyor** — yalnızca düğmesi olmayan bir uzantı
klavye kısayoluyla ve yapıştırmayla hâlâ ulaşılabilir olurdu.

**Görsel node'u yirmi satır, beşinci paket değil.** Ve `@tiptap/extension-image`
olsaydı varacağımızdan daha iyi bir yere varıyor: node atomik ve düzenlenemez,
yani ürün fotoğrafı birebir korunuyor ve `src`'si yeniden yazılamıyor. Uydurulmuş
bir `src` zaten sunucuda düşüyor; onu yazdıran bir editör çalışıyormuş gibi
görünen bir editör olurdu.

**Ölçülen maliyet ve ona verilen cevap.** Editör paketi gzip'li **+130 kB**
büyüttü (227 → 357 kB) — paketin yarısından fazlası kadar. `React.lazy` ile
Katalog ayrı bir parçaya taşındı: başlangıç paketi 227 kB'de kaldı ve 130 kB'lik
parça yalnız ekran açılınca yükleniyor. Uygulamada tembel yüklenen tek ekran bu;
diğerleri bölünse bir spinner'dan başka bir şey kazandırmazdı.

**Bellek içi süzme — ledger'ın hatasının tekrarı değil.** İlk yazımda süzme
daemon'daydı ve ray sayımları süzülmüş listeden geliyordu: "onaylandı"ya
tıklayınca diğer bütün satırlar 0 okuyordu. Lead ledger'ı sınırsız olduğu için
daemon'ın süzmesi gerekiyor (task-64); bir katalog import'u **yükleme anında
sayılmış tek bir dosya**, ve rayın süzgeci değil kataloğu sayması gerekiyor.
Bir sayfa import'un tamamı değilse `pageCoversImport` bunu söylüyor.

**CSP'de tek değişiklik `img-src`'ye `https:`.** `script-src`, `connect-src` ve
`default-src` değişmedi. Önizleme `sandbox`'lı bir `srcdoc` iframe ve `srcdoc`
ebeveynin CSP'sini devraldığı için script iki kere birden imkânsız.

## task-85'te bulunan ve burada düzeltilen dört kusur

Ekranı gerçek daemon'a karşı sürerken çıktılar. Numara sınırı iki ajanın
çakışmasını önlemek için var; bilinen bir kusuru numara yüzünden bırakmak yanlış
olurdu, o yüzden düzeltildiler ve burada kayda geçtiler.

1. **İçe aktarma 4 dakika bloke oluyordu.** Ses distil'i `RefineTimeout`'u
   (180 sn) devralıyordu — arka planda bir sayfa damıtmak için ölçülmüş bir
   bütçe. `CatalogVoiceTimeout` (45 sn) eklendi; süre dolunca sözlük ve import
   ayakta kalıyor ve sebep panelde yazıyor.
   Test: `TestDeriveBrand_GivesUpOnASlowProviderAndStillReturnsAnImport`.
2. **Operatörün yazdığı bağlantı düşüyordu.** "Kaynakta olmayan URL bir URL
   değildir" kuralı bir modelin uydurmasına karşıydı; insana uygulanınca
   editörün bağlantı düğmesi sessizce hiçbir şey yapmayan bir düğme oluyordu.
   `urlPolicy` ile iki yol ayrıldı. Testler:
   `TestSaveDraft_KeepsALinkTheOperatorTypedThemselves` ve
   `TestSanitize_UnderTheSourcePolicyRefusesAURLTheProductNeverHad` — ikincisi
   task-87'nin gevşetmemesi gereken sözleşmeyi kullanıcısı gelmeden sabitliyor.
3. **`<script>` gövdesi metin olarak sızıyordu.** Etiket düşüyordu ama `alert(1)`
   ürün sayfasına görünür bir cümle olarak yazılıyordu. `opaqueTags` eklendi.
   Test: `TestParseHTML_DoesNotTreatScriptOrStyleContentAsProse`.
4. **Operatör kendi taslağını ikinci kez kaydedemiyordu.** `edited_by_operator`
   koruması makineyi durdurmalıydı, insanı değil; ikinci kayıt açıklanamayan bir
   500 dönüyordu. SQL koşuluna `OR excluded.edited_by_operator = 1` eklendi ve
   sentinel `catalog.ErrDraftLocked` olarak 409'a eşlendi.
   Test: `TestPutCatalogDraft_LetsTheOperatorEditTheirOwnDraftAgain`.

## Uçtan uca doğrulama (gerçek daemon, gerçek IKAS export'u)

BOM'lu, noktalı virgüllü, her alanı tırnaklı 883 baytlık bir dosyayla:

- lehçe `ikas`, çerçeve `; · utf-8 · BOM · LF`, 3 ürün;
- sözlük model olmadan çıktı: `div p li ul a b h2 img` + `class="rte"`;
- dokunulmadan dışa aktarım **`cmp` ile bayt bayt aynı** (883 → 883);
- düşmanca bir taslak (`<script>`, `onclick`, `<section>`, `<h1>`, 78 karakterlik
  SEO başlık) kaydedildi: script gövdesi dahil hepsi süzüldü, markanın
  `h2`/`b`/`ul`/`rte` yapısı korundu, SEO başlık 59 karaktere indi, ve neyin
  sadeleştirildiği notlarda döndü;
- geçersiz durum 400; onaylanan ürün dışa aktarıldı ve **`diff` tam olarak bir
  satır** gösterdi — dokunulmamış iki ürünün her hücresi birebir aynı.

`make check` ve `make desktop-check` yeşil.
