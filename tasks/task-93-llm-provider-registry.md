# task-93 — Sağlayıcı kaydı: kurulu CLI'lar, yetenekleri, ve operatörün seçtiği varsayılan

- **Status:** done
- **Owner agent:** Coder (Gemini)
- **Prerequisites:** task-41, task-51, task-70
- **Primary paths:** `internal/llm/**`, `internal/config/config.go`, `internal/settings/**`, `internal/api/llm.go`, `cmd/mimir-daemon`
- **Roadmap bucket:** §B — model yönlendirme

## Context

Bugün `internal/llm` iki sağlayıcı tanıyor — `agy` ve `claude` — ve ikisi de
elle yazılmış birer struct. Paketin var olma sebebi *"aynı alt süreç dansının
iki elle yazılmış kopyası vardı ve çoktan birbirinden ayrışmıştı"*; o kopya şimdi
paketin **içinde** iki kez duruyor. Üçüncü bir sağlayıcı elle eklemek, aynı
hatayı üçüncü kez yapmak olur.

Operatörün istediği: Gemini, GPT, DeepSeek ve adı yazılmayan popüler
sağlayıcıların da içeride olması, ve **brain dâhil her görevde** hangi
sağlayıcı/model ile çalışılacağının seçilebilmesi.

### Taşıma kararı: kurulu CLI'lar, API anahtarı değil

Operatörün kararı. Bedeli ve kazancı net: anahtar saklamak gerekmiyor (her CLI
kendi oturumunu taşıyor, `docs/SECURITY.md`'ye yeni bir sır yüzeyi girmiyor),
karşılığında sağlayıcı kümesi **"bu makinede kurulu olan"** hâline geliyor. Yani
kayıt statik bir liste değil, bir **keşif**: `internal/account`'un
`~/.claude-accounts`'u taraması ve `graphify`'ın *"kurulu değilse yok, ve bunu
sessizce geçer"* deseni burada da geçerli.

### Bu makinede gerçekten ne var (ölçüldü, hafızadan yazılmadı)

| CLI | Durum |
|---|---|
| `claude` | kurulu (`~/.local/bin/claude`) |
| `agy` | kurulu (`~/.local/bin/agy`) |
| `gemini` | kurulu, **oturum açmamış** — `GEMINI_API_KEY` yok |
| `codex` | kurulu değil |
| `ollama` | kurulu |

task-91'in dersi burada bire bir geçerli: **kurulu olmayan bir CLI'ın
bayraklarını hafızadan yazmak, uydurma bir lehçe profili yazmaktır.** Bu task
yalnız elde olan CLI'ları ekler; `codex` ve diğerleri kurulduklarında kendi
task'larıyla gelir.

### Asıl bulgu: sağlayıcılar birbirinin yerine geçemez

`gemini --help` ölçüldü. `-m`, `-p`, `-s`, `-o json|stream-json`,
`--approval-mode` var; **`--json-schema` yok.** `agy`'de var. Brain'in distil'i
ve ilişki pasosu şema ile çağırıyor.

Yani "sağlayıcıyı değiştir" düğmesi, şema isteyen bir işi şema veremeyen bir
sağlayıcıya yönlendirebilir ve sonuç **sessiz bir ayrıştırma hatası** olur —
başlığı olan, değerlendirmesi olmayan bir düğüm. Bu yüzden yetenek bir yorum
satırı değil, **tipin parçası** olmak zorunda: `Provider` yapısal çıktı verip
veremediğini söyler, ve router şema taşıyan bir isteği veremeyen bir sağlayıcıya
yönlendirmeyi **reddeder**.

Gözlenen bir başka gerçek: `gemini`, güvenilmeyen bir klasörde
`--approval-mode`'u sessizce `default`'a düşürüyor (*"Approval mode overridden
to 'default' because the current folder is not trusted"*). Bu task için sonucu
yok (distil zaten araç istemiyor), ama task-95 için kritik ve oraya yazılı
gidiyor.

## Scope (do exactly this)

1. **`cliProvider` — paylaşılan çekirdek.** Alt süreç dansı bir kez yazılır:
   zaman aşımı, iki denemelik yeniden deneme, stdin'den içerik, iptal edilen
   context'in fallback tetiklememesi, `ErrProviderUnavailable` sarmalaması.
   `agy.go` ve `claude.go` bu çekirdeğin üstüne birer **spec**'e iner.
2. **`Spec` — sağlayıcıyı tarif eden veri**: CLI adı, argüman kurucusu, env
   eklemeleri, çalışma dizini politikası, ve bir `decode func([]byte) (Response, error)`.
   Zarf çözücüsü fonksiyon kalır, veri olmaz: üç farklı JSON zarfını veri gibi
   göstermek, olmayan bir ortaklık uydurmaktır.
3. **`Capabilities`** — `Provider` arayüzü büyür:
   `Capabilities() Capabilities`, en az `StructuredOutput bool` ve
   `Agentic bool` taşır. `Router.CompleteWith` şema taşıyan bir isteği
   `StructuredOutput` olmayan bir sağlayıcıya yönlendirmeyi
   `ErrNoStructuredOutput` ile reddeder — sessizce düz metin döndürmez.
4. **`gemini` spec'i**, yalnız ölçülen bayraklarla: `-m`, `-p`, `-o text`,
   `--approval-mode plan`, `-s`. **`-o text` bilinçli**: başarı zarfının anahtar
   adı gözlemlenemedi (CLI oturum açmamış), ve `response` ile `text` arasında
   tahmin yürütmek sessizce boş cevap üreten bir ayrıştırıcı demek. Düz metin
   stdout'un tamamıdır, tahmin gerektirmez. `StructuredOutput: false`.
5. **`ollama` spec'i** — kurulu, oturum gerektirmez, tamamen yerel.
   `ollama run <model>` + stdin. `StructuredOutput: false` (şema bayrağı yok).
   Modelleri sabit bir liste değil: `ollama list` ile **keşfedilir**.
6. **Keşif.** `Discover(ctx)` her spec için CLI'ı arar ve `Health` çağırır;
   sonucu `Availability{Provider, Installed, SignedIn, Detail}` olarak döner.
   `GET /llm/providers` artık allow-list'i **artı** bu durumu yayınlar, böylece
   masaüstü seçicisi kurulu olmayan bir sağlayıcıyı seçilebilir göstermez.
7. **Operatörün varsayılanı `internal/settings`'e girer, `config`'e değil.**
   Sınıf → sağlayıcı eşlemesi kodun kararı (SD-1) ve öyle kalır; *operatörün*
   bu makinede hangi sağlayıcıyı tercih ettiği onun yazısıdır — `internal/settings`
   tam olarak bunun için var. `Defaults{Distill, Reason llm.Selection}`, ve
   `Router` bunu sınıf yönlendirmesinden **önce** okur.
8. **`config.LLMProviders`** tablosu yeni sağlayıcıları ve modellerini alır.
   Her model tag'i pinli (SD-5).

## Out of scope (do NOT do here)

- **API anahtarı taşıyan hiçbir sağlayıcı.** Karar CLI sarmalayıcı; anahtar
  saklamak `docs/SECURITY.md`'ye yeni bir sır yüzeyi ekler ve kendi task'ını
  hak eder.
- **Kodlama koşuları** (`internal/coderunner`). Ajan CLI'larının akış formatı,
  izin kipi ve süreç grubu sinyalleri farklı bir sözleşme — task-95.
- **Kurulu olmayan bir CLI'ın spec'i.** `codex` bu makinede yok; bayraklarını
  hafızadan yazmak task-91'in hatası olur.
- `desktop/**` — seçiciler task-98.

## Interfaces / contracts

```go
type Capabilities struct {
    StructuredOutput bool // --json-schema ya da eşdeğeri
    Agentic          bool // bir klasörde araç kullanabilir
}

type Provider interface {
    Name() string
    Model() string
    Capabilities() Capabilities
    WithModel(model string) Provider
    Complete(ctx context.Context, r Request) (Response, error)
    Health(ctx context.Context) error
}

var ErrNoStructuredOutput = errors.New("llm: provider cannot return structured output")

type Availability struct {
    Provider  string `json:"provider"`
    Installed bool   `json:"installed"`
    SignedIn  bool   `json:"signed_in"`
    Detail    string `json:"detail,omitempty"`
}
func Discover(ctx context.Context, r *Router) []Availability
```

## Definition of Done

- [x] `agy` ve `claude` tek bir alt süreç çekirdeği paylaşıyor; davranışları
      birebir aynı (mevcut testler değişmeden geçiyor).
- [x] Şema taşıyan bir istek, şema veremeyen bir sağlayıcıya yönlendirilince
      `ErrNoStructuredOutput` ile reddediliyor — testi var.
- [x] `gemini` ve `ollama` kayıtta; sahte CLI ile testleri var.
- [x] `Discover` kurulu olmayanı `installed: false`, oturum açmamışı
      `signed_in: false` ile bildiriyor; `gemini` bu makinede ikincisi.
- [x] Operatörün varsayılanı `internal/settings`'te ve `Router` onu okuyor.
- [x] `make check` yeşil.
- [x] Status `done` + changelog.

## Notes for the reviewer (Opus)

- **SD-1**: sınıf yönlendirmesi kodda kalıyor; ayara giren tek şey operatörün
  tercihi.
- **SD-5**: her model tag'i pinli, `latest` yok.
- **SD-6**: kurulu olmayan sağlayıcı bir hata değil, bir yokluk — sessizce
  geçilir, ama seçilebilir gösterilmez.
- En çok bakılacak yer 3. madde: yetenek tipin parçası mı, yoksa bir yorum mu?
  Yorumsa brain sessizce bozulur.

## Changelog — bu turda inen

- **`Capabilities` `Provider` arayüzünün parçası oldu.** `StructuredOutput` ve
  `Agentic`. `CompleteWith`, şema taşıyan bir isteği şema veremeyen bir
  sağlayıcıya **hiçbir şey harcamadan** `ErrNoStructuredOutput` ile reddediyor.
  O hata bilinçli olarak `ErrProviderUnavailable`'a sarmalanmadı: sağlayıcı
  erişilebilir, yalnızca bunu yapamıyor — sarmalamak işi fallback yoluna
  sokardı ve operatörün seçmediği bir yerde koşmakla biterdi.
- **`gemini` sağlayıcısı**, yalnız kurulu ikiliden okunan bayraklarla.
  `-o text` **bilinçli**: hata zarfı gözlemlendi
  (`{"session_id":…,"error":{type,message,code}}`, kod 41, "Please set an Auth
  method"), başarı zarfı gözlemlenemedi çünkü CLI bu makinede oturum açmamış.
  `response` ile `text` arasında tahmin yürütmek, sessizce boş cevap veren bir
  ayrıştırıcı demekti; düz metin tahmin gerektirmiyor.
- **`Discover`** — "kurulu mu" (`--version`, bedava) ile "oturum açık mı" (bir
  model çağrısı) ayrı sorular; ikincisi `?probe=1` ile istenir. Sınırlı
  eşzamanlılık (SD-3).
- **`GET /llm/providers`** artık sabit tabloya ek olarak bu makinenin durumunu
  yayınlıyor; `Deps.LLM` yokken alan hiç görünmüyor ("kurulu değil" ile
  "kimse sormadı" ayrı şeyler).
- **`Router.UseDefaults`** ve `settings.Values.Distill/Reason`. Brain'in kendi
  pasoları ilk kez operatörün seçtiği sağlayıcıda koşabiliyor. Öncelik:
  koşunun kendi seçimi > operatörün varsayılanı > sınıf yönlendirmesi. Ayar
  yazılırken de allow-list'ten geçiyor — saklanan bir değer, koşuya özel
  olandan **daha** tehlikeli, çünkü sonrasında kimse bir daha okumadan
  harcanıyor.
- Testler sahte CLI ile: `Discover` testi ilk yazılışında gerçek `claude` ve
  `agy`'yi çağırıp 25 sn sürüyordu — `AGENTS.md`'nin tam olarak uyardığı şey.
  Hepsi sahteye bağlandı, 1.2 sn.

## Changelog — kalan iki madde

- **`runCLI` — alt süreç dansı bir kez yazılı.** Üç dosyada üç kopyası vardı,
  ki bu paketin var olma gerekçesinin ta kendisiydi (iki elle yazılmış kopya,
  çoktan ayrışmış). Sağlayıcılarda yalnız gerçekten farklı olan kaldı: argv,
  ortam, ve çözdükleri zarf. Zarflar paylaşılmadı çünkü aynı şey değiller —
  üç CLI'ın JSON çıktısı üç şekildir, ve onları bir tabloya indirmek olmayan
  bir ortaklık uydurmaktır. Mevcut testlerin tamamı değişmeden geçti.
- **`ollama` sağlayıcısı**, ve iki şey ölçüldü:
  - **stdout temiz.** İlk sonda akışları birleştirmişti ve cevabı spinner ile
    imleç kaçış dizilerinin arasında gösterdi — bu, bir ANSI temizleyicisi
    yazmayı haklı çıkarırdı. stderr'ı atınca stdout'un tam olarak `OK\n\n`
    taşıdığı görüldü. Süsün hepsi stderr'da; gözlem tahmini bir komutla yendi.
  - **`--hidethinking` isteğe bağlı değil.** Bu makinede kurulu `qwen3:8b`'nin
    yetenekleri arasında `thinking` var; bayrak olmadan modelin akıl yürütmesi
    çıktının parçası oluyor ve bu paketteki her çağıran çıktıyı ayrıştırıyor.
- **Model keşfi ad tahminiyle değil, yetenekle.** `ollama show` bir
  `Capabilities` bloğu yazıyor — `nomic-embed-text` için `embedding`, `qwen3`
  için `completion`. Gömme modeli listeden adı öyle göründüğü için değil, öyle
  olduğunu söylediği için çıkıyor.
- **`LLMProviderChoice.Discovered`** — allow-list ile keşif arasındaki gerçek
  çatışmanın çözümü. Sabit bir model listesi burada *başka bir makinenin*
  dosyalarının listesi olurdu; ama listesiz bir sağlayıcı da `Validate`'in
  haklı olarak reddettiği şey (seçilip sonra başarısız olan bir sağlayıcı).
  Keşfedilen sağlayıcı listeden muaf, ve yalnız ondan: yine bir varsayılanı
  olmak zorunda. Model adı yerine **şekli** denetleniyor, ve bunun neden
  yeterli olduğu yazılı: `runCLI` `exec.Command` kullanıyor, kabuk yok, ikili
  ve bayraklar sağlayıcının kendisinde sabit, model adı bilinen bir konumda tek
  bir argüman. Keyfi bir adla ulaşılabilecek şey "model bulunamadı"; başka bir
  komut değil.
