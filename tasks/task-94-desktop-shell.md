# task-94 — Desktop: menü çubuğu paneli, açılışta ekran yok, ana ekranda hesap yok

- **Status:** done
- **Owner agent:** Coder (Gemini)
- **Prerequisites:** task-76, task-86
- **Primary paths:** `desktop/**`
- **Roadmap bucket:** Desktop kabuğu

## Context

Dört şikâyet, üçü aynı yere çıkıyor: uygulamanın **kabuğu** — pencere olmayan
yüzeyler — kendi tasarım dilinde değil.

1. **Açılış ekranı gereksiz.** El sıkışma 200 ms sürüyor ve karşılığında bir
   poster, dört bağımlılık satırı ve basılacak bir düğme var. Ekranın var olma
   sebebi hâlâ geçerli — *"çalışmayan bir taşımanın üstüne proje seçici çizen
   bir pencere yalan söyler"* — ama bu sebep yalnızca el sıkışma **gecikince ya
   da başarısız olunca** geçerli. Başarılı ve hızlı bir el sıkışmanın
   gösterilecek bir şeyi yok.
2. **Ana ekranda hesap paneli gereksiz.** Aynı panel `Workspace`'te de var, ve
   Mimir'in tek bir Claude hesabı var — ana ekranda sürekli duran bir "Hesap"
   kutusu, hiç değişmeyen bir bilgiyi en değerli yere koyuyor.
3. **⌘⇧G ekranın ortasında bir pop-up açıyor.** Tasarımı iyi, yaklaşımı değil:
   menü çubuğunda yaşayan bir uygulamanın hızlı yakalama yüzeyi ekranın
   ortasında belirmez — **ikonunun altında** belirir. Ortada beliren bir kutu,
   arkasındaki her şeyi bağlamdan çıkarır.
4. **Tray menüsü native `NSMenu`.** Sistem grisi, sistem tipografisi, sistem
   ayırıcıları — bu uygulamanın kendi dilinden hiçbir şey taşımıyor ve
   taşıyamaz da, çünkü `NSMenu` biçimlendirilemez.

3 ile 4 aynı cevabı istiyor: **menü çubuğu ikonuna tutturulmuş tek bir panel**,
hem menü hem hızlı görev bestecisi. Bir yerine iki yüzey yok, ortada beliren
kutu yok, sistem menüsü yok.

## Scope (do exactly this)

1. **El sıkışma sessiz.** `App` üç durumu ayırır:
   - hazır (hızlı) → hiçbir şey gösterilmez, doğrudan Dashboard;
   - `starting` **ve** eşikten uzun sürüyor → yalnız wordmark ve tek satır;
   - `failed` → bugünkü dürüst yüzey, teşhisler ve yeniden dene ile.
   Eşik `desktop/src/lib/shell.ts`'de bir sabit, JSX'te bir sayı değil.
2. **`Home`'dan "Hesap" paneli kalkar.** `AccountPanel` `Workspace`'te kalır.
   `accountLoad`/`Meter` kullanımı da gider; ölü kalan importlar temizlenir.
3. **`quick` penceresi menü çubuğu ikonuna tutturulur.** `TrayIconEvent::Click`
   ikonun `rect`'ini veriyor; pencere onun altına, sağ kenarı hizalı
   konumlanır ve ekranın dışına taşmaz. `center()` çağrısı gider.
4. **Panel Mimir'in dilinde çizilir** (`desktop/src/screens/TrayPanel.tsx`,
   `QuickTask`'ın yerine):
   - üstte tek satır durum — mark, `daemon ok · v2.5.0`, ve canlı iş sayısı;
   - altında **besteci**: tek satır giriş, klasör ve model çipleri, `⏎`;
   - altında **kuyruk/çalışan** özeti (varsa) ve modüllere üç kısayol;
   - en altta `Mimir'i aç · Daemon'ı yeniden başlat · Girişte başlat · Çıkış`.
   Ölçüler ramp'tan: `radius-lg`, `shadow-elev-3`, `text-*` adımları, dört renk.
   Sihirli sayı yok.
5. **Köşeler yuvarlanır.** Pencere saydam olur (`transparent: true`) ve köşeyi
   CSS çizer. Bu, `app.macOSPrivateApi` bayrağını ve `tauri`'nin
   `macos-private-api` özelliğini açmayı gerektirir.
6. **Native menü bir cankurtaran olarak kalır, sağ tıkta.** Yalnız iki satır:
   `Restart daemon`, `Quit Mimir`. Gerekçe: uygulama accessory — Dock ikonu yok
   — ve panelin WebView'ı bozulursa native menü olmadan uygulamayı **kapatmanın
   yolu kalmaz**. `menuOnLeftClick: false`.

## Out of scope (do NOT do here)

- `internal/**`. Hiçbir rota, hiçbir sabit değişmez.
- **`NewTaskOverlay`** (uygulama içindeki "Yeni task"). Şikâyet menü çubuğu
  yüzeyineydi; uygulamanın *içinde*, bir kartı yazmak için açılan bir diyalog
  karar ağacının doğru cevabı. Değişecekse kendi task'ıyla değişir.
- Global kısayolun kendisi (⌘⇧G). Aynı pencereyi açar, yalnız yeri değişir.
- Hesap yönetimini kaldırmak. Panel `Workspace`'te duruyor.

## Interfaces / contracts

```ts
// desktop/src/lib/shell.ts
export const HANDSHAKE_QUIET_MS = 600;
export type ShellPhase = "quiet" | "waiting" | "failed" | "ready";
export function shellPhase(state: DaemonStatus, elapsedMs: number): ShellPhase;
```

```rust
// tray.rs — sol tık paneli açar, sağ tık cankurtaranı
fn on_tray_event(app: &AppHandle, event: TrayIconEvent);
// quick.rs — ikonun altına
pub fn show_at(app: &AppHandle, anchor: tauri::Rect);
// macos.rs
pub fn round_panel(window: &WebviewWindow, radius: f64);
```

## Definition of Done

- [x] Hazır bir daemon'da uygulama hiçbir açılış ekranı göstermeden açılıyor.
- [x] Daemon yokken teşhisli yüzey hâlâ çıkıyor.
- [x] Ana ekranda "Hesap" paneli yok; `Workspace`'te duruyor.
- [x] Menü çubuğu ikonuna tıklamak paneli ikonun altında açıyor.
- [x] Sağ tık `Restart daemon` / `Quit Mimir` veriyor.
- [x] `make desktop-check` yeşil.
- [x] Status `done` + changelog.

## Notes for the reviewer (Opus)

- **Cankurtaran menü şart.** Accessory bir uygulamanın WebView'ı bozulursa
  native bir çıkış yolu yoksa uygulama kapatılamaz.
- `shellPhase` saf ve testli; JSX'te zamanlayıcı mantığı olmamalı.
- Panelin ölçüleri ramp'tan gelmeli — `desktop/AGENTS.md`'nin "sihirli sayı
  yok" kuralı bu dosyada da geçerli.

## Changelog

- **Açılış ekranı yalnız hak ettiği yerde.** `lib/shell.ts` beklemeyi
  derecelendiriyor: 600 ms'ye kadar **hiçbir şey**, sonra tek satır, ve
  başarısızlıkta bugünkü dürüst yüzey — stderr ve yeniden dene. Kapının kendi
  argümanı yerinde duruyor (çalışmayan bir taşımanın üstüne proje seçici çizen
  bir pencere yalan söyler); değişen, *başarılı* bir el sıkışmanın artık sessiz
  olması. "Devam" düğmesi silindi: arkasında hiç karar yoktu, daemon çoktan
  cevap vermişti. Markanın kendi yüzeyi başarısızlığa taşındı — gerçekten
  yapacak bir şeyin olmadığı tek durum orası.
- **Ana ekrandan "Hesap" paneli kalktı.** `AccountPanel` `Workspace`'te duruyor;
  hiç değişmeyen bir bilgi en değerli yeri tutuyordu.
- **Menü çubuğu paneli.** `NSMenu` gitti (biçimlendirilemiyor: sistemin grisi,
  sistemin yüzü, sistemin ayırıcıları), ekranın ortasındaki 680×460 kutu da
  gitti. Yerine 360 puanlık tek bir panel — ikonun altına tutturulmuş, sıralama
  bir argüman: **durum** (menü çubuğuna bakma sebebi), **besteci** (paneli açma
  sebebi), **koşu** (bestecinin ürettiği şey), **dört eylem** (en nadir ve tek
  geri alınamaz olanlar).
- **Sağ tık cankurtaranı kaldı**: `Restart daemon` ve `Quit Mimir`, AppKit'in
  çizdiği. Uygulama accessory — Dock ikonu yok — ve panelin WebView'ı takılırsa
  o WebView'ın çizdiği bir "Çıkış" ulaşılamaz bir çıkıştır. `liveness.rs` zaten
  cevap vermeyen WebView'lar için var.
- **Panel bakış kaçınca kapanıyor**, ve asıl hatayı ortaya çıkaran tıklama şu:
  macOS pencereyi tray olayı gelmeden **önce** blur ediyor, yani yalnız
  görünürlüğe bakan bir toggle "kapalı, aç" diye okuyor ve ikon kendi panelini
  kapatamıyor. `hide_from_blur` kapanışı zaman damgalıyor, `toggle_at` onu
  okuyor.
- Panelin köşeleri gerçek: pencere saydam, kabuk `data-window="quick"` ile
  belgeyi de saydamlaştırıyor.

### Sapmalar

- **Köşe yuvarlaması `objc2` ile değil, `macOSPrivateApi` ile.** Task dosyası
  "bayrak açılmaz" diyordu; `tauri`'nin `transparent` özelliği macOS'ta o bayrağı
  şart koşuyor ve bayrağın altında yaptığı şey zaten public AppKit çağrıları
  (`setOpaque:` / `setBackgroundColor:`). Kendi elimizle yazmak `CALayer` için
  yeni bir Rust bağımlılığı (`objc2-quartz-core`) demekti. Bayrak App Store
  dağıtımını engeller; Mimir `make install-agent` ile kurulan yerel bir araç ve
  App Store'a hiç gitmiyor.
- **`NewTaskOverlay`'e dokunulmadı.** Şikâyet menü çubuğu yüzeyineydi.
  Uygulamanın *içinde*, bir kartı yazmak için açılan bir diyalog, karar ağacının
  doğru cevabı — ama aynı itiraz oraya da uzatılabilir; uzatılacaksa kendi
  task'ıyla.
- **`QuickTask.tsx` → `TrayPanel.tsx`.** Dosya adı ne olduğunu söylemeli;
  bu artık bir "hızlı görev penceresi" değil, uygulamanın kalıcı yüzeyi.

`make desktop-check: 0` · `make check: 0`
