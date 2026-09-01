// Package mapscrape is the fallback half of Maps region search: when the
// Places API's coverage or cost is not worth it, this reads the same question
// off the public Google Maps results feed.
//
// It answers with the same types internal/maps does — a maps.Query in,
// []maps.Company out — so a caller can swap providers without reshaping
// anything. Two things about those companies are deliberately different:
//
//   - Provenance. Source is maps.SourceScrape and PlaceID carries the
//     PlaceIDPrefix, so a scraped row can never be mistaken for, or collide
//     with, a billed Places row in store.companies.
//   - Fewer fields, honestly empty. The feed gives a name, coordinates, a
//     rating, a review count and sometimes a website. It does not give
//     Google's types[] taxonomy or a phone number, and its address is
//     best-effort. Unfound fields are left zero — never guessed, never
//     inferred. Downstream already expects this: internal/leadgen's rule table
//     sends a typeless company to its model tier rather than assume.
//
// It is a scraper against a DOM whose owner changes it without notice.
// Extraction is anchored on the most stable surface the feed has — the
// accessibility markup on each result link — but the honest expectation is
// that this breaks, is noticed by the integration test rather than by silent
// wrong answers, and is fixed. See internal/gmaps's package doc for what was
// already learned the hard way about scraping Maps: the detail panel is an
// obfuscated blob, and Open Graph tags there are generic. This package does
// not go near either.
package mapscrape

import (
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/logrenant/goat-mcp/internal/maps"
)

// ImageTag is the pinned sidecar image (see
// deploy/playwright-maps/docker-compose.yml). Reported by the diagnostics tool
// so an operator can see which renderer is expected without reading compose.
const ImageTag = "mcr.microsoft.com/playwright:v1.62.1-noble"

// PlaceIDPrefix marks an id this package derived from a Maps URL rather than
// received from the Places API.
//
// The prefix is the whole reason a scraped region and a billed one can share
// store.companies: place_id is that table's primary key, so two providers
// writing the same business under the same id would silently overwrite each
// other's provenance, rating and field coverage.
const PlaceIDPrefix = "mapscrape:"

var (
	// ErrSidecarUnavailable means the Playwright container could not be
	// reached or did not answer. It is the mapscrape twin of
	// crawl.ErrDockerUnavailable, and its message names the fix (SD-6).
	ErrSidecarUnavailable = errors.New("mapscrape: playwright sidecar unavailable")

	// ErrBlocked means Google answered with a consent wall, a captcha, or a
	// /sorry/ interstitial instead of a feed. Retrying immediately will not
	// help, and defeating it is explicitly not this package's job.
	ErrBlocked = errors.New("mapscrape: google served a consent or block page")

	// ErrNoResults means the feed rendered but no result carried a usable
	// name. That is either a genuinely empty region or a moved selector — the
	// integration test is what tells those apart.
	ErrNoResults = errors.New("mapscrape: feed rendered no usable results")
)

// SearchURL builds the Maps results-feed URL for a query.
//
// The /maps/search/ form is used rather than /maps/place/ because this package
// wants the feed — the list of every business matching a region query — not one
// business's panel. It takes the same maps.Query the Places client takes, so
// the two providers cannot drift on what a region search means.
func SearchURL(q maps.Query) string {
	var b strings.Builder
	b.WriteString("https://www.google.com/maps/search/")
	b.WriteString(url.PathEscape(strings.TrimSpace(q.Text)))

	if q.Bias != nil {
		// The @lat,lng,zoom segment is how Maps is told where to look; without
		// it the query text alone has to carry the location.
		b.WriteString("/@")
		b.WriteString(strconv.FormatFloat(q.Bias.Latitude, 'f', 7, 64))
		b.WriteString(",")
		b.WriteString(strconv.FormatFloat(q.Bias.Longitude, 'f', 7, 64))
		b.WriteString(",")
		b.WriteString(zoomForRadius(q.Bias.RadiusMeters))
	}

	params := url.Values{}
	// hl selects the interface language. It matters for parsing: the rating
	// label is localized, which is why extraction reads numbers out of it
	// rather than words.
	if q.LanguageCode != "" {
		params.Set("hl", q.LanguageCode)
	}
	if q.RegionCode != "" {
		params.Set("gl", strings.ToLower(q.RegionCode))
	}
	if encoded := params.Encode(); encoded != "" {
		b.WriteString("?")
		b.WriteString(encoded)
	}

	return b.String()
}

// zoomForRadius picks the Maps zoom level whose viewport roughly covers a
// radius. Coarse on purpose: the zoom biases which results the feed loads
// first, it does not filter them, so a level that is one step off costs
// ordering, not correctness.
func zoomForRadius(meters float64) string {
	switch {
	case meters <= 0:
		return "14z"
	case meters <= 500:
		return "17z"
	case meters <= 1500:
		return "15z"
	case meters <= 5000:
		return "14z"
	case meters <= 15000:
		return "12z"
	default:
		return "11z"
	}
}
