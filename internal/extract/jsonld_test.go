package extract_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/logrenant/mimir/internal/extract"
)

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestJSONLD_Product(t *testing.T) {
	doc, err := extract.Document(readFixture(t, "jsonld_product.html"))
	if err != nil {
		t.Fatal(err)
	}
	blocks := extract.JSONLD(doc)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 JSON-LD block, got %d", len(blocks))
	}
	product, ok := extract.FindJSONLDByType(blocks, "Product")
	if !ok {
		t.Fatal("expected to find a Product block")
	}
	if product["name"] != "Wireless Headphones" {
		t.Errorf("unexpected name: %v", product["name"])
	}
	offers, ok := product["offers"].(map[string]any)
	if !ok {
		t.Fatalf("expected offers to be an object, got %T", product["offers"])
	}
	if offers["price"] != "129.99" {
		t.Errorf("unexpected price: %v", offers["price"])
	}
}

func TestJSONLD_LocalBusiness_ViaGraph(t *testing.T) {
	doc, err := extract.Document(readFixture(t, "jsonld_localbusiness.html"))
	if err != nil {
		t.Fatal(err)
	}
	blocks := extract.JSONLD(doc)
	biz, ok := extract.FindJSONLDByType(blocks, "LocalBusiness")
	if !ok {
		t.Fatal("expected to find a LocalBusiness block nested under @graph")
	}
	if biz["name"] != "Example Cafe" {
		t.Errorf("unexpected name: %v", biz["name"])
	}
}

func TestJSONLD_MalformedBlockSkipped(t *testing.T) {
	doc, err := extract.Document(readFixture(t, "malformed_jsonld.html"))
	if err != nil {
		t.Fatal(err)
	}
	blocks := extract.JSONLD(doc)
	if len(blocks) != 1 {
		t.Fatalf("expected the malformed block to be skipped, leaving 1 valid block, got %d", len(blocks))
	}
	if blocks[0]["name"] != "Still Extractable" {
		t.Errorf("unexpected surviving block: %v", blocks[0])
	}
}

func TestJSONLD_NotFound(t *testing.T) {
	doc, err := extract.Document(readFixture(t, "empty.html"))
	if err != nil {
		t.Fatal(err)
	}
	blocks := extract.JSONLD(doc)
	if len(blocks) != 0 {
		t.Fatalf("expected 0 blocks, got %d", len(blocks))
	}
	if _, ok := extract.FindJSONLDByType(blocks, "Product"); ok {
		t.Fatal("expected no Product block to be found")
	}
}
