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
| `brain_scan_repo` | `project_path?: string`, `limit?: int` (≤50), `dry_run?: bool` | `{ scanned, skipped_unchanged, failed, remaining, eligible_total, files[], metadata_only: true }` | ~1400 token | store; bir damıtma sağlayıcısı |

**Kendini kaydeden kısım.** Daemon, damıtılmış hafıza episode'larını `session`
düğümüne ve dokundukları her yol için bir `file` düğümüne çeviriyor, agy
oturumlarını hook spool'undan çekiyor ve yeni commit'leri `commit` düğümü
yapıyor — beş dakikalık bir tikte ve **model çağrısı olmadan**.
`brain_scan_repo` bunun tersi: bilerek pahalı olan tek iş, dosya başına bir
damıtma, çağrı başına bir batch ile sınırlı ve hash ile atlamalı, yani
değişmemiş bir repoda ikinci geçiş bedava. Önce `dry_run` ile faturayı gör.

**Yerleşik tarama.** `mimir-daemon` yaşadığı sürece bir tarama sürüyor:
`~/development` ve `~/Documents` altındaki her proje `agy` üzerinden bitene
kadar okunuyor, sonra bir bekleme ve baştan — yani her zaman çalışan bir agy var
ve öğleden sonra yazılan bir dosya akşam Brain'de oluyor. PDF'ler poppler'ın
`pdftotext`'i kuruluysa okunuyor, değilse sessizce atlanıyor. Yanıt vermeyen bir
sağlayıcı taramayı geri çekiyor (bir dakika, katlanarak, en fazla otuz), saatte
binlerce başarısız süreç doğurmak yerine; damıtılamayan dosya content hash
tutmadığı için sonsuza dek atlanmıyor, yeniden deneniyor. Masaüstündeki **Brain**
sekmesi bunu gösteriyor — `GET /brain/scan`, duraklat / sürdür / şimdi tara —
ve yanında düğümlerle kenarların kuvvet yönlendirmeli resmi (`GET /brain/graph`,
`/brain/projects`, `/brain/nodes/{id}`).

**Yapısal katman, varsa.** Yukarıdaki her düğüm bir modelin bir şeyi okumasının
sonucu. [Graphify](https://github.com/Graphify-Labs/graphify) kuruluysa
(`pip install graphifyy` — Apache-2.0, ücretsiz), her tur ayrıca onun
tree-sitter geçişini taramanın okumasına izin verilen aynı dosyalar üzerinde
çalıştırıyor ve bulduklarını — fonksiyonlar, sınıflar ve aralarındaki `calls`,
`imports`, `defines`, `inherits`, `uses` kenarları — **tek bir model çağrısı
olmadan** yazıyor. Semboller taramanın zaten yazdığı `file` düğümlerine
bağlanıyor, yani "bu dosya ne anlatıyor" ile "içinde ne var" tek bir grafik.
Graphify olmayan makinede hiçbir şey değişmiyor ve bu söylenmiyor: katman
sunuluyor, dayatılmıyor. `GET`/`PUT /brain/structural` hem anahtar hem de
"kurulu mu" cevabı; Brain sekmesi ikisini de gösteriyor. Resimde varsayılan
olarak kapalı (`?kinds=all` açıyor): semboller her şeyden hem daha kalabalık hem
daha bağlantılı, bağlantıya göre sıralanan bir grafik yalnızca sembollerden
oluşurdu.

Bilinmesi gereken bir sınır, varsayım değil ölçüm: Go'da alıcı üzerinden yapılan
çağrılar zayıf çözülüyor. Bu deponun 300 dosyasında ayrıştırıcı 2359 fonksiyona
3154 çağrı kenarı üretti, 738 metoda ise yalnızca 325 — yani `writeJSON(...)`
47 çağıranını da gösterirken `c.relate(...)` hiç çağıran göstermiyor.
**Çağıranın yokluğu, çağıran olmadığının kanıtı değildir.** Paket düzeyi
fonksiyonlar, import'lar ve içerme güvenilir; metotlar eksik.

**Taşımak ve unutmak.** Adı değişen ya da taşınan bir depo eski yolunu ayrı bir
proje olarak geride bırakıyordu ve bunu kaldıracak hiçbir yol yoktu — capture
döngüsü eski dizini adlandıran transcript'lerden onu yeniden yazmaya devam
ediyordu. `POST /brain/projects/{id}/move` her şeyi yeni yolun altına taşıyor
(düğüm kimliği yoldan türediği için bütün kimlikler ve kenarlar tek bir
transaction'da yeniden yazılıyor, hedefin zaten bildiği düğümler birleştiriliyor),
`DELETE /brain/projects/{id}` ise projeyi tamamen unutuyor. İkisi de Brain
sekmesinin yapılandırma yarısında; klasörü olmayan proje orada işaretli.
Diskteki transcript dosyalarına asla dokunulmuyor.

**Bir makinenin tamamı.** `bin/mimir-scan ~/development` (`make scan`) aynı
taramanın arada oturum olmayan hâli: bir kökün altındaki bütün projeleri bulur —
git checkout'ları ve hiç repo olmamış ama kendi dosyaları olan klasörler — ve her
birini bitene kadar sürer, ilerlemeyi stderr'e yazar. Dosya başına `agy`
üzerinden bir `gemini-3.8-flash-high` çağrısı; `-n` faturayı söyler, hiçbir şey
harcamaz. Damıtılamayan bir düğüm content hash tutmaz, yani sağlayıcının çöktüğü
bir aralık sonsuza dek atlanmaz, bir sonraki çalışmada yeniden denenir.

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

### Katalog (task-87)

İki okuma, ve kasıtlı olarak yazan hiçbir şey yok. Toplu yeniden yazımı kuyruğa
almak bir araç değil bir rota (`POST /catalog/rewrite`): bu, kayıtta bütün
etkisi para harcamak olan tek araç olurdu — birinin yazdığı bir cümleden bir
katalog dolusu arama, tarama ve akıl yürütme çağrısı. Rota bu yüzden açık bir
ürün listesi alıyor ve operatörün onlara az önce baktığı bir ekranda duruyor.

| Araç | Girdi | Çıktı | Tavan | Gerektirir |
|---|---|---|---|---|
| `catalog_products` | `import_id?: string`, `status?: enum`, `category?: string`, `limit?: int` (≤ sayfa tavanı) | `{ imports[] \| products[] { id, title, sku, category, status, has_draft }, total, metadata_only: true }` | ~1200 token | store |
| `catalog_product` | `product_id: string` | `{ id, status, current, draft?, notes[], refined: true }` — listeleme düzyazı olarak, mağazanın HTML'i asla | ~1500 token | store |

İkisi de taslak sürümünü kendisi çözüyor. O bir önbellek anahtarı ve daemon onu
prompt sabitinden, modelden, marka hash'inden, skill sürümünden ve — hedef dil
için — dilden ve gözden geçirme prompt sabitinden kuruyor; onu adlandırabilen
bir çağıran kendisine artık var olmayan bir marka sesiyle yazılmış metni servis
edebilir ve bunu bilmesinin hiçbir yolu olmazdı. Bileşim tek bir yerde
(`Studio.CurrentDraftVersion`), çünkü daha önce üç yerdeydi ve ayrıştılar: HTTP
katmanı, skill gemiye girdikten çok sonra bile boş bir skill sürümü geçiyordu,
dolayısıyla toplu yeniden yazımın parasını ödediği her taslak hiçbir okumanın
bakmadığı bir anahtar altına yazılıyordu.

#### Diller (task-103, task-105, task-107)

Bir katalog birden fazla dil taşıyabilir ve hangilerini taşıdığı bu ikili
dosyanın değil **operatörün kendi dışa aktarımının** özelliğidir: gerçek IKAS
özel alanlar dışa aktarımında `Html:Detay` ile `Html:Detay-AR` yan yana duruyor,
ve Arapça sütunu olmayan bir mağazaya Arapça pas önerilmiyor — cevabın
yazılacağı bir yer olmazdı.

Bir kart bir dildir. İngilizce, düz Amerikan İngilizcesiyle; Arapça, Körfez
e-ticaretinde yazıldığı biçimiyle Modern Standart Arapçayla (الفصحى) yazılıyor:
Arapça noktalama, Batı rakamı, kaşide ve hareke yok, marka adı/model kodu/birim
Latin harfle kalıyor.

Hedef dildeki bir taslakla mağaza arasında iki şey duruyor ve ikisi türce farklı:

- **Deterministik bir kapı** (`internal/catalog/lint.go`), hiçbir model çağrısı
  yapmaz. Yargı gerektirmeden düzeltilebilecek olanı düzeltir — kaşide, bidi
  denetim karakteri, Arap-Hint rakamı, Arapça cümlenin içindeki ASCII virgül —
  ve bunu sessizce değil, söyleyerek yapar. Karar verilebilir olanı reddeder:
  iddia ettiği dilde olmayan metin, içeride kalmış kaynak dil harfleri, uzun
  çevrilmemiş Latin bölüm, ve **kaynağın hiç söylemediği bir sayı**. Sonuncusu
  mevcut en ucuz halüsinasyon kontrolüdür ve kontrole taslağın yanında kaynağın
  da verilmesinin sebebidir.
- **Anadil gözden geçirme pasosu** (`internal/catalog/review.go`), bu depodaki
  ilk doğrulayıcı paso. Hedef dili anadili olarak okuyan bir editör rolünde
  ikinci bir `llm.Reason` çağrısı; kaynağı, taslağı ve deterministik kapının
  zaten bulduklarını görür — "500 sayısı kaynakta yok" denen bir editör onu
  düzeltir, genel olarak hata aramaya çıkan bir editör her seferinde başka bir
  şey bulur. Çıktısı tekrar kapıdan geçer.

Kapı tabandır ve esnemez: reddettiği bir taslak, gözden geçirene erişilemedi
diye saklanmaz. Hem yeniden yazım yolunda hem operatörün düzenleme yolunda
çalışır, yani kapıyı atlayan bir taslak saklamanın yolu yoktur — ama operatörün
yolunda bulguları ret değil nottur, çünkü bir insanın Arapçası bizim oran
sezgimiz yüzünden reddedilecek bir şey değildir.

Arapça gövde tek bir `<div dir="rtl" lang="ar">` içinde yayına gider. Paragraf
yönü olmadan çift yönlü algoritma noktalamayı ve sayıları hiçbir şeye göre
çözer, `50 مل، يدوم` yanlış sırada görünür — ve `Render` bir `dir` niteliğini
sessizce düşürürdü, çünkü Türkçe bir mağazanın kendi HTML'inde hiç `dir`
olmamıştır.


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
| `GET /accounts` | bağlı Claude hesabı ya da boş liste — en fazla bir tane olur |
| `POST /accounts/login` · `GET /accounts/login` | girişi başlat ve takip et. Daemon kendi kimlik yuvasına karşı `claude auth login` çalıştırır ve yetkilendirme sayfasını Chrome'da gizli pencerede açar; GET `opening` / `waiting` / `code` / `done` / `failed` döner |
| `POST /accounts/login/code` | `{ code }` → CLI tarayıcıyı kendisi açamadığında düşülen kod yapıştırma yolu |
| `POST /accounts/reset` | çıkış yap, yuvayı sil, kaydı unut. "Çıkış yap" düğmesi — bağlantıyı kesen tek yol, ve hesap değiştirmenin yolu |
| `GET /accounts/{id}/status` | canlı `claude auth status` — kim giriş yapmış, hangi planla. Bedava |
| `POST /maps/leadgen/export` | aynı gövde + `{ enrich, dir }` → `.xlsx` yazar (özet + kategori başına bir sayfa), yolunu ve sayıları döner |
| `GET /coding-tasks/{id}` | çalıştırma meta verisi / durum / maliyet |
| `PATCH /coding-tasks/{id}` | `{ title?, prompt?, model?, attachment_ids? }` → kartın ne istediğini yeniden yazar. Patch'tir: gönderilmeyen alana dokunulmaz. Karta henüz bir şey harcanmadıysa — `backlog`, `queued`, `failed`, `stopped` — geçerlidir; diğer hallerde 409, çünkü çalışan ya da bitmiş bir çalıştırmanın istemi harcananın kaydıdır. Görsel kart yazıldıktan çok sonra da eklenebilir; düzenlemeyle düşen görsel aynı anda geri alınır |
| `POST /coding-tasks/queue/kick` | dispatcher'a kuyruğa yeniden bakmasını söyler. Kuyruk yalnızca iş bırakıldığında ve bir çalışma slotunu boşalttığında yoklanır, *hesap* değiştiğinde değil — yani hiçbir şey bağlı değilken kuyruğa giren kart sonsuza kadar bekler. Harcanacak kimlik hâlâ yoksa — ya da token bütçesi bitmiş, kuyruk penceresini bekliyorsa — 409 ve nedeni |
| `GET /coding-tasks/queue/limits` | `?limit=` → kuyruğun neden ilerlemediği ve ne zaman ilerleyeceği: `holds` dispatcher'ın şu an beklediği kimlik yuvası ve yenilenme saati, `log` kalıcı kayıt — `run` (bütçenin ortasında kestiği görev, kuyruğa geri kondu), `dispatch` (kuyruktaki bir görev başlatılamadı), `resumed` (pencere yenilendi, kuyruk kendi kendine devam etti) |
| `POST /coding-tasks/{id}/retry` | `{ fresh? }` → `failed` ya da `stopped` bir çalıştırmayı kuyruğa geri koyar. Alan yoksa ya da `false` ise satırın `session_id`'si korunur; dispatcher kartı yeniden aldığında CLI `--resume` ile açılır ve oturum kaldığı yerden sürer. `true` oturumu atar, görev baştan yapılır. Başka her durum 409 |
| `GET /ws/runs/{id}` | WebSocket: çalıştırmanın JSONL transkriptini yeniden oynatır, sonra canlı olay veri yolunu takip eder — `RunStarted`, `TextDelta`, `ReasoningDelta`, `ToolCall`, `ToolResult`, `RunCompleted`, `RunFailed`, `rate_limit`; `Event.Seq` üzerinden birleştirilir, böylece kayıp veren bir veri yolu izleyiciye asla boşluk göstermez |
| `POST /maps/leadgen` | `{ query, region, count, language_code, region_code, near, gap_analysis, emails, provider?, model? }` → lead-gen pipeline'ını çalıştırır (§5); yalnızca Places anahtarıyla kaydedilir. `provider`/`model` gönderilmezse operatörün `PUT /settings` ile kaydettiği seçim, o da yoksa sınıf yönlendirmesi devreye girer |
| `GET /llm/providers` | `provider`/`model` alanlarının denetlendiği sağlayıcı/model izin listesi ve hiçbiri gönderilmediğinde bir çalıştırmanın alacağı varsayılan. Her daemon'da yanıtlar — masaüstündeki seçici bunun bir görünümüdür, kopyası değil |
| `POST /maps/outreach` | `{ place_ids, channels, region?, provider?, model? }` → *seçilen* şirketlere yazar: kanal başına bir taslak (`email`, `whatsapp`). Süzgeç değil kimlik listesi alır — kısa bir dizeyle bir bölgenin tamamını harcatabilecek tek şey bir süzgeç olurdu. `place_ids` defterle çözülür ve `LeadsPageMax` ile sınırlıdır |
| `POST /maps/outreach/status` | `{ place_id, channel, status }`, status ∈ draft / sent / skipped — SQL, bir bölge yeniden çalıştırmasında `sent`/`skipped` taslağın yeniden üretilmesini engeller. Karar kanal başınadır: e-postayı gönderip WhatsApp'ı atlamak olağan bir karardır |
| `GET`/`PUT /settings` | Operatörün kendi ayarları: lead-gen'in model aşamalarının harcadığı varsayılan `provider`/`model`, ve her kanalın kural dosyası. `PUT` çifti aynı izin listesinden geçirir — ikisi de bir alt sürece argv olur |
| `PUT /settings/rules` | `{ channel, body }` → o kanalın kural dosyasını değiştirir. Boş gövde sıfırlamadır, boş istem değil |
| `POST /settings/rules/reset` | `{ channel }` → binary'nin gönderdiği varsayılan metni geri koyar. İstemcinin varsayılanın kopyasını taşımaması için bir rota |
| `GET /maps/leads` | Lead defteri: bir koşunun bulduğu her işletme, kategorisi ve taslak durumuyla. Süzgeçler: `category`, `run_id`, `q`, `without_website`, `limit`, `offset`. Hiçbir şey harcamaz, hiçbir yerde arama yapmaz |
| `GET /maps/leads/categories` | Kategori rayı — aynı süzgeç altında, sayfanın değil defterin tamamı üzerinden sayılır |
| `GET /maps/leads/runs` | Koşu geçmişi: hangi arama neyi, ne zaman buldu |
| `GET /chat/sessions` · `GET /chat/sessions/{id}` | Kelimesi kelimesine sohbet arşivi — Claude Code oturumları, daemon'ın kendi kodlama koşuları ve agy konuşmaları, tur tur |
| `GET /chat/search` | Söylenenin tam metni üzerinde arama, proje kimliğiyle sınırlandırılabilir |
| `GET /brain/nodes/{id}/versions` | Bir düğümün geçmişi: kaynağın taşıdığı her farklı içerik hash'i için bir kayıt, o sürümün değerlendirmesiyle |
| `GET`/`PUT /brain/structural` | Yapısal katman: Graphify kullanılabilir mi, kurulu mu, ve kurmak için gereken komut |
| `POST /brain/projects/{id}/move`, `DELETE /brain/projects/{id}` | Bir projenin bütün geçmişini yeni bir yola taşır ya da unutur — sistemde silen tek iki yazma |
| `POST /catalog/imports` | `{ filename, data_base64 }` → bir Shopify ya da IKAS ürün export (ürünler ya da özel alanlar)'unu okur: BOM'u, kodlamayı (UTF-8 / Windows-1254), ayracı (`,` / `;`) ve satır sonunu tespit eder, varyant satırlarını ürünlere toplar (Shopify handle'a göre, IKAS ürün grup ID'sine göre) ve marka kitini çıkarır. Multipart yerine base64, ekler ile aynı sebepten — masaüstü daemon'a string gövde ileten bir Rust proxy üzerinden ulaşıyor — ve burada fazladan bir şey kazandırıyor: baytlar bozulmadan geliyor, yani kodlama tespiti export'un gerçekte yazdığını görüyor. Bu yüzeydeki gövdesi bir belge olan tek rota, o yüzden tüm yüzeyin capini yükseltmek yerine kendi sınırı var (`CatalogCSVMaxBytes`) |
| `GET /catalog/imports` · `GET`/`DELETE /catalog/imports/{id}` | listele, oku, unut. Listenin her satırı eşleşen profili ve `counts`'u — duruma göre ürün sayılarını — taşır: "hangi dosyayı açacağım" sorusunu dosyanın adı değil hangi durumda olduğu yanıtlar. Sayımlar satır başına bir okuma değil, bütün liste için tek bir `GROUP BY`'dır; okuyamayan bir depo ekranı düşürmek yerine özeti boş bırakır. Liste dosya gövdesini hâlâ taşımaz — her CSV'yi taşıyan bir menüyü açmak bütün import'ları birden açmak kadar pahalı olurdu, çerçeve de bu yüzden dosyanın kendi ekranında kalır |
| `PUT /catalog/imports/{id}/mapping` | `{ mapping }` → hiçbir platform profiline uymayan bir başlık için — ya da uyan bir profildeki yanlış bir sütun için — operatörün kendi sütun eşlemesi. Dosya yeniden okunur ve marka kiti onun altında yeniden çıkarılır, çünkü eşlemeden önce bir başlık sütunu ve dolayısıyla hiçbir başlık yoktu. Ne başlık ne açıklama adlandıran bir eşleme, boş ürünlerle dolu bir import'a dönüşmek yerine bir cümleyle reddedilir. Görünüm `readable` (bir platform uydu **ya da** eşleme yeterli — ekranın kapısı budur, `dialect` değil), kayıtlı `mapping`, hiçbir şey uymadığında deterministik bir `suggested` eşleme ve ilk veri satırından sütun başına bir `sample` değeri taşır |
| `GET /catalog/profiles` | bu ikilinin sevk ettiği platform profilleri, her birinin okuduğu sütunlarla. Var, çünkü Go'da yaşayan kapalı bir kümenin kendi kopyasını tutan bir istemci bayatladı: masaüstü task-91'de silinen bir profili hâlâ sunuyordu ve o günden beri eklenen her profili kaçırırdı |
| `PUT /catalog/imports/{id}/fields` | `{ fields }` → operatörün yapılandırdığı biçimde, dil başına hangi alanların yeniden yazılabileceği. Var, çünkü bir ürün export'unun sabit bir alan kümesi yok: buradaki gerçek IKAS export'unda mağazaya özel bir satış kanalı sütunu, tamamen boş bir SKU ve özel bir Arapça gövde var; başka bir mağazanınkinde bunların hiçbiri yok. Bu yüzden açılabilir küme **dosyadan** okunuyor — sütunu olmayan alan sunulmuyor. Bir varsayılan değil **kapı**: alan adı veren bir kart bile operatörün kapattığı alanı yazamaz, ve pas neyi atladığını notlarında söyler. Boş küme "yapılandırılmadı" demek, yani hepsi — o yüzden boşa normalleşen bir seçim saklanmaz, saklansaydı her anahtarı sessizce geri açardı |
| `PUT /catalog/imports/{id}/target-lang` | `{ lang }` → bir çeviri export'unun hedef sütunlarının hangi dili taşıdığı. IKAS'ın Çeviriler export'unda `İsim, Açıklama, …` yanında `Çevrilecek İsim, Çevrilecek Açıklama, …` var ve dosya "Çevrilecek"in hangi dile çevrildiğini yazmıyor — operatör onu dışa aktarırken panelde seçmişti. İçerikten algılamak bu sistemde olmayan bir dil algılayıcısı olurdu; yanlış algılamak Arapçayı Almanca sütununa yazar, dosyanın her satırında |
| `PUT /catalog/imports/{id}/dialect` | `{ key }` → operatörün seçtiği profil, algılamayı ezerek; boş anahtar dosyayı algılamaya döndürür. Algılama "bu dosyayı hangi platform yazdı" sorusunu cevaplıyor, "bu mağaza hangi platformda" sorusunu değil; bir sütununu yeniden adlandırmış mağaza, operatör onun bir IKAS export'u olduğunu görebilirken `Detect`'in göremediği bir dosya üretir — ve tek çıkış yolu profil tablosunda zaten duran bir haritayı elle yeniden yazmaktı. Profil dosyanın **kendi** başlık yazımına bağlanır; tablodaki yazımı yazmak, dosyada hiç olmayan bir sütun adını dışa aktarmak ve panelin dosyayı reddetmesi demekti |
| `POST /catalog/imports/{id}/reread` | Bir import'un ürünlerini ve marka kitini, lehçe tablosunun bugünkü hâliyle yeniden kurar. Bir lehçe profili koddur: dosya yüklendikten sonra eklenen bir profil o dosyayı doğru okur, ama yükleme anında yazılmış ürünler sütunsuz kurulmuştur ve hâlâ boştur. Bir okumanın yan etkisi değil, açık bir işlem — bin satırı yeniden yazan bir GET, kimsenin akıl yürütemeyeceği bir GET'tir |
| `GET`/`PUT /catalog/imports/{id}/brand` | marka kiti: **sözlük** (bu mağazanın kendi açıklamalarının kullandığı etiketler, sınıflar ve stil özellikleri, sayılmış — model çağrısı harcamadan çıkarılır) ve **ses** (hitap, ton, tekrar eden kalıplar, kaçınılan klişeler, sözcük dağarcığı — tek bir distil çağrısı). `PUT` sesi alır ve yanında gelen başka şeyi yok sayar: ses operatörün kendi yazısı, sözlük ise her yeniden yazımın ölçüldüğü şey |
| `POST /catalog/imports/{id}/brand/rescan` | iki yarımı da saklanan dosyadan yeniden çıkarır |
| `GET /catalog/products` | `?import_id=&status=&category=&limit=&offset=` → dosya sırasında bir sayfa ürün; her biri özgün içeriği, güncel taslağı ve üzerindeki her düzenlemenin tabi olduğu sözlükle. Taslak sürümü **sunucuda** çözülür: bir önbellek anahtarını adlandırabilen bir istemci, kendisine artık var olmayan bir marka sesiyle yazılmış metni servis edebilir ve bunu bilmesinin hiçbir yolu olmazdı |
| `GET /catalog/products/{id}` | bir ürün, taslağı ve sözlüğü |
| `PUT /catalog/products/{id}/draft` | `{ content, fields }` → operatörün kendi düzenlemesi. Her alan, makine yeniden yazımının geçtiği aynı render-ve-sadeleştirme kapısından geçer ve cevap neyin sadeleştirildiğini söyler: editör bir kolaylık, sunucu yetkili olan taraf. Satır elle düzenlenmiş olarak işaretlenir ve sonrasında SQL, sonraki hiçbir geçişin onu ezmesine izin vermez |
| `POST /catalog/products/{id}/status` | `{ status, reason }`; status ∈ pending / researched / drafted / approved / rejected / failed |
| `POST /catalog/imports/{id}/export` | kataloğu `ExportDir`'e dosyanın kendi çerçevesinde CSV olarak geri yazar, **yalnız onaylanmış** taslakları uygulayarak. Kimsenin değişiklik onaylamadığı bir hücre yeniden türetilmez, kopyalanır — her bir hücrenin tırnaklamasına kadar — yani hiçbir şeyin onaylanmadığı bir import, kendisine verilen dosyayı bayt bayt üretir |
| `POST /catalog/rewrite` | `{ import_id, product_ids[], fields[]?, research?, title?, lang?, provider?, model? }` → toplu geçişi panoda bir kart olarak kuyruğa alır ve `run_id` döner. `provider`/`model` çifti karta yazılır ve pas onu okur: bir ayar değişikliği pası ortasından taşıyamaz, kart haftaya yeniden çalıştırıldığında da seçilen modeli harcar; ikisini de boş bırakmak "operatörün kayıtlı seçimi" demektir ve pas başlarken çözülür. Çift, her per-run seçimin geçtiği izin listesinden geçer ve yapılandırılmış çıktı kapısı kayıtlı modele değil **bu** çifte sorulur. Seçtiğiniz ürünleri alır, **asla bir filtre değil** — `POST /maps/outreach`'in koyduğu kural, ve burada daha çok önemli: bir filtre tek bir kısa dizeden bir katalog dolusu arama, tarama ve model çağrısı harcatırdı. Kart başına `CatalogProductsPageMax` ile sınırlı. Yalnız bir kuyruk varken kaydedilir; diğer bütün `/catalog/*` rotaları kuyruksuz bir daemon'da da cevap verir |
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

**Tek Claude hesabı, Mimir'in kendi yuvasında, ve bir kez bağlanmak yeterli.**
Kimlik yuvası, CLI'ın hash'leyip Keychain girdisi adına çevirdiği bir dizindir
(`CLAUDE_SECURESTORAGE_CONFIG_DIR`); Mimir bunu kendi store'unun yanında türetir
ve oraya kendisi giriş yapar — yani operatörün terminaldeki oturumu değildir.
Bağlanmak `claude auth login`'i bir pty üzerinde çalıştırır ve yetkilendirme
sayfasını Chrome'da **gizli** pencerede açar: normal pencere tarayıcıda zaten
açık olan Claude oturumunu taşır ve hangi hesapla bağlanıldığını hiç sormaz.
Yuva bundan sonra kapanışlara ve yeniden başlatmalara dayanır: girişi Keychain
tutar, açılışta daemon yuvayı Keychain ile uzlaştırır (`Restore`), oturumu
kapatmaz. Süresi dolmuş ya da iptal edilmiş bir giriş orada düşürülür, yani
bağlı görünen bir kayıt her zaman Keychain'in gerçekten arkasında durduğu
kapasitedir. Oturumu kapatan tek şey `POST /accounts/reset` — "çıkış yap"
düğmesi — ve başka bir Anthropic hesabına geçmenin yolu da budur.

Dolayısıyla kapasite aynı anda tek çalıştırmadır ve hiçbir hesap bağlı değilken
oluşturulan bir task kuyruğa alınmaz, reddedilir: harcanacak kimlik yoktur ve
CLI'ın kendi oturumuna düşmek, kuyruğu Mimir'in bilerek dokunmadığı hesap
üzerinden boşaltmak olurdu. Mimir hiçbir kimlik bilgisini okumaz, taşımaz,
saklamaz — onu Keychain tutar.

Daemon'un kendi model çağrıları — refine, distill, recap — dispatcher'dan
geçmez ama aynı hesabı harcar: "bunu hangi hesap ödedi?" sorusunun tek yanıtı
vardır.

**Biten token bütçesi pipeline'ı durdurur, başarısız etmez.** Bir çalıştırma
görev ortasında hesabın limitine takıldığında kart `failed` olmaz: `session_id`
üzerinde kalarak `queued`'a döner, yuva tutulur — harcayacak şeyi kalmamış bir
hesaba yeni iş verilmez — ve olan biten kalıcı bir loga yazılır
(`GET /coding-tasks/queue/limits`): limite takılan çalıştırma, arkasında
bekletilen her kart ve pencerenin yenilendiği an. Yenilenme saati için tek bir
uyandırma kurulur — CLI'ın kendi bildirdiği saat (`rate_limit_event` ya da
kullanım limiti mesajının `…|<unix>` eki; hiçbiri yoksa 15 dakika) — ve o an
gelince kuyruk, her zamanki gibi pompalanır. Hiçbir şeye basılmaz; devam eden
çalıştırma `--resume` ile kaldığı yerden sürer, görevi baştan yapmaz. Duraklama
yeniden başlatmayı da aşar: daemon açılışta onu logdan geri kurar, bir CLI
çağrısı harcayarak yeniden keşfetmez.

Gereksinim: `$PATH` üzerinde ve giriş yapılmış `claude` CLI.

---

### Bölge araması Google kimlik bilgisi istemiyor

`internal/regionsearch` sağlayıcıları tek bir sabit sırayla sorar, **önce
bedava olan**:

| Sıra | Sağlayıcı | Maliyet | Verdiği |
|---|---|---|---|
| 1 | `internal/mapscrape` — halka açık Maps sonuç akışı, yerel bir Playwright konteyneri render eder | yok; kimlik bilgisi gerekmez | ad, koordinat, puan, yorum sayısı, bazen web sitesi ve adres |
| 2 | `internal/mapsllm` — aynı sayfa Crawl4AI ile çekilir, claude haiku okur | model token'ı; Google parası değil | sayfa render olduğunda aynı alanlar |
| 3 | `internal/maps` — Google Places API | her istek faturalanır | yukarıdakiler + telefon, biçimli adres ve Google `types[]` |

Model aynı zamanda scraper'ın kendi kurtarma yolu: seçiciler hiçbir şey
okuyamadığında — o markup Google'ın ve haber vermeden değişir — render edilmiş
sayfa aynı profille yeniden okunur, bölge boş bildirilmez. Bir şey ayrıştırabilen
akış modele hiç gitmez, ve buradaki hiçbir prompt sayfada olmayan bir şirketi
üretemez.

Konteyner operatörün derdi değil: onu kapalı bulan bir arama, daemon'un yanına
kurulan compose dosyasıyla `docker compose up -d` çalıştırır, sağlık kontrolünü
bekler ve isteği bir kez tekrarlar. `make maps-up` hâlâ elle başlatır. Kaynak
bilgisi her satırla birlikte gider — kazınmış bir `place_id` `mapscrape:` önekini
taşır, yani faturalı bir satırın üzerine asla yazamaz — ve rapor, `maps_search`
yanıtı ve masaüstü rozeti hangi kaynağın cevapladığını söyler.

### Excel çıktısı

`POST /maps/leadgen/export` aramayı çalıştırır ve
`~/Library/Application Support/mimir/exports/` altına bir `.xlsx` yazar: bir özet
sayfası, ardından **her kategori için bir sayfa**. Her satırda şirket, web
sitesi olup olmadığı, web sitesi, telefon, e-posta, adres, puan, koordinat,
şirketi hangi kaynağın bulduğu ve iletişim bilgisini hangi katmanın bulduğu var.

`enrich: true` ile daemon her şirketin kendi sitesini bir kez açar ve iletişim
bilgilerini oradan okur — önce `tel:`/`mailto:` bağlantısı ya da alt bilgideki
numara, yalnızca onların bulamadığı sayfalarda claude haiku, ve modelin cevabı
da aynı kalıplardan geçirilir. Hiçbir şey tahmin edilmez: boş bir telefon hücresi
"arandı, bulunamadı" demektir ve yöntem sütunu hangi katmanın baktığını söyler.

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
| 4 | **Erişim mesajı** | evet | Şirket ve kanal başına bir taslak; o şirketin bilgileri + kategorisinin 3. aşama boşluk analizi + o kanalın kural dosyası verilir. Kanallar: `email`, `whatsapp`. | `outreach_emails` (`place_id, channel, prompt_version` ile anahtarlanır; `prompt_version` model *ve* kural dosyasının hash'ini taşır, bölge yeniden çalıştırmasının uyduğu bir `status` ile) |

### Modeli seçmek

2–4. aşamalar *sınıfa* göre yönlendirilir — tek seferlik sıkıştırma ücretsiz
`agy` katmanına, sentez `claude`'a — ve bir çalıştırma hiçbir şey istemediğinde
aldığı budur. Bunun yerine bir çalıştırma bir sağlayıcı ve model adı
verebilir (`provider` / `model`, ya da masaüstündeki lead-gen ekranının
seçicisi); o zaman üç model aşamasının üçü de onu harcar. Çift, hiçbir yere
gitmeden önce `config.LLMProviders`'a karşı denetlenir, çünkü iki metin de bir
alt sürecin argv'sine dönüşür; tanınmayan bir çift 400'dür, sessizce
varsayılana düşmek değil. Bir seçim ayrıca erişilebilirlik yedeğini de kapatır
ve okuduğu önbellekleri ad alanına ayırır — farklı bir modelle yapılan
çalıştırma, öncekinin yanıtlarını tekrarlamak yerine gerçekten o modeli çağırır.
Bölge aramasının kendi model yedeği (`internal/mapsllm`) buna dâhil değildir:
o, kuruluş anında bağlanır ve `maps_search`'ün her çağıranı tarafından
paylaşılır.

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
| **Leadgen** | Maps pipeline'ı: bir bölge arama formu, kategori başına boşluk analizi kartları, ve onay kutulu bir şirket tablosu. İşaretlenen şirketler için alttaki çubuk ne harcanacağını söyler (şirket × kanal) ve `POST /maps/outreach`'i çağırır; taslaklar kanal sekmeleriyle tek tek okunur, gönderildi / atla `POST /maps/outreach/status`'a bağlıdır. `report.notes` birebir gösterilir. |
| **Ayarlar** | Operatörün kendi ayarları tek ekranda: lead-gen'in harcayacağı varsayılan model, ve e-posta ile WhatsApp kural dosyalarının düzenleyicileri (yol, "varsayılana dön", ⌘S). Kural dosyası taslak isteminin parçası olduğu için kaydetmek eski taslakları geçersiz kılar ve ekran bunu söyler. |

Paketleme: `tauri.conf.json`, sertleştirilmiş çalışma zamanı ve
`entitlements.plist` ile `app` + `dmg` üretir. İmzalama/noterleme derleme
zamanında ortam değişkeni tabanlıdır (`APPLE_SIGNING_IDENTITY` + Apple-ID / API
anahtarı kimlik bilgileri); anahtarsız bir derleme yine de ad-hoc bir uygulama
üretir.

- **Katalog** — Shopify ya da IKAS CSV'sinden (ürünler ya da özel alanlar) içe
  aktarılmış bir ürün kataloğu, mağazanın kendi HTML'inden okunmuş marka
  sözlüğü, ve açıklamaların o sözlüğün içinde düzenlenmesi: eski ve yeni yan
  yana, `sandbox`'lı bir çerçevede, ve şeması sabit bir araç çubuğundan değil
  sözlükten kurulan bir zengin metin editörü. Bir ürün tam genişlikte açıldığında
  editör solda, canlı önizleme sağda ve önizlemenin üstünde bir `önce · sonra`
  anahtarı durur. Sütun eşlemesi, dosya tanınmış olsa bile her zaman açılabilir
  ve dosyanın kendi örnek değerleriyle dolu gelir. İstendiğinde yükleniyor —
  paketten ayrılmaya değecek kadar ağır olan tek ekran o.

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
| `accounts` | bağlı Claude hesabı: Mimir'in kendi yuva dizini | daemon açılışında ve kapanışında temizlenir — hesap uygulamadan uzun yaşamaz |
| `coding_runs` | çalıştırma meta verisi, maliyet, oturum id'si, transkript işaretçisi | yok (geçmiş) |
| `companies` | normalleştirilmiş Places/kazıma sonucu, `place_id` ile anahtarlı | çağıran tarafın verdiği uzun TTL (~30 gün) |
| `region_searches` | bir bölge aramasının döndürdüğü sıralı `place_id` listesi | aynı TTL; okumada ya hep ya hiç |
| `company_categorization` | normalleştirilmiş kategori + hangi katmanın yanıtladığı | `LeadgenCategoryVersion` yükseltmesi |
| `category_gap_analysis` | Claude'un sentezlediği kategori başına boşluklar/ihtiyaçlar | `LeadgenGapVersion` yükseltmesi; değişen bir şirket kümesi ıskalar |
| `outreach_emails` | kanal başına taslak mesaj + `status` (draft/sent/skipped) | `LeadgenEmailVersion` yükseltmesi, farklı bir model, **ya da düzenlenmiş bir kural dosyası**; "sent"/"skipped" yeniden üretimi engeller (SQL ile zorlanır) |
| `memory_episodes` | bir damıtılmış iterasyon: deterministik olgular her zaman, kabul edilmişse kısa bir özet | `MemoryPromptVersion` yükseltmesi özetleri yeniden üretir; olgular kalır |
| `memory_notes` | `context_remember` ile sabitlenen olgular | yok — ingest tarafından asla yeniden yazılmaz |
| `memory_ingest_state` | her transkriptin ne kadarının ayrıştırıldığı | transkript küçüldüğünde sıfırlanır (eklenmiş değil, değiştirilmiş demektir) |
| `memory_fts` | episode başlık, özet, dosya ve komutları üzerinde FTS5 indeksi | trigger'larla senkron tutulur |
| `leads` | Lead defteri: bulunmuş her işletme için tek satır, kategorisiyle. **Önbellek değil kayıt** — TTL yok, okuyan hiçbir şey silmez | yok; yeniden koşu alanları tazeler ama dolu bir alanı boşaltmaz |
| `lead_runs` · `lead_run_members` | Hangi arama hangi işletmeyi, ne zaman buldu | yok (geçmiş) |
| `chat_sessions` · `chat_turns` | Konuşmalar olduğu gibi — `memory_episodes`'ın kırptığı metin. Aynı ayrıştırmadan, aynı episode anahtarıyla yazılır | yok; yeniden okuma turu çoğaltmaz, günceller |
| `chat_fts` | İstem ve asistan yanıtları üzerinde FTS5 indeksi | trigger'larla senkron tutulur |
| `catalog_imports` · `catalog_products` | bir ürün kataloğu yüklendiği hâliyle: başlık, çerçeve ve **her satırın tüm ham hücreleri**, Mimir'in adını bilmediği sütunlar dahil. Önbellek değil kayıt — dışa aktarımı kayıpsız yapan şey budur, ve operatörün silmiş olabileceği bir dosyadan yeniden türetmeyi daemon denemez | yok; aynı dosyanın yeniden yüklenmesi aynı ürün id'lerini adresler ve onaylarını korur |
| `catalog_research` · `catalog_drafts` | bir yeniden yazımın ürün başına zaten ödediği şey: pazar araştırması ve metin, **kasıtlı olarak farklı iki anahtar** altında. Marka hash'i taslak anahtarında var, araştırma anahtarında yok; böylece marka sesini düzeltmek her açıklamayı yeniden yazdırır ve hiçbir rakip araştırmasını atmaz | yükseltilmiş `CatalogContentVersion`, farklı bir model, düzenlenmiş marka kiti ya da skill; elle düzenlenmiş bir taslak asla ezilmez (SQL ile) |
| `brain_node_versions` | Bir düğüm kaynağının taşıdığı her farklı içerik hash'i için bir kayıt: ne zaman, ne kadar büyüktü, ne anlama geliyordu. Dosya içeriği saklanmaz | eklemede `BrainVersionsPerNode` sınırına budanır |

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
| Katalog içe/dışa aktarma | — | — | — | — |
| Katalog marka sesi | — | damıtma için (sözlük hiçbir şey istemez) | — | — |
| Katalog yeniden yazımı (pano kartı) | pazar yarısı için Crawl4AI | evet | — | — |
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
