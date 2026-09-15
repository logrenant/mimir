# task-72 — Graphify: opsiyonel, modelsiz yapısal katman

- **Status:** done
- **Owner agent:** Claude Opus
- **Prerequisites:** task-41 (brain çekirdeği), task-51 (yerleşik tarama),
  task-69 (tarama izinleri), task-71 (Brain ekranının performansı)
- **Primary paths:** `internal/graphify/`, `internal/brain/structural.go`,
  `internal/brain/supervisor.go`, `internal/settings/structural.go`,
  `internal/api/structural.go`, `internal/api/api.go`,
  `internal/config/config.go`, `cmd/mimir-daemon/main.go`,
  `desktop/src/lib/daemon.ts`, `desktop/src/lib/brainGraph.ts`,
  `desktop/src/screens/Brain.tsx`
- **Roadmap bucket:** B.9 — Brain

## Context

Bilgi grafiği bugün **dosya** granülaritesinde ve kenarları bir model kararıyla
kuruluyor (`relate.go` → `semanticEdges`: düğüm başına FTS ile ~20 aday, bir LLM
çağrısı). Bu "bu dosya ne anlatıyor" sorusunu cevaplıyor; "bu fonksiyonu kim
çağırıyor" sorusunu cevaplayamıyor, çünkü grafikte sembol yok
(`validKinds`: note/research/session/repo/file/commit/decision).

Graphify'ın tree-sitter geçişi tam bu eksik yarı: deterministik, model çağrısı
yok, Apache-2.0 ve ücretsiz. Bir import kenarı bir olgudur; iki dosyanın ilgili
olup olmadığını bir modele sormak, ayrıştırıcı yokken elde olan en iyi cevap ve
ayrıştırıcı varken daha kötü olanıdır.

**Gömmüyoruz.** Graphify bir Python paketi (`pip install graphifyy`), Mimir ise
iki self-contained Go ikilisi olarak paketleniyor; Python'ı çalışma zamanı ön
koşulu yapmak kurulum hikâyesini bir özelliğe takas etmek olurdu. Bu yüzden
`internal/brain/github.go`'nun deseni: cevap verdiğinde kullanılan, vermediğinde
unutulan bir dış kaynak. Kurulu olmayan makine sembol katmanını kaybeder, başka
hiçbir şeyi — hata yok, boş ekran yok, kurulum adımı yok.

**Planın düzeltilmesi gereken bir varsayımı vardı.** Plan `graphify <path>
--code-only --update` diye bir CLI çağrısı öngörüyordu; böyle bir komut yok.
`graphify` komutu bir grafik üreticisi değil, bir *skill yükleyici*
(`graphify install`, `hook install`, `benchmark`) — grafiği `skill.md`'yi
izleyen **ajanın kendisi** kuruyor, alt-ajanlarla, doküman ve görseller
üzerinde, yani bir model harcayarak. Deterministik yarısı
`python -m graphify.extract` modülüdür: paketin belgelenmiş giriş noktası,
sonucu stdout'a JSON basıyor, hiçbir opsiyonel extra'ya (leiden, neo4j, LLM
sağlayıcıları) ihtiyaç duymuyor. Dokunulan tek yüzey o.

## Scope (do exactly this)

1. `internal/graphify/` (yeni paket)
   - `Detect(ctx, python)` — `import graphify` + `importlib.metadata.version`.
     `--version` değil **import**, çünkü Extract'in yapacağı şey import ve
     ikisi birbiriyle çelişememeli.
   - `Extract(ctx, info, dir, files)` — `python -c <driver>`; dosya listesi
     **stdin'den**. İki sebep: argüman listesine sığmayacak kadar çok dosya
     var, ve — asıl olanı — hangi dosyanın okunacağına **Mimir** karar verir.
     Graphify'a bir dizin göstermek kararı ona geri vermek olurdu: tarama
     izinlerinin dışladığı dosyayı açardı.
   - `Prune()` — Graphify'ın kendi build adımının attıklarını atar: iki ucu da
     mevcut olmayan kenarlar (stdlib/üçüncü parti) ve bağsız kalmış kod
     düğümleri.
   - `Detector` — TTL'li önbellek. Sweep on sekiz proje geziyor; proje başına
     bir Python açılışı aynı şeyi on sekiz kez öğrenmek olurdu. Olumsuz cevap
     da önbelleklenir: Graphify'ı hiç olmayacak makine yaygın durumdur.
2. `internal/brain/structural.go`
   - `KindSymbol` kapalı kümeye eklenir; kaynak anahtarı `path/file.go::Name`
     (Graphify'ın slug id'si değil — o tek bir çıkarım içinde anlamlı).
   - `Core.IngestStructural` doğrudan `store.UpsertBrainNode` /
     `UpsertBrainEdges` kullanır. `Core.Ingest` kullanılmaz: o her zaman distil
     eder, ve bu katmanın tüm iddiası model çağrısı olmaması.
   - Her sembol, taramanın zaten yazdığı `file` düğümüne `defines` kenarıyla
     bağlanır — yoksa iki paralel grafik olur. Dosya düğümü **yaratılmaz**:
     onu tarama sahiplenir ve bir modelin cümlesini dosya adıyla ezmek olurdu.
   - `StructuralFiles` — `listScannable` + `Excluder` + `graphify.Parses`.
3. `internal/brain/supervisor.go` — `Structural func() (graphify.Info, bool)`
   deps'i (Policy ile aynı şekil, aynı gerekçe) ve proje başına, distil
   geçişinden **önce** çalışan `structural()`.
4. `internal/settings/structural.go` — `structural.json` (`enabled`, `python`),
   varsayılan **açık**: Graphify yokken "açık", daemon'un sweep başına bir kez
   bakıp bulamaması demek.
5. `internal/api/structural.go` — `GET`/`PUT /brain/structural`. İki ayrı cevap
   döner: `enabled` (operatörün, saklı) ve `installed` (makinenin, yoklanan).
6. `desktop/` — `symbol` rengi (dosyanın grisi bir ton koyu, beşinci renk
   değil), "sembol" istatistiği, ve Graphify kartı: kurulu değilse `install`
   komutu, kuruluysa sürüm ve hangi yorumlayıcının cevapladığı.

## Out of scope (do NOT do here)

- **Graphify'ın çıktı katmanı.** `graph.html`, `GRAPH_REPORT.md`, Obsidian
  vault, `graph.json`: bizim store'umuz, API'miz ve canvas'ımız zaten var.
- **Graphify'ın MCP sunucusu (`--mcp`).** Mimir'in kendi MCP kaydı var; ikinci
  bir sunucu kullanıcıya iki ayrı beyin gibi görünür.
- **Skill yolu.** Doküman/görsel geçişi bir model harcar ve alt-ajan ister; bu
  paket yalnızca deterministik yarıyı çağırır.
- **Paketi vendor etmek ya da dağıtmak.** Operatörün kurduğu şey çağrılır.
- **Leiden toplulukları.** Opsiyonel bir extra'ya bağımlılık; grafiği gruplamak
  ayrı bir karar.

## Interfaces / contracts

```go
func Detect(ctx context.Context, python string) (Info, error)   // ErrUnavailable
func Extract(ctx context.Context, info Info, dir string, files []string) (Extraction, error)
func (e Extraction) Prune() Extraction
func Parses(rel string) bool
func NewDetector(ttl time.Duration) *Detector

func (c *Core) IngestStructural(ctx, projectPath string, ex graphify.Extraction, version string) (StructuralResult, error)
func StructuralFiles(ctx, projectPath string, exclude Excluder) ([]string, error)
```

```
GET /brain/structural  → { enabled, python?, installed, version?, interpreter?, install }
PUT /brain/structural    { enabled, python? }
```

Kenar türleri: `calls`, `imports`, `defines`, `inherits`, `uses`, ve tanınmayan
her şey için `structural`. Ağırlık Graphify'ın kendi sayısı; yoksa güvene göre
(EXTRACTED 1.0 · INFERRED 0.6 · AMBIGUOUS 0.3).

## Definition of Done

- [x] Graphify kurulu değilken hiçbir şey değişmiyor: hata yok, ekranda kırmızı
      yok, sweep aynı.
- [x] Kurulu olduğunda semboller ve `calls`/`defines`/`imports` kenarları
      **tek bir model çağrısı olmadan** yazılıyor; düğümlerin provider ve model
      alanları boş.
- [x] Sembol, ait olduğu dosya düğümüne bağlanıyor; dosya düğümü ezilmiyor.
- [x] Hariç tutulan bir yol Graphify'a hiç isim olarak verilmiyor (task-69).
- [x] Tavan aşıldığında en bağlantılı semboller kalıyor; kaç tanesinin
      düştüğü rapor ediliyor.
- [x] Aynı proje iki kez taranınca aynı satırlar yazılıyor (içerik hash'i
      sabit), yani sürüm geçmişi tur başına büyümüyor.
- [x] `make check` yeşil.
- [x] `make desktop-check` yeşil.

## Gerçek pakete karşı doğrulandı

`graphifyy 0.9.54`, `~/.mimir/graphify` sanal ortamında, bu deponun kendi 316
ayrıştırılabilir dosyası üzerinde: 3668 düğüm / 10774 kenar çıktı, tavan 2000
sembolde kesti (1228 düştü), 6589 kenar yazıldı, düğümlerin `provider`/`model`
alanları boş.

İki şey ancak burada görüldü:

1. **`pip install graphifyy` bu makinede çalışmıyor.** `pip` PATH'te yok
   (Homebrew `pip3` veriyor) ve sistem Python'ına yazmak ya reddediliyor
   (PEP 668) ya da `brew upgrade`'in sileceği bir dizine yazıyor. Komut artık
   kendi sanal ortamını kuruyor, ve `Detect` o ortamı — bir de pipx'inkini —
   PATH'ten önce arıyor, yani operatörün bir yol yapıştırması gerekmiyor.
2. **`graphify.extract` ilerlemesini stdout'a basıyor** (`extract.py:5964`,
   birkaç düzine dosyanın üstünde). JSON'ın ortasına düşüyor ve bütün çıkarımı
   okunamaz hâle getiriyor — 316 dosyada hata `invalid character 'A'` idi.
   Sürücü artık kütüphanenin bastığı her şeyi stderr'e yönlendiriyor; stdout
   tek bir şey taşıyor.

## Notes for the reviewer (Opus)

- `Detect` bir asgari sürüm dayatmıyor. `--version` çıktısının biçimini
  doğrulayamadım ve doğrulanmamış bir eşik, özelliği yanlışlıkla kapatan bir
  tahmin olurdu; bunun yerine `Parse` bilmediği alanı yok sayıyor ve şema
  uyuşmazlığı "özellik yok" demek oluyor.
- Sembolün `assessment`'ı bir cümle değil, konumu (`api/client.go L20`). Buraya
  bir cümle yazmak onu uydurmak olurdu; boş assessment `0013_brain.sql`'in
  belgelediği normal bir durum.
- Ayar varsayılanı açık. "Açık ama kurulu değil" bir hata değil, en yaygın
  durum — ve tab'ın `pip install graphifyy` cümlesini gösterdiği durum.
