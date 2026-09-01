package maps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/logrenant/mimir/internal/config"
)

// maxResponseBytes bounds how much of a Places response is read. A full page
// of 20 places with this field mask is a few tens of KiB; anything past this
// is a malfunctioning endpoint, not data.
const maxResponseBytes = 4 << 20

// retryBackoff is the pause before the single retry of a 5xx/transport
// failure. One retry, fixed backoff — the same posture internal/crawl takes.
const retryBackoff = 500 * time.Millisecond

// Options carries the operator-provisioned credential and the two test seams.
type Options struct {
	// APIKey is the Places API key. Required. It is sent only in the
	// X-Goog-Api-Key header and never appears in a log line or an error.
	APIKey string
	// BaseURL overrides DefaultBaseURL. Test seam only — production callers
	// leave it empty (SD-1: this is not a user knob).
	BaseURL string
	// HTTPClient overrides the default client built from cfg.SearchTimeout.
	// Test seam only.
	HTTPClient *http.Client
}

// Client talks to the Places API (New).
type Client struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
}

// New builds a Client. It fails closed when no credential was provided: a
// keyless Places call is a guaranteed 403, so failing here names the real fix
// instead of surfacing an opaque denial later (SD-6).
//
// The HTTP timeout comes from cfg.SearchTimeout — this is a search-shaped
// call against a fast, first-party API, not a page render.
func New(cfg config.Config, opts Options) (*Client, error) {
	if opts.APIKey == "" {
		return nil, fmt.Errorf("%w — the Places key is operator-provisioned and injected by the parent process; "+
			"it is not a runtime setting the consumer can pass", ErrCredentialMissing)
	}

	base := opts.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	base = strings.TrimSuffix(base, "/")

	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: cfg.SearchTimeout}
	}

	return &Client{httpClient: httpClient, baseURL: base, apiKey: opts.APIKey}, nil
}

// searchTextRequest is the places:searchText body. Fields are omitempty so an
// unset Query sends the minimal request Places documents.
type searchTextRequest struct {
	TextQuery    string           `json:"textQuery"`
	PageSize     int              `json:"pageSize,omitempty"`
	PageToken    string           `json:"pageToken,omitempty"`
	LanguageCode string           `json:"languageCode,omitempty"`
	RegionCode   string           `json:"regionCode,omitempty"`
	LocationBias *locationBiasDTO `json:"locationBias,omitempty"`
}

type locationBiasDTO struct {
	Circle circleDTO `json:"circle"`
}

type circleDTO struct {
	Center latLngDTO `json:"center"`
	Radius float64   `json:"radius"`
}

type latLngDTO struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// searchTextResponse mirrors only the fields FieldMask requests.
type searchTextResponse struct {
	Places        []placeDTO `json:"places"`
	NextPageToken string     `json:"nextPageToken"`
}

type placeDTO struct {
	ID          string `json:"id"`
	DisplayName struct {
		Text string `json:"text"`
	} `json:"displayName"`
	FormattedAddress string    `json:"formattedAddress"`
	Location         latLngDTO `json:"location"`
	Rating           float64   `json:"rating"`
	UserRatingCount  int       `json:"userRatingCount"`
	WebsiteURI       string    `json:"websiteUri"`
	NationalPhone    string    `json:"nationalPhoneNumber"`
	Types            []string  `json:"types"`
	PrimaryType      string    `json:"primaryType"`
	BusinessStatus   string    `json:"businessStatus"`
}

// apiError is Google's standard error envelope.
type apiError struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
		// Details carries google.rpc.ErrorInfo, whose Reason is the only
		// machine-readable part of the envelope. It matters because a bad key
		// arrives as 400 INVALID_ARGUMENT, not 401/403 — see statusError.
		Details []struct {
			Reason string `json:"reason"`
		} `json:"details"`
	} `json:"error"`
}

// SearchText returns the companies Places reports for q.
//
// Pagination is a bounded loop: it stops at q's result cap, at an absent
// nextPageToken, or at a token the server repeats — never on the server's
// word alone (SD-3). Duplicate place ids across pages collapse to one
// Company, because Places may repeat a place when its ranking shifts between
// page requests.
//
// An empty result set is not an error: "no companies here" is a legitimate,
// cacheable answer.
func (c *Client) SearchText(ctx context.Context, q Query) ([]Company, error) {
	if strings.TrimSpace(q.Text) == "" {
		return nil, fmt.Errorf("%w: empty text query — pass what you would type into Google Maps", ErrBadRequest)
	}

	limit := q.limit()
	// The API tops out at MaxResults/PageSize pages; the +1 guards against a
	// server that keeps issuing tokens.
	maxPages := MaxResults/PageSize + 1

	var (
		out       []Company
		seen      = make(map[string]struct{}, limit)
		pageToken string
		seenToken = make(map[string]struct{}, maxPages)
		fetchedAt = time.Now().UTC()
	)

	for page := 0; page < maxPages && len(out) < limit; page++ {
		resp, err := c.searchPage(ctx, q, pageToken)
		if err != nil {
			return nil, err
		}

		for _, p := range resp.Places {
			if p.ID == "" {
				continue
			}
			if _, dup := seen[p.ID]; dup {
				continue
			}
			seen[p.ID] = struct{}{}
			out = append(out, toCompany(p, fetchedAt))
			if len(out) >= limit {
				break
			}
		}

		if resp.NextPageToken == "" {
			break
		}
		if _, repeated := seenToken[resp.NextPageToken]; repeated {
			break
		}
		seenToken[resp.NextPageToken] = struct{}{}
		pageToken = resp.NextPageToken
	}

	return out, nil
}

// searchPage performs one places:searchText call, retrying once on a
// transport failure or 5xx.
func (c *Client) searchPage(ctx context.Context, q Query, pageToken string) (searchTextResponse, error) {
	body := searchTextRequest{
		TextQuery:    strings.TrimSpace(q.Text),
		PageSize:     PageSize,
		PageToken:    pageToken,
		LanguageCode: q.LanguageCode,
		RegionCode:   q.RegionCode,
	}
	if q.Bias != nil {
		body.LocationBias = &locationBiasDTO{Circle: circleDTO{
			Center: latLngDTO{Latitude: q.Bias.Latitude, Longitude: q.Bias.Longitude},
			Radius: q.Bias.RadiusMeters,
		}}
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return searchTextResponse{}, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}

	endpoint := c.baseURL + "/places:searchText"

	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		if attempt > 1 {
			select {
			case <-ctx.Done():
				return searchTextResponse{}, ctx.Err()
			case <-time.After(retryBackoff):
			}
		}

		out, retryable, err := c.doSearch(ctx, endpoint, payload)
		if err == nil {
			return out, nil
		}
		// A cancelled or expired caller context is the caller's answer, not a
		// provider outage: surface it as-is and stop.
		if ctx.Err() != nil {
			return searchTextResponse{}, ctx.Err()
		}
		lastErr = err
		if !retryable {
			return searchTextResponse{}, err
		}
	}
	return searchTextResponse{}, lastErr
}

// doSearch is one HTTP attempt. The bool reports whether the failure is worth
// retrying.
func (c *Client) doSearch(ctx context.Context, endpoint string, payload []byte) (searchTextResponse, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return searchTextResponse{}, false, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goog-Api-Key", c.apiKey)
	req.Header.Set("X-Goog-FieldMask", FieldMask)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Deliberately does not wrap err: a transport error's message can
		// contain the request URL, and the key must never reach a log.
		return searchTextResponse{}, true, fmt.Errorf(
			"%w: could not reach the Places API — check network access and that the Places API (New) is enabled for this project", ErrUnavailable)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return searchTextResponse{}, true, fmt.Errorf("%w: truncated response body", ErrUnavailable)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return searchTextResponse{}, resp.StatusCode >= 500, statusError(resp.StatusCode, raw)
	}

	var out searchTextResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return searchTextResponse{}, false, fmt.Errorf("%w: unparseable JSON from the Places API (status %d)", ErrUnavailable, resp.StatusCode)
	}
	return out, false, nil
}

// statusError maps an HTTP status (and Google's error envelope, when present)
// to a sentinel a caller can act on.
func statusError(status int, raw []byte) error {
	var env apiError
	_ = json.Unmarshal(raw, &env)
	detail := strings.TrimSpace(env.Error.Message)
	gstatus := env.Error.Status

	switch {
	// Checked before the status switch: a rejected credential does not arrive
	// as 401 or 403. Google answers an invalid, blocked, or restricted key
	// with 400 INVALID_ARGUMENT and puts the real cause in details[].reason
	// ("API_KEY_INVALID", "API_KEY_HTTP_REFERRER_BLOCKED", …). Reading only the
	// status maps that to ErrBadRequest, and the operator is told to rephrase
	// a query that was never the problem.
	case isCredentialReason(env):
		return fmt.Errorf("%w: the Places key was rejected — confirm the key is valid, the Places API (New) is enabled, and any key restriction allows this machine (%s)",
			ErrRequestDenied, describe(detail))
	case status == http.StatusTooManyRequests || gstatus == "RESOURCE_EXHAUSTED":
		return fmt.Errorf("%w: the project's Places quota or billing cap is spent — raise the quota or wait for the window to reset (%s)",
			ErrQuotaExceeded, describe(detail))
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fmt.Errorf("%w: the Places key was rejected — confirm the key is valid, the Places API (New) is enabled, and any key restriction allows this machine (%s)",
			ErrRequestDenied, describe(detail))
	case status == http.StatusBadRequest || status == http.StatusNotFound:
		return fmt.Errorf("%w: status %d (%s)", ErrBadRequest, status, describe(detail))
	default:
		return fmt.Errorf("%w: status %d (%s)", ErrUnavailable, status, describe(detail))
	}
}

// isCredentialReason reports whether Google's error envelope blames the key
// rather than the request. Every key-related reason it emits is in the
// API_KEY_* family; SERVICE_DISABLED means the key is fine but the Places API
// is not switched on for its project — the same fix, from the operator's side.
func isCredentialReason(env apiError) bool {
	for _, d := range env.Error.Details {
		if strings.HasPrefix(d.Reason, "API_KEY_") || d.Reason == "SERVICE_DISABLED" {
			return true
		}
	}
	return false
}

// describe renders the provider's message, or a placeholder when it sent none.
func describe(detail string) string {
	if detail == "" {
		return "no detail returned"
	}
	return detail
}

func toCompany(p placeDTO, fetchedAt time.Time) Company {
	return Company{
		PlaceID:          p.ID,
		Name:             p.DisplayName.Text,
		FormattedAddress: p.FormattedAddress,
		Latitude:         p.Location.Latitude,
		Longitude:        p.Location.Longitude,
		Rating:           p.Rating,
		ReviewCount:      p.UserRatingCount,
		Website:          p.WebsiteURI,
		Phone:            p.NationalPhone,
		Types:            append([]string(nil), p.Types...),
		PrimaryType:      p.PrimaryType,
		BusinessStatus:   p.BusinessStatus,
		Source:           SourcePlacesAPI,
		FetchedAt:        fetchedAt,
	}
}
