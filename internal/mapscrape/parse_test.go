package mapscrape

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/logrenant/goat-mcp/internal/maps"
)

func loadFixture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "feed.html"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func parseFixture(t *testing.T) ([]maps.Company, int) {
	t.Helper()
	companies, skipped, err := ParseFeed(loadFixture(t), time.Unix(1700000000, 0).UTC())
	if err != nil {
		t.Fatalf("ParseFeed: %v", err)
	}
	return companies, skipped
}

func TestParseFeed_ExtractsCompanies(t *testing.T) {
	companies, skipped := parseFixture(t)

	if len(companies) != 4 {
		t.Fatalf("got %d companies, want 4: %+v", len(companies), companies)
	}
	// Two anchors are unusable — one with no aria-label, one with no feature id
	// — and both must be counted rather than silently dropped.
	if skipped != 2 {
		t.Errorf("skipped = %d, want 2", skipped)
	}

	first := companies[0]
	if first.Name != "Acme Dental" {
		t.Errorf("name = %q", first.Name)
	}
	if first.PlaceID != PlaceIDPrefix+"0x14caba1234567890:0xabcdef0123456789" {
		t.Errorf("place id = %q", first.PlaceID)
	}
	if first.Rating != 4.6 || first.ReviewCount != 231 {
		t.Errorf("rating/reviews = %v/%d, want 4.6/231", first.Rating, first.ReviewCount)
	}
	if first.Website != "https://acme-dental.example/" {
		t.Errorf("website = %q", first.Website)
	}
	if first.Latitude != 41.0082376 || first.Longitude != 28.9783589 {
		t.Errorf("coords = %v,%v", first.Latitude, first.Longitude)
	}
	if first.Source != maps.SourceScrape {
		t.Errorf("source = %q, want %q", first.Source, maps.SourceScrape)
	}
	if first.FetchedAt.IsZero() {
		t.Error("FetchedAt must be set")
	}
}

// The feed is localized: "4,5" is four-and-a-half, and "1.024" is a thousand
// and twenty-four. Reading either with the wrong separator convention produces
// a plausible, wrong number — 45 stars, or 1 review.
func TestParseFeed_LocalizedNumbers(t *testing.T) {
	companies, _ := parseFixture(t)

	var got maps.Company
	for _, c := range companies {
		if strings.HasPrefix(c.Name, "Kadıköy") {
			got = c
		}
	}
	if got.Name == "" {
		t.Fatal("the localized card was not parsed at all")
	}
	if got.Rating != 4.5 {
		t.Errorf("rating = %v, want 4.5", got.Rating)
	}
	if got.ReviewCount != 1024 {
		t.Errorf("review count = %d, want 1024", got.ReviewCount)
	}
	// Its only link is Google's own directions URL, which is navigation, not a
	// business website.
	if got.Website != "" {
		t.Errorf("website = %q, want empty", got.Website)
	}
}

// An unrated business is normal — a new one, or one nobody reviewed. Its
// fields must be empty, and the aria-label that happens to start with a house
// number must not be read as a score.
func TestParseFeed_MissingFieldsStayEmpty(t *testing.T) {
	companies, _ := parseFixture(t)

	var got maps.Company
	for _, c := range companies {
		if c.Name == "Brand New Clinic" {
			got = c
		}
	}
	if got.Name == "" {
		t.Fatal("the unrated card was not parsed")
	}
	if got.Rating != 0 || got.ReviewCount != 0 {
		t.Errorf("rating/reviews = %v/%d, want 0/0 — a street number is not a rating", got.Rating, got.ReviewCount)
	}
	if got.Website != "" {
		t.Errorf("website = %q, want empty", got.Website)
	}
}

// Southern and western coordinates are negative; a parser that drops the sign
// puts the business on the other side of the planet.
func TestParseFeed_NegativeCoordinates(t *testing.T) {
	companies, _ := parseFixture(t)

	for _, c := range companies {
		if c.Name != "Brand New Clinic" {
			continue
		}
		if c.Latitude != -33.86882 || c.Longitude != 151.20929 {
			t.Errorf("coords = %v,%v; want -33.86882,151.20929", c.Latitude, c.Longitude)
		}
		return
	}
	t.Fatal("card not found")
}

// The feed links the same place more than once (the card, then a secondary
// link). One business, one row.
func TestParseFeed_DeduplicatesByPlaceID(t *testing.T) {
	companies, _ := parseFixture(t)

	seen := map[string]int{}
	for _, c := range companies {
		seen[c.PlaceID]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("place id %q appears %d times", id, n)
		}
	}
}

// A scraped id must never be mistakable for a Places id: store.companies is
// keyed by place_id, so a collision would overwrite a billed row with a
// scraped one.
func TestParseFeed_IDsAreNamespaced(t *testing.T) {
	companies, _ := parseFixture(t)

	for _, c := range companies {
		if !strings.HasPrefix(c.PlaceID, PlaceIDPrefix) {
			t.Errorf("place id %q is not namespaced", c.PlaceID)
		}
		// Places ids look like "ChIJN1t_tDeuEmsRUsoyG83frY4" — no colon, no 0x.
		if !strings.Contains(strings.TrimPrefix(c.PlaceID, PlaceIDPrefix), ":") {
			t.Errorf("place id %q does not look like a Maps feature id", c.PlaceID)
		}
	}
}

// The regression that the live run found: Google serves two variants of the
// rating label, and the bare one carries no count. Reading a parenthesised
// number from elsewhere in the card turns an Istanbul phone number into 216
// reviews. An absent count is zero — never filled in from the nearest number.
func TestParseFeed_PhoneNumberIsNotAReviewCount(t *testing.T) {
	companies, _ := parseFixture(t)

	var got maps.Company
	for _, c := range companies {
		if strings.HasPrefix(c.Name, "Diş Polikliniği") {
			got = c
		}
	}
	if got.Name == "" {
		t.Fatal("the bare-label card was not parsed")
	}
	if got.Rating != 4.9 {
		t.Errorf("rating = %v, want 4.9 from the bare label", got.Rating)
	}
	if got.ReviewCount != 0 {
		t.Errorf("review count = %d, want 0 — the card has no count, only a phone number", got.ReviewCount)
	}
}

func TestParseFeed_EmptyFeedIsTyped(t *testing.T) {
	_, _, err := ParseFeed(`<div role="feed"></div>`, time.Now())
	if err == nil || !strings.Contains(err.Error(), "no usable results") {
		t.Errorf("err = %v, want ErrNoResults", err)
	}
}

// One broken card must not cost the others.
func TestParseFeed_SurvivesGarbage(t *testing.T) {
	html := `<div role="feed">
		<div><a href="/not/a/place" aria-label="Wrong Link"></a></div>
		<div><a href="https://www.google.com/maps/place/X/data=!1s0x1:0x2!3d1.0!4d2.0" aria-label="Real Business"></a></div>
		<div><a href="https://www.google.com/maps/place/Y/data=!1sgarbage" aria-label="No ID"></a></div>
	</div>`

	companies, skipped, err := ParseFeed(html, time.Now())
	if err != nil {
		t.Fatalf("ParseFeed: %v", err)
	}
	if len(companies) != 1 || companies[0].Name != "Real Business" {
		t.Errorf("got %+v, want just Real Business", companies)
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}
}

func TestSearchURL(t *testing.T) {
	cases := []struct {
		name  string
		query maps.Query
		want  []string
		omit  []string
	}{
		{
			name:  "text only",
			query: maps.Query{Text: "dentists in Kadıköy"},
			want:  []string{"https://www.google.com/maps/search/", "Kad"},
			omit:  []string{"@", "hl=", "gl="},
		},
		{
			name: "with bias and locale",
			query: maps.Query{
				Text:         "dentists",
				LanguageCode: "tr",
				RegionCode:   "TR",
				Bias:         &maps.Circle{Latitude: 40.99021, Longitude: 29.02703, RadiusMeters: 1000},
			},
			want: []string{"/@40.9902100,29.0270300,15z", "hl=tr", "gl=tr"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SearchURL(tc.query)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("SearchURL = %q, want it to contain %q", got, want)
				}
			}
			for _, omit := range tc.omit {
				if strings.Contains(got, omit) {
					t.Errorf("SearchURL = %q, want it not to contain %q", got, omit)
				}
			}
		})
	}
}

func TestIsGoogleHost(t *testing.T) {
	google := []string{
		"https://www.google.com/maps/dir//x",
		"http://google.com/",
		"https://maps.google.com/x",
		"https://lh3.ggpht.com/photo.jpg",
	}
	notGoogle := []string{
		"https://notgoogle.com/",
		"https://google.com.evil.example/",
		"https://acme-dental.example/",
	}

	for _, href := range google {
		if !isGoogleHost(href) {
			t.Errorf("%q should be recognised as Google's own", href)
		}
	}
	for _, href := range notGoogle {
		if isGoogleHost(href) {
			t.Errorf("%q must not be treated as Google's own", href)
		}
	}
}
