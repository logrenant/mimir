package catalog

import (
	"strings"
	"unicode"

	"github.com/logrenant/mimir/internal/config"
)

// The language gate.
//
// A rewrite into a target language is measured here before any model is asked
// to judge it, and this file makes no model call at all. That is the same
// argument DeriveVocabulary makes: a gate that needed a model would be a gate
// that opens when the CLI is signed out, and "the Arabic is correct" is not a
// claim worth making only while a subscription is live.
//
// It does not try to be a grammar checker. It catches the things that are
// decidable from the bytes — the script the text is actually in, the
// punctuation the language actually uses, digits that were not in the source —
// and leaves judgement to the reviewer pass. Those two together are the answer
// to "no grammar mistakes"; neither is alone.

// LangRule names a check. It is a wire string: it lands in a draft's notes and
// in the reason a product failed, so it is never renamed.
type LangRule string

const (
	RuleNotInLanguage    LangRule = "not_in_language"
	RuleTurkishLeak      LangRule = "turkish_leak"
	RuleWrongScript      LangRule = "wrong_script"
	RuleUntranslatedSpan LangRule = "untranslated_span"
	RuleInventedNumber   LangRule = "invented_number"
	RuleEmpty            LangRule = "empty"

	RuleASCIIPunctuation LangRule = "ascii_punctuation"
	RuleArabicIndicDigit LangRule = "arabic_indic_digits"
	RuleTatweel          LangRule = "tatweel"
	RuleBidiControl      LangRule = "bidi_control"
	RuleTashkeel         LangRule = "tashkeel"
	RuleSpacing          LangRule = "spacing"
)

// LangFinding is one thing the gate noticed.
type LangFinding struct {
	Field Field    `json:"field"`
	Rule  LangRule `json:"rule"`
	// Note is the sentence an operator reads. Turkish, like every other note
	// this package produces.
	Note string `json:"note"`
	// At is a short excerpt of what tripped it, so the note points somewhere.
	At string `json:"at,omitempty"`
	// Fatal separates "this is not the language it claims to be" from "this is
	// stylistically off". Only the first refuses copy.
	Fatal bool `json:"fatal"`
}

// LangReport is everything the gate noticed about one draft.
type LangReport struct {
	Findings []LangFinding `json:"findings,omitempty"`
}

// OK reports whether nothing fatal was found.
func (r LangReport) OK() bool {
	for _, f := range r.Findings {
		if f.Fatal {
			return false
		}
	}
	return true
}

// Fatal is the findings that refuse the copy.
func (r LangReport) Fatal() []LangFinding {
	var out []LangFinding
	for _, f := range r.Findings {
		if f.Fatal {
			out = append(out, f)
		}
	}
	return out
}

// OnlySEO reports whether every fatal finding is on an SEO field.
//
// It matters because assemble already has a posture for those: a rejected SEO
// field costs that field and keeps the old one, while a rejected description
// costs the product. A title that is 40% Arabic is a bad title; a description
// that is 40% Arabic is not a description.
func (r LangReport) OnlySEO() bool {
	fatal := r.Fatal()
	if len(fatal) == 0 {
		return false
	}
	for _, f := range fatal {
		if f.Field != FieldSEOTitle && f.Field != FieldSEODescription {
			return false
		}
	}
	return true
}

// Notes renders the report as the sentences a draft carries.
func (r LangReport) Notes() []string {
	out := make([]string, 0, len(r.Findings))
	for _, f := range r.Findings {
		note := f.Note
		if f.At != "" {
			note += " (" + f.At + ")"
		}
		out = append(out, note)
	}
	return out
}

// checkedFields is what the gate reads, in the order an operator would.
var checkedFields = []Field{
	FieldTitle, FieldDescriptionHTML, FieldSEOTitle, FieldSEODescription, FieldTags,
}

// NormalizeForLang is the deterministic repair half, over a draft's plain-text
// fields.
//
// Everything fixable without judgement is fixed here rather than asked of a
// model: asking a model to move a comma costs a call, can fail, and can change
// something else while it is in there. What it fixes it also reports, because
// an operator who is not told we tidied their copy cannot tell whether the
// model or the tool wrote what they are looking at.
//
// The description is NOT handled here. It is markup by the time it reaches a
// Content, and a repair pass over a string of HTML rewrites the inside of a
// tag. NormalizeBlocks is its half, called where the blocks still exist.
func NormalizeForLang(c Content, lang Lang) (Content, []LangFinding) {
	if lang == LangSource {
		// The source language is the operator's own and this package does not
		// know what it is. Tidying it would be this tool editing copy nobody
		// asked it to edit, and it would put a diff in every cell that the
		// byte-identity claim says is copied.
		return c, nil
	}

	var found []LangFinding
	out := c
	for _, field := range checkedFields {
		if field == FieldDescriptionHTML {
			continue
		}
		v := c.Get(field)
		if v == "" {
			continue
		}
		fixed, rules := normalizeText(v, lang)
		if fixed != v {
			out.Set(field, fixed)
		}
		found = append(found, findingsFor(field, rules)...)
	}
	return out, found
}

// NormalizeBlocks is the same repair over a description, while it is still the
// list of text blocks a model returned and before Render turns it into the
// brand's own markup.
func NormalizeBlocks(blocks []Block, lang Lang) ([]Block, []LangFinding) {
	if lang == LangSource {
		return blocks, nil
	}
	out := make([]Block, len(blocks))
	copy(out, blocks)
	rules := ruleSet{}
	for i, b := range out {
		fixed, r := normalizeText(b.Text, lang)
		rules = rules.merge(r)
		out[i].Text = fixed
	}
	return out, findingsFor(FieldDescriptionHTML, rules)
}

// ruleSet records which repairs a normalisation made.
type ruleSet map[LangRule]bool

func (r ruleSet) merge(o ruleSet) ruleSet {
	if r == nil {
		r = ruleSet{}
	}
	for k := range o {
		r[k] = true
	}
	return r
}

// normalizeText is the whole repair, on one string of prose.
func normalizeText(s string, lang Lang) (string, ruleSet) {
	rules := ruleSet{}
	var b strings.Builder
	b.Grow(len(s))

	for _, r := range s {
		switch {
		case isBidiControl(r):
			// A copy-paste artefact. It is invisible in an editor and it
			// reorders the line in a storefront, which is the worst pair of
			// properties a character can have.
			rules[RuleBidiControl] = true
			continue
		case r == tatweel:
			rules[RuleTatweel] = true
			continue
		case r == ' ':
			b.WriteRune(' ')
			rules[RuleSpacing] = true
			continue
		case unicode.IsControl(r) && r != '\n':
			rules[RuleSpacing] = true
			continue
		}
		if lang == LangAR {
			if d, ok := westernDigit(r); ok {
				// One digit system per catalog. Gulf storefronts write Western
				// digits and the store's own data already carries them, so a
				// catalog that mixes the two is the failure — pick the one the
				// source uses and hold it.
				b.WriteRune(d)
				rules[RuleArabicIndicDigit] = true
				continue
			}
		}
		b.WriteRune(r)
	}

	s = b.String()
	if lang == LangAR {
		var fixed string
		fixed, rules = arabicPunctuation(s, rules)
		s = fixed
	}
	s, rules = tidySpacing(s, lang, rules)
	return s, rules
}

// arabicPunctuation swaps ASCII marks for Arabic ones, but only where the text
// around them is actually Arabic.
//
// The guard is the point. A product listing carries "SPF 50, 100 ml" and model
// numbers with commas in them, and turning every comma in the field into a "،"
// would corrupt exactly the strings that must survive a translation unchanged.
func arabicPunctuation(s string, rules ruleSet) (string, ruleSet) {
	rs := []rune(s)
	for i, r := range rs {
		swap, ok := arabicMark[r]
		if !ok {
			continue
		}
		if !nearArabic(rs, i) {
			continue
		}
		rs[i] = swap
		rules[RuleASCIIPunctuation] = true
	}
	return string(rs), rules
}

// nearArabic reports whether the nearest letter on either side is Arabic. A
// mark between two Latin runs — inside a model number, or in a bracketed brand
// name — is left alone.
func nearArabic(rs []rune, at int) bool {
	before := nearestLetter(rs, at, -1)
	after := nearestLetter(rs, at, +1)
	if before == 0 && after == 0 {
		return false
	}
	if before != 0 && !isArabicLetter(before) {
		return false
	}
	if after != 0 && !isArabicLetter(after) {
		return false
	}
	return true
}

func nearestLetter(rs []rune, at, step int) rune {
	for i := at + step; i >= 0 && i < len(rs); i += step {
		if unicode.IsLetter(rs[i]) {
			return rs[i]
		}
		if rs[i] == '\n' {
			return 0
		}
	}
	return 0
}

// tidySpacing collapses runs of spaces and puts the space on the correct side
// of a punctuation mark.
func tidySpacing(s string, lang Lang, rules ruleSet) (string, ruleSet) {
	rs := []rune(s)
	out := make([]rune, 0, len(rs))
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if r == ' ' {
			// Drop a space before a mark that never takes one, and collapse a
			// run of spaces to one.
			if j := nextNonSpace(rs, i); j < len(rs) && closesTight(rs[j], lang) {
				rules[RuleSpacing] = true
				continue
			}
			if len(out) > 0 && out[len(out)-1] == ' ' {
				rules[RuleSpacing] = true
				continue
			}
			if len(out) == 0 {
				rules[RuleSpacing] = true
				continue
			}
		}
		out = append(out, r)
	}
	trimmed := strings.TrimRight(string(out), " ")
	if trimmed != string(out) {
		rules[RuleSpacing] = true
	}
	return trimmed, rules
}

func nextNonSpace(rs []rune, from int) int {
	i := from
	for i < len(rs) && rs[i] == ' ' {
		i++
	}
	return i
}

func closesTight(r rune, lang Lang) bool {
	switch r {
	case '.', ',', ';', ':', '!', '?':
		return true
	}
	if lang == LangAR {
		switch r {
		case arabicComma, arabicSemicolon, arabicQuestion:
			return true
		}
	}
	return false
}

func findingsFor(field Field, rules ruleSet) []LangFinding {
	var out []LangFinding
	for _, r := range []LangRule{
		RuleBidiControl, RuleTatweel, RuleArabicIndicDigit, RuleASCIIPunctuation, RuleSpacing,
	} {
		if !rules[r] {
			continue
		}
		out = append(out, LangFinding{
			Field: field, Rule: r, Note: repairNote(r), Fatal: false,
		})
	}
	return out
}

func repairNote(r LangRule) string {
	switch r {
	case RuleBidiControl:
		return "Görünmez yön denetim karakterleri temizlendi."
	case RuleTatweel:
		return "Arapça metinden kaşide (tatweel) temizlendi."
	case RuleArabicIndicDigit:
		return "Arap-Hint rakamları, mağazanın kendi verisiyle aynı olsun diye Batı rakamlarına çevrildi."
	case RuleASCIIPunctuation:
		return "Arapça cümlelerdeki ASCII virgül/soru işareti Arapça karşılıklarıyla (،؟؛) değiştirildi."
	case RuleSpacing:
		return "Fazla boşluklar ve noktalamadan önceki boşluk düzeltildi."
	}
	return string(r)
}

// CheckLanguage is the gate every stored draft in a target language passes,
// whoever wrote it.
//
// source is the copy this draft was written from. It is a parameter because
// the single most valuable check here is not about language at all: a number in
// the draft that is in no field of the source is invented, and an invented
// measurement on a product page is the failure this whole package is built to
// avoid. That one is decidable without a model, so it is decided here.
//
// want is the set of fields this draft was asked to cover. It decides which
// fields being *empty* is a failure: a file whose Arabic surface is one
// description column has no Arabic title, and demanding one would refuse every
// draft it could ever produce. A field outside want is still checked when it
// has content — it is in the draft, so it has to be valid — it is just not
// required to be there. An empty want means every writable field is required,
// which is what the operator's own save path asks for.
//
// The description is read as text, never as markup: a check over HTML would
// count tag names as untranslated English and reject every draft.
func CheckLanguage(c, source Content, lang Lang, cfg config.Config, want []Field) LangReport {
	var rep LangReport
	if lang == LangSource {
		// Nothing to check. This package does not know what language the
		// operator's file is in, and a gate that guessed would reject the
		// source-language path that has worked since task-87.
		return rep
	}

	sourceNumbers := digitRuns(plainAll(source))

	required := map[Field]bool{}
	for _, f := range want {
		required[f] = true
	}
	if len(want) == 0 {
		required[FieldTitle] = true
		required[FieldDescriptionHTML] = true
	}

	for _, field := range checkedFields {
		text := fieldText(c, field)
		if strings.TrimSpace(text) == "" {
			if required[field] {
				rep.Findings = append(rep.Findings, LangFinding{
					Field: field, Rule: RuleEmpty, Fatal: true,
					Note: "Bu alan boş kaldı.",
				})
			}
			continue
		}
		rep.Findings = append(rep.Findings, checkField(field, text, sourceNumbers, lang, cfg)...)
	}
	return rep
}

func checkField(
	field Field, text string, sourceNumbers map[string]bool, lang Lang, cfg config.Config,
) []LangFinding {
	var out []LangFinding
	add := func(rule LangRule, fatal bool, note, at string) {
		out = append(out, LangFinding{Field: field, Rule: rule, Fatal: fatal, Note: note, At: at})
	}

	// Turkish leaking into either target is the same failure with the same
	// cause: the model copied instead of writing.
	for _, r := range text {
		if isTurkishLetter(r) {
			add(RuleTurkishLeak, true,
				"Kaynak dilin harfleri (ç, ğ, ı, İ, ş) hedef metinde kaldı — bir kısmı çevrilmemiş.",
				excerpt(text))
			break
		}
	}

	switch lang {
	case LangAR:
		share, letters := scriptShare(text, isArabicLetter)
		if letters > 0 && share < cfg.CatalogArabicMinLetterRatio {
			add(RuleNotInLanguage, true,
				"Metnin harflerinin çoğu Arapça değil.", excerpt(text))
		}
		if n, at := longestLatinRun(text); n > cfg.CatalogLatinRunMaxRunes {
			add(RuleUntranslatedSpan, true,
				"Arapça metnin içinde çevrilmemiş uzun bir Latin harfli bölüm var.", excerpt(at))
		}
		if marks := countIf(text, isTashkeel); letters > 0 {
			if float64(marks)/float64(letters) > cfg.CatalogTashkeelMaxRatio {
				add(RuleTashkeel, false,
					"Metin yoğun harekelenmiş. Ticari Arapça içerik harekesiz yazılır.", "")
			}
		}
		for _, r := range text {
			if _, ok := arabicMark[r]; ok && nearArabicIn(text, r) {
				add(RuleASCIIPunctuation, false,
					"Arapça cümlede ASCII noktalama kaldı (، ؟ ؛ bekleniyordu).", excerpt(text))
				break
			}
		}

	case LangEN:
		if share, letters := scriptShare(text, isLatinLetter); letters > 0 &&
			share < cfg.CatalogEnglishMinLetterRatio {
			add(RuleNotInLanguage, true,
				"Metnin harflerinin çoğu Latin alfabesinde değil.", excerpt(text))
		}
		for _, r := range text {
			if isArabicLetter(r) {
				add(RuleWrongScript, true,
					"İngilizce metnin içinde Arapça harfler var.", excerpt(text))
				break
			}
		}
	}

	// Every number the draft states must already be in the source. A rewrite
	// may drop a fact; it may not make one up.
	for n := range digitRuns(text) {
		if !sourceNumbers[n] {
			add(RuleInventedNumber, true,
				"Kaynakta geçmeyen bir sayı yazılmış: "+n, excerpt(text))
			break
		}
	}
	return out
}

// nearArabicIn is nearArabic over a whole string, for the check half — the
// normaliser already fixed what it could, so anything left here sat between
// Latin runs or arrived after normalisation.
func nearArabicIn(s string, mark rune) bool {
	rs := []rune(s)
	for i, r := range rs {
		if r == mark && nearArabic(rs, i) {
			return true
		}
	}
	return false
}

func countIf(s string, want func(rune) bool) int {
	var n int
	for _, r := range s {
		if want(r) {
			n++
		}
	}
	return n
}

// fieldText is a field as prose. The description is flattened through its
// blocks so the gate reads what a shopper reads and not what a browser parses.
func fieldText(c Content, field Field) string {
	if field != FieldDescriptionHTML {
		return c.Get(field)
	}
	return htmlText(c.DescriptionHTML)
}

// htmlText flattens a description to the text inside it, with the inline link
// syntax's URLs removed — a URL is not prose and counting it as untranslated
// English would reject every draft that kept a link.
func htmlText(html string) string {
	if strings.TrimSpace(html) == "" {
		return ""
	}
	doc, err := ParseHTML(html)
	if err != nil {
		return ""
	}
	parts := make([]string, 0, len(doc.Blocks))
	for _, b := range doc.Blocks {
		parts = append(parts, stripInlineURLs(b.Text))
	}
	return strings.Join(parts, "\n")
}

// stripInlineURLs drops the "(url)" half of the closed inline syntax and keeps
// the link text.
func stripInlineURLs(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if rs[i] != ']' || i+1 >= len(rs) || rs[i+1] != '(' {
			b.WriteRune(rs[i])
			continue
		}
		j := i + 2
		for j < len(rs) && rs[j] != ')' {
			j++
		}
		if j >= len(rs) {
			b.WriteRune(rs[i])
			continue
		}
		b.WriteRune(']')
		i = j
	}
	return b.String()
}

// plainAll is every field of a Content as one string, for the number check.
func plainAll(c Content) string {
	parts := make([]string, 0, len(checkedFields))
	for _, f := range checkedFields {
		parts = append(parts, fieldText(c, f))
	}
	return strings.Join(parts, "\n")
}
