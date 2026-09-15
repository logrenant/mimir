# task-83 — Graphify'dan alınmayan verim: 102 uzantı, yönlü okuma, beş traversal

- **Status:** done
- **Owner agent:** Coder
- **Prerequisites:** task-72, task-75 (graphify), task-77 (`graph-query` skill'i)
- **Primary paths:** `internal/graphify/graphify.go`,
  `internal/brain/traverse.go` (yeni), `internal/store/{brain.go,migrations/0023_graph_memory.sql}`,
  `internal/tools/{graph.go,register.go}`, `internal/api/brain.go`
- **Roadmap bucket:** B.9 — Brain

## Context

Graphify'dan bugün tek bir şey alınıyor: `python -m graphify.extract`. Kurulu sürüm
0.9.54 ve iki ölçülebilir boşluk var.

**Birincisi bir tavan.** `internal/graphify.Parses()` **18** uzantıya izin veriyor;
`graphify.detect.CODE_EXTENSIONS` **102** uzantı ayrıştırıyor. `.svelte .vue .swift
.lua .sh .sql .tf .dart .ex .jl .ps1 .gradle .hcl .astro .zig` ve fazlası sessizce
atlanıyor.

**İkincisi bir kayıp.** `store.BrainNeighbors` kenarın yönünü **siliyor**
(`brain.go:384-386`: `if e.Dst == id { e.Src, e.Dst = e.Dst, e.Src }`). Oysa kenarlar
yönlü yazılıyor (`structural.go:135`, Src = çağıran). "Bu fonksiyonu kim çağırıyor"
sorusunun verisi store'da duruyor ama okunamıyor.

Graphify'ın okuma tarafını Python'a shell out ederek değil, Go'da Brain'in kendi
kenarları üzerine yazıyoruz — task-72'nin çizdiği sınırın aynısı: ikinci bir grafik
dosyası ve ikinci bir beyin, kullanıcıya iki ayrı sistem gibi görünür. Alınan şey
yetenek; kopyalanan şey değil.

## Scope (do exactly this)

1. **Uzantı tavanı 18 → 102.** Liste Go'da **sabittir** (SD-1/SD-5 — kurulu paketin
   sürümünden türetilirse davranış sessizce değişir). `Detect()` sırasında kurulu
   paketin kendi listesiyle karşılaştırılır ve fark `slog`'a yazılır: sürüm
   ilerlediğinde sessizce geri kalmamak için.
2. **Yönü koruyan okumalar:**
   ```go
   func (s *Store) BrainInboundEdges(ctx, dst string, kinds []string, limit int) ([]BrainEdgeRow, error)
   func (s *Store) BrainOutboundEdges(ctx, src string, kinds []string, limit int) ([]BrainEdgeRow, error)
   ```
   `BrainNeighbors` **değişmez** — grafik görünümü yönü umursamıyor ve onu değiştirmek
   ekranı bozar.
3. **`internal/brain/traverse.go`** — beş okuma, hepsi model çağrısız:

   | Graphify | Mimir | Dayanağı |
   |---|---|---|
   | `query` | `Query(ctx, projectPath, q, budget)` | `SearchBrainNodes` (FTS) + BFS + mevcut `fitToBudget` |
   | `path` | `Path(ctx, a, b)` | çift yönlü BFS |
   | `explain` | `Explain(ctx, id)` | `Related()` genişletmesi |
   | `affected` | `Affected(ctx, id, kinds, depth)` | `BrainInboundEdges`, ters gezinme |
   | `god-nodes` | `Hubs(ctx, projectPath, top)` | `BrainGraphIDs` zaten derece sıralı |

   `Query`, graphify'ın `query.md` referansındaki **kısıtlı genişletme** disiplinini
   taşır: aday token'lar grafiğin kendi düğüm başlıklarından çıkarılır, listede olmayan
   token uydurulmaz, hiç eşleşme yoksa boş sonuç ve "bu grafikte bu konuda kelime yok"
   der ve durur. Seçilen token'lar sonuçta döner — denetlenebilirlik.
4. **Geri besleme** (`0023_graph_memory.sql`): `graph_results` — soru, anılan düğümler,
   sonuç (`useful` | `dead_end` | `corrected`), zaman. `RecordOutcome` ve
   `Lessons(ctx, projectPath)`: `reflect`'in deterministik yarısı — yarı ömürle
   ağırlıklandırılmış, en az iki bağımsız `useful` görmüş düğümler.
5. **Dört MCP aracı** (`internal/tools/graph.go`): `graph_query`, `graph_path`,
   `graph_affected`, `graph_hubs`. Dördü de `mcp.SkilledTool`'u karşılar ve
   `graph-query` skill'ini bildirir — task-77'nin kapısından geçer. Yanıtlar
   `MetadataOnly`: sembol adı, dosya, satır, kenar türü — sayfa metni değil.
6. **HTTP**: `POST /brain/query`, `POST /brain/affected`, `GET /brain/hubs`.

## Out of scope (do NOT do here)

- Graphify'ın kendi MCP sunucusu (`--mcp`) — task-72'de kapsam dışı, öyle kalıyor.
- `graph.html`, Obsidian, `wiki`, `tree` çıktıları.
- Topluluk kümeleme ve etiketleme (`cluster-only`, `label`) — model harcar, kendi task'ı.
- `--postgres`, `--cargo`, `--global`.
- Desktop — task-84.

## Interfaces / contracts

```go
type Hit struct {
    NodeID, Title, Kind, File, Location string
    Why  string // hangi kenar üzerinden ulaşıldı
    Hops int
}
type QueryResult struct {
    Expanded []string
    Hits     []Hit
    Note     string
}
func (c *Core) Query(ctx context.Context, projectPath, q string, budget int) (QueryResult, error)
func (c *Core) Path(ctx context.Context, a, b string) ([]Hit, error)
func (c *Core) Explain(ctx context.Context, id string) (QueryResult, error)
func (c *Core) Affected(ctx context.Context, id string, kinds []string, depth int) ([]Hit, error)
func (c *Core) Hubs(ctx context.Context, projectPath string, top int) ([]Hit, error)
```

## Definition of Done

- [x] `TestParses_CoversGraphifyCodeExtensions` — `.svelte .tf .lua .swift` dahil 102.
- [x] `TestDetect_LogsWhenTheInstalledPackageKnowsExtensionsWeDoNot`.
- [x] `TestBrainInboundEdges_KeepsTheDirectionNeighborsThrowsAway`.
- [x] `TestAffected_FindsTheCallersOfASymbol` — `BrainNeighbors` ile cevaplanamayan soru.
- [x] `TestQuery_ExpandsOnlyIntoVocabularyTheGraphActuallyHas`.
- [x] `TestQuery_SaysSoInsteadOfGuessingWhenNothingMatches`.
- [x] `TestPath_FindsTheShortestChainBetweenTwoSymbols`.
- [x] `TestLessons_PrefersNodesCorroboratedTwiceAndDecaysOldSignal`.
- [x] `TestBrainNeighbors_StillNormalisesDirection` — eski davranış korunuyor.
- [x] `make check` yeşil.
- [x] Status `done` + changelog.

## Notes for the reviewer (Opus)

- **SD-2/SD-7**: `graph_*` sayfa metni taşımıyor; tavanlar testle aşılıyor mu?
- **SD-3**: traversal'lar derinlik ve düğüm sayısıyla sınırlı mı? Bu depo tek başına
  3668 sembol üretiyor.
- **SD-5**: uzantı listesi sabit mi, yoksa kurulu paketten mi türetiliyor?

---

## Changelog — 2026-09-06

### Task'ın kendi öncülü yanlıştı — ve asıl bulgu bu

Bu dosya (ve onu doğuran plan) şöyle diyordu: *"kenarlar yönlü yazılıyor
(`structural.go:135`) ama `BrainNeighbors` okurken yönü siliyor, yani veri
store'da duruyor, sadece okunamıyor."*

Yarısı doğruydu. `structural.go` gerçekten yönlü kenar üretiyor. Ama
`store.UpsertBrainEdges` **yazmadan önce** src ve dst'yi sözlük sırasına
sokuyordu — "aynı çift iki uçtan da bulunsa tek satır olsun" diye. Yani yön
okumada değil, **yazmada** kayboluyordu. Veri store'da yoktu.

Bu, sessiz olmasıyla kötü bir hatadır: kenar duruyordu, doğru iki düğümü
bağlıyordu, grafik doğru görünüyordu. Yalnızca "bu fonksiyonu kim çağırıyor"
sorusu cevapsızdı ve hiçbir şey bunu söylemiyordu.

**Düzeltme.** `DirectedEdgeKind` — ayrıştırıcının kenarları (`calls`, `imports`,
`defines`, `inherits`, `implements`, `references`, `uses`, `structural`) yönünü
koruyor; simetrik olanlar (`semantic`, `tag`) eski davranışı sürdürüyor, çünkü
onlar için çift zaten iddianın tamamı. `0023_directed_structural_edges.sql`
mevcut yapısal kenarları siliyor ve `structural:` cursor'larını temizliyor:
yapısal kenarlar türetilmiş veridir, bedava ve deterministik olarak yeniden
üretilir, ve cursor temizlenmezse task-75'in "değişmemiş projeyi atla"
optimizasyonu onları asla yeniden yazmazdı.

`TestUpsertBrainEdges_KeepsTheParsersDirectionAndSortsTheRest` bilerek
sözlük sırasına aykırı id'ler kullanıyor (`zzz-caller` → `aaa-callee`), yani
eski davranış geri gelirse burada patlıyor.

### Uzantı tavanı 18 → 97

`parsedExtensions`, `graphify.detect.CODE_EXTENSIONS`'ı yansıtıyor. Upstream
102 girdi listeliyor; harf duyarlılığı düşünce 97 kalıyor (Fortran hem `.F90`
hem `.f90` olarak geçiyor, bu eşleştirici küçük harfe çeviriyor). Liste **Go'da
sabit** (SD-1/SD-5): kurulu paketten türetmek, bu deponun bir satırı bile
kıpırdamadan bir `pip upgrade`'in taramanın kapsamını değiştirmesi demek olurdu.

Bunun raporlama yarısı `noteExtensionDrift`: probe artık kurulu paketin kendi
listesini de basıyor ve fark `slog`'a yazılıyor. Sessizce geri kalan bir liste,
yapısal katmanın bir deponun yarısını kapsamayı sessizce bırakmasının yoludur —
task-72 ile bu task arasında tam olarak bu oldu.

### Beş okuma, model çağrısı yok

`internal/brain/traverse.go`: `Query`, `Affected`, `Path`, `Explain`, `Hubs`.
Şekiller Graphify'ın, çünkü bir kod grafiğine sorulmaya değer beş soruyu o
çıkarmış; uygulama Graphify'ın değil, çünkü CLI'ına gitmek diskte ikinci bir
grafik dosyası ve Mimir'in zaten sahip olduğu veriyi okumak için bir Python ön
koşulu demekti. task-72'nin çizdiği sınır korunuyor.

`Query`'nin **kısıtlı genişletmesi** birebir taşındı ve bir nezaket değil:
indeks harfi harfine eşleştiriyor, yani grafiğin kendi kelimelerinden farklı
sorulmuş bir soru sıfır döndürüyor, ve sıfır tam olarak "orada bir şey yok" gibi
görünüyor. Aday kelimeler grafiğin kendi düğüm başlıklarından çıkarılıyor, yani
"uydurma" bir rica değil, uygulanabilir bir kural. Hiç eşleşme yoksa cevap boş
liste değil, bir cümle.

`Path` çift yönlü BFS — hub'ları yüzlerce kenarlı bir grafikte tek uçtan arama,
varmadan önce grafiğin çoğunu geziyor.

### Dört MCP aracı, mandanın arkasında

`graph_query`, `graph_affected`, `graph_path`, `graph_hubs`. Dördü de
`graph-query` skill'ini bildiriyor, yani task-77'nin choke-point kapısından
geçiyorlar. Bu bir süs değil: o disiplin olmadan bu araçlar kendinden emin
saçmalık üretmenin bir yolu, çünkü literal bir eşleştirici başka kelimelerle
sorulmuş soruya sıfır döndürür. `Skills()` ortak `graphBase`'de, ki beşinci bir
araç onsuz eklenemesin. Yanıtlar `MetadataOnly` — sembol adı, dosya, satır,
kenar türü; sayfa metni yok.

`internal/api/graph.go`: `POST /brain/query`, `POST /brain/affected`,
`GET /brain/hubs`. Kendi `GraphRead` bağımlılığına bağlı, `BrainReader`'a değil.

### Geri besleme halkası

`0024_graph_memory.sql` + `internal/store/graphmemory.go`. `RecordGraphResult`
ve `GraphLessons` — graphify'ın `reflect`'inin model çağrısız yarısı: yarı ömürle
ağırlıklandırılmış sayım, en az iki bağımsız sonuç görmemiş düğüm önerilmiyor,
çıkmaz sokaklar eksi puan. Düzeltmeler puana **girmiyor**: bir kez yanlış çıkmış
ama düzeltilmiş bir düğüm bunun için cezalandırılmamalı, ve düzeltme metni bu
tablodaki en değerli satır.

Bu, bilgi tabanının dosyalardan türetilemeyen tek parçası — bir depoyu iki kez
tarayınca aynı grafiği alırsınız, ama soranların öğrendiğini alamazsınız. O
yüzden bir kayıt, önbellek değil.

### Sapmalar

- `internal/api/integration_test.go`'daki kanonik araç listesi dört yeni adı
  aldı. Bu bir sözleşme testidir ve araç eklemek onu güncellemeyi gerektirir.
- `internal/brain/brain_test.go` **açılmadı**: `Store` arayüzü genişledi ve
  fake'in iki yeni metodu `traverse_test.go`'ya kondu.
- `internal/store/brain.go`'nun yazma davranışı değişti — task dosyası bunu
  öngörmüyordu, gerekçesi yukarıda.

`make check` yeşil, `-race` dahil.
