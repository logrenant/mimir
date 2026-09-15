# task-100 — Desktop: bağlantılar bölümü, bölüm rayı, ve kaydedilmeyen ayar

- **Status:** done
- **Owner agent:** Coder (Gemini)
- **Prerequisites:** task-97, task-98
- **Primary paths:** `desktop/**`
- **Roadmap bucket:** Desktop kabuğu

## Context

İki iş, biri hata.

**Hata:** task-98'de eklediğim sınıf varsayılanları **hiç kaydedilmiyordu.**
`Settings.tsx` `classesDirty`'yi hesaplayıp `dirty`'ye katıyordu, ama `save()`
yalnız `if (modelDirty)` içinde yazıyor ve `api.saveSettings`'in üçüncü
argümanını hiç geçmiyordu. Yalnız sınıf varsayılanını düzenlemek: düğme açılır,
**hiçbir şey yazılmaz**, ekran "Kaydedildi" der.

Sebebi tesadüf değil: `anyDirty` testli bir dosyadaydı, sınıf karşılaştırması
JSX'te dört satır içi ifadeydi. İki kural iki yerde yaşayınca ayrıştılar.

**İş:** task-97 bağlantıyı yönlendirmenin birimi yaptı. Ekranın bunu
gösterebilmesi gerekiyor — özellikle `agy` ile `gemini`'nin aynı Google
hesabından harcayabildiğini.

## Scope (do exactly this)

1. **`classesDirty`/`classDraftFrom`/`anyDirty` `lib/settings.ts`'e taşınır**
   ve testlenir. `save()` `modelDirty || classDirty` olduğunda **dört alanı da**
   gönderir — `PUT /settings` bütün-belge yazımı, model çiftini sınıf
   varsayılanları olmadan göndermek onları temizlerdi.
2. **`lib/connections.ts`** — durum (`ok`/`signed-out`/`missing`/`disabled`/
   `unknown`), satıcıya göre gruplama, paylaşım cümlesi, taşıma çipi.
   `unknown` gerçek bir cevap: oturumun çalışıp çalışmadığını öğrenmek bir model
   çağrısına mal oluyor, sorulmadan iddia edilmez.
3. **`ConnectionsCard`** — satıcıya göre gruplanmış liste, grup paylaşımlıysa
   cümlesiyle. Kendi yazımına sahip (`SkillsCard` emsali), kaydet çubuğuna
   girmez.
4. **Bölüm rayı** — Bağlantılar · Yönlendirme · Skill'ler · Kurallar. Ray,
   kaydedilmemiş değişiklik taşıyan bölümü işaretler.

## Out of scope (do NOT do here)

- `internal/**`.
- Bağlantı ekleme/silme/sır girişi — task-99 sunucu tarafını, task-102 arayüzü.
- Üst sekme şeridi: `RulesCard` zaten içinde segmentli `Tabs` çiziyor.

## Definition of Done

- [x] Yalnız sınıf varsayılanı değiştirmek **kaydediliyor** — regresyon testi.
- [x] `classesDirty` ve `anyDirty` tek bir testli dosyada.
- [x] Bağlantılar satıcıya göre gruplanıyor; `agy` ile `gemini` tek grupta ve
      "aynı Google hesabından harcayabilir" yazıyor.
- [x] Sınanmamış bir oturum "sınanmadı" diyor, "bağlı" demiyor.
- [x] Ray, kaydedilmemiş bölümü işaretliyor; bağlantılar hiç işaretlenmiyor.
- [x] `make desktop-check` yeşil.
- [x] Status `done` + changelog.

## Changelog

- **Kaydetme hatası kaynağında kapandı.** İki kural tek bir testli fonksiyona
  indi (`classesDirty`), ve `save()` artık dört alanı birden gönderiyor.
  Regresyon testi `anyDirty`'nin sınıf varsayılanını gördüğünü sabitliyor.
  Hatanın kendisi, kuralın ikiye bölünmüş olmasıydı — düzeltme de o.
- **`lib/connections.ts`**, 10 testle. En çok önemseyeni: **`unknown` bir cevap,
  "ok" değil.** Sınanmamış bir oturumu "bağlı" göstermek, operatöre bilmediğim
  bir şeyi bilir gibi söylemek olurdu.
- **`ConnectionsCard` satıcıya göre grupluyor**, ve paylaşımlı grup bunu bir
  cümleyle söylüyor. Bu, task-97'nin modelinin tek görünür karşılığı: `agy` ile
  `gemini` iki ikili ve tek şirket — Antigravity'nin kendi aboneliği yok, bir
  Google AI planına biniyor. Onları bağımsız iki sağlayıcı gibi çizmek,
  operatöre tek bütçesi varken iki bütçesi olduğunu söylemektir, ve biri limit
  bildirdiğinde bu yanlış okuma pahalıya patlar.
- **Bölüm rayı.** Tek sütun üç kart ve 380px'lik bir metin alanı taşıyordu;
  "neyin var ve çalışıyor mu" listesini de taşıyıp taranabilir kalamazdı. Üst
  şerit değil: `RulesCard` zaten segmentli bir şerit çiziyor, ikincisi iç içe
  sekme gibi okunurdu. Ray, kaydedilmemiş değişiklik taşıyan bölümü işaretliyor
  — rayın kendi getirdiği tehlike buydu.
- **Bağlantılar bölümü hiç işaretlenmiyor**, çünkü o kart kendi yazımına sahip.
  Hiç temizlenmeyecek bir nokta, nokta olmaktan çıkar.

`make desktop-check: 0`
