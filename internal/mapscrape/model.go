package mapscrape

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/logrenant/mimir/internal/maps"
)

// FeedExtractor is the model fallback for a feed the selectors could not read.
//
// A seam with this package's own types rather than internal/refine's: the
// adapter lives at the wiring site (internal/mapsllm), which keeps a scraper
// free of a dependency on the model layer and keeps this interface small
// enough to fake in a test.
type FeedExtractor interface {
	// ExtractPlaces reads businesses out of a rendered feed. An empty slice is
	// a valid answer — a page that genuinely listed nothing.
	ExtractPlaces(ctx context.Context, html string) ([]maps.Company, error)
}

// UseExtractor installs the model fallback.
//
// It is optional and set after construction, like llm.Router's environment
// builder, because the model layer is wired later than the scraper and a
// scraper with no fallback must stay a working scraper.
func (c *Client) UseExtractor(e FeedExtractor) { c.extractor = e }

// modelFallback runs the extractor over a feed the selectors found nothing in.
//
// Only that case reaches here. A feed that parsed one card out of twenty is a
// selector that still works on a page that had one result, and asking a model
// to second-guess it would cost money to make the answer *less* anchored: the
// selector path reads ids and coordinates out of URL grammar, which a model
// reading rendered text cannot do as well.
func (c *Client) modelFallback(ctx context.Context, html string) ([]maps.Company, error) {
	if c.extractor == nil {
		return nil, ErrNoResults
	}
	trimmed := TrimFeedHTML(html, c.cfg.MapScrapeModelMaxChars)
	if strings.TrimSpace(trimmed) == "" {
		return nil, ErrNoResults
	}
	companies, err := c.extractor.ExtractPlaces(ctx, trimmed)
	if err != nil {
		return nil, err
	}
	if len(companies) == 0 {
		return nil, ErrNoResults
	}
	return companies, nil
}

// TrimFeedHTML reduces a rendered page to something worth paying to read.
//
// A Maps feed is megabytes of inline script and base64 imagery; none of it is
// a business name, and all of it is billed as input tokens. Scripts, styles and
// SVG go first, then whitespace collapses, then the result is cut to maxChars.
//
// It is deliberately crude — regex-free, tag-boundary only. Anything cleverer
// would be a second HTML parser living beside the real one in parse.go.
func TrimFeedHTML(html string, maxChars int) string {
	for _, tag := range []string{"script", "style", "svg", "noscript"} {
		html = dropElements(html, tag)
	}
	html = strings.Join(strings.Fields(html), " ")
	if maxChars > 0 && len(html) > maxChars {
		html = html[:maxChars]
	}
	return html
}

// dropElements removes every <tag>...</tag> pair, unclosed tails included.
func dropElements(html, tag string) string {
	var b strings.Builder
	lower := strings.ToLower(html)
	open, closeTag := "<"+tag, "</"+tag

	for {
		start := strings.Index(lower, open)
		if start < 0 {
			b.WriteString(html)
			return b.String()
		}
		b.WriteString(html[:start])

		end := strings.Index(lower[start:], closeTag)
		if end < 0 {
			// An unclosed element runs to the end of the document; dropping
			// the tail is right, because that is what a browser would have
			// treated as script.
			return b.String()
		}
		cut := start + end
		if gt := strings.Index(lower[cut:], ">"); gt >= 0 {
			cut += gt + 1
		}
		html, lower = html[cut:], lower[cut:]
	}
}

// CompanyFromModel turns one extracted business into a Company.
//
// Provenance is the whole job here. The id comes from the Maps URL when the
// model reported one — the same feature id the selector path reads — and is
// otherwise derived from the name, so two runs over the same page produce the
// same row rather than a duplicate. Either way it carries PlaceIDPrefix: a
// model-read row must never be mistaken for, or overwrite, a billed one.
func CompanyFromModel(name, address, website, mapsURL string, rating float64, reviews int, fetchedAt time.Time) (maps.Company, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return maps.Company{}, false
	}

	id := placeIDFromHref(mapsURL)
	if id == "" {
		sum := sha256.Sum256([]byte(strings.ToLower(name) + "|" + strings.ToLower(address)))
		id = PlaceIDPrefix + "llm:" + hex.EncodeToString(sum[:8])
	}

	lat, lng := coordsFromHref(mapsURL)

	return maps.Company{
		PlaceID:          id,
		Name:             name,
		FormattedAddress: strings.TrimSpace(address),
		Website:          strings.TrimSpace(website),
		Latitude:         lat,
		Longitude:        lng,
		Rating:           rating,
		ReviewCount:      reviews,
		Source:           maps.SourceScrape,
		FetchedAt:        fetchedAt,
	}, true
}
