# task-77 — Skill deposu, ajan defteri, ve iki sert kapı

- **Status:** done
- **Owner agent:** Coder
- **Prerequisites:** task-41 (brain çekirdeği + `internal/llm`), task-70 (operatör kural dosyaları)
- **Primary paths:** `internal/skills/**` (yeni), `internal/agents/**` (yeni),
  `internal/mcp/{tool.go,finalize.go,server.go}`, `internal/coderunner/runner.go`,
  `internal/config/config.go`, `internal/api/{api.go,skills.go}`,
  `internal/tools/register.go`, `cmd/mimir-daemon/main.go`, `cmd/mimir-mcp/main.go`
- **Roadmap bucket:** B.10 — alt-ajanlar

## Context

Repoda "skill" diye bir kavram yok. Geçen iki kelime de task-72'nin graphify CLI'ını
"bir skill yükleyici" diye tarif ettiği yorum satırları — yani bugün Mimir'in bir işi
*nasıl* yapacağını söyleyen tek yapılandırılmış metin, `internal/settings/rules/`
altındaki iki outreach kural dosyası.

Bu task o boşluğu, o kural dosyalarının kendi desenini kullanarak doldurur: gömülü
varsayılan (`//go:embed`), diskte operatörün düzenleyebildiği kopya, ve gövdenin
hash'i olan bir sürüm — çünkü bir skill'in sessizce değişmesi, onunla üretilmiş her
şeyi açıklamasız değiştirir (`settings.go:293` `RuleBody`'nin var olma sebebi budur).

Üstüne bir **ajan defteri** gelir: bir işin hangi alt-ajana gideceğini model seçer,
ve her ajan en az bir skill'e **zorunlu** olarak bağlıdır. Zorunluluk bir varsayılan
değil, bir kapıdır: skill yüklenemiyorsa iş çalışmaz.

`internal/settings` açılmaz. Sıcak dosyadır ve buraya eklenecek şey onun sorusunu
(operatörün kampanya başına verdiği karar) değil, başka bir soruyu (bir ajanın işini
nasıl yaptığı) cevaplar.

## Scope (do exactly this)

1. **`internal/skills`** — `internal/settings`'in `Rule` deseninin aynısı:
   - `//go:embed skills/*.md`, dört varsayılan: `marketing.md`, `code-review.md`,
     `graph-query.md`, `lead-outreach.md`.
   - `Skill{ID, Title, Path, Body, IsDefault, Version, UpdatedAt}`; `Version` =
     `sha256(body)`'nin ilk 8 hex hanesi.
   - `New(dir)`, `Skill(id)` (ilk okumada diske tohumlar), `Skills()`, `PutSkill(id, body)`
     (boş gövde = reset), `ResetSkill(id)`, `Body(id) (body, version string)` — sıcak yol,
     hata döndürmez, okunamayan skill gömülü varsayılana düşer (SD-6).
   - `IDs()` kapalı küme; bilinmeyen id → `ErrUnknownSkill`.
   - Dizin: `filepath.Dir(cfg.StorePath)/skills`.
   - `lead-outreach` gövdesi kural dosyalarını **silmez**; `settings.RuleBody(ch)`
     ile okunanın üstüne binen bir çerçeve yazar.
2. **`internal/agents`** — derlenmiş sabit defter (SD-1):
   - `Agent{Key, Name, Desc, Exec, RequiredSkills, ModelClass}`, `Executor` string
     tipi (`ExecClaude`, `ExecLeadgen`).
   - Beş ajan: `coding`, `review`, `marketing`, `leadgen`, `graph`.
   - `Registry() []Agent`, `Lookup(key) (Agent, bool)`, `Keys() []string`.
   - `RequiredSkills` hiçbir ajanda boş olamaz — bunu bir test zorlar.
3. **`internal/agents/route.go`** — yönlendirici:
   - `Route(ctx, c Completer, title, prompt string) (Choice, error)`;
     `Choice{Agent, Skills, Why, Source}` (`Source`: `model` | `rules` | `default`).
   - Modele yalnız `Registry()` anahtarları teklif edilir ve dönen anahtar o listede
     değilse reddedilir — `brain/relate.go:semanticEdges`'in izin listesi deseni.
   - Model ulaşılamaz ya da cevabı geçersizse anahtar kelime tablosuna, o da tutmazsa
     `coding`'e düşer. Yönlendirici **asla** hata döndürüp işi bloke etmez (SD-6).
   - Sınıf: `llm.Distill` — bu kapalı kelimeli bir sınıflandırma, `Reason` değil.
4. **Sert kapı (a) — dispatcher.** `coderunner`:
   - `Runner` yeni bir bağımlılık alır: `SkillSource interface{ Body(id string) (string, string) }`.
   - Bir koşu başlamadan önce ajanın `RequiredSkills`'i çözülür. Çözülemezse
     `ErrSkillUnavailable` — koşu `queued`'da kalır, `failed` olmaz.
   - Çözülünce gövdeler tek dosyada birleşip `--append-system-prompt-file` ile
     `claude` komutuna girer; dosya koşunun geçici dizinindedir ve koşu bitince silinir.
   - Kullanılan skill id'leri ve sürümleri koşu satırına yazılır (sütunlar task-79'da
     gelir; bu task'ta alan `Run` struct'ında taşınır ve store'a yazılması task-79'a bırakılır).
5. **Sert kapı (b) — MCP.**
   - `internal/mcp`: `SkilledTool interface{ Skills() []string }` opsiyonel arayüzü.
   - `Registry`/`Server` bir `SkillSource` tutar ve `finalizeResponse` (finalize.go:33)
     içinde: araç skill bildiriyorsa ve gövde yüklenemiyorsa `ErrSkillUnavailable` —
     yanıt değil hata, mevcut fail-closed davranışın aynısı.
   - Yüklenebiliyorsa: **süreç başına ilk kez** o skill'i taşıyan yanıta gövde
     `skill` alanında iliştirilir; sonraki yanıtlar yalnız `{id, version}` taşır.
   - `SkillBudgetTokens()` opsiyonel arayüzü: skill gövdesi bütçeyi **atlatmaz**,
     açıkça ona eklenir (SD-7).
6. **HTTP** — `internal/api/skills.go`:
   `GET /skills`, `PUT /skills/{id}`, `POST /skills/{id}/reset`, `GET /agents`.
   Rota yalnız `Deps.Skills != nil` iken kaydedilir.
7. **Wiring** — `cmd/mimir-daemon` ve `cmd/mimir-mcp` birer `skills.New(...)` kurar
   ve MCP sunucusuna verir. `internal/config`: `SkillDir` türetmesi ve
   `SkillBodyMaxTokens` sabiti.

## Out of scope (do NOT do here)

- `coding_runs` şeması, `agent`/`skills` sütunları, executor dikişi — **task-79**.
- Board'un kendisi, `GET /board` — task-79.
- Grafik traversal'ları ve `graph_*` MCP araçları — **task-81**. `graph` ajanı bu
  task'ta deftere yazılır ama çalıştıracak bir executor'ı henüz yoktur.
- Her türlü desktop değişikliği — task-78.
- `internal/settings/rules/{email,whatsapp}.md`'nin emekliye ayrılması.

## Interfaces / contracts

```go
// internal/skills
type Skill struct {
    ID, Title, Path, Body string
    IsDefault bool
    Version   string
    UpdatedAt time.Time
}
func New(dir string) *Store
func IDs() []string
func Default(id string) (string, error)
func (s *Store) Skill(id string) (Skill, error)
func (s *Store) Skills() ([]Skill, error)
func (s *Store) PutSkill(id, body string) (Skill, error)
func (s *Store) ResetSkill(id string) (Skill, error)
func (s *Store) Body(id string) (body, version string)
var ErrUnknownSkill = errors.New("skills: unknown skill")

// internal/agents
type Executor string
const (ExecClaude Executor = "claude"; ExecLeadgen Executor = "leadgen")
type Agent struct {
    Key, Name, Desc string
    Exec            Executor
    RequiredSkills  []string
    ModelClass      llm.Class
}
func Registry() []Agent
func Lookup(key string) (Agent, bool)
func Keys() []string

type Choice struct{ Agent string; Skills []string; Why, Source string }
type Completer interface {
    Complete(ctx context.Context, c llm.Class, r llm.Request) (llm.Response, error)
}
func Route(ctx context.Context, c Completer, title, prompt string) (Choice, error)

// internal/mcp
type SkilledTool interface{ Skills() []string }
type SkillBudgeted interface{ SkillBudgetTokens() int }
type SkillSource interface{ Body(id string) (body, version string) }
var ErrSkillUnavailable = errors.New("mcp: required skill could not be loaded")

// internal/coderunner
var ErrSkillUnavailable = errors.New("coderunner: required skill could not be loaded")
```

Ajan defteri (kod içinde sabit):

| Key | Exec | RequiredSkills | ModelClass |
|---|---|---|---|
| `coding` | claude | `code-review`* | reason |
| `review` | claude | `code-review` | reason |
| `marketing` | claude | `marketing` | reason |
| `leadgen` | leadgen | `lead-outreach` | distill |
| `graph` | claude | `graph-query` | distill |

\* `coding` ajanının zorunlu skill'i, "yazdığın kodu kendi repo'sunun kurallarına
göre gözden geçir" çerçevesidir; ayrı bir `review` kartı açmayı gereksiz kılmaz.

## Definition of Done

- [x] Dört skill gömülü, diske tohumlanıyor, düzenlenip resetlenebiliyor.
- [x] `TestShippedSkills_FitTheirCeiling` — dördü de `SkillBodyMaxTokens` altında.
- [x] `TestRegistry_EveryAgentDeclaresASkill` — `RequiredSkills` boş ajan yok.
- [x] `TestRoute_RefusesAnAgentTheRegistryDoesNotOffer`.
- [x] `TestRoute_FallsBackToTheRuleTableWhenTheModelIsDown` — hata değil, seçim döner.
- [x] `TestRoute_MakesNoModelCallWhenTheOperatorAlreadyChose`.
- [x] `TestDispatch_RefusesACardWhoseSkillWillNotLoad` — koşu `queued`'da kalıyor.
- [x] `TestFinalize_AttachesASkillBodyOnceAndThenOnlyItsVersion`.
- [x] `TestFinalize_SkillBodyIsAddedToTheBudgetNotHiddenFromIt`.
- [x] `TestFinalize_FailsClosedWhenADeclaredSkillIsMissing`.
- [x] `GET /skills`, `PUT /skills/{id}`, `POST /skills/{id}/reset`, `GET /agents` çalışıyor.
- [x] `make check` yeşil.
- [x] Status `done` + changelog.

## Notes for the reviewer (Opus)

- **SD-1**: ajan defteri ve skill id'leri kodda sabit mi? `os.Getenv` eklendi mi?
  Skill *gövdesi* operatörün — bu `internal/settings`'in zaten kabul edilmiş istisnası,
  yeni bir kategori değil.
- **SD-2 / SD-7**: skill gövdesi bütçeyi atlatmıyor, ona ekleniyor mu? Bir skill'i
  büyütmek bir aracın yanıtını sessizce tavana çarptırabilir mi?
- **SD-6**: yönlendirici hiçbir yolda hata döndürüp işi bloke etmiyor; sert kapı ise
  typed error ile konuşuyor ve koşuyu `failed` değil `queued` bırakıyor.
- **SD-8**: `internal/settings`'in test üslubu (gerçek `t.TempDir()`, elle fake).

---

## Changelog — 2026-09-06

**Yeni paketler**

- `internal/skills` — `internal/settings`'in kural dosyası deseninin aynısı:
  `//go:embed skills/*.md` ile dört varsayılan, ilk okumada diske tohumlama,
  `PutSkill`/`ResetSkill`, ve gövdenin sha256'sının ilk 8 hanesi olan `Version`.
  `Body(id)` sıcak yol: okunamayan operatör dosyası gömülü varsayılana düşer,
  yalnız **bilinmeyen** bir id boş döner — kapıların gerçekten kapandığı yer orası.
  `Compose(ids)` ya hepsini taşır ya hiçbirini; yarım karşılanmış bir sözleşme yok.
- `internal/agents` — beş ajanlık sabit defter (`coding`, `review`, `marketing`,
  `leadgen`, `graph`) ve `Route`. Yönlendirici modele yalnız defterin anahtarlarını
  teklif ediyor ve dışındaki cevabı reddediyor (`brain/relate.go` deseni); model
  ulaşılamazsa anahtar kelime tablosuna, o da tutmazsa `coding`'e düşüyor
  (`leadgen/categorize.go` deseni). Hiçbir yolda hata döndürüp kartı bloke etmiyor.
  Skill'ler her hâlükârda **defterden** okunuyor — yönlendirme cevabı onları
  genişletemiyor ya da daraltamıyor, yoksa zorunluluk tavsiyeye dönerdi.

**İki sert kapı**

- MCP (`internal/mcp/finalize.go`): `SkilledTool` ve `SkillBudgeted` opsiyonel
  arayüzleri, `skillGate`, ve `finalizeWithSkills`. Kapı **boyut kontrolünden
  önce** çalışıyor ki ek ölçülsün; skill bütçesi aracın kendi bütçesine
  **ekleniyor**, ondan kesilmiyor. Gövde süreç başına bir kez gidiyor, sonra
  yalnız `{id, version}` — ve operatör gövdeyi düzenlerse yeniden gidiyor.
  Kaynağı olmayan bir süreçte skill bildiren araç **hata veriyor**: wiring hatası,
  zorunluluğun geçerli olmadığı bir süreç gibi okunamaz.
  Mevcut `finalizeResponse(tool, v)` imzası korundu, `finalize_test.go` düzenlenmedi.
- Dispatcher (`internal/coderunner/skills.go`): `SetSkills`, `requireSkills`,
  `writeSkillFile`. Kapı üç yerde — `insert` ve `Enqueue`'da operatör hâlâ
  bastığı şeye bakarken, `launch`'ta ise **claim-then-finish** ile, çünkü sıradaki
  satırı atlamak onu her pump'ta yeniden denemek ve operatöre hiç başlamayan ve
  sebebini söylemeyen bir kart göstermek olurdu. Çözülen gövde CLI'a
  `--append-system-prompt-file` ile giriyor; dosya koşu bitince siliniyor.

**Yüzey**

- `GET /agents` — **koşulsuz** kayıtlı (`GET /coding-models` gibi: shipped bir
  sabitin görünümü, boş bir liste yanlış olurdu).
- `GET /skills`, `PUT /skills/{id}`, `POST /skills/{id}/reset` — `Deps.Skills` varken.
- `writeDomainError`: `skills.ErrUnknownSkill` → 404, `coderunner.ErrSkillUnavailable`
  → 409 (durum yanlış, istek değil).
- `internal/config`: `SkillDir` (store'un yanında türetiliyor) ve
  `SkillBodyMaxTokens = 600`, `Validate` kapsamında.
- Wiring: tek `skills.New(cfg.SkillDir)` üç tüketiciye de veriliyor — ekran,
  dispatcher, MCP choke-point — ki ekranda yapılan düzenleme bir sonraki koşunun
  ve bir sonraki araç çağrısının bağlı olduğu metin olsun. `mimir-mcp` aynı dizini
  okuyor.

**Kararlar**

- Skill kümesi **kapalı**. Yeni bir skill eklemek bir varsayılan shipping etmek
  demek, bir yere string eklemek değil (`settings.Channel`'ın gerekçesinin aynısı).
- Kaynağı olmayan bir `Runner` zorunluluğu kapalı bir runner değil: gömülü gövdeler
  her zaman orada. Gerçekten başarısız olabilecek tek şey, binary'nin shipping
  etmediği bir skill — yani defterin kaymış olması — ve o bir ret.
- Her ajanın en az bir zorunlu skill'i olduğunu bir test zorluyor; `requireSkills`
  ayrıca çalışma zamanında da reddediyor, ki testten kaçan bir ajan yönergesiz koşamasın.

**Sapmalar:** yok. `make check` yeşil (`-race` dahil). Mevcut hiçbir test dosyası
düzenlenmedi.
