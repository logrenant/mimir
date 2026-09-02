# Changelog

Bu dosya [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) biçimini,
sürüm numaraları [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
kuralını izler.

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

### Şema

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
