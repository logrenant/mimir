# task-105 — Katalog: hedef dilde yeniden yazım, MSA kaydı ve RTL

- **Status:** done
- **Owner agent:** Coder
- **Prerequisites:** task-103
- **Primary paths:** `internal/catalog/{prompt,rewrite,render}.go`, `internal/skills/**`, `internal/agents/agents.go`, `internal/catalogjob/catalogjob.go`, `internal/api/catalog.go`
- **Roadmap bucket:** Katalog — ürün içeriği stüdyosu

## Context

task-103 dili okuyup yazabilen bir motor bıraktı ama hâlâ tek dilde **üretiyor**.
Hedef: İngilizce ve Arapça içerik üretmek — Arapçada gramer hatası olmayacak ve
metin native hissettirecek.

## Scope (do exactly this)

1. `RewriteRequest.Lang`, `catalogjob.Params.Lang`, `POST /catalog/rewrite`'ta
   `lang`. **Bir kart bir dil**: kart operatörün izlediği, parkettiği ve devam
   ettirdiği birim; iki dili tek karta katlamak "40/200 yazıldı"yı belirsizleştirir.
2. `buildRewritePrompt(..., lang, ...)`. Her dil cümlesi `lang != LangSource`
   arkasında; kaynak dil promptu **byte-identical** kalır ve golden dosyayla çivilenir.
3. Arapça yönergesi: MSA (الفصحى), Körfez e-ticareti; Arapça noktalama `، ؛ ؟`;
   **Batı rakamı**; kaşide yok; hareke yok; marka/model/birim Latin harfle.
   İngilizce: Amerikan yazımı, cümle düzeni başlık, Türkçe harf yok.
4. `addressClause`: `Voice.Address` Türkçe kalır (Türkçe metinden okundu; ikinci
   bir enum türetmek `BrandKit.hash`'i değiştirir ve tüm katalogu çöpe atar) ve
   hedef dile o dilin kendi ekseniyle çevrilir. İngilizcede `siz`/`sen` birleşir.
5. `RenderLang` + `stripDirectionWrapper`: RTL hedefte tek bir
   `<div dir="rtl" lang="ar">`. Sözlük **genişletilmez**.
6. `skills.ProductContentAR` / `ProductContentEN` + `agents.CatalogSkills(lang)`
   + `catalogjob.Executor.UseSkills`.
7. `POST /catalog/rewrite`, dosyanın sütunu olmayan dili **reddeder**.

## Out of scope (do NOT do here)

- Deterministik dil kapısı ve gözden geçirme pasosu (task-107).
- İngilizce için `ikas-fields` sütunu. Gerçek dosyada `Html:Detay-EN` yok;
  operatör kendi sütununu eşleme formundan seçer.

## Definition of Done

- [x] Kaynak dil promptu golden ile byte-identical; `content-v1` yükseltilmedi
- [x] `CatalogSkills("")` bugünkü tek elemanlı küme; Türkçe sürüm dizesi değişmedi
- [x] `RenderLang(_, _, _, LangSource) == Render(...)` byte-identical
- [x] RTL sarmalayıcı, markanın sözlüğünde `div` **olsa da** iç içe geçmiyor
- [x] `DeriveVocabulary` hedef dil sütunundan `div`/`dir` öğrenmiyor
- [x] Dosyanın taşımadığı dil 400 ile reddediliyor
- [x] `make check` yeşil

## Notes for the reviewer (Opus)

- `agents.CatalogSkills` neden var: coderunner bir **ajanın** becerilerini
  birleştiriyor ve o dize taslak anahtarının yarısı. Üç beceriyi koşulsuz
  bildirmek Türkçe pasın sürümünü değiştirir ve onaylanmış her taslak bulunamaz
  hâle gelir — task-101'in düzelttiği hatanın başka bir kapıdan geri gelmesi.
- Yön niteliği neden sözlüğe eklenmedi: sözlüğü genişletmek `dir`'i modelin
  seçtiği her eleman üzerinde serbest bırakır ve sözlüğü dosyanın değil hedef
  dilin fonksiyonu yapar. Bunun yerine bu paketin kendi yazdığı **tek** eleman.
- `stripDirectionWrapper` olmadan: markanın HTML'inde `div` varsa —
  çoğunda var — `sanitizeAttrs` `dir`'i düşürür, yeniden sarmalama bir kat daha
  ekler ve her kayıtta bir kat daha derine iner. Testi bu durumu kuruyor.
