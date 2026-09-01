package ecommerce_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/crawl"
	"github.com/logrenant/goat-mcp/internal/ecommerce"
)

func fixturePage(t *testing.T, name, url string) crawl.Page {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return crawl.Page{URL: url, RawHTML: string(b)}
}

func testCfg() config.Config {
	return config.Config{ProductDescriptionMaxChars: 100}
}

func TestExtract_JSONLD_HappyPath(t *testing.T) {
	page := fixturePage(t, "product.html", "https://shop.example.com/headphones")
	p, err := ecommerce.Extract(testCfg(), page)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name != "Wireless Headphones" {
		t.Errorf("unexpected name: %q", p.Name)
	}
	if p.Brand != "ExampleAudio" {
		t.Errorf("unexpected brand: %q", p.Brand)
	}
	if p.Price != 129.99 {
		t.Errorf("unexpected price: %v", p.Price)
	}
	if p.Currency != "USD" {
		t.Errorf("unexpected currency: %q", p.Currency)
	}
	if p.Rating != 4.6 {
		t.Errorf("unexpected rating: %v", p.Rating)
	}
	if p.ReviewCount != 2153 {
		t.Errorf("unexpected review count: %v", p.ReviewCount)
	}
	if p.ImageURL != "https://example.com/img/headphones.jpg" {
		t.Errorf("unexpected image: %q", p.ImageURL)
	}
	if len([]rune(p.Description)) > 101 {
		t.Errorf("expected description to be clamped to ~100 chars, got %d: %q", len([]rune(p.Description)), p.Description)
	}
}

func TestExtract_JSONLD_OffersArrayAndImageArray(t *testing.T) {
	page := fixturePage(t, "product_offers_array.html", "https://shop.example.com/lamp")
	p, err := ecommerce.Extract(testCfg(), page)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Brand != "GenericBrand" {
		t.Errorf("unexpected brand: %q", p.Brand)
	}
	if p.Price != 24.5 {
		t.Errorf("expected first offer's price, got %v", p.Price)
	}
	if p.ImageURL != "https://example.com/img/lamp1.jpg" {
		t.Errorf("expected first image, got %q", p.ImageURL)
	}
	if p.Rating != 4.1 || p.ReviewCount != 87 {
		t.Errorf("unexpected rating/reviewCount: %v/%v", p.Rating, p.ReviewCount)
	}
}

func TestExtract_OGFallback(t *testing.T) {
	page := fixturePage(t, "og_fallback.html", "https://shop.example.com/tote")
	p, err := ecommerce.Extract(testCfg(), page)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Name != "Canvas Tote Bag" {
		t.Errorf("unexpected name: %q", p.Name)
	}
	if p.Description == "" {
		t.Error("expected non-empty description from og:description")
	}
	if p.ImageURL != "https://example.com/img/tote.jpg" {
		t.Errorf("unexpected image: %q", p.ImageURL)
	}
}

func TestExtract_NoProductData(t *testing.T) {
	page := fixturePage(t, "no_data.html", "https://shop.example.com/nothing")
	_, err := ecommerce.Extract(testCfg(), page)
	if !errors.Is(err, ecommerce.ErrNoProductData) {
		t.Fatalf("expected ErrNoProductData, got %v", err)
	}
}
