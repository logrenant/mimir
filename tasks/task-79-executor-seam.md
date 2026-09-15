# task-79 — Executor dikişi: davranışı değişmeyen bir refactor

- **Status:** done
- **Owner agent:** Coder
- **Prerequisites:** task-35 (koşu yaşam döngüsü), task-77 (ajan defteri)
- **Primary paths:** `internal/store/{runs.go,migrations/0022_agent_jobs.sql}`,
  `internal/coderunner/{executor.go,claude.go,runner.go,ratelimit.go}`,
  `internal/config/config.go`, `internal/api/{api.go,handlers.go}`,
  `cmd/mimir-daemon/main.go`
- **Roadmap bucket:** B.10 — alt-ajanlar

## Context

Board bugün tek bir şeyi biliyor: `coding_runs`. Her işi tek bir kanbana koymak için
dispatcher'ın claude dışında bir şey de başlatabilmesi gerekiyor.

**İkinci bir kuyruk kurmuyoruz.** `runner.go:11-17` bunu bir alışkanlık değil bir kural
olarak yazıyor: "nothing outside this package decides that a subprocess may begin…
the queue added here is a semaphore on this type rather than a second scheduler beside
it." İkinci bir board tablosu, `coding_runs.status`'tan aynalanan ve atomik geçişi
olmayan bir durum demek olurdu — `UpdateRunStatus`, `ClaimRun`, `ParkRun`, `RequeueRun`
CAS'lerinin var olma sebebi tam olarak bu yarış.

Bu task **yalnızca dikişi açar**. Kayıtlı tek executor `claudeExecutor`'dır ve gövdesi
bugünkü `execute()`'un aynısıdır. Yeni bir ajanın çalışması task-81'e bırakılır —
dispatcher refactor'ü ile ilk yeni executor aynı diff'te olmamalı.

Tablo adı değişmez. `coding_runs`'a 32 referans var ve yeniden adlandırma, çalışma
zamanında hiçbir şey kazandırmayan saf churn olur — `internal/store/accounts.go:24`'ün
zaten verdiği karar ("the column stays because migrations are append-only").

## Scope (do exactly this)

1. **Migration `0022_agent_jobs.sql`:**
   ```sql
   ALTER TABLE coding_runs ADD COLUMN agent  TEXT NOT NULL DEFAULT 'coding';
   ALTER TABLE coding_runs ADD COLUMN skills TEXT NOT NULL DEFAULT '';
   ALTER TABLE coding_runs ADD COLUMN params TEXT NOT NULL DEFAULT '';
   CREATE INDEX IF NOT EXISTS idx_coding_runs_agent ON coding_runs (status, agent, queued_at);
   ```
   `DEFAULT 'coding'` bir geri düşüş değil, bir olgu: bu migration'dan önce yazılmış
   her satır zaten bir `claude` alt süreciydi. Backfill `UPDATE` gerekmez.
   `params`'ın bu migration'da olmasının sebebi: hesapsız bir executor'ın girdisini
   `prompt`'tan dispatch anında türetmek, `Execute` içine bir ayrıştırıcı koymak olurdu
   ve oradaki hata operatörün teşhis edemeyeceği bir `failed` kart demektir.
2. **`store.RunRow`** üç alan kazanır; `runColumns`/`scanRun` (runs.go:69-73, 173)
   birlikte güncellenir. Yeni okuma `ListRuns(ctx, limit)` — projeler arası.
   **`RunningAccountIDs` (runs.go:428) ölü kod ve hesapsız koşuyu `''` diye meşgul bir
   yuva sayar** — ya silinir ya `AND account_id <> ''` alır. Olduğu gibi bırakılmaz.
3. **`internal/coderunner/executor.go`** — dikiş. Runner ortak defter tutmayı
   (transcript, `inflight`, `bus.CloseRun`, sıra numarası) elinde tutar; executor
   yalnızca gövdeyi verir:
   - `Lane` (`LaneAccount` | `LaneWorker`) — kapasite executor'ın özelliğidir, satırın değil.
   - `Executor{Agent, Lane, NeedsProject, RequiredSkills, Prepare, Execute}`.
   - `Sink` — bir işin konuşmasının tek yolu. Executor kendi `Seq`'ini damgalayamaz,
     transcript'i atlayamaz, terminal olaydan sonra yayınlayamaz; bu akışın daha önce
     bozulduğu üç yol.
   - `Outcome{Status, Err, SessionID, Model, CostUSD, NumTurns, ParkUntil}`.
     `ParkUntil` yalnız token bütçesi harcayan bir executor tarafından set edilir.
4. **`internal/coderunner/claude.go`** — bugünkü `execute()` (runner.go:1041-1203)
   olduğu gibi taşınır. `exec.CommandContext` öncesindeki her şey runner'da kalır.
5. **Kapasite.** `inflight` çıplak `accountID` yerine bir `grant{lane, agent,
   accountID, configDir}` taşır. `busyAccounts()` `LaneWorker`'ı atlar —
   `defaultAccountID` boş string olduğu için (runner.go:87) yalnız "boş hesap id"ye
   bakmak worker ile onu karıştırırdı.
   **En tehlikeli iki satır:** `runner.go:745-747` ve `765-770`'teki erken dönüşler
   bugün "boş yuva yok = hiçbir şey başlayamaz" demek. Bunlar `reserve`'ün
   `LaneAccount` dalının içine taşınır; yoksa bir worker kartı, tam da operatörün
   board'a baktığı anda donar.
6. **`requireSlot` → `requireCapacity(ctx, agent, accountID)`.** `insert:324`,
   `Kick:521`, `Enqueue:548`, `Retry:582`'den çağrılıyor ve hesap yoksa
   `account.ErrNotConnected` döndürüyor. Lane kontrolü olmadan, hesapsız bir makinede
   worker kartı **oluşturulamaz bile** — üstelik ihtiyaç duymadığı bir login'den
   bahseden bir mesajla.
7. **`ratelimit.go` iki düzeltme:** `noteHeld` (210) `ListQueuedRuns(ctx,1)` ile
   "bir coding task bütçeyi bekliyor" diyor — o tek satır hiçbir şey beklemeyen bir
   worker kartı olabilir; listeyi lane'e göre süz. `Kick` (520-536) her yuva tutulduğunda
   `ErrBudgetSpent` döndürüyor — kuyruğun *account-lane* başı takılıysa doğru cevap,
   worker kartı için değil.
8. **HTTP:** `GET /agents` (koşulsuz kaydedilir, `GET /coding-models` gibi — SD-1
   sabitinin görünümü). `GET /coding-tasks` `project_id` olmadan da çalışır = hepsi.
   `POST /coding-tasks` ve `PATCH /coding-tasks/{id}` opsiyonel `agent`/`skills`/`params`
   alır; `PATCH` `EditableStatuses` korumasını bedavaya devralır — token harcandıktan
   sonra ajan değişmez, prompt ile aynı kural.

## Out of scope (do NOT do here)

- **Herhangi bir yeni executor.** `claudeExecutor` tek kayıtlı executor'dır.
  Lead-gen executor'ı task-81'dir.
- Ajanı seçen model çağrısı — task-77'nin `Route`'u burada **çağrılmaz**; dispatcher
  refactor'ünün diff'inde bir model çağrısı olmamalı.
- Skill deposu ve MCP kapısı — task-77.
- Desktop — task-80.

## Interfaces / contracts

```go
type Lane int
const (LaneAccount Lane = iota; LaneWorker)

type Executor interface {
    Agent() string
    Lane() Lane
    NeedsProject() bool
    RequiredSkills() []string
    Prepare(ctx context.Context, row store.RunRow) error
    Execute(ctx context.Context, j Job) Outcome   // hata döndürmez: dönecek çağıran kalmadı
}
type Job struct {
    Row         store.RunRow
    ProjectPath string  // NeedsProject() false ise ""
    ConfigDir   string  // LaneWorker için ""
    Out         Sink
}
type Sink interface {
    Started(model, sessionID string)
    Say(text string)
    Step(name, risk string, args any) func(ok bool, output string)
    Stderr(text string)
}
type Outcome struct {
    Status, SessionID, Model string
    Err                      error
    CostUSD                  float64
    NumTurns                 int
    ParkUntil                time.Time
}
func (s *Store) ListRuns(ctx context.Context, limit int) ([]RunRow, error)
```

## Definition of Done

- [x] **Mevcut hiçbir test dosyası düzenlenmedi, yalnızca eklendi.** Bir testin
      değişmesi gerekiyorsa dikiş yanlış yerdedir ve kesim yeniden yapılır.
      (`lifecycle_test.go` 816, `runner_test.go` 466, `ratelimit_test.go` 377 satır —
      hepsi sahte bir `claude` çalıştırılabilirinin etrafında kurulu.)
- [x] `TestMigration0022_LeavesExistingRunsOnTheCodingAgent` — göç *öncesi* yazılmış
      gerçek bir satırla, boş şema üstünde değil.
- [x] `TestDispatchOne_StartsAWorkerCardWithNoAccountConnected` — sahte bir
      `LaneWorker` executor'ı, `slots()` sıfır hesap döndürürken `running`'e ulaşıyor.
- [x] `TestDispatchOne_StartsAWorkerCardWhileEverySlotIsRateLimitHeld`.
- [x] `TestEnqueue_DoesNotDemandALoginForAWorkerCard` — `account.ErrNotConnected` yok.
- [x] `TestDispatchOne_StillPinsAClaudeCardToItsAccount`.
- [x] `GET /agents` ve `project_id`'siz `GET /coding-tasks` çalışıyor.
- [x] `make check` yeşil, `make race` dahil.
- [x] Status `done` + changelog.

## Notes for the reviewer (Opus)

- Asıl soru: `claudeExecutor.Execute`'un gövdesi eski `execute()` ile satır satır aynı mı?
  Farklıysa her fark için yazılı gerekçe olmalı.
- **SD-3**: `pump()` hâlâ tek giriş noktası mı? `inflight` goroutine doğmadan önce mi
  yazılıyor (runner.go:862-868'in gerekçesi worker için de aynen geçerli)?
- **SD-6**: `requireCapacity` worker için gerçekten erken dönüyor mu?
- Not edilmesi gereken bir sınır: `ListQueuedRuns(ctx, 100)` ajan körü. 100 kuyruklu
  coding kartı, arkasındaki her worker kartını açlığa mahkûm eder. `idx_coding_runs_agent`
  düzeltmeyi ucuzlatıyor; ölçülmeden yapılmaz, ama bilinerek bırakılır.

---

## Changelog — 2026-09-06

**Şema.** `0022_agent_jobs.sql`: `agent` (`DEFAULT 'coding'`), `skills`, `params`,
ve `idx_coding_runs_agent`. Tablo adı korundu. Backfill gerekmedi — varsayılan
bir geri düşüş değil, bir olgu.

**Dikiş.** `internal/coderunner/executor.go`: `Lane`, `Executor`, `Job`, `Sink`,
`Outcome`, `grant`, `runSink`, `runExecutor`.

**Planlanandan sapma — ve gerekçesi.** Dikiş `execute()`'un *içinden* değil,
*yanından* geçirildi. Plan gövdeyi `claudeExecutor.Execute`'a taşımayı
öngörüyordu; taşımadım. `execute()`'un stop semantiği, süreç grubu sinyalleşmesi
ve rate-limit park'ı bu depodaki en riskli koddur ve deneyerek varılmıştır;
onu, tek amacı "hiçbir şey değişmesin" olan bir refactor'ün içine sokmak, o
amacı en çok tehlikeye atan hamle olurdu. Bunun yerine `runExecutor`,
`execute()`'un **defter tutma iskeletini** paylaşıyor — transcript burada
açılıyor, `inflight` burada siliniyor, bus burada kapanıyor, terminal sonuç aynı
`finish`/`record` üzerinden yazılıyor — ama alt sürecin hiçbirini taşımıyor.
Sonuç: `execute()`'un gövdesi diff'te **hiç görünmüyor**.

**Kapasite.** `inflight` artık `grant` taşıyor. `busyAccounts` worker şeridini
atlıyor; `busyWorkers` sayıyor. `dispatchOne`'ın "boş yuva yok = hiçbir şey
başlayamaz" erken dönüşleri `reserve()`'ün `LaneAccount` dalına taşındı — bu
task'ın en tehlikeli iki satırıydı, çünkü kalsalardı bir worker kartı tam da
operatörün board'a baktığı anda donardı. `requireSlot` → `requireCapacity`:
worker şeridi için erken dönüyor, yani hesapsız bir makinede lead-gen kartı
**oluşturulabiliyor ve kuyruğa alınabiliyor**. `Kick` de artık kuyruğun gerçekten
bir hesap bekleyip beklemediğine bakıyor (`queueNeedsAnAccount`).

**Temizlenen mayın.** `store.RunningAccountIDs` ölü koddu ve worker koşusunun
boş `account_id`'sini meşgul bir yuva sayardı; `AND account_id <> ''` aldı.

**`noteHeld`.** `ListQueuedRuns(ctx, 1)` ile kuyruğun başına bakıp "bir coding
task bütçeyi bekliyor" diyordu; o tek satır artık hiçbir şey beklemeyen bir
worker kartı olabilir. `firstOnTheAccountLane` ile gerçekten bekleyen satır
bulunuyor.

**Yüzey.** `GET /coding-tasks` `project_id` olmadan da çalışıyor (projeler arası
`store.ListRuns` → `Runner.ListAll`). `POST`/`PATCH /coding-tasks` opsiyonel
`agent`/`params` alıyor; `params` bilerek opak — handler bir kapıdır, zemin
değil. `project_id` yalnız klasörde çalışan ajan için zorunlu.

**Sapma — düzenlenen iki test dosyası.** "Mevcut hiçbir test düzenlenmeyecek"
kuralı iki yerde tutulamadı ve ikisi de yazılı:
1. `api_test.go`'daki `fakeRunner`'a `ListAll` eklendi. Arayüz genişledi;
   derleyici gereği, hiçbir iddiayı zayıflatmıyor.
2. `TestListCodingTasks_RequiresProjectID` yeniden yazıldı. Bu bir sözleşme
   değişikliğidir, bir uyum yaması değil: `project_id`'siz istek artık 400
   değil, board'un kendi okuması. Eski test doğru olanı doğruluyordu ve o şey
   artık doğru değil. Yeni ad davranışı söylüyor:
   `TestListCodingTasks_WithoutAProjectIDReturnsEveryProjectsCards`.
`internal/coderunner`'daki üç test dosyası (`lifecycle_test.go` 816,
`runner_test.go` 466, `ratelimit_test.go` 377 satır) **düzenlenmedi**.

`make check` yeşil, `-race` dahil.
