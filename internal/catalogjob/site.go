package catalogjob

import (
	"context"
	"time"

	"github.com/logrenant/mimir/internal/catalog"
	"github.com/logrenant/mimir/internal/crawl"
)

// The adapter between the crawler and the studio's storefront scan.
//
// It lives here for the reason research.go gives: internal/catalog is imported
// by internal/store, so it states its own one-method shape and the translation
// happens where both halves already meet.

// Fetcher is internal/crawl's client, narrowed to the one call the scan makes.
type Fetcher interface {
	MarkdownWithOptions(ctx context.Context, url string, opts crawl.FetchOptions) (crawl.Page, error)
}

// Site adapts a crawler into the studio's SiteReader.
func Site(f Fetcher) catalog.SiteReader { return &siteReader{f: f} }

type siteReader struct{ f Fetcher }

// sitePageTimeout is what a storefront gets to finish rendering.
//
// Longer than a plain fetch on purpose: the scan measures *computed* styles, so
// the page has to have applied its own CSS and web fonts before the probe runs.
// A theme that streams its stylesheet late would otherwise be measured as the
// browser's defaults and stored as the brand's look, which is the one failure
// this whole feature would have no way to show an operator.
const sitePageTimeout = 45 * time.Second

func (r *siteReader) Fetch(ctx context.Context, url, js string) (string, error) {
	page, err := r.f.MarkdownWithOptions(ctx, url, crawl.FetchOptions{
		JSCode:      js,
		PageTimeout: sitePageTimeout,
	})
	if err != nil {
		return "", err
	}
	// RawHTML, never Markdown: the probe's answer rides on an attribute of the
	// document element, and Markdown conversion drops every attribute there is.
	return page.RawHTML, nil
}
