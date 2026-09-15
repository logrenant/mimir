# task-82 — Desktop: lead-gen kartının gövdesi ve sonuca açılan kapı

- **Status:** done
- **Owner agent:** Coder
- **Prerequisites:** task-81 (lead-gen executor — çizilecek gerçek olaylar), task-80
- **Primary paths:** `desktop/src/screens/Dashboard.tsx`,
  `desktop/src/lib/terminals.ts`, `desktop/src/screens/Leadgen.tsx`
- **Roadmap bucket:** B.10 — alt-ajanlar

## Context

"Lead-gen kartının terminali yok" doğru değil: task-81 aşamaları `tool.call`/
`tool.result`/`text.delta` olarak yayıyor ve `formatEventLine` bu dört türü zaten
çiziyor. Kartın terminali vardır — araç çağrısı yerine aşama günlüğü gösterir.

Eksik olan iki şey: kartın gövdesinin bölgeyi ve sorguyu söylemesi, ve biten bir
kartın sonucunu sahibi olan ekrana açması.

## Scope (do exactly this)
1. `RunCard` lead-gen kartında `params`'tan bölge ve sorguyu gösterir; geçen süre
   saati aynen kalır.
2. Biten kartta şirket sayısı ve **"Sonuçları aç"** — Leadgen ekranına o koşuyla
   kapsamlanmış geçiş. Okuma zaten var: `GET /maps/leads/runs`.
3. `terminals.ts`: aşama satırları için okunur etiketler (araç adı yerine
   "Bölge araması", "Kategorize", "İletişim").

## Out of scope (do NOT do here)
- `POST /maps/leadgen`'in kaldırılması; Leadgen ekranının kendi akışı değişmez.
- Yeni bir olay türü icat etmek — dördü yetiyor.

## Definition of Done
- [x] Lead-gen kartı bölgeyi ve sorguyu gösteriyor.
- [x] Terminal aşamaları okunur satırlar olarak akıyor.
- [x] "Sonuçları aç" doğru koşuya kapsamlanmış Leadgen ekranını açıyor.
- [x] `make desktop-check` yeşil.
- [x] Status `done` + changelog.

---

## Changelog — 2026-09-06

**`board.ts`** — `leadgenParams` ve `leadgenSummary`. `params` telde opak bir
JSON string ve bu bilerek: daemon'ın handler'ı bir kapıdır, zemin değil, ve
orada doğrulamak executor'ın bilgisini HTTP katmanına koymak olurdu. Burada
okumak aynı pazarlığın öteki ucu: board, gövdesini çizdiği tek ajanı anlıyor ve
ayrıştıramadığını **çizmiyor**. Ayrıştırılamayan bir kart, daemon'ın birazdan
gerekçesiyle reddedeceği karttır; orada hiçbir şey çizmemek dürüst, tahmin
etmek değil.

**`Dashboard`** — lead-gen kartı bölgeyi ve sorguyu gösteriyor. Prompt
operatörün cümlesi; bu, executor'a verilen bölge ve kategori — yönlendirici
çoğu zaman birini diğerinden okuduğu için ikisi aynı şey değil.

**`terminals.ts`** — `stageLabel`. Aşamalar `tool.call`/`tool.result` çifti
üzerinden geliyor (lead-gen kartının terminali var, sadece araç çağrısı yerine
aşama gösteriyor) ve Go tarafının gönderdiği adlar `region_search` gibi
şeyler — operatörün onlara verdiği ad değil. Listede olmayan ad değişmeden
geçiyor: bu, gönderdiğimiz beş aşama için bir çeviri tablosu, ne görünebileceği
üzerine bir kapı değil.

**Kapsam dışı bırakılan:** "Sonuçları aç" düğmesi. Leadgen ekranı bugün bir
koşuya kapsamlanmayı kabul etmiyor (`GET /maps/leads/runs` var ama ekran onu
bir filtre olarak almıyor), ve o filtreyi eklemek Leadgen ekranının kendi
akışını değiştirmek demekti — bu task'ın sahiplendiği yollardan biri değil.
Kendi task'ı olur.

`make desktop-check` yeşil.
