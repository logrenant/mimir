# task-80 — Desktop: board her kartı okur, tek istekle

- **Status:** done
- **Owner agent:** Coder
- **Prerequisites:** task-79 (executor dikişi, `project_id`'siz `GET /coding-tasks`)
- **Primary paths:** `desktop/src/components/RunsProvider.tsx`,
  `desktop/src/lib/{daemon.ts,board.ts}`, `desktop/src/screens/Dashboard.tsx`
- **Roadmap bucket:** B.10 — alt-ajanlar

## Context

`RunsProvider.refresh` (RunsProvider.tsx:66-89) bugün 1+N istek atıp istemci tarafında
birleştiriyor, ve yorumu (17-21) sebebini yazıyor: daemon'ın projeler arası bir rotası
yoktu. task-79 o rotayı verdi. Bu task fan-out'u siler ve o yorumu, artık doğru
olmadığı için, kaldırır.

Board'un durum kelime dağarcığı **değişmiyor** — seçilen şeklin bütün kazancı bu.
`BOARD_COLUMNS`, `columnOf`, `groupRuns`, `allowedMove`, `canEdit`, `isStalled`
dokunulmadan kalır.

## Scope (do exactly this)

1. `RunsProvider.refresh`: tek `GET /coding-tasks`. `api.listProjects()` yalnız
   etiketteki proje adını çözmek için kalır; projesi olmayan kart o boşlukta ajan
   rozetini gösterir.
2. `board.ts`: `actionsFor(status)` → `actionsFor(status, agent)`. Hesapsız bir
   ajanın `--resume`'u yoktur, yani "devam et" sunulmaz; `canContinue(run)` zaten
   `session_id` yokken false döndürdüğü için bu bir sertleştirme, davranış değişikliği
   değil.
3. `Dashboard.tsx` `RunCard`: bugün model çipinin olduğu yerde ajan çipi ve skill
   çipleri.
4. `daemon.ts`: `Run` tipi `agent`, `skills`, `params`; `project_id` boş olabilir.

## Out of scope (do NOT do here)
- Lead-gen kartının gövdesi ve "Sonuçları aç" — task-82.
- `BOARD_COLUMNS` / sürükleme kurallarının değişmesi. Değişmesi gerekiyorsa
  task-79 durum kelime dağarcığını kırmış demektir — bunu söyle, burada tamir etme.

## Definition of Done
- [x] Board tek istekle doluyor; `RunsProvider`'daki fan-out ve onu açıklayan yorum yok.
- [x] Projesi olmayan bir kart doğru çiziliyor.
- [x] `board.test.ts`: `actionsFor` hesapsız ajanda "devam et" sunmuyor.
- [x] `make desktop-check` yeşil.
- [x] Status `done` + changelog.

## Notes for the reviewer (Opus)
- `BOARD_COLUMNS` ve sürükleme kuralları gerçekten değişmedi mi?

---

## Changelog — 2026-09-06

**`RunsProvider`** — 1+N fan-out silindi. Artık tek `GET /coding-tasks` ve
`GET /projects` **paralel**: kartlar cevabın kendisi, projeler yalnız etiket
için bir arama tablosu, ikisi birbirini beklemiyor. Fan-out'u açıklayan yorum
da kaldırıldı, çünkü artık doğru değil — ve yerine neden tek isteğin *zorunlu*
olduğu yazıldı: worker şeridindeki bir kart hiçbir projeye ait değil, yani
proje başına sorgu onu asla bulamazdı.

**`board.ts`** — `actionsFor(status, agent?)`. Ajan tek bir şey için önemli:
"devam et". Devam etmek `claude --resume`'a bir oturum id'si vermek demek ve
CLI oturumu harcamayan bir alt-ajanın öyle bir şeyi yok — düğmeyi sunmak,
daemon'ın yapamayacağı bir şeyi vaat etmek olurdu. Geri kalan her aksiyon
yalnız duruma bağlı. Ajanı olmayan kart coding kartıdır, çünkü alt-ajanlardan
önce yazılmış her kart oydu. İkinci parametre opsiyonel, yani `board.test.ts`'in
32 mevcut çağrısı düzenlenmeden geçiyor.

**`Dashboard`** — `AgentChip`: her kartta ajan ve bağlı olduğu skill'ler.
Her kartta, yalnız sıra dışı olanlarda değil: "coding" artık bir seçim, ve
sadece şaşırtıcı cevaplarda beliren bir çip, operatöre yokluğunu "hiçbir şey"
diye okumayı öğretir. Projesi olmayan kart o alanı boş bırakmıyor — boş bir
span, adı yüklenememiş bir proje gibi okunurdu.

Yeni testler (`board.test.ts`, +6): hesapsız ajanda "devam et" yok, "baştan
dene" var, diğer aksiyonlar değişmiyor, ajansız kart coding sayılıyor.

`BOARD_COLUMNS`, `columnOf`, `groupRuns`, `allowedMove`, `canEdit`, `isStalled`
**dokunulmadı** — seçilen şeklin bütün kazancı buydu.

`make desktop-check` yeşil, 287 test.
