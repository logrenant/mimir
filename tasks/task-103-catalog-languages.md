# task-103 — Katalog: dil boyutu, çok dilli okuma ve kayıpsız çok dilli dışa aktarım

- **Status:** done
- **Owner agent:** Coder
- **Prerequisites:** task-101
- **Primary paths:** `internal/catalog/**`, `internal/api/catalog.go`, `internal/tools/catalog.go`
- **Roadmap bucket:** Katalog — ürün içeriği stüdyosu

## Context

Motor tek dilliydi. `Field` kapalı kümesinde tam olarak **bir** `description_html`
var ve `Product`, `Content`, `Export`, `rewriteSchema` hep bu tekilliğe göre
yazılmış. Oysa gerçek IKAS özel alanlar dışa aktarımının başlığında
`Html:Detay`'ın yanında **`Html:Detay-AR`** duruyor: mağaza Arapça gövdeyi zaten
orada tutuyor, motor onu ne okuyor ne yazıyor.

## Scope (do exactly this)

1. `internal/catalog/lang.go`: `Lang` (`""`/`en`/`ar`), `KnownLang`, `RTL`,
   `Dir`, `Tag`, `Label`; `LangField{Field, Lang}` + `String`/`ParseLangField`.
2. `Dialect.Translations map[Lang]map[Field]string`; `bind` onu da dosyanın
   kendi yazımına bağlar, hiçbir sütunu bulunmayan dil profile hiç girmez.
3. `File.Translations`; `File.ColumnsFor(l)`, `File.index(LangField)`,
   `File.cell(row, LangField)`, `File.Langs()`.
4. `Product.Translations map[Lang]Content` + `Product.Content(l)`; `Products()`
   her dili okur.
5. `Export(f, products, map[string]map[Lang]Content)` — tek geçişte her dil;
   değişiklik testi **o dilin kendi eskisine** karşı. `rowsFor(f, LangField)`;
   boş hedef sütun kaynak dilin satırlarını izler.
6. `DraftVersion(..., lang)` — dil bir **son ek** ve yalnızca hedef dil için.
7. `SetMapping` `map[LangField]string` alır; tamlık testi yalnızca kaynak dil.
8. `ikas-fields` profiline `LangAR: {description_html: "Html:Detay-AR"}`.
9. `stored.go` yeniden algılama koşulu: `len(imp.File.Mapping) == 0`.
10. API: `?lang=` sorgu parametresi (bilinmeyen dil 400), içe aktarım yanıtında
    `languages`, eşleme telinde `field@lang` anahtarı.

## Out of scope (do NOT do here)

- Hedef dilde **yazma**. Bu task okumayı ve dışa aktarma mekanizmasını getirir.
- Dil başına durum/onay. Hedef dilde taslak olmadan onaylanacak bir şey yok;
  task-105 taslakları getirirken kendi kapısını da getirir. `Studio.Export` bu
  yüzden şimdilik yalnızca kaynak dilin onaylarını topluyor — Türkçe kopyayı
  onaylamak, kimsenin okumadığı bir Arapça kopyayı yayına göndermemeli.
- Uzun/dar (tall) format. Gerçek dosya yok.

## Definition of Done

- [x] Beş gerçek fixture üzerinde byte-identity testi hâlâ yeşil
- [x] `ikas-fields` Arapça gövdeyi okuyor; boş hücre boş okunuyor
- [x] Sütunu olmayan dosyaya dil önerilmiyor (`TestFileLangs_…`)
- [x] Onaylanan dilin hücresi yazılıyor, diğer dilin hücresine dokunulmuyor
- [x] `stored.go` düzeltmesi, eski koşulla düştüğü doğrulanmış testle çivili
- [x] Kaynak dil tel yazımı `description_html` olarak değişmedi
- [x] `make check` yeşil

## Notes for the reviewer (Opus)

- `Dialect.Columns` **yeniden tiplenmedi**, yeni bir anahtar eklendi. Bir
  `Dialect` `catalog_imports.file_json`'a bütün olarak serileşiyor; `Columns`'ın
  şeklini değiştirmek saklı her satırın `json.Unmarshal`'ını düşürürdü ve
  `Studio.List` okunamayan satırı bilerek yutuyor — belirti, her operatörün
  katalog ekranının sessizce boşalması olurdu.
- Dil neden `Field` içine gömülmedi: `Field` bir düzine yerde `switch` ile
  eşleniyor ve hepsi tanımadığını yok sayan bir `default` ile bitiyor. Bileşik
  bir `Field` değeri hepsinden sessizce düşerdi.
- `DraftVersion`'da dil neden son ek: dillerden önce yazılmış her taslak, son
  eki olmayan anahtar altında duruyor. Başka her yerleşim operatörün onaylamış
  olduğu kopyayı yetim bırakır — okuma `draft: null` döner, export hiçbir şey
  yazmaz.
