# task-92 — Desktop: grafiğin kaydırmayı sahiplenmesi ve sarmayan metin

- **Status:** done
- **Owner agent:** Coder (Gemini)
- **Prerequisites:** task-52, task-84
- **Primary paths:** `desktop/src/screens/Brain.tsx`, `desktop/src/components/GraphConsole.tsx`
- **Roadmap bucket:** Brain — bilgi grafiği

## Context

Brain ekranında iki hata, ikisi de aynı cinsten: bir öğe kendi alanının dışına
taşıyor.

**Bir.** Grafiğin üstünde trackpad'le gezerken *hem* grafik kayıyor *hem* sayfa.
Tek el hareketi iki şeyi birden oynatıyor. Sebep `onWheel`'in JSX'te olması:
React `wheel`'i kök kapsayıcıya **pasif** olarak bağlar, dolayısıyla bir JSX
işleyicisi içindeki `preventDefault` yok sayılır. Kod tarayıcıya "bu hareket
benim" diyemiyordu.

**İki.** Düğüm panelindeki metin sarmıyor. Modelin bir dosya hakkında yazdığı
düzyazı düzenli olarak hiçbir satır sonu kuralının kendiliğinden bölemeyeceği
bir şey taşıyor — bir oturum URL'i, bir hash, bir import yolu. O en uzun şey
panelin genişliğini, panel de sayfanın genişliğini belirliyor ve pencereyi
sahiplenen bir yerleşimde yatay kaydırma çubuğu beliriyor. Yana doğru okumak
okumak değildir.

## Scope (do exactly this)

1. **`GraphCanvas` tekerleği native ve pasif olmayan bir dinleyiciyle alır.**
   `useEffect` içinde `addEventListener("wheel", …, { passive: false })`, ilk
   iş `preventDefault()`. Davranış değişmez — kaydırma kaydırır, ⌘/ctrl ve
   pinch yakınlaştırır; değişen tek şey hareketin artık **sahiplenilmesi**.
2. Canvas'a `touch-action: none` ve `overscroll-behavior: contain`; sarmalayan
   kutuya `overscroll-contain`. Dinleyici koşmadan önce tarayıcının hareketi
   bir üst kapsayıcıya vermesini engeller.
3. **`NodePanel`'in gövdesi `wrap-anywhere`.** Tek tek paragraflara değil
   gövdeye: oradaki her şey bir modelin yazdığı düzyazı ve hepsi aynı riski
   taşıyor. Ayrıca sürüm zaman çizelgesinin değerlendirme paragrafı ve
   `GraphConsole`'un not/hata satırları.
4. Tam ekran yerleşiminde panel sütununa `min-w-0` — kendisine verilen rayın
   dışına itilememesi için.

## Out of scope (do NOT do here)

- `internal/**`. Bu tamamen bir yerleşim ve olay hatası.
- Grafiğin kendi çizimi, düzeni ya da hit-test'i.
- Uygulama kabuğuna `overflow-x: hidden` koymak. O, taşmayı düzeltmez —
  görünmez yapar, ve bir sonraki taşma sessizce kırpılır.

## Interfaces / contracts

Yeni bir dışa açık yüzey yok. `wrap-anywhere` Tailwind 4.3'ün kendi utility'si
(`overflow-wrap: anywhere`), derlenmiş CSS'te doğrulandı.

## Definition of Done

- [x] Grafiğin üstünde trackpad kaydırması sayfayı oynatmıyor.
- [x] ⌘/ctrl + kaydırma ve pinch hâlâ yakınlaştırıyor.
- [x] Uzun bir URL taşıyan bir düğüm değerlendirmesi sarıyor; yatay kaydırma
      çubuğu yok.
- [x] `make desktop-check` yeşil.
- [x] Status `done` + changelog.

## Notes for the reviewer (Opus)

- Bu bir takas taşıyor: sayfa kipinde grafik `62vh` yüksekliğinde bir kart ve
  artık üstündeki kaydırmayı yutuyor. İmleç grafiğin üstündeyken sayfa
  kaydırılamaz. Haritalarda standart olan davranış bu, ve kullanıcının istediği
  de tam olarak "aynı anda çalışmasın"dı — ama bedeli yazılı olmalı.
- `overflow-x: hidden` ile "düzeltme" cazibesine dikkat: taşmanın kendisi
  düzeltilmeli.

## Changelog

- **Teker artık native ve pasif değil.** `onWheel` JSX'ten kalktı; `useEffect`
  içinde `{ passive: false }` ile bağlanan bir dinleyici ilk iş
  `preventDefault()` çağırıyor. Hatanın tamamı buydu: React `wheel`'i kök
  kapsayıcıya pasif bağlar, yani JSX işleyicisi içindeki bir `preventDefault`
  sessizce yok sayılır — grafik kayıyor **ve** sayfa altından kayıyordu, tek
  hareket iki şeyi birden oynatıyordu. Bir canvas bir kaydırmayı tükettiğini
  tarayıcıya söylemek zorunda, ve bunu yalnız pasif olmayan bir dinleyici
  söyleyebilir.
- Canvas'a `touch-action: none` + `overscroll-behavior: contain`, sarmalayana
  `overscroll-contain`.
- **`NodePanel`'in gövdesi `wrap-anywhere`.** Paragraf paragraf değil gövdeye,
  çünkü oradaki her şey bir modelin bir dosya hakkında yazdığı düzyazı ve hepsi
  aynı şeyi taşıyor: bir oturum URL'i, bir hash, bir import yolu — hiçbir satır
  sonu kuralının kendiliğinden bölmediği şeyler. Aynısı sürüm zaman
  çizelgesinin değerlendirmesine ve `GraphConsole`'un not/hata satırlarına.
- Tam ekran yerleşiminde panel sütununa `min-w-0`.

### Takas

Sayfa kipinde grafik `h-[clamp(26rem,62vh,44rem)]` bir kart ve artık üstündeki
kaydırmayı yutuyor: imleç grafiğin üstündeyken sayfa kaydırılamıyor.
Haritaların standart davranışı bu ve istenen de "aynı anda çalışmasın"dı, ama
bedeli bu. Alternatifi — kaydırmayı yalnız bir değiştirici tuşla sahiplenmek —
kullanıcının şikâyet ettiği şeyi geri getirirdi.

`make desktop-check: 0`
