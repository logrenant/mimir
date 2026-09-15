package catalog

import (
	"strings"
	"unicode"
)

// Script facts, in one place.
//
// Every one of these is a property of a writing system rather than of a
// product, a brand or a model, which is why they are constants here and not
// config: an operator who could tune what counts as an Arabic letter would be
// tuning the definition of the check rather than its strictness.

const (
	tatweel         = 'ـ' // kashida, the stretching character
	arabicComma     = '،'
	arabicSemicolon = '؛'
	arabicQuestion  = '؟'
)

// arabicMark maps an ASCII mark to the Arabic one that belongs in its place.
// The full stop is absent on purpose: Arabic uses the same "." and swapping it
// would be an invention.
var arabicMark = map[rune]rune{
	',': arabicComma,
	';': arabicSemicolon,
	'?': arabicQuestion,
}

// turkishLetters are the letters that exist in Turkish and not in English.
//
// ö and ü are deliberately absent. They are ordinary in loanwords and in brand
// names — Müller, Röntgen — and a check that fires on a brand name is a check
// an operator learns to ignore, which is worse than not having it.
const turkishLetters = "çğıİşÇĞŞ"

func isTurkishLetter(r rune) bool { return strings.ContainsRune(turkishLetters, r) }

// isArabicLetter reports whether a rune is a letter of the Arabic script,
// excluding the vowel marks, which are counted separately.
func isArabicLetter(r rune) bool {
	if isTashkeel(r) || r == tatweel {
		return false
	}
	switch {
	case r >= 0x0620 && r <= 0x064A, // core letters
		r >= 0x0671 && r <= 0x06D3, // extended letters
		r >= 0x0750 && r <= 0x077F, // supplement
		r >= 0x08A0 && r <= 0x08FF, // extended-A
		r >= 0xFB50 && r <= 0xFDFF, // presentation forms A
		r >= 0xFE70 && r <= 0xFEFF: // presentation forms B
		return true
	}
	return false
}

// isTashkeel reports whether a rune is an Arabic vowel or gemination mark.
func isTashkeel(r rune) bool {
	switch {
	case r >= 0x064B && r <= 0x065F,
		r == 0x0670,
		r >= 0x06D6 && r <= 0x06ED:
		return true
	}
	return false
}

// isBidiControl reports whether a rune is one of the invisible characters that
// override the bidirectional algorithm. They are stripped rather than
// preserved: they are invisible in an editor, they reorder a line in a
// storefront, and nothing that writes product copy legitimately emits one.
func isBidiControl(r rune) bool {
	switch r {
	case 0x200E, 0x200F, // LRM, RLM
		0x202A, 0x202B, 0x202C, 0x202D, 0x202E, // embedding / override
		0x2066, 0x2067, 0x2068, 0x2069: // isolates
		return true
	}
	return false
}

// westernDigit maps an Arabic-Indic digit to its Western counterpart.
func westernDigit(r rune) (rune, bool) {
	switch {
	case r >= 0x0660 && r <= 0x0669: // Arabic-Indic
		return '0' + (r - 0x0660), true
	case r >= 0x06F0 && r <= 0x06F9: // Extended Arabic-Indic
		return '0' + (r - 0x06F0), true
	}
	return r, false
}

// isLatinLetter reports whether a rune is a letter of the Latin script,
// including the accented ones every European brand name carries.
func isLatinLetter(r rune) bool {
	if !unicode.IsLetter(r) {
		return false
	}
	return r < 0x0370 // Latin, Latin-1, Latin Extended-A/B and IPA
}

// scriptShare counts letters and reports how many of them satisfy want.
//
// Letters only: digits, punctuation and whitespace say nothing about which
// language a sentence is in, and counting them would make "100 ml" look like
// evidence.
func scriptShare(s string, want func(rune) bool) (share float64, letters int) {
	var hit int
	for _, r := range s {
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		if want(r) {
			hit++
		}
	}
	if letters == 0 {
		return 1, 0
	}
	return float64(hit) / float64(letters), letters
}

// longestLatinRun is the longest unbroken run of Latin letters, counting a
// space between two Latin words as part of the run — "intensive moisturizer
// for dry skin" is one run, not four.
func longestLatinRun(s string) (int, string) {
	var best, cur int
	var start, bestStart int
	rs := []rune(s)
	for i := 0; i <= len(rs); i++ {
		latin := i < len(rs) && (isLatinLetter(rs[i]) ||
			(cur > 0 && (rs[i] == ' ' || rs[i] == '-' || rs[i] == '\'')))
		if latin {
			if cur == 0 {
				start = i
			}
			if isLatinLetter(rs[i]) {
				cur = i - start + 1
			}
			continue
		}
		if cur > best {
			best, bestStart = cur, start
		}
		cur = 0
	}
	if best == 0 {
		return 0, ""
	}
	return best, strings.TrimSpace(string(rs[bestStart : bestStart+best]))
}

// digitRuns is every number in a string, as it is written.
//
// It is the cheapest anti-hallucination check available: a "50 ml" that became
// "500 ml", or a warranty period nobody wrote, is decidable without a model.
func digitRuns(s string) map[string]bool {
	out := map[string]bool{}
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out[cur.String()] = true
			cur.Reset()
		}
	}
	for _, r := range s {
		if r >= '0' && r <= '9' {
			cur.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return out
}

// excerpt is a short, safe quotation for a finding: enough to point at, never
// enough to dump a description into a note.
func excerpt(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	rs := []rune(s)
	if len(rs) <= 40 {
		return s
	}
	return string(rs[:40]) + "…"
}
