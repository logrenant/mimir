package catalog

import (
	"strings"
	"testing"

	"github.com/logrenant/mimir/internal/llm"

	"github.com/logrenant/mimir/internal/config"
)

// Real sentences, not synthetic strings. A gate tuned against "ا" repeated
// forty times is a gate tuned against nothing a model would ever produce.
const (
	goodArabic = "مرطب مكثف للبشرة الجافة، يمنح ترطيبًا يدوم طوال اليوم. " +
		"يحتوي على 50 مل ومناسب للاستخدام اليومي."
	goodEnglish = "An intensive moisturizer for dry skin that keeps working all day. " +
		"The 50 ml jar is suited to daily use."
)

func TestNormalizeForLang_LeavesTheSourceLanguageAlone(t *testing.T) {
	in := Content{Title: "Nemlendirici  Krem ,  50 ml"}
	out, found := NormalizeForLang(in, LangSource)
	if out != in {
		t.Errorf("kaynak dil metni değiştirildi: %q → %q", in.Title, out.Title)
	}
	if len(found) != 0 {
		t.Errorf("kaynak dil için bulgu üretildi: %+v", found)
	}
}

func TestNormalizeForLang_RepairsWhatIsFixableWithoutJudgement(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
		rule LangRule
	}{
		{
			name: "ASCII virgül Arapça cümlede",
			in:   "مرطب مكثف, يدوم طوال اليوم",
			want: "مرطب مكثف، يدوم طوال اليوم",
			rule: RuleASCIIPunctuation,
		},
		{
			name: "kaşide",
			in:   "مرطـــب مكثف",
			want: "مرطب مكثف",
			rule: RuleTatweel,
		},
		{
			name: "yön denetim karakteri",
			// U+200F RIGHT-TO-LEFT MARK and U+202C POP DIRECTIONAL FORMATTING,
			// escaped because they are invisible in a source file — which is
			// the whole reason they are worth stripping from a storefront.
			in:   "\u200fمرطب مكثف\u202c",
			want: "مرطب مكثف",
			rule: RuleBidiControl,
		},
		{
			name: "Arap-Hint rakamı",
			in:   "٥٠ مل",
			want: "50 مل",
			rule: RuleArabicIndicDigit,
		},
		{
			name: "noktalamadan önce boşluk",
			in:   "مرطب مكثف ، يدوم",
			want: "مرطب مكثف، يدوم",
			rule: RuleSpacing,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, found := NormalizeForLang(Content{Title: tc.in}, LangAR)
			if out.Title != tc.want {
				t.Errorf("beklenen %q, alınan %q", tc.want, out.Title)
			}
			var saw bool
			for _, f := range found {
				if f.Rule == tc.rule {
					saw = true
				}
				if f.Fatal {
					t.Errorf("onarım fatal olarak bildirildi: %+v", f)
				}
			}
			if !saw {
				t.Errorf("%s onarımı bildirilmedi: %+v", tc.rule, found)
			}
		})
	}
}

// A product listing carries model numbers and measurements whose punctuation
// must survive a translation exactly. Swapping every comma in the field would
// corrupt precisely the strings that may not change.
func TestNormalizeForLang_LeavesPunctuationInsideLatinRunsAlone(t *testing.T) {
	in := "مرطب SPF 50, ref. A-12,5 مل"
	out, _ := NormalizeForLang(Content{Title: in}, LangAR)
	if !strings.Contains(out.Title, "A-12,5") {
		t.Errorf("Latin harfler arasındaki virgül değiştirildi: %q", out.Title)
	}
}

func TestCheckLanguage_PassesCopyThatIsActuallyInTheLanguage(t *testing.T) {
	cfg := config.Load()
	source := Content{Title: "Nemlendirici Krem", DescriptionHTML: "<p>50 ml yoğun nemlendirici.</p>"}

	for _, tc := range []struct {
		lang Lang
		text string
	}{
		{LangAR, goodArabic},
		{LangEN, goodEnglish},
	} {
		draft := Content{Title: tc.text, DescriptionHTML: "<p>" + tc.text + "</p>"}
		rep := CheckLanguage(draft, source, tc.lang, cfg, nil)
		if !rep.OK() {
			t.Errorf("%s: doğru yazılmış metin reddedildi: %+v", tc.lang, rep.Fatal())
		}
	}
}

func TestCheckLanguage_RefusesWhatIsNotTheLanguageItClaimsToBe(t *testing.T) {
	cfg := config.Load()
	source := Content{
		Title:           "Nemlendirici Krem",
		DescriptionHTML: "<p>Kuru ciltler için 50 ml yoğun nemlendirici.</p>",
	}

	for _, tc := range []struct {
		name string
		lang Lang
		text string
		rule LangRule
	}{
		{
			name: "çevrilmemiş Türkçe, Arapça olarak",
			lang: LangAR,
			text: "Kuru ciltler için yoğun nemlendirici krem.",
			rule: RuleTurkishLeak,
		},
		{
			name: "Arapçanın içinde çevrilmemiş İngilizce paragraf",
			lang: LangAR,
			text: "مرطب. An intensive moisturizer for very dry skin, all day long.",
			rule: RuleUntranslatedSpan,
		},
		{
			name: "İngilizce isteniyor, Arapça gelmiş",
			lang: LangEN,
			text: goodArabic,
			rule: RuleWrongScript,
		},
		{
			name: "Türkçe harf İngilizcede",
			lang: LangEN,
			text: "An intensive moisturizer for yağlı ciltler and dry skin.",
			rule: RuleTurkishLeak,
		},
		{
			name: "kaynakta olmayan sayı",
			lang: LangEN,
			text: "An intensive moisturizer. The 500 ml jar lasts for 24 months.",
			rule: RuleInventedNumber,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			draft := Content{Title: "x", DescriptionHTML: "<p>" + tc.text + "</p>"}
			rep := CheckLanguage(draft, source, tc.lang, cfg, nil)
			if rep.OK() {
				t.Fatalf("kabul edildi: %+v", rep.Findings)
			}
			var saw bool
			for _, f := range rep.Fatal() {
				if f.Rule == tc.rule {
					saw = true
				}
			}
			if !saw {
				t.Errorf("beklenen %s kuralı yakalanmadı: %+v", tc.rule, rep.Fatal())
			}
		})
	}
}

// The gate reads what a shopper reads. A check over markup would count every
// tag name as untranslated English and reject every draft there is.
func TestCheckLanguage_ReadsTheDescriptionAsProseNotAsMarkup(t *testing.T) {
	cfg := config.Load()
	source := Content{DescriptionHTML: "<p>50 ml yoğun nemlendirici.</p>", Title: "Krem"}
	draft := Content{
		Title: goodArabic,
		DescriptionHTML: `<div class="product-detail"><h3>` + goodArabic +
			`</h3><p><strong>` + goodArabic + `</strong></p></div>`,
	}
	rep := CheckLanguage(draft, source, LangAR, cfg, nil)
	if !rep.OK() {
		t.Errorf("işaretleme yüzünden reddedildi: %+v", rep.Fatal())
	}
}

// A link the product already had is not untranslated English.
func TestCheckLanguage_DoesNotCountAURLAsUntranslatedText(t *testing.T) {
	cfg := config.Load()
	source := Content{DescriptionHTML: `<p>50 ml. <a href="https://example.com/kullanim-kilavuzu-detayli">Kılavuz</a></p>`, Title: "Krem"}
	draft := Content{
		Title:           goodArabic,
		DescriptionHTML: `<p>` + goodArabic + ` <a href="https://example.com/kullanim-kilavuzu-detayli">الدليل</a></p>`,
	}
	if rep := CheckLanguage(draft, source, LangAR, cfg, nil); !rep.OK() {
		t.Errorf("bağlantı çevrilmemiş metin sayıldı: %+v", rep.Fatal())
	}
}

// A rejected SEO field costs that field; a rejected description costs the
// product. assemble already draws that line and the report has to be able to
// say which side a failure is on.
func TestLangReport_SeparatesAnSEOFailureFromADescriptionFailure(t *testing.T) {
	cfg := config.Load()
	source := Content{DescriptionHTML: "<p>50 ml krem.</p>", Title: "Krem"}

	seoOnly := Content{
		Title:           goodArabic,
		DescriptionHTML: "<p>" + goodArabic + "</p>",
		SEOTitle:        "Nemlendirici Krem",
	}
	rep := CheckLanguage(seoOnly, source, LangAR, cfg, nil)
	if rep.OK() || !rep.OnlySEO() {
		t.Errorf("yalnız SEO alanı düşmedi: %+v", rep.Fatal())
	}

	bodyToo := seoOnly
	bodyToo.DescriptionHTML = "<p>Kuru ciltler için krem.</p>"
	if rep := CheckLanguage(bodyToo, source, LangAR, cfg, nil); rep.OnlySEO() {
		t.Error("açıklama da düştüğü hâlde yalnız SEO denildi")
	}
}

func TestNormalizeBlocks_RepairsTheDescriptionWhileItIsStillText(t *testing.T) {
	blocks := []Block{
		{Kind: BlockParagraph, Text: "مرطـب مكثف, ٥٠ مل"},
		{Kind: BlockParagraph, Text: "يدوم طوال اليوم"},
	}
	out, found := NormalizeBlocks(blocks, LangAR)
	if out[0].Text != "مرطب مكثف، 50 مل" {
		t.Errorf("blok onarılmadı: %q", out[0].Text)
	}
	if len(found) == 0 {
		t.Error("onarım bildirilmedi")
	}
	for _, f := range found {
		if f.Field != FieldDescriptionHTML {
			t.Errorf("bulgu yanlış alana bağlandı: %+v", f)
		}
	}
}

// The source-language path must be untouched, byte for byte. Every stored
// draft is one, and this is the assertion that says so.
func TestRenderLang_IsByteIdenticalToRenderForALeftToRightLanguage(t *testing.T) {
	blocks := []Block{{
		Kind: BlockParagraph, Text: "Kuru ciltler için yoğun nemlendirici.",
		Envelope: []Elem{{Tag: "p"}},
	}}
	v := Vocabulary{Tags: map[string]int{"p": 1}, Attrs: map[string]int{},
		Classes: map[string]int{}, Styles: map[string]int{}}

	for _, lang := range []Lang{LangSource, LangEN} {
		if got, want := RenderLang(blocks, v, nil, lang), Render(blocks, v, nil); got != want {
			t.Errorf("%s: %q != %q", lang, got, want)
		}
	}
}

// Arabic with no paragraph direction renders with its punctuation on the wrong
// side. The vocabulary would drop a dir attribute — a Turkish store never wrote
// one — so the direction is this package's own, on one element it writes itself.
func TestRenderLang_WrapsRightToLeftEvenWhenTheBrandNeverWroteADiv(t *testing.T) {
	blocks := []Block{{
		Kind: BlockParagraph, Text: "مرطب مكثف للبشرة الجافة.",
		Envelope: []Elem{{Tag: "p"}},
	}}
	// A brand vocabulary with no div and no dir — which is every Turkish store.
	v := Vocabulary{Tags: map[string]int{"p": 1}, Attrs: map[string]int{},
		Classes: map[string]int{}, Styles: map[string]int{}}

	got := RenderLang(blocks, v, nil, LangAR)
	if !strings.HasPrefix(got, `<div dir="rtl" lang="ar">`) {
		t.Fatalf("yön sarmalayıcısı yazılmadı: %q", got)
	}
	if !strings.Contains(got, "<p>مرطب مكثف للبشرة الجافة.</p>") {
		t.Errorf("gövde markanın kendi işaretlemesiyle çıkmadı: %q", got)
	}
	// And it carries nothing else: no class, no style, no opinion.
	if strings.Contains(got, "class=") || strings.Contains(got, "style=") {
		t.Errorf("sarmalayıcı görsel bir karar taşıyor: %q", got)
	}
}

// The operator's first edit must not silently strip the direction from copy
// that already shipped with it.
func TestRenderLang_ReWrappingAnAlreadyWrappedDraftDoesNotNestOrLoseIt(t *testing.T) {
	// The brand's own HTML uses divs, which most stores' does. That is the
	// dangerous case: sanitizeEnvelope keeps the wrapper because div is in the
	// vocabulary, sanitizeAttrs strips its dir and lang because those are not,
	// and a naive re-render would wrap the stripped div in a fresh one — a
	// layer deeper on every single save.
	v := Vocabulary{Tags: map[string]int{"p": 1, "div": 1}, Attrs: map[string]int{},
		Classes: map[string]int{}, Styles: map[string]int{}}
	blocks := []Block{{
		Kind: BlockParagraph, Text: "مرطب مكثف للبشرة الجافة.",
		Envelope: []Elem{{Tag: "p"}},
	}}

	once := RenderLang(blocks, v, nil, LangAR)

	// What SaveDraft does: parse the stored HTML back and render it again.
	doc, err := ParseHTML(once)
	if err != nil {
		t.Fatalf("ParseHTML: %v", err)
	}
	twice := RenderLang(stripDirectionWrapper(doc.Blocks), v, nil, LangAR)

	if twice != once {
		t.Errorf("tekrar işlemek sonucu değiştirdi:\n  bir kez: %q\n  iki kez: %q", once, twice)
	}
	if strings.Count(twice, `dir="rtl"`) != 1 {
		t.Errorf("yön sarmalayıcısı iç içe geçti: %q", twice)
	}
}

// If our own output were ever re-imported, DeriveVocabulary must not learn the
// wrapper from it and permanently widen the brand's own vocabulary with a tag
// the brand never wrote.
func TestDeriveVocabulary_DoesNotLearnFromATargetLanguageColumn(t *testing.T) {
	f, _ := parseFixture(t, "ikas-fields.csv")
	products := Products("imp_1", f)
	v, _, err := DeriveVocabulary(f, products)
	if err != nil {
		t.Fatalf("DeriveVocabulary: %v", err)
	}

	if v.Allows("div") {
		t.Error("sözlük Arapça sütundan div öğrendi")
	}
	if v.Attrs["dir"] > 0 || v.Attrs["lang"] > 0 {
		t.Errorf("sözlük yön niteliklerini öğrendi: %+v", v.Attrs)
	}
}

// The source language's key must be exactly what it was before languages
// existed. Every draft an operator has approved is stored under one.
func TestDraftVersion_TheSourceLanguageKeyIsUnchangedAndTargetsAreSuffixed(t *testing.T) {
	s := New(config.Load(), nil, nil)
	kit := BrandKit{Version: "brand-v1:aaaa"}
	sel := llm.Selection{Provider: "claude", Model: "opus-5"}

	src := s.DraftVersion(kit, sel, "product-content:11aa22bb", LangSource)
	// Spelled out rather than pattern-matched: the model selection legitimately
	// contains a slash, so only the exact string says what this test means.
	const want = "content-v1@claude/opus-5:brand-v1:aaaa#product-content:11aa22bb"
	if src != want {
		t.Errorf("kaynak dil anahtarı değişti:\n  beklenen: %q\n  alınan:   %q", want, src)
	}

	ar := s.DraftVersion(kit, sel, "product-content:11aa22bb|product-content-ar:33cc", LangAR)
	if !strings.HasPrefix(ar, src) {
		t.Errorf("hedef dil anahtarı bir son ek değil:\n  kaynak: %q\n  ar:     %q", src, ar)
	}
	if !strings.HasSuffix(ar, "/ar+"+config.Load().CatalogReviewVersion) {
		t.Errorf("dil ve gözden geçirme sürümü anahtarda yok: %q", ar)
	}

	// And the three languages cannot collide.
	seen := map[string]bool{}
	for _, l := range Langs() {
		v := s.DraftVersion(kit, sel, "skill:1", l)
		if seen[v] {
			t.Errorf("%s başka bir dille aynı anahtarı üretti: %q", l, v)
		}
		seen[v] = true
	}
}
