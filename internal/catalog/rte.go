// Package catalog reads an e-commerce product export, learns the brand's own
// markup and voice from it, and writes it back without disturbing anything it
// was not asked to change.
//
// The load-bearing idea is in this file. A product description in a Shopify or
// IKAS export is not text — it is HTML the merchant composed in the platform's
// rich-text editor, carrying that brand's own tags, classes and inline styles.
// Handing that HTML to a model and asking for HTML back does not work: the
// model invents markup, and the invented markup is what the operator then
// pastes into a live storefront.
//
// So a model never sees markup here. A description is split into Blocks: the
// text a rewrite may touch, and an opaque Envelope recording exactly what that
// text sat inside. Rendering replays the envelopes. A rewrite can therefore
// change every word and still cannot introduce a tag the brand does not use,
// because the tags never left this package.
package catalog

import (
	"fmt"
	"sort"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// BlockKind is what a block is, at the level a rewrite cares about.
type BlockKind string

const (
	BlockParagraph BlockKind = "paragraph"
	BlockHeading   BlockKind = "heading"
	BlockListItem  BlockKind = "listitem"
	BlockQuote     BlockKind = "quote"
	BlockCell      BlockKind = "cell"
)

// Elem is one element of an envelope: a tag and the attributes it carried.
type Elem struct {
	Tag   string      `json:"tag"`
	Attrs [][2]string `json:"attrs,omitempty"`
}

// Block is the smallest rewritable piece of a description.
type Block struct {
	Kind  BlockKind `json:"kind"`
	Level int       `json:"level,omitempty"` // h1..h6
	// Text is the block's content in a small closed inline syntax:
	// **bold**, _italic_, [text](url), ![alt](src), and \n for a line break.
	// A model reads and writes this and nothing else.
	Text string `json:"text"`
	// Envelope is the tag path this text sat inside, outermost first, ending
	// with the block's own element. A model never sees it.
	Envelope []Elem `json:"envelope"`
}

// Doc is a parsed description.
type Doc struct {
	Blocks []Block
	// Marks counts the inline tags the source used, by their real names. It is
	// kept apart from the closed syntax on purpose: a store that writes <b>
	// must get <b> back, and the syntax says "bold" without saying which tag
	// the brand spells it with.
	Marks map[string]int
	// URLs is every href and src the source actually contained. It is the
	// allow-list a rewrite is held to: a link whose target is not in here was
	// invented, and an invented URL on a product page is a broken link the
	// merchant ships to customers.
	URLs map[string]bool
}

// Vocabulary is the markup the brand actually uses, counted.
//
// It is derived, never configured, and it is the gate Render gauges every
// output against: the brand's own past HTML is the only definition of "what
// this store's descriptions look like" that cannot be wrong.
type Vocabulary struct {
	Tags    map[string]int `json:"tags"`
	Attrs   map[string]int `json:"attrs"`
	Classes map[string]int `json:"classes"`
	Styles  map[string]int `json:"styles"`
}

// NewVocabulary returns an empty, usable vocabulary.
func NewVocabulary() Vocabulary {
	return Vocabulary{
		Tags:    map[string]int{},
		Attrs:   map[string]int{},
		Classes: map[string]int{},
		Styles:  map[string]int{},
	}
}

// Add folds one document's markup into the vocabulary.
func (v Vocabulary) Add(d Doc) {
	for _, b := range d.Blocks {
		for _, e := range b.Envelope {
			v.Tags[e.Tag]++
			for _, a := range e.Attrs {
				v.Attrs[a[0]]++
				switch a[0] {
				case "class":
					for _, c := range strings.Fields(a[1]) {
						v.Classes[c]++
					}
				case "style":
					for _, decl := range strings.Split(a[1], ";") {
						if name, _, ok := strings.Cut(decl, ":"); ok {
							v.Styles[strings.ToLower(strings.TrimSpace(name))]++
						}
					}
				}
			}
		}
	}
	for tag, n := range d.Marks {
		v.Tags[tag] += n
	}
}

// Allows reports whether a tag is part of the brand's vocabulary.
func (v Vocabulary) Allows(tag string) bool { return v.Tags[tag] > 0 }

// IsEmpty reports whether nothing was ever added.
func (v Vocabulary) IsEmpty() bool { return len(v.Tags) == 0 }

// opaqueTags are elements whose text content is not prose.
//
// Unwrapping an unknown element keeps its text, which is right for a <section>
// and wrong for these: the body of a <script> is code, and letting it through
// as text puts `alert(1)` on the product page as a visible sentence. It cannot
// execute — the tag is gone and the preview frame is sandboxed — but it is
// still not something a merchant wrote for a customer to read.
var opaqueTags = map[string]bool{
	"script": true, "style": true, "noscript": true, "template": true,
	"iframe": true, "object": true, "embed": true,
}

// blockTags maps an element name to the kind of block it opens. An element not
// in here is an envelope element (a wrapper) or an inline mark.
var blockTags = map[string]BlockKind{
	"p": BlockParagraph, "h1": BlockHeading, "h2": BlockHeading, "h3": BlockHeading,
	"h4": BlockHeading, "h5": BlockHeading, "h6": BlockHeading,
	"li": BlockListItem, "blockquote": BlockQuote,
	"td": BlockCell, "th": BlockCell,
}

// ParseHTML splits a description into blocks and records its URLs.
func ParseHTML(s string) (Doc, error) {
	d := Doc{URLs: map[string]bool{}, Marks: map[string]int{}}
	if strings.TrimSpace(s) == "" {
		return d, nil
	}
	nodes, err := html.ParseFragment(strings.NewReader(s), &html.Node{
		Type: html.ElementNode, Data: "div", DataAtom: atom.Div,
	})
	if err != nil {
		return Doc{}, fmt.Errorf("catalog: parse description: %w", err)
	}
	for _, n := range nodes {
		d.walk(n, nil)
	}
	return d, nil
}

func (d *Doc) walk(n *html.Node, env []Elem) {
	switch n.Type {
	case html.TextNode:
		// Text loose in a wrapper, with no block element around it. It is
		// still content somebody wrote, so it becomes a paragraph carrying the
		// wrapper it was found in rather than being dropped.
		if strings.TrimSpace(n.Data) == "" {
			return
		}
		d.Blocks = append(d.Blocks, Block{
			Kind: BlockParagraph, Text: normalizeSpace(n.Data), Envelope: cloneEnv(env),
		})
		return
	case html.ElementNode:
	default:
		return
	}

	if opaqueTags[n.Data] {
		return
	}

	e := Elem{Tag: n.Data, Attrs: attrsOf(n)}
	next := append(cloneEnv(env), e)

	if kind, ok := blockTags[n.Data]; ok {
		// An empty block is kept rather than dropped: an empty <p> between two
		// sections is a spacer the merchant put there, and losing it changes
		// the layout of a page nobody asked us to relayout.
		text := d.inlineText(n)
		d.Blocks = append(d.Blocks, Block{
			Kind: kind, Level: headingLevel(n.Data), Text: text, Envelope: next,
		})
		return
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		d.walk(c, next)
	}
}

func headingLevel(tag string) int {
	if len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6' {
		return int(tag[1] - '0')
	}
	return 0
}

// inlineText flattens a block's children into the closed inline syntax and
// records every URL it meets.
func (d *Doc) inlineText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(c *html.Node) {
		switch c.Type {
		case html.TextNode:
			b.WriteString(escapeInline(c.Data))
			return
		case html.ElementNode:
		default:
			return
		}
		if opaqueTags[c.Data] {
			return
		}
		switch c.Data {
		case "br":
			d.Marks["br"]++
			b.WriteString("\n")
			return
		case "img":
			d.Marks["img"]++
			src := attr(c, "src")
			if src != "" {
				d.URLs[src] = true
			}
			b.WriteString("![" + escapeInline(attr(c, "alt")) + "](" + src + ")")
			return
		case "strong", "b":
			d.Marks[c.Data]++
			b.WriteString("**")
			for k := c.FirstChild; k != nil; k = k.NextSibling {
				walk(k)
			}
			b.WriteString("**")
			return
		case "em", "i":
			d.Marks[c.Data]++
			b.WriteString("_")
			for k := c.FirstChild; k != nil; k = k.NextSibling {
				walk(k)
			}
			b.WriteString("_")
			return
		case "a":
			d.Marks["a"]++
			href := attr(c, "href")
			if href != "" {
				d.URLs[href] = true
			}
			b.WriteString("[")
			for k := c.FirstChild; k != nil; k = k.NextSibling {
				walk(k)
			}
			b.WriteString("](" + href + ")")
			return
		}
		for k := c.FirstChild; k != nil; k = k.NextSibling {
			walk(k)
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c)
	}
	return normalizeSpace(b.String())
}

func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if a.Key == name {
			return a.Val
		}
	}
	return ""
}

func attrsOf(n *html.Node) [][2]string {
	if len(n.Attr) == 0 {
		return nil
	}
	out := make([][2]string, 0, len(n.Attr))
	for _, a := range n.Attr {
		out = append(out, [2]string{a.Key, a.Val})
	}
	// Sorted so two elements that carry the same attributes compare equal
	// regardless of the order the source wrote them in — otherwise a <ul>
	// would fail to match itself and every <li> would get its own list.
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

func cloneEnv(e []Elem) []Elem {
	if len(e) == 0 {
		return nil
	}
	out := make([]Elem, len(e))
	copy(out, e)
	return out
}

func normalizeSpace(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.Join(strings.Fields(l), " ")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func escapeInline(s string) string {
	// '!' is not escaped: an image is "![", and the '[' is escaped already, so
	// a literal exclamation mark cannot start one.
	r := strings.NewReplacer(`\`, `\\`, `*`, `\*`, `_`, `\_`, `[`, `\[`, `]`, `\]`)
	return r.Replace(s)
}
