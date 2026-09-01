//go:build integration

package mapscrape_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/maps"
	"github.com/logrenant/mimir/internal/mapscrape"
)

// This file is the only thing that can prove the extraction selectors still
// match what Google actually serves — the unit tests prove the parser handles
// the shape it is given, not that the shape is still real. Run it by hand
// after any change here, and after any week in which Maps was redesigned:
//
//	make maps-up && go test -v -tags=integration ./internal/mapscrape
//
// It talks to Google. Keep it out of anything automated.

func TestIntegration_SidecarHealth(t *testing.T) {
	cfg := config.Load()
	client := mapscrape.New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ok, err := client.Health(ctx)
	if err != nil || !ok {
		t.Fatalf("sidecar health failed, is it running (`make maps-up`)? %v", err)
	}
}

// TestIntegration_SidecarRefusesNonMapsURL is the security boundary, tested
// where it actually lives. The Go client only ever sends Maps URLs, so this
// posts to the sidecar directly: a loopback service with a browser attached
// must not fetch the host's own network, a file:// path, or a cloud metadata
// endpoint for anyone who can reach the port.
func TestIntegration_SidecarRefusesNonMapsURL(t *testing.T) {
	cfg := config.Load()

	for _, target := range []string{
		"http://127.0.0.1:11235/health",
		"http://169.254.169.254/latest/meta-data/",
		"file:///etc/passwd",
		"https://evil.example/?x=www.google.com/maps/",
		"https://www.google.com/search?q=x",
		"https://google.com.evil.example/maps/search/x",
	} {
		t.Run(target, func(t *testing.T) {
			body := strings.NewReader(`{"url":"` + target + `"}`)
			resp, err := http.Post(cfg.MapScrapeBaseURL+"/search", "application/json", body)
			if err != nil {
				t.Fatalf("post: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 — the sidecar accepted a non-Maps URL", resp.StatusCode)
			}
		})
	}
}

// TestIntegration_SearchLive is the selector check. It is deliberately loose
// about *what* it finds and strict about the shape: a region query must return
// namespaced ids, real names, and plausible coordinates. If it returns nothing,
// the feed's markup has moved and this package needs a look — that is the
// signal this test exists to produce.
func TestIntegration_SearchLive(t *testing.T) {
	cfg := config.Load()
	client := mapscrape.New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	companies, err := client.Search(ctx, maps.Query{
		Text:         "dentists in Kadıköy Istanbul",
		LanguageCode: "en",
		RegionCode:   "TR",
		MaxResults:   20,
	})
	if errors.Is(err, mapscrape.ErrBlocked) {
		t.Skipf("google served a consent or block page — not a code failure: %v", err)
	}
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(companies) < 5 {
		t.Fatalf("got %d companies for a dense urban query; the feed selectors have probably moved", len(companies))
	}

	rated := 0
	for _, c := range companies {
		if !strings.HasPrefix(c.PlaceID, mapscrape.PlaceIDPrefix) {
			t.Errorf("%q: un-namespaced id %q", c.Name, c.PlaceID)
		}
		if c.Name == "" {
			t.Error("a company came back with no name")
		}
		if c.Latitude == 0 || c.Longitude == 0 {
			t.Errorf("%q: no coordinates parsed from its URL", c.Name)
		}
		if c.Rating > 0 {
			rated++
		}
		t.Logf("%-40s %-28s %.1f (%d) %s", c.Name, c.PlaceID, c.Rating, c.ReviewCount, c.Website)
	}

	// Ratings are the most redesign-prone field. Most established businesses in
	// a dense area have one, so none at all means the rating label moved.
	if rated == 0 {
		t.Error("no company had a rating; the rating aria-label has probably changed shape")
	}
}
