package catalog

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/logrenant/mimir/internal/config"
)

var updateGolden = flag.Bool("update", false, "rewrite golden files")

func samplePromptInput(t *testing.T) (Product, BrandKit, Findings, Doc) {
	t.Helper()
	f, _ := parseFixture(t, "ikas-fields.csv")
	products := Products("imp_1", f)
	p := products[0]

	kit := BrandKit{
		Structure: Structure{MedianBlocks: 3, MedianChars: 240, HeadingLevels: []int{3}},
		Voice: Voice{
			Address: "siz", Tone: "sakin, bilgilendirici",
			Patterns: []string{"kullanım talimatını son cümlede verir"},
			Banned:   []string{"mucizevi", "eşsiz"},
			Lexicon:  []string{"nemlendirici", "cilt bariyeri"},
		},
	}
	findings := Findings{
		Summary:   "Rakipler bu kategoride hacim ve cilt tipini başlıkta veriyor.",
		KeyPoints: []string{"hacim başlıkta geçiyor", "cilt tipi ilk cümlede"},
		Gaps:      []string{"bir kaynak okunamadı"},
	}
	doc, err := ParseHTML(p.Original.DescriptionHTML)
	if err != nil {
		t.Fatalf("ParseHTML: %v", err)
	}
	return p, kit, findings, doc
}

// The prompt is pure and deterministic, so it is pinned the way internal/refine
// pins its five (SD-8).
//
// The source-language half of this golden is the load-bearing one. Every
// language clause sits behind `lang != LangSource`, and if the source prompt
// ever drifts, CatalogContentVersion has to be bumped — which orphans every
// draft an operator has already approved. This test is how that decision stops
// being accidental.
func TestBuildRewritePrompt_Golden(t *testing.T) {
	p, kit, findings, doc := samplePromptInput(t)
	cfg := config.Load()
	fields := []Field{FieldTitle, FieldDescriptionHTML, FieldSEOTitle, FieldSEODescription, FieldTags}
	skill := "## Ton\n\n- Ünlem kullanma."

	type pair struct {
		System string `json:"system"`
		User   string `json:"user"`
	}
	out := map[string]pair{}
	for _, lang := range Langs() {
		system, user := buildRewritePrompt(p, kit, findings, doc, fields, lang, skill, cfg)
		key := string(lang)
		if key == "" {
			key = "source"
		}
		out[key] = pair{system, user}
	}

	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')

	golden := filepath.Join("testdata", "rewrite_prompt.golden")
	if *updateGolden {
		if err := os.WriteFile(golden, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden %s (run `go test -run TestBuildRewritePrompt_Golden -update ./internal/catalog`): %v", golden, err)
	}
	if string(want) != string(b) {
		t.Errorf("rewrite prompt drifted from golden.\n--- got ---\n%s\n--- want ---\n%s", b, want)
	}
}

// A model that is not told the typography rules produces copy the gate then
// rejects, and the operator pays for both calls.
func TestBuildRewritePrompt_TellsTheModelWhatTheGateWillCheck(t *testing.T) {
	p, kit, findings, doc := samplePromptInput(t)
	fields := []Field{FieldTitle, FieldDescriptionHTML}

	ar, _ := buildRewritePrompt(p, kit, findings, doc, fields, LangAR, "", config.Load())
	for _, want := range []string{"الفصحى", "،", "؟", "Western (0-9)", "tatweel", "tashkeel"} {
		if !strings.Contains(ar, want) {
			t.Errorf("Arapça yönergesinde %q yok", want)
		}
	}

	en, _ := buildRewritePrompt(p, kit, findings, doc, fields, LangEN, "", config.Load())
	if !strings.Contains(en, "US spelling") {
		t.Error("İngilizce yönergesinde yazım kararı yok")
	}
	if strings.Contains(en, "الفصحى") {
		t.Error("İngilizce yönergesine Arapça kuralları sızmış")
	}
}

// The stored address axis is Turkish because it was read from Turkish text.
// It is rendered per language rather than re-derived, so the brand hash — and
// therefore every draft in the catalogue — does not move.
func TestAddressClause_RendersTheBrandsOwnAxisInEachLanguage(t *testing.T) {
	for _, tc := range []struct {
		address string
		lang    Lang
		want    string
	}{
		{"siz", LangSource, `"siz"`},
		{"siz", LangAR, "أنتم"},
		{"sen", LangAR, "أنتَ"},
		{"siz", LangEN, `"you"`},
		{"sen", LangEN, `"you"`}, // English carries no T–V distinction
		{"yok", LangAR, "impersonal"},
		{"yok", LangEN, "impersonal"},
	} {
		got := addressClause(tc.address, tc.lang)
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s/%s: beklenen %q, alınan %q", tc.address, tc.lang, tc.want, got)
		}
	}
	if got := addressClause("", LangAR); got != "" {
		t.Errorf("bilinmeyen hitap ekseni için cümle kuruldu: %q", got)
	}
}
