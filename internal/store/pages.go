package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/logrenant/mimir/internal/crawl"
	"github.com/logrenant/mimir/internal/refine"
)

// HashContent returns the digest used for RefineKey.ContentHash.
func HashContent(markdown string) string {
	sum := sha256.Sum256([]byte(markdown))
	return hex.EncodeToString(sum[:])
}

func hashURL(url string) string {
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:])
}

// RefineKey identifies one refine result.
//
// A refine output is a function of the page content, the query, the token
// ceiling, and the prompt template — so every one of those participates in the
// key. In particular ContentHash makes a changed page miss naturally: correct
// invalidation with no cache-busting logic.
type RefineKey struct {
	URL           string
	Query         string
	MaxTokens     int
	ContentHash   string
	PromptVersion string
}

// id is the primary key for this refine result. Fields are NUL-separated so
// no two different field splits can produce the same digest.
func (k RefineKey) id() string {
	h := sha256.New()
	for _, part := range []string{
		k.URL,
		k.Query,
		strconv.Itoa(k.MaxTokens),
		k.ContentHash,
		k.PromptVersion,
	} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// expired reports whether a row cached at unix second cachedAt has outlived
// ttl. A non-positive ttl means "never cache" — always expired.
func expired(cachedAt int64, ttl time.Duration) bool {
	if ttl <= 0 {
		return true
	}
	return time.Since(time.Unix(cachedAt, 0)) > ttl
}

// GetCrawl returns the cached raw page for url, if present and within ttl.
//
// SD-2: the markdown returned here is RAW, untrusted scraped text. It exists
// only to be fed into internal/refine. It must never reach an MCP tool
// response — see internal/store/AGENTS.md.
//
// Deliberately does not select raw_html even though the column exists (added
// by 0003 for GetCrawlFull, below) — this keeps the "flows only into refine"
// invariant true by construction for every caller of this method, not just
// by convention.
func (s *Store) GetCrawl(ctx context.Context, url string, ttl time.Duration) (crawl.Page, bool, error) {
	if s == nil || s.db == nil {
		return crawl.Page{}, false, nil
	}

	var (
		title, markdown     string
		fetchedAt, cachedAt int64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT title, markdown, fetched_at, cached_at FROM crawl_pages WHERE url_hash = ?`,
		hashURL(url),
	).Scan(&title, &markdown, &fetchedAt, &cachedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return crawl.Page{}, false, nil
	}
	if err != nil {
		return crawl.Page{}, false, unavailable(err)
	}
	if expired(cachedAt, ttl) {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM crawl_pages WHERE url_hash = ?`, hashURL(url))
		return crawl.Page{}, false, nil
	}

	return crawl.Page{
		URL:       url,
		Title:     title,
		Markdown:  markdown,
		FetchedAt: time.Unix(fetchedAt, 0).UTC(),
	}, true, nil
}

// GetCrawlFull is GetCrawl plus RawHTML.
//
// SD-2 justification: unlike GetCrawl, this result may leave internal/refine
// out of its path entirely. Its only caller is pipeline.FetchRaw, which
// serves Stage F tools (internal/ecommerce, internal/tiktok, internal/gmaps,
// internal/instagram) — those parse RawHTML with internal/extract into
// clamped, structured fields (JSON-LD, Open Graph, embedded SPA JSON) and
// never return the HTML itself in an MCP tool response. If a future caller
// wants to return RawHTML verbatim, that is a new decision this comment does
// not authorize.
func (s *Store) GetCrawlFull(ctx context.Context, url string, ttl time.Duration) (crawl.Page, bool, error) {
	if s == nil || s.db == nil {
		return crawl.Page{}, false, nil
	}

	var (
		title, markdown, rawHTML string
		fetchedAt, cachedAt      int64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT title, markdown, raw_html, fetched_at, cached_at FROM crawl_pages WHERE url_hash = ?`,
		hashURL(url),
	).Scan(&title, &markdown, &rawHTML, &fetchedAt, &cachedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return crawl.Page{}, false, nil
	}
	if err != nil {
		return crawl.Page{}, false, unavailable(err)
	}
	if expired(cachedAt, ttl) {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM crawl_pages WHERE url_hash = ?`, hashURL(url))
		return crawl.Page{}, false, nil
	}

	return crawl.Page{
		URL:       url,
		Title:     title,
		Markdown:  markdown,
		RawHTML:   rawHTML,
		FetchedAt: time.Unix(fetchedAt, 0).UTC(),
	}, true, nil
}

// PutCrawl stores (or replaces) the raw page for page.URL, including
// RawHTML — written unconditionally so a row populated via the refine path
// (cachedCrawl) still serves a later FetchRaw hit and vice versa; GetCrawl
// simply never selects the column back out (see its doc comment above).
func (s *Store) PutCrawl(ctx context.Context, page crawl.Page) error {
	if s == nil || s.db == nil {
		return nil
	}
	if page.URL == "" {
		return nil
	}

	fetchedAt := page.FetchedAt
	if fetchedAt.IsZero() {
		fetchedAt = time.Now()
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO crawl_pages (url_hash, url, title, markdown, raw_html, fetched_at, cached_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(url_hash) DO UPDATE SET
			title      = excluded.title,
			markdown   = excluded.markdown,
			raw_html   = excluded.raw_html,
			fetched_at = excluded.fetched_at,
			cached_at  = excluded.cached_at`,
		hashURL(page.URL), page.URL, page.Title, page.Markdown, page.RawHTML,
		fetchedAt.Unix(), time.Now().Unix())
	if err != nil {
		return unavailable(err)
	}
	return nil
}

// GetRefined returns the cached refine output for k, if present and within ttl.
func (s *Store) GetRefined(ctx context.Context, k RefineKey, ttl time.Duration) (refine.Output, bool, error) {
	if s == nil || s.db == nil {
		return refine.Output{}, false, nil
	}

	var (
		text          string
		truncated     int
		tokenEstimate int
		cachedAt      int64
	)
	err := s.db.QueryRowContext(ctx,
		`SELECT text, truncated, token_estimate, cached_at FROM refined_pages WHERE refine_key = ?`,
		k.id(),
	).Scan(&text, &truncated, &tokenEstimate, &cachedAt)

	if errors.Is(err, sql.ErrNoRows) {
		return refine.Output{}, false, nil
	}
	if err != nil {
		return refine.Output{}, false, unavailable(err)
	}
	if expired(cachedAt, ttl) {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM refined_pages WHERE refine_key = ?`, k.id())
		return refine.Output{}, false, nil
	}

	// Refined is always true on a hit: only a successful Distil is ever stored.
	return refine.Output{
		Text:          text,
		Refined:       true,
		Truncated:     truncated != 0,
		TokenEstimate: tokenEstimate,
	}, true, nil
}

// PutRefined stores (or replaces) the refine output for k.
func (s *Store) PutRefined(ctx context.Context, k RefineKey, out refine.Output) error {
	if s == nil || s.db == nil {
		return nil
	}
	// Never cache a failed or unrefined result: replaying it would hand the
	// consumer text that never passed the refiner (SD-2).
	if !out.Refined {
		return nil
	}

	truncated := 0
	if out.Truncated {
		truncated = 1
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO refined_pages
			(refine_key, url, query, max_tokens, content_hash, prompt_version,
			 text, truncated, token_estimate, cached_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(refine_key) DO UPDATE SET
			text           = excluded.text,
			truncated      = excluded.truncated,
			token_estimate = excluded.token_estimate,
			cached_at      = excluded.cached_at`,
		k.id(), k.URL, k.Query, k.MaxTokens, k.ContentHash, k.PromptVersion,
		out.Text, truncated, out.TokenEstimate, time.Now().Unix())
	if err != nil {
		return unavailable(err)
	}
	return nil
}
