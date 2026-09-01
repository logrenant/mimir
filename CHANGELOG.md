# Changelog

Bu dosya [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) biçimini,
sürüm numaraları [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
kuralını izler.

## [2.0.0] — 2026-09-01

**GOAT artık Mimir.** Marka adı, tüm görsel sistem ve kodun içindeki her
tanımlayıcı Mimir Studio Brand System Guide'a göre yeniden yazıldı. Sürüm
numarası major: ikili adları, ortam değişkenleri, store yolu ve launchd
label'ı değişti — eski kurulum bu sürümle konuşmaz.

### Değişti — isimler

| Eski | Yeni |
|---|---|
| `bin/goat-mcp` · `bin/goat-daemon` | `bin/mimir-mcp` · `bin/mimir-daemon` |
| `github.com/logrenant/goat-mcp` | `github.com/logrenant/mimir` |
| `GOAT_DAEMON_PORT` · `GOAT_DAEMON_TOKEN` · `GOAT_*` | `MIMIR_*` |
| `com.goat.daemon` · `com.goat.desktop` | `studio.mimir.daemon` · `studio.mimir.app` |
| `~/Library/Application Support/goat-mcp/goat.db` | `~/Library/Application Support/mimir/mimir.db` |
| `~/Library/Logs/goat-daemon.log` | `~/Library/Logs/mimir-daemon.log` |
| `goat.bearer.<token>` (WebSocket alt protokolü) | `mimir.bearer.<token>` |
| `sessionlog` wire string `goat_run` | `mimir_run` (migration `0009`) |

`goat v1` adı yalnızca emekli Node öncülünü anlatan tarihsel pasajlarda kaldı;
o bir kayıt, marka kullanımı değil.

### Değişti — görsel sistem

- **Palet**: Carbon `#101114` · Mist `#eef0f2` · Electric `#2547e8` · Lime
  `#c6f04a`. Dört renk, beşincisi yok; arayüz yapısı yalnızca Carbon/Mist
  tonlarından kuruldu. Eski altın aksan Electric'e, yeşil "tamamlandı" Lime'a
  döndü. Hata kırmızısı palet dışı tek renk ve bilerek öyle: dekorasyon gibi
  okunan bir hata, beşinci renkten daha kötü.
- **Tipografi**: Aldrich (display, yalnızca büyük harf başlıklar ve etiketler)
  + Open Sans (gövde, 300/400/600). İkisi de OFL, `desktop/src/assets/fonts/`
  altında woff2 olarak gömülü — uygulama dışarı font istemiyor, offline
  çalışıyor, latin-ext ile Türkçe karakterler tam.
- **Marka**: ürün arayüzünde wordmark, ikonlarda altı kollu asterisk. İkisi de
  marka SVG'lerinin kendi path verisinden geliyor (`desktop/src/components/brand.tsx`),
  tek düz renkte çiziliyor.
- **İkonlar**: `scripts/make-icons.py` menü çubuğu template ikonunu, 1024px
  uygulama ikonunu, `.icns` setini ve favicon'u tek geometriden üretiyor.

### Eklendi

- `scripts/install-agent.sh` kurulumda eski store'u yeni yola **taşıyor**
  (`sqlite3 .backup` ile tutarlı anlık görüntü; eskisi yedek olarak yerinde
  kalır). Kayıtlı projeler, run geçmişi ve proje hafızası korunur.
- Store migration `0009`: `memory_episodes.source_kind` satırlarında
  `goat_run` → `mimir_run`.
- Store migration `0010`: run transkript yolları (`coding_runs.transcript_path`,
  `memory_episodes.source_path`, `memory_ingest_state.source_path`) yeni
  dizine yazıldı; kurulum betiği transkript dosyalarını da kopyalıyor. Böylece
  eski `goat-mcp` klasörü gerçekten silinebilir hale geliyor.

### Düzeltildi

- `test/e2e` sürüm dizesini sabit `0.1.0` olarak bekliyordu ve 1.1.0'daki
  sürüm bump'ından beri kırıktı — Go test cache'i maskelemişti. Artık
  `mcp.Version` sabitini okuyor, bir daha eskiyemez.

## [1.1.1] — 2026-09-01

### Eklendi

- Uygulama ilk çalıştırmasında kendini **login item** olarak kaydediyor
  (`~/Library/Application Support/mimir/.autostart-initialized` işaretiyle
  bir kez). Daemon zaten login'de geliyordu; menü çubuğu gelmeyince operatörün
  elinde çalışan bir sistem ve ona giden bir kapı kalmıyordu. Sonrasında karar
  tray'deki anahtarın.

## [1.1.0] — 2026-09-01

GOAT artık "açınca çalışan bir uygulama" değil, sistemde sürekli çalışan bir
servis ve menü çubuğundan tek kısayolla erişilen bir giriş noktası.

### Eklendi

- **launchd agent** (`scripts/install-agent.sh`, `make install-agent`) —
  `mimir-daemon` login'de başlar, ölürse `KeepAlive` ile geri gelir, uygulamadan
  bağımsız yaşar. Port + token kurulumda üretilir; `endpoint.json` ve plist
  ikisi de `0600`. `make agent-status` / `agent-logs` / `agent-restart` /
  `uninstall-agent`.
  - plist `PATH`'i genişletir: launchd'nin verdiği `/usr/bin:/bin:/usr/sbin:/sbin`
    ile `internal/refine` ve `internal/coderunner`'ın `claude` CLI'yi bulması
    mümkün değil.
- **Menü çubuğu uygulaması** — Dock ikonu yok (accessory), tray'de canlı daemon
  durumu, `New task…`, `Open GOAT`, `Restart daemon`, login'de başlatma anahtarı
  ve `Quit GOAT`. Uygulamadan çıkmak daemon'ı durdurmaz; pencereyi kapatmak
  gizler.
- **Hızlı task penceresi (⌘⇧G)** — son kullanılan projeye varsayılan, prompt
  yaz `⏎` ile başlat; canlı akış aynı pencerede. Pencereyi kapatmak run'ı iptal
  etmez, bitince sistem bildirimi gelir. Saf karar mantığı
  `desktop/src/lib/quickTask.ts` içinde, testli.

### Değişti

- **Masaüstü kabuğu artık attach-first.** `endpoint.json` varsa launchd'nin
  daemon'ına bağlanır (sağlıksızsa `launchctl kickstart -k`), asla ikinci bir
  daemon doğurmaz — tek SQLite store'a iki yazar olmasın diye. Dosya yoksa
  eskisi gibi kendi çocuğunu başlatır (`make desktop-dev` yolu).
- `endpoint.json` bir girdi olarak doğrulanır: `0600` değilse veya `base_url`
  loopback değilse **reddedilir**, okunmaz.
- Sürüm dizesi tek kaynaktan (`internal/mcp.Version`) geliyor ve git etiketiyle
  aynı: `/healthz`, `diagnostics` ve tray durum satırı aynı numarayı gösterir.
- Bundle hedefi yalnızca `app`; `.dmg` adımı Finder otomasyon izni istiyor ve
  GOAT dağıtılmıyor, kopyalanarak kuruluyor.

## [1.0.0] — 2026-09-01

İlk sürüm etiketi: bugüne kadar inşa edilmiş ve çalışan sistemin tamamı.
`make check` ve `make desktop-check` yeşil.

### Eklendi

- **`bin/mimir-mcp`** — Claude Code oturumu için yerel, sıfır maliyetli MCP
  sunucusu. Araçlar: `web_search`, `fetch_page`, `research`, `diagnostics`,
  `ecommerce_product_lookup`, `tiktok_profile_lookup`, `gmaps_business_lookup`,
  `instagram_profile_lookup`, `maps_search` (yalnızca Places anahtarıyla) ve
  proje hafızası araçları `project_context`, `context_recall`, `context_remember`.
- **`bin/mimir-daemon`** — yalnızca loopback dinleyen, uzun ömürlü HTTP servisi:
  klasör kapsamlı coding-task koşucusu (canlı akış), Google Maps lead-gen
  hattı ve aynı MCP kayıt defterinin `/mcp` üzerinden sunumu.
- **`desktop/`** — Tauri + React kabuğu: bağlantı el sıkışması, Workspace
  (klasör seç → görev ver → akışı izle), Leadgen (bölge araması → kategorilendirme
  → boşluk analizi → e-posta taslakları).
- **Proje hafızası (M8)** — Claude Code oturum transkriptlerini damıtıp
  proje başına aranabilir bağlam olarak geri veren `internal/{sessionlog,memory}`.
- Bağımlılıkların tamamı tam sürümle sabitlendi (SD-5); Crawl4AI ve Playwright
  Maps yardımcı konteynerleri `deploy/` altında sabit imajlarla tanımlı.

### Notlar

- Bu sürümde `mimir-daemon`'ın ömrü masaüstü penceresinin ömrüne bağlıdır:
  kabuk her açılışta port + token üretip daemon'ı çocuk süreç olarak başlatır.
  Sürekli çalışan servis ve menü çubuğu 1.1.0'da gelir.
