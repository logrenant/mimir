# task-95 — Kodlama koşuları başka ajan CLI'larında da çalışır

- **Status:** blocked
- **Owner agent:** Coder (Gemini)
- **Prerequisites:** task-79, task-93
- **Primary paths:** `internal/coderunner/**`, `internal/agents/agents.go`, `internal/api`, `cmd/mimir-daemon`
- **Roadmap bucket:** §B — model yönlendirme

## Context

task-79 dispatcher'ı `claude` dışında bir şey de başlatabilecek hâle getirdi ve
task-81 ilk örneği verdi (lead-gen: Go kodu, alt süreç yok). Ama **ajan
koşusu** — bir klasörde dosya okuyup yazan, araç çağıran koşu — hâlâ tek bir
CLI'a bağlı: `claude`, kendi stream-json akışıyla.

`internal/llm/AGENTS.md` bunu bilerek dışarıda bırakıyordu: *"stream-json
taşıması, izin kipi ve süreç grubu sinyalleri farklı bir sözleşme."* Doğru; ama
"farklı bir sözleşme" ile "tek bir sağlayıcıya çakılı" aynı şey değil.
`gemini` de ajan: `-o stream-json`, `--approval-mode`, kendi araç döngüsü.

## Ölçülen ve tasarımı belirleyen gerçek

`gemini`, **güvenilmeyen bir klasörde `--approval-mode`'u sessizce `default`'a
düşürüyor**:

```
Approval mode overridden to "default" because the current folder is not trusted.
```

`default` "her araç için onay sor" demek, ve onay soracak kimsenin olmadığı bir
daemon alt sürecinde bu, **süresiz bekleyen bir koşu** demek. `--skip-trust`
bunun içindir ve bu task'ta bir tercih değil, şart. Bu cümle, kurulu CLI'ı
gerçekten çalıştırmadan asla bilinemezdi.

## Scope (do exactly this)

1. **`AgentCLI` — ajan alt süreçlerinin sözleşmesi.** Bir spec: ikili,
   klasör + prompt için argümanlar, olay akışının formatı, iptal sinyali, ve
   akış olaylarını `internal/events`'in tiplerine çeviren bir çözücü.
2. **`claude` mevcut davranışını birebir korur** ve bu bir refactor olarak
   ispatlanır: mevcut testler değişmeden geçer.
3. **`gemini` ajan spec'i**: `-o stream-json`, `--approval-mode yolo`
   (ya da `auto_edit`), ve **`--skip-trust`** — yukarıdaki gerekçeyle.
4. **Kart hangi ajan CLI'ında koşacağını taşır.** `store.RunRow` bir sağlayıcı
   alanı kazanır; boş olan satırlar `claude` demektir (append-only göç, geçmiş
   satırlar anlamını korur).
5. **Kimlik yuvası yalnız kimlik isteyen sağlayıcıya sayılır.** `claude`
   koşuları bir hesap yuvası tutar; `gemini` tutmaz ama kendi limitini harcar.
   task-89'un `slotFor`'u bu ayrımı zaten taşıyor — sağlayıcıya göre
   anahtarlanır.
6. `internal/agents` kaydı, hangi ajanın hangi CLI'larda koşabileceğini söyler.

## Out of scope (do NOT do here)

- İkinci bir kuyruk. `internal/coderunner` tek kuyruğun sahibi; yeni iş bir
  `Executor` alır, ikinci bir zamanlayıcı almaz.
- Kurulu olmayan CLI'lar (`codex`). Bayrakları ölçülemez.
- `desktop/**` — kartın sağlayıcı seçicisi task-98.

## Interfaces / contracts

```go
type AgentCLI interface {
    Name() string
    Available(ctx context.Context) error
    Start(ctx context.Context, job Job) (Stream, error)
}
```

## Definition of Done

- [ ] `claude` koşuları bir spec üzerinden koşuyor, davranış değişmemiş.
- [ ] `gemini` bir kartı koşturabiliyor; `--skip-trust` testle sabitlenmiş.
- [ ] Satırdaki boş sağlayıcı `claude` olarak okunuyor.
- [ ] Yuva tutma sağlayıcıya göre anahtarlanıyor.
- [ ] `make check` yeşil · Status `done` + changelog.

## Notes for the reviewer (Opus)

- Tek kuyruk kuralı: bu task bir `Executor` ekler, zamanlayıcı eklemez.
- `--skip-trust` olmadan koşu sessizce onay bekler; testi olmadan bu bir kez
  daha kaybolur.

## Neden başlanmadı (task-93 ve 98 bitmişken)

Bu task'ın tamamı tek bir şeye bağlı: **ikinci bir ajan CLI'ının akış
formatını gözlemlemek.** `gemini` bu makinede kurulu ama **oturumu kapalı** —
`GEMINI_API_KEY` yok ve `~/.gemini/settings.json` bir yöntem tanımlamıyor. CLI
bunu kendi ağzıyla söylüyor:

```
{"session_id":"…","error":{"type":"Error",
 "message":"Please set an Auth method in your …/settings.json or specify one of
 the following environment variables …","code":41}}
```

Yani `-o stream-json`'ın olay şekillerini **hiç göremiyorum**. Onları
hafızadan yazmak, bu oturumda iki kez kaçındığım hatanın aynısı olurdu:
task-91'de uydurma IKAS profili kendi fikstürünü geçiyordu ve hiçbir gerçek
dosyaya uymuyordu; sahte bir CLI'a karşı yazılmış bir çözücü de kendi
uydurmasını doğrular, gerçek CLI hakkında hiçbir şey kanıtlamaz.

**Yarısını yapmak da doğru değil.** Satıra bir `agent_cli` sütunu, bir
`AgentCLI` arayüzü ve sağlayıcıya göre anahtarlanan bir yuva eklenebilirdi —
hepsi test edilebilir. Ama hiçbiri çalışamayan bir sağlayıcı için: kullanılmayan
şema, tek uygulaması olan bir arayüz, ve hiç ikinci değer almayan bir anahtar.
Bu, deponun kendi kurallarının spekülatif genellik dediği şey.

### Engeli kaldıran şey

`gemini` oturumu açılınca (`GEMINI_API_KEY` ya da CLI'ın kendi girişi), bir
gerçek koşu gözlemlenip çözücü ona göre yazılabilir. O ana kadar bu task'ın
**tasarımı hazır ve bulgusu kayıtlı**:

- `--skip-trust` bir tercih değil **şart**. Ölçüldü: `gemini` güvenilmeyen bir
  klasörde `--approval-mode`'u sessizce `default`'a düşürüyor
  (*"Approval mode overridden to 'default' because the current folder is not
  trusted"*), ve `default` "her araç için onay sor" demek — onay soracak
  kimsenin olmadığı bir daemon alt sürecinde bu, **süresiz bekleyen bir koşu**.
  Bu cümle, kurulu CLI gerçekten çalıştırılmadan asla bilinemezdi.
- Yuva anahtarı sağlayıcıya göre olmalı: `claude` koşusu bir kimlik yuvası
  tutar, `gemini` tutmaz ama kendi limitini harcar. task-89'un `slotFor`'u bu
  ayrımı zaten taşıyor.
- Satırdaki boş sağlayıcı `claude` demek (append-only göç, geçmiş satırlar
  anlamını korur).
