# task-74 — Chat arşivi: her proje, ve birikmiş borç

- **Status:** done
- **Owner agent:** Claude Opus
- **Prerequisites:** task-65 (chat arşivi), task-73 (proje kimliği)
- **Primary paths:** `cmd/mimir-mcp/main.go`, `cmd/mimir-daemon/main.go`,
  `internal/memory/backfill.go`, `internal/memory/memory.go`,
  `internal/store/memory.go`
- **Roadmap bucket:** B.8 — proje hafızası

## Context

Diskte 284 transcript vardı; arşivde 78 oturum ve 172 tur. Üstünde çalışılan
depo (`mimir-agent`) için **11 episode, 0 arşivlenmiş tur**. Üç bağımsız sebep:

| Sebep | Kanıt |
|---|---|
| `mimir-mcp` arşivi hiç kurmuyordu | `memory.New(...)` vardı, `UseArchive` yoktu. Gerekçe "kısa ömürlü süreç arşivlemez" idi; `PutChatTurn` tek bir insert, model çağrısı yok. Oturumun *içinde* olduğu proje tam da bu yüzden ham metinsiz kalıyordu. |
| Daemon yalnızca **kayıtlı** projeleri işliyordu | Hem memory hem capture döngüsü `projects.List` üzerindeydi: 9 kayıtlı proje, taramanın bulduğu 18 proje. |
| task-65 öncesi episode'lar geri doldurulmamıştı | UretimStudio 222 episode / 73 tur; goat-remastered 122 / 60; stralgo 39 / 39 (arşivden sonra tarandığı için tam). |

## Scope (do exactly this)

1. `cmd/mimir-mcp/main.go` — `mem.UseArchive(pageStore)`.
2. `cmd/mimir-daemon/main.go` — `knownProjects`: kayıtlı projelerle
   `brain.DiscoverProjects`'in tarama kökleri altında bulduklarının birleşimi,
   yolla tekilleştirilmiş. Kayıt hâlâ id'leri veriyor, çünkü coding run'ı olan
   tek proje türü o. Hem memory hem capture aynı listeyi kullanıyor.
3. `internal/memory/backfill.go` — **recap bütçesi proje başına değil tur
   başına.** 9 projeden 18'e çıkmak, proje başına bütçeyle model harcamasını
   ikiye katlardı; makinede tek bir distil sağlayıcı var ve operatör de onu
   kullanıyor. Transcript okumak ve arşivlemek sınırsız kalıyor — ikisi de
   bedava — yani listenin sonundaki projeler her turda *kaydediliyor*, sırasını
   bekledikleri şey özet.
4. Geri doldurma — `SourcesMissingArchive` + `RewindIngest`, ve her turun
   başında `catchUpArchive`. Model çağrısı yok: episode upsert'i `title`,
   `summary`, `recap_attempts` ve `prompt_version` alanlarına dokunmuyor, yani
   zaten özetlenmiş bir episode'u yeniden okumak bedava.

## Out of scope (do NOT do here)

- **Yeni bir boru hattı.** Transcript'i yeniden okumak ingest'in normal işi;
  geri doldurmanın tek yaptığı hangi dosyanın yeniden okunacağını söylemek.
- **Arşivi model'e sokmak.** Chat arşivi hiçbir modelin okumadığı bir kayıt;
  bu onun sözleşmesi.

## Interfaces / contracts

```go
func (s *Store) SourcesMissingArchive(ctx context.Context, limit int) ([]string, error)
func (s *Store) RewindIngest(ctx context.Context, sourcePath string) error
func (m *Memory) catchUpArchive(ctx context.Context, log *slog.Logger)
```

## Definition of Done

- [x] `mimir-mcp` çalıştığı projenin sohbetlerini arşivliyor.
- [x] Daemon, taramanın bulduğu her projeyi hafızaya ve capture'a alıyor.
- [x] Recap bütçesi tur başına; kapsam iki katına çıkarken model harcaması
      çıkmıyor.
- [x] Arşivsiz kalmış transcript'ler yeniden okunuyor ve **iş bitiyor**.
- [x] `make check` yeşil.

## Notes for the reviewer (Opus)

- Geri doldurmanın sonlanma özelliği bir testle sabitlendi ve ilk yazdığım
  ölçüt yanlıştı: `PutChatTurn` ne prompt'u ne cevabı olan bir episode için
  hiçbir şey yazmıyor (araç-only bir alışveriş), yani "turu olmayan episode"
  ölçütü **asla yakınsamıyordu** — aynı dosya her turda yeniden sarılırdı.
  Ölçüt kaynak başına: "bu transcript arşive hiçbir şey katmadı". Üstüne bir de
  çalışma başına bir kez sarma güvencesi.
- `rewound` süreç içinde tutuluyor, saklanmıyor: transcript'i yeniden okumak
  model çağrısı harcamıyor, yani yeniden başlatmadan sonra bir kez daha ödemek
  bir şema açmaktan ucuz.
