// Package ecommerce extracts a normalized Product from an already-crawled
// product page: schema.org "Product" JSON-LD first (the stable, widely
// adopted contract most real storefronts — Amazon included — emit),
// falling back to Open Graph meta tags. Deliberately no site-specific CSS
// selectors: page DOMs churn, JSON-LD/OpenGraph is the sturdier bet.
package ecommerce

import (
	"errors"
	"fmt"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/crawl"
	"github.com/logrenant/goat-mcp/internal/extract"
)

// ErrNoProductData means the page exposed neither a Product JSON-LD block
// nor Open Graph title/description — nothing usable to extract.
var ErrNoProductData = errors.New("ecommerce: page has no Product JSON-LD or Open Graph metadata")

// Product is the normalized shape returned regardless of source page.
type Product struct {
	URL             string
	Name            string
	Brand           string
	Price           float64
	Currency        string
	AvailabilityRaw string
	Rating          float64
	ReviewCount     int
	Description     string
	ImageURL        string
}

// Extract pulls a Product out of an already-crawled page's RawHTML.
func Extract(cfg config.Config, page crawl.Page) (Product, error) {
	doc, err := extract.Document(page.RawHTML)
	if err != nil {
		return Product{}, fmt.Errorf("ecommerce: parse html: %w", err)
	}

	blocks := extract.JSONLD(doc)
	if block, ok := extract.FindJSONLDByType(blocks, "Product"); ok {
		return fromJSONLD(cfg, page.URL, block), nil
	}

	title := extract.Meta(doc, "og:title")
	desc := extract.Meta(doc, "og:description")
	img := extract.Meta(doc, "og:image")
	if title == "" && desc == "" {
		return Product{}, ErrNoProductData
	}

	return Product{
		URL:         page.URL,
		Name:        title,
		Description: extract.ClampText(desc, cfg.ProductDescriptionMaxChars),
		ImageURL:    img,
	}, nil
}

func fromJSONLD(cfg config.Config, url string, block map[string]any) Product {
	p := Product{URL: url}
	if name, ok := block["name"].(string); ok {
		p.Name = name
	}
	p.Brand = brandName(block["brand"])
	if desc, ok := block["description"].(string); ok {
		p.Description = extract.ClampText(desc, cfg.ProductDescriptionMaxChars)
	}
	p.ImageURL = firstImage(block["image"])

	if offer := firstOffer(block["offers"]); offer != nil {
		if price, ok := toFloat(offer["price"]); ok {
			p.Price = price
		}
		if cur, ok := offer["priceCurrency"].(string); ok {
			p.Currency = cur
		}
		if avail, ok := offer["availability"].(string); ok {
			p.AvailabilityRaw = avail
		}
	}

	if rating, ok := block["aggregateRating"].(map[string]any); ok {
		if rv, ok := toFloat(rating["ratingValue"]); ok {
			p.Rating = rv
		}
		if rc, ok := toInt(rating["reviewCount"]); ok {
			p.ReviewCount = rc
		}
	}
	return p
}

// brandName handles schema.org's "brand" as either a plain string or a
// {"@type":"Brand","name":"..."} object.
func brandName(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		if n, ok := t["name"].(string); ok {
			return n
		}
	}
	return ""
}

// firstImage handles "image" as a string, a []string-ish array (schema.org
// allows one or many), or an ImageObject with a "url" field.
func firstImage(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		if len(t) > 0 {
			return firstImage(t[0])
		}
	case map[string]any:
		if u, ok := t["url"].(string); ok {
			return u
		}
	}
	return ""
}

// firstOffer handles "offers" as either a single Offer object or an array
// of them (AggregateOffer-style multi-seller listings) — we take the first.
func firstOffer(v any) map[string]any {
	switch t := v.(type) {
	case map[string]any:
		return t
	case []any:
		if len(t) > 0 {
			if m, ok := t[0].(map[string]any); ok {
				return m
			}
		}
	}
	return nil
}

// toFloat handles a JSON-LD numeric field encoded as either a JSON number
// (unmarshalled to float64) or, very commonly in the wild, a JSON string.
func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case string:
		return extract.ParseFloatLoose(t)
	}
	return 0, false
}

func toInt(v any) (int, bool) {
	switch t := v.(type) {
	case float64:
		return int(t), true
	case string:
		return extract.ParseCompactInt(t)
	}
	return 0, false
}
