package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/crawl"
)

type fakeRawFetcher struct {
	page crawl.Page
	err  error
}

func (f *fakeRawFetcher) FetchRaw(ctx context.Context, url string, opts crawl.FetchOptions) (crawl.Page, error) {
	return f.page, f.err
}

const productJSONLDHTML = `<html><head><script type="application/ld+json">
{"@type":"Product","name":"Test Widget","offers":{"price":"9.99","priceCurrency":"USD"}}
</script></head><body></body></html>`

func TestEcommerceLookup_HappyPath(t *testing.T) {
	cfg := config.Config{EcommerceLookupMaxTokens: 400, ProductDescriptionMaxChars: 600}
	fetcher := &fakeRawFetcher{page: crawl.Page{URL: "https://shop.example.com/widget", RawHTML: productJSONLDHTML}}
	tool := NewEcommerceLookup(cfg, fetcher)

	res, err := tool.Handle(context.Background(), json.RawMessage(`{"url":"https://shop.example.com/widget"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := res.(ecommerceLookupResponse)
	if resp.Name != "Test Widget" {
		t.Errorf("unexpected name: %q", resp.Name)
	}
	if resp.Price != 9.99 {
		t.Errorf("unexpected price: %v", resp.Price)
	}
	if !resp.MetadataOnly() {
		t.Error("expected MetadataOnly() to be true")
	}
	if resp.SizeBudgetTokens() != 400 {
		t.Errorf("unexpected budget: %d", resp.SizeBudgetTokens())
	}
}

func TestEcommerceLookup_InvalidURL(t *testing.T) {
	tool := NewEcommerceLookup(config.Config{}, &fakeRawFetcher{})
	_, err := tool.Handle(context.Background(), json.RawMessage(`{"url":"not-a-url"}`))
	if err == nil {
		t.Fatal("expected an error for an invalid URL")
	}
}

func TestEcommerceLookup_NoProductData(t *testing.T) {
	cfg := config.Config{EcommerceLookupMaxTokens: 400, ProductDescriptionMaxChars: 600}
	fetcher := &fakeRawFetcher{page: crawl.Page{URL: "https://shop.example.com/nothing", RawHTML: "<html><body>nothing here</body></html>"}}
	tool := NewEcommerceLookup(cfg, fetcher)

	_, err := tool.Handle(context.Background(), json.RawMessage(`{"url":"https://shop.example.com/nothing"}`))
	if err == nil {
		t.Fatal("expected an error when no Product data is present")
	}
}

func TestEcommerceLookup_CrawlUnavailable(t *testing.T) {
	fetcher := &fakeRawFetcher{err: crawl.ErrDockerUnavailable}
	tool := NewEcommerceLookup(config.Config{}, fetcher)

	_, err := tool.Handle(context.Background(), json.RawMessage(`{"url":"https://shop.example.com/widget"}`))
	if err == nil {
		t.Fatal("expected an error")
	}
	if err.Error() != "crawl4ai not reachable — run `make crawl-up`" {
		t.Errorf("unexpected error message: %q", err.Error())
	}
}
