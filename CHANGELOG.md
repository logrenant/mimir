# Changelog

Bu dosya [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) biçimini,
sürüm numaraları [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
kuralını izler.

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
