# task-75 — Graphify: her turda yeniden ayrıştırma, süzgeçsiz grafik, sessiz tavan

- **Status:** done
- **Owner agent:** Claude Opus
- **Prerequisites:** task-72 (Graphify entegrasyonu)
- **Primary paths:** `internal/brain/structural.go`, `internal/brain/supervisor.go`,
  `internal/brain/relate.go`, `internal/brain/brain.go`,
  `internal/store/brain.go`, `internal/api/brain.go`,
  `desktop/src/lib/daemon.ts`, `desktop/src/screens/Brain.tsx`,
  `docs/CAPABILITIES.md` · `.tr.md`
- **Roadmap bucket:** B.9 — Brain

## Context

task-72 çalışıyordu ama üç yerde eksikti, ve biri açılmadan önce kapatılması
gereken bir riskti.

1. **Değişiklik tespiti yoktu.** `supervisor.structural()` her turda her
   projenin bütün dosyalarını yeniden ayrıştırıyordu — bu depoda 316 dosya, 18
   projede, sonsuza kadar. Ayrıştırıcı deterministik: değişmemiş bir proje
   store'da zaten olan satırları üretiyor. Saf maliyet.
2. **Kesin kenarlar modele söylenmiyordu.** `relate.go` düğüm başına bir LLM
   çağrısı harcayıp FTS adayları arasından seçim yapıyordu — ayrıştırıcının
   zaten kesin bildiği komşuları görmeden.
3. **Semboller resmi süpürecekti.** Bu depo tek başına 3668 sembol üretiyor ve
   semboller grafikteki en bağlantılı şeyler; `/brain/graph` en bağlantılı 1500
   düğümü döndürdüğü için katman açılır açılmaz dosya grafiği ekrandan silinirdi.
4. Ve tavan sessizdi: 1228 sembol her turda düşüyordu, kimse görmüyordu.

## Scope (do exactly this)

1. `StructuralDigest(projectPath, rels)` — dosya yolu, boyut ve mtime üzerinden
   bir özet; `StructuralCursorKey` ile mevcut cursor deposuna yazılıyor ve
   değişmediyse ayrıştırıcı hiç çalışmıyor. İçerik hash'lenmiyor: okunacak şeyi
   hash'lemek kazancın çoğunu geri verirdi.
   Özet **store yazmayı kabul ettikten sonra** kaydediliyor.
2. `relate.go` — `structuralNeighbours` ile o düğümün yapısal komşuları
   okunuyor, prompt'a "bunlar zaten kesin, tekrarlama" olarak giriyor ve
   `withoutKnown` ile aday listesinden çıkıyor. Model çağrısı duruyor ama
   ayrıştırıcının göremediğine harcanıyor, üstelik aday listesi kısaldığı için
   ucuzluyor. `isStructuralEdge` bu pass'in kendi ürettiği `tag`/`semantic`
   kenarlarını dışarıda bırakıyor — yoksa kendi kararını bir daha gözden
   geçiremezdi.
3. `BrainGraphIDs` bir `kinds` süzgeci alıyor; `GET /brain/graph?kinds=` —
   boş **semantik türler** demek (bir modelin ürettiği her şey), `all` yapısal
   katmanı da ekliyor. Masaüstünde grafik başlığında "semboller" anahtarı,
   varsayılan kapalı.
4. `ScanStatus.symbols_dropped` ve ekranda "tavana takılan".
5. `docs/CAPABILITIES*.md` — Go'da metot çağrılarının zayıf çözüldüğü ölçümle
   birlikte yazıldı.

## Out of scope (do NOT do here)

- **Graphify'ın metot çözümünü düzeltmek.** Yukarı akışın ayrıştırıcısı.
- **Tavanı kaldırmak.** Görünür olması yeterli; kaç sembolün okunur bir grafik
  ürettiği ayrı bir karar.

## Interfaces / contracts

```go
func StructuralDigest(projectPath string, rels []string) string
func StructuralCursorKey(projectPath string) string
func SemanticKinds() []string
func IsKind(kind string) bool
func (s *Store) BrainGraphIDs(ctx, projectPath string, limit int, kinds []string) ([]BrainNodeDegree, error)
```

`GET /brain/graph?kinds=` — boş: semantik türler · `all`: hepsi ·
`symbol,file`: adı geçenler.

## Definition of Done

- [x] Dosyası değişmemiş bir proje için ayrıştırıcı hiç çalışmıyor.
- [x] Yapısal komşular relate prompt'una giriyor ve aday listesinden çıkıyor.
- [x] `/brain/graph` varsayılanı sembolsüz; anahtar açıldığında dolu.
- [x] Düşen sembol sayısı ekranda.
- [x] Ölçülen sınır belgelendi (2359 fonksiyona 3154 çağrı kenarı, 738 metoda 325).
- [x] `make check` ve `make desktop-check` yeşil.

## Notes for the reviewer (Opus)

- İlişki sözlüğü gerçek çıktıya bakılarak genişletildi: bu deponun 300
  dosyasında `references` (3215 kenar), `embeds`, `implements`,
  `dynamic_import` ve `indirect_call` da çıkıyor; hepsi genel `structural`a
  düşüyordu.
- `kinds` boşken "hepsi" değil "semantik türler" demesi bilinçli. Varsayılanın
  eskisiyle aynı resmi vermesi, katmanın *eklenmesi* ile *değiştirmesi*
  arasındaki fark.
