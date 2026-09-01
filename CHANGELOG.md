# Changelog

Bu dosya [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) biçimini,
sürüm numaraları [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
kuralını izler.

## [1.1.0] — 2026-09-01

GOAT artık "açınca çalışan bir uygulama" değil, sistemde sürekli çalışan bir
servis ve menü çubuğundan tek kısayolla erişilen bir giriş noktası.

### Eklendi

- **launchd agent** (`scripts/install-agent.sh`, `make install-agent`) —
  `goat-daemon` login'de başlar, ölürse `KeepAlive` ile geri gelir, uygulamadan
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

- **`bin/goat-mcp`** — Claude Code oturumu için yerel, sıfır maliyetli MCP
  sunucusu. Araçlar: `web_search`, `fetch_page`, `research`, `diagnostics`,
  `ecommerce_product_lookup`, `tiktok_profile_lookup`, `gmaps_business_lookup`,
  `instagram_profile_lookup`, `maps_search` (yalnızca Places anahtarıyla) ve
  proje hafızası araçları `project_context`, `context_recall`, `context_remember`.
- **`bin/goat-daemon`** — yalnızca loopback dinleyen, uzun ömürlü HTTP servisi:
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

- Bu sürümde `goat-daemon`'ın ömrü masaüstü penceresinin ömrüne bağlıdır:
  kabuk her açılışta port + token üretip daemon'ı çocuk süreç olarak başlatır.
  Sürekli çalışan servis ve menü çubuğu 1.1.0'da gelir.
