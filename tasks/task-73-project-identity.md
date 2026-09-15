# task-73 — Proje kimliği: bir projeyi taşımak ve unutmak

- **Status:** done
- **Owner agent:** Claude Opus
- **Prerequisites:** task-41 (beyin çekirdeği), task-45 (capture), task-51 (yerleşik tarama)
- **Primary paths:** `internal/store/brainmove.go`, `internal/brain/move.go`,
  `internal/brain/capture.go`, `internal/brain/brain.go`,
  `internal/project/project.go`, `internal/api/brainprojects.go`,
  `internal/api/api.go`, `cmd/mimir-daemon/main.go`,
  `desktop/src/lib/daemon.ts`, `desktop/src/screens/Brain.tsx`
- **Roadmap bucket:** B.9 — Brain

## Context

`goat-remastered` beyinde ayrı bir proje olarak duruyordu. Depo
`mimir-studio/mimir-agent`'a taşınmış, eski yolda 0 baytlık bir kabuk kalmıştı
(içinde tek bir boş `deploy/`). Beyin onu bir proje saymaya devam ediyordu:

- Kayıtlı projeler tablosunda duruyordu ve capture döngüsü `projects.List`
  üzerinden çalışıyordu.
- `~/.claude/projects/-Users-...-goat-remastered/` altındaki transcript'ler
  dizinden bağımsız yaşıyor, capture onları 5 dakikada bir okuyup düğüm
  yazmaya devam ediyordu: **364 düğüm** (92 oturum · 246 dosya · 26 commit),
  **674 kenar**.
- Tarama tarafı bu dizini `discover.go`'daki `os.Stat` ile zaten atlıyordu;
  capture'da böyle bir kontrol yoktu.
- Ve asıl mesele: **sistemde hiçbir silme yolu yoktu.** Depoda
  `DELETE FROM brain_nodes` diye bir ifade yoktu, `Registry`'de bir `Remove`
  yoktu. Bir proje bir kez beyne girdiyse çıkamıyordu.

## Scope (do exactly this)

1. `internal/store/brainmove.go` (yeni)
   - `MoveBrainProject(ctx, from, to, identity)` — tek transaction. Düğüm
     kimliği `sha256(project_path|kind|source_key)` olduğu için taşımak
     **bütün kimlikleri yeniden hesaplamak** demek; eşleme transaction'ın
     *içinde* okunuyor, yoksa kararla uygulama arasında yazılan bir dosya eski
     yolda kalırdı. Kenarlar önce çıkarılıp sonra iki ucu çevrilmiş hâlde geri
     konuyor: birincil anahtar `(src, dst, kind)` ve iki uç da değişeceği için
     yerinde UPDATE kendi ulaşmadığı satırla çakışırdı.
   - Hedefte aynı kimlik zaten varsa **birleştirme**: hedef satır kalır (onu
     tarama güncel tutuyor), eski satırın sürüm geçmişi ona geçer.
   - `project_path` taşıyan öteki tablolar aynı transaction'da:
     `memory_episodes`, `memory_ingest_state`, `memory_notes`, `chat_sessions`,
     `chat_turns`. Capture cursor'ları taşınmaz, **silinir** — anahtarı
     `'<kind>:<project_path>'` ve yeniden okumak fikir sahibi olmayı gerektirmeyecek
     kadar idempotent.
   - `ForgetBrainProject(ctx, path)`, `DeleteProject`, `SetProjectPath`.
2. `internal/brain/move.go` (yeni) — `Core.MoveProject` / `Core.ForgetProject`.
   Hedef `project.Canonicalize`'dan geçer (taşımak ev dizinini adlandırmanın
   yolu değildir); **kaynak kasten kontrol edilmez**, çünkü bunun var olma
   sebebi artık orada olmayan bir dizin.
   Ayrıca `OnDisk(path)`: klasör var mı ve içinde bir şey var mı.
3. `internal/brain/capture.go` — `pass()` içinde `OnDisk` kontrolü. Bu tek
   başına diriltmeyi durduruyor: silmek yetmezdi, beş dakika sonra geri gelirdi.
4. `internal/project` — `Registry.Forget` ve `Registry.Repoint`.
5. `internal/api/brainprojects.go` (yeni) — `POST /brain/projects/{id}/move`,
   `DELETE /brain/projects/{id}`; id opak kalır (`resolveBrainProject`), yol
   asla id olarak kabul edilmez. Taşıma kayıt satırını da yeni yola çevirir.
   `GET /brain/projects` artık `on_disk` taşıyor.
6. Masaüstü — Yapılandırma sekmesinde proje listesi: düğüm sayısı, "klasör yok"
   rozeti, "taşı" (klasör seçici) ve "unut" (onaylı).

## Out of scope (do NOT do here)

- **Transcript silmek.** Unutmak beyindeki satırları siler;
  `~/.claude/projects/` altındaki dosyalar Claude Code'un.
- **Otomatik taşıma tahmini.** Taşınmış bir depoyu kendiliğinden tanıyıp
  birleştirmek cazip, ama yanlış birleştirme geri alınamaz; karar operatörün.
- **Tombstone / "silindi" bayrağı.** Her sorgunun süzmeyi hatırlaması gereken
  bir durum, silmekten daha kırılgan.

## Interfaces / contracts

```go
type NodeIdentity func(projectPath, kind, sourceKey string) string
func (s *Store) MoveBrainProject(ctx, from, to string, identity NodeIdentity) (MoveResult, error)
func (s *Store) ForgetBrainProject(ctx, path string) (int, error)

func (c *Core) MoveProject(ctx, from, to string) (store.MoveResult, error) // ErrCannotMove
func (c *Core) ForgetProject(ctx, path string) (int, error)
func OnDisk(path string) bool
```

```
POST   /brain/projects/{id}/move  { to } → { from, to, id, result{nodes,merged,edges,rows} }
DELETE /brain/projects/{id}             → { path, nodes_removed }
```

## Definition of Done

- [x] Bir projeyi taşımak geçmişini koruyor: düğümler yeni kimlikleriyle yeni
      yolun altında, **hiçbir kenar kaybolmadan**, hedefin zaten bildiği
      dosyalar birleştirilerek.
- [x] Episode'lar, chat oturumları ve ingest ofsetleri taşımayla birlikte
      geliyor.
- [x] Unutmak proje ve kenarlarını siliyor, komşu projeye dokunmuyor.
- [x] Klasörü olmayan proje capture edilmiyor — silinen bir proje geri gelmiyor.
- [x] Hedef, tarama kökünün geçtiği kapıdan geçiyor; kaynak geçmiyor.
- [x] `make check` ve `make desktop-check` yeşil.

## Notes for the reviewer (Opus)

- Kimlik kuralı `internal/brain`'de kalıyor; store onu bir fonksiyon olarak
  alıyor (`NodeIdentity`). İki tanım olmasındansa bir tane ve bir parametre.
- Capture cursor'larını taşımak yerine silmek bilinçli: hangisinin daha ileride
  olduğuna dair bir kurala ihtiyaç duymamak için. Bedeli bir turluk yeniden
  okuma, ve o okuma idempotent.
- `OnDisk` bir seviye derinliğe bakıyor: yalnızca boş klasörler tutan bir dizin
  taşımanın geride bıraktığı şeydir, ve `goat-remastered` tam olarak oydu.
