package maps

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/logrenant/goat-mcp/internal/config"
)

const testKey = "test-places-key"

// newTestClient wires a Client at a httptest server. No live network, no real
// credential — every test in this file runs offline.
func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	c, err := New(config.Load(), Options{APIKey: testKey, BaseURL: srv.URL, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, body any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func TestNewRequiresAPIKey(t *testing.T) {
	_, err := New(config.Load(), Options{})
	if !errors.Is(err, ErrCredentialMissing) {
		t.Fatalf("want ErrCredentialMissing, got %v", err)
	}
}

func TestNewDefaultsToPinnedBaseURL(t *testing.T) {
	c, err := New(config.Load(), Options{APIKey: testKey})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.baseURL != DefaultBaseURL {
		t.Fatalf("baseURL = %q, want %q", c.baseURL, DefaultBaseURL)
	}
	if !strings.HasSuffix(DefaultBaseURL, "/v1") {
		t.Fatalf("DefaultBaseURL must pin a major version, got %q", DefaultBaseURL)
	}
}

func TestSearchTextSendsCredentialAndFieldMask(t *testing.T) {
	var (
		gotKey, gotMask, gotType string
		gotBody                  searchTextRequest
	)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Goog-Api-Key")
		gotMask = r.Header.Get("X-Goog-FieldMask")
		gotType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if !strings.HasSuffix(r.URL.Path, "/places:searchText") {
			t.Errorf("path = %q, want .../places:searchText", r.URL.Path)
		}
		writeJSON(t, w, http.StatusOK, searchTextResponse{})
	})

	q := Query{
		Text:         "  dentists in Kadıköy  ",
		LanguageCode: "tr",
		RegionCode:   "TR",
		Bias:         &Circle{Latitude: 40.99, Longitude: 29.02, RadiusMeters: 1500},
	}
	if _, err := c.SearchText(context.Background(), q); err != nil {
		t.Fatalf("SearchText: %v", err)
	}

	if gotKey != testKey {
		t.Errorf("X-Goog-Api-Key = %q, want %q", gotKey, testKey)
	}
	if gotMask != FieldMask || gotMask == "" {
		t.Errorf("X-Goog-FieldMask = %q, want %q", gotMask, FieldMask)
	}
	if gotType != "application/json" {
		t.Errorf("Content-Type = %q", gotType)
	}
	if gotBody.TextQuery != "dentists in Kadıköy" {
		t.Errorf("textQuery = %q, want trimmed query", gotBody.TextQuery)
	}
	if gotBody.PageSize != PageSize {
		t.Errorf("pageSize = %d, want %d", gotBody.PageSize, PageSize)
	}
	if gotBody.LanguageCode != "tr" || gotBody.RegionCode != "TR" {
		t.Errorf("language/region not sent: %+v", gotBody)
	}
	if gotBody.LocationBias == nil || gotBody.LocationBias.Circle.Radius != 1500 {
		t.Errorf("locationBias not sent: %+v", gotBody.LocationBias)
	}
}

func TestSearchTextNormalizesEveryField(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]any{
			"places": []map[string]any{{
				"id":                  "ChIJ_place_1",
				"displayName":         map[string]any{"text": "Acme Dental", "languageCode": "en"},
				"formattedAddress":    "1 Test Street, Istanbul",
				"location":            map[string]any{"latitude": 40.99, "longitude": 29.02},
				"rating":              4.6,
				"userRatingCount":     231,
				"websiteUri":          "https://acme.example",
				"nationalPhoneNumber": "0216 000 00 00",
				"types":               []string{"dentist", "health"},
				"primaryType":         "dentist",
				"businessStatus":      "OPERATIONAL",
			}},
		})
	})

	got, err := c.SearchText(context.Background(), Query{Text: "dentists"})
	if err != nil {
		t.Fatalf("SearchText: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}

	c0 := got[0]
	if c0.PlaceID != "ChIJ_place_1" || c0.Name != "Acme Dental" {
		t.Errorf("identity: %+v", c0)
	}
	if c0.FormattedAddress != "1 Test Street, Istanbul" || c0.Latitude != 40.99 || c0.Longitude != 29.02 {
		t.Errorf("location: %+v", c0)
	}
	if c0.Rating != 4.6 || c0.ReviewCount != 231 {
		t.Errorf("ratings: %+v", c0)
	}
	if c0.Website != "https://acme.example" || c0.Phone != "0216 000 00 00" {
		t.Errorf("contact: %+v", c0)
	}
	if len(c0.Types) != 2 || c0.PrimaryType != "dentist" || c0.BusinessStatus != "OPERATIONAL" {
		t.Errorf("taxonomy: %+v", c0)
	}
	if c0.Source != SourcePlacesAPI {
		t.Errorf("Source = %q, want %q", c0.Source, SourcePlacesAPI)
	}
	if c0.FetchedAt.IsZero() {
		t.Error("FetchedAt not set")
	}
}

// placesPage builds a response page of n places with ids prefixed by prefix.
func placesPage(prefix string, n int, next string) map[string]any {
	places := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		places = append(places, map[string]any{
			"id":          prefix + string(rune('a'+i)),
			"displayName": map[string]any{"text": "Company"},
		})
	}
	body := map[string]any{"places": places}
	if next != "" {
		body["nextPageToken"] = next
	}
	return body
}

func TestSearchTextPaginatesAndDeduplicates(t *testing.T) {
	var tokens []string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body searchTextRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		tokens = append(tokens, body.PageToken)

		switch body.PageToken {
		case "":
			writeJSON(t, w, http.StatusOK, placesPage("p1-", 3, "TOKEN-2"))
		case "TOKEN-2":
			// "p1-a" repeats: Places may re-rank between page requests.
			page := placesPage("p2-", 2, "")
			page["places"] = append([]map[string]any{{
				"id": "p1-a", "displayName": map[string]any{"text": "Duplicate"},
			}}, page["places"].([]map[string]any)...)
			writeJSON(t, w, http.StatusOK, page)
		default:
			t.Errorf("unexpected page token %q", body.PageToken)
		}
	})

	got, err := c.SearchText(context.Background(), Query{Text: "cafes"})
	if err != nil {
		t.Fatalf("SearchText: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("len = %d, want 5 (3 + 2 unique)", len(got))
	}
	if len(tokens) != 2 || tokens[0] != "" || tokens[1] != "TOKEN-2" {
		t.Fatalf("page tokens = %v", tokens)
	}
	seen := map[string]bool{}
	for _, c := range got {
		if seen[c.PlaceID] {
			t.Fatalf("duplicate place id %q survived", c.PlaceID)
		}
		seen[c.PlaceID] = true
	}
}

func TestSearchTextStopsOnRepeatedPageToken(t *testing.T) {
	calls := 0
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		// A malfunctioning server that always hands back the same token.
		writeJSON(t, w, http.StatusOK, placesPage("x", 1, "SAME"))
	})

	got, err := c.SearchText(context.Background(), Query{Text: "loop"})
	if err != nil {
		t.Fatalf("SearchText: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 (first page, then the repeated token ends it)", calls)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
}

func TestSearchTextHonoursResultCap(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, placesPage("p", 10, "NEXT"))
	})

	got, err := c.SearchText(context.Background(), Query{Text: "cafes", MaxResults: 4})
	if err != nil {
		t.Fatalf("SearchText: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4", len(got))
	}
}

func TestSearchTextClampsCapToAPIMaximum(t *testing.T) {
	pages := 0
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		pages++
		writeJSON(t, w, http.StatusOK, placesPage(string(rune('a'+pages))+"-", 20, "NEXT-"+string(rune('a'+pages))))
	})

	got, err := c.SearchText(context.Background(), Query{Text: "everything", MaxResults: 5000})
	if err != nil {
		t.Fatalf("SearchText: %v", err)
	}
	if len(got) != MaxResults {
		t.Fatalf("len = %d, want the API ceiling %d", len(got), MaxResults)
	}
}

func TestSearchTextEmptyResultIsNotAnError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, searchTextResponse{})
	})

	got, err := c.SearchText(context.Background(), Query{Text: "nothing here"})
	if err != nil {
		t.Fatalf("SearchText: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}

func TestSearchTextRejectsEmptyQuery(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("no request should be sent for an empty query")
	})
	if _, err := c.SearchText(context.Background(), Query{Text: "   "}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("want ErrBadRequest, got %v", err)
	}
}

func TestSearchTextStatusMapping(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		gstatus  string
		want     error
		wantCall int
	}{
		{"unauthorized", http.StatusUnauthorized, "UNAUTHENTICATED", ErrRequestDenied, 1},
		{"forbidden", http.StatusForbidden, "PERMISSION_DENIED", ErrRequestDenied, 1},
		{"too many requests", http.StatusTooManyRequests, "RESOURCE_EXHAUSTED", ErrQuotaExceeded, 1},
		{"bad request", http.StatusBadRequest, "INVALID_ARGUMENT", ErrBadRequest, 1},
		{"server error retries once", http.StatusInternalServerError, "INTERNAL", ErrUnavailable, 2},
		{"bad gateway retries once", http.StatusBadGateway, "", ErrUnavailable, 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				writeJSON(t, w, tc.status, map[string]any{"error": map[string]any{
					"code": tc.status, "message": "provider detail", "status": tc.gstatus,
				}})
			})

			_, err := c.SearchText(context.Background(), Query{Text: "q"})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if calls != tc.wantCall {
				t.Errorf("calls = %d, want %d", calls, tc.wantCall)
			}
			if strings.Contains(err.Error(), testKey) {
				t.Error("error message leaked the API key")
			}
			if !strings.Contains(err.Error(), "provider detail") {
				t.Errorf("provider detail dropped from %q", err)
			}
		})
	}
}

// A rejected key does not arrive as 401/403. Google answers an invalid or
// restricted key with 400 INVALID_ARGUMENT and names the real cause only in
// details[].reason — verified against the live endpoint with a bogus key,
// which returned exactly this envelope. Mapping it on status alone told the
// operator to rephrase a query that was never the problem.
func TestSearchTextCredentialRejectionArrivesAs400(t *testing.T) {
	for _, reason := range []string{"API_KEY_INVALID", "API_KEY_HTTP_REFERRER_BLOCKED", "SERVICE_DISABLED"} {
		t.Run(reason, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(t, w, http.StatusBadRequest, map[string]any{"error": map[string]any{
					"code": 400, "message": "API key not valid. Please pass a valid API key.", "status": "INVALID_ARGUMENT",
					"details": []map[string]any{
						{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": reason},
					},
				}})
			})

			_, err := c.SearchText(context.Background(), Query{Text: "q"})
			if !errors.Is(err, ErrRequestDenied) {
				t.Fatalf("err = %v, want ErrRequestDenied", err)
			}
			if strings.Contains(err.Error(), testKey) {
				t.Error("error message leaked the API key")
			}
		})
	}

	// A 400 that genuinely blames the request still maps to ErrBadRequest.
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, http.StatusBadRequest, map[string]any{"error": map[string]any{
			"code": 400, "message": "Invalid field mask", "status": "INVALID_ARGUMENT",
			"details": []map[string]any{{"reason": "FIELD_MASK_INVALID"}},
		}})
	})
	if _, err := c.SearchText(context.Background(), Query{Text: "q"}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("err = %v, want ErrBadRequest", err)
	}
}

func TestSearchTextQuotaByGoogleStatusAlone(t *testing.T) {
	// Google sometimes reports exhaustion with a 4xx other than 429.
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusForbidden, map[string]any{"error": map[string]any{
			"code": 403, "message": "quota", "status": "RESOURCE_EXHAUSTED",
		}})
	})
	if _, err := c.SearchText(context.Background(), Query{Text: "q"}); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("want ErrQuotaExceeded, got %v", err)
	}
}

func TestSearchTextRecoversOnRetry(t *testing.T) {
	calls := 0
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeJSON(t, w, http.StatusOK, placesPage("ok-", 1, ""))
	})

	got, err := c.SearchText(context.Background(), Query{Text: "q"})
	if err != nil {
		t.Fatalf("SearchText: %v", err)
	}
	if len(got) != 1 || calls != 2 {
		t.Fatalf("len = %d, calls = %d", len(got), calls)
	}
}

func TestSearchTextUnreachableEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	base := srv.URL
	srv.Close() // nothing is listening any more

	c, err := New(config.Load(), Options{APIKey: testKey, BaseURL: base})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.SearchText(context.Background(), Query{Text: "q"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
	if strings.Contains(err.Error(), testKey) {
		t.Error("error message leaked the API key")
	}
}

func TestSearchTextUnparseableBody(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte("{not json")); err != nil {
			t.Errorf("write: %v", err)
		}
	})
	if _, err := c.SearchText(context.Background(), Query{Text: "q"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
}

func TestSearchTextCancelledContextAbortsPagination(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		cancel() // the caller gives up while page 1 is in flight
		writeJSON(t, w, http.StatusOK, placesPage("p-", 1, "NEXT"))
	})

	_, err := c.SearchText(ctx, Query{Text: "q"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if calls > 2 {
		t.Fatalf("kept paginating after cancellation: %d calls", calls)
	}
}

func TestSearchTextDeadlineIsSurfacedNotMasked(t *testing.T) {
	// Answers, but later than the caller is willing to wait. A bounded sleep
	// rather than a block on r.Context(): a net/http server does not reliably
	// cancel a handler's context when an idle client gives up, so blocking
	// here would hang the server's own shutdown.
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		// The caller is already gone; whether this write lands is irrelevant.
		_, _ = w.Write([]byte(`{}`))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := c.SearchText(ctx, Query{Text: "q"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}

func TestSearchTextSkipsPlacesWithoutID(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]any{"places": []map[string]any{
			{"displayName": map[string]any{"text": "No id"}},
			{"id": "has-id", "displayName": map[string]any{"text": "Kept"}},
		}})
	})

	got, err := c.SearchText(context.Background(), Query{Text: "q"})
	if err != nil {
		t.Fatalf("SearchText: %v", err)
	}
	if len(got) != 1 || got[0].PlaceID != "has-id" {
		t.Fatalf("got %+v", got)
	}
}
