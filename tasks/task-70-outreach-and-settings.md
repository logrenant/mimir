# task-70 — Seçilen şirketlere iki kanaldan taslak, ve konfigürasyonun kendi ekranı

- **Status:** done
- **Owner agent:** Claude Opus
- **Prerequisites:** task-33 (4. aşama taslak yazımı), task-63/64 (lead defteri
  ve masaüstü defter ekranı), task-39/40 (model izin listesi ve seçici)
- **Primary paths:** `internal/settings/`, `internal/leadgen/message.go`,
  `internal/refine/message.go`, `internal/store/outreach.go`,
  `internal/store/migrations/0020_outreach_channel.sql`,
  `internal/api/settings.go`, `internal/api/maps.go`,
  `desktop/src-tauri/src/daemon.rs`, `desktop/src/lib/daemon.ts`,
  `desktop/src/lib/leadgen.ts`, `desktop/src/lib/settings.ts`,
  `desktop/src/components/ui/checkbox.tsx`, `desktop/src/screens/Settings.tsx`,
  `desktop/src/screens/Leadgen.tsx`, `desktop/src/screens/Dashboard.tsx`
- **Roadmap bucket:** B.6 — Lead-gen

## Context

4. aşama (task-33) bir bölgede **bulunan herkese** e-posta taslağı yazıyordu ve
başka bir şey yazamıyordu. İki ayrı sorun:

1. **Kararı kimse vermemişti.** Bir bölge araması iki yüz şirket döndürüyor,
   `emails: true` iki yüz model çağrısı harcıyor, ve operatör listeyi
   okumadan önce ödemiş oluyordu. Harcamayı hak eden şey aramanın kendisi
   değil, "şunlara yazacağım" kararıydı — ve o kararı ifade edecek bir yüzey
   yoktu.
2. **Tek kanal, tek ses.** Taslak metni Go'da bir sabitti. WhatsApp bir e-posta
   değildir (karşı taraf telefonunda, iş başındayken okur) ve operatörün kendi
   diliyle yazılmış bir kural dosyası olmadan ikisi de aynı ajans dilinde
   çıkıyordu.

Buna bağlı üçüncüsü: model seçici lead-gen arama çubuğundaydı. Bir çalıştırmanın
hangi modeli harcadığı kampanyaya ait bir karardır; arama çubuğundaki seçici
her aramada sıfırlanıyor ve durumunu sonradan kimse göremiyordu.

## Scope (do exactly this)

1. `internal/settings/` — operatörün sahip olduğu değerler için, `internal/config`
   *değil*: `Channel` kapalı kümesi, `Values{Provider, Model}`, ve kanal başına
   bir kural dosyası (`rules/*.md`, gömülü varsayılanlarla, okumada tohumlanır).
2. `internal/refine/message.go` + `internal/leadgen/message.go` — 4. aşama kanal
   ve kural dosyası alır; `prompt_version` model **ve** kural hash'ini taşır.
3. `internal/store/outreach.go` + migration `0020` — taslaklar `channel` sütunu
   kazanır; `SetOutreachStatus` kanal başına karar yazar.
4. `internal/leadgen.DraftOutreach` — bölge araması olmadan, verilen şirket
   kümesi için 3. ve 4. aşama.
5. `internal/api` — `POST /maps/outreach`, `POST /maps/outreach/status`,
   `GET`/`PUT /settings`, `PUT /settings/rules`, `POST /settings/rules/reset`;
   `leadgenSelection` kaydedilmiş varsayılanı arka planda okur.
6. `desktop/src-tauri/src/daemon.rs` — verb izin listesine `PUT` ve `PATCH`.
7. `desktop/src/lib/leadgen.ts` — seçim modeli (`Selection`, `toggleOne`,
   `setMany`, `rangeOf`, `headerState`, `summarize`, `toggleChannel`) ve kanal
   başına taslak modeli (`draftFor`, `draftQueue`, `draftCounts`, `withDraft`,
   `withDraftStatus`, `mergeDrafts`, `whatsappHref`) — hepsi saf ve testli.
8. `desktop/src/components/ui/checkbox.tsx` — üç durumlu, klavyeyle
   çalıştırılabilir kutu; shift değiştiricisi `change` olayına taşınır.
9. `desktop/src/screens/Leadgen.tsx` — onay kutusu sütunu, seçim çubuğu, kanal
   sekmeli taslak ekranı; model seçici kaldırılır.
10. `desktop/src/screens/Settings.tsx` + sidebar girdisi — model seçimi ve iki
    kural düzenleyicisi, tek kaydetme çubuğu.

## Out of scope (do NOT do here)

- **Mesaj göndermek.** Uygulamanın posta hesabı ya da WhatsApp oturumu yok;
  "gönderildi" operatörün kararı olarak kalır. `wa.me` / `mailto:` bağlantıları
  metni operatörün zaten kullandığı uygulamaya devreder, o kadar.
- **Üçüncü bir kanal.** Kanal kapalı bir küme ve bir kanal *bir kural
  dosyasıdır*: üçüncüsü üçüncü bir varsayılan metin göndermek demektir.
- **Brain taramasının kendi model seçicisi.** O tek bir tarama turunu
  yönlendirir; kampanya ayarı değildir ve yerinde kalır.
- **`internal/config`'e ayar eklemek.** SD-1: bir zaman aşımı ya da token tavanı
  makinenin özelliğidir ve buradaki hiçbir şey oraya taşınmaz.

## Interfaces / contracts

```go
type Channel string // "email" | "whatsapp"
func (s *Store) RuleBody(ch Channel) (body string, version string)

type OutreachRequest struct {
    Companies  []maps.Company
    Categories []Category
    Region     string
    Channels   []settings.Channel
    Selection  llm.Selection
}
func (p *Pipeline) DraftOutreach(ctx context.Context, req OutreachRequest) (OutreachResult, error)
```

```
POST /maps/outreach         { place_ids, channels, region?, provider?, model? }
POST /maps/outreach/status  { place_id, channel, status }     → 204
GET  /settings              → { provider, model, routed, rules[] }
PUT  /settings              { provider, model }
PUT  /settings/rules        { channel, body }
POST /settings/rules/reset  { channel }
```

`prompt_version` = `LeadgenEmailVersion[@model]#channel[:ruleHash8]`.

## Definition of Done

- [x] `POST /maps/outreach` süzgeç değil kimlik listesi alır, `LeadsPageMax` ile
      sınırlı, defterde olmayan kimliğe yazmaz.
- [x] Taslak ve karar kanal başına saklanır; bir kanaldaki "gönderildi" ötekini
      etkilemez.
- [x] Kural dosyası düzenlemek `prompt_version`'ı değiştirir ve eski taslakları
      geçersiz kılar; ekran bunu düzenlemenin yapıldığı yerde söyler.
- [x] Seçim `place_id` kümesidir ve süzgeç değiştiğinde düşmez; çubuk süzgeç
      dışında kalan sayıyı açıkça yazar.
- [x] Başlıktaki kutu üç durumlu ve yalnızca tablodaki satırlara dokunur.
- [x] `POST /maps/leadgen` artık `provider`/`model` göndermez; kaydedilmiş
      varsayılan daemon tarafında okunur.
- [x] `daemon_request` PUT ve PATCH'i kabul eder.
- [x] `make check` yeşil.
- [x] `make desktop-check` yeşil.

## Notes for the reviewer (Opus)

- **SD-1:** `internal/settings` bilerek `internal/config` değil. Buraya bir zaman
  aşımı ya da token tavanı sızdı mı? (Sızmamalı: burası operatörün kendi yazdığı
  metin ve kampanya başına verdiği karar.)
- **SD-6:** okunamayan bir kural dosyası gömülü varsayılana düşer, ekrana hata
  basmaz — ama `GET /settings` bozuk bir `settings.json`'ı **söyler**, çünkü o
  ekranın işi tam olarak kaydedileni göstermektir. İki farklı davranış kasıtlı.
- **Maliyet sınırı:** `POST /maps/outreach`'in üst sınırı isteğin kendisidir.
  Bir süzgeç kabul eden bir sürümü bu sınırı kaldırır.
- **Masaüstü:** seçimin satır üzerinde bir bayrak olmadığını doğrulayın; süzgeç
  değiştiğinde sessizce eriyen bir seçim, operatörün işaretlediğinden daha az
  şirkete yazmak demektir.
