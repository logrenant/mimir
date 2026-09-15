package catalog

import (
	"fmt"
	"strings"
)

// Lang is a language a product's content can be carried in.
//
// It is a closed set and the values are wire strings — they land in an
// operator's saved column map, in a card's params and in a draft's cache key —
// so they are never renamed, only added to.
type Lang string

const (
	// LangSource is the import file's own language, whatever that happens to
	// be. It is the empty string deliberately: every mapping, every draft and
	// every version written before this type existed is a source-language one,
	// and the zero value has to keep meaning what those rows already mean.
	// Anything else orphans an operator's approved work.
	LangSource Lang = ""
	LangEN     Lang = "en"
	LangAR     Lang = "ar"
)

// Langs is every language, in the order a UI should offer them.
func Langs() []Lang { return []Lang{LangSource, LangEN, LangAR} }

// KnownLang reports whether a wire value names a language this binary carries.
func KnownLang(l Lang) bool {
	switch l {
	case LangSource, LangEN, LangAR:
		return true
	}
	return false
}

// RTL reports whether the language is written right to left.
//
// This is the one property of a language this package acts on by itself. A
// paragraph of Arabic with no direction renders with its punctuation on the
// wrong side and its numbers reordered, because the bidirectional algorithm
// resolves neutral characters against a paragraph direction that is not there.
func (l Lang) RTL() bool { return l == LangAR }

// Dir is the HTML direction value.
func (l Lang) Dir() string {
	if l.RTL() {
		return "rtl"
	}
	return "ltr"
}

// Tag is the value for an HTML lang attribute, empty for the source language —
// this package does not know what the operator's file is written in and will
// not guess.
func (l Lang) Tag() string {
	if l == LangSource {
		return ""
	}
	return string(l)
}

// Label is the operator-facing name.
func (l Lang) Label() string {
	switch l {
	case LangEN:
		return "İngilizce"
	case LangAR:
		return "Arapça"
	}
	return "Kaynak dil"
}

// LangField is one field in one language: the unit a column carries, a draft
// covers and a mapping names.
//
// It is a struct rather than a composite string everywhere inside this package
// on purpose. Field is matched by switch in a dozen places — Content.Get/Set,
// Writable, assemble, rowsFor — and every one of those switches ends in a
// default that ignores what it does not recognise. A composite Field value
// would fall through all of them silently, which is the worst failure this code
// can have: a field that is quietly not written.
type LangField struct {
	Field Field
	Lang  Lang
}

// String is the wire spelling: "description_html" for the source language and
// "description_html@ar" for a target.
//
// The source spelling is byte-identical to what every saved column map already
// holds, which is the whole reason the language is a suffix rather than a
// second map on the wire: an operator's stored mapping keeps parsing.
func (lf LangField) String() string {
	if lf.Lang == LangSource {
		return string(lf.Field)
	}
	return string(lf.Field) + "@" + string(lf.Lang)
}

// MarshalText / UnmarshalText make a LangField serialise as its wire spelling
// everywhere — in a slice, and as a map key. That matters here: the operator's
// field selection is stored inside the import's own JSON, and a selection
// written as ["title","description_html@ar"] is one a person can read in a
// database row and one an older binary parses the source-language half of.
func (lf LangField) MarshalText() ([]byte, error) { return []byte(lf.String()), nil }

func (lf *LangField) UnmarshalText(b []byte) error {
	got, ok := ParseLangField(string(b))
	if !ok {
		return fmt.Errorf("catalog: %q is not a field", string(b))
	}
	*lf = got
	return nil
}

// ParseLangField reads the wire spelling back. A value with no "@" is a
// source-language field, which is what every mapping written before languages
// existed is.
func ParseLangField(s string) (LangField, bool) {
	field, lang, found := strings.Cut(s, "@")
	lf := LangField{Field: Field(field), Lang: LangSource}
	if found {
		lf.Lang = Lang(lang)
	}
	if !knownField(lf.Field) || !KnownLang(lf.Lang) {
		return LangField{}, false
	}
	return lf, true
}

// LangFields is every field in every given language, fields in Fields() order
// and languages in the order given.
func LangFields(langs []Lang) []LangField {
	out := make([]LangField, 0, len(Fields())*len(langs))
	for _, l := range langs {
		for _, f := range Fields() {
			out = append(out, LangField{Field: f, Lang: l})
		}
	}
	return out
}

// Content reads the product's content in one language. A language the file does
// not carry reads as empty, which is the truth rather than a failure.
func (p Product) Content(l Lang) Content {
	if l == LangSource {
		return p.Original
	}
	return p.Translations[l]
}

// ColumnsFor resolves the file's effective field → header map for one language:
// the operator's own map when they have made one, and the detected profile's
// otherwise.
//
// **The operator's answer outranks the profile's.** SetDialect already says the
// converse — picking a profile clears a hand-made map, because "the one the
// operator just picked is the newer one" — and for a long time this function
// did not say it in the other direction: a map saved over a file a profile had
// matched was stored, re-read, and then ignored on every read, so the "bir sütun
// yanlış eşlendiyse buradan düzeltin" form silently did nothing. A profile is a
// guess about somebody else's export; the person looking at the file is not.
//
// It replaces the profile's columns for that language rather than merging over
// them, for the same reason SetDialect replaces rather than merges: a field the
// operator cleared has to come back *unmapped*, and a merge would quietly hand
// it back the profile's column. The undo is SetDialect with the same key, which
// re-binds the profile from the header and drops the map.
//
// A profile's TargetColumns come before its per-language ones, and only once a
// person has said which language they hold. IKAS's translations export is the
// case: its header is "İsim, Açıklama, …, Çevrilecek İsim, Çevrilecek Açıklama,
// …" and nothing in it records what "Çevrilecek" was translated into — the
// operator picked the language in the admin panel when they pressed export, and
// the file came back without that answer. So this package does not guess it.
func (f File) ColumnsFor(l Lang) map[Field]string {
	if l == LangSource {
		if len(f.Mapping) > 0 {
			return f.Mapping
		}
		return f.Dialect.Columns
	}
	if cols := f.Translations[l]; len(cols) > 0 {
		return cols
	}
	if l == f.TargetLang && len(f.Dialect.TargetColumns) > 0 {
		return f.Dialect.TargetColumns
	}
	return f.Dialect.Translations[l]
}

// PendingTarget reports that this file has a translation surface whose language
// nobody has named yet.
//
// Worth showing rather than hiding: the columns are right there, and the only
// thing between the operator and a thousand translated products is one answer
// they are the only one who has.
func (f File) PendingTarget() bool {
	return len(f.Dialect.TargetColumns) > 0 && f.TargetLang == LangSource
}

// Langs is every language this file can actually carry: the source language
// always, plus each target language at least one of whose columns is present.
//
// The gate matters. A store whose export has no Arabic column must not be
// offered an Arabic pass, because there would be nowhere to write the answer
// and the operator would pay for copy that cannot be exported.
func (f File) Langs() []Lang {
	out := []Lang{LangSource}
	for _, l := range Langs() {
		if l == LangSource {
			continue
		}
		for field := range f.ColumnsFor(l) {
			if f.index(LangField{Field: field, Lang: l}) >= 0 {
				out = append(out, l)
				break
			}
		}
	}
	return out
}

// Langs is every language this profile can carry.
func (d Dialect) Langs() []Lang {
	out := []Lang{LangSource}
	for _, l := range Langs() {
		if l == LangSource {
			continue
		}
		if len(d.Translations[l]) > 0 {
			out = append(out, l)
		}
	}
	return out
}

// Offered is every field a rewrite could change in one language: writable, and
// with a column in this file to write it into.
//
// It is derived from the file rather than from a constant because a product
// export does not have a fixed field set. One store's has an SEO description
// and a custom Arabic body; another's has neither, and offering a toggle for a
// field with nowhere to go is offering somebody a switch that does nothing.
func (f File) Offered(l Lang) []Field {
	var out []Field
	for _, field := range Fields() {
		if !field.Writable() {
			continue
		}
		if f.index(LangField{Field: field, Lang: l}) < 0 {
			continue
		}
		out = append(out, field)
	}
	return out
}

// Writes reports whether a rewrite may change this field in this language.
//
// Two gates, and both have to pass: the file must have somewhere to write it,
// and the operator must not have switched it off. An empty configuration is not
// "nothing selected" — it is "not configured", and it means everything the file
// offers, which is what this tool did before the configuration existed.
func (f File) Writes(lf LangField) bool {
	if !lf.Field.Writable() || f.index(lf) < 0 {
		return false
	}
	if len(f.Write) == 0 {
		return true
	}
	for _, got := range f.Write {
		if got == lf {
			return true
		}
	}
	return false
}

// WriteSet is the fields a pass in one language will actually change.
func (f File) WriteSet(l Lang) []Field {
	var out []Field
	for _, field := range f.Offered(l) {
		if f.Writes(LangField{Field: field, Lang: l}) {
			out = append(out, field)
		}
	}
	return out
}

// normalizeWrite drops what this file cannot honour: a field it has no column
// for, a duplicate, or one that is not writable at all.
//
// Dropping rather than refusing, because the configuration outlives the file it
// was made against — an operator who re-exports with one column removed should
// get their other switches back, not an error.
func normalizeWrite(f File, want []LangField) []LangField {
	seen := map[LangField]bool{}
	var out []LangField
	for _, l := range f.Langs() {
		for _, field := range f.Offered(l) {
			lf := LangField{Field: field, Lang: l}
			if seen[lf] {
				continue
			}
			for _, got := range want {
				if got == lf {
					seen[lf] = true
					out = append(out, lf)
					break
				}
			}
		}
	}
	return out
}
