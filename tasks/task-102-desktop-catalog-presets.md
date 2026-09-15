# task-102 — Masaüstü: platform preset seçici ve profil listesi

- **Status:** done
- **Owner agent:** Coder
- **Prerequisites:** task-101
- **Primary paths:** `desktop/src/lib/daemon.ts`, `desktop/src/lib/catalog.ts`, `desktop/src/screens/Catalog.tsx`
- **Roadmap bucket:** Katalog — ürün içeriği stüdyosu

## Context

`lib/catalog.ts` profil adlarını kendi `DIALECT_LABELS` sabitinde tutuyordu.
Go'da yaşayan kapalı bir kümenin ikinci bir kopyası, bir süre sonra iki farklı
kapalı küme olur — ve oldu: task-91'de silinen `ikas-en` ekranda kaldı.

Ayrıca profil **rozet**ti: operatör dosyasının bir IKAS export'u olduğunu
görebiliyor ama algılama göremiyorsa tek çıkış yolu elle sütun formuydu.

## Scope (do exactly this)

1. `daemon.ts`: `CatalogProfile`, `api.catalogProfiles()`, `api.setCatalogDialect()`.
2. `catalog.ts`: `dialectLabel(dialect, profiles)` — ad yalnızca daemon'ın az önce
   verdiği bir addır; tanımadığı anahtar uydurulmaz, anahtar olarak gösterilir.
3. `Catalog.tsx`: `ImportBar`'daki rozet yerine `Select`. Boş seçenek "yok"
   değil, **otomatik algılama** — yanlış seçim yeniden yükleme olmadan geri alınır.

## Definition of Done

- [x] `DIALECT_LABELS` silindi; profil listesi daemon'dan geliyor
- [x] Profil listesi alınamazsa ekran çalışmaya devam ediyor (anahtar gösterilir)
- [x] `npm run typecheck && npm test` yeşil
