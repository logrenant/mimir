package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/llm"
)

// fakeCompleter counts calls, because "this pass costs no model call" is a
// claim that is only worth making if something counts.
type fakeCompleter struct {
	calls int
	resp  llm.Response
	err   error
}

func (f *fakeCompleter) CompleteWith(_ context.Context, _ llm.Class, _ llm.Selection, _ llm.Request) (llm.Response, error) {
	f.calls++
	if f.err != nil {
		return llm.Response{}, f.err
	}
	return f.resp, nil
}

func testStudio(t *testing.T, c Completer) *Studio {
	t.Helper()
	return New(config.Load(), nil, c)
}

// The vocabulary is the gate Render measures every rewrite against. A gate that
// needed a model call would be a gate that opens when the model is signed out,
// so the claim is asserted against a nil Completer: a broken claim panics here
// rather than quietly starting a subprocess.
func TestDeriveVocabulary_CostsNoModelCall(t *testing.T) {
	f, _ := parseFixture(t, "ikas.csv")
	products := Products("imp", f)

	v, st, err := DeriveVocabulary(f, products)
	if err != nil {
		t.Fatalf("DeriveVocabulary: %v", err)
	}
	for _, tag := range []string{"div", "p", "ul", "li", "b"} {
		if !v.Allows(tag) {
			t.Errorf("%q sözlükte yok", tag)
		}
	}
	if v.Classes["rte"] == 0 {
		t.Error("markanın rte sınıfı sayılmamış")
	}
	if st.Descriptions != 2 {
		t.Errorf("beklenen 2 açıklama, alınan %d", st.Descriptions)
	}
	if st.MedianChars == 0 || st.MedianBlocks == 0 {
		t.Errorf("yapı sayımları boş: %+v", st)
	}
}

// A rejected or unavailable voice costs the voice and nothing else. An import
// whose model call failed is still a usable import (SD-6).
func TestDeriveBrand_KeepsTheVocabularyWhenTheVoiceIsUnavailable(t *testing.T) {
	f, _ := parseFixture(t, "ikas.csv")
	products := Products("imp", f)

	fc := &fakeCompleter{err: llm.ErrProviderUnavailable}
	kit := testStudio(t, fc).DeriveBrand(context.Background(), f, products, llm.Selection{})

	if fc.calls != 1 {
		t.Errorf("beklenen 1 model çağrısı, alınan %d", fc.calls)
	}
	if kit.Vocab.IsEmpty() {
		t.Error("ses çıkarılamadı diye sözlük de kayboldu")
	}
	if kit.VoiceNote == "" {
		t.Error("boş ses profilinin sebebi yazılmamış")
	}
	if kit.Version == "" {
		t.Error("marka sürümü boş")
	}
}

func TestDeriveBrand_ReadsAValidatedVoice(t *testing.T) {
	f, _ := parseFixture(t, "ikas.csv")
	products := Products("imp", f)

	out, _ := json.Marshal(Voice{
		Address: "SİZ", Tone: "sade ve teknik",
		Patterns: []string{"kısa bir vaatle açar", "kısa bir vaatle açar"},
		Banned:   []string{"dijital dönüşüm"},
		Lexicon:  []string{"nemlendirici", "parabensiz"},
	})
	fc := &fakeCompleter{resp: llm.Response{Structured: out}}
	kit := testStudio(t, fc).DeriveBrand(context.Background(), f, products, llm.Selection{})

	if kit.Voice.Address != "siz" {
		t.Errorf("hitap normalize edilmedi: %q", kit.Voice.Address)
	}
	if len(kit.Voice.Patterns) != 1 {
		t.Errorf("tekrar eden kalıp ayıklanmadı: %v", kit.Voice.Patterns)
	}
	if kit.VoiceNote != "" {
		t.Errorf("başarılı ses için not yazılmış: %q", kit.VoiceNote)
	}
}

// A voice with no tone says nothing. Believing it would put an empty register
// in front of every rewrite in the catalog.
func TestDeriveBrand_RejectsAVoiceWithNoTone(t *testing.T) {
	f, _ := parseFixture(t, "ikas.csv")
	products := Products("imp", f)

	out, _ := json.Marshal(Voice{Address: "siz", Tone: "  "})
	fc := &fakeCompleter{resp: llm.Response{Structured: out}}
	kit := testStudio(t, fc).DeriveBrand(context.Background(), f, products, llm.Selection{})

	if kit.Voice.Tone != "" {
		t.Errorf("tonsuz ses kabul edildi: %+v", kit.Voice)
	}
	if !strings.Contains(kit.VoiceNote, "çıkarılamadı") {
		t.Errorf("ret sebebi yazılmamış: %q", kit.VoiceNote)
	}
}

// The brand hash is what a draft's cache key carries. Editing the voice must
// change it, and adding a product must not.
func TestBrandHash_ChangesWithTheVoiceAndNotWithTheCounts(t *testing.T) {
	base := BrandKit{
		Vocab:     Vocabulary{Tags: map[string]int{"p": 3}},
		Voice:     Voice{Address: "siz", Tone: "sade"},
		Structure: Structure{MedianBlocks: 2, MedianChars: 200},
	}
	grown := base
	grown.Vocab = Vocabulary{Tags: map[string]int{"p": 99}}
	grown.Structure.Descriptions = 500

	if base.hash("brand-v1") != grown.hash("brand-v1") {
		t.Error("sayımların artması taslakları geçersiz kıldı")
	}

	edited := base
	edited.Voice.Tone = "coşkulu"
	if base.hash("brand-v1") == edited.hash("brand-v1") {
		t.Error("ses düzenlendi ama hash değişmedi — eski metinler yeniden yazılmaz")
	}

	if base.hash("brand-v1") == base.hash("brand-v2") {
		t.Error("prompt sürümü hash'e girmiyor")
	}
}

// The first N products of an export are one collection, and a voice read from
// one collection is that collection's voice rather than the store's.
func TestSampleDescriptions_SpreadsAcrossTheCatalog(t *testing.T) {
	var products []Product
	for i := 0; i < 100; i++ {
		p := Product{Key: string(rune('a' + i%26))}
		p.Original.DescriptionHTML = "<p>" + strings.Repeat("uzun bir açıklama ", 5) + "</p>"
		products = append(products, p)
	}
	got := sampleDescriptions(products, 5)
	if len(got) != 5 {
		t.Fatalf("beklenen 5 örnek, alınan %d", len(got))
	}
	if got[0].Key == got[1].Key && got[1].Key == got[2].Key {
		t.Error("örnekler kataloğun bir ucundan alınmış")
	}
}

func TestSampleDescriptions_SkipsProductsWithNothingToRead(t *testing.T) {
	products := []Product{{Original: Content{DescriptionHTML: "<p>kısa</p>"}}}
	if got := sampleDescriptions(products, 5); len(got) != 0 {
		t.Errorf("okunacak metni olmayan ürün örneklenmiş: %d", len(got))
	}
}

func TestDeriveVocabulary_ReportsAFileWithNoMarkupAtAll(t *testing.T) {
	f := File{Header: []string{"ad"}, Rows: [][]string{{"x"}},
		Mapping: map[Field]string{FieldTitle: "ad"}}
	_, _, err := DeriveVocabulary(f, Products("imp", f))
	if err == nil {
		t.Fatal("biçimlendirmesi olmayan dosya sessizce kabul edildi")
	}
}

func TestDeriveBrand_SaysSoWhenThereIsNoMarkupToLearnFrom(t *testing.T) {
	f := File{Header: []string{"ad"}, Rows: [][]string{{"x"}},
		Mapping: map[Field]string{FieldTitle: "ad"}}
	fc := &fakeCompleter{err: errors.New("çağrılmamalıydı")}
	kit := testStudio(t, fc).DeriveBrand(context.Background(), f, Products("imp", f), llm.Selection{})

	if fc.calls != 0 {
		t.Error("okunacak biçimlendirme yokken model çağrıldı")
	}
	if kit.VoiceNote == "" {
		t.Error("boş sözlüğün sebebi yazılmamış")
	}
}

// slowCompleter blocks until the context it was handed is cancelled, which is
// what a signed-out or overloaded CLI looks like from here.
type slowCompleter struct{ waited time.Duration }

func (s *slowCompleter) CompleteWith(ctx context.Context, _ llm.Class, _ llm.Selection, _ llm.Request) (llm.Response, error) {
	start := time.Now()
	<-ctx.Done()
	s.waited = time.Since(start)
	return llm.Response{}, ctx.Err()
}

// Measured against a real CLI, this call ran for minutes inside an HTTP request
// an operator was watching after dropping a file. internal/llm's own budget is
// RefineTimeout, which was sized for distilling a page in the background; an
// upload needs its own, and the voice being optional is what makes giving up
// the better answer.
func TestDeriveBrand_GivesUpOnASlowProviderAndStillReturnsAnImport(t *testing.T) {
	f, _ := parseFixture(t, "ikas.csv")
	products := Products("imp", f)

	cfg := config.Load()
	cfg.CatalogVoiceTimeout = 40 * time.Millisecond
	sc := &slowCompleter{}

	start := time.Now()
	kit := New(cfg, nil, sc).DeriveBrand(context.Background(), f, products, llm.Selection{})
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Fatalf("içe aktarma modelin arkasında %v bekledi", elapsed)
	}
	if sc.waited == 0 {
		t.Error("sağlayıcıya hiç deadline geçmedi")
	}
	if kit.Vocab.IsEmpty() {
		t.Error("model zaman aşımına uğradı diye sözlük de kayboldu")
	}
	if kit.VoiceNote == "" {
		t.Error("operatöre sesin neden boş olduğu söylenmiyor")
	}
	if kit.Version == "" {
		t.Error("marka sürümü yazılmamış — taslak anahtarı kurulamaz")
	}
}
