# task-81 — Lead-gen bir alt-ajan: kartı olan, kuyruğa giren, akan

- **Status:** done
- **Owner agent:** Coder
- **Prerequisites:** task-79 (executor dikişi), task-77 (ajan defteri + skill kapısı)
- **Primary paths:** `internal/leadgenjob/**` (yeni), `internal/leadgen/pipeline.go`,
  `cmd/mimir-daemon/main.go`
- **Roadmap bucket:** B.10 — alt-ajanlar

## Context

task-79 dikişi açtı ama tek kayıtlı executor hâlâ `claudeExecutor`. Bu task ilk
hesapsız ajanı takar: lead-gen.

Yeni bir paket, çünkü `tasks/README.md`'nin paralellik kuralı #2 tam olarak bunu söylüyor:
"A new package with a constructor argument conflicts with nothing." `internal/leadgenjob`
hem `coderunner` (tipler) hem `leadgen` (`*Pipeline`) import eder ve `runner.go`'ya
**hiç dokunmaz** — task-79'u önce yapmanın bütün kazancı budur.

`POST /maps/leadgen` **senkron kalır**. Üç sebep: Leadgen ekranının kendi etkileşimi
odur (operatör bölgeyi yazar ve tablonun dolmasını izler; onu karta çevirmek o ekrana
kendi sonucunu görmek için board'u yoklatmak olurdu); `/maps/leadgen/export` ve
`/maps/outreach` aynı `Pipeline`'a başka noktalardan giriyor ve birini karta çevirip
diğerlerini bırakmak tek ekranı iki paradigmaya bölerdi; ve rota koruması
`Deps.LeadGen != nil`, `Deps.Runner`'dan bağımsız — runner'ı olmayan bir daemon onu
yine servis eder.

Kart ve rota **aynı** `Pipeline.Run`'ı çağırır. Kart "bunu kuyruğa al ve bitince söyle"
yoludur; rota "şu an bakıyorum" yolu.

## Scope (do exactly this)

1. **`internal/leadgenjob`** — `coderunner.Executor` uygulaması:
   - `Agent() == "leadgen"`, `Lane() == LaneWorker`, `NeedsProject() == false`,
     `RequiredSkills() == []string{"lead-outreach"}`.
   - `Prepare` — `row.Params`'ı `leadgen.RunRequest`'e çözer; çözemezse hata döndürür
     ve kart dispatch'te reddedilir (task-79'un sert kapısı), `failed` olur ve sebebi
     kartta yazar.
   - `Execute` — `Pipeline.Run`'ı çağırır ve her aşamayı `Sink` üzerinden yayar:

     | `Sink` çağrısı | Olay | Operatörün gördüğü |
     |---|---|---|
     | `Started("", "")` | `run.started` | kart canlanır |
     | `Step("region_search", …)` | `tool.call`/`tool.result` | "İstanbul — 62 işletme (mapscrape)" |
     | `Step("categorize", …)` | `tool.call`/`tool.result` | kategori dağılımı |
     | `Step("contacts", …)` | `tool.call`/`tool.result` | "41 telefon, 12 site" |
     | `Say(note)` | `text.delta` | her `Report.Notes` satırı düştükçe |
     | terminal `Outcome` | `run.completed` | şirket sayısı |

     Desktop'ın `formatEventLine`'ı (`desktop/src/lib/terminals.ts`) dört olay türünü
     de zaten çiziyor: **lead-gen kartının terminali vardır, sadece araç çağrısı yerine
     aşama gösterir.**
2. **Paylaşılan izin havuzu `internal/leadgen`'in kendisinde** (SD-3). İki giriş
   noktası tek `Pipeline`'a bakıyor: HTTP rotası hiçbir sınıra uymuyordu, kartınki ise
   dispatcher'da olurdu. Havuz `Pipeline`'ın içine konur ki **ikisi de** aynı kaynaktan
   çeksin; rota tükendiğinde `writeDomainError` ile 429 şeklinde cevap verir.
   Dispatcher'ın `busyWorkers` kontrolü böylece tek sınır değil, bir optimizasyon olur.
3. **`cmd/mimir-daemon/main.go`** — executor runner'a kaydedilir. Tek satırlık wiring.

## Out of scope (do NOT do here)

- `runner.go`, `executor.go`, `ratelimit.go`, `store/runs.go` — hiçbirine dokunulmaz.
  Dokunulması gerekiyorsa task-79 eksik kalmış demektir.
- `POST /maps/leadgen`'in kaldırılması ya da Leadgen ekranının taşınması.
- Kartın gövdesinin nasıl göründüğü — task-82.
- Ajanı seçen model çağrısı — task-77'nin `Route`'u; bu executor ajanı hazır alır.

## Interfaces / contracts

```go
// internal/leadgenjob
type Executor struct{ pipeline *leadgen.Pipeline }
func New(p *leadgen.Pipeline) *Executor

// row.Params'ın şekli:
// {"region":"İstanbul","query":"diş kliniği","with_contacts":true,
//  "with_gap_analysis":false,"with_emails":false}
```

## Definition of Done

- [x] `TestLeadgenExecutor_EmitsAStageEventForEveryPipelinePhase`.
- [x] `TestLeadgenExecutor_RefusesACardWhoseParamsWillNotDecode` — sebep kartta.
- [x] `TestLeadgenExecutor_NeedsNoAccountAndNoProject`.
- [x] `TestPipeline_SharesOnePermitPoolBetweenTheRouteAndTheCard`.
- [x] `TestReconcile_RequeuesAnInterruptedLeadgenCard` — yarıda kalan kart `failed`
      değil `queued`; ledger'daki kısmi yazım `ON CONFLICT DO UPDATE` olduğu için
      bozulmadan durur (`leads.go:132-133`).
- [x] Uçtan uca: hiçbir Claude hesabı bağlı değilken bir lead-gen kartı kuyruğa girip
      çalışıyor, `/ws/runs/{id}` aşamaları akıtıyor, ledger'a yazıyor.
- [x] `make check` yeşil.
- [x] Status `done` + changelog.

## Notes for the reviewer (Opus)

- **SD-3**: izin havuzu gerçekten `Pipeline`'da mı, yoksa dispatcher'da mı? Rota da
  ondan çekiyor mu?
- **SD-6**: yarıda kalan kart `queued`'a mı dönüyor? Kısmi ledger yazımı yıkıcı mı?
- Diff `internal/coderunner/**` altında bir dosyaya dokunuyor mu? Dokunuyorsa
  task-79 eksik bitmiş demektir — bunu söyle, burada tamamlama.

---

## Changelog — 2026-09-06

**Yeni paket `internal/leadgenjob`.** `coderunner.Executor` uygulaması:
`Agent() == "leadgen"`, `Lane() == LaneWorker`. `Prepare` kartın `params`'ını
çözüyor — çözemezse hiçbir şey claim edilmeden reddediliyor, yani sebep yarım
kalmış bir bölge aramasının ortasında değil kartın üstünde beliriyor.
`Execute` `Pipeline.Run`'ı çağırıp her aşamayı `Sink` üzerinden anlatıyor.

`internal/coderunner` altında **hiçbir dosyaya dokunulmadı** — task-79'u önce
yapmanın bütün kazancı buydu ve tuttu.

**Parse'ın prompt'a düşmesi.** Yönlendirici serbest metinden `leadgen` ajanını
seçtiğinde kartta `params` olmayabilir. O durumda prompt sorgu olarak
kullanılıyor: operatörün düz cümleyle yazdığı kart bir ret değil bir arama
üretiyor.

**Paylaşılan izin havuzu — `internal/leadgen`'in kendisinde.** İki giriş noktası
tek `Pipeline`'a bakıyor: `POST /maps/leadgen` hiçbir sınıra uymuyordu, kartınki
dispatcher'da olurdu ve diğerini sınırsız bırakırdı. `Pipeline.permits`
(`cfg.MaxConcurrentLeadgenRuns = 1`) ikisinden de çekiliyor. `acquire` **asla
bloke olmuyor** — bekleyen bir çağıran, operatörün göremediği bir işin arkasında
bir HTTP isteğini açık tutardı, ki kuyruğun var olma sebebi tam olarak budur.
Tükendiğinde `ErrBusy` → 429.

**Anlatım.** `region_search`, `categorize`, `contacts`, `gap_analysis`,
`outreach` — yalnız pipeline'ın gerçekten koştuğu aşamalar. `Report.Notes`
`text.delta` olarak akıyor, çünkü pipeline hata vermek yerine bozuluyor (SD-6)
ve kısmi bir cevabın kısmi olduğunu söyleyen tek yer orası. Kategori özeti
sıralı: aynı bölgenin iki transcript'i karşılaştırılabilir olmalı, map iterasyon
sırası bunu engellerdi.

**Kararlar.** `POST /maps/leadgen` senkron kaldı. Kart "kuyruğa al ve bitince
söyle", rota "şu an bakıyorum" yolu; ikisi aynı `Pipeline.Run`'a giriyor.

`make check` yeşil.
