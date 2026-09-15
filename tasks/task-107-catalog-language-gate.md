# task-107 — Katalog: deterministik dil kapısı ve anadil gözden geçirmesi

- **Status:** done
- **Owner agent:** Coder
- **Prerequisites:** task-105
- **Primary paths:** `internal/catalog/{lint,script,review,rewrite}.go`, `internal/config/config.go`
- **Roadmap bucket:** Katalog — ürün içeriği stüdyosu

## Context

"Arapçada hiçbir şekilde gramer hatası bulunmamalı." Depoda bugüne kadar
**hiçbir** doğrulayıcı paso, geçersiz çıktıda tekrar deneme ya da dil kontrolü
yoktu; tek güçlü doğrulama katmanı `Render` ve o deterministik.

Asimetri şu: Türkçe bir yeniden yazım yanlış giderse ekrana bakan operatör
yakalar. Arapça yanlış giderse bir müşteri okuyana kadar kimse yakalamaz.

## Scope (do exactly this)

1. `internal/catalog/script.go` — yazı sistemi olguları tek yerde.
2. `internal/catalog/lint.go`:
   - `NormalizeForLang` / `NormalizeBlocks`: kaşide, bidi denetim karakteri,
     Arap-Hint → Batı rakamı, Arapça cümledeki ASCII `,;?` → `،؛؟`, boşluk.
     Hepsi **not**, hiçbiri ret. Açıklama blok hâlindeyken onarılır — HTML
     üzerinde regex etiketin içini bozar.
   - `CheckLanguage`: Türkçe harf sızıntısı, dil dışı harf oranı, çevrilmemiş
     uzun Latin bölüm, yanlış yazı sistemi, **kaynakta olmayan sayı**, boş alan.
   - `LangReport.OnlySEO()` — reddedilen SEO alanı o alana mal olur,
     reddedilen açıklama ürüne.
3. `internal/catalog/review.go` — ikinci `llm.Reason` çağrısı: anadili hedef dil
   olan bir e-ticaret editörü rolünde, **yalnızca metin** görür, makine kapısının
   bulgularıyla birlikte. Çıktı tekrar kapıdan geçer.
4. `CatalogReviewVersion` taslak anahtarına, **yalnızca hedef dilde**.

## Interfaces / contracts

```go
func NormalizeForLang(c Content, lang Lang) (Content, []LangFinding)
func NormalizeBlocks(blocks []Block, lang Lang) ([]Block, []LangFinding)
func CheckLanguage(c, source Content, lang Lang, cfg config.Config) LangReport
```

## Definition of Done

- [x] Kural başına geçen ve kalan örnek; örnekler gerçek Arapça cümleler
- [x] Latin harfler arasındaki virgül değiştirilmiyor (`A-12,5` korunuyor)
- [x] Kapı açıklamayı **düzyazı** okuyor; etiket adı çevrilmemiş İngilizce sayılmıyor
- [x] Bağlantı hedefi çevrilmemiş metin sayılmıyor
- [x] Hedef dilde iki model çağrısı, kaynak dilde bir
- [x] Makine kapısının reddettiği metin, gözden geçiren erişilemez diye saklanmıyor
- [x] `make check` yeşil

## Notes for the reviewer (Opus)

- `invented_number` en ucuz halüsinasyon kontrolü ve bu yüzden `CheckLanguage`
  kaynağı parametre alıyor: "50 ml"nin "500 ml" olması modelsiz karar verilebilir.
- Neden `Render` değil: `Render` yalnızca blokları görüyor; "model Türkçe
  cevapladı" ilk olarak `title` ve `seo_title`'da görünür. Ayrıca dil kararını
  işaretleme kapısının içine koymak, iki kapıdan birinin incelenmemesi demek.
- Operatör yolu: bulgular **not**, ret değil. Bir insanın Arapçasını oran
  sezgimiz beğenmedi diye reddetmek, editörün kaydet düğmesini sessizce
  çalışmayan bir düğme yapar.
- Gözden geçirene makine bulgularının verilmesi kritik: "500 sayısı kaynakta
  yok" diyen bir editör onu düzeltir; genel olarak hata aramaya çıkan bir editör
  her seferinde başka bir şey bulur.
