package search

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/logrenant/mimir/internal/config"
	"golang.org/x/time/rate"
)

var ErrSearchUnavailable = errors.New("search: duckduckgo unavailable")

// healthDrainLimit caps how much of the probe response is read before the
// connection is put back: enough to reuse the socket, never the whole page.
const healthDrainLimit = 64 << 10

type Result struct {
	Title   string
	URL     string
	Snippet string
}

type Client struct {
	httpClient *http.Client
	cfg        config.Config
	limiter    *rate.Limiter
}

func New(cfg config.Config) *Client {
	// Enforce cfg.PerHostMinInterval between outbound calls.
	var limiter *rate.Limiter
	if cfg.PerHostMinInterval > 0 {
		limiter = rate.NewLimiter(rate.Every(cfg.PerHostMinInterval), 1)
	}

	return &Client{
		httpClient: &http.Client{
			// The SD-3 rule requires context timeouts, which we set on the request.
			// We also have a transport default timeout.
		},
		cfg:     cfg,
		limiter: limiter,
	}
}

// Health checks if DuckDuckGo is reachable.
//
// It probes with a GET and the same browser User-Agent the real search path
// sends, because that is the only request shape DuckDuckGo actually answers:
// a HEAD is rejected with 400 whatever the User-Agent, so probing with one
// reported the search backend as down while web_search was working fine. The
// body is discarded — this answers "is the endpoint reachable", not "does
// this query return results".
func (c *Client) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.SearchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.DuckDuckGoLiteURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", browserUserAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: failed to connect to DuckDuckGo — check your network (cause: %v)", ErrSearchUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Drain so the connection can be reused by the search that follows.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, healthDrainLimit))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: GET returned status code %d", ErrSearchUnavailable, resp.StatusCode)
	}
	return nil
}

// Search queries DuckDuckGo and returns up to 'count' results.
func (c *Client) Search(ctx context.Context, query string, count int) ([]Result, error) {
	if count <= 0 {
		count = c.cfg.SearchDefaultCount
	}
	if count > c.cfg.SearchMaxCount {
		count = c.cfg.SearchMaxCount
	}

	if c.limiter != nil {
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, err
		}
	}

	// Try HTML endpoint first
	results, err := c.searchHTML(ctx, query)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
	}
	if err != nil || len(results) == 0 {
		// Fallback to Lite endpoint
		results, err = c.searchLite(ctx, query)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
		}
		if err != nil || len(results) == 0 {
			return nil, ErrSearchUnavailable
		}
	}

	// Deduplicate by normalized URL
	var unique []Result
	seen := make(map[string]bool)
	for _, r := range results {
		if !seen[r.URL] {
			seen[r.URL] = true
			unique = append(unique, r)
		}
	}

	// Cap at count
	if len(unique) > count {
		unique = unique[:count]
	}

	return unique, nil
}
