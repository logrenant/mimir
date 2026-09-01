// Package extract turns already-fetched HTML (Crawl4AI's RawHTML) into
// small structured facts — JSON-LD blocks, embedded SPA JSON state, Open
// Graph meta tags, CSS-selector scalars — for the free, self-written
// provider scrapers (internal/ecommerce, internal/tiktok, internal/gmaps,
// internal/instagram). Every function here is pure: it takes an HTML/JSON
// string or a parsed *goquery.Document and returns a value, never performs
// network I/O, so every function is testable against local testdata/*.html
// fixtures with no live network or Crawl4AI container required.
package extract

import (
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// Document parses an HTML string into a goquery document for the other
// functions in this package to query.
func Document(html string) (*goquery.Document, error) {
	return goquery.NewDocumentFromReader(strings.NewReader(html))
}

// Text returns the trimmed text content of the first element matching
// selector, or "" if none matches.
func Text(doc *goquery.Document, selector string) string {
	return strings.TrimSpace(doc.Find(selector).First().Text())
}

// Attr returns the trimmed value of attr on the first element matching
// selector, or "" if the element or attribute is absent.
func Attr(doc *goquery.Document, selector, attr string) string {
	v, _ := doc.Find(selector).First().Attr(attr)
	return strings.TrimSpace(v)
}

// TextList returns the trimmed text content of every element matching
// selector, skipping empty results.
func TextList(doc *goquery.Document, selector string) []string {
	var out []string
	doc.Find(selector).Each(func(_ int, s *goquery.Selection) {
		t := strings.TrimSpace(s.Text())
		if t != "" {
			out = append(out, t)
		}
	})
	return out
}

// Meta returns the "content" attribute of a <meta> tag identified by
// property, e.g. Meta(doc, "og:title") checks property="og:title" first,
// then falls back to name="og:title" for pages that use the non-standard
// but common `name` attribute for Open Graph tags.
//
// property must be a fixed, code-controlled constant (it is interpolated
// into a CSS attribute-selector string) — never pass scraped/user input.
func Meta(doc *goquery.Document, property string) string {
	if v, ok := doc.Find(`meta[property="` + property + `"]`).First().Attr("content"); ok {
		return strings.TrimSpace(v)
	}
	if v, ok := doc.Find(`meta[name="` + property + `"]`).First().Attr("content"); ok {
		return strings.TrimSpace(v)
	}
	return ""
}
