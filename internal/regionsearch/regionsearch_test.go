package regionsearch

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/logrenant/mimir/internal/maps"
)

type fakeScraper struct {
	calls  int
	result []maps.Company
	err    error
}

func (f *fakeScraper) Search(_ context.Context, _ maps.Query) ([]maps.Company, error) {
	f.calls++
	return f.result, f.err
}

type fakePlaces struct {
	calls  int
	result []maps.Company
	err    error
}

func (f *fakePlaces) SearchText(_ context.Context, _ maps.Query) ([]maps.Company, error) {
	f.calls++
	return f.result, f.err
}

func company(id string) maps.Company { return maps.Company{PlaceID: id, Name: id} }

// newTestRouter is the shipped two-source order: free scrape, then billed API.
func newTestRouter(scrape Scraper, places Places) *Router {
	return New(
		FromScraper(maps.SourceScrape, true, scrape),
		FromPlaces(places),
	)
}

// The order is the whole policy: free first, always, so a machine with a key
// spends nothing on a region the scrape can serve.
func TestSearch_TriesTheFreeSourceFirst(t *testing.T) {
	scrape := &fakeScraper{result: []maps.Company{company("s1")}}
	places := &fakePlaces{result: []maps.Company{company("p1")}}

	got, source, _, err := newTestRouter(scrape, places).Search(context.Background(), maps.Query{Text: "x"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].PlaceID != "s1" {
		t.Fatalf("got %+v, want the scraped row", got)
	}
	if source != maps.SourceScrape {
		t.Errorf("source: got %q, want %q", source, maps.SourceScrape)
	}
	if places.calls != 0 {
		t.Errorf("the billed source was called %d times, want 0", places.calls)
	}
}

func TestSearch_FallsBackToPlacesAndSaysItWasBilled(t *testing.T) {
	scrape := &fakeScraper{err: errors.New("sidecar down")}
	places := &fakePlaces{result: []maps.Company{company("p1")}}

	got, source, notes, err := newTestRouter(scrape, places).Search(context.Background(), maps.Query{Text: "x"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || source != maps.SourcePlacesAPI {
		t.Fatalf("got %+v from %q", got, source)
	}
	var sawFailure, sawBilled bool
	for _, n := range notes {
		if strings.Contains(n, "sidecar down") {
			sawFailure = true
		}
		if strings.Contains(n, "billed") {
			sawBilled = true
		}
	}
	if !sawFailure || !sawBilled {
		t.Errorf("notes must carry the failure and the price: %v", notes)
	}
}

// One provider is a complete configuration, and it is the shipped one on a
// machine with no Places key.
func TestSearch_ScrapeOnlyIsAValidConfiguration(t *testing.T) {
	r := New(FromScraper(maps.SourceScrape, true, &fakeScraper{result: []maps.Company{company("s1")}}))

	if !r.Available() || !r.Free() {
		t.Fatalf("a scrape-only router must be available and free: %+v", r.Sources())
	}
	if got := r.Sources(); len(got) != 1 || got[0] != maps.SourceScrape {
		t.Errorf("sources: %v", got)
	}
	if _, _, _, err := r.Search(context.Background(), maps.Query{Text: "x"}); err != nil {
		t.Errorf("Search: %v", err)
	}
}

// Three sources, and the order holds all the way down: the model-assisted
// provider is asked only after the sidecar fails, and Places only after both.
func TestSearch_WalksEveryFreeSourceBeforeABilledOne(t *testing.T) {
	sidecar := &fakeScraper{err: errors.New("sidecar down")}
	model := &fakeScraper{err: errors.New("feed did not render")}
	places := &fakePlaces{result: []maps.Company{company("p1")}}

	r := New(
		FromScraper(maps.SourceScrape, true, sidecar),
		FromScraper("mapsllm", true, model),
		FromPlaces(places),
	)
	if got := r.Sources(); len(got) != 3 || got[1] != "mapsllm" {
		t.Fatalf("sources: %v", got)
	}

	_, source, notes, err := r.Search(context.Background(), maps.Query{Text: "x"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if source != maps.SourcePlacesAPI {
		t.Errorf("source: got %q", source)
	}
	if sidecar.calls != 1 || model.calls != 1 || places.calls != 1 {
		t.Errorf("each source should be asked once: %d/%d/%d", sidecar.calls, model.calls, places.calls)
	}
	if len(notes) != 3 {
		t.Errorf("two failures and one billed note expected, got %v", notes)
	}
}

func TestSearch_NoSourceAndNoData(t *testing.T) {
	empty := New()
	if empty.Available() {
		t.Error("a router with no provider must not report itself available")
	}
	if _, _, _, err := empty.Search(context.Background(), maps.Query{Text: "x"}); !errors.Is(err, ErrNoSource) {
		t.Errorf("got %v, want ErrNoSource", err)
	}

	broken := newTestRouter(&fakeScraper{err: errors.New("down")}, &fakePlaces{err: errors.New("down")})
	if _, _, _, err := broken.Search(context.Background(), maps.Query{Text: "x"}); !errors.Is(err, ErrNoData) {
		t.Errorf("got %v, want ErrNoData", err)
	}
}

// A caller that went away is not a provider failure, and must not be reported
// as one — nor should it make the router try the next source.
func TestSearch_CancelledContextIsNotAProviderFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	places := &fakePlaces{result: []maps.Company{company("p1")}}
	_, _, _, err := newTestRouter(&fakeScraper{err: errors.New("stopped")}, places).Search(ctx, maps.Query{Text: "x"})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	if places.calls != 0 {
		t.Errorf("the billed source was called after cancellation: %d", places.calls)
	}
}
