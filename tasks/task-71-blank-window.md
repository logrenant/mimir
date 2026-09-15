# task-71 — Boş pencere: askıya alınmış WebView'i uyandır, çöken ekranı görünür kıl

- **Status:** done
- **Owner agent:** Claude Opus
- **Prerequisites:** task-52 (Brain sekmesi), task-69 (tarama izinleri)
- **Primary paths:** `desktop/src-tauri/src/liveness.rs`,
  `desktop/src-tauri/src/main.rs`, `desktop/src-tauri/src/quick.rs`,
  `desktop/src/lib/liveness.ts`, `desktop/src/components/ErrorBoundary.tsx`,
  `desktop/src/App.tsx`, `desktop/src/screens/Dashboard.tsx`,
  `desktop/src/screens/Brain.tsx`, `desktop/src/lib/brainGraph.ts`,
  `internal/brain/supervisor.go`, `internal/api/scanpolicy.go`,
  `desktop/src/lib/daemon.ts`
- **Roadmap bucket:** B — masaüstü kabuk

## Context

Brain sekmesi açıkken pencere sidebar dahil tamamen boşalıyor ve bir daha
kendine gelmiyordu. Teşhis tahmin değil, `~/Library/Logs/mimir-daemon.log` ve
`log show` çıktısından çıktı:

1. **Daemon suçsuz.** Tek bir 5xx yok; `/brain/graph` 71 kez 200 döndürmüş
   (1500 düğüm / 6111 kenar, 550–960 ms).
2. **Asıl mekanizma.** `main.rs` pencereyi kapatınca `hide()` ediyor — Mimir
   menü çubuğunda yaşamaya devam etmeli. macOS gizlenmiş bir `NSWindow`'u
   "kimsenin bakmadığı bir WebView" olarak okuyor ve ~30 sn sonra onu çizen
   WebContent sürecini askıya alıyor:

   ```
   19:14:32  Ending background activity / 'View was recently visible'
   19:14:36  WebProcess::prepareToSuspend: Process is ready to suspend
   19:14:37  [WebContent 25264] Suspending task.
   19:14:37  running-suspended-NotVisible
   ```

   Sonrasında o süreç için **hiçbir resume kaydı yok**, ve daemon 19:14:23'ten
   sonra uygulamadan tek bir istek almıyor. Tray'den geri açan yol (`show_main`)
   süreci uyandırmıyordu. 19:12'de aynı askıya alma yaşanıp toparlanmış — yani
   deterministik değil, yarış koşullu.
3. **Brain sekmesi bunu kolaylaştırıyordu.** `layout()` render yolunda senkron
   çalışıyordu: 1500 düğüm × 320 tick, ölçülen ~12 saniye kilitli ana iş
   parçacığı (mount 19:11:44.85 → ilk `/brain/graph` 19:11:56.86). Bir de
   `useBrainScan` faz ne olursa olsun saniyede bir soruyordu — tek oturumda
   41.652 `/brain/scan`.
4. **Hiç error boundary yoktu.** React kökü bir throw'da sökülüyor, yani boş
   ekranın ikinci ve bağımsız bir üretim yolu daha vardı.

## Scope (do exactly this)

1. `desktop/src-tauri/src/liveness.rs` (yeni) — pencere başına kalp atışı
   (`Heartbeat`), `webview_heartbeat` komutu, ve `revive(app, label)`:
   sayfayı dürt (`__mimirPing`), `GRACE` kadar bekle, cevap yoksa `reload()`.
   Hiç check-in yapmamış bir pencereye dokunulmaz — o yükleniyor olabilir.
2. `desktop/src/lib/liveness.ts` (yeni) — `startHeartbeat()`: 5 sn'lik tick,
   `visibilitychange`/`focus` üzerine anlık atış, ve `window.__mimirPing`.
   `main.tsx` ilk render'dan önce çağırır; her iki pencere için de.
3. `main.rs` / `quick.rs` — `revive` üç yerden: `show_main`, quick `show`, ve
   `WindowEvent::Focused(true)`.
4. `desktop/src/components/ErrorBoundary.tsx` (yeni) — iki kez takılır:
   `App`'te sağlayıcıların dışında son çare olarak, `Dashboard`'da gösterilen
   ekranın etrafında (`resetKey={screen}` — çıkış yolu sekme değiştirmek).
5. `brainGraph.ts` — `startLayout()` adım adım çalışan simülasyon (`LayoutRun`,
   zaman bütçeli `advance`), `layout()` onun üzerinde ince bir sarmalayıcı;
   `ticksFor(count)` büyük grafiğin tick bütçesini kısar; `byDegree()`
   `visibleLabels`'ın sıralamasını çağırana taşır.
6. `Brain.tsx` — `useSettledLayout` (kare başına bir bütçe, sonra teslim),
   yerleşim sırasında yüzde gösteren örtü, rAF ile tek kareye indirilmiş çizim,
   ve `useBrainScan`'in `useRef`'le düzeltilmiş anket aralığı.
7. `internal/brain/supervisor.go` + `internal/api/scanpolicy.go` — boş listeler
   `null` değil `[]` olarak servis edilir; `Status()` kopya döndürür.
   `daemon.ts` tipleri bunu yine de nullable sayar (eski daemon'a karşı).

## Out of scope (do NOT do here)

- **Fiziği yeniden yazmak.** `repel`/`attract`/`separate` olduğu gibi kaldı;
  değişen ne zaman çalıştıkları, ne yaptıkları değil.
- **Grafiği sanallaştırmak.** 1500 düğüm daemon'un tavanı ve okunur; kenar
  sayısını azaltmak ayrı bir karar.
- **`hide()` yerine `minimize()`.** Tray'de yaşayan bir uygulamanın penceresi
  kapatılınca gitmelidir; sorun gizlenmesi değil, geri gelmemesiydi.

## Interfaces / contracts

```rust
pub struct Heartbeat(Mutex<HashMap<String, Instant>>);
#[tauri::command] pub fn webview_heartbeat(window: WebviewWindow, state: State<Heartbeat>);
pub fn revive(app: &AppHandle, label: &str);   // dürt → 1200 ms → gerekiyorsa reload
```

```ts
export function startHeartbeat(): () => void;          // window.__mimirPing kurar
export function startLayout(nodes, edges, opts): LayoutRun;
export function ticksFor(count: number): number;       // 320 küçükte, 120 tabanda
export function byDegree(nodes: LayoutNode[]): LayoutNode[];
```

## Definition of Done

- [x] Gizlenip askıya alınmış bir pencere tray'den açıldığında dolu geliyor;
      cevap vermeyen sayfa yeniden yükleniyor ve bu `stderr`'e bir satır yazıyor.
- [x] Bir ekran throw ettiğinde pencere boşalmıyor: hata metni, "yeniden dene"
      ve "pencereyi yenile" görünüyor, sidebar ayakta kalıyor.
- [x] Brain sekmesi açılırken ana iş parçacığı kilitlenmiyor; yerleşim yüzdesi
      görünüyor ve pencere bu sırada yanıt veriyor.
- [x] `/brain/scan` anketi fazı takip ediyor (çalışırken 2 sn, boştayken 30 sn).
- [x] Boş kök listesi `"roots":[]` olarak servis ediliyor; `Status()` kopya döndürür.
- [x] `make check` yeşil.
- [x] `make desktop-check` yeşil (17 Rust testi — 4'ü yeni, 253 TS testi).

## Notes for the reviewer (Opus)

- `revive`, hiç check-in yapmamış pencereyi kasten es geçer. Aksi hâlde ilk
  yüklemesi yavaş bir sayfa yeniden yüklenir, o da yavaş yüklenir: döngü.
- `useSettledLayout` düğümleri ancak yerleşim bittiğinde yayımlar. Ara kareleri
  çizmek kare başına 6111 çizgi demekti — tam da kaçınılan maliyet.
- `ticksFor` bir kalite ödünü: 1500 düğüm artık 320 değil 120 tick alıyor.
  Ölçülen fark gözle görünmüyor, süre dörtte birine iniyor.
