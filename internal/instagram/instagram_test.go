package instagram_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/crawl"
	"github.com/logrenant/goat-mcp/internal/instagram"
)

func fixturePage(t *testing.T, name string) crawl.Page {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return crawl.Page{RawHTML: string(b)}
}

func TestExtract_HappyPath(t *testing.T) {
	p, err := instagram.Extract(config.Config{}, "nasa", fixturePage(t, "profile.html"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Username != "nasa" {
		t.Errorf("unexpected username: %q", p.Username)
	}
	if p.DisplayName != "NASA" {
		t.Errorf("unexpected display name: %q", p.DisplayName)
	}
	if p.FollowerCount != 104_000_000 {
		t.Errorf("unexpected follower count: %d", p.FollowerCount)
	}
	if p.FollowingCount != 95 {
		t.Errorf("unexpected following count: %d", p.FollowingCount)
	}
	if p.PostCount != 4900 {
		t.Errorf("unexpected post count: %d", p.PostCount)
	}
}

func TestExtract_NotFoundOrLoginWall(t *testing.T) {
	_, err := instagram.Extract(config.Config{}, "this_username_almost_certainly_does_not_exist_xyz123", fixturePage(t, "not_found.html"))
	if !errors.Is(err, instagram.ErrLoginWall) {
		t.Fatalf("expected ErrLoginWall, got %v", err)
	}
}

func TestProfileURL(t *testing.T) {
	if got := instagram.ProfileURL("nasa"); got != "https://www.instagram.com/nasa/" {
		t.Errorf("unexpected profile url: %q", got)
	}
}
