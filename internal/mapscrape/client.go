package mapscrape

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/maps"
)

// maxResponseBytes bounds what a compromised or broken sidecar can make this
// process allocate. The sidecar caps its own HTML at 4MB; this is that plus
// room for the JSON envelope, and it is enforced here too because a client
// must not trust a server's promise about its own size.
const maxResponseBytes = 6 << 20

// Client drives the Playwright sidecar.
//
// Same shape as internal/crawl.Client on purpose: a thin HTTP caller over one
// local container, with per-host politeness, an explicit timeout, and typed
// errors that name their fix. It holds no store and no credential.
type Client struct {
	httpClient *http.Client
	cfg        config.Config
}

func New(cfg config.Config) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: cfg.MapScrapeTimeout},
		cfg:        cfg,
	}
}

func (c *Client) unavailable(cause error) error {
	return fmt.Errorf("%w at %s: %v — run `make maps-up` to start it",
		ErrSidecarUnavailable, c.cfg.MapScrapeBaseURL, cause)
}

// Health reports whether the sidecar is up. Used by the diagnostics tool, so a
// down container is answerable without reading docker logs (SD-6).
func (c *Client) Health(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.MapScrapeBaseURL+"/health", nil)
	if err != nil {
		return false, c.unavailable(err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, c.unavailable(err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	if resp.StatusCode != http.StatusOK {
		return false, c.unavailable(fmt.Errorf("health returned %d", resp.StatusCode))
	}
	return true, nil
}

type searchRequest struct {
	URL          string `json:"url"`
	WaitSelector string `json:"wait_selector"`
	MaxResults   int    `json:"max_results"`
	MaxScrolls   int    `json:"max_scrolls"`
	Locale       string `json:"locale"`
}

type searchResponse struct {
	HTML        string `json:"html"`
	ResultCount int    `json:"result_count"`
	Scrolls     int    `json:"scrolls"`
	Truncated   bool   `json:"truncated"`
	Error       string `json:"error"`
}

// Search renders one region query's results feed and extracts the companies in
// it.
//
// Takes and returns exactly what internal/maps.Client.SearchText does, so a
// caller can fall back to this provider without reshaping anything, and both
// share maps.Query.Key() as a cache key. One attempt per call: this is someone
// else's site, and a retry loop against it is not politeness, it is load.
func (c *Client) Search(ctx context.Context, q maps.Query) ([]maps.Company, error) {
	if strings.TrimSpace(q.Text) == "" {
		return nil, fmt.Errorf("%w: empty query text", maps.ErrBadRequest)
	}

	target := SearchURL(q)
	if err := waitPolite(ctx, c.cfg.PerHostMinInterval, target); err != nil {
		return nil, err
	}

	limit := q.MaxResults
	if limit <= 0 || limit > c.cfg.MapScrapeMaxResults {
		limit = c.cfg.MapScrapeMaxResults
	}

	body, err := json.Marshal(searchRequest{
		URL:          target,
		WaitSelector: c.cfg.MapScrapeWaitSelector,
		MaxResults:   limit,
		MaxScrolls:   c.cfg.MapScrapeMaxScrolls,
		Locale:       q.LanguageCode,
	})
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, c.cfg.MapScrapeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.MapScrapeBaseURL+"/search", bytes.NewReader(body))
	if err != nil {
		return nil, c.unavailable(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// A cancelled or timed-out caller is not a broken container, and must
		// not be reported as one.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, c.unavailable(err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, c.unavailable(err)
	}

	var payload searchResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, c.unavailable(fmt.Errorf("sidecar returned unparseable JSON (%d bytes, status %d)", len(raw), resp.StatusCode))
	}

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusConflict:
		// The sidecar's own signal that Google answered with a consent wall or
		// a captcha. A different problem from a broken container, and not one
		// this repo tries to defeat.
		return nil, fmt.Errorf("%w: %s", ErrBlocked, payload.Error)
	case http.StatusBadRequest:
		return nil, fmt.Errorf("%w: sidecar rejected the request: %s", maps.ErrBadRequest, payload.Error)
	default:
		return nil, c.unavailable(fmt.Errorf("status %d: %s", resp.StatusCode, payload.Error))
	}

	companies, skipped, err := ParseFeed(payload.HTML, time.Now().UTC())
	if err != nil {
		return nil, err
	}

	// Trim to the caller's cap: the feed loads in pages, so the last scroll
	// routinely overshoots.
	if len(companies) > limit {
		companies = companies[:limit]
	}

	// Logged, not returned: a caller gets companies, an operator gets the
	// numbers that show whether the selectors are still finding what the feed
	// rendered. A growing `skipped`, or `parsed` far below `rendered`, is how a
	// silent Maps redesign announces itself.
	slog.Info("mapscrape feed extracted",
		"parsed", len(companies),
		"skipped", skipped,
		"rendered", payload.ResultCount,
		"scrolls", payload.Scrolls,
		"truncated", payload.Truncated,
	)

	return companies, nil
}
