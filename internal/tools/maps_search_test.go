package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/maps"
	"github.com/logrenant/mimir/internal/mcp"
)

// fakeSearcher records the query it was handed so the tests can assert on
// what would have been billed, and returns canned results.
type fakeSearcher struct {
	got    maps.Query
	calls  int
	result []maps.Company
	err    error
}

func (f *fakeSearcher) SearchText(_ context.Context, q maps.Query) ([]maps.Company, error) {
	f.calls++
	f.got = q
	return f.result, f.err
}

func mapsTestConfig() config.Config {
	return config.Config{
		MapsSearchDefaultCount: 20,
		MapsSearchMaxCount:     60,
		MapsSearchMaxTokens:    2000,
	}
}

func TestMapsSearch_HappyPath(t *testing.T) {
	searcher := &fakeSearcher{result: []maps.Company{
		{
			PlaceID: "p1", Name: "Dent Clinic", FormattedAddress: "Bahariye Cd. 1, Kadıköy",
			Latitude: 40.99, Longitude: 29.02, Rating: 4.6, ReviewCount: 312,
			Website: "https://dent.example", Phone: "0216 000 0000",
			Types:       []string{"dentist", "health", "point_of_interest"},
			PrimaryType: "dentist", BusinessStatus: "OPERATIONAL",
		},
		{PlaceID: "p2", Name: "Smile Studio"},
	}}
	tool := NewMapsSearch(mapsTestConfig(), searcher)

	res, err := tool.Handle(context.Background(), json.RawMessage(`{"query":"dentists in Kadıköy","language_code":"tr","region_code":"TR"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := res.(mapsSearchResponse)

	if resp.Returned != 2 || resp.TotalFound != 2 || resp.Truncated {
		t.Errorf("counts: got returned=%d total=%d truncated=%v, want 2/2/false", resp.Returned, resp.TotalFound, resp.Truncated)
	}
	if resp.Companies[0].PlaceID != "p1" || resp.Companies[0].Rating != 4.6 || resp.Companies[0].ReviewCount != 312 {
		t.Errorf("first company not carried through: %+v", resp.Companies[0])
	}
	if searcher.got.LanguageCode != "tr" || searcher.got.RegionCode != "TR" {
		t.Errorf("language/region not passed through: %+v", searcher.got)
	}

	// types[] is leadgen's input, not the consumer's — it must not be on the
	// wire, both for budget and because nothing downstream of the tool reads it.
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "point_of_interest") {
		t.Errorf("Google's types[] leaked into the response: %s", b)
	}
}

func TestMapsSearch_BlankQueryIsNotBilled(t *testing.T) {
	searcher := &fakeSearcher{}
	tool := NewMapsSearch(mapsTestConfig(), searcher)

	if _, err := tool.Handle(context.Background(), json.RawMessage(`{"query":"   "}`)); err == nil {
		t.Fatal("a blank query should be rejected")
	}
	if searcher.calls != 0 {
		t.Errorf("a blank query reached the paid API %d time(s)", searcher.calls)
	}
}

func TestMapsSearch_CountClamping(t *testing.T) {
	cases := []struct {
		args string
		want int
	}{
		{`{"query":"q"}`, 20},
		{`{"query":"q","count":5}`, 5},
		{`{"query":"q","count":999}`, 60},
		{`{"query":"q","count":60}`, 60},
	}

	for _, c := range cases {
		t.Run(c.args, func(t *testing.T) {
			searcher := &fakeSearcher{}
			tool := NewMapsSearch(mapsTestConfig(), searcher)

			if _, err := tool.Handle(context.Background(), json.RawMessage(c.args)); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if searcher.got.MaxResults != c.want {
				t.Errorf("MaxResults: got %d, want %d", searcher.got.MaxResults, c.want)
			}
		})
	}
}

func TestMapsSearch_NearBias(t *testing.T) {
	searcher := &fakeSearcher{}
	tool := NewMapsSearch(mapsTestConfig(), searcher)

	_, err := tool.Handle(context.Background(),
		json.RawMessage(`{"query":"q","near":{"latitude":40.99,"longitude":29.02,"radius_meters":1500}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if searcher.got.Bias == nil {
		t.Fatal("near was not passed through as a location bias")
	}
	if searcher.got.Bias.Latitude != 40.99 || searcher.got.Bias.RadiusMeters != 1500 {
		t.Errorf("bias: got %+v", *searcher.got.Bias)
	}

	// A zero radius is a circle that can match nothing; reject it before it is
	// billed rather than passing it on.
	searcher = &fakeSearcher{}
	tool = NewMapsSearch(mapsTestConfig(), searcher)
	if _, err := tool.Handle(context.Background(),
		json.RawMessage(`{"query":"q","near":{"latitude":1,"longitude":2,"radius_meters":0}}`)); err == nil {
		t.Error("a zero radius should be rejected")
	}
	if searcher.calls != 0 {
		t.Errorf("a zero radius reached the paid API %d time(s)", searcher.calls)
	}
}

func TestMapsSearch_NoResultsIsNotAnError(t *testing.T) {
	tool := NewMapsSearch(mapsTestConfig(), &fakeSearcher{})

	res, err := tool.Handle(context.Background(), json.RawMessage(`{"query":"nowhere"}`))
	if err != nil {
		t.Fatalf("an empty region is a legitimate answer, got error: %v", err)
	}
	resp := res.(mapsSearchResponse)
	if resp.Returned != 0 || resp.TotalFound != 0 || resp.Truncated {
		t.Errorf("empty result should report 0/0/false, got %d/%d/%v", resp.Returned, resp.TotalFound, resp.Truncated)
	}
}

// SD-6: a sentinel has to reach the consumer as the fix, not as a status code,
// and no message may carry the credential.
func TestMapsSearch_ErrorMapping(t *testing.T) {
	const key = "AIzaSy-SECRET-KEY"

	cases := []struct {
		err  error
		want string
	}{
		{maps.ErrCredentialMissing, "MIMIR_GOOGLE_PLACES_API_KEY"},
		{maps.ErrRequestDenied, "rejected"},
		{maps.ErrQuotaExceeded, "quota"},
		{maps.ErrBadRequest, "rephrase"},
		{maps.ErrUnavailable, "unreachable"},
	}

	for _, c := range cases {
		t.Run(c.err.Error(), func(t *testing.T) {
			// Wrapped, and with a key spliced into the cause, so the test also
			// proves the tool writes its own message instead of forwarding one.
			cause := fmt.Errorf("%w: key %s was used", c.err, key)
			tool := NewMapsSearch(mapsTestConfig(), &fakeSearcher{err: cause})

			_, err := tool.Handle(context.Background(), json.RawMessage(`{"query":"q"}`))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("message should name the fix (%q), got %q", c.want, err.Error())
			}
			if strings.Contains(err.Error(), key) {
				t.Errorf("the API key leaked into a tool error: %q", err.Error())
			}
		})
	}
}

func TestMapsSearch_UnknownErrorIsPassedThrough(t *testing.T) {
	sentinel := errors.New("something else entirely")
	tool := NewMapsSearch(mapsTestConfig(), &fakeSearcher{err: sentinel})

	_, err := tool.Handle(context.Background(), json.RawMessage(`{"query":"q"}`))
	if !errors.Is(err, sentinel) {
		t.Errorf("an unrecognised error should not be swallowed, got %v", err)
	}
}

// SD-7: a full 60-company page does not fit the ceiling. The tool has to trim
// it here — if the choke-point is the thing that notices, a paid search
// returns nothing at all.
func TestMapsSearch_TrimsToBudgetRatherThanFailingTheChokePoint(t *testing.T) {
	cfg := mapsTestConfig()

	companies := make([]maps.Company, 0, 60)
	for i := 0; i < 60; i++ {
		companies = append(companies, maps.Company{
			PlaceID:          fmt.Sprintf("ChIJ_place_identifier_%02d", i),
			Name:             fmt.Sprintf("A Reasonably Long Business Name Number %02d", i),
			FormattedAddress: fmt.Sprintf("%d Bahariye Caddesi, Caferağa Mahallesi, 34710 Kadıköy/İstanbul, Türkiye", i),
			Latitude:         40.990123,
			Longitude:        29.024567,
			Rating:           4.5,
			ReviewCount:      1234,
			Website:          fmt.Sprintf("https://www.a-fairly-long-business-domain-%02d.example.com/", i),
			Phone:            "+90 216 000 00 00",
			PrimaryType:      "dentist",
			BusinessStatus:   "OPERATIONAL",
		})
	}

	tool := NewMapsSearch(cfg, &fakeSearcher{result: companies})
	res, err := tool.Handle(context.Background(), json.RawMessage(`{"query":"dentists","count":60}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := res.(mapsSearchResponse)

	if resp.TotalFound != 60 {
		t.Errorf("total_found should stay honest at 60, got %d", resp.TotalFound)
	}
	if !resp.Truncated || resp.Returned >= resp.TotalFound {
		t.Errorf("this result set must be trimmed, got returned=%d truncated=%v", resp.Returned, resp.Truncated)
	}
	if resp.Returned == 0 {
		t.Fatal("trimming to nothing is not a useful answer")
	}
	if resp.Returned != len(resp.Companies) {
		t.Errorf("returned=%d disagrees with %d companies on the wire", resp.Returned, len(resp.Companies))
	}

	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(b) / 4; got > cfg.MapsSearchMaxTokens {
		t.Errorf("response is ~%d tokens, over the %d budget — the choke-point would reject it", got, cfg.MapsSearchMaxTokens)
	}
}

// The credential decides availability, not behaviour: a keyless install must
// not advertise a tool whose every answer would be "no key".
func TestRegisterAll_MapsSearchFollowsTheCredential(t *testing.T) {
	cfg := config.Load()

	cfg.PlacesAPIKey = ""
	if names := registeredNames(t, cfg); contains(names, "maps_search") {
		t.Errorf("maps_search was offered without a key: %v", names)
	}

	cfg.PlacesAPIKey = "test-key"
	names := registeredNames(t, cfg)
	if !contains(names, "maps_search") {
		t.Errorf("maps_search missing with a key present: %v", names)
	}
	// The rest of the canonical set is unaffected either way.
	for _, want := range []string{"web_search", "fetch_page", "research", "diagnostics"} {
		if !contains(names, want) {
			t.Errorf("%s missing from the canonical set: %v", want, names)
		}
	}
}

func registeredNames(t *testing.T, cfg config.Config) []string {
	t.Helper()

	srv := mcp.NewServer(cfg)
	if err := RegisterAll(srv.Registry(), cfg, Deps{}); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}
	return srv.Registry().Names()
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
