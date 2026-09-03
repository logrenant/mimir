# Changelog

Bu dosya [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) biçimini,
sürüm numaraları [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
kuralını izler.

## [2.11.1] — 2026-09-03

**İki hesap aynı anda açık kalıyor, ve `claude` artık her seferinde yeniden
erişim izni sormuyor.**

### Düzeltildi

- **Kabuğun ömrü artık soketin ömrü değil.** pty'yi websocket sahipleniyordu:
  her bağlantıda yeni bir `Start`, ve socket kapanınca `Close` kabuğa SIGHUP.
  Arayüz de aynı anda tek terminal mount ettiği için ikinci hesabı açmak
  birincinin viewer'ını unmount ediyor, o da soketi kapatıyor, o da **kabuğu
  öldürüyordu**. Artık oturumları `ptyterm.Registry` sahipleniyor: viewer
  ayrılır, kabuk çalışmaya devam eder. İki hesap aynı anda açık kalabiliyor.
- **Tekrar tekrar sorulan workspace-trust sorusu.** Aynı hatanın ikinci yüzüydü:
  öldürülen her oturumla birlikte, operatörün az önce verdiği "Yes, I trust this
  folder" cevabını taşıyan `claude` süreci de gidiyordu, ve profil her
  değiştiğinde sıfırdan bir `claude` başlıyordu. Oturum yaşadığı için cevap da
  yaşıyor.
- **`Registry.Kill` haritadan senkron siliyor.** Eskiden silme işini, kabuk
  gerçekten ölünce uyanan gözcü goroutine yapıyordu; bir oturumu öldürüp hemen
  "ne çalışıyor" diye soran çağıran, hâlâ çalıştığı cevabını alıyordu.

### Eklendi

- **Oturum geçmişi geri oynatma.** Bir profile yeniden bağlanan viewer, kabuğun
  o ana kadar söylediklerini (son 256 KiB) alıyor — profil değiştirip geri
  dönünce boş ekran değil, bıraktığın ekran geliyor.
- **`DELETE /terminals/{profile}`** — viewer kapatmak artık kabuğu bitirmediği
  için, bir oturumu kasten sonlandırmanın tek yolu. Kabuğu kapanmış bir panelde
  "yeniden başlat" düğmesi.

## [2.11.0] — 2026-09-03

**Bir lead artık aranabilir bir şey: telefonlar defterde, ve bir bölge tek bir
bölge.**

### Eklendi

- **İletişim zenginleştirme artık koşunun bir aşaması** (stage 1b). Daha önce
  yalnızca Export'ta çalışıyor ve cevabı hiçbir yere yazılmıyordu — yetmiş
  şirketlik bir defterde sıfır telefon olmasının sebebi buydu. Artık her koşuda
  çalışıyor ve `leads`'e yazılıyor, yani getirme şirket başına bir kez ödeniyor.
  Ölçülen: 0 → 40 telefon.
- **Web sitesi olmayanlar için DuckDuckGo araması.** Listede site yoksa enricher
  şirketin sitesini arayıp buluyor; dizin siteleri (Google, Facebook, firma
  rehberleri) eleniyor, çünkü küçük bir işletmeyi kendi adında geçen bu siteler
  onu geçiyor ve birini "web sitesi" diye kaydetmek hiç bulmamaktan kötü.
- **Bölge toplaması** (`GET /maps/leads/regions`). Seçici artık koşuları değil
  yerleri listeliyor: yirmi bir kez aranmış bir bölge tek bir "Denizli", yirmi
  bir satır değil. `GET /maps/leads?region=` ile filtreleniyor, büyük/küçük harf
  duyarsız — "denizli" ile "Denizli" iki ayrı yer değil.

### Değiştirildi

- **Ulaşılamayan şirketler `unknown` kategorisinde.** Ne telefonu ne sitesi olan
  bir şirket aranamaz, ve bir outreach listesi arayabildiklerinin listesidir;
  ticaretinin adı altında dosyalamak operatörü çıkmaz bir satıra götürürdü.
  `category_method` bunu `unreachable` olarak kaydediyor — sınıflandırma
  başarısızlığı değil, ulaşılamazlık.

## [2.10.0] — 2026-09-03

**Uygulamanın içindeki terminal artık gerçek bir terminal, ve bir modelin kotası
dolduğunda bölge sınıflandırması boş dönmüyor.**

### Eklendi

- **İnteraktif kabuk** (`internal/ptyterm`). Terminals ekranındaki `KABUK`
  bölümü, operatörün kendi giriş kabuğunu bir pty üzerinde çalıştırıyor —
  `$SHELL -l -i`, Terminal.app'in başlattığı sürecin aynısı. oh-my-zsh,
  eklentileri, prompt ve `.zshrc`'de tanımlı `claude-acct` fonksiyonu birebir
  çalışıyor, çünkü aynı rc dosyalarını okuyan aynı kabuk. İki profil:
  `salihdevran` prompt'a `claude`, `eziode` ise `claude-acct eziode` yazıyor.
  Komut exec edilmiyor, *yazılıyor* — `claude-acct` bir kabuk fonksiyonu, exec
  edilecek bir ikilisi yok, ve scrollback'te operatörün kendi yazacağı satır
  görünüyor. `GET /terminals/profiles`, `GET /ws/terminals/pty`.
- **İsim tabanlı kategori kuralı** (`CategoryForName`). Kazınan satırlarda
  Google'ın `types[]` etiketi yok, bu yüzden ücretsiz kural katmanı her zaman
  ıskalıyor ve her şirket modele düşüyordu; modelin kotası dolunca bütün bölge
  `unknown` dönüyordu. Artık isim ticareti açıkça söylüyorsa ("yazılım",
  "eczane") kural cevaplıyor. Ölçülen: 20 şirketin 18'i ücretsiz çözüldü.

### Düzeltildi

- **`claude` CLI'ın gerçek hatası artık görünüyor.** CLI kotası dolduğunda 1 ile
  çıkıyor, stderr'i *boş* bırakıp sebebi stdout'a JSON olarak yazıyor. Kod
  stdout'u atıp boş stderr'i raporladığı için "session limit" hatası "run
  `claude login`" tavsiyesine dönüşüyordu — düzeltmesi imkânsız bir tavsiye.
  Çıkış kodu sıfır olmasa da stdout okunuyor, ve 429 `ErrRateLimited` olarak
  auth hatasından ayrılıyor.

## [2.9.0] — 2026-09-03

**Hiçbir şey oturumla birlikte kaybolmuyor: bulunan işletmeler bir defterde,
konuşmalar olduğu gibi veritabanında, taranan dosyaların geçmişi kayıtlı.**

### Eklendi

- **Lead defteri** (task-63). Bir arama artık cevap verip unutmuyor. `leads`
  tablosu işletme başına tek satır tutuyor — kategorisiyle, TTL'siz, kimsenin
  okurken sildiği bir satır değil — ve `lead_runs` / `lead_run_members` hangi
  aramanın ne zaman neyi bulduğunu saklıyor. `companies` ve `region_searches`
  olduğu gibi kaldı: onlar önbellek, bu bir kayıt, ve ikisini aynı tabloya
  koymak süresi dolan bir satırın kaydı sessizce silmesi demekti. Aynı arama
  ikinci kez çalışınca `lead_runs` bir satır artıyor, `leads` artmıyor. Ücretsiz
  kazıma telefonu boş döndürdüğünde daha önce Places'ten gelmiş numara
  silinmiyor — `ON CONFLICT` dolu bir alanın üzerine boş yazmıyor.
  `GET /maps/leads`, `/maps/leads/categories`, `/maps/leads/runs`.
- **Kayıtlı işletmeler ekranı** (task-64). Lead-gen sekmesi artık boş bir panelle
  değil, defterle açılıyor — "elimde hangi işletmeler var" sorusunun dürüst
  cevabı bu. Süzme ve kategori sayımı daemon'da yapılıyor; ekranda duran sayfayı
  süzmek "baktığınız iki yüz satırda ara" demek olurdu. Koşu geçmişi bir süzgeç,
  taslak kararı satırın yanında.
- **Sohbet arşivi** (task-65). Claude Code oturumları, Terminals'taki kodlama
  koşuları ve agy konuşmaları artık **kelimesi kelimesine** veritabanında:
  `chat_turns` her turu, `chat_fts` de aranabilir hâlini tutuyor. Bugüne kadar
  yalnızca damıtılmış özet saklanıyordu ve ham metin `~/.claude/projects`
  altındaki JSONL dosyasında duruyordu — o dosya silinince konuşma da gidiyordu.
  Arşiv aynı ayrıştırmadan besleniyor (ikinci bir okuma yok, ikinci bir imleç
  yok) ve **hiç model çağırmıyor**, ki aylardır biriken bir geçmişi almak fatura
  değil taşıma olsun. `GET /chat/sessions`, `/chat/sessions/{id}`,
  `/chat/search`.
- **Dosya sürüm geçmişi** (task-67). Değişiklik tespiti zaten çalışıyordu — Brain
  her taramada dosyanın ham baytlarının SHA-256'sını karşılaştırıyor ve
  `~/development` ile `~/Documents` üzerinde on beş dakikada bir geçiyor — ama
  yeni değerlendirme eskisinin üzerine yazılıyordu. Artık her farklı içerik
  hash'i için bir satır: ne zaman, ne kadar büyüktü, ve o hâliyle ne anlama
  geliyordu. Dosya içeriği saklanmıyor; git zaten baytları tutuyor, tutmadığı
  şey okuma. Sürüm satırını `UpsertBrainNode` yazıyor, çağıran değil — eski hash
  yalnızca orada görünür, ve böylece her ingest yolu geçmişi bedavaya kazanıyor.
- **"Değişti, yeniden okundu"** (task-67, task-68). Tarama artık yeni bir dosyayı
  değişmiş bir dosyadan ayırıyor; konsolda ayrı bir satır olarak görünüyor, ve
  düğüm panelinde bir sürüm zaman çizelgesi var — bir satıra tıklayınca o
  sürümün değerlendirmesi açılıyor.

- **Lead-gen çalıştırmasında model seçimi.** Arama formuna iki açılır liste
  geldi: sağlayıcı (`agy` — ücretsiz, ya da `claude` — kotanızdan) ve o
  sağlayıcının modeli. Seçim, bir çalıştırmanın üç model aşamasının üçünü de
  birden bağlıyor (`RunRequest.Selection` → `Run`'ın başında `.With(sel)`), ki
  bir arama yarısı bir modelde yarısı başkasında bitmesin. Seçim yapılmadığında
  hiçbir şey değişmiyor: sınıfa göre yönlendirme hâlâ varsayılan.
  `GET /llm/providers` izin listesini yayımlıyor; `POST /maps/leadgen` bir
  `provider`/`model` çifti alıyor ve listede olmayanı 400 ile reddediyor —
  ikisi de bir alt sürecin argv'sine dönüştüğü için sessizce varsayılana
  düşmek yanlış cevap. Bir seçim erişilebilirlik yedeğini de kapatıyor:
  "bunu agy'de çalıştır" dedikten sonra sessizce claude'a geçmek, seçilmeyen
  bir bütçeyi harcamak olurdu. Aşamaların önbellekleri seçimle ad alanına
  ayrılıyor, yoksa farklı bir modele geçen çalıştırma öncekinin cevaplarını
  okur ve seçici hiçbir şey yapmamış gibi görünürdü.

### Düzeltildi

- **`could not reach the daemon: timeout: global`.** Tauri kabuğu her daemon
  çağrısına sabit 30 saniye veriyordu. Bir lead-gen çalıştırması ise bir bölgeyi
  kazıyıp her şirketi sınıflandırıp kategori başına bir sentez üretiyor —
  tasarımı gereği dakikalar sürüyor — ve bağlantı tüm bu süre boyunca açık
  duruyor. Sonuç hataların en kötüsüydü: iş bitiyordu, cevap çöpe gidiyordu,
  ekranda "daemon'a ulaşılamadı" yazıyordu. Artık bütçe rotaya göre veriliyor:
  pipeline rotaları 45 dakika, geri kalanı 2 dakika, bağlanma ise ayrı ve kısa
  (5 sn) — daemon loopback'te, bağlanamıyorsa ölüdür. Zaman aşımı mesajı da
  hangi çağrının ne kadar beklediğini söylüyor.
- **Ağ bütçeleri paket kaybeden bir hat için yeniden ölçüldü.** Yeniden iletim
  saniyeler yiyor ve eski değerler çalışan bir isteği başarısız bir aşamaya
  çevirecek kadar dardı: `SearchTimeout` 10→30 sn, `CrawlTimeout` 45→150 sn,
  `RefineTimeout` 60→180 sn, `ResearchTimeout` 120→360 sn, `GMapsPageTimeout`
  30→90 sn, `AgyPrintTimeout` 90→240 sn, `MapScrapeTimeout` 120→300 sn. Sabit,
  ayar düğmesi değil (SD-1). Yoklama bunun dışında tutuldu — yeni
  `LLMHealthTimeout` (20 sn): "bu CLI kurulu ve giriş yapılmış mı" sorusunun
  cevabı ya hemen gelir ya hiç, ve kullanılamayan tek bir sağlayıcının
  `/diagnostics`'i dakikalarca açık tutması gerekmiyor.

### Değişti

- **İki metin tavanı ayrıştırıcıdan tüketiciye taşındı.** `MaxPromptChars` (600)
  ve `MaxAssistantChars` (1200) artık `internal/memory.toRow` içinde
  uygulanıyor. Aynı ayrıştırmanın iki tüketicisi var ve talepleri zıt: özet satırı
  küçük kalmalı, arşiv ise tam olarak o kırpılan metni saklamalı. Ayrıştırıcı
  kırpsaydı arşiv her transkripti ikinci kez okumak zorunda kalırdı.

## [2.8.0] — 2026-09-03

**Lead-gen bir ekran değil, bir çalışma alanı oldu — ve kategoriler artık
gerçekten dolu.**

### Düzeltildi

- **Her şirket `unknown` kategorisine düşüyordu.** Kazınan satırlarda Google
  `types[]` yok, dolayısıyla kural katmanı boş dönüyor ve karar modele kalıyor;
  o model de task-51'den beri agy-only ve agy bu makinede girişsiz. Artık
  **yalnızca sınıflandırma** profili, sağlayıcı kullanılamıyorsa claude haiku'ya
  düşüyor. task-51'in kapattığı makine çapındaki distil yedeği kapalı kalıyor:
  bu yedek bir batch (yirmi şirket, birkaç yüz token) ve yalnızca operatörün
  başlattığı bir çalışmada devreye giriyor. Kötü bir cevap tekrar denenmiyor —
  o ikinci görüş olurdu, kullanılabilirlik değil.

### Değişti

- **Distil katmanı `gemini-3.8-flash-high`'a taşındı** (task-61). Sabitlenmiş bir
  model etiketini bumplamak kendi başına bir iştir (SD-5); `-high` soneki modelle
  birlikte taşındı, çünkü gerekçesi değişmedi. Bu aynı zamanda bir arızayı da
  kapatıyor: bu makinede distil çağrıları boş stderr ile `exit status 1` veriyordu,
  daemon'un birebir aynı çağrısı (şema ve scratch dizini dahil) 3.8'de `SUCCESS`
  dönüyor. Önbellekler geçersizleştirilmedi — 3.7 ile üretilmiş özetler hâlâ
  doğru özetler, ve `brain_nodes` her satırın hangi model tarafından yazıldığını
  zaten kaydediyor.
- **Şirket listesi.** Sol tarafta kategori rayı: her kategori, kaç şirket,
  kaçının web sitesi yok ve kütlenin nerede olduğunu gösteren iki piksellik bir
  metre. Sağda sıralanabilir, süzülebilir bir tablo — "web" sütunundaki *yok*
  ekrandaki tek sıcak renk, çünkü operatörün aradığı şey o. Seçilen satırın
  detayı yanda, seçili kategorinin boşluk analizi altta.
- **Taslak ekranı.** Karar listesi olarak bir kuyruk (önce kararsızlar), sağda
  tek seferde bir mektup — okunabilir ölçüde, ortalanmış, monospace değil.
  `↑↓` gez, `g` gönderildi, `a` atla; işaretlemek bir sonrakine geçirir.
- **Dürüst hata durumu.** Hiçbir şey sınıflandırılamadıysa başlık bunu söylüyor
  ve çalışmanın kendi gerekçesini yazıyor — tek bir `unknown` kovasını sonuç
  gibi göstermiyor.
- Ekranın metinleri Türkçeleşti; Brain ve Dashboard ile aynı dil.
- Sayma işleri `lib/leadgen.ts`'e taşındı ve test edildi: JSX içinde alınan
  karar, kimsenin denetleyemediği karardır.

## [2.7.0] — 2026-09-02

**Kazınan şirketler artık bir Excel dosyası, ve scraper'ın kırıldığı yerde bir
model duruyor.**

### Eklendi

- **Feed için model yedeği (claude haiku).** `internal/mapscrape`'in seçicileri
  hiçbir şey okuyamadığında — o markup Google'ın ve haber vermeden değişir —
  render edilmiş sayfa `internal/refine`'ın yeni `ExtractFeed` profiliyle
  yeniden okunuyor. Bir şey ayrıştırabilen akış modele hiç gitmiyor: seçici yolu
  id'yi ve koordinatı URL gramerinden okuyor, model bunu yapamaz. Kurtarılan her
  satır yine `mapscrape:` önekini taşıyor.
- **Üçüncü bir sağlayıcı.** `internal/mapsllm`: sidecar hiç ayağa kalkamayan
  makinede aynı Maps sayfası Crawl4AI ile çekilip aynı profille okunuyor. Sıra
  artık sidecar → model → Places, ve `regionsearch.Standard` bu sırayı tek yerde
  kuruyor.
- **İletişim zenginleştirme.** `internal/contacts` her şirketin kendi sitesini
  bir kez açıp telefon/e-posta çıkarıyor: önce `tel:`/`mailto:`/alt bilgi
  kalıpları, yalnızca onların bulamadığı sayfalarda claude haiku — ve modelin
  cevabı da aynı kalıplardan geçiriliyor, yani "beş altı yedi" diye bir telefon
  dosyaya girmiyor. Hiçbir alan tahmin edilmiyor; her satır hangi katmanın
  baktığını kaydediyor, böylece boş hücre "arandı, bulunamadı" demek oluyor.
- **Excel çıktısı.** `POST /maps/leadgen/export` bir `.xlsx` yazıyor: özet
  sayfası + **her kategori için bir sayfa**, her satırda ulaşım bilgileri ve
  "web sitesi var mı" sütunu. Masaüstünde "Excel'e aktar" düğmesi, iletişim
  tamamlama anahtarı ve "Finder'da göster" — Finder komutu yalnızca exports
  dizinini açabiliyor, WebView'a genel bir dosya açıcı verilmedi.

### Değişti

- `internal/regionsearch` sıralı bir sağlayıcı listesi tutuyor (ikili değil).
- `internal/refine` altı profile çıktı; iki yeni çıkarım profili distil yerine
  Reason (claude) sınıfında: ikisi de ancak daha ucuz bir şey başarısız olduktan
  sonra çalışıyor, ve en çok düşen katmana bağlı bir yedek yedek değildir.
- Yeni bağımlılık: `github.com/xuri/excelize/v2 v2.9.1` (sabit sürüm).

## [2.6.0] — 2026-09-02

**Bölgesel şirket araması artık hiçbir Google API anahtarı istemiyor.** Ücretsiz
scraper (`internal/mapscrape` + `deploy/playwright-maps`) task-28'den beri
duruyordu ama ulaşılamıyordu: `cmd/mimir-daemon` lead-gen pipeline'ını yalnızca
`PlacesAPIKey` varsa kuruyordu, yani anahtarsız makinede ne `/maps/*` rotaları
ne `maps_search` ne de lead-gen vardı.

### Düzeltildi

- **Anahtarsız makinede bölge araması diye bir şey yoktu.** Artık kaynak sırası
  tek bir yerde (`internal/regionsearch`) ve **önce bedava olan** deneniyor:
  yerel scrape birincil, faturalı Places yedek. Bunun iki sonucu var — anahtarsız
  makinenin tam bölge araması oluyor, ve anahtarsız yol artık her makinenin
  geçtiği yol, yalnızca anahtarsızların düştüğü test edilmemiş bir dal değil.
- **Sıra iki yerde yazılıydı ve ikisi aynı şeyi söylemiyordu.** Pipeline "Places,
  sonra scrape" diyordu, `maps_search`'ün kaydı ise "Places ya da hiç". İkisi de
  artık aynı router'ı kullanıyor.

### Eklendi

- `internal/regionsearch` — sağlayıcı sırası, sağlanabilirlik ve hangi kaynağın
  cevapladığı. Faturalı bir cevap bunu not olarak söylüyor.
- **Sidecar'ı daemon kendi başlatıyor.** Bağlantı reddedilirse
  `mapscrape.EnsureRunning` `docker compose up -d` çalıştırıp `/health`'i bekliyor
  ve istek bir kez tekrarlanıyor. Kimlik bilgisi istemeyen bir yetenek terminal de
  istememeli. Compose dosyası `scripts/install-agent.sh` ile binary'nin yanına
  kuruluyor — launchd ile başlayan daemon'un bulabileceği bir repo yok.
- `maps_search` her makinede kayıtlı; açıklaması hangi kaynağın cevaplayacağını
  söylüyor ("hiçbir şey harcamaz" / "faturalanır"), yanıt `source` ve notları
  taşıyor. `/diagnostics` `region_sources` + `region_search_free` bildiriyor.
- Masaüstü: sonuç başlığında `scrape · ücretsiz` / `places · faturalı` rozeti —
  boş telefon sütunu artık eksik veri değil, kaynak farkı olarak okunuyor.

## [2.5.0] — 2026-09-02

**Hesap ayrımı artık Mimir'de de çalışıyor.** Mekanizma task-37'den beri
doğruydu; eksik olan kayıttı, ve kayıt hiç yapılmamıştı: `accounts` tablosu boş
olduğu için hesap seçicide yalnızca "Otomatik" vardı, dispatcher tek bir
sentetik varsayılan yuva görüyordu ve ikinci kimlik hiç harcanmıyordu.

### Düzeltildi

- **İkinci hesap hiç kullanılmıyordu.** Bir yuva, ancak biri uygulamada dizinini
  seçtiğinde vardı. Artık `~/.claude-accounts` otorite: daemon açılışta tarıyor,
  `POST /accounts/scan` istek üzerine tarıyor, ve kayıt dizine göre idempotent.
  Kural kabuktakiyle birebir aynı (`claude-acct` / `claude-who`): varsayılan yuva
  = değişkenin *hiç* set edilmemesi, `default`/`a`/`salihdevran` adlı bir dizin de
  o varsayılana çöker. İki kimlik = aynı anda iki task.
- **Daemon'un kendi model çağrıları rastgele bir hesabı harcıyordu.**
  `internal/llm/claude.go` hiç `cmd.Env` kurmuyordu, yani refine/distill/recap
  daemon'u kim başlattıysa onun kimliğini — bir dev kabuğunda operatörün kendi
  oturumunu — harcıyordu. Artık `account.Environ` uygulanıyor ve harcanacak yuva
  işaretlenebiliyor (`POST /accounts/background`, boş id = CLI'ın varsayılanı).
  Aynı düzeltme oturum değişkenlerinin (`CLAUDECODE`, oturum id'si, mesajlaşma
  soketi) alt süreçlere sızmasını da bitiriyor.

### Eklendi

- `0015_account_slots.sql` — `accounts` üzerinde `discovered` ve `is_background`,
  ve en fazla bir arka plan yuvası olsun diye kısmi tekil indeks.
- `internal/account/discover.go` — `Discover` + `Registry.Sync`. Yalnızca ekler:
  dizini silinmiş bir yuvanın satırı kalır, çünkü ona iğnelenmiş bir run olabilir
  ve doğru rapor kırmızı bir probe'dur, kuyruğun altından kaybolan bir satır değil.
- `internal/config` — `ClaudeAccountsDir` (test override'ı
  `MIMIR_CLAUDE_ACCOUNTS_DIR`). Kabuğun kendi `CLAUDE_ACCOUNTS_DIR`'ı bilerek
  okunmuyor: daemon launchd altında onu görmüyor, okumak da hangi hesapların var
  olduğunu "daemon'u kim başlattı"ya bağlardı.
- Masaüstü: taramadan gelen satırda "unut" yerine "taramadan", her satırda "arka
  plan" işareti, ve yenile artık listelemeden önce tarıyor.

### Değişti

- **Taramadan gelen bir yuva uygulamadan unutulamıyor** (`ErrAccountDiscovered`,
  409). Dosya sistemi otorite: unutmak, bir sonraki taramanın geri aldığı bir söz
  olurdu. Kaldırmak dizini silmekle olur.

## [2.4.0] — 2026-09-02

**Brain yeniden kuruldu, ve ucuz iş ucuz modele taşındı.** Bir önceki sürümde
eklenen düğüm katmanı hiç çalışmıyordu; bu sürüm onu store'un üstüne yeniden
kuruyor ve aynı anda model çağrılarını tek bir çıkışın arkasına alıyor.

### Düzeltildi

- **Brain'in üç aracı da her çağrıda hata veriyordu.** Handler'lar düz `string`
  döndürüyordu; `internal/mcp/finalize.go` fail-closed olduğu için her çağrı
  `mcp: response not refined` ile düşüyordu. Testler yeşildi çünkü kanonik liste
  testi araçların yalnızca *adını* sayıyor, hiçbirini çağırmıyor — ve üç yanıt
  tipi `chokepoint_test.go`'ya, yani tam bu hatayı yakalamak için var olan teste,
  eklenmemişti. Dördü de artık orada.
- **Aynı kaynağı iki kez ingest etmek iki düğüm üretiyordu.** Kimlik `UnixNano`
  ile tuzlanmıştı, dolayısıyla bir repo iki kez okununca birbirine bağlanan iki
  ayrı düğüm çıkıyordu. Kimlik artık `(project_path, kind, source_key)`.
- **Özet başarısız olunca hiçbir şey kaydedilmiyordu.** Commit mesajı "bağlama
  hatası ingest'i düşürmüyor" diyordu ama özetleyici düşünce `IngestData` erken
  dönüyordu. Artık düğüm önce yazılıyor, sonra bağlanıyor: sağlayıcı yoksa düğüm
  başlığıyla ve etiketsiz duruyor, aranabilir kalıyor.
- **Testler ağa çıkıyordu.** Sahte bir `claude` yazan testler, distil'in birincil
  sağlayıcısı `agy` olunca PATH'teki gerçek binary'e gidiyordu. `e2e` bu yüzden
  29 saniye sürüyordu; şimdi 7. Boş bir `AgyCLIPath` artık `"agy"`ye
  varsayılmıyor, sağlayıcı kendini kullanılamaz ilan ediyor.

### Değişti

- **Model çağrıları sınıfa göre yönlendiriliyor** (`internal/llm`). Damıtma —
  sayfa özeti, episode recap'i, düğüm etiketi, sınıflandırma — `agy` ile
  `gemini-3.7-flash-low` üzerinde; sentez ve gap analizi `claude-haiku-4-5` ile;
  coding runner'a dokunulmadı. `agy` yoksa ya da kotası dolduysa router claude'a
  düşüyor. `docs/ROADMAP.md` §B.1'in ilk maddesi sahip kararıyla bu doğrultuda
  yeniden yazıldı.
- **İki kopya exec kodu teke indi.** `internal/refine` ve eski Brain aynı
  subprocess dansının iki elle yazılmış kopyasını taşıyordu ve ikisi çoktan
  ayrışmıştı. `internal/refine` artık `internal/llm`'i çağırıyor;
  `ErrClaudeUnavailable` sentinel'i yerinde duruyor.
- **Etiket çıkarımı metin ayrıştırmayı bıraktı.** `agy --json-schema` ile
  yapılandırılmış çıktı alınıyor. Eski kod `SUMMARY:` öneki bulamayınca ham
  yanıtı değerlendirme sanıp sıfır etiketle kaydediyordu — ve sıfır etiketli bir
  düğüm ne aramada ne kümelemede görünüyordu. Sıfır etiket artık reddediliyor.
- **Kümeleme ingest başına O(N)'den O(1) yazmaya indi.** Eski `LinkNode` diskteki
  her düğümü okuyor, tek ortak etiketi olan herkese kenar atıyor ve her komşunun
  dosyasını yeniden yazıyordu. Şimdi adaylar tek bir FTS sorgusundan geliyor,
  ucuz kenarlar yerel Jaccard ile hesaplanıyor, ve gerisine tek bir ilişki
  geçişi karar veriyor — kenarlar tek yönde, tek transaction'da yazılıyor.

### Eklendi

- **`brain_related`** — bir düğümden komşularına yürüyor, ikinci bir arama
  gerekmeden.
- **Alias'lar.** Damıtma, düğümün metninde geçmeyen eşanlamlı ve komşu terimleri
  de üretiyor ve bunlar FTS indeksine giriyor. Vektör indeksi olmadan semantik
  erişimin karşılığı bu: "corruption prevention" araması, o kelimelerin hiçbiri
  geçmeyen bir düğümü buluyor.
- **`MIMIR_GITHUB_TOKEN`.** Eskiden `os.Getenv("GITHUB_TOKEN")` istek yolunun
  ortasında okunuyordu (SD-1 ihlali). Artık `config.Load`'da, bir kez, ve
  `docs/SECURITY.md`'de ikinci operatör kimlik bilgisi olarak yazılı.

- **`make install-mcp`.** Bu repoda hiçbir şey mimir-mcp'yi bir istemciye
  kaydetmiyordu; `claude mcp add` yalnızca INSTALL.md'de elle yazılacak bir
  komut olarak duruyordu ve bu makinedeki sonucu, başka bir dizine geçince
  sessizce yok olan proje kapsamlı bir kayıttı. Artık tek komut: `claude` (user
  scope), `agy` + Antigravity IDE (ikisi aynı `mcp_config.json`'ı okuyor),
  `gemini` CLI ve VS Code. Dördünün de kendi `mcp add`'i var, o yüzden JSON'ları
  elle birleştirilmiyor. İkili checkout'ta bırakılmıyor, support dizinine
  kuruluyor: repo taşınınca dört yapılandırma birden kırılmasın.
- **Oturum ön-kontrolü.** Claude Code'da `SessionStart`, agy'de `PreInvocation`
  (agy'nin `SessionStart` olayı yok; `invocationNum` koruması olayı konuşma
  başına bire indiriyor). İkiliyi kontrol ediyor, istemcinin kaydı düşmüşse
  yeniden kaydediyor, daemon cevap vermiyorsa `launchctl kickstart` ile
  kaldırıyor — on yarım saniyelik deneme, sonra dürüst bir notla vazgeçiyor — ve
  modele bir hafıza olduğunu, repoyu yeniden okumadan önce `project_context`
  çağırmasını söylüyor. Oturumu asla bloklamıyor, her yolda exit 0.

- **Brain artık kendini kaydediyor.** Bir şeyin hatırlanması için modelin
  `brain_ingest_data` çağırmayı hatırlaması gerekiyordu — yani tam olarak
  güvenilmeyecek şey. Üç kaynak diskte zaten duruyordu ve üçü de **sıfır model
  çağrısıyla** okunuyor: M8'in çoktan damıttığı Claude Code episode'ları oturum
  düğümüne ve dokundukları her dosya için bir dosya düğümüne dönüşüyor; agy
  konuşmaları `Stop` hook'unun bıraktığı spool'dan çekiliyor; commit'ler
  `git log`'dan okunuyor. Operatörün gerçek store'unda tek tikte 147 oturum,
  371 dosya, 16 commit ve 1015 kenar.
- **Terminal işleri commit üzerinden yakalanıyor.** Her komutu kaydeden bir
  kabuk hook'u bilerek yapılmadı: gürültülü, komut satırına yazılan sırları
  toplar, ve bir hafta sonra hiçbirinin değeri kalmaz. Commit, birinin saklamaya
  değer bulduğu kısım ve zaten nedenini anlatan bir mesaj taşıyor.
- **`brain_scan_repo`.** Bir repoyu bir kez okuyup düğümlere çeviriyor, böylece
  bir oturumun tanımadığı bir dosya hakkındaki ilk sorusu dosyayı açmadan
  yanıtlanıyor. Bu paketteki tek bilerek pahalı iş, o yüzden `dry_run` faturayı
  harcamadan gösteriyor ve `content_hash` değişmemiş dosyayı bedavaya atlıyor:
  ikinci tarama hiç model çağrısı yapmıyor, yarıda kesilen tarama kaldığı yerden
  devam ediyor. Bu repoda ölçüldü: 365 dosya, elemelerden sonra 320; 6 dosyalık
  batch 60 saniye, yani ilk tam geçiş ~50 dakika.
- **Elemeler boyutla ilgili değil.** Kilit dosyaları, `testdata`/golden
  fixture'ları, fontlar, ikonlar ve minified bundle'lar gayet okunabilir ve
  sonraki bir oturuma hiçbir şey vermiyor — ama her biri, önemli bir dosyayla
  aynı maliyeti çıkarıyor.
- **Dosya düğümleri birikiyor.** Bir dosyaya elli oturum dokunduysa
  `brain_related` o dosyada "bu dosyaya ne oldu" sorusunu yanıtlıyor — başka
  hiçbir yüzeyin yanıtlamadığı bir soru.

### Şema

- `0014_brain_capture.sql` — `brain_nodes.content_hash` (bir yeniden taramanın
  değişmemiş dosyayı atlayabilmesi için) ve `brain_capture_state` (imleçler).
- `0013_brain.sql` — `brain_nodes`, `brain_edges`, `brain_fts`. Markdown dosya
  deposu tamamen kaldırıldı; `data/brain/` `.gitignore`'dan çıktı.

### Notlar

- `agy`'nin araç kısıtlama bayrağı yok. Yerine `--sandbox`, repo olmayan boş bir
  scratch çalışma dizini ve `MIMIR_NESTED=1` var — sonuncusu `mimir-mcp`'nin
  sıfır araçla açılmasını sağlıyor, yani global kayıtlı Mimir'a özyineleme
  kapalı. Bu, claude yolunun garantisinden zayıf ve `docs/SECURITY.md` bunu
  olduğu gibi yazıyor.
- `docs/CAPABILITIES.md` coding runner'ın `--mcp-config` ile Mimir'ı iç içe
  yüklediğini iddia ediyordu; kod hiçbir zaman öyle yapmadı. İddia kaldırıldı.
- Oturum etiketlerinde iki hata, ikisi de teste yakalandı. Etiketler
  `facts.commands`'tan türetiliyordu; orası komut satırı değil düzyazı açıklama
  tutuyor ("Run full make check"), yani ilk kelime bir fiil — neredeyse her
  oturum `check`, `find`, `read` etiketi alıyordu. Ve mutlak yollardan
  türetiliyordu, o yüzden ilk iki parça paket adı değil makinenin dizin düzeni
  oluyordu (`internal-store` yerine `repo-internal`). Artık yalnızca göreli
  yollardan.
- `/brain/*` route'ları ve `make brain-scan` planlanmıştı, yapılmadı: birincinin
  tüketicisi yok, ikincisi ise batch'li tarama sayesinde gereksiz kaldı — bir
  iş kuyruğuna, iş kimliğine ve ilerleme akışına ihtiyaç kalmadı.
- `-race`, okumakla görülmeyecek bir şeyi yakaladı: `Scan`, `Ingest`'i eşzamanlı
  çağırıyor ama ne `brain.Store` ne `brain.Completer` bunu şart koşuyordu. İkisi
  de artık koşuyor ve sahteleri korumalı.
  `desktop/src/lib/modules.ts` bu kuralı zaten yazıyor — gerçek bir route'a
  bağlı olmayan yüzey, kabuğun daemon hakkında yalan söylemesinin en hızlı yolu.

## [2.3.0] — 2026-09-02

**Model seçimi ve gerçek bir dashboard.** Bir task'ın hangi modeli harcayacağı
artık seçilebiliyor, ve uygulamanın açıldığı ekran ilk kez canlı veri gösteriyor.

### Eklendi

- **Task başına model.** `coding_runs.model` ilk günden beri vardı ama bir karar
  değil bir yankıydı: `Create` sabiti damgalıyor, `args()` aynı sabiti CLI'a
  geçiriyordu. Artık task yazılırken seçiliyor. Liste `internal/config`'te bir
  sabit (SD-1) ve `GET /coding-models` ile yayınlanıyor — masaüstündeki seçici
  o listenin görünümü, ikinci bir kopyası değil. Bilinmeyen model, üç saniye
  sonra ölen bir run değil, formdayken gelen bir 400.
- **Tam ad, takma ad değil.** `opus` "o an en yenisi" demek; backlog'da bir
  hafta bekleyen kart için yanlış sözleşme. Kart, o gün seçilen modelle çalışır.
- **Dashboard.** Açılış ekranı bir broşürdü — bir başlık, bir cümle, iki bağlantı
  — yani uygulamanın açıldığı ekran canlı verisi olmayan tek ekrandı. Artık
  sırayla: çalışan işler ve **canlı terminal çıktıları**, kuyruk, son bitenler,
  bağımlılık sağlığı, hesap doluluğu, günlük maliyet.
- **Çalışan her job kendi terminalini kendisi açıyor.** Eskiden bu yalnız
  operatörün tıkladığı işler için geçerliydi; kuyruktan çıkan ya da menü
  çubuğundan başlatılan bir iş kimse aramadan konsol açmıyordu. Artık poll
  döngüsü onları sahipleniyor. Elle kapatılan sekme kapalı kalıyor.
- **Bağımlılık sağlığı ilk ekranda.** Crawl4AI bir oturum boyunca kapalıydı;
  `/diagnostics` bunu — düzeltme komutunu adıyla vererek — söylüyordu ve kabuk
  hiçbir yerde göstermiyordu. Artık daemon'un kendi cümlesi olduğu gibi
  gösteriliyor. Uygulama hiçbir şeyi kendisi başlatmıyor: konteyner kaldırmak
  operatörün kendi makinesindeki kararı.

### Değişti

- **Tek poll döngüsü.** Board ve dashboard aynı diziyi okuyor (`RunsProvider`).
  Daemon'da projeler arası run rotası yok, yani bir board proje başına bir
  istek demek — bu fan-out bir kez yapılabilir, iki kez israf.
- `Dashboard.tsx`'ten `css`/`HoverDiv`/`HoverButton`, modül kaydı, yeni task
  formu ve diagnostics kancası ayrı dosyalara çıktı; iki ekran aynı formu
  paylaşıyor, ikinci bir kopya üretmiyor.
- `internal/api/AGENTS.md`: model listesi yayınlanır, aynalanmaz.
- `internal/coderunner/AGENTS.md`: model satırdan gelir, config'ten değil.

- **Aynı hesaba bağlı iki yuva uyarısı.** Bir yuva, CLI'ın hash'leyip Keychain
  girdisi adına çevirdiği bir dizin — zaten kullandığınız hesapla ikinci bir
  yuvaya giriş yaparsanız iki girdi, iki yeşil satır ve **tek** rate limit
  olur. Satırlardan anlaşılmıyor, artık hesap yöneticisi açıkça söylüyor.

### Düzeltildi

- **Bir hesap aynı anda iki task çalıştırabiliyordu.** `launch` run'ı sahipleniyor
  ama yuvayı meşgul olarak *başlattığı goroutine'in içinde* işaretliyordu; `pump`
  boş yuva kalmayana dek döndüğü için bir sonraki tur, çocuk henüz
  zamanlanmadan aynı yuvayı boş görüp ikinci bir run'ı aynı kimliğe
  bağlıyordu. Kuyrukta beş iş varken beşini birden alabiliyordu. Dispatch
  kilidi bunu kapatmıyor — pencere `launch`'ın dönüşü ile çocuğun
  zamanlanması arasında. Artık işaretleme goroutine'den önce, senkron yapılıyor.
  Regresyon testi düzeltme olmadan kesin olarak kırmızı.

### Notlar

- Crawl4AI bu makinede kapalıydı; sebep koddaki bir hata değil, Docker
  Desktop'ın çalışmıyor oluşuydu. `make crawl-up` sonrası `/diagnostics` yeşil.
- Model listesi bir sabit: nesil değiştiğinde elle güncellenir, tıpkı
  `CodingModel` ve `ResearchModel`'in bugün olduğu gibi.

## [2.2.0] — 2026-09-01

**İki Claude Code hesabı, iki paralel iş.** Bir task'ın hangi kimliği
harcayacağı artık seçilebiliyor ve kapasite bir sayı değil: her hesap aynı anda
tek task.

### Eklendi

- **Hesaplar.** Bir hesap = bir dizin. Claude Code kimlik bilgilerini macOS
  Keychain'de tutuyor ve hangi girdiyi kullanacağını
  `CLAUDE_SECURESTORAGE_CONFIG_DIR` yolundan türetiyor — yani dizin sadece bir
  hash girdisi, Mimir hiçbir kimlik bilgisi görmüyor. Projeler gibi: yol bir kez
  alınır, sonrası id ile taşınır. Yeni rotalar `GET|POST /accounts`,
  `DELETE /accounts/{id}`, `GET /accounts/{id}/status`.
- **Canlı kimlik sorgusu.** `claude auth status` her yuva için `email`,
  `orgName` ve `subscriptionType` döndürüyor ve hiçbir şey harcamıyor, bu yüzden
  hesap satırındaki bilgi kayıttan değil o andan geliyor. Login'i düşmüş bir
  hesap sağlıklı görünmüyor.
- **Hesap başına tek run.** `CodingMaxConcurrentRuns` sabiti kaldırıldı: aynı
  kimliği paylaşan iki run aynı rate limit'i ve aynı oturum durumunu paylaşır,
  yani ikincisi verim değil çekişme. Daha fazla kapasite = bir hesap daha
  kaydetmek.
- **Otomatik atama, isteğe bağlı sabitleme.** Task varsayılan olarak ilk boşalan
  hesaba düşer; istenirse bir hesaba sabitlenir. Dispatcher kuyruğun başını
  almak yerine kuyruğu yürüyor, böylece meşgul bir hesaba sabitlenmiş bir kart
  arkasındaki başka hesaba ait işi bekletmiyor.
- **Recents.** Terminals kenar çubuğunda varsayılan olarak kapalı bir bölüm:
  geçmiş oturumlar. Açıldığında transcript aynı soketten baştan oynatılıyor —
  ikinci bir depo değil, board'ın kendi listesi eksi zaten açık olanlar.

### Değişti

- Çocuk süreç ortamı artık miras alınmıyor, kuruluyor. Kimlik yuvası seçiliyor
  ve daemon'ı başlatan Claude Code oturumunun değişkenleri (`CLAUDECODE`,
  `CLAUDE_CODE_*`, `AI_AGENT`) temizleniyor — bunları okuyan iç içe bir CLI
  başkasının oturumunu sürdürdüğünü sanıyordu.
- `internal/api/AGENTS.md`'deki "dosya sistemi yolu tam olarak tek rotada kabul
  edilir" kuralı "tam olarak iki rotada" oldu (`POST /projects`,
  `POST /accounts`). Kuralın koruduğu şey değişmedi: yol bir kez, kaydı sırasında,
  o kararın sahibi olan paket tarafından doğrulanır.

### Şema

- `0012_accounts.sql` — `accounts` tablosu (`config_dir` üzerinde tekil indeks)
  ve `coding_runs`'a `requested_account_id` + `account_id`. İkisi ayrı: biri
  operatörün sabitlemesi, diğeri run'ın gerçekten çalıştığı yuva.

## [2.1.0] — 2026-09-01

**Coding task artık bir yaşam döngüsü.** Bir görev yazılıp bekletilebiliyor,
kuyruğa alınabiliyor, durdurulabiliyor; görsel eklenebiliyor; ve çalışan her
job'ın kendi terminali var. Bu sürümün asıl konusu, "çalışıyor yazan ama
çalışmayan" kartların kökünü kurutmak.

### Eklendi

- **Backlog ve kuyruk.** `POST /coding-tasks` artık `start: false` kabul ediyor:
  kart önce yazılıyor, token ancak biri çalıştırdığında harcanıyor. Yeni
  statüler `backlog`, `queued`, `stopped`; yeni rotalar
  `POST /coding-tasks/{id}/enqueue`, `POST /coding-tasks/{id}/stop`,
  `DELETE /coding-tasks/{id}`. Kuyruk SQLite'ta durduğu için daemon yeniden
  başladığında da yerinde kalıyor ve kaldığı yerden dağıtılıyor.
- **Eşzamanlılık sınırı.** Aynı anda en çok `CodingMaxConcurrentRuns` (2) run;
  üçüncüsü kuyrukta bekliyor. Dispatcher `internal/coderunner`'ın bir metodu —
  ikinci bir orkestratör değil, `docs/ROADMAP.md`'nin terk ettiği DAG/kanban
  ayrımına dönüş yok.
- **Run durdurma.** Önce SIGINT (CLI kendi `result` satırını yazabilsin diye),
  `CodingStopGrace` sonra SIGKILL, ikisi de sürece değil süreç grubuna — yoksa
  `claude`'un çocukları stdout borusunu açık tutuyor.
- **Görsel eki.** `POST /coding-tasks/attachments` + `GET .../{id}`. Tür,
  istemcinin dosya adına değil baytlara bakılarak (`http.DetectContentType`)
  belirleniyor; yalnız PNG/JPEG/GIF/WebP. Dosyalar store'un yanındaki
  `attachments/` dizinine yazılıyor, prompt'a yol olarak enjekte ediliyor ve
  o dizin `--add-dir` ile okunabilir kılınıyor.
- **stderr artık akıyor.** CLI'ın stderr'ı `stderr` olayı olarak yayınlanıyor ve
  transcript'e yazılıyor. "run `claude login`" gibi bir run'ın neden hiç
  çalışamayacağını söyleyen tek yer burasıydı ve hiçbir yere ulaşmıyordu.
- **Terminals ekranı.** Çalışan her job kendi sekmesini alıyor: canlı satırlar,
  scrollback, kopyala, STOP. Oturumlar `Dashboard`'ın üstünde tutuluyor, yani
  ekran değiştirmek soketi öldürmüyor. PTY değil — daemon `claude`'u borularla
  çalıştırıyor, gösterilebilecek dürüst şey o akış ve stderr.
- **Board artık çalışıyor.** Beş kolon (Backlog · Queued · Running · Done ·
  Failed), her zaman görünür "+ Yeni task", kart üzerinde run/stop/sil/terminal,
  ve Backlog↔Queued arası sürükle-bırak. Yoklama aralığı panoya göre değişiyor.

### Düzeltildi

- **Yeniden başlatmadan sağ çıkan "running" satırları.** Daemon açılışta
  `Resume` ile bunları `failed` + "the daemon restarted while this run was in
  flight" yapıyor. Bir run'ın sonucu artık `context.WithoutCancel` ile
  yazılıyor: kapanış sırasında biten her run satırını kaybediyordu ve sonsuza
  kadar `running` kalıyordu.
- **Görünmez WebSocket hataları.** `socket.onerror`'ın yazdığı sebebi
  `socket.onclose` siliyordu; bağlanamayan bir soket tamamen sessizdi. Ayrıca
  `openRunStream`'in reddi yutuluyordu (`void … .then(…)`), rozet sonsuza kadar
  "running" kalıyordu. Soket terminal olay görmeden kapanırsa artık
  `GET /coding-tasks/{id}` ile yoklamaya düşülüyor.
- **`/coding-tasks` rotaları `Runner` yokken de kayıtlıydı** ve nil-interface
  çağrısı 500 üretiyordu; `/ws/runs/{id}` koruması `Transcripts`'i kontrol
  etmiyor, geçmişsiz soket sunuyordu.
- **Yalan söyleyen bağlantı göstergeleri.** Başlıktaki yeşil nokta, overlay'deki
  "ready" ve kart etiketleri sabit yazılmıştı; artık gerçek daemon durumundan
  geliyor.
- **Klasör seçicinin sessiz hatası** (try/catch dışındaydı), başarısız bir
  `start`'ın önceki run'ı boş panelle yeniden çizmesi, ve run akarken "Start
  run"ın yeniden etkinleşip ilk akışı terk etmesi.
- `consume`'un yuttuğu `scanner.Err()` artık başarısızlık nedeni olarak
  raporlanıyor; zaman aşımı da kendi mesajını alıyor.

### Şema

- `0011_coding_task_lifecycle.sql` — `coding_runs`'a `title`, `created_at`,
  `queued_at`, `attachments` kolonları ve `(status, queued_at)` indeksi.
  `started_at = 0` artık "hiç başlamadı" demek.

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
