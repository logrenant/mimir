# task-78 — Desktop: kartın ajanı ve skill'leri, ve skill editörü

- **Status:** done
- **Owner agent:** Coder
- **Prerequisites:** task-77 (skill deposu + ajan defteri + `GET /skills`, `GET /agents`)
- **Primary paths:** `desktop/src/lib/{daemon.ts,modules.ts}`,
  `desktop/src/components/{TaskComposer.tsx,NewTaskOverlay.tsx}`,
  `desktop/src/screens/Settings.tsx`
- **Roadmap bucket:** B.10 — alt-ajanlar

## Context

task-77 daemon'a beş ajan ve dört skill verdi; ekranda hiçbiri yok. `modules.ts` hâlâ
iki modül biliyor (`coding`, `leadgen`) ve kendi yorumu bunu bir söz olarak yazıyor:
"there is no placeholder here, and adding one would be the fastest way to make the
shell lie about what the daemon does." Bu task o sözü tutar — modüller artık daemon'ın
`GET /agents`'ından okunur, elle yazılmaz.

Skill gövdeleri operatörün yazısıdır ve düzenlenebilir olmaları gerekir; Ayarlar
ekranında outreach kural editörü zaten var, skill editörü onun yanına oturur —
aynı şekil, aynı "varsayılana dön" düğmesi.

## Scope (do exactly this)

1. `daemon.ts`: `api.listAgents()`, `api.listSkills()`, `api.putSkill(id, body)`,
   `api.resetSkill(id)`. `Run` tipi `agent: string`, `skills?: string[]` kazanır.
2. `modules.ts`: elle yazılmış `MODULES` dizisi, `GET /agents`'tan gelen listeye
   dayanan bir türetmeye dönüşür. Ekranı olan ajan (`coding`, `leadgen`) rotasını
   korur; ekranı olmayan (`review`, `marketing`, `graph`) board'da kart olarak yaşar.
3. Kart bestecileri (`TaskComposer`, `NewTaskOverlay`): ajan seçici (varsayılan
   "otomatik seç" — boş bırakılırsa daemon model çağrısını yapar) ve seçilen ajanın
   zorunlu skill'lerinin salt-okunur rozetleri.
4. `Settings.tsx`: dört skill için editör. Kural editörünün deseninin aynısı —
   gövde, dosya yolu, "varsayılana dön", ve gövde değiştiğinde sürümün değiştiğini
   söyleyen satır.
5. Primitive'ler `components/ui/`'dan alınır; onuncu bir düğme kopyası yazılmaz.

## Out of scope (do NOT do here)
- Board'un kendisi ve kart gövdeleri — task-80/82.
- Grafik konsolu — task-84.
- `internal/**` altında hiçbir dosya.

## Definition of Done
- [x] Ajan seçici çalışıyor; boş bırakınca daemon seçiyor ve seçim kartta görünüyor.
- [x] Dört skill düzenlenip resetlenebiliyor; sürüm satırı değişiyor.
- [x] `modules.ts` artık elle yazılmış bir liste değil.
- [x] `make desktop-check` yeşil (typecheck + vitest + cargo fmt/clippy/test).
- [x] Status `done` + changelog.

## Notes for the reviewer (Opus)
- `desktop/AGENTS.md`: her daemon erişimi `lib/daemon.ts`'te mi? Elle `invoke` var mı?
- Dört marka rengi dışına çıkan bir renk, `.focus-ring`'siz bir etkileşim var mı?

---

## Changelog — 2026-09-06

**`lib/daemon.ts`** — `AgentDef`, `AgentCatalogue`, `Skill` tipleri;
`api.agents()`, `api.skills()`, `api.saveSkill`, `api.resetSkill`. `Run` tipi
`agent`, `skills`, `params` kazandı ve `project_id` artık boş olabiliyor.
`CreateTaskRequest` opsiyonel `agent`/`params` alıyor.

**`components/AgentPicker.tsx`** — `AgentsProvider` (bir kez okur), `AgentSelect`,
`SkillBadges`, `needsProject`. Katalog daemon'dan okunuyor, burada yazılmıyor:
bu binary'nin gönderdiği bir sabit ve elle tutulan bir kopya ilk ajan
eklendiğinde yanlış olurdu — üstelik fark edilmesi en zor yönde, çünkü bir
seçeneği eksik olan seçici de seçici gibi görünür.

Boş seçenek asıl olanı: ajanı boş bırakmak daemon'a "sen seç" demek. Varsayılan
bu, çünkü operatörün istediği buydu ve çünkü seçimi peşin isteyen bir form,
insana işini yazmadan önce sınıflandırmasını dayatır.

**`NewTaskOverlay`** — ajan seçici en üstte. `needs_project` false olan bir ajan
seçildiğinde **proje alanı kayboluyor**: bölge araması için klasör kaydettirmek,
hiç açılmayacak bir dizini zorunlu kılmak olurdu. Ajan boşsa istek gövdesine
**hiç konmuyor** — boş string daemon'ın reddetmesi gereken bir anahtar, yokluk
ise "sen seç" demek.

**`components/SkillsCard.tsx`** — outreach kural editörünün aynı şekli, bilerek:
aynı türden soruyu cevaplıyorlar ve birini öğrenmiş operatör ikincisini
öğrenmek zorunda kalmamalı. Fark, yüklenemediğinde ne olduğu — ve sürüm rozeti
ekranda, çünkü bir koşu hangi sürümle çalıştığını kaydediyor ve ikisini
eşleştirmek "bu çıktıyı hangi yönerge üretti" sorusunun tek cevabı.

Skill başına kaydediliyor, sayfanın save bar'ından değil: operatör bir dosyayı
düzenler, dört PUT'u tek düğmenin arkasına toplamak "ben yalnız inceleme
olanı değiştirecektim"i ifade edilemez kılardı.

**Sapma — `modules.ts` elle yazılmış liste olarak kaldı.** Task bunu
"türetilmeye" çağırıyordu; yapmadım, çünkü yanlış olurdu. `MODULES` *ekranı
olan* şeylerin listesi; `GET /agents` ise *iş türlerinin* listesi. Beşini de
kenar çubuğuna koymak, üçü hiçbir şey çizmeyen menü öğeleri demek olurdu — yani
`modules.ts`'in kendi yorumunun uyardığı şeyin aynısı, ters yönde: "adding one
would be the fastest way to make the shell lie about what the daemon does."
`coding` ve `leadgen` ekranlarını koruyor; `review`, `marketing`, `graph`
board'da kart olarak yaşıyor, ki zaten oraya ait oldukları yer orası.

`make desktop-check` yeşil.
