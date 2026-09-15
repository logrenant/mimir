package catalog

import (
	"strings"
	"testing"
)

// vocabOf is the shorthand every test here needs: a document's own markup is
// what that document is allowed to be rendered back into.
func vocabOf(t *testing.T, htmlIn string) (Doc, Vocabulary) {
	t.Helper()
	d, err := ParseHTML(htmlIn)
	if err != nil {
		t.Fatalf("ParseHTML: %v", err)
	}
	v := NewVocabulary()
	v.Add(d)
	return d, v
}

// A description that survives one render is the floor. Anything below it and
// the export would rewrite cells nobody approved.
func TestRender_OfAParsedDescriptionIsStable(t *testing.T) {
	cases := []string{
		`<p>Merhaba dünya</p>`,
		`<h2>Özellikler</h2><ul><li>Pamuk</li><li>Yıkanabilir</li></ul>`,
		`<p>Bu <strong>çok</strong> iyi bir <em>ürün</em>.</p>`,
		`<div class="rte"><p style="text-align: center;">Ortalanmış</p></div>`,
		`<p>Detaylar <a href="https://example.com/x">burada</a>.</p>`,
		`<p><img src="https://cdn.example.com/a.jpg" alt="ürün"></p>`,
		`<blockquote>Alıntı</blockquote><p>Sonra</p>`,
		`<ul><li>bir</li><li>iki</li></ul><ul class="b"><li>üç</li></ul>`,
	}
	for _, in := range cases {
		d, v := vocabOf(t, in)
		once := Render(d.Blocks, v, d.URLs)

		d2, err := ParseHTML(once)
		if err != nil {
			t.Fatalf("re-parse %q: %v", once, err)
		}
		twice := Render(d2.Blocks, v, d.URLs)
		if once != twice {
			t.Errorf("render is not stable\n in:   %s\n once: %s\n twice:%s", in, once, twice)
		}
	}
}

// Consecutive list items must share one list. Getting this wrong turns a
// three-item list into three one-item lists, which is a visible change to a
// page the operator did not ask us to change.
func TestRender_KeepsSiblingItemsInOneList(t *testing.T) {
	d, v := vocabOf(t, `<ul><li>bir</li><li>iki</li><li>üç</li></ul>`)
	got := Render(d.Blocks, v, d.URLs)
	if strings.Count(got, "<ul>") != 1 {
		t.Errorf("beklenen tek <ul>, alınan: %s", got)
	}
	if strings.Count(got, "<li>") != 3 {
		t.Errorf("beklenen üç <li>, alınan: %s", got)
	}
}

// The claim of this package, as a test: whatever a rewrite returns, the tags in
// the output are a subset of the tags in the input.
func TestRender_CannotEmitATagTheInputDidNotUse(t *testing.T) {
	d, v := vocabOf(t, `<p>Sade bir paragraf</p>`)

	// A rewrite that came back with markup the brand has never used.
	rewritten := []Block{
		{Kind: BlockParagraph, Text: "Yeni **kalın** metin", Envelope: d.Blocks[0].Envelope},
		{Kind: BlockHeading, Level: 2, Text: "Uydurma başlık",
			Envelope: []Elem{{Tag: "h2", Attrs: [][2]string{{"class", "flashy"}}}}},
	}
	got := Render(rewritten, v, d.URLs)

	for _, forbidden := range []string{"<h2", "<strong", "<b>", "class="} {
		if strings.Contains(got, forbidden) {
			t.Errorf("sözlük dışı %q çıktıya sızdı: %s", forbidden, got)
		}
	}
	// The words survive; only the markup is refused.
	if !strings.Contains(got, "Uydurma başlık") || !strings.Contains(got, "kalın") {
		t.Errorf("metin kayboldu: %s", got)
	}
}

// A brand that writes <b> gets <b> back. The closed syntax says "bold" without
// saying which tag the store spells it with.
func TestRender_UsesTheBrandsOwnMarkTag(t *testing.T) {
	d, v := vocabOf(t, `<p>Bu <b>önemli</b>.</p>`)
	got := Render(d.Blocks, v, d.URLs)
	if !strings.Contains(got, "<b>önemli</b>") {
		t.Errorf("markanın <b>'si <strong>'a çevrilmiş: %s", got)
	}
}

// An invented URL on a product page is a broken link the merchant ships to
// customers. The words stay; the link does not.
func TestRender_DropsALinkWhoseHrefWasNotInTheInput(t *testing.T) {
	d, v := vocabOf(t, `<p>Bilgi <a href="https://example.com/gercek">burada</a>.</p>`)

	rewritten := []Block{{
		Kind: BlockParagraph, Envelope: d.Blocks[0].Envelope,
		Text: "Bilgi [burada](https://uydurma.example/yok) ve [şurada](https://example.com/gercek).",
	}}
	got := Render(rewritten, v, d.URLs)

	if strings.Contains(got, "uydurma.example") {
		t.Errorf("uydurulmuş URL çıktıda: %s", got)
	}
	if !strings.Contains(got, "burada") {
		t.Errorf("bağlantı metni de kaybolmuş: %s", got)
	}
	if !strings.Contains(got, `href="https://example.com/gercek"`) {
		t.Errorf("gerçek bağlantı düşürülmüş: %s", got)
	}
}

func TestRender_DropsAnImageWhoseSourceWasInvented(t *testing.T) {
	d, v := vocabOf(t, `<p><img src="https://cdn.example.com/a.jpg" alt="a"></p>`)
	rewritten := []Block{{
		Kind: BlockParagraph, Envelope: d.Blocks[0].Envelope,
		Text: "![yeni](https://uydurma.example/b.jpg)",
	}}
	if got := Render(rewritten, v, d.URLs); strings.Contains(got, "uydurma") {
		t.Errorf("uydurulmuş görsel çıktıda: %s", got)
	}
}

// An unknown wrapper is unwrapped, never dropped with its contents. Losing text
// because its container was unfamiliar is the one failure this layer exists to
// prevent.
func TestRender_UnwrapsAnUnknownWrapperWithoutLosingItsText(t *testing.T) {
	v := NewVocabulary()
	v.Tags["p"] = 1
	blocks := []Block{{
		Kind: BlockParagraph, Text: "korunmalı",
		Envelope: []Elem{{Tag: "section"}, {Tag: "p"}},
	}}
	got := Render(blocks, v, nil)
	if strings.Contains(got, "<section") {
		t.Errorf("bilinmeyen sarmalayıcı çıktıda: %s", got)
	}
	if got != "<p>korunmalı</p>" {
		t.Errorf("beklenen <p>korunmalı</p>, alınan %s", got)
	}
}

func TestVocabulary_RecordsClassesAndStyleProperties(t *testing.T) {
	_, v := vocabOf(t, `<div class="rte urun"><p style="color: red; font-weight: bold">x</p></div>`)
	for _, c := range []string{"rte", "urun"} {
		if v.Classes[c] == 0 {
			t.Errorf("%q sınıfı sözlükte yok", c)
		}
	}
	for _, s := range []string{"color", "font-weight"} {
		if v.Styles[s] == 0 {
			t.Errorf("%q stili sözlükte yok", s)
		}
	}
	if v.Styles["display"] != 0 {
		t.Error("kullanılmayan stil sözlükte görünüyor")
	}
}

func TestRender_DropsAStylePropertyTheBrandDoesNotUse(t *testing.T) {
	d, v := vocabOf(t, `<p style="color: red">x</p>`)
	rewritten := []Block{{
		Kind: BlockParagraph, Text: "y",
		Envelope: []Elem{{Tag: "p", Attrs: [][2]string{{"style", "color: blue; display: none"}}}},
	}}
	got := Render(rewritten, v, d.URLs)
	if strings.Contains(got, "display") {
		t.Errorf("sözlük dışı stil geçti: %s", got)
	}
	if !strings.Contains(got, "color: blue") {
		t.Errorf("sözlükteki stil düşürüldü: %s", got)
	}
}

func TestParseHTML_EmptyDescriptionIsNotAnError(t *testing.T) {
	d, err := ParseHTML("   ")
	if err != nil {
		t.Fatalf("boş açıklama hata verdi: %v", err)
	}
	if len(d.Blocks) != 0 {
		t.Errorf("beklenen 0 blok, alınan %d", len(d.Blocks))
	}
}

func TestInlineSyntax_EscapesRoundTripThroughTheModel(t *testing.T) {
	d, v := vocabOf(t, `<p>Ürün kodu ABC_123 ve 5*5 ölçü [özel]</p>`)
	got := Render(d.Blocks, v, d.URLs)
	for _, want := range []string{"ABC_123", "5*5", "[özel]"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q kaçış turunda bozuldu: %s", want, got)
		}
	}
}

// Unwrapping an unknown element keeps its text, which is right for a <section>
// and wrong for a <script>: the body of a script is code, and letting it
// through as text puts `alert(1)` on the product page as a visible sentence.
func TestParseHTML_DoesNotTreatScriptOrStyleContentAsProse(t *testing.T) {
	d, v := vocabOf(t, `<p>gerçek</p><script>alert(1)</script><style>.x{color:red}</style>`)
	got := Render(d.Blocks, v, d.URLs)

	for _, leaked := range []string{"alert(1)", "color:red", "<script", "<style"} {
		if strings.Contains(got, leaked) {
			t.Errorf("%q çıktıya sızdı: %s", leaked, got)
		}
	}
	if !strings.Contains(got, "gerçek") {
		t.Errorf("meşru metin kayboldu: %s", got)
	}
}

func TestParseHTML_DropsAScriptNestedInsideAParagraph(t *testing.T) {
	d, v := vocabOf(t, `<p>önce<script>alert(1)</script>sonra</p>`)
	got := Render(d.Blocks, v, d.URLs)
	if strings.Contains(got, "alert") {
		t.Errorf("satır içi script metni sızdı: %s", got)
	}
	if !strings.Contains(got, "önce") || !strings.Contains(got, "sonra") {
		t.Errorf("çevresindeki metin kayboldu: %s", got)
	}
}
