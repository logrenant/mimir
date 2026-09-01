package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/crawl"
	"github.com/logrenant/mimir/internal/gmaps"
	"github.com/logrenant/mimir/internal/mcp"
)

// GMapsBusinessLookupTool looks up a business on Google Maps — free,
// self-scraped, no API key, no Places API. Google Maps' info panel
// (rating/address/phone) loads via an undocumented, obfuscated internal
// data blob that did not reliably render during this tool's development;
// only the business name is reliably extracted (see internal/gmaps's
// package doc for what was verified against a live container). Other
// fields are best-effort and are reported missing via "notes", never
// fabricated.
type GMapsBusinessLookupTool struct {
	fetcher RawFetcher
	cfg     config.Config
}

// NewGMapsBusinessLookup creates a new GMapsBusinessLookupTool.
func NewGMapsBusinessLookup(cfg config.Config, fetcher RawFetcher) *GMapsBusinessLookupTool {
	return &GMapsBusinessLookupTool{fetcher: fetcher, cfg: cfg}
}

func (t *GMapsBusinessLookupTool) Name() string { return "gmaps_business_lookup" }

func (t *GMapsBusinessLookupTool) Description() string {
	return "Look up a business on Google Maps by free-text query (name + city) or a direct Maps place URL. Free, self-scraped, no API key. Reliably returns the business name; rating/address/phone/website are best-effort and may be missing (see the notes field) — Google Maps' info panel loads via an undocumented internal format this tool cannot always parse."
}

func (t *GMapsBusinessLookupTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": { "type": "string", "minLength": 1, "description": "Free-text business name + location, e.g. 'Blue Bottle Coffee, San Francisco'" },
			"url": { "type": "string", "minLength": 1, "description": "A direct google.com/maps/place/... URL, if you already have one" }
		},
		"additionalProperties": false
	}`)
}

type gmapsLookupArgs struct {
	Query string `json:"query"`
	URL   string `json:"url"`
}

type gmapsLookupResponse struct {
	SourceURL string   `json:"source_url"`
	Name      string   `json:"name"`
	Address   string   `json:"address,omitempty"`
	Phone     string   `json:"phone,omitempty"`
	Website   string   `json:"website,omitempty"`
	Rating    float64  `json:"rating,omitempty"`
	Notes     []string `json:"notes,omitempty"`

	budget int `json:"-"`
}

func (r gmapsLookupResponse) MetadataOnly() bool    { return true }
func (r gmapsLookupResponse) SizeBudgetTokens() int { return r.budget }

func (t *GMapsBusinessLookupTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var input gmapsLookupArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	targetURL := input.URL
	switch {
	case targetURL != "":
		u, err := url.Parse(targetURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, errors.New("url must be a valid http or https URL")
		}
	case input.Query != "":
		targetURL = gmaps.SearchURL(input.Query)
	default:
		return nil, errors.New("either query or url must be provided")
	}

	page, err := t.fetcher.FetchRaw(ctx, targetURL, crawl.FetchOptions{
		WaitForSelector: t.cfg.GMapsWaitForSelector,
		PageTimeout:     t.cfg.GMapsPageTimeout,
	})
	if err != nil {
		if errors.Is(err, crawl.ErrDockerUnavailable) {
			return nil, errors.New("crawl4ai not reachable — run `make crawl-up`")
		}
		return nil, err
	}

	place, err := gmaps.Extract(t.cfg, page)
	if err != nil {
		if errors.Is(err, gmaps.ErrNotAPlacePage) {
			return nil, errors.New("page did not render a specific place — try a more specific query, or a direct place URL")
		}
		return nil, err
	}

	return gmapsLookupResponse{
		SourceURL: targetURL,
		Name:      place.Name,
		Address:   place.Address,
		Phone:     place.Phone,
		Website:   place.Website,
		Rating:    place.Rating,
		Notes:     place.Notes,
		budget:    t.cfg.GMapsLookupMaxTokens,
	}, nil
}

var _ mcp.Tool = (*GMapsBusinessLookupTool)(nil)
