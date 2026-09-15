# task-89 — `Outcome.ParkUntil` dikişte okunur

- **Status:** done
- **Owner agent:** Coder
- **Prerequisites:** task-87 (katalog yeniden yazımı)
- **Primary paths:** `internal/coderunner/executor.go`,
  `internal/coderunner/ratelimit.go`, `internal/store/runs.go` (gerekirse)
- **Roadmap bucket:** B.10 — alt-ajanlar

## Context

`coderunner.Outcome` bir `ParkUntil` alanı ilan ediyor ve alanın kendi yorumu
onu şöyle anlatıyor: *"Only an executor that spends a token budget ever sets it;
leaving it zero is what 'this agent has nothing to run out of' means, and it is
why the park path cannot be reached from the worker lane at all."*

task-87 bu cümlenin ikinci yarısının artık doğru olmadığını gösterdi. Katalog
kartı worker lane'de koşuyor — bir claude kimlik yuvası tutmuyor, doğru — ama
`internal/llm` üzerinden daemon'ın **kendi** model bütçesini harcıyor ve o bütçe
tükenebiliyor. Aynı şey lead-gen'in `Reason` sınıfı çağrıları için de geçerli;
orada fark edilmemesinin sebebi Distill tier'ının ücretsiz olması.

Bugünkü durum: `runExecutor` `out.ParkUntil`'i **hiç okumuyor**. Bir executor onu
doldursa sessizce yok sayılıyor. task-87 bu yüzden park etmiyor; yerine kartı,
kaç ürünün korunduğunu ve yeniden çalıştırmanın yalnız kalanları harcayacağını
söyleyerek düşürüyor. Bu dürüst ve ürün başına önbellek sayesinde ucuz, ama
otomatik değil: pencere dönünce kartı elle yeniden çalıştırmak gerekiyor.

## Scope (do exactly this)

1. **`runExecutor` terminal `Outcome`'da `ParkUntil`'i okur.** Sıfır olmayan bir
   değer `finish` yerine `park` yoluna gider: satır `running → queued`, sebep
   satırda, `events.KindRateLimit` yayınlanır.
2. **Worker lane'de limitin neye anahtarlandığına karar verilir.** Bugünkü
   `holdSlot`/`heldUntil` bir `accountID`'ye anahtarlı ve worker işinde o yok.
   İki seçenek var ve task bunu bir kere seçip yazmalı: (a) daemon'ın kendi
   kimliği için tek bir sentetik yuva, (b) lane'in kendisi için ayrı bir hold.
   Hangisi seçilirse `restoreHolds` yeniden başlatmada onu da kurmalı.
3. **`store.ParkRun` worker satırı için doğru davranır.** `session_id` boş
   olacak; park'ın onu koruması bir claude oturumu içindi ve burada koruyacak
   bir şey yok.
4. **`internal/catalogjob` `ParkUntil` doldurur.** `llm.ErrRateLimited`'ın
   penceresini `internal/llm`'den okuyabiliyorsa onu, okuyamıyorsa
   `coderunner`'ın `resetFromMessage`'ını kullanır. Kartın mesajı korunur —
   park otomatiği ekler, açıklamayı kaldırmaz.

## Out of scope (do NOT do here)

- `execute()`'un claude yolu. Dikiş onun *yanında* kesildi ve öyle kalır.
- Katalog motorunda hiçbir şey: `internal/catalog` bu task'ta değişmez.
- Masaüstünde park rozeti — task-88.

## Definition of Done

- [x] `TestRunExecutor_ParksAJobThatAskedToBeParked` — satır `queued`, sebep
      satırda, duraklama `WorkerSlot` altında ve `Limits()` onu bildiriyor.
- [x] `TestRunExecutor_StillFailsAJobThatDidNotAskForOne` — davranış değişmedi.
- [x] `TestRunExecutor_DoesNotParkOnATimeThatHasPassed` — geçmiş bir zamana
      park edilmiyor.
- [x] `TestDispatch_HoldsTheWorkerLaneWhileItsBudgetIsSpent`.
- [x] `TestResume_RestoresTheWorkerLaneHold` — yeniden başlatmadan sonra.
- [x] `TestResetFromMessage_ReadsTheProvidersOwnWindow`.
- [x] `internal/catalogjob`: limit park istiyor, sağlayıcının kendi zamanıyla;
      zaman yoksa geri düşüş aralığı; başka hiçbir hata park istemiyor.
- [x] `leadgenjob` davranışı değişmedi (hâlâ `ParkUntil` doldurmuyor).
- [x] `make check` yeşil.

## Notes for the reviewer (Opus)

- `execute()`'un claude yolundaki park davranışı bit bit aynı mı? Bu task'ın
  hiçbir şeyi değiştirmediği yer orası.
- Worker lane'in hold'u yeniden başlatmada geri kuruluyor mu, yoksa daemon
  yeniden başlayınca limit unutuluyor mu?
- `Outcome.ParkUntil`'in yorumu güncellendi mi? Bugün "worker lane'den
  erişilemez" diyor ve bu task onu yanlış hâle getiriyor.

---

## Changelog — 2026-09-07

**Dikiş artık `ParkUntil`'i okuyor.** `runExecutor` terminal `Outcome`'da sıfır
olmayan ve geçmemiş bir `ParkUntil` görünce `finish` yerine `park` yoluna
gidiyor: satır `queued`'a dönüyor, sebep satırda kalıyor, `events.KindRateLimit`
yayınlanıyor ve pencerenin kendi uyandırması kuyruğu yeniden başlatıyor.

**Worker lane'in limiti neye anahtarlandı: `WorkerSlot`.** Hesap lane'i kimlik
yuvası başına bir duraklama tutuyor, çünkü tükenen şey o. Worker lane'deki bir iş
öyle bir yuva tutmuyor — ama bütçesiz de değil: `internal/llm` üzerinden
daemon'ın **kendi** model kimliğini harcıyor ve süreçte o kimlikten bir tane var.
Dolayısıyla bütün worker işleri için tek bir duraklama, tek bir anahtar altında.
`slotFor` anahtarı satırdan türetiyor, ki hesap lane'inin davranışı bit bit
eskisi kalsın.

**`Outcome.ParkUntil`'in yorumu düzeltildi.** "park yolu worker lane'den hiç
erişilemez" diyordu; bir worker işi model bütçesi harcamaya başladığı anda bu
doğru olmaktan çıkmıştı. Yorum artık ayrımı doğru yerden çekiyor: kimlik yuvası
tutmak ile bütçe harcamak aynı şey değil.

**`ResetFromMessage` dışa açıldı.** Bir executor, claude yolunun cevapladığı
soruyu aynı `…|<unix>` ekinden cevaplamak zorunda; bir CLI'ın tek bir cümlesinin
iki ayrıştırıcısı olmasının sebebi yok.

**Katalog kartı park ediyor.** `llm.ErrRateLimited`'da `Outcome.ParkUntil`
sağlayıcının kendi zamanıyla, o yoksa `CodingLimitRecheck` ile doluyor. Kartın
cümlesi de düzeldi: artık "yeniden çalıştırın" değil "kendiliğinden sürüyor"
diyor, çünkü öyle. Başka hiçbir hata park istemiyor — operatörün gidip bakması
gereken bir kart, kendi kendine sürecekmiş gibi görünmemeli.

**Silinen:** `firstOnTheAccountLane`. Yerine `firstWaitingOn(slot)` geçti;
`noteHeld` artık her duraklamayı gerçekten beklettiği lane'in ilk kartıyla
eşleştiriyor, ikisini birbirine karıştırmadan.

## Uçtan uca doğrulama (gerçek daemon)

Duraklama `rate_limit_log`'a elle yazıldı, daemon yeniden başlatıldı:

1. `restoreHolds` duraklamayı `worker-lane` altında geri kurdu ve
   `GET /coding-tasks/queue/limits` onu bildirdi.
2. Duraklama yürürlükteyken kuyruğa alınan katalog kartı üç yoklama boyunca
   `queued` kaldı ve **CLI hiç çağrılmadı** (daemon logunda sıfır eşleşme) —
   aranan tasarruf tam olarak bu.
3. Pencere dönünce log "the token budget has reset; the coding queue is
   restarting" yazdı, duraklama düştü ve kart **kendiliğinden** koştu.

(Koşu sonunda `failed`, çünkü bu makinede `claude` oturumu kapalı; park
mekanizmasının ölçtüğü şey o değil.)

`make check` yeşil.
