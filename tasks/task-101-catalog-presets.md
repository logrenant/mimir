# task-101 — Preset seçimi, profil listesi ve beceri sürümü düzeltmesi

- **Status:** done
- **Owner agent:** Coder
- **Prerequisites:** task-87, task-91
- **Primary paths:** `internal/api/api.go`, `internal/api/catalog.go`, `internal/catalog/ops.go`, `internal/skills/skills.go`, `internal/coderunner/skills.go`, `internal/agents/agents.go`, `internal/tools/catalog.go`
- **Roadmap bucket:** Katalog — ürün içeriği stüdyosu

## Context

Üç ayrı eksik, hepsi aynı yüzeyde.

**1. Profilleri kimse listeleyemiyor.** `catalog.Dialects()` task-85'te ihraç
edildi ve bugüne kadar **hiç çağrılmadı**. Masaüstü, profil adlarını
`desktop/src/lib/catalog.ts` içindeki `DIALECT_LABELS` sabitine elle kopyalamış.
Daemon'a bir profil eklendiğinde ekran onu tanımıyor; task-91'de `ikas-en`
silindiğinde de ekranda kaldı. İki yerde tutulan kapalı bir küme, bir süre sonra
iki farklı kapalı küme olur.

**2. Operatör preset seçemiyor.** `Detect` otomatik çalışıyor ve yanıldığında —
ya da bir mağaza kendi sütunlarını yeniden adlandırdığında — tek çıkış yolu elle
sütun eşleme formu. Oysa doğru profil çoğu zaman listede duruyor, sadece imza
tutmuyor. Kullanıcı "şu preset'i kullan" diyebilmeli.

**3. Kart taslakları ekranda görünmüyor.** `internal/api/catalog.go:500`:

```go
// catalogSkillVersion is ... empty until task-87 ships the skill
func (s *Server) catalogSkillVersion() string { return "" }
```

task-87 gemide: `internal/agents` `catalog` ajanına `skills.ProductContent`'i
zorunlu kılıyor, yani `coderunner.requireSkills` `"product-content:<v>"`
döndürüyor ve `catalogjob` onu `RewriteRequest.SkillVersion` olarak taşıyor.
Kart taslağı `content-v1@<model>:<brand>#product-content:<v>` altına yazıyor;
her HTTP okuması ise `catalogSkillVersion()` boş döndüğü için
`content-v1@<model>:<brand>` anahtarına bakıyor. `GetCatalogDraft` sürümü
**birebir** eşliyor (`internal/store/catalog.go:350,371`), dolayısıyla toplu
yeniden yazmanın ürettiği hiçbir taslak ürün ekranında görünmüyor. Aynı saplama
`internal/tools/catalog.go:161,250`'de de var.

Kök sebep, sürümün **iki yerde ayrı ayrı** kuruluyor olması. Düzeltme sadece
saplamayı doldurmak değil, iki tarafın aynı fonksiyonu çağırması.

## Scope (do exactly this)

1. `internal/skills`: sürüm/gövde birleştirmesini tek bir yere al.
   `type BodySource interface { Body(id string) (body, version string) }` ve
   `func Require(src BodySource, ids []string) (body, version string, ok bool)`.
   Sürüm dizesi bugünkü `requireSkills` ile **birebir aynı** kalır:
   `<id>:<version>`, verilen sırayla `|` ile birleşik.
2. `internal/coderunner/skills.go`: `requireSkills` gövdeyi ve sürümü
   `skills.Require`'dan alır; `SkillSource` `skills.BodySource`'a takma ad olur.
   Hata mesajları ve `ErrSkillUnavailable` davranışı değişmez.
3. `internal/agents/agents.go`: `KeyCatalog = "catalog"` sabiti; registry onu kullanır.
4. `internal/api/api.go`: `SkillStore` arayüzü `Body(id string) (string, string)`
   kazanır (`*skills.Store` zaten sağlıyor).
5. `internal/api/catalog.go`: `catalogSkillVersion()` `skills.Require` ile
   `catalog` ajanının `RequiredSkills`'inden gerçek sürümü üretir; beceri kaynağı
   yoksa boş döner (anahtarın o yarısı yine görünmez).
6. `internal/tools/catalog.go`: aynı sürümü kullanır, `Selection{}` yerine
   operatörün kayıtlı seçimi — okuma ve yazma aynı anahtara bakmalı.
7. `internal/catalog/ops.go`: `func (s *Studio) SetDialect(ctx, id, key string, sel llm.Selection) (Import, error)`.
   Bilinmeyen anahtar `ErrUnknownDialect`. Profil bağlanırken `Detect`'in yaptığı
   gibi dosyanın **kendi yazımına** bağlanır. Boş anahtar otomatik algılamaya döner.
   Sonrasında `reread` — `SetMapping` ile aynı ikinci yarı.
8. `internal/api`: `GET /catalog/profiles` → `{profiles: [Dialect]}`;
   `PUT /catalog/imports/{id}/dialect` → `{key}` → `catalogImportResponse`.

## Out of scope (do NOT do here)

- Dil boyutu, `Locales`, çok dilli hiçbir şey (task-103).
- Yeni bir dialect profili eklemek. Gerçek dosya olmadan profil yazılmaz.
- Masaüstü (task-102).

## Interfaces / contracts

```go
// internal/skills
type BodySource interface{ Body(id string) (body, version string) }
func Require(src BodySource, ids []string) (body, version string, ok bool)

// internal/catalog
var ErrUnknownDialect = errors.New("catalog: unknown dialect")
func (s *Studio) SetDialect(ctx context.Context, id, key string, sel llm.Selection) (Import, error)
```

```
GET /catalog/profiles           -> {"profiles":[{"key","name","group_by","columns"}]}
PUT /catalog/imports/{id}/dialect  {"key":"ikas-fields"} -> catalogImportResponse
```

## Definition of Done

- [x] `skills.Require` tek kaynak; `requireSkills` onu çağırıyor
- [x] Anahtar bileşimi `Studio.CurrentDraftVersion`'a taşındı; HTTP, MCP ve kart
      üçü de onu soruyor. `UseDraftKey` ile daemon'da bağlandı.
- [x] `TestCurrentDraftVersion_FindsWhatARewritePassWrote` — iki tarafı birden
      kapsıyor ve eski davranışta düştüğü doğrulandı
- [x] `SetDialect` bilinmeyen anahtarı reddediyor, boş anahtar algılamaya dönüyor,
      hiçbir sütununu okumadığı dosyayı reddediyor
- [x] `GET /catalog/profiles` ve `PUT …/dialect` testli; studio yokken 404
- [x] `make check` yeşil
- [x] Status `done`, CHANGELOG

## task-103 için bulgu (burada düzeltilmedi)

`internal/catalog/stored.go:69`:

```go
if imp.File.Dialect.IsZero() && len(imp.File.Mapping) == 0 {
	if d, ok := Detect(imp.File.Header); ok { imp.File.Dialect = d }
}
```

Üstündeki yorum "sonradan eklenen bir profil, ondan önce içe aktarılmış
dosyalara da uygulanır" diyor ama koşul bunu yalnızca **hiçbir profile
uymamış** dosyalar için yapıyor. `ikas-fields`'a zaten uymuş bir içe aktarım,
saklanmış bağlı `Columns`'ını sonsuza kadar koruyor — ve `Reread` de aynı bayat
lehçe üzerinden okuyor. Yani profile `Html:Detay-AR` eklendiğinde mevcut hiçbir
içe aktarıma ulaşmaz.

Koşul `len(imp.File.Mapping) == 0` olmalı: operatörün kendi haritası yine
kazanır, yorum da doğru olur. task-103'te dil sütunları eklenirken zorunlu.

## Notes for the reviewer (Opus)

- Sürüm dizesi bugünküyle byte-identical mi? Değilse her mevcut taslak bir kere
  boşa çıkar — kabul edilebilir ama bilerek yapılmalı.
- `SetDialect` profili dosyanın kendi başlık yazımına bağlıyor mu, yoksa
  profildeki yazımı mı yazıyor? İkincisi export'u bozar.
