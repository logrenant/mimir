package crawl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/logrenant/goat-mcp/internal/config"
)

var (
	ErrDockerUnavailable = errors.New("crawl: crawl4ai container unavailable")
	ErrEmptyResult       = errors.New("crawl: empty markdown result")
)

// ImageTag is the pinned Crawl4AI Docker image (see deploy/crawl4ai/docker-compose.yml).
const ImageTag = "unclecode/crawl4ai:0.8.9"

// Page holds the returned data from a crawl.
type Page struct {
	URL         string
	Title       string
	Markdown    string
	RawHTML string // Crawl4AI's raw "html" field (fallback "cleaned_html").
	// Needed to extract structured data (JSON-LD, embedded SPA JSON, Open
	// Graph meta tags) that both Markdown conversion AND Crawl4AI's own
	// "cleaned_html" strip out — verified empirically against a live
	// container: cleaned_html drops every <script> and <meta> tag and most
	// element attributes, so it cannot carry JSON-LD, embedded state blobs,
	// or OG metadata. Prefer raw html for any structured extraction.
	FetchedAt time.Time
}

// FetchOptions are optional Crawl4AI crawler_config passthrough knobs. The
// zero value reproduces exactly today's request — additive, not breaking.
type FetchOptions struct {
	// WaitForSelector is a CSS selector (or a "js:"-prefixed expression, per
	// Crawl4AI's own wait_for contract) Crawl4AI waits for before returning —
	// needed for JS-heavy single-page-app targets.
	WaitForSelector string
	// PageTimeout overrides Crawl4AI's internal page-render timeout for slow
	// SPA targets. Zero means "use Crawl4AI's own default".
	PageTimeout time.Duration
}

// Client interacts with the Crawl4AI container.
type Client struct {
	httpClient *http.Client
	cfg        config.Config
}

// New creates a new Client.
func New(cfg config.Config) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: cfg.CrawlTimeout},
		cfg:        cfg,
	}
}

// Health performs a cheap GET to check if the container is ready.
func (c *Client) Health(ctx context.Context) error {
	reqURL, err := url.JoinPath(c.cfg.Crawl4AIBaseURL, "health")
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: Crawl4AI not reachable on %s — run `make crawl-up` (cause: %v)", ErrDockerUnavailable, c.cfg.Crawl4AIBaseURL, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: non-2xx from health endpoint (status %d)", ErrDockerUnavailable, resp.StatusCode)
	}

	return nil
}

type crawlerConfig struct {
	WaitFor     string `json:"wait_for,omitempty"`
	PageTimeout int    `json:"page_timeout,omitempty"` // milliseconds
}

type crawlRequest struct {
	// Crawl4AI 0.8.9's real /crawl endpoint requires a JSON array under
	// "urls", even for a single URL — {"url": "..."} is rejected with a 422
	// ("Field required: urls"). Verified empirically against the pinned
	// image; see internal/crawl/AGENTS.md.
	URLs          []string       `json:"urls"`
	CrawlerConfig *crawlerConfig `json:"crawler_config,omitempty"`
}

// Markdown POSTs the target URL to Crawl4AI and returns the cleaned Markdown.
// Equivalent to MarkdownWithOptions(ctx, targetURL, FetchOptions{}).
func (c *Client) Markdown(ctx context.Context, targetURL string) (Page, error) {
	return c.MarkdownWithOptions(ctx, targetURL, FetchOptions{})
}

// MarkdownWithOptions POSTs the target URL to Crawl4AI and returns the
// cleaned Markdown plus cleaned HTML, honouring optional render knobs.
//
// Pinned image endpoint shape (v0.8.9), verified against a live container:
//
//	POST /crawl {"urls": ["..."], "crawler_config": {"wait_for": "...", "page_timeout": 5000}}
//	→ {"success": true, "results": [{"url", "success", "error_message",
//	   "cleaned_html", "html", "metadata": {"title"}, "markdown": {"raw_markdown"}}]}
//
// A legacy/alternate flat shape ({"markdown": "...", "title": "..."} at the
// top level, "markdown" as a plain string) is tolerated as a fallback.
func (c *Client) MarkdownWithOptions(ctx context.Context, targetURL string, opts FetchOptions) (Page, error) {
	reqURL, err := url.JoinPath(c.cfg.Crawl4AIBaseURL, "crawl")
	if err != nil {
		return Page{}, err
	}

	if err := c.waitPolite(ctx, targetURL); err != nil {
		return Page{}, err
	}

	payload := crawlRequest{URLs: []string{targetURL}}
	if opts.WaitForSelector != "" || opts.PageTimeout > 0 {
		cc := &crawlerConfig{WaitFor: opts.WaitForSelector}
		if opts.PageTimeout > 0 {
			cc.PageTimeout = int(opts.PageTimeout / time.Millisecond)
		}
		payload.CrawlerConfig = cc
	}
	bodyData, err := json.Marshal(payload)
	if err != nil {
		return Page{}, err
	}

	// Retry logic: one retry with short backoff on 5xx or timeout
	var resp *http.Response
	var reqErr error
	for attempt := 1; attempt <= 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(bodyData))
		if err != nil {
			return Page{}, err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, reqErr = c.httpClient.Do(req)
		if reqErr != nil {
			// Check if context was cancelled
			if errors.Is(reqErr, context.Canceled) || errors.Is(reqErr, context.DeadlineExceeded) {
				return Page{}, reqErr
			}
			// Transient network error
			time.Sleep(100 * time.Millisecond)
			continue
		}

		if resp.StatusCode >= 500 && attempt < 2 {
			_ = resp.Body.Close()
			time.Sleep(100 * time.Millisecond)
			continue
		}
		break
	}

	if reqErr != nil {
		return Page{}, fmt.Errorf("%w: Crawl4AI not reachable on %s — run `make crawl-up` (cause: %v)", ErrDockerUnavailable, c.cfg.Crawl4AIBaseURL, reqErr)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Page{}, fmt.Errorf("%w: non-2xx status code %d", ErrDockerUnavailable, resp.StatusCode)
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return Page{}, fmt.Errorf("failed to read response: %w", err)
	}

	var raw struct {
		Results []struct {
			URL          string          `json:"url"`
			Success      bool            `json:"success"`
			ErrorMessage string          `json:"error_message"`
			CleanedHTML  string          `json:"cleaned_html"`
			HTML         string          `json:"html"`
			Metadata     struct {
				Title string `json:"title"`
			} `json:"metadata"`
			Markdown json.RawMessage `json:"markdown"`
		} `json:"results"`
		// Legacy/alternate flat shape, tolerated as a fallback.
		Markdown json.RawMessage `json:"markdown"`
		Title    string          `json:"title"`
	}
	if err := json.Unmarshal(respBytes, &raw); err != nil {
		return Page{}, fmt.Errorf("failed to parse JSON response: %w", err)
	}

	var md, title, rawHTML string

	if len(raw.Results) > 0 {
		r0 := raw.Results[0]
		title = r0.Metadata.Title
		rawHTML = r0.HTML
		if rawHTML == "" {
			rawHTML = r0.CleanedHTML
		}
		md = extractMarkdownString(r0.Markdown)
		if !r0.Success && md == "" {
			detail := r0.ErrorMessage
			if detail == "" {
				detail = "crawl4ai reported failure with no detail"
			}
			return Page{}, fmt.Errorf("%w: crawl4ai failed to fetch %s: %s", ErrDockerUnavailable, targetURL, detail)
		}
	} else {
		title = raw.Title
		md = extractMarkdownString(raw.Markdown)
	}

	if md == "" {
		return Page{}, ErrEmptyResult
	}

	return Page{
		URL:       targetURL,
		Title:     title,
		Markdown:  md,
		RawHTML:   rawHTML,
		FetchedAt: time.Now(),
	}, nil
}

// extractMarkdownString handles both Crawl4AI 0.8.9's real "markdown" shape
// (an object, {"raw_markdown": "..."}) and a plain string (legacy/fallback
// shape). Returns "" (never an error) when neither shape matches — callers
// treat empty markdown as ErrEmptyResult.
func extractMarkdownString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var obj struct {
		RawMarkdown string `json:"raw_markdown"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj.RawMarkdown
	}
	return ""
}
