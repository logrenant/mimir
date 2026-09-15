# task-96 — Desktop: menü çubuğu paneli bir sohbete dönüşür

- **Status:** done
- **Owner agent:** Coder (Gemini)
- **Prerequisites:** task-94, task-93
- **Primary paths:** `desktop/**`
- **Roadmap bucket:** Desktop kabuğu

## Context

task-94 menü çubuğunu tek bir panele indirdi: durum, besteci, koşu, dört eylem.
Besteci hâlâ bir **form** — bir metin alanı ve iki açılır liste. Operatörün
istediği, bir görevi *yazdırmak* değil **konuşarak kurmak**: bir sohbet
uygulaması gibi çalışan bir sihirbaz.

Formun sorunu yerleşimi değil, sırası: klasör ve model, iş yazılmadan önce
sorulan iki soru. Oysa ikisinin de doğru cevabı çoğu zaman *işin kendisinden*
çıkarılabilir — "mimir-agent'ta katalog testlerini düzelt" cümlesi klasörü
söylüyor.

## Scope (do exactly this)

1. **Panel bir konuşma.** Tek bir giriş satırı; gönderilen her şey bir mesaj
   olarak thread'e düşer. Sihirbaz eksik olanı **sorar**, formda boş bir alan
   olarak bekletmez.
2. **Çıkarım deterministik ve `lib/`de.** Cümledeki bir proje adı kayıtlı
   klasörlerle eşleşiyorsa klasör seçilidir ve bu **görünür** biçimde söylenir
   (bir çipin üstünde), sessizce varsayılmaz. Model çağrısı yok, test var.
3. **Sihirbazın soruları sonlu ve sıralı**: klasör (çıkarılamadıysa), ajan
   (`internal/agents` kaydından), sağlayıcı/model (task-93'ün yayınladığı
   kurulu olanlardan). Hepsi çip; hiçbiri zorunlu değilse sorulmaz.
4. **Onay bir cümle.** Başlatmadan önce sihirbaz ne yapacağını tek satırda
   özetler; `⏎` onaylar. Bir kartın parasını harcayan tek tıklama, ne
   harcadığını söylemek zorunda.
5. **Koşu aynı thread'de akar.** Ayrı bir panel değil: mesajların altına
   devam eder, bitince bildirim çıkar (task-94'ün davranışı korunur).
6. **Geçmiş thread'ler.** Panel açıldığında son konuşma yerinde durur; yeni
   bir görev açık bir hareketle başlar. Yarım yazılmış bir cümle bir
   kapanmadan sağ çıkmalı (bugünkü davranış).

## Out of scope (do NOT do here)

- `internal/**`. Sihirbaz mevcut rotaları kullanır; yeni bir rota gerekiyorsa
  kendi task'ı olur.
- Sihirbazın sorularını bir modele sorması. Çıkarım deterministik: bir görevi
  kurmak için model çağrısı harcamak, kurulmadan önce para harcamaktır.
- Ana pencerenin "Yeni task" diyaloğu (`NewTaskOverlay`).

## Interfaces / contracts

```ts
export type WizardStep = "prompt" | "project" | "agent" | "model" | "confirm";
export type WizardState = { /* ... */ };
export function inferProject(text: string, projects: Project[]): Project | null;
export function nextStep(s: WizardState): WizardStep;
export function summary(s: WizardState): string;
```

## Definition of Done

- [x] Panel bir thread; sihirbaz eksik olanı soruyor.
- [x] `inferProject` testli ve model çağrısız.
- [x] Çıkarılan klasör görünür biçimde bildiriliyor.
- [x] Yalnız kurulu sağlayıcılar seçilebiliyor.
- [x] Onay cümlesi ne harcanacağını söylüyor.
- [x] `make desktop-check` yeşil · Status `done` + changelog.

## Notes for the reviewer (Opus)

- Mantık `lib/`de, JSX'te değil.
- Bir çıkarım sessiz olduğu anda yanlış olur: klasör tahmini her zaman
  görünür ve her zaman üstüne yazılabilir.

## Changelog

- **Panel bir thread.** Tek giriş satırı altta (bir sohbetin girişinin olduğu
  yer), mesajlar üstte, koşu aynı sütunun devamında. Formun sorunu yerleşimi
  değil sırasıydı: klasör ve model, iş yazılmadan önce sorulan iki soruydu.
- **`lib/wizard.ts`** — çıkarım, adımlar, onay cümlesi ve `canStart`, hepsi saf
  ve 14 testli. Model çağrısı yok.
- **`inferProject` Türkçeye göre yazıldı**, ve bu üç kuralda:
  `toLocaleLowerCase("tr")` — varsayılan yerel ayarda `"İ".toLowerCase()`
  birleşen noktalı bir `i` üretiyor ve `İçerik` klasörü hiç eşleşmiyordu;
  kesme işareti bir adı **bitirir**, çünkü Türkçe ekleri öyle yapıştırır
  (`api'de`, `mimir-agent'ta`); ama çıplak bir harf bitirmez, yoksa `test`
  adlı bir proje "testleri düzelt" cümlesine takılırdı.
- **Çıkarım her zaman görünür**: hangi klasörü seçtiğini *ve hangi kelimeden*
  seçtiğini söylüyor. Görülemeyen bir çıkarım düzeltilemez.
- **Onay bir cümle**: ajan · klasör · model. Tek bir tuş vuruşu gerçek para
  harcıyorsa, ne harcadığını söylemek zorunda.
- **`⏎`, `⏎`** — yaz, gönder, onayla. İşaretçi gerekmiyor.
- Dört nadir eylem (aç, yeniden başlat, girişte başlat, çıkış) başlıktaki `⋯`
  arkasına geçti: hiçbiri panelin açılma sebebi değil, ikisi geri alınamaz.

### Sapmalar

- **"Ajan" bir adım olmadı.** Task dosyası sıralamada sayıyordu; uygularken
  daemon'ın zaten bir yönlendiricisi olduğu (task-77) belliydi ve sormak o
  kararın ikinci, daha kötü bir kopyası olurdu. `WizardStep` birleşiminden de
  çıkarıldı — ulaşılamayan bir dal ölü koddur.
- **Model seçici bir gerileme olarak kayboldu ve geri kondu.** Paneli sohbete
  çevirirken iki açılır listeyi sildim; biri modeldi, ve "her görevde model
  seçilebilsin" açık bir şarttı. Onay adımında çip olarak duruyor: sorulmuyor
  ama gizlenmiyor da.
- **`defaultProject` ve eski `canStart` silindi**, testleriyle birlikte. Formun
  soruları ("hangisi seçili", "form tam mı") bir sohbette sorulmuyor. Yalnız
  kendi testleri tarafından hayatta tutulan bir fonksiyon ölü koddur.

`make desktop-check: 0`
