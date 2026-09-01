package mapscrape

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"github.com/logrenant/mimir/internal/extract"
	"github.com/logrenant/mimir/internal/maps"
)

// The anchors extraction is built on, in descending order of how likely they
// are to survive a redesign:
//
//  1. `a[href*="/maps/place/"]` with an aria-label — accessibility markup for
//     the result link. Every result has one, and it is the business name.
//  2. The href's own !-segments — `!1s<feature id>`, `!3d<lat>`, `!4d<lng>`.
//     These are Maps' internal URL grammar, unchanged for years, and they are
//     what a shared link has to carry to still resolve.
//  3. A rating label of the shape "4.5 stars 231 reviews" — localized, so it
//     is read as numbers, never as words.
//
// Class names (`hfpxzc`, `MW4etd`, …) are deliberately not used anywhere: they
// are minified build output and change without notice.
var (
	// featureIDRe captures Maps' stable place identifier out of a place URL:
	// !1s0x14caba0000000000:0xdeadbeef.
	featureIDRe = regexp.MustCompile(`!1s(0x[0-9a-f]+:0x[0-9a-f]+)`)
	// coordRe captures the !3d<lat>!4d<lng> pair.
	coordRe = regexp.MustCompile(`!3d(-?\d+\.\d+)!4d(-?\d+\.\d+)`)
	// ratingRe reads the leading number of a localized rating label: "4.5
	// stars", "4,5 yıldız", "4.5 étoiles".
	ratingRe = regexp.MustCompile(`^\s*(\d+[.,]\d+|\d+)`)
	// numberRe reads the next grouped number in a string — the review count
	// that follows the score in a rating label ("4.6 stars 231 reviews",
	// "4,5 yıldız 1.024 yorum"). The label is localized, so the count is found
	// by position after the score, never by matching the word "reviews".
	numberRe = regexp.MustCompile(`\d[\d.,\x{00a0} ]*`)
)

// ParseFeed extracts companies from the results-feed HTML the sidecar returned.
//
// Positional and forgiving in one direction only: a card that yields no name is
// skipped and counted, because one unparseable result must not lose the other
// forty. A field that is not found is left zero — this package never infers a
// rating from a review count, an address from a name, or a place id from a
// position in the list.
func ParseFeed(html string, fetchedAt time.Time) ([]maps.Company, int, error) {
	doc, err := extract.Document(html)
	if err != nil {
		return nil, 0, err
	}

	var (
		out     []maps.Company
		skipped int
		seen    = map[string]struct{}{}
	)

	doc.Find(`a[href*="/maps/place/"]`).Each(func(_ int, a *goquery.Selection) {
		name := strings.TrimSpace(a.AttrOr("aria-label", ""))
		href := a.AttrOr("href", "")
		if name == "" || href == "" {
			skipped++
			return
		}

		id := placeIDFromHref(href)
		if id == "" {
			// No feature id means nothing downstream can key this row, and a
			// synthetic id from the name would collide the moment two branches
			// share one. Better to lose the row and say so.
			skipped++
			return
		}
		if _, dup := seen[id]; dup {
			// The feed renders the same place twice — once for the card, once
			// for a "directions"-style link.
			return
		}
		seen[id] = struct{}{}

		company := maps.Company{
			PlaceID:   id,
			Name:      name,
			Source:    maps.SourceScrape,
			FetchedAt: fetchedAt,
		}
		company.Latitude, company.Longitude = coordsFromHref(href)

		// The card is the anchor's nearest ancestor that also holds the rating
		// and the outbound website link. Walking up from the anchor rather than
		// down from a container selector means the feed's own structure can
		// change without breaking this.
		card := cardFor(a)
		company.Rating, company.ReviewCount = ratingFrom(card)
		company.Website = websiteFrom(card)

		out = append(out, company)
	})

	if len(out) == 0 {
		return nil, skipped, ErrNoResults
	}
	return out, skipped, nil
}

// cardFor walks up from a result link to the element that holds the rest of
// that result, stopping before it reaches a scope containing other results.
func cardFor(a *goquery.Selection) *goquery.Selection {
	node := a
	for range 4 {
		parent := node.Parent()
		if parent.Length() == 0 {
			break
		}
		// Stop as soon as the ancestor contains a second result link: one more
		// step up and the rating read below would belong to a neighbour.
		if parent.Find(`a[href*="/maps/place/"]`).Length() > 1 {
			break
		}
		node = parent
	}
	return node
}

// placeIDFromHref derives a stable, prefixed id from a Maps place URL.
func placeIDFromHref(href string) string {
	m := featureIDRe.FindStringSubmatch(href)
	if len(m) < 2 {
		return ""
	}
	return PlaceIDPrefix + m[1]
}

// coordsFromHref reads the !3d/!4d pair.
//
// strconv, not extract.ParseFloatLoose: that helper's number pattern does not
// include a leading sign, so every southern latitude and western longitude
// would come back positive — a silently wrong coordinate on the other side of
// the planet.
func coordsFromHref(href string) (lat, lng float64) {
	m := coordRe.FindStringSubmatch(href)
	if len(m) < 3 {
		return 0, 0
	}
	lat, errLat := strconv.ParseFloat(m[1], 64)
	lng, errLng := strconv.ParseFloat(m[2], 64)
	if errLat != nil || errLng != nil {
		return 0, 0
	}
	return lat, lng
}

// parseRating turns the leading number of a localized rating label into a
// score. "4,5" is four-and-a-half in most of Europe, so the comma is a decimal
// point here — not the thousands separator extract.ParseFloatLoose would strip,
// which would read it as forty-five.
func parseRating(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.Replace(strings.TrimSpace(s), ",", ".", 1), 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// parseCount reads an integer written with any locale's grouping: "1,024",
// "1.024" and "1 024" are all 1024. Every separator is a thousands separator
// because a review count has no fractional part, so the digits alone are the
// number.
func parseCount(s string) (int, bool) {
	var digits strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	if digits.Len() == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(digits.String())
	if err != nil {
		return 0, false
	}
	return n, true
}

// ratingFrom reads the rating and review count out of the card's rating label.
//
// The label is localized ("4.6 stars 231 Reviews", "4,9 yıldızlı 506 Yorum"),
// so only its numbers are read, and the count is taken by position — the number
// after the score — never by matching the word "reviews".
//
// The rating label is the ONLY place a review count may come from. Google
// serves two variants of this label, apparently A/B rather than by locale: a
// rich one carrying the count, and a bare "4,9 yıldızlı" that does not. Verified
// live: the same query at the same minute produced both, depending on the
// browser locale sent, and the bare variant has the count nowhere in the feed.
//
// So an absent count stays zero. An earlier version searched the card for a
// parenthesised number as a fallback and read Istanbul phone numbers —
// "(0216) 330 09 99" — as 216 reviews. That is the exact failure this package's
// "never fabricate a field" rule exists to prevent, and it was caught by the
// live integration test, not by review.
func ratingFrom(card *goquery.Selection) (float64, int) {
	var rating float64
	var reviews int

	card.Find(`[aria-label]`).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		label := strings.TrimSpace(s.AttrOr("aria-label", ""))
		m := ratingRe.FindStringSubmatch(label)
		if len(m) < 2 {
			return true
		}
		value, ok := parseRating(m[1])
		// A rating is a number between 1 and 5. Anything else — a price, a
		// distance, a house number — is a label that merely starts with a
		// digit, and must not be read as a score.
		if !ok || value < 1 || value > 5 {
			return true
		}
		rating = value

		// The count is whatever number comes next, if any: an unrated-but-listed
		// business has a score and no count, and both forms are normal.
		if rest := label[len(m[0]):]; rest != "" {
			if token := numberRe.FindString(rest); token != "" {
				if n, ok := parseCount(token); ok {
					reviews = n
				}
			}
		}
		return false
	})

	return rating, reviews
}

// websiteFrom returns the business's own site: the first outbound link in the
// card that is not Google's own. Anything google.* is navigation, not a
// business website.
func websiteFrom(card *goquery.Selection) string {
	var website string
	card.Find(`a[href^="http"]`).EachWithBreak(func(_ int, s *goquery.Selection) bool {
		href := strings.TrimSpace(s.AttrOr("href", ""))
		if href == "" || isGoogleHost(href) {
			return true
		}
		website = href
		return false
	})
	return website
}

func isGoogleHost(href string) bool {
	rest, ok := strings.CutPrefix(href, "https://")
	if !ok {
		rest, ok = strings.CutPrefix(href, "http://")
		if !ok {
			return false
		}
	}
	host, _, _ := strings.Cut(rest, "/")
	host, _, _ = strings.Cut(host, ":")
	host = strings.ToLower(host)

	// Suffix-matched on a dot boundary, so "google.com" and "www.google.com"
	// match while "notgoogle.com" and "google.com.evil.example" do not.
	for _, suffix := range []string{"google.com", "google.co.uk", "goo.gl", "gstatic.com", "ggpht.com"} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}
