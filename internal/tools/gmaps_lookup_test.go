package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/crawl"
)

const gmapsRenderedTitleHTML = `<html><head><title>Blue Bottle Coffee - Google Maps</title></head><body></body></html>`

func TestGMapsLookup_ByQuery(t *testing.T) {
	cfg := config.Config{GMapsLookupMaxTokens: 400, BusinessAboutMaxChars: 300, GMapsWaitForSelector: "h1", GMapsPageTimeout: 1}
	fetcher := &fakeRawFetcher{page: crawl.Page{RawHTML: gmapsRenderedTitleHTML}}
	tool := NewGMapsBusinessLookup(cfg, fetcher)

	res, err := tool.Handle(context.Background(), json.RawMessage(`{"query":"Blue Bottle Coffee, San Francisco"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := res.(gmapsLookupResponse)
	if resp.Name != "Blue Bottle Coffee" {
		t.Errorf("unexpected name: %q", resp.Name)
	}
	if len(resp.Notes) == 0 {
		t.Error("expected degraded-field notes since the info panel didn't render")
	}
}

func TestGMapsLookup_ByURL(t *testing.T) {
	cfg := config.Config{GMapsLookupMaxTokens: 400, BusinessAboutMaxChars: 300}
	fetcher := &fakeRawFetcher{page: crawl.Page{RawHTML: gmapsRenderedTitleHTML}}
	tool := NewGMapsBusinessLookup(cfg, fetcher)

	res, err := tool.Handle(context.Background(), json.RawMessage(`{"url":"https://www.google.com/maps/place/Blue+Bottle+Coffee/data=..."}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := res.(gmapsLookupResponse)
	if resp.SourceURL != "https://www.google.com/maps/place/Blue+Bottle+Coffee/data=..." {
		t.Errorf("unexpected source url: %q", resp.SourceURL)
	}
}

func TestGMapsLookup_NeitherQueryNorURL(t *testing.T) {
	tool := NewGMapsBusinessLookup(config.Config{}, &fakeRawFetcher{})
	_, err := tool.Handle(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected an error when neither query nor url is provided")
	}
}

func TestGMapsLookup_LoadingSkeleton(t *testing.T) {
	cfg := config.Config{GMapsLookupMaxTokens: 400, BusinessAboutMaxChars: 300}
	fetcher := &fakeRawFetcher{page: crawl.Page{RawHTML: `<html><head><title>Google Maps</title></head><body></body></html>`}}
	tool := NewGMapsBusinessLookup(cfg, fetcher)

	_, err := tool.Handle(context.Background(), json.RawMessage(`{"query":"anything"}`))
	if err == nil {
		t.Fatal("expected an error for a page that never rendered a specific place")
	}
}
