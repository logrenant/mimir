// Package mapsllm is the model-assisted half of Maps region search.
//
// It does two jobs, both of them recovery rather than routine:
//
//   - It adapts internal/refine's feed-extraction profile to the seam
//     internal/mapscrape calls when its selectors read nothing. Google changes
//     that markup without notice, and a page that rendered results is worth
//     re-reading with a model before reporting an empty region.
//   - It is a region-search provider in its own right, for the machine where
//     the Playwright sidecar cannot run at all: fetch the same Maps results URL
//     through Crawl4AI, then read it the same way.
//
// The honest expectation for the second one is that it often finds nothing.
// The Maps feed is rendered by JavaScript, and internal/gmaps' package doc
// records what was already learned the hard way about Crawl4AI's ability to
// wait for it. It is a third chance, not a replacement for the sidecar, and it
// says so where an operator can read it.
//
// Neither job invents a business. The prompt forbids it, the profile drops any
// entry with no name, and every row this package produces carries
// mapscrape.PlaceIDPrefix so it can never overwrite a billed Places row.
package mapsllm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/crawl"
	"github.com/logrenant/mimir/internal/maps"
	"github.com/logrenant/mimir/internal/mapscrape"
	"github.com/logrenant/mimir/internal/refine"
)

// ErrNoPage means Crawl4AI returned nothing usable for the Maps URL — the
// normal outcome when the feed did not render, which is why it is its own
// error rather than a generic failure.
var ErrNoPage = errors.New("mapsllm: the maps page did not render anything to read")

// Extractor is internal/refine's feed profile. An interface so this package's
// tests need no model.
type Extractor interface {
	ExtractFeed(ctx context.Context, in refine.FeedInput) (refine.FeedOutput, error)
}

// Fetcher is internal/crawl's client. Same reason.
type Fetcher interface {
	MarkdownWithOptions(ctx context.Context, targetURL string, opts crawl.FetchOptions) (crawl.Page, error)
}

// Client is both the extractor adapter and the fallback provider.
type Client struct {
	cfg       config.Config
	extractor Extractor
	fetcher   Fetcher
}

func New(cfg config.Config, extractor Extractor, fetcher Fetcher) *Client {
	return &Client{cfg: cfg, extractor: extractor, fetcher: fetcher}
}

// ExtractPlaces satisfies mapscrape.FeedExtractor.
func (c *Client) ExtractPlaces(ctx context.Context, html string) ([]maps.Company, error) {
	if c == nil || c.extractor == nil {
		return nil, errors.New("mapsllm: no extractor configured")
	}

	out, err := c.extractor.ExtractFeed(ctx, refine.FeedInput{
		HTML:      html,
		MaxItems:  c.cfg.MapScrapeModelMaxItems,
		MaxTokens: c.cfg.MapScrapeModelMaxTokens,
	})
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	companies := make([]maps.Company, 0, len(out.Places))
	seen := make(map[string]struct{}, len(out.Places))
	for _, p := range out.Places {
		company, ok := mapscrape.CompanyFromModel(p.Name, p.Address, p.Website, p.MapsURL, p.Rating, p.Reviews, now)
		if !ok {
			continue
		}
		// The same business twice is a model repeating a card, not two
		// businesses. Dedupe on the id the row will be stored under.
		if _, dup := seen[company.PlaceID]; dup {
			continue
		}
		seen[company.PlaceID] = struct{}{}
		companies = append(companies, company)
	}
	return companies, nil
}

// Search satisfies the region-search provider seam: fetch the feed through
// Crawl4AI and read it with the model.
//
// It waits for the feed container and gives the render a generous budget,
// because the failure this is trying to survive is exactly a page that is slow
// to hydrate. When it still does not render, that is ErrNoPage — a true answer
// about this provider, and the router moves on to the next one.
func (c *Client) Search(ctx context.Context, q maps.Query) ([]maps.Company, error) {
	if c == nil || c.fetcher == nil {
		return nil, errors.New("mapsllm: no fetcher configured")
	}

	page, err := c.fetcher.MarkdownWithOptions(ctx, mapscrape.SearchURL(q), crawl.FetchOptions{
		WaitForSelector: c.cfg.MapScrapeWaitSelector,
		PageTimeout:     c.cfg.MapScrapeTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("mapsllm: fetching the maps feed: %w", err)
	}

	// RawHTML, not Markdown: the place links carry the feature id and the
	// coordinates in their URL grammar, and Markdown conversion drops the
	// attributes that hold them.
	html := page.RawHTML
	if html == "" {
		html = page.Markdown
	}
	trimmed := mapscrape.TrimFeedHTML(html, c.cfg.MapScrapeModelMaxChars)
	if trimmed == "" {
		return nil, ErrNoPage
	}

	companies, err := c.ExtractPlaces(ctx, trimmed)
	if err != nil {
		return nil, err
	}
	if len(companies) == 0 {
		return nil, ErrNoPage
	}

	limit := q.MaxResults
	if limit <= 0 || limit > c.cfg.MapScrapeMaxResults {
		limit = c.cfg.MapScrapeMaxResults
	}
	if len(companies) > limit {
		companies = companies[:limit]
	}
	return companies, nil
}
