# task-97 — Bağlantı, ikili değil: yönlendirmenin birimi değişiyor

- **Status:** done
- **Owner agent:** Coder (Gemini)
- **Prerequisites:** task-93
- **Primary paths:** `internal/llm/**`, `internal/connections/**`, `internal/store/**`, `internal/config/config.go`, `internal/api/maps.go`, `cmd/mimir-daemon`
- **Roadmap bucket:** §B — model yönlendirme

## Context

Bugün bir sağlayıcının kimliği **sabit bir CLI ikili adı**. Üç yerde kırılıyor:

1. **Aynı satıcının iki yolu bir arada duramıyor.** "Claude Code CLI" (aboneliği
   harcar) ile "Anthropic API" (anahtar, ayrı fatura) aynı anda var olamıyor:
   `NewRouter` `byName`'i `Provider.Name()` ile kuruyor, ikincisi birincisinin
   üstüne yazıyor.
2. **Kota modeli yanlış.** `agy` ile `gemini` bağımsız görünüyor. Değiller:
   Antigravity'nin kendi aboneliği yok, bir Google AI planına biniyor; Gemini
   CLI'ın kota havuzunu ise CLI değil **oturum yöntemi** belirliyor (kişisel
   OAuth → Code Assist 1000/gün, AI Pro ile 1500; AI Studio anahtarı → ayrı
   havuz, 250/gün; Vertex ve Workspace ayrı). Aynı Google hesabıyla girilmişse
   `agy` ile OAuth'lu `gemini` **aynı cüzdanı** harcıyor.
3. **Operatör ekrandan sağlayıcı ekleyemiyor.** Liste derleme zamanında sabit.

### Ve düzeltilecek bir canlı hata

**`Router.Providers()` `byName`'i hiç gezmiyor** — yalnız `byClass` ve
`fallback`. Gönderilen yapılandırmada bu `{agy, claude}` demek, yani `Discover`
`gemini` ve `ollama`'yı **hiç sormuyor**, `GET /llm/providers` onlar için
`available` satırı döndürmüyor, ve `desktop/src/lib/settings.ts`'in
`isSelectable`/`availabilityNote`'u "veri yoksa sorun yok" dalına düşüyor.
task-98'in erişilebilirlik raporu iki sağlayıcıyı **sormadan** "kurulu ve
sorunsuz" göstermiş. Bu task'ta düzeltiliyor ve regresyon testi alıyor.

## Scope (do exactly this)

1. **`internal/llm/connection.go`** — yalnız veri: `Adapter` (kapalı, derleme
   zamanı küme), `ConnectionSpec`, `ModelSpec`. Sır yok, store yok, import
   döngüsü yok.
2. **`Selection` ve `Selection.Key()` DEĞİŞMİYOR.** Değişen tek şey ilk
   dizgenin neyi gösterdiği: ikili adı değil **bağlantı kimliği**. Dört
   yerleşiğin kimlikleri birebir bugünkü adlar. Bu tek karar migration'ı
   ortadan kaldırıyor ve `Selection.Key()`'den türeyen her önbellek anahtarını
   (`internal/leadgen/*`, `internal/catalog/*`) bayt bayt aynı bırakıyor.
3. **Router `byName` → `byID`**, artı `specs map[string]ConnectionSpec`.
   `UseConnections(specs, SecretFn)` ile çalışma anında doldurulur —
   `UseEnviron`/`UseDefaults` ile aynı enjeksiyon şekli, aynı gerekçeyle
   (`internal/llm` `internal/store`'u import edemez).
4. **`Providers()` `byID`'yi gezer.** Hatanın düzeltmesi.
5. **`internal/connections/`** — store üzerinden CRUD, doğrulama, ve
   `[]llm.ConnectionSpec` üretimi. `Allows(connID, model)` ve
   `DefaultModel(connID)`.
6. **`migrations/0026_llm_connections.sql`** — dört yerleşiği bugünkü
   kimlikleriyle `builtin=1` olarak tohumlar. Append-only.
7. **`internal/api`'nin `llmSelection`'ı** `cfg.HasLLMModel` yerine kayıt
   üzerinden doğrular; `cmd/mimir-daemon`'ın `UseDefaults` kapanışı da.

## Out of scope (do NOT do here)

- **Yeni adaptör yok** (API taşımaları task-99).
- **Sır yok** (`internal/secrets` task-99).
- **Kimlik/kota tablosu yok** (task-101).
- `desktop/**` — task-100.
- `Selection`'ı üç alanlı bir struct'a genişletmek. Hiçbir şey kazandırmıyor ve
  bütün türetilmiş içerik önbelleğini geçersizleştiriyor.

## Interfaces / contracts

```go
type Adapter string
const (
    AdapterClaudeCLI Adapter = "claude-cli"
    AdapterAgyCLI    Adapter = "agy-cli"
    AdapterGeminiCLI Adapter = "gemini-cli"
    AdapterOllamaCLI Adapter = "ollama-cli"
)

type ConnectionSpec struct {
    ID, Label, Vendor string
    Adapter           Adapter
    DefaultModel      string
    Models            []ModelSpec
    Discovered        bool
    Caps              Capabilities
    Enabled, Builtin  bool
}

func (r *Router) UseConnections(specs []ConnectionSpec)
```

**İki sert kural, `internal/llm/AGENTS.md`'ye yazılır:**

- **Operatörün hiçbir dizgesi çalıştırılabilir ya da bayrak olmaz.** CLI
  adaptörü yolunu `internal/config`'ten alır, asla satırdan. `cli_path` diye
  bir sütun **yok** — eklemek allow-list'i bir kabuğa çevirir.
- **Adaptör kümesi derleme zamanında kapalı.** Operatör listeden seçer.

## Definition of Done

- [x] `Selection.Key()` yerleşik bağlantılar için bayt bayt aynı — testi var.
- [x] `Providers()` dört yerleşiğin dördünü de veriyor; `Discover` hepsini
      soruyor — **hatanın regresyon testi**.
- [x] Bir bağlantı satırı kendi çalıştırılabilirini adlandıramıyor.
- [x] `llmSelection` kayıt üzerinden doğruluyor; bilinmeyen kimlik reddediliyor.
- [x] `settings.json`'daki `provider: "agy"` hâlâ çözülüyor.
- [x] `make check` yeşil.
- [x] Status `done` + changelog.

## Notes for the reviewer (Opus)

- **SD-1**: yerleşik satırlar yapılandırma değil etiket. CLI yolu ve pinli model
  kataloğu `internal/config`'ten gelmeye devam ediyor; `0026`'nın SD-1'in arka
  kapısı olmasını engelleyen şey bu.
- En çok bakılacak yer: bir bağlantı satırının taşıyabildiği her alan. Yol,
  bayrak ya da `ConfigDir` taşıyan bir alan bu tasarımın çöktüğü yerdir.

## Changelog

- **`Router.Providers()` `byID`'yi geziyor — canlı hatanın düzeltmesi.**
  Önce `byClass` ve `fallback`'i geziyordu; gönderilen yapılandırmada bu iki
  giriş demek, yani `gemini` ile `ollama` kayıtlı, seçilebilir, ve **hiçbir şey
  sorulmamış** durumdaydı. `Discover` bu listeyi geziyor, dolayısıyla
  `GET /llm/providers` onlar için erişilebilirlik satırı taşımıyordu ve
  masaüstü seçicisinin "veri yoksa sorun yok" dalı ikisini de bakmadan
  onaylıyordu. **Kendine dair yarım cevap veren bir kayıt, diğer yarısı için
  sessizce kefil olur.**
  Önce hatayı kanıtlayan test yazıldı, sonra düzeltildi.
- **Mevcut bir test hatayı beklenti olarak yazmıştı** (`want 2`). Niyeti
  ("her biri bir kez") doğruydu, sayısı kusurun kendisiydi; dördü de bekleyecek
  şekilde düzeltildi.
- **Ve düzeltme başka bir şeyi ortaya çıkardı:** `TestDiscover_SeparatesInstalledFromSignedIn`
  artık gerçek `ollama`'yı çağırıp yerel bir model yüklüyordu — 7,5 saniye.
  `internal/llm/AGENTS.md`'nin tam olarak uyardığı şey. Sahte CLI eklendi.
- **`ConnectionSpec`** — yönlendirmenin birimi artık bağlantı. Dört yerleşiğin
  kimlikleri birebir bugünkü adlar, ve `Selection`/`Selection.Key()` hiç
  değişmedi: `settings.json`'daki `provider: "agy"` çözülmeye devam ediyor ve
  `Selection.Key()`'den türeyen her önbellek anahtarı bayt bayt aynı.
- **Satır ne çalıştırılacağını adlandıramıyor.** `cli_path` diye bir alan yok;
  CLI adaptörü yolunu `internal/config`'ten alıyor. Adaptör kümesi derleme
  zamanında kapalı. İkisi de `internal/llm/connection.go`'da gerekçesiyle
  yazılı.
- **`internal/connections`** — allow-list'in yeni evi, ve gerekçe taşımaya göre
  ayrışıyor: CLI adaptöründe model adı argv olur (pinli liste ya da şekil
  denetimi), API adaptöründe TLS üzerinden bir JSON alanı olur — orada komut
  diye bir şey yok, kalan risk "operatörün seçmediği modele harcamak".
- **`agy` ve `gemini` ikisi de `google`.** Antigravity'nin kendi aboneliği yok;
  bir Google AI planına biniyor. Kim faturalıyor sorusu, hangi ikili koşuyor
  sorusundan ayrı ve arayüzün göstereceği olan birincisi.
- **`GET /llm/connections`** — bağlantı şeklinde görünüm. Eski rota şekil-uyumlu
  bir üst küme olarak duruyor. Yüzeyde sır ya da yol taşıyabilecek hiçbir alan
  yok, ve bunun testi var.

### Sapmalar

- **`0026` migration'ı yazılmadı, yerleşikler türetiliyor.** Task dosyası bir
  tohumlama migration'ı öngörüyordu. Uygularken bunun bu turda tam olarak sıfır
  şey kazandırdığı görüldü: yerleşiklerin CLI yolu ve pinli model kataloğu
  `internal/config`'te kalıyor, yani satır yalnız bir etiket katkısı yapardı —
  ve boş bir tablo, SD-1'in arka kapısı olma riskini şimdiden açardı. Migration
  operatörün *kendi* bağlantılarıyla birlikte, task-99'da geliyor.
- **`UseConnections` yalnız metadata taşıyor.** Hangi sağlayıcıların var
  olduğuna hâlâ `NewRouter` karar veriyor; kayıtta olmayan bir kimliği
  adlandıran spec yok sayılıyor, bir etiketten sağlayıcı uydurulmuyor.

`make check: 0`
