# task-84 — Desktop: Brain ekranında grafik sorgu konsolu

- **Status:** done
- **Owner agent:** Coder
- **Prerequisites:** task-83 (`POST /brain/query`, `/brain/affected`, `GET /brain/hubs`)
- **Primary paths:** `desktop/src/screens/Brain.tsx`, `desktop/src/lib/daemon.ts`
- **Roadmap bucket:** B.9 — Brain

## Context

Brain ekranı bugün grafiği çiziyor ama ona **soru soramıyorsun**. task-83 beş okumayı
verdi; bu task üçünü ekrana koyar. Grafiğin kendisi zaten orada — eksik olan, resmin
yanında bir cevap alanı.

## Scope (do exactly this)
1. Sorgu kutusu: soru → `POST /brain/query`. Sonuçta **seçilen kelime dağarcığı
   token'ları görünür** — cevabın neye dayandığı denetlenebilir olmalı, bu skill'in
   kendi kuralı.
2. Bir düğüm seçiliyken "Kim çağırıyor" → `POST /brain/affected`, çağıranlar listesi.
   Bugün cevapsız olan soru budur.
3. "Merkezler" → `GET /brain/hubs`, dereceye göre sıralı mimari düğümler; tıklayınca
   grafik o düğüme odaklanır.
4. Hiç eşleşme olmayan sorguda daemon'ın kendi cümlesi gösterilir ("bu grafikte bu
   konuda kelime yok"); ekran boş bir liste çizip sessiz kalmaz.

## Out of scope (do NOT do here)
- `path` ve `explain` için ayrı bir yüzey — üçü yeter, dördüncüsü kendi task'ı.
- Sonuç geri beslemesinin (`useful` / `dead_end`) UI'ı.
- Yapısal katman kartının (task-72) değişmesi.

## Definition of Done
- [x] Üç okuma da ekranda çalışıyor.
- [x] Genişletilen token'lar görünüyor.
- [x] Boş sonuç sessiz kalmıyor.
- [x] `make desktop-check` yeşil.
- [x] Status `done` + changelog.

---

## Changelog — 2026-09-06

**`lib/daemon.ts`** — `GraphHit`, `GraphAnswer`; `api.brainQuery`,
`api.brainAffected`, `api.brainHubs`.

**`components/GraphConsole.tsx`** — Brain ekranının sağ rayında, resmin yanında.
Grafik birkaç sürümdür çizilebiliyordu ve hiç sorulamıyordu: operatör iki şeyin
bağlı olduğunu görebiliyor, nasıl bağlı olduğunu ya da biri değişirse ne
kırılacağını öğrenemiyordu.

Üç okuma: soru, "kim çağırıyor", "merkezler". Hiçbiri model çağrısı harcamıyor —
zaten olgu olan kenarlar üzerinde yürüyorlar — yani konsol bir arama kutusu gibi
kullanılabilir: serbestçe, ve ilk iki denemede yanlış.

**Genişletilen token'lar ekranda.** İndeks harfi harfine eşleştiriyor; bunlar
olmadan bir ıskalama ile bir yokluk birbirinin aynı görünür ve cevap
denetlenmek yerine inanılmak zorunda kalır. `graph-query` skill'inin kuralı,
ekranda karşılığı olan hâliyle.

**"Kim çağırıyor" seçim yokken gizlenmiyor, pasif.** Operatör düğmenin orada
olduğunu görebilmeli ve bir düğüm istediğini öğrenebilmeli.

Boş sonuçta daemon'ın kendi cümlesi gösteriliyor; ekran boş liste çizip sessiz
kalmıyor.

**Kapsam dışı:** `path` ve `explain` için ayrı yüzey (task zaten dışarıda
bırakmıştı), ve sonuç geri beslemesinin UI'ı — `store.RecordGraphResult` ve
`GraphLessons` task-83'te yazıldı ama onları besleyecek düğmeler kendi task'ı
olur, çünkü "bu cevap işe yaradı mı" sorusunu ne zaman sorduğun bir tasarım
kararı ve bu task onu almadı.

`make desktop-check` yeşil.
