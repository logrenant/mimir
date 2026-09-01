package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/crawl"
)

const instagramProfileHTML = `<html><head>
<meta property="og:title" content="NASA (@nasa) • Instagram photos and videos">
<meta property="og:description" content="104M Followers, 95 Following, 4,900 Posts - See Instagram photos and videos from NASA (@nasa)">
</head><body></body></html>`

func TestInstagramProfileLookup_HappyPath(t *testing.T) {
	cfg := config.Config{InstagramProfileMaxTokens: 300}
	fetcher := &fakeRawFetcher{page: crawl.Page{RawHTML: instagramProfileHTML}}
	tool := NewInstagramProfileLookup(cfg, fetcher)

	res, err := tool.Handle(context.Background(), json.RawMessage(`{"username":"@nasa"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := res.(instagramLookupResponse)
	if resp.Username != "nasa" {
		t.Errorf("unexpected username: %q", resp.Username)
	}
	if resp.FollowerCount != 104_000_000 {
		t.Errorf("unexpected follower count: %d", resp.FollowerCount)
	}
}

func TestInstagramProfileLookup_EmptyUsername(t *testing.T) {
	tool := NewInstagramProfileLookup(config.Config{}, &fakeRawFetcher{})
	_, err := tool.Handle(context.Background(), json.RawMessage(`{"username":""}`))
	if err == nil {
		t.Fatal("expected an error for empty username")
	}
}

func TestInstagramProfileLookup_LoginWall(t *testing.T) {
	cfg := config.Config{InstagramProfileMaxTokens: 300}
	fetcher := &fakeRawFetcher{page: crawl.Page{RawHTML: `<html><head><title>Instagram</title></head><body></body></html>`}}
	tool := NewInstagramProfileLookup(cfg, fetcher)

	_, err := tool.Handle(context.Background(), json.RawMessage(`{"username":"someuser"}`))
	if err == nil {
		t.Fatal("expected an error when no profile data is exposed")
	}
}
