package extract

import (
	"strings"
	"unicode/utf8"
)

// ClampText is the isolation control for every free-scraper provider
// package (internal/ecommerce, internal/tiktok, internal/gmaps,
// internal/instagram): those tools skip the `claude` CLI refine step (the
// project's usual prompt-injection firewall, see internal/refine) because
// they return small deterministic structured facts, not prose to distil.
// Any free-text field pulled from a scraped page — a bio, a product
// description, an "about" blurb — MUST go through this function before it
// can reach a tool response.
//
// It drops '<'/'>' entirely (so a scraped bio containing a literal
// "<script>" or similar can never be misread as markup by any downstream
// renderer or confuse the MCP consumer), collapses all whitespace runs to
// single spaces, and hard-truncates to maxChars. It never summarizes or
// otherwise interprets the text — only makes it inert and bounded.
func ClampText(s string, maxChars int) string {
	s = strings.Map(func(r rune) rune {
		if r == '<' || r == '>' {
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")

	if maxChars <= 0 || utf8.RuneCountInString(s) <= maxChars {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:maxChars])) + "…"
}
