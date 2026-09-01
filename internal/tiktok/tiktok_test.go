package tiktok_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/crawl"
	"github.com/logrenant/mimir/internal/tiktok"
)

func fixturePage(t *testing.T, name string) crawl.Page {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return crawl.Page{URL: tiktok.ProfileURL("exampleuser"), RawHTML: string(b)}
}

func testCfg() config.Config { return config.Config{BioMaxChars: 200} }

func TestExtract_UniversalDataHappyPath(t *testing.T) {
	p, err := tiktok.Extract(testCfg(), fixturePage(t, "profile.html"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Username != "exampleuser" {
		t.Errorf("unexpected username: %q", p.Username)
	}
	if p.Nickname != "Example User" {
		t.Errorf("unexpected nickname: %q", p.Nickname)
	}
	if !p.Verified {
		t.Error("expected verified=true")
	}
	if p.FollowerCount != 125000 {
		t.Errorf("unexpected follower count: %d", p.FollowerCount)
	}
	if p.LikeCount != 3400000 {
		t.Errorf("unexpected like count: %d", p.LikeCount)
	}
	if p.VideoCount != 87 {
		t.Errorf("unexpected video count: %d", p.VideoCount)
	}
}

func TestExtract_OGFallback(t *testing.T) {
	p, err := tiktok.Extract(testCfg(), fixturePage(t, "og_fallback.html"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Nickname != "Fallback User (@fallbackuser)" {
		t.Errorf("unexpected nickname: %q", p.Nickname)
	}
	if p.Bio == "" {
		t.Error("expected non-empty bio from og:description fallback")
	}
}

func TestExtract_NotFound(t *testing.T) {
	_, err := tiktok.Extract(testCfg(), fixturePage(t, "not_found.html"))
	if !errors.Is(err, tiktok.ErrProfileNotFound) {
		t.Fatalf("expected ErrProfileNotFound, got %v", err)
	}
}

func TestProfileURL(t *testing.T) {
	if got := tiktok.ProfileURL("some user"); got != "https://www.tiktok.com/@some%20user" {
		t.Errorf("unexpected profile URL: %q", got)
	}
}
