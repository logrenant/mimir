# Değerlendirme — task-35 … task-68 (27 `done` task)

- **Tarih:** 2026-09-05
- **Reviewer:** Claude Opus (`docs/AGENT_RULES.md` §5)
- **Kapsam:** `Status: done` olan 27 task, **çalışma ağacının bugünkü hâli**
  (HEAD değil — 91 dosyalık commit'lenmemiş değişiklik dahil)
- **Karar:** Aşağıdaki tabloya bakın. 27 task'ın **21'i** onaylanabilir,
  **2'si** takip maddeleriyle (B-2), **4'ü** onaylanamaz — çünkü artık var
  olmayan bir sistemi anlatıyorlar.
- **Güncelleme:** B-1 kapandı (task-69), tarama izinleri operatöre açıldı.
  task-47/49/51 onaya hazır.

---

## 1. Ne çalıştırıldı

Gate'ler bu makinede baştan koşturuldu, task dosyalarının iddiasına
güvenilmedi:

| Gate | Sonuç |
|---|---|
| `make build` | ✅ |
| `make vet` | ✅ |
| `make lint` (stdout + context + golangci-lint + 4 shell script) | ✅ |
| `go test ./...` | ✅ 30 paket |
| `go test -race ./...` | ✅ |
| `npm run typecheck` (desktop) | ✅ |
| `npm test` (desktop) | ✅ 12 dosya / 189 test |
| `cargo fmt --check` + `clippy -D warnings` + `cargo test` | ✅ 13 test |

**`make check` ve `make desktop-check` yeşil.** Yani her task'ın DoD'sindeki
"gate yeşil" maddesi doğru.

> ⚠️ **Ortam notu (kod kusuru değil):** `make desktop-check` ilk denemede
> `Error 101` verdi — `desktop/src-tauri/target/` içinde depo eski yolunda
> (`development/personal/goat-remastered`) derlenmiş bayat tauri build-script
> çıktısı duruyordu. `cargo clean` sonrası Rust yarısı tamamen yeşil. Ama şu
> anlama geliyor: **depo taşındığından beri Rust gate'i bu makinede hiç
> koşmamış.** 13 desktop task'ının DoD'sindeki `make desktop-check` yeşil
> iddiası, bugün ben `cargo clean` çekene kadar doğrulanmamıştı.

---

## 2. Task tablosu

| Task | Karar | Gerekçe |
|---|---|---|
| task-35 lifecycle | ✅ ONAYLA | Kuyruk, stop, reconciliation, attachment sniffing hepsi testli. Bkz. B-4. |
| task-36 board/terminals | ✅ ONAYLA | — |
| task-37 coding accounts | ⛔ **GEÇERSİZ** | Konusu ("kapasite hesap sayısıdır, kimlik başına bir çalıştırma") kaldırıldı. Bkz. §4. |
| task-38 accounts/recents | ⛔ **GEÇERSİZ** | `AccountPicker.tsx`, `lib/accounts.ts` silindi. |
| task-39 model seçimi | ✅ ONAYLA | Allow-list `internal/config`'te, argv'ye giden tek yol doğrulamadan geçiyor. Örnek uygulama. |
| task-40 dashboard | ✅ ONAYLA | — |
| task-41 brain core | ✅ ONAYLA | — |
| task-43 mcp enforcement | ✅ ONAYLA | `finalize.go` fail-closed, doğrulandı. |
| task-45 autonomous capture | ✅ ONAYLA | — |
| task-47 repo scan | ✅ ONAYLA | B-1 task-69'da kapandı. |
| task-49 machine scan | ✅ ONAYLA | B-1 task-69'da kapandı. |
| task-51 resident scan | ✅ ONAYLA | B-1 task-69'da kapandı. |
| task-52 desktop brain | ✅ ONAYLA | — |
| task-53 account discovery | ⛔ **GEÇERSİZ** | `account.Discover`, `ClaudeAccountsDir`, `MIMIR_CLAUDE_ACCOUNTS_DIR`: 0 hit. |
| task-54 desktop slots | ⛔ **GEÇERSİZ** | Aynı sebep. |
| task-55 keyless region | ✅ ONAYLA | — |
| task-56 region source | ✅ ONAYLA | — |
| task-57 contacts/export | ⚠️ TAKİPLİ | **B-2** (export dizini doğrulanmıyor). Ayrıca e-posta yarısı `message.go`'ya taşındı. |
| task-58 desktop export | ⚠️ TAKİPLİ | **B-2** — okuma tarafı (`reveal_export`) korumalı, yazma tarafı değil. |
| task-59 classify fallback | ✅ ONAYLA | — |
| task-60 leadgen workspace | ✅ ONAYLA | — |
| task-61 gemini-3.8 | ✅ ONAYLA | Pin doğru, SD-5 temiz. |
| task-63 lead ledger | ✅ ONAYLA | 5 DoD maddesi de kodda karşılanıyor. |
| task-64 desktop leads | ✅ ONAYLA | — |
| task-65 chat archive | ✅ ONAYLA | Model çağrısı yok, doğrulandı. Bkz. B-5 (dosya izni). |
| task-67 file versions | ✅ ONAYLA | — |
| task-68 desktop versions | ✅ ONAYLA | — |

---

## 3. Bulgular

Şiddet sırasına göre. Her biri dosya:satır ile doğrulandı, tahmin yok.

### B-1 · Yüksek · ✅ **KAPANDI** (task-69) · Daimî tarama sır dosyalarını dışarı gönderiyor
**`internal/brain/scan.go:350` (`scannable`) — task-47, task-49, task-51**

> **Çözüldü.** `internal/brain/exclude.go`'daki `isSecret` artık `scannable`'ın
> ilk kuralı ve yapılandırılamaz; operatörün kendi hariç tutma listesi ayrı bir
> katman olarak `Excluder`'da. Aşağıdaki teşhis, kapanmadan önceki durumu
> anlatıyor — kaydı için bırakıldı.

`scannable()` üretilmiş/ikili dosyaları eliyor; **kimlik bilgisi eleyen hiçbir
kural yok.** Paketin içinde çalıştırdığım doğrulama:

```
scannable(".env")                    = true
scannable(".env.local")              = true
scannable("secrets/id_rsa")          = true
scannable("certs/server.key")        = true
scannable(".npmrc")  .netrc  kubeconfig  credentials  = true
scannable("terraform.tfstate")       = true
scannable("service-account.json")    = true
scannable("Bank-Statement-2026.pdf") = true
```

Bu içerik `internal/llm/agy.go:174`'te `cmd.Stdin`'e yazılıp **harici bir model
CLI'ına** gidiyor. Aradaki tek engel `.gitignore` (`git ls-files
--exclude-standard`) ve o engel iki yerde hiç yok:

1. **Repo olmayan dizinlerde** (`scan.go:288` walk fallback) `.gitignore`
   hiç okunmuyor. `BrainScanRoots` (`config.go:943`) `~/Documents`'ı içeriyor.
2. `--others` takip edilmeyen dosyaları **kasten** kapsama katıyor
   (task-49'un getirdiği davranış), yani gitignore'lanmamış bir `.env.local`
   doğrudan geçiyor.

`.pem` ve `.key` uzantı denylist'inde yok; `id_rsa`'nın uzantısı zaten yok ve
`isText` onu metin sayıyor.

**Uygulanan değişiklik (task-69):** `isSecret` denylist'i `exclude.go`'da,
`scannable`'ın ilk kuralı olarak — yani her iki giriş yolu (git listesi ve
walk) aynı cevabı veriyor. `TestIsSecret_CoversCredentialShapes` yukarıdaki
listenin hepsinin elendiğini, `TestIsSecret_LeavesOrdinaryFilesAlone` ise
`docs/secrets.md` ve `internal/keyboard.go` gibi sıradan dosyaların
düşmediğini tutuyor. Buna ek olarak operatör artık kökleri ve hariç tutulan
yolları Brain sekmesinden yönetiyor.

**Açık kalan:** hariç tutulan ya da artık sır sayılan bir yoldan gelmiş
**mevcut düğümler** grafikte duruyor. Tarama duruyor, geçmiş silinmiyor — ayrı
ve açık bir onay olmalı, task-69 kapsamı dışında bırakıldı.

---

### B-2 · Orta-Yüksek · Export dizini istemciden geliyor, hiç doğrulanmıyor
**`internal/api/maps.go:105` → `internal/leadgen/export.go:80` — task-57, task-58**

`POST /maps/leadgen/export` gövdesindeki `dir` alanı hiçbir kontrolden
geçmeden `os.MkdirAll(dir, 0o755)`'e ve ardından bir `.xlsx` yazımına gidiyor.
Dosya *adı* temizleniyor (`export.go:329`, `sheetNameUnsafe`) ama **dizin
temizlenmiyor** — yani token'ı olan bir istemci için keyfi dizin oluşturma +
keyfi konuma dosya yazma primitifi.

Asıl mesele tutarsızlık: bu deponun her yolu ya `project.Canonicalize`'dan
geçiyor ya sabit bir dizin. Üstelik **aynı özelliğin okuma tarafı korumalı** —
`desktop/src-tauri/src/exports.rs:35` "exports dizini dışındaki hiçbir şeyi
açmaz" diyor. Yazma tarafında o kontrolün karşılığı yok. task-58'in kapsamı
zaten "exports dizinine sınırlı bir reveal komutu"ydu; sınır tek yönlü kalmış.

**Önerilen değişiklik:** `dir`'i tamamen kaldırın (arayüz zaten göndermiyor —
`daemon.ts:496`'da tip olarak duruyor, kullanılmıyor) ya da `ExportDir`
altında olmaya zorlayın; `reveal_export`'un yaptığı `canonicalize` +
`starts_with` kontrolünün Go karşılığı.

---

### B-3 · Orta · `StartLogin`'de TOCTOU: iki eşzamanlı istek iki `claude auth login` başlatır
**`internal/account/login.go:143-154`**

Kodun kendi yorumu doğru teşhisi koyuyor: *"iki `claude auth login` süreci tek
yuvada aynı keychain girdisi için yarışır"*. Ama koruma çalışmıyor — kilit
`switch`'ten hemen sonra (satır 151/154) bırakılıyor, süreç ise
`ensureDir` → `writeBrowserShim` → `pty.StartWithSize` sonrasında, yani
kilitsiz bir pencerede başlıyor. İki eşzamanlı `POST /accounts/login` ikisi de
`switch`'i geçer.

İkincisi `r.login.cmd`/`tty`'yi üzerine yazar; birincisinin `gen`'i bayatlar,
durumu artık yazamaz ve süreci **10 dakikalık `loginTimeout` dolana kadar
yaşamaya devam eder**, keychain girdisini tutarak.

**Önerilen değişiklik:** Durumu `switch` içinde hemen `LoginOpening`'e alıp
kilidi öyle bırakın (bir "başlatılıyor" nöbeti), spawn başarısız olursa geri
alın. Testi: iki eşzamanlı `StartLogin`, sahte CLI'ın tek kez çalıştığının
iddiası.

---

### B-4 · Orta · "quota"/"rate limit" kelimesi geçen her hata kuyruğu durduruyor
**`internal/coderunner/ratelimit.go:386-393` (`spentPhrases`) — task-35 + commit'lenmemiş iş**

`looksSpent()` altı jenerik alt dize arıyor: `usage limit`, `rate limit`,
`rate_limit`, `rate-limited`, `too many requests`, **`quota`**.

Yorum "yalnızca CLI'ın hata metninde eşleşir, bir çalıştırmanın çıktısında
asla" diyor ama iki çağrı yolu bunu tutmuyor:

- `runner.go:1184` — `terminal.Error`, yani CLI'ın `result` satırındaki cümle.
  CHANGELOG'a göre artık bu cümle doğrudan karta yazılıyor, yani ajanın kendi
  ifadesi.
- `runner.go:1197` — `fmt.Errorf("... : %s", tail)` ve `tail` **stderr'in
  kuyruğu**. Ajanın çalıştırdığı herhangi bir alt komut stderr'e yazabilir.

Yani disk quota'sı hakkında bir hata veren, ya da "rate limit" yazan bir test
suite'i olan bir task başarısız olduğunda **`failed` olmuyor: park ediliyor.**
Kapasite artık tek hesap olduğu için o tek yuvanın tutulması **bütün kuyruğun
durması** demek — hem de sessizce, `CodingLimitRecheck` (15 dk) boyunca.

**Önerilen değişiklik:** Eşleşmeyi daraltın — `resetsAt` alanı ya da
`…|<unix>` ekinin varlığını zorunlu kılın, ya da yalnızca CLI'ın kendi hata
zarfında (`s.rejected` / structured `result` alanı) arayın; serbest metin
stderr'i bu karara hiç sokmayın. En azından `quota` kelimesini listeden çıkarın.

---

### B-5 · Düşük-Orta · Veri deposu 0644, `endpoint.json` 0600
**`internal/store/store.go:59`**

```
drwxr-xr-x  ~/Library/Application Support/mimir
-rw-r--r--  mimir.db   (49 MB)
```

`mimir.db` **herkes tarafından okunabilir** ve içinde şunlar var: taranmış her
dosyanın distile edilmiş içeriği (task-47/49/51), **kelimesi kelimesine sohbet
arşivi** (task-65), lead telefonları ve iletişim bilgileri (task-63).

Karşılaştırma, aynı deponun kendi standardı: `endpoint.json` `0600` yazılıyor
*ve* `0o077` biti varsa okumayı reddediyor (`daemon.rs:617`); attachment'lar
`0600` (`attachments.go:130`). Çok daha fazlasını tutan dosya en gevşek olan.

**Önerilen değişiklik:** Dizin `0o700`, DB dosyası `0o600`. Var olan kurulumlar
için açılışta bir chmod.

---

### B-6 · Düşük · SD-1 ihlali: iki env override belgelenmemiş
**`internal/config/config.go:915`, `cmd/mimir-mcp/main.go:67`**

SD-1 açıkça diyor: izin verilen env override'lar *"`internal/config/AGENTS.md`
içinde sayılmak zorunda"*. Kodda okunan ama tabloda olmayan iki tane var:

| Değişken | Nerede okunuyor | AGENTS.md'de |
|---|---|---|
| `MIMIR_GITHUB_TOKEN` | `config.go:915` | ❌ yok |
| `MIMIR_NESTED` | `cmd/mimir-mcp/main.go:67` | ❌ yok |

`MIMIR_GITHUB_TOKEN` bir **kimlik bilgisi** — SD-1'in "operatör tarafından
sağlanan kimlik" kategorisine `MIMIR_GOOGLE_PLACES_API_KEY` gibi açık bir
gerekçeyle yazılması gereken tam olarak bu tür bir değer. SD-1'in son cümlesi:
*"aynı gerekçe olmadan üçüncü bir kategori eklenemez."*

**Önerilen değişiklik:** İkisini de `internal/config/AGENTS.md` tablolarına
ekleyin, `MIMIR_GITHUB_TOKEN`'ı kimlik satırına.

---

### B-7 · Düşük · WebSocket upgrade'inde Origin kontrolü kapalı
**`internal/api/ws.go:73`, `internal/api/pty.go:118`**

`InsecureSkipVerify: true`. Gerekçe yorumda yazılı ve makul (meşru istemci bir
Tauri WebView, origin'i bu host değil) — ayrıca **token upgrade'den önce
kontrol ediliyor**, yani bu tek başına bir açık değil.

Yine de: WebSocket CORS'a tabi değil ve subprotocol listesi JS'ten
ayarlanabilir, yani token'ı ele geçiren herhangi bir sayfa `/ws/terminals/pty`
üzerinden **tam interaktif kabuk** açabilir. Token 32 bayt CSPRNG
(`daemon.rs:697`) ve argv yerine env'den geçiyor, o yüzden risk düşük — ama
derinlemesine savunma için bir `OriginPatterns` (`tauri://localhost`,
`http://localhost:5173`) ucuz.

---

### B-8 · Düşük · Attachment `ext`'i sidecar JSON'dan, yeniden doğrulanmıyor
**`internal/coderunner/attachments.go:164`**

`statAttachment` `id`'yi `isHexID` ile koruyor (doğru), ama `att.Ext`'i
metadata JSON'ından okuyup `attachmentFile`'da `filepath.Join(dir, id+"."+ext)`
içine koyuyor. `ext` = `"./../../../etc/passwd"` olan uydurma bir sidecar
keyfi dosya okutur.

Ön koşul attachment dizinine yerel yazma erişimi, yani şiddet düşük. Ama
`sniffImage` sonucu zaten `imageTypes`'la sınırlı — okurken de aynı haritaya
karşı doğrulamak tek satır.

---

### B-9 · İz · İki küçük tutarsızlık

- `desktop/src-tauri/src/exports.rs:39` — `Command::new("open")`, PATH'ten
  çözülüyor. `internal/account/login.go` aynı ikiliyi `/usr/bin/open` mutlak
  yoluyla çağırıyor. Aynısını burada da yapın.
- `internal/store/leads.go:199` — LIKE araması kullanıcı metnindeki `%` ve `_`
  karakterlerini kaçırmıyor; "a_b" araması "axb" buluyor. Güvenlik değil, arama
  davranışı.

---

## 4. Süreç bulgusu: dört task artık var olmayan bir sistemi anlatıyor

`task-37`, `task-38`, `task-53`, `task-54` `Status: done` ve `tasks/README.md`
tablosunda yaşıyor gibi duruyorlar. Konuları commit'lenmemiş iş tarafından
**tamamen kaldırıldı** (CHANGELOG "Yayımlanmadı" + 2.12.0: *"Çok hesaplı yuva
kaydı kalktı"*):

```
ClaudeAccountsDir            0 hit
account.Discover             0 hit
MIMIR_CLAUDE_ACCOUNTS_DIR    0 hit
AccountPicker / AccountSelect 0 hit
silinen: internal/account/discover.go, desktop/src/lib/accounts.ts,
         desktop/src/components/AccountPicker.tsx
```

task-53'ün DoD'si hâlâ *"iki coding task aynı anda çalışır"* diyor; kapasite
artık tek çalıştırma. Bunlar kötü yapılmış işler değil — **doğru yapılmış ve
sonra yerine başkası konmuş** işler. "Approved" onlar için yanlış kutu.

**Öneri:** `tasks/README.md`'ye `superseded` durumu ekleyin ve dördünü oraya
alın, hangi sürümün onları değiştirdiğine referansla. Bir sonraki okuyucunun
task-53'ü okuyup var olmayan bir dizini araması bundan ucuza engellenmez.

**İkinci süreç bulgusu:** Çalışma ağacındaki 91 dosyalık iş (rate-limit park
mekanizması, hesap giriş akışı, `internal/settings`, outreach kanalları,
`shellPaste`, `macos.rs`) **hiçbir task dosyasına ait değil.** `tasks/README.md`
task-68'de bitiyor. `AGENTS.md` §4 protokolü her işin bir task dosyasıyla
başlamasını istiyor. B-3 ve B-4'ün ikisi de bu dosyasız işin içinden çıktı —
gözden geçirilmiş kodla gözden geçirilmemiş kod arasındaki fark görünür.

---

## 5. Onaylamadan önce

Sırayla:

1. ~~**B-1**~~ kapandı — task-69, `make check` + `make desktop-check` yeşil.
   **B-2** hâlâ onaydan önce kapanmalı.
2. **B-3**, **B-4**, **B-5** yeni birer task dosyası (task-70…) — ayrı işler,
   mevcut task numaralarının altına sokulmamalı (AGENT_RULES §5: *"kapsam
   dışı işi mevcut task numarası altında uygulamayın"*).
3. **B-6** tek dosyalık belge düzeltmesi, herhangi bir task'a iliştirilebilir.
4. **B-7**, **B-8**, **B-9** birikmiş iş; acele değil.
5. §4'teki dört task `superseded`'e taşınsın, `approved`'a değil.
6. Çalışma ağacındaki iş için geriye dönük task dosyaları yazılsın — yoksa bir
   sonraki değerlendirme aynı boşluğu bulacak.

**Uygulanan:** B-1 (task-69). Kalan bulgular için kod değişikliği yapılmadı;
hangilerini yazmamı istediğinizi söyleyin.
