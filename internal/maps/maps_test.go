package maps

import (
	"strings"
	"testing"
)

func TestQueryKeyIsStable(t *testing.T) {
	q := Query{Text: "bakeries in Beşiktaş", LanguageCode: "tr", RegionCode: "TR",
		Bias: &Circle{Latitude: 41.04, Longitude: 29.00, RadiusMeters: 800}, MaxResults: 20}
	first, second := q.Key(), q.Key()
	if first != second {
		t.Fatal("Key is not deterministic")
	}
	if len(first) != 64 {
		t.Fatalf("Key length = %d, want a sha256 hex digest", len(first))
	}
}

func TestQueryKeyNormalizesText(t *testing.T) {
	a := Query{Text: "  Bakeries in Beşiktaş  "}
	b := Query{Text: "bakeries in beşiktaş"}
	if a.Key() != b.Key() {
		t.Fatal("case and surrounding whitespace should not split the cache")
	}
}

// Every field that can change which companies come back must change the key —
// a missed field means a stale region replays under a different question.
func TestQueryKeyIsTotal(t *testing.T) {
	base := Query{Text: "bakeries", LanguageCode: "tr", RegionCode: "TR",
		Bias: &Circle{Latitude: 41.04, Longitude: 29.00, RadiusMeters: 800}, MaxResults: 20}

	variants := map[string]Query{
		"text":      {Text: "cafes", LanguageCode: "tr", RegionCode: "TR", Bias: base.Bias, MaxResults: 20},
		"language":  {Text: "bakeries", LanguageCode: "en", RegionCode: "TR", Bias: base.Bias, MaxResults: 20},
		"region":    {Text: "bakeries", LanguageCode: "tr", RegionCode: "DE", Bias: base.Bias, MaxResults: 20},
		"no bias":   {Text: "bakeries", LanguageCode: "tr", RegionCode: "TR", MaxResults: 20},
		"latitude":  {Text: "bakeries", LanguageCode: "tr", RegionCode: "TR", Bias: &Circle{Latitude: 41.05, Longitude: 29.00, RadiusMeters: 800}, MaxResults: 20},
		"longitude": {Text: "bakeries", LanguageCode: "tr", RegionCode: "TR", Bias: &Circle{Latitude: 41.04, Longitude: 29.01, RadiusMeters: 800}, MaxResults: 20},
		"radius":    {Text: "bakeries", LanguageCode: "tr", RegionCode: "TR", Bias: &Circle{Latitude: 41.04, Longitude: 29.00, RadiusMeters: 900}, MaxResults: 20},
		"max":       {Text: "bakeries", LanguageCode: "tr", RegionCode: "TR", Bias: base.Bias, MaxResults: 40},
	}

	baseKey := base.Key()
	for name, v := range variants {
		if v.Key() == baseKey {
			t.Errorf("%s does not change the cache key", name)
		}
	}
}

func TestQueryKeyClampsMaxResults(t *testing.T) {
	// Both mean "give me everything the API will return", so they must share
	// one cache entry rather than paying twice for the same region.
	unset := Query{Text: "bakeries"}
	ceiling := Query{Text: "bakeries", MaxResults: MaxResults}
	beyond := Query{Text: "bakeries", MaxResults: 10_000}

	if unset.Key() != ceiling.Key() || ceiling.Key() != beyond.Key() {
		t.Fatal("an unset, exact-ceiling and above-ceiling cap must share a key")
	}
}

func TestQueryLimit(t *testing.T) {
	cases := []struct {
		in   int
		want int
	}{{0, MaxResults}, {-5, MaxResults}, {7, 7}, {MaxResults, MaxResults}, {MaxResults + 1, MaxResults}}
	for _, tc := range cases {
		if got := (Query{MaxResults: tc.in}).limit(); got != tc.want {
			t.Errorf("limit(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestFieldMaskRequestsNoFreeText(t *testing.T) {
	// SD-2/SD-7: this package returns structured facts and skips refine, which
	// only holds while the mask asks for nothing unbounded. Adding one of these
	// is a decision that needs a clamp, and a billing change besides.
	for _, banned := range []string{"editorialSummary", "reviews", "generativeSummary", "photos"} {
		if strings.Contains(FieldMask, banned) {
			t.Errorf("FieldMask requests unbounded free-text field %q", banned)
		}
	}
	for _, required := range []string{"places.id", "places.displayName", "nextPageToken"} {
		if !strings.Contains(FieldMask, required) {
			t.Errorf("FieldMask is missing %q", required)
		}
	}
}
