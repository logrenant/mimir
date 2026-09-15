package catalog

import (
	"html"
	"strings"
)

// voidTags never get a closing tag.
var voidTags = map[string]bool{
	"br": true, "hr": true, "img": true, "input": true, "meta": true, "link": true,
}

// Render turns blocks back into HTML, replaying their envelopes.
//
// Every tag, attribute, class and style declaration is checked against v and
// dropped when it is not part of the brand's own vocabulary. The check fails
// *closed*: an unknown wrapper is unwrapped rather than emitted, an unknown
// attribute is removed, and a link whose target is not in allowedURLs becomes
// plain text. That is what makes the claim of this package testable — the tag
// set of the output cannot be larger than the tag set of the input.
//
// allowedURLs may be nil, which forbids every link and image. That is the
// correct posture for a rewrite with no source document: a model that produced
// a URL out of nothing produced a broken link.
func Render(blocks []Block, v Vocabulary, allowedURLs map[string]bool) string {
	var b strings.Builder
	var open []Elem

	closeTo := func(depth int) {
		for i := len(open) - 1; i >= depth; i-- {
			if !voidTags[open[i].Tag] {
				b.WriteString("</" + open[i].Tag + ">")
			}
		}
		open = open[:depth]
	}

	for _, blk := range blocks {
		env := sanitizeEnvelope(blk.Envelope, v)
		if len(env) == 0 {
			// Nothing in the envelope survived the vocabulary. The text still
			// did, and dropping content because its wrapper was unknown would
			// be the one failure this package exists to prevent.
			b.WriteString(renderInline(blk.Text, v, allowedURLs))
			continue
		}
		ancestors, self := env[:len(env)-1], env[len(env)-1]

		common := 0
		for common < len(open) && common < len(ancestors) && sameElem(open[common], ancestors[common]) {
			common++
		}
		closeTo(common)
		for _, e := range ancestors[common:] {
			b.WriteString(openTag(e))
			open = append(open, e)
		}

		b.WriteString(openTag(self))
		b.WriteString(renderInline(blk.Text, v, allowedURLs))
		if !voidTags[self.Tag] {
			b.WriteString("</" + self.Tag + ">")
		}
	}
	closeTo(0)
	return b.String()
}

func sameElem(a, b Elem) bool {
	if a.Tag != b.Tag || len(a.Attrs) != len(b.Attrs) {
		return false
	}
	for i := range a.Attrs {
		if a.Attrs[i] != b.Attrs[i] {
			return false
		}
	}
	return true
}

func openTag(e Elem) string {
	var b strings.Builder
	b.WriteString("<" + e.Tag)
	for _, a := range e.Attrs {
		b.WriteString(" " + a[0] + `="` + html.EscapeString(a[1]) + `"`)
	}
	b.WriteString(">")
	return b.String()
}

// sanitizeEnvelope drops elements the brand does not use and prunes the
// attributes of the ones it keeps. Dropping a wrapper keeps its descendants —
// the path simply gets shorter.
func sanitizeEnvelope(env []Elem, v Vocabulary) []Elem {
	out := make([]Elem, 0, len(env))
	for _, e := range env {
		if !v.Allows(e.Tag) {
			continue
		}
		out = append(out, Elem{Tag: e.Tag, Attrs: sanitizeAttrs(e.Attrs, v)})
	}
	return out
}

func sanitizeAttrs(attrs [][2]string, v Vocabulary) [][2]string {
	if len(attrs) == 0 {
		return nil
	}
	out := make([][2]string, 0, len(attrs))
	for _, a := range attrs {
		if v.Attrs[a[0]] == 0 {
			continue
		}
		switch a[0] {
		case "class":
			kept := make([]string, 0, 4)
			for _, c := range strings.Fields(a[1]) {
				if v.Classes[c] > 0 {
					kept = append(kept, c)
				}
			}
			if len(kept) == 0 {
				continue
			}
			out = append(out, [2]string{"class", strings.Join(kept, " ")})
		case "style":
			kept := make([]string, 0, 4)
			for _, decl := range strings.Split(a[1], ";") {
				name, _, ok := strings.Cut(decl, ":")
				if !ok {
					continue
				}
				if v.Styles[strings.ToLower(strings.TrimSpace(name))] > 0 {
					kept = append(kept, strings.TrimSpace(decl))
				}
			}
			if len(kept) == 0 {
				continue
			}
			out = append(out, [2]string{"style", strings.Join(kept, "; ")})
		default:
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// --- the closed inline syntax -----------------------------------------------

type inlineKind int

const (
	inlineText inlineKind = iota
	inlineBold
	inlineItalic
	inlineLink
	inlineImage
	inlineBreak
)

type inlineToken struct {
	kind inlineKind
	text string
	url  string
}

// parseInline reads the small syntax a model writes back. It is deliberately
// tiny and total: anything it does not recognise stays literal text, so a
// malformed answer degrades into prose rather than into markup.
func parseInline(s string) []inlineToken {
	var out []inlineToken
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			out = append(out, inlineToken{kind: inlineText, text: lit.String()})
			lit.Reset()
		}
	}
	rs := []rune(s)
	for i := 0; i < len(rs); {
		switch {
		case rs[i] == '\\' && i+1 < len(rs):
			lit.WriteRune(rs[i+1])
			i += 2
		case rs[i] == '\n':
			flush()
			out = append(out, inlineToken{kind: inlineBreak})
			i++
		case strings.HasPrefix(string(rs[i:]), "**"):
			if j := indexRunes(rs, i+2, "**"); j >= 0 {
				flush()
				out = append(out, inlineToken{kind: inlineBold, text: string(rs[i+2 : j])})
				i = j + 2
				continue
			}
			lit.WriteRune(rs[i])
			i++
		case rs[i] == '_':
			if j := indexRunes(rs, i+1, "_"); j >= 0 {
				flush()
				out = append(out, inlineToken{kind: inlineItalic, text: string(rs[i+1 : j])})
				i = j + 1
				continue
			}
			lit.WriteRune(rs[i])
			i++
		case rs[i] == '!' && i+1 < len(rs) && rs[i+1] == '[':
			if txt, url, next, ok := readLink(rs, i+1); ok {
				flush()
				out = append(out, inlineToken{kind: inlineImage, text: txt, url: url})
				i = next
				continue
			}
			lit.WriteRune(rs[i])
			i++
		case rs[i] == '[':
			if txt, url, next, ok := readLink(rs, i); ok {
				flush()
				out = append(out, inlineToken{kind: inlineLink, text: txt, url: url})
				i = next
				continue
			}
			lit.WriteRune(rs[i])
			i++
		default:
			lit.WriteRune(rs[i])
			i++
		}
	}
	flush()
	return out
}

func indexRunes(rs []rune, from int, needle string) int {
	for i := from; i < len(rs); i++ {
		if rs[i] == '\\' {
			i++
			continue
		}
		if strings.HasPrefix(string(rs[i:]), needle) {
			return i
		}
	}
	return -1
}

// readLink reads "[text](url)" starting at the '['.
func readLink(rs []rune, i int) (text, url string, next int, ok bool) {
	close := indexRunes(rs, i+1, "]")
	if close < 0 || close+1 >= len(rs) || rs[close+1] != '(' {
		return "", "", 0, false
	}
	end := indexRunes(rs, close+2, ")")
	if end < 0 {
		return "", "", 0, false
	}
	return unescapeInline(string(rs[i+1 : close])), string(rs[close+2 : end]), end + 1, true
}

func unescapeInline(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if rs[i] == '\\' && i+1 < len(rs) {
			i++
		}
		b.WriteRune(rs[i])
	}
	return b.String()
}

// pickTag returns the first tag the brand actually uses out of the candidates,
// or "" when it uses none of them. It is why a store that writes <b> gets <b>
// back and a store that writes <strong> gets <strong> — the mark is the
// brand's, not ours.
func pickTag(v Vocabulary, candidates ...string) string {
	best, bestN := "", 0
	for _, c := range candidates {
		if n := v.Tags[c]; n > bestN {
			best, bestN = c, n
		}
	}
	return best
}

func renderInline(s string, v Vocabulary, allowedURLs map[string]bool) string {
	var b strings.Builder
	for _, t := range parseInline(s) {
		switch t.kind {
		case inlineText:
			b.WriteString(html.EscapeString(t.text))
		case inlineBreak:
			if tag := pickTag(v, "br"); tag != "" {
				b.WriteString("<br>")
			} else {
				b.WriteString(" ")
			}
		case inlineBold:
			writeMark(&b, pickTag(v, "strong", "b"), t.text, v, allowedURLs)
		case inlineItalic:
			writeMark(&b, pickTag(v, "em", "i"), t.text, v, allowedURLs)
		case inlineLink:
			tag := pickTag(v, "a")
			if tag == "" || !allowedURLs[t.url] {
				// An invented target, or a brand that never links. Either way
				// the words survive and the link does not.
				b.WriteString(html.EscapeString(t.text))
				continue
			}
			b.WriteString(`<a href="` + html.EscapeString(t.url) + `">` +
				html.EscapeString(t.text) + `</a>`)
		case inlineImage:
			if !v.Allows("img") || !allowedURLs[t.url] {
				continue
			}
			b.WriteString(`<img src="` + html.EscapeString(t.url) + `"`)
			if t.text != "" {
				b.WriteString(` alt="` + html.EscapeString(t.text) + `"`)
			}
			b.WriteString(">")
		}
	}
	return b.String()
}

func writeMark(b *strings.Builder, tag, text string, v Vocabulary, allowedURLs map[string]bool) {
	inner := renderInline(text, v, allowedURLs)
	if tag == "" {
		b.WriteString(inner)
		return
	}
	b.WriteString("<" + tag + ">" + inner + "</" + tag + ">")
}

// rtlWrapper is the one element in this package that is not the brand's own.
const rtlWrapper = "div"

// RenderLang is Render plus a direction, and the direction is deliberately not
// a Vocabulary widening.
//
// The trap it exists for: Vocabulary is counted from the store's own past HTML,
// and sanitizeAttrs drops any attribute that is not in it. A Turkish store has
// never written dir, so a dir="rtl" a model emitted — or one this package added
// to an envelope — is silently dropped, and the Arabic ships left-to-right with
// its punctuation on the wrong side and its numbers reordered. Silently, which
// is the part that makes it dangerous: nothing fails, the file exports, and the
// storefront is wrong.
//
// Widening the vocabulary for a target language was the other option and it is
// worse. It would let dir through on every element, including ones a model
// chose, and it would make the vocabulary a function of the target language
// rather than of the file — which is the one thing internal/catalog/AGENTS.md
// says the vocabulary must never be.
//
// So: one element, written by this function, carrying nothing but direction and
// language. No class, no style, no visual opinion. It is the minimum HTML that
// makes Arabic legible, and dropping it because the brand never wrote a <div>
// would be the vocabulary refusing the one thing it has no opinion about.
//
// For a left-to-right language the output is byte-identical to Render, which is
// what keeps every source-language draft exactly as it was.
func RenderLang(blocks []Block, v Vocabulary, allowedURLs map[string]bool, lang Lang) string {
	inner := Render(blocks, v, allowedURLs)
	if !lang.RTL() || strings.TrimSpace(inner) == "" {
		return inner
	}
	return `<` + rtlWrapper + ` dir="` + lang.Dir() + `" lang="` + lang.Tag() + `">` +
		inner + `</` + rtlWrapper + `>`
}

// stripDirectionWrapper removes a wrapper this package added, so a draft that
// is edited and saved again is re-wrapped rather than nested.
//
// It is needed because SaveDraft re-parses stored HTML: ParseHTML puts the
// wrapper into every block's envelope, sanitizeEnvelope then drops it — no div
// in the vocabulary, no dir in Attrs — and the operator's first edit would
// silently strip the direction from copy that already shipped with it.
func stripDirectionWrapper(blocks []Block) []Block {
	out := make([]Block, len(blocks))
	copy(out, blocks)
	for i, b := range out {
		if len(b.Envelope) == 0 || !isDirectionWrapper(b.Envelope[0]) {
			continue
		}
		env := make([]Elem, len(b.Envelope)-1)
		copy(env, b.Envelope[1:])
		out[i].Envelope = env
	}
	return out
}

func isDirectionWrapper(e Elem) bool {
	if e.Tag != rtlWrapper || len(e.Attrs) == 0 || len(e.Attrs) > 2 {
		return false
	}
	var sawDir bool
	for _, a := range e.Attrs {
		switch a[0] {
		case "dir":
			sawDir = true
		case "lang":
		default:
			return false
		}
	}
	return sawDir
}
