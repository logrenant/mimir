// Package gmaps extracts a normalized business Place from an already-crawled
// Google Maps page.
//
// Verified against a live Crawl4AI container while building this package
// (not guessed):
//
//   - Google Maps' business detail panel (rating, address, phone, hours)
//     loads via a late, JS-driven, undocumented internal data blob — an
//     obfuscated `)]}'`-prefixed positional JSON array, not a stable,
//     named-key contract like TikTok's __UNIVERSAL_DATA_FOR_REHYDRATION__.
//     It did not reliably populate within Crawl4AI's wait_for/page_timeout
//     knobs during manual verification.
//   - Open Graph meta tags are unusable here: Google Maps emits the same
//     generic "Google Maps" og:title/og:description on every place page
//     regardless of which business it is.
//   - What IS reliable: the rendered <title> tag, which Google Maps
//     populates with the place/query name once the page has actually
//     hydrated — as opposed to still showing the generic "Google
//     Maps"/"Google Haritalar" loading-skeleton title — provided the crawl
//     uses FetchOptions{WaitForSelector: "h1"} and a generous PageTimeout
//     (~30s; 20s was not enough in testing).
//
// This package therefore extracts a reliable Name from <title> and treats
// rating/address/phone/website as best-effort CSS-selector extraction that
// frequently misses — every miss is surfaced in Notes, never silently wrong
// or fabricated. This is a known, open gap for follow-up work (a task-22
// DoD explicitly calling for manual live verification), not a claim of
// parity with the richer fields internal/ecommerce or internal/tiktok
// return.
package gmaps

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/crawl"
	"github.com/logrenant/goat-mcp/internal/extract"
)

// ErrNotAPlacePage means the page never rendered past Google's generic
// loading-skeleton title — the query didn't resolve to a specific place
// within the render budget, or Crawl4AI hit a consent/redirect wall.
var ErrNotAPlacePage = errors.New("gmaps: page did not render a specific place — try a more specific query, or a direct place URL")

// Place is the normalized shape returned. Only Name is reliably populated;
// every other field is best-effort and may be empty — check Notes.
type Place struct {
	Name    string
	Address string
	Phone   string
	Website string
	Rating  float64
	Notes   []string
}

// SearchURL builds a Google Maps URL for a free-text query (e.g. "Blue
// Bottle Coffee San Francisco"). Verified to reliably render a
// place-specific <title> when crawled with FetchOptions{WaitForSelector:
// "h1", PageTimeout: ~30s}.
func SearchURL(query string) string {
	return "https://www.google.com/maps/place/?q=" + url.QueryEscape(query)
}

var genericTitles = map[string]bool{
	"Google Maps":      true,
	"Google Haritalar": true,
}

var titleSuffixRe = regexp.MustCompile(`\s*[-–]\s*Google (Maps|Haritalar)\s*$`)

// Extract pulls a Place out of an already-crawled page's RawHTML.
func Extract(cfg config.Config, page crawl.Page) (Place, error) {
	doc, err := extract.Document(page.RawHTML)
	if err != nil {
		return Place{}, fmt.Errorf("gmaps: parse html: %w", err)
	}

	rawTitle := extract.Text(doc, "title")
	name := strings.TrimSpace(titleSuffixRe.ReplaceAllString(rawTitle, ""))
	if name == "" || genericTitles[rawTitle] || genericTitles[name] {
		return Place{}, ErrNotAPlacePage
	}

	p := Place{Name: name}
	var notes []string

	if addr := extract.Text(doc, `[data-item-id="address"]`); addr != "" {
		p.Address = extract.ClampText(addr, cfg.BusinessAboutMaxChars)
	} else {
		notes = append(notes, "address not extractable from this render")
	}
	if phone := extract.Text(doc, `[data-item-id^="phone"]`); phone != "" {
		p.Phone = phone
	} else {
		notes = append(notes, "phone not extractable from this render")
	}
	if site := extract.Attr(doc, `[data-item-id="authority"]`, "href"); site != "" {
		p.Website = site
	} else {
		notes = append(notes, "website not extractable from this render")
	}
	if ratingLabel := extract.Attr(doc, `span[role="img"][aria-label*="star"]`, "aria-label"); ratingLabel != "" {
		if r, ok := extract.ParseFloatLoose(ratingLabel); ok {
			p.Rating = r
		}
	} else {
		notes = append(notes, "rating not extractable from this render")
	}

	p.Notes = notes
	return p, nil
}
