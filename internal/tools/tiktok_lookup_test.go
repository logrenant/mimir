package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/crawl"
)

const tiktokUniversalDataHTML = `<html><body>
<script id="__UNIVERSAL_DATA_FOR_REHYDRATION__" type="application/json">
{"__DEFAULT_SCOPE__":{"webapp.user-detail":{"userInfo":{"user":{"uniqueId":"exampleuser","nickname":"Example User","signature":"bio","verified":true},"stats":{"followerCount":100,"followingCount":5,"heartCount":200,"videoCount":3}}}}}
</script>
</body></html>`

func TestTikTokProfileLookup_HappyPath(t *testing.T) {
	cfg := config.Config{TikTokProfileMaxTokens: 400, BioMaxChars: 300}
	fetcher := &fakeRawFetcher{page: crawl.Page{RawHTML: tiktokUniversalDataHTML}}
	tool := NewTikTokProfileLookup(cfg, fetcher)

	res, err := tool.Handle(context.Background(), json.RawMessage(`{"username":"@exampleuser"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := res.(tikTokLookupResponse)
	if resp.Username != "exampleuser" {
		t.Errorf("unexpected username: %q", resp.Username)
	}
	if resp.FollowerCount != 100 {
		t.Errorf("unexpected follower count: %d", resp.FollowerCount)
	}
	if resp.ProfileURL != "https://www.tiktok.com/@exampleuser" {
		t.Errorf("unexpected profile url: %q", resp.ProfileURL)
	}
}

func TestTikTokProfileLookup_EmptyUsername(t *testing.T) {
	tool := NewTikTokProfileLookup(config.Config{}, &fakeRawFetcher{})
	_, err := tool.Handle(context.Background(), json.RawMessage(`{"username":""}`))
	if err == nil {
		t.Fatal("expected an error for empty username")
	}
}

func TestTikTokProfileLookup_NotFound(t *testing.T) {
	cfg := config.Config{TikTokProfileMaxTokens: 400, BioMaxChars: 300}
	fetcher := &fakeRawFetcher{page: crawl.Page{RawHTML: "<html><body>nothing here</body></html>"}}
	tool := NewTikTokProfileLookup(cfg, fetcher)

	_, err := tool.Handle(context.Background(), json.RawMessage(`{"username":"exampleuser"}`))
	if err == nil {
		t.Fatal("expected an error when profile data can't be extracted")
	}
}
