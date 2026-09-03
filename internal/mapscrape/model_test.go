package mapscrape

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/maps"
)

type fakeExtractor struct {
	calls   int
	gotHTML string
	out     []maps.Company
	err     error
}

func (f *fakeExtractor) ExtractPlaces(_ context.Context, html string) ([]maps.Company, error) {
	f.calls++
	f.gotHTML = html
	return f.out, f.err
}

// sidecarServing answers /search with this HTML and /health with 200.
func sidecarServing(t *testing.T, html string) (config.Config, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"html": html})
	}))
	cfg := config.Load()
	cfg.MapScrapeBaseURL = srv.URL
	cfg.PerHostMinInterval = 0
	return cfg, srv.Close
}

// Google moves the markup, the selectors read nothing, and the page is still in
// hand: the model reads it rather than the region being reported empty.
func TestSearch_ModelRecoversAFeedTheSelectorsCouldNotRead(t *testing.T) {
	cfg, stop := sidecarServing(t, `<html><body><div role="feed"><div class="new-markup">A Diş 4,9</div></div></body></html>`)
	defer stop()

	extractor := &fakeExtractor{out: []maps.Company{{PlaceID: PlaceIDPrefix + "llm:1", Name: "A Diş"}}}
	c := New(cfg)
	c.UseExtractor(extractor)

	got, err := c.Search(context.Background(), maps.Query{Text: "x"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].Name != "A Diş" {
		t.Fatalf("got %+v", got)
	}
	if extractor.calls != 1 {
		t.Errorf("the extractor ran %d times, want 1", extractor.calls)
	}
}

// A feed the selectors *could* read never reaches the model: the selector path
// reads ids and coordinates out of URL grammar, which a model reading rendered
// text cannot do as well, and asking would cost money to get a worse answer.
func TestSearch_AParsedFeedNeverReachesTheModel(t *testing.T) {
	feed := `<html><body><div role="feed">
		<a aria-label="A Diş" href="/maps/place/A/data=!1s0x14cab8:0x1234!3d40.9!4d29.0"></a>
	</div></body></html>`
	cfg, stop := sidecarServing(t, feed)
	defer stop()

	extractor := &fakeExtractor{}
	c := New(cfg)
	c.UseExtractor(extractor)

	got, err := c.Search(context.Background(), maps.Query{Text: "x"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("the selectors should have parsed one card: %+v", got)
	}
	if extractor.calls != 0 {
		t.Errorf("the model was asked about a feed that parsed fine")
	}
}

// With no extractor wired, an unreadable feed is still ErrNoResults — a
// scraper without a fallback is a working scraper.
func TestSearch_NoExtractorStillReportsNoResults(t *testing.T) {
	cfg, stop := sidecarServing(t, `<html><body>nothing here</body></html>`)
	defer stop()

	_, err := New(cfg).Search(context.Background(), maps.Query{Text: "x"})
	if !errors.Is(err, ErrNoResults) {
		t.Fatalf("got %v, want ErrNoResults", err)
	}
}

func TestTrimFeedHTML_DropsScriptsAndCaps(t *testing.T) {
	html := `<div>keep<script>var a="` + strings.Repeat("x", 200) + `";</script>me</div>`

	got := TrimFeedHTML(html, 0)
	if strings.Contains(got, "var a") {
		t.Errorf("script survived: %s", got)
	}
	if !strings.Contains(got, "keep") || !strings.Contains(got, "me") {
		t.Errorf("content was dropped: %s", got)
	}
	if capped := TrimFeedHTML(html, 10); len(capped) != 10 {
		t.Errorf("cap not applied: %q", capped)
	}
	// An unclosed script runs to the end of the document, exactly as a browser
	// would treat it.
	if got := TrimFeedHTML(`<div>a</div><script>tail`, 0); strings.Contains(got, "tail") {
		t.Errorf("an unclosed script's tail survived: %q", got)
	}
}

func TestCompanyFromModel_RefusesANamelessRow(t *testing.T) {
	if _, ok := CompanyFromModel("  ", "addr", "", "", 0, 0, time.Time{}); ok {
		t.Error("a row with no name is not a business")
	}
}
