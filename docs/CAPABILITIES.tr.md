# CAPABILITIES.tr.md — Mimir bugün neler yapabiliyor

İngilizce aslı: [`CAPABILITIES.md`](CAPABILITIES.md). Bir tutarsızlık olursa
İngilizce sürüm esastır.

Her iki track'in tüm milestone'ları tamamlandı. Bu belge, sistemin şu an ne
yaptığının, her özelliğin çalışması için neye ihtiyaç duyduğunun ve kodun nerede
olduğunun eksiksiz ve güncel envanteridir. *Neden* böyle kurgulandığı için
[`ARCHITECTURE.md`](ARCHITECTURE.md); plan ve geçmişi için
[`ROADMAP.md`](ROADMAP.md).

---

## 1. İki binary

| Binary | Taşıma (transport) | Ömrünü yöneten | Amaç |
|---|---|---|---|
| `bin/mimir-mcp` | stdio MCP | onu kaydeden Claude Code oturumu | Bir Claude Code oturumuna web araması, sayfa kazıma, rafine edilmiş araştırma ve giriş gerektirmeyen ücretsiz kazıyıcılar verir. |
| `bin/mimir-daemon` | loopback HTTP (`127.0.0.1` + her başlatmada üretilen bearer token) | Tauri masaüstü uygulaması (onu bir sidecar süreç olarak başlatır) | Canlı akışlı, klasör kapsamlı kod görevi çalıştırıcısı; Google Maps lead-gen pipeline'ı; ve aynı MCP araçlarını `/mcp` altında yeniden sunar. |

İkisi de aynı runtime paketlerini import eder — tek motor, iki taşıma. `mimir-mcp`
içinde MCP taşıması dışında hiçbir şey stdout'a yazmaz; daemon portunu asla
yazdırmaz (portu ana süreç seçer).

---

## 2. MCP araçları (`bin/mimir-mcp`, ayrıca `mimir-daemon` `/mcp` altında)

Tüm yanıtlar **kompakt ve rafine**dir — ham kazınmış metin bir aracı asla terk
edemez (tek bir kontrol noktasında zorlanır: `internal/mcp/finalize.go`).
Aşağıdaki token tavanları yaklaşıktır.

### Her zaman kullanılabilir

| Araç | Girdi | Çıktı | Tavan | Gereksinim |
|---|---|---|---|---|
| `web_search` | `query: string`, `count?: int` (≤30, varsayılan 8) | `results: [{title, url, snippet}]` | 30 sonuç, yalnız meta veri | Ağ (DuckDuckGo) |
| `fetch_page` | `url: string` | `{ url, title, refined: true, markdown }` | ~1500 token | Crawl4AI Docker + `claude` CLI |
| `research` | `query: string`, `depth?: int` | `{ summary, key_points[], sources[{n,title,url}], gaps[], refined: true }` | ~2000 token | DuckDuckGo + Crawl4AI + `claude` CLI |
| `diagnostics` | yok | `{ crawl4ai, claude, duckduckgo, maps_scraper{…,optional}, versions }` | küçük, sabit | yok (sağlık kontrolünün kendisi) |

### Proje hafızası (M8)

Bir depoda daha önceki oturumların ortaya koyduğu şeyler — böylece bir sonraki
oturum bunları yeniden keşfetmek yerine öğrenmiş olarak başlar. Claude Code'un
kendi oturum transkriptlerinden ve bu daemon'ın kod çalıştırmalarından, pinlenmiş
haiku modeliyle **episode başına** damıtılır. Yalnızca yerel store açılabildiyse
kaydedilir: hatırlayacak yeri olmayan bir hafıza, bozuk bir hafıza değil, hiç
hafıza yok demektir — ve tek verebileceği cevap "hafıza yok" olan bir araç, her
oturumun context'inden pay alıp çıkmaz sokak reklamı yapar.

| Araç | Girdi | Çıktı | Tavan | Gereksinim |
|---|---|---|---|---|
| `project_context` | `project_path?: string` (varsayılan: çalışma dizini) | `{ project, repo{summary,top_level[],rule_files[]}, pinned_notes[], recent_work[], hot_files[], coverage, guidance, refined: true }` | ~1400 token | store; damıtma için `claude` CLI (onsuz da aranabilir) |
| `context_recall` | `query: string`, `limit?: int` (≤20), `project_path?: string` | `{ query, hits[{at,title,summary,files[]}], notes[], guidance, refined: true }` | ~1100 token | store |
| `context_remember` | `text: string`, `kind: decision\|convention\|trap\|todo`, `project_path?: string` | `{ stored{at,kind,text}, metadata_only: true }` | ~400 token | store |

### Bilgi çekirdeği (task-41)

Proje hafızası "bu klasörde ne oldu" sorusunu yanıtlıyor; bunlar "bu şey
hakkında ne biliyoruz" sorusunu — projeler arası ve içine yazan her model için.
Kullanılabilirlik yukarıdakiyle aynı şekilde store'u izler.

| Araç | Girdi | Çıktı | Tavan | Gerektirir |
|---|---|---|---|---|
| `brain_ingest_data` | `source: string`, `content: string`, `kind?: note\|research\|decision\|session\|file\|commit`, `project_path?: string` | `{ node{id,kind,source,title,assessment,tags[],neighbors[]}, distilled, note?, linked, refined: true }` | ~1400 token | store; bir damıtma sağlayıcısı (hiçbiri yanıtlamazsa düğüm değerlendirmesiz saklanır) |
| `brain_ingest_github` | `repo: string` (owner/name veya URL) | yukarıdakiyle aynı, global kapsamda | ~1400 token | store; `api.github.com`; özel repolar için `MIMIR_GITHUB_TOKEN` |
| `brain_query_nodes` | `query: string`, `limit?: int` (≤20), `project_path?: string` | `{ query, nodes[…], refined: true }` | ~1400 token | store |
| `brain_related` | `node_id: string`, `limit?: int` (≤40) | `{ node{…,neighbors[]}, refined: true }` | ~1400 token | store |

Kimlik `(project_path, kind, source_key)`; aynı kaynağı yeniden ingest etmek yeni
bir düğüm üretmez, mevcut satırı günceller. Damıtmanın FTS indeksine yazdığı
`aliases` terimleri — eşanlamlılar ve komşu kavramlar — bir aramanın, sorgunun
kelimelerini hiç içermeyen bir düğümü bulmasını sağlayan şeydir; vektör indeksi
yoktur. `project_path` boş olan düğümler globaldir ve her projeden görünür.

`context_recall` **içerik değil işaretçi** döndürür: cevabın yaşadığı başlıklar,
tarihler ve dosya yolları. Adı verilen dosyaları yeniden okumak ucuzdur; *hangi*
dosyalar olduğunu yeniden keşfetmek bir context penceresine mal olur.

Her yol `internal/project.Canonicalize`'dan geçer (symlink çözülür, denylist
uygulanır). Bu araçlar bilinçli olarak `Register` çağırmaz: bir projenin
geçmişini okumak, bir kod çalıştırmasının ihtiyaç duyduğu kaydı oluşturmak için
gerekçe değildir.

### Stage F — ücretsiz, elle yazılmış, giriş gerektirmeyen kazıyıcılar

Deterministik HTML/JSON çıkarımı (`internal/extract`); **rafine çağrısı yok**, bu
yüzden sıfır Claude token'ı harcar. Küçük yapılandırılmış veriler, düz metin yok.

| Araç | Girdi | Çıktı | Tavan |
|---|---|---|---|
| `ecommerce_product_lookup` | bir ürün URL'si | ad, fiyat, para birimi, stok durumu, puan, görsel | 300–400 token |
| `tiktok_profile_lookup` | bir kullanıcı adı / profil URL'si | görünen ad, takipçi/takip/beğeni sayıları, biyografi, doğrulanmış mı | 300–400 token |
| `gmaps_business_lookup` | bir Google Maps işletme URL'si | ad, adres, telefon, puan, yorum sayısı, çalışma saatleri, kategori — **tek adlı işletme** | 300–400 token |
| `instagram_profile_lookup` | bir kullanıcı adı / profil URL'si | görünen ad, takipçi/takip/gönderi sayıları, biyografi, doğrulanmış mı | 300–400 token |

Park edilmiş (yapılmadı): `linkedin_company_lookup` ve `ROADMAP.md` §A.3–§A.4
içindeki ücretli sağlayıcı araçları.

### Operatör tarafından sağlanan Places anahtarının arkasında

| Araç | Girdi | Çıktı | Tavan |
|---|---|---|---|
| `maps_search` | `query: string`, `count?: int` (≤60), `language_code?`, `region_code?`, `near?: {lat,lng,radius_meters}` | `{ query, returned, total_found, truncated, companies[{place_id,name,address,…}] }` | ~2000 token; **ücretlendirilir** |

`maps_search` **yalnızca** `MIMIR_GOOGLE_PLACES_API_KEY` ayarlıysa kaydedilir.
Anahtarsız kurulum normal kurulumdur — araç sadece yoktur, "anahtar yok" diyen
ölü bir uç değildir. Bu araç bir bölgedeki **her** işletmeyi listeler
(ücretlendirilen Places API), bu `gmaps_business_lookup`'tan (tek ücretsiz kayıt)
farklıdır.

---

## 3. Daemon HTTP yüzeyi (`bin/mimir-daemon`)

Her rota aynı zincirin arkasındadır: panic-recover → istek logu → loopback
koruması → bearer token kontrolü → gövde boyutu sınırı. REST'in Tauri Rust
kabuğundan çağrılması beklenir (daemon tasarım gereği CORS başlığı göndermez); çalıştırma
WebSocket'inin el sıkışması preflight'tan muaftır ve token'ı bir alt protokol
olarak taşır.

| Rota | Ne yapar |
|---|---|
| `GET /healthz` | `{ ok, version, uptime_ms }` |
| `GET /diagnostics` | daemon sağlığı + store durumu + proje sayısı + `places_configured` + kod çalıştırma istatistikleri (toplam / çalışan / başarısız / toplam `cost_usd`) + MCP `diagnostics` yükü |
| `GET /projects` | kayıtlı proje klasörlerini listeler |
| `POST /projects` | `{ path }` → kanonikleştir (`Abs`+`EvalSymlinks`), `/`, `$HOME` ve kara listedeki kökleri reddet, var olan bir dizin olmalı → opak bir `project_id` döner. **Asla varsayılan proje yok.** |
| `POST /coding-tasks` | `{ project_id, prompt }` → klasör kapsamlı akışlı bir `claude` oturumu başlatır, bir `run_id` döner |
| `GET /coding-tasks/{id}` | çalıştırma meta verisi / durum / maliyet |
| `GET /ws/runs/{id}` | WebSocket: çalıştırmanın JSONL transkriptini yeniden oynatır, sonra canlı olay veri yolunu takip eder — `RunStarted`, `TextDelta`, `ReasoningDelta`, `ToolCall`, `ToolResult`, `RunCompleted`, `RunFailed`, `rate_limit`; `Event.Seq` üzerinden birleştirilir, böylece kayıp veren bir veri yolu izleyiciye asla boşluk göstermez |
| `POST /maps/leadgen` | `{ query, region, count, language_code, region_code, near, gap_analysis, emails }` → lead-gen pipeline'ını çalıştırır (§5); yalnızca Places anahtarıyla kaydedilir |
| `POST /maps/emails/status` | `{ place_id, status }`, status ∈ draft / sent / skipped — SQL, bir bölge yeniden çalıştırmasında `sent`/`skipped` taslağın yeniden üretilmesini engeller; yalnızca Places anahtarıyla kaydedilir |
| `/mcp`, `/mcp/` | tüm MCP araç seti StreamableHTTP üzerinden — stdio ile aynı kayıt defteri, aynı kontrol noktası |

---

## 4. Klasör kapsamlı kod görevi çalıştırıcısı

**Yapabilecekleriniz:** bir proje klasörü seçin, ardından `claude`'un o klasörün
içinde bir kod görevini tamamlamasını sağlayın; bu sırada onun tüm
düşünce/eylem akışını canlı, saniye saniye izleyin.

- **Kapsamlama katıdır.** `internal/coderunner`, `cmd.Dir = project.Path`
  ayarlar *ve* `--add-dir <project.Path>` geçirir, ayrıca sabit bir
  `--permission-mode` sabiti kullanır. Çalıştırıcı yalnızca seçtiğiniz klasörü
  görebilir.
- **Mimir'a diğer her oturum gibi ulaşır.** İç içe bir `--mcp-config` ile değil,
  istemcinin kendi MCP kaydı üzerinden. İç içe yapılandırma
  `docs/ROADMAP.md` §B.2.1'de planlanmış, `task-35`'in *Out of scope* maddesinde
  ertelenmişti; bu belge onu yanlışlıkla gönderilmiş gibi anlatıyordu.
  Çalıştırıcı böyle bir bayrak geçirmiyor.
- **Canlı akış.** stdout satır satır ayrıştırılır (`stream-json`), kümülatif
  metin/düşünme uzunluğu **`message.id` başına** izlenir ve yalnızca farklar
  (delta) tipli olaylar olarak yayınlanır — bir süreç içi veri yolunda yayınlanır
  *ve* bir `coding_runs` satırıyla indekslenen bir JSONL transkriptine eklenir.
- **Zarif kapanış.** SIGTERM'de daemon, çıkmadan önce devam eden çalıştırmaları
  boşaltır.

Gereksinim: `$PATH` üzerinde ve giriş yapılmış `claude` CLI.

---

## 5. Google Maps lead-gen pipeline'ı

`internal/leadgen`, uçtan uca `place_id` ile iş parçacığına bağlanır. Dört aşama,
maliyet soldan sağa artar; her aşama önce önbelleğe bakar ve **başarısız olmak
yerine kalitesini düşürür** — `Pipeline.Run` yalnızca `ErrNoData` (hiçbir kaynak
liste üretmedi) veya iptal edilmiş bir bağlam için hata döner. Diğer her şey bir
`Report.Notes` metnidir.

| # | Aşama | LLM? | Ürettiği | Önbellek tablosu |
|---|---|---|---|---|
| 1 | **Bölge araması** | hayır | Bir bölgedeki her işletme. Birincil: Places API Text Search (`internal/maps`). Yedek: deterministik DOM çıkarımlı bir Playwright docker sidecar'ı (`internal/mapscrape`); id'ler ad alanına ayrılır, böylece kazınmış bir satır ücretlendirilmiş bir satırın üzerine asla yazamaz. | `companies`, `region_searches` (okumada ya hep ya hiç) |
| 2 | **Kategorize et** | çoğunlukla hayır | Şirket başına normalleştirilmiş bir `Category`. Statik bir Google-`types[]` → kategori tablosu yaygın durumu ücretsiz yanıtlar; `claude` yalnızca belirsiz kalıntıyı, kapalı bir sözcük dağarcığına karşı, gruplar hâlinde sınıflandırır (düz metin çıktısı yok). | `company_categorization` (`LeadgenCategoryVersion` ile anahtarlanır) |
| 3 | **Kategori başına boşluk analizi** | evet | Bir kategorideki ortak boşluklar/ihtiyaçlar; şirket başına deterministik hesaplanan beş skalerden sentezlenir (asla ham sayfa değil). SD-7 katı token tavanı. | `category_gap_analysis` (`region, category, LeadgenGapVersion, company_set_hash` ile anahtarlanır) |
| 4 | **Erişim e-postası** | evet | Şirket başına bir taslak pazarlama e-postası; o şirketin bilgileri + kategorisinin 3. aşama boşluk analizi verilir. | `outreach_emails` (`place_id, prompt_version` ile anahtarlanır; bölge yeniden çalıştırmasının uyduğu bir `status` ile) |

`Pipeline.Run` her zaman 1–2. aşamaları yapar. 3. aşama `gap_analysis: true` iken;
4. aşama `emails: true` iken (bu boşluk analizini de gerektirir) çalışır. İki model
aşaması, `MaxConcurrentRefines` ile sınırlı bounded `errgroup` fan-out'larıdır.

Her aşama bir `prompt_version` sabitiyle anahtarlanan bir önbellek yazdığı için,
bir önbellek isabeti **API çağrısı yok, `claude` alt süreci yok, sıfır token**
demektir. Bir bölgeyi yeniden çalıştırmak token'ı yalnızca gerçekten yeni olan ya
da `prompt_version`'ı kasıtlı olarak yükseltilen şirketler/kategoriler için
harcar.

Gereksinim: birincil yol için `MIMIR_GOOGLE_PLACES_API_KEY`; yedek için `make
maps-up` (Playwright sidecar'ı); 3–4. aşamalar için `claude` CLI.

---

## 6. Masaüstü uygulaması (`desktop/`)

Tauri (Rust kabuk) + React + shadcn/ui + Tailwind, macOS / Apple Silicon. Rust
kabuğu boş bir loopback portu seçer, her başlatmada 32 baytlık bir bearer token
üretir, `mimir-daemon`'ı tam olarak bu iki ortam değişkeniyle başlatır ve çıkışta
onu sonlandırır. WebView, alt süreç çıktısını asla ayrıştırmaz; REST, Rust
üzerinden gider (`daemon_request`), böylece token WebView'e hiç girmez.

Yedi ekran:

| Ekran | Orada ne yaparsınız |
|---|---|
| **Connection** | Daemon el sıkışması — sidecar'ın ayakta, kimliği doğrulanmış ve sağlıklı olduğunu doğrular. |
| **Workspace** | Yerel klasör seçici (`NSOpenPanel`) → bir proje kaydet → bir kod görevi istemi yaz → çalıştırmayı canlı bir "terminal"de izle: metin/akıl yürütme delta'ları eklenir, araç çağrıları/sonuçları risk rozetli katlanabilir kartlar olarak görünür. |
| **Leadgen** | Maps pipeline'ı: bir bölge arama formu, kategori başına boşluk analizi kartları ve genişletilebilir taslak e-posta ile bir şirket listesi; gönderildi / atla düğmeleri `POST /maps/emails/status`'a bağlı. `report.notes` birebir gösterilir. |

Paketleme: `tauri.conf.json`, sertleştirilmiş çalışma zamanı ve
`entitlements.plist` ile `app` + `dmg` üretir. İmzalama/noterleme derleme
zamanında ortam değişkeni tabanlıdır (`APPLE_SIGNING_IDENTITY` + Apple-ID / API
anahtarı kimlik bilgileri); anahtarsız bir derleme yine de ad-hoc bir uygulama
üretir.

---

## 7. Yerel kalıcılık (`internal/store`)

`modernc.org/sqlite` (saf Go, CGO yok) üzerinden SQLite, WAL modu +
`busy_timeout`, böylece iki binary tek bir DB dosyasını paylaşır. Migration'lar
gömülüdür ve yalnızca ekleme yapılır. Bu bir **önbellek ve yerel kayıttır**,
asla MCP tüketicisinin gördüğü bir doğruluk kaynağı değildir.

| Tablo | İçerik | Geçersiz kılma |
|---|---|---|
| `crawl_pages` | ham crawl önbelleği (markdown + HTML), `sha256(url)` ile anahtarlı | `config.PageCacheTTL` |
| `refined_pages` | rafine önbelleği (rafine metin + token tahmini) | aynı TTL; `RefinePromptVersion` yükseltmesi |
| `projects` | kod görevi çalıştırıcısı için kanonikleştirilmiş klasör yolu | değiştirmek için yeniden seç |
| `coding_runs` | çalıştırma meta verisi, maliyet, oturum id'si, transkript işaretçisi | yok (geçmiş) |
| `companies` | normalleştirilmiş Places/kazıma sonucu, `place_id` ile anahtarlı | çağıran tarafın verdiği uzun TTL (~30 gün) |
| `region_searches` | bir bölge aramasının döndürdüğü sıralı `place_id` listesi | aynı TTL; okumada ya hep ya hiç |
| `company_categorization` | normalleştirilmiş kategori + hangi katmanın yanıtladığı | `LeadgenCategoryVersion` yükseltmesi |
| `category_gap_analysis` | Claude'un sentezlediği kategori başına boşluklar/ihtiyaçlar | `LeadgenGapVersion` yükseltmesi; değişen bir şirket kümesi ıskalar |
| `outreach_emails` | taslak e-posta + `status` (draft/sent/skipped) | `LeadgenEmailVersion` yükseltmesi; "sent"/"skipped" yeniden üretimi engeller (SQL ile zorlanır) |
| `memory_episodes` | bir damıtılmış iterasyon: deterministik olgular her zaman, kabul edilmişse kısa bir özet | `MemoryPromptVersion` yükseltmesi özetleri yeniden üretir; olgular kalır |
| `memory_notes` | `context_remember` ile sabitlenen olgular | yok — ingest tarafından asla yeniden yazılmaz |
| `memory_ingest_state` | her transkriptin ne kadarının ayrıştırıldığı | transkript küçüldüğünde sıfırlanır (eklenmiş değil, değiştirilmiş demektir) |
| `memory_fts` | episode başlık, özet, dosya ve komutları üzerinde FTS5 indeksi | trigger'larla senkron tutulur |

Claude Code token'ı harcayan her şey `internal/refine`'daki tek `claude -p`
headless alt sürecinden geçer: sayfalar için `Distil`, M8'den beri proje hafızası
için — her seferinde tek bir episode üzerinde — `Recap`. Her çağrı noktası önce
store'a bakar ve her tavan bir `config` sabitidir.

Hafıza, *tasarruf etmek için* harcayan taraftır: episode başına sınırlı, tek
seferlik bir maliyete karşılık, bir oturumun aynı depoyu sıfırdan yeniden
keşfetmesinin tekrar eden maliyeti.

---

## 8. Çalışması için gerekenler

| Özellik | Docker | `claude` CLI | Places anahtarı | Node/Rust araç zinciri |
|---|---|---|---|---|
| `web_search` | — | — | — | — |
| `fetch_page`, `research` | Crawl4AI (`make crawl-up`) | evet | — | — |
| `diagnostics` | — | — | — | — |
| Stage F kazıyıcılar | — | — | — | — |
| `maps_search` | — | — | **evet** (`MIMIR_GOOGLE_PLACES_API_KEY`) | — |
| `project_context`, `context_recall`, `context_remember` | — | damıtma için (onsuz da aranabilir) | — | — |
| Kod görevi çalıştırıcısı + canlı akış | — | evet | — | — |
| Lead-gen (birincil yol) | — | evet (3–4. aşamalar) | **evet** | — |
| Lead-gen (kazıma yedeği) | Playwright sidecar (`make maps-up`) | evet (3–4. aşamalar) | — | — |
| Masaüstü uygulaması | (kullanılan özelliğe göre) | evet | Leadgen ekranı için | evet (`make desktop-dev`) |

Rafine işlemi mevcut `claude login` oturumunuza biner — **Anthropic API anahtarı
yok**, başka bir LLM sağlayıcısı yok. Sabitlenmiş rafine modeli
`claude-haiku-4-5-20251001`'dir (`internal/config`).

Tam kurulum ve sorun giderme: [`INSTALL.md`](INSTALL.md).

---

## 9. Doğrulama durumu

Temiz bir checkout üzerinde son tam çalıştırma itibarıyla:

- `make check` (build + `go vet` + lint + birim testleri + `-race`) — **yeşil**
- `make e2e` (mock servislere karşı uçtan uca MCP smoke testi) — **geçti**
- `desktop/` TypeScript: `tsc --noEmit` temiz, `vitest` 22/22 geçti
- `desktop/` Rust (`cargo fmt`/`clippy`/`test`) — `make desktop-check` ile
  çalışır; yerel bir Rust araç zinciri gerektirir
- Entegrasyon testleri (gerçek Docker / `claude` CLI / Places API)
  `//go:build integration` arkasındadır ve `make check`'in parçası **değildir**
