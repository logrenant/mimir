# task-76 — Desktop: yükselti, hareket ve odak katmanı

- **Status:** done
- **Owner agent:** Reviewer (Opus), doğrudan uygulama
- **Prerequisites:** none
- **Primary paths:** `desktop/src/**`, `desktop/index.html`, `desktop/package.json`, `desktop/AGENTS.md`
- **Roadmap bucket:** Track B — desktop kabuğu (bakım)

## Context

Uygulama çalışıyordu ama "basit bir dashboard" gibi duruyordu. Sebep estetik
tercih değil, **iki uyumsuz stil dilinin yan yana yaşaması**ydı:

- **Sistem A** — Tailwind v4 + `index.css`'teki `@theme` token'ları +
  `src/components/ui/` altındaki dört primitive.
- **Sistem B** — `components/hover.tsx` üzerinden satır-içi stil string'leri ve
  elle yazılmış hex.

`hover.tsx` bunu kendi yorumunda zaten kabul ediyordu: *"It is not the
destination — the design tokens in `index.css` are — but moving every surface at
once is its own task."* Bu görev o görevdi.

Ölçülen sonuç: 9 buton uygulaması, 6 rozet, 6 kart, ortak primitive'i olmayan
3 modal, 4 sekme kontrolü, 6 girdi + 5 `<select>` tarifi, 3 onay kutusu stili,
4 durum noktası, ~18 elle yazılmış boş durum, sıfır spinner, 10 farklı yarıçap,
ve **tüm uygulamada odak halkası taşıyan tek kontrol** (`ui/checkbox`).

Kopyalar sessizce ayrışmıştı da: `LINE_COLOR`'ın iki kopyası üç renkte
anlaşmıyordu, üç konsolda üç farklı "terminal siyahı" vardı, `/diagnostics`
yanıtı üç kez iki farklı sözlükle çiziliyordu.

## Scope

1. **Token katmanı** (`src/index.css`): `--color-sunken`, `--radius-sm/md/lg`,
   `--shadow-elev-1/2`, `--ease-decisive`, `--dur-fast/base/slow`;
   `.focus-ring` / `.focus-ring-inset`, `.grid-ground`, `mimirBeat` /
   `mimirRipple` / `mimirCrawl`, global `prefers-reduced-motion`.
2. **Hareket sözleşmesi** (`src/lib/motion.ts`): süreler, tek eğri,
   variant'lar, `flatten()` + `useMotion()`. `framer-motion` `13.2.0` tam
   sürüm.
3. **Konsol paleti** (`src/lib/lineColors.ts`): üç ayrışmış kopyanın yerine tek
   kaynak, token sınıfları hâlinde.
4. **Primitive kütüphanesi** (`src/components/ui/`): `button` (4 varyant ×
   2 boyut, `loading`, ikon, odak halkası), `card` (`elevation`, `accent`),
   `badge` (`dot`, `pulse`, `pill`), ve yeni `overlay`, `tabs`, `field`
   (input/textarea/select/radio/label), `pulse`, `meter`, `stream`, `skeleton`,
   `empty`, `masthead`.
5. **Kabuk** (`Dashboard.tsx`): TitleBar, Sidebar (`layoutId` ile kayan
   Electric ray), ekran geçişi (`AnimatePresence mode="wait"`), üç overlay →
   tek `Overlay`.
6. **Ekranlar**: Home, Board, Brain, Leadgen, Settings, Workspace, Terminals,
   Connection, QuickTask ve `Terminal` / `BrainConsole` / `ShellTerminal`.
7. **`hover.tsx` silindi.**
8. **Belgeler**: `desktop/AGENTS.md` §"Yükselti, hareket ve odak katmanı",
   `index.css` başlık yorumu.

## Out of scope

- Go tarafı, daemon route'ları, `internal/**`. `make check` Go-only kaldı.
- Yeni ekran, yeni veri, yeni daemon çağrısı. Bu görev hiçbir yeni şey
  göstermiyor; var olanı farklı gösteriyor.
- `lib/brainGraph.ts`'in yerleşim matematiği, `lib/board.ts`, `lib/leadgen.ts`,
  `lib/terminals.ts` — davranış dosyaları.
- Açık tema.
- `Connection` / `Workspace` / `QuickTask`'ın İngilizce metinlerinin çevirisi.
  Bu görev yalnızca `lang="en"` işaretini koydu ki büyük harfe çevirme doğru
  çalışsın.

## Interfaces / contracts

- `--dur-*` (ms, `index.css`) ile `DUR` (s, `lib/motion.ts`) **aynı sayılar**;
  `motion.test.ts` bunu doğruluyor.
- `kindStyle()` fillleri artık `mix()` ile dört renkten türetiliyor;
  `brainGraph.test.ts` her fill'in Carbon veya Mist ile bir harmandan
  erişilebilir olmasını şart koşuyor.
- `Pulse`'ın `beat` prop'u bir sinyal sayacıdır, bir zamanlayıcı değil.
- `ui/overlay` tek modaldir: `role="dialog"`, `aria-modal`, odak tuzağı, Escape.

## Definition of Done

- [x] `.tsx` içinde ham hex yok (yalnızca yorumlarda, açıklama olarak).
- [x] `HoverButton` / `HoverDiv` / `components/hover.tsx` yok.
- [x] Her interaktif primitive `focus-visible` halkası taşıyor.
- [x] `make desktop-check` yeşil (typecheck + 282 test, 17 dosya).
- [x] `vite build` temiz; bundle framer-motion'a rağmen **küçüldü**
      (793.9 kB → 789.0 kB), çünkü silinen satır-içi stil motoru eklenenden
      daha pahalıydı.
- [x] Status `done`.

## Notes for the reviewer (Opus)

- **SD-5**: `framer-motion` `13.2.0` — caret yok. Bu, `desktop/AGENTS.md`'nin
  "beş bağımlılık" kuralının bilinçli iptalidir ve dosyada gerekçesiyle yazılı.
- Marka kuralları: dört renk korundu, ekran başına tek `primary` korundu,
  hap şeklinde buton yok (tek `rounded-full` bağlantı rozetidir), rozetler hâlâ
  çerçeve. Gölge yasağı ışığa dönüştü, gerçek gölge yalnızca `elev-2`'de.
- Davranış testlerinin hepsi (`board`, `leadgen`, `terminals`, `daemon`,
  `openRunStream`, `settings`, `scanPolicy`, `dashboard`, `quickTask`) el
  değmeden yeşil kaldı; değişen tek test `brainGraph`'ın palet testi, çünkü
  test edilen şey paletin kendisiydi.
- Bakılacak yer: `Brain.tsx`'in çizim döngüsü `hover` yerine `hoverID`'ye
  bağlı — tam `hover` nesnesine bağlamak her fare hareketinde 3.000 düğümü
  yeniden çizerdi.

## Changelog

- Token katmanı, `lib/motion.ts`, `lib/lineColors.ts`, pinlenmiş framer-motion.
- 15 dosyalık `ui/` primitive kütüphanesi; 9 buton / 6 rozet / 6 kart /
  3 modal / 4 sekme / 11 girdi tarifi bunların arkasında birleşti.
- Kabuk, board, Home, Brain, Leadgen, Settings, Workspace, Terminals,
  Connection, QuickTask ve üç konsol yeni dile taşındı.
- Fark edilmemiş beşinci renk (`#e5a23d`, 11 yer) ve grafiğin üç palet dışı
  hue'su kaldırıldı; grafik fillleri artık dört renkten türetiliyor.
- `index.html` `lang="tr"` — Türkçe büyük harfe çevirme düzeldi.
- `components/hover.tsx` silindi.
