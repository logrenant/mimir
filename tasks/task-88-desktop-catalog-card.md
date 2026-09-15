# task-88 — Desktop: katalog kartının gövdesi ve park göstergesi

- **Status:** done
- **Owner agent:** Coder
- **Prerequisites:** task-86 (Katalog ekranı), task-87 (yeniden yazım executor'ı), task-89 (dikişte park)
- **Primary paths:** `desktop/src/screens/Dashboard.tsx`,
  `desktop/src/lib/board.ts` + testleri, `desktop/src/screens/Catalog.tsx`
- **Roadmap bucket:** B.11 — katalog / ürün içeriği

## Context

task-87 katalog kartını kuyruğa soktu; panoda bugün başlığından başka bir şey
göstermiyor. Lead-gen'in task-82'de aldığı gövdeyi katalog da alır: kart neyin
üzerinde çalıştığını (hangi import, kaç ürün) ve nerede durduğunu söyler.

Bir fark var ve asıl sebep o: katalog kartı model limitine **park edebilen ilk
kart**. Park edilmiş bir kart `queued` görünür, ki bu "hiç başlamadı"dan ayırt
edilemez. Operatörün gece boyunca ne olduğunu görebilmesi için parkın kendi
rozeti olmalı — `GET /coding-tasks/queue/limits` verisini kart üzerinde okuyan.

## Scope (do exactly this)

1. **Kart gövdesi** — `agent === "catalog"` için: import adı, ürün sayısı,
   yazılacak alanlar, ve ilerleme (`taslak yazılan / toplam`). `params`'tan
   okunur, ayrı bir istek açılmaz.
2. **Park rozeti** — `LimitReport.holds` içinde bu kartın hesabı varsa kart
   "limit · <saat>'te sürüyor" der. `queued` ile karıştırılamaz. `lib/board.ts`'de
   saf bir fonksiyon + testi.
3. **Sonuca açılan kapı** — bitmiş bir katalog kartından "ürünleri gör" Katalog
   ekranını o import'ta açar. Lead-gen kartının task-82'de aldığı kapının aynısı.
4. **Ürün başına satır akışı** — kartın terminali `research`/`draft` adımlarını
   zaten çiziyor (`formatEventLine`); önbellek isabetinin isabet olarak
   göründüğü doğrulanır, gerekirse `terminals.ts`'de tek satırlık bir etiket.

## Out of scope (do NOT do here)

- `internal/**`, `cmd/**`.
- Katalog ekranının kendisi — task-86.
- Yeni bir pano sütunu ya da yeni bir durum kelimesi. Durum sözlüğü
  `internal/store/runs.go`'nun.

## Definition of Done

- [x] `lib/board.test.ts`: park edilmiş bir kart `queued`'dan ayrı okunuyor;
      hold'u olmayan `queued` kart rozet almıyor; bir lane diğerinin limitiyle
      tutulmuş gösterilmiyor; adsız bir hold hiçbir kartla eşleşmiyor.
- [x] `catalogParams` / `catalogSummary` — kart gövdesi `params`'tan, çözülemeyen
      bir karttan hiçbir şey çizmeden.
- [x] Kart gövdesi ek bir HTTP isteği açmıyor; `queue/limits` paylaşılan poll'a
      binmiş (`RunsProvider`), ayrı bir döngü yok.
- [x] "Ürünleri gör" doğru import'u açıyor ve yalnız bitmiş bir katalog kartında
      görünüyor.
- [x] Katalog ekranındaki "seçilenleri yeniden yaz" düğmesi bağlandı — task-87
      rotayı getirdiği için artık devre dışı bırakılacak bir sebep yok.
- [x] `make desktop-check` yeşil.

## Notes for the reviewer (Opus)

- Park rozeti gerçek `LimitReport` verisinden mi geliyor, yoksa `queued` +
  `error` metnini okuyan bir tahminden mi?
- Kart gövdesi `params`'tan mı okuyor, yoksa ürün başına bir istek mi açıyor?
- Durum sözlüğüne yeni bir kelime eklenmiş mi? Eklenmişse `CHANGES REQUIRED`.

---

## Changelog — 2026-09-07

**Kart gövdesi ve park rozeti.** `catalogSummary` kaç ürün, araştırma açık mı ve
kaç alan yazılacağını `params`'tan okuyor — hiçbir ek istek açmadan (task-80'in
1+N kuralı). `heldLabel` park edilmiş bir kartı `queued`'dan ayırıyor ve işin ne
zaman süreceğini söylüyor, çünkü "neden hiçbir şey çalışmıyor"un tek yararlı
cevabı o.

**Rozetin verisi daemon'ın kendi hold'larından, kartın hata metninden değil.**
`error` bir insan için yazılmış düzyazı; onu ayrıştırmak bir veri kaynağı değil
bir tahmin olurdu. `GET /coding-tasks/queue/limits` `RunsProvider`'ın aynı
poll'una bindi — ayrı bir döngü, bir duraklamanın ne zaman kalktığı konusunda
birinciyle çelişebilecek ikinci bir cevap olurdu. Üçüncü istek kendi başına
düşebiliyor: "neden hiçbir şey çalışmıyor"u cevaplayamayan bir daemon panoyu
yine de çizebilmeli.

**İki lane ayrı tutuluyor.** Bir worker kartı (katalog, lead-gen) daemon'ın kendi
model kimliğini bekliyor (`worker-lane`); bir hesap kartı kendi kimlik yuvasını.
Birini diğerinin limitiyle tutulmuş göstermek, operatörü kendisiyle ilgisi
olmayan bir limite bakmaya gönderirdi.

**Sonuca açılan kapı.** Bitmiş bir katalog kartında "ürünleri gör" Katalog
ekranını o import'ta açıyor. Yalnız `completed` kartta sunuluyor: kuyruktaki bir
kartta içinde taslak olmayan bir import'u açardı.

**Ve task-86'nın bıraktığı boşluk kapandı.** Ürün tablosundaki "seçilenleri
yeniden yaz" düğmesi artık gerçek: task-87 rotayı getirdi, seçim açık bir id
listesi olarak gidiyor ve iş panoda bir kart oluyor.

`make desktop-check` yeşil.
