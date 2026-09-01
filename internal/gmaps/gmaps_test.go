package gmaps_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/crawl"
	"github.com/logrenant/goat-mcp/internal/gmaps"
)

func fixturePage(t *testing.T, name string) crawl.Page {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return crawl.Page{RawHTML: string(b)}
}

func testCfg() config.Config { return config.Config{BusinessAboutMaxChars: 300} }

func TestExtract_NameOnly_DegradesWithNotes(t *testing.T) {
	p, err := gmaps.Extract(testCfg(), fixturePage(t, "rendered_place.html"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name != "Blue Bottle Coffee San Francisco" {
		t.Errorf("unexpected name: %q", p.Name)
	}
	if len(p.Notes) != 4 {
		t.Errorf("expected 4 degraded-field notes (address/phone/website/rating), got %d: %v", len(p.Notes), p.Notes)
	}
	if p.Address != "" || p.Phone != "" || p.Website != "" {
		t.Errorf("expected empty fields when the info panel didn't render, got %+v", p)
	}
}

func TestExtract_FullInfoPanel(t *testing.T) {
	p, err := gmaps.Extract(testCfg(), fixturePage(t, "rendered_place_full.html"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name != "Blue Bottle Coffee" {
		t.Errorf("unexpected name: %q", p.Name)
	}
	if p.Address != "300 Webster St, San Francisco, CA 94117" {
		t.Errorf("unexpected address: %q", p.Address)
	}
	if p.Website != "https://bluebottlecoffee.com" {
		t.Errorf("unexpected website: %q", p.Website)
	}
	if p.Rating != 4.6 {
		t.Errorf("unexpected rating: %v", p.Rating)
	}
	if len(p.Notes) != 0 {
		t.Errorf("expected no degraded-field notes, got %v", p.Notes)
	}
}

func TestExtract_LoadingSkeleton(t *testing.T) {
	_, err := gmaps.Extract(testCfg(), fixturePage(t, "loading_skeleton.html"))
	if !errors.Is(err, gmaps.ErrNotAPlacePage) {
		t.Fatalf("expected ErrNotAPlacePage, got %v", err)
	}
}

func TestSearchURL(t *testing.T) {
	got := gmaps.SearchURL("Blue Bottle Coffee, San Francisco")
	want := "https://www.google.com/maps/place/?q=Blue+Bottle+Coffee%2C+San+Francisco"
	if got != want {
		t.Errorf("unexpected search url: got %q want %q", got, want)
	}
}
