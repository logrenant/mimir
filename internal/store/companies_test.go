package store

import (
	"context"
	"testing"
	"time"

	"github.com/logrenant/goat-mcp/internal/maps"
)

func sampleCompany(id string) maps.Company {
	return maps.Company{
		PlaceID:          id,
		Name:             "Acme Dental",
		FormattedAddress: "1 Test Street, Istanbul",
		Latitude:         40.99,
		Longitude:        29.02,
		Rating:           4.6,
		ReviewCount:      231,
		Website:          "https://acme.example",
		Phone:            "0216 000 00 00",
		Types:            []string{"dentist", "health"},
		PrimaryType:      "dentist",
		BusinessStatus:   "OPERATIONAL",
		Source:           maps.SourcePlacesAPI,
		FetchedAt:        time.Now().Add(-time.Hour).UTC().Truncate(time.Second),
	}
}

func TestCompanyRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	want := sampleCompany("place-1")
	if err := s.PutCompany(ctx, want); err != nil {
		t.Fatalf("PutCompany: %v", err)
	}

	got, ok, err := s.GetCompany(ctx, "place-1", time.Hour)
	if err != nil || !ok {
		t.Fatalf("GetCompany: ok=%v err=%v", ok, err)
	}
	if got.Name != want.Name || got.FormattedAddress != want.FormattedAddress {
		t.Errorf("identity mismatch: %+v", got)
	}
	if got.Latitude != want.Latitude || got.Longitude != want.Longitude {
		t.Errorf("coordinates mismatch: %+v", got)
	}
	if got.Rating != want.Rating || got.ReviewCount != want.ReviewCount {
		t.Errorf("ratings mismatch: %+v", got)
	}
	if got.Website != want.Website || got.Phone != want.Phone {
		t.Errorf("contact mismatch: %+v", got)
	}
	if len(got.Types) != 2 || got.Types[0] != "dentist" || got.PrimaryType != "dentist" {
		t.Errorf("types mismatch: %+v", got.Types)
	}
	if got.BusinessStatus != want.BusinessStatus || got.Source != want.Source {
		t.Errorf("provenance mismatch: %+v", got)
	}
	if !got.FetchedAt.Equal(want.FetchedAt) {
		t.Errorf("FetchedAt = %v, want %v", got.FetchedAt, want.FetchedAt)
	}
}

func TestCompanyUpsertRefreshes(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	c := sampleCompany("place-1")
	if err := s.PutCompany(ctx, c); err != nil {
		t.Fatalf("PutCompany: %v", err)
	}
	c.Name = "Acme Dental Clinic"
	c.ReviewCount = 240
	if err := s.PutCompany(ctx, c); err != nil {
		t.Fatalf("PutCompany (update): %v", err)
	}

	got, ok, err := s.GetCompany(ctx, "place-1", time.Hour)
	if err != nil || !ok {
		t.Fatalf("GetCompany: ok=%v err=%v", ok, err)
	}
	if got.Name != "Acme Dental Clinic" || got.ReviewCount != 240 {
		t.Fatalf("upsert did not refresh: %+v", got)
	}
}

func TestGetCompanyMisses(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if _, ok, err := s.GetCompany(ctx, "never-stored", time.Hour); ok || err != nil {
		t.Fatalf("unknown id: ok=%v err=%v", ok, err)
	}

	if err := s.PutCompany(ctx, sampleCompany("place-1")); err != nil {
		t.Fatalf("PutCompany: %v", err)
	}
	// A non-positive TTL means "never cache", exactly as in pages.go.
	if _, ok, err := s.GetCompany(ctx, "place-1", 0); ok || err != nil {
		t.Fatalf("expired row: ok=%v err=%v", ok, err)
	}
	// ...and the expired row is dropped opportunistically by its reader.
	if _, ok, _ := s.GetCompany(ctx, "place-1", time.Hour); ok {
		t.Fatal("expired row was not deleted on read")
	}
}

func TestPutCompanyIgnoresEmptyPlaceID(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.PutCompany(ctx, maps.Company{Name: "No id"}); err != nil {
		t.Fatalf("PutCompany: %v", err)
	}
	if _, ok, _ := s.GetCompany(ctx, "", time.Hour); ok {
		t.Fatal("a company without a place id was stored")
	}
}

func TestRegionSearchRoundTripPreservesOrder(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	cs := []maps.Company{sampleCompany("p-3"), sampleCompany("p-1"), sampleCompany("p-2")}
	if err := s.PutRegionSearch(ctx, "region-key", "dentists in Kadıköy", cs); err != nil {
		t.Fatalf("PutRegionSearch: %v", err)
	}

	got, ok, err := s.GetRegionSearch(ctx, "region-key", time.Hour)
	if err != nil || !ok {
		t.Fatalf("GetRegionSearch: ok=%v err=%v", ok, err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	for i, want := range []string{"p-3", "p-1", "p-2"} {
		if got[i].PlaceID != want {
			t.Fatalf("position %d = %q, want %q — result order was not preserved", i, got[i].PlaceID, want)
		}
	}
	// The companies themselves are readable on their own too.
	if _, ok, _ := s.GetCompany(ctx, "p-2", time.Hour); !ok {
		t.Fatal("region search did not store its companies individually")
	}
}

func TestRegionSearchEmptyResultIsAHit(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if err := s.PutRegionSearch(ctx, "empty-region", "dentists on the moon", nil); err != nil {
		t.Fatalf("PutRegionSearch: %v", err)
	}

	got, ok, err := s.GetRegionSearch(ctx, "empty-region", time.Hour)
	if err != nil {
		t.Fatalf("GetRegionSearch: %v", err)
	}
	if !ok {
		t.Fatal("a search that found nothing must still be a cache hit — re-asking Places costs money")
	}
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}

func TestRegionSearchMisses(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	if _, ok, err := s.GetRegionSearch(ctx, "never-searched", time.Hour); ok || err != nil {
		t.Fatalf("unknown region: ok=%v err=%v", ok, err)
	}

	if err := s.PutRegionSearch(ctx, "region-key", "q", []maps.Company{sampleCompany("p-1")}); err != nil {
		t.Fatalf("PutRegionSearch: %v", err)
	}
	if _, ok, err := s.GetRegionSearch(ctx, "region-key", 0); ok || err != nil {
		t.Fatalf("expired region: ok=%v err=%v", ok, err)
	}
}

func TestRegionSearchIsAllOrNothing(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	cs := []maps.Company{sampleCompany("p-1"), sampleCompany("p-2")}
	if err := s.PutRegionSearch(ctx, "region-key", "q", cs); err != nil {
		t.Fatalf("PutRegionSearch: %v", err)
	}
	// One company evicted (the shape a longer region TTL than company TTL, or
	// a manual delete, would produce): a partial region must not read as a hit.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM companies WHERE place_id = ?`, "p-2"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	got, ok, err := s.GetRegionSearch(ctx, "region-key", time.Hour)
	if err != nil {
		t.Fatalf("GetRegionSearch: %v", err)
	}
	if ok {
		t.Fatalf("a region missing one of its companies reported a hit: %+v", got)
	}
}

func TestPutRegionSearchSkipsCompaniesWithoutID(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)

	cs := []maps.Company{sampleCompany("p-1"), {Name: "No id"}}
	if err := s.PutRegionSearch(ctx, "region-key", "q", cs); err != nil {
		t.Fatalf("PutRegionSearch: %v", err)
	}

	got, ok, err := s.GetRegionSearch(ctx, "region-key", time.Hour)
	if err != nil || !ok {
		t.Fatalf("GetRegionSearch: ok=%v err=%v", ok, err)
	}
	if len(got) != 1 || got[0].PlaceID != "p-1" {
		t.Fatalf("got %+v, want only the identified company", got)
	}
}

func TestCompanyAccessorsTolerateNilStore(t *testing.T) {
	ctx := context.Background()
	var s *Store

	if err := s.PutCompany(ctx, sampleCompany("p-1")); err != nil {
		t.Errorf("PutCompany on nil store: %v", err)
	}
	if _, ok, err := s.GetCompany(ctx, "p-1", time.Hour); ok || err != nil {
		t.Errorf("GetCompany on nil store: ok=%v err=%v", ok, err)
	}
	if err := s.PutRegionSearch(ctx, "k", "q", nil); err != nil {
		t.Errorf("PutRegionSearch on nil store: %v", err)
	}
	if _, ok, err := s.GetRegionSearch(ctx, "k", time.Hour); ok || err != nil {
		t.Errorf("GetRegionSearch on nil store: ok=%v err=%v", ok, err)
	}
}
