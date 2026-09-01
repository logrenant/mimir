package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/crawl"
	"github.com/logrenant/mimir/internal/ecommerce"
	"github.com/logrenant/mimir/internal/mcp"
)

// RawFetcher is the narrow interface every free-scraper tool (ecommerce,
// tiktok, gmaps, instagram) needs: a raw, non-refined single-page crawl.
// pipeline.Pipeline satisfies it via FetchRaw.
type RawFetcher interface {
	FetchRaw(ctx context.Context, url string, opts crawl.FetchOptions) (crawl.Page, error)
}

// EcommerceLookupTool extracts a normalized Product from a product page's
// schema.org JSON-LD (falling back to Open Graph metadata) — a free,
// self-written replacement for a paid product-data API. No refine call: the
// extracted fields are small deterministic facts, not prose to distil (see
// internal/extract.ClampText for the isolation control instead).
type EcommerceLookupTool struct {
	fetcher RawFetcher
	cfg     config.Config
}

// NewEcommerceLookup creates a new EcommerceLookupTool.
func NewEcommerceLookup(cfg config.Config, fetcher RawFetcher) *EcommerceLookupTool {
	return &EcommerceLookupTool{fetcher: fetcher, cfg: cfg}
}

func (t *EcommerceLookupTool) Name() string { return "ecommerce_product_lookup" }

func (t *EcommerceLookupTool) Description() string {
	return "Look up a product page (any storefront exposing schema.org Product JSON-LD or Open Graph tags — Amazon and most independent stores included) and return normalized fields: name, brand, price, rating, review count. Free, self-scraped, no API key. Best-effort: pages without Product JSON-LD or Open Graph metadata return an error."
}

func (t *EcommerceLookupTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"url": { "type": "string", "minLength": 1, "description": "Direct product page URL. Must start with http:// or https://" }
		},
		"required": ["url"],
		"additionalProperties": false
	}`)
}

type ecommerceLookupArgs struct {
	URL string `json:"url"`
}

type ecommerceLookupResponse struct {
	URL             string  `json:"url"`
	Name            string  `json:"name"`
	Brand           string  `json:"brand,omitempty"`
	Price           float64 `json:"price,omitempty"`
	Currency        string  `json:"currency,omitempty"`
	AvailabilityRaw string  `json:"availability,omitempty"`
	Rating          float64 `json:"rating,omitempty"`
	ReviewCount     int     `json:"review_count,omitempty"`
	Description     string  `json:"description,omitempty"`
	ImageURL        string  `json:"image_url,omitempty"`

	budget int `json:"-"`
}

func (r ecommerceLookupResponse) MetadataOnly() bool    { return true }
func (r ecommerceLookupResponse) SizeBudgetTokens() int { return r.budget }

func (t *EcommerceLookupTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var input ecommerceLookupArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	u, err := url.Parse(input.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("url must be a valid http or https URL")
	}

	page, err := t.fetcher.FetchRaw(ctx, input.URL, crawl.FetchOptions{})
	if err != nil {
		if errors.Is(err, crawl.ErrDockerUnavailable) {
			return nil, errors.New("crawl4ai not reachable — run `make crawl-up`")
		}
		return nil, err
	}

	product, err := ecommerce.Extract(t.cfg, page)
	if err != nil {
		if errors.Is(err, ecommerce.ErrNoProductData) {
			return nil, errors.New("page has no Product JSON-LD or Open Graph metadata — not extractable with the free scraper")
		}
		return nil, err
	}

	return ecommerceLookupResponse{
		URL:             product.URL,
		Name:            product.Name,
		Brand:           product.Brand,
		Price:           product.Price,
		Currency:        product.Currency,
		AvailabilityRaw: product.AvailabilityRaw,
		Rating:          product.Rating,
		ReviewCount:     product.ReviewCount,
		Description:     product.Description,
		ImageURL:        product.ImageURL,
		budget:          t.cfg.EcommerceLookupMaxTokens,
	}, nil
}

var _ mcp.Tool = (*EcommerceLookupTool)(nil)
