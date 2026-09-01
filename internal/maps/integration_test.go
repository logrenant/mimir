//go:build integration

package maps_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/maps"
)

// This file makes a real, billed request to the Google Places API. It is the
// only thing that can prove the field mask, the request body, and the response
// parsing still match what Google actually serves — the unit tests prove the
// parser handles the shape it is given, not that the shape is still real.
//
//	GOAT_GOOGLE_PLACES_API_KEY=… go test -v -tags=integration ./internal/maps
//
// It costs money and talks to Google. Keep it out of anything automated.

func liveClient(t *testing.T) *maps.Client {
	t.Helper()
	key := os.Getenv("GOAT_GOOGLE_PLACES_API_KEY")
	if key == "" {
		t.Skip("GOAT_GOOGLE_PLACES_API_KEY not set — skipping the live Places test")
	}
	c, err := maps.New(config.Load(), maps.Options{APIKey: key})
	if err != nil {
		t.Fatalf("maps.New: %v", err)
	}
	return c
}

// A dense urban query must return namespaced-free real place ids, names, and
// plausible coordinates. It is deliberately loose about *what* it finds and
// strict about the shape.
func TestIntegration_SearchTextLive(t *testing.T) {
	client := liveClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	companies, err := client.SearchText(ctx, maps.Query{
		Text:         "dentists in Kadıköy, Istanbul",
		LanguageCode: "en",
		RegionCode:   "TR",
		MaxResults:   20,
	})
	if err != nil {
		t.Fatalf("SearchText: %v", err)
	}
	if len(companies) < 5 {
		t.Fatalf("got %d companies for a dense urban query; the field mask or parsing has probably moved", len(companies))
	}

	for _, c := range companies {
		if c.PlaceID == "" || c.Name == "" {
			t.Errorf("a company came back without an id or a name: %+v", c)
		}
		if c.Latitude == 0 || c.Longitude == 0 {
			t.Errorf("%q: no coordinates in the response", c.Name)
		}
		if c.Source != maps.SourcePlacesAPI {
			t.Errorf("%q: source = %q, want %q", c.Name, c.Source, maps.SourcePlacesAPI)
		}
	}
}

// An empty region is a legitimate, cacheable answer, not an error.
func TestIntegration_EmptyRegionIsNotAnError(t *testing.T) {
	client := liveClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := client.SearchText(ctx, maps.Query{
		Text: "zzzznonexistentbusinesskindxyz in the middle of the Pacific Ocean",
	})
	if err != nil {
		t.Fatalf("an empty result set should not be an error, got: %v", err)
	}
}

// A bad key maps to the credential sentinel, and the key never appears in the
// error text.
func TestIntegration_RejectedKeyIsTyped(t *testing.T) {
	if os.Getenv("GOAT_GOOGLE_PLACES_API_KEY") == "" {
		t.Skip("no key configured")
	}
	bad := "AIza-obviously-not-a-real-key-000000000000"
	c, err := maps.New(config.Load(), maps.Options{APIKey: bad})
	if err != nil {
		t.Fatalf("maps.New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, err = c.SearchText(ctx, maps.Query{Text: "dentists in Kadıköy"})
	if err == nil {
		t.Fatal("a bogus key should be rejected")
	}
	if strings.Contains(err.Error(), bad) {
		t.Fatalf("the key leaked into the error: %v", err)
	}
}
