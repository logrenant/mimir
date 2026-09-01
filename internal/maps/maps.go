// Package maps is the Google Places API (New) client — the primary,
// deterministic data source for the Maps lead-generation pipeline.
//
// It answers exactly one question: "which companies exist in this region?".
// The answer is structured business facts (name, address, coordinates,
// rating, review count, website, phone, Google's own type taxonomy), not page
// text — so, like the Stage F scrapers, this package never touches
// internal/refine and costs zero Claude tokens. Every token the Maps pipeline
// spends is spent later, in internal/leadgen, on work that genuinely needs a
// model.
//
// Two deliberate omissions:
//
//   - No store access. internal/leadgen decides when a search may be served
//     from cache, exactly as internal/pipeline — not internal/crawl — owns
//     the page cache. A pure HTTP client is testable without a database and
//     cannot quietly become a second orchestrator.
//   - No credential lookup. The API key arrives as a constructor argument
//     (SD-1); this package never reads the environment.
package maps

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Pinned endpoint and API-imposed limits. PageSize and MaxResults are the
// Places API's own ceilings for places:searchText, not preferences — the
// server rejects a larger pageSize and stops issuing page tokens after the
// third page.
const (
	// DefaultBaseURL pins the major version (SD-5): no floating alias.
	DefaultBaseURL = "https://places.googleapis.com/v1"
	PageSize       = 20
	MaxResults     = 60
)

// FieldMask is the exact set of fields requested from Places.
//
// This is not only a schema decision, it is the cost control: the Places API
// bills by SKU tier according to which fields the mask names, and unrequested
// fields are neither returned nor billed. Adding a field here changes the
// bill. Everything in this list is Essentials/Pro-tier data the lead-gen
// pipeline actually consumes downstream; nothing free-text (editorial
// summaries, reviews) is requested, which is also why no response from this
// package needs refining (SD-2).
const FieldMask = "places.id," +
	"places.displayName," +
	"places.formattedAddress," +
	"places.location," +
	"places.rating," +
	"places.userRatingCount," +
	"places.websiteUri," +
	"places.nationalPhoneNumber," +
	"places.types," +
	"places.primaryType," +
	"places.businessStatus," +
	"nextPageToken"

// Provenance of a Company row. internal/mapscrape (M5) is the fallback that
// fills the second one when Places coverage is insufficient.
const (
	SourcePlacesAPI = "places_api"
	SourceScrape    = "mapscrape"
)

// Sentinel errors. Each carries an actionable fix in its message (SD-6);
// none of them ever embeds the API key.
var (
	// ErrCredentialMissing means no operator-provisioned Places key was passed.
	ErrCredentialMissing = errors.New("maps: no Google Places API key")
	// ErrRequestDenied means the key was rejected: unknown, restricted to
	// other referrers/IPs, or the Places API is not enabled for its project.
	ErrRequestDenied = errors.New("maps: places api denied the request")
	// ErrQuotaExceeded means the project's request quota or billing cap is
	// exhausted. Retrying immediately will not help.
	ErrQuotaExceeded = errors.New("maps: places api quota exhausted")
	// ErrBadRequest means the query itself was malformed — a caller bug, not
	// an outage.
	ErrBadRequest = errors.New("maps: places api rejected the query")
	// ErrUnavailable means the API could not be reached, or answered 5xx
	// twice. Degrade to internal/mapscrape or serve stale cache.
	ErrUnavailable = errors.New("maps: places api unreachable")
)

// Circle is a location bias: search near this point, within this radius.
type Circle struct {
	Latitude     float64
	Longitude    float64
	RadiusMeters float64
}

// Query is one region search.
//
// Every field participates in Key, so two queries that could return different
// companies can never share a cache entry.
type Query struct {
	// Text is the free-text query, e.g. "dentists in Kadıköy, Istanbul".
	Text string
	// LanguageCode ("tr", "en") selects the language of returned names and
	// addresses. Empty lets Places choose.
	LanguageCode string
	// RegionCode is a CLDR region ("TR", "US") biasing result formatting.
	RegionCode string
	// Bias optionally restricts results to a circle. Nil means the text query
	// alone carries the location.
	Bias *Circle
	// MaxResults caps how many companies are collected across pages. Zero or
	// anything above MaxResults means MaxResults.
	MaxResults int
}

// limit returns the effective, clamped result cap.
func (q Query) limit() int {
	if q.MaxResults <= 0 || q.MaxResults > MaxResults {
		return MaxResults
	}
	return q.MaxResults
}

// Key is the total cache key for this search, suitable as
// store.GetRegionSearch's regionKey.
//
// "Total" is the contract: if a field can change which companies come back,
// it is hashed here. Adding a request field without adding it to Key is a
// correctness bug — a stale region replays under a different question.
// Fields are NUL-separated so no two different field splits can collide, and
// Text is normalized (trimmed, lower-cased) so trivially different spellings
// of the same search share one entry.
func (q Query) Key() string {
	bias := ""
	if q.Bias != nil {
		bias = strconv.FormatFloat(q.Bias.Latitude, 'f', 6, 64) + "," +
			strconv.FormatFloat(q.Bias.Longitude, 'f', 6, 64) + "," +
			strconv.FormatFloat(q.Bias.RadiusMeters, 'f', 2, 64)
	}
	h := sha256.New()
	for _, part := range []string{
		strings.ToLower(strings.TrimSpace(q.Text)),
		q.LanguageCode,
		q.RegionCode,
		bias,
		strconv.Itoa(q.limit()),
	} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Company is one normalized business.
//
// Flat and closed on purpose: there is no description, review text, or other
// unbounded free-text field, so a tool can return a Company without a size
// ceiling negotiation (SD-7). If a future field is free-text, it needs a
// clamp at the point it is added, not at the point it leaks.
type Company struct {
	PlaceID          string
	Name             string
	FormattedAddress string
	Latitude         float64
	Longitude        float64
	Rating           float64
	ReviewCount      int
	Website          string
	Phone            string
	// Types is Google's own type taxonomy for this place. internal/leadgen's
	// rule table maps it to a normalized category for free, before any model
	// is involved.
	Types          []string
	PrimaryType    string
	BusinessStatus string
	// Source is provenance: SourcePlacesAPI here, SourceScrape for the M5
	// fallback. Downstream stages treat scraped rows as lower-confidence.
	Source    string
	FetchedAt time.Time
}
