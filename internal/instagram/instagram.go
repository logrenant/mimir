// Package instagram extracts a normalized profile from an already-crawled
// public Instagram profile page.
//
// Verified against a live Crawl4AI container while building this package: a
// public profile's Open Graph tags reliably carry the display name and a
// follower/following/post-count sentence (og:description, format "104M
// Followers, 95 Following, 4,900 Posts - See Instagram photos and videos
// from NASA (@nasa)"), no login/wait tuning needed for well-known accounts.
// A nonexistent (or blocked/rate-limited) account instead serves a generic
// "Instagram" title with no Open Graph tags at all — a clean, detectable
// signal. Note og:description is a stats sentence, not the account's bio
// text — a real bio is not reliably extractable from this public,
// unauthenticated render, so Bio is intentionally left for future work
// rather than populated from a guess.
package instagram

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/crawl"
	"github.com/logrenant/goat-mcp/internal/extract"
)

// ErrLoginWall means the page exposed no Open Graph profile data — most
// likely a nonexistent account, a login wall, or rate limiting. Instagram
// is the least reliable of the four free scrapers in this tree; expect this
// more often than with internal/tiktok or internal/ecommerce.
var ErrLoginWall = errors.New("instagram: page did not expose profile data — likely a login wall, rate limit, or nonexistent account")

// Profile is the normalized shape returned.
type Profile struct {
	Username       string
	DisplayName    string
	FollowerCount  int
	FollowingCount int
	PostCount      int
}

// ProfileURL builds the canonical public profile URL for a username.
func ProfileURL(username string) string {
	return "https://www.instagram.com/" + url.PathEscape(username) + "/"
}

var titleRe = regexp.MustCompile(`^(.*?)\s*\(@([^)]+)\)`)
var statsRe = regexp.MustCompile(`(?i)^\s*([\d.,]+[kmb]?)\s*Followers,\s*([\d.,]+[kmb]?)\s*Following,\s*([\d.,]+[kmb]?)\s*Posts`)

// Extract pulls a Profile out of an already-crawled page's RawHTML.
// username is the one requested, used as a fallback if it can't be parsed
// back out of the page title.
// cfg is currently unused (no clamped free-text field yet — see the package
// doc on Bio) but kept in the signature for symmetry with the other
// providers and so adding one later isn't a breaking API change.
func Extract(cfg config.Config, username string, page crawl.Page) (Profile, error) {
	doc, err := extract.Document(page.RawHTML)
	if err != nil {
		return Profile{}, fmt.Errorf("instagram: parse html: %w", err)
	}

	title := extract.Meta(doc, "og:title")
	desc := extract.Meta(doc, "og:description")
	if title == "" && desc == "" {
		return Profile{}, ErrLoginWall
	}

	p := Profile{Username: username}
	if m := titleRe.FindStringSubmatch(title); m != nil {
		p.DisplayName = strings.TrimSpace(m[1])
		if m[2] != "" {
			p.Username = m[2]
		}
	} else {
		p.DisplayName = title
	}

	if m := statsRe.FindStringSubmatch(desc); m != nil {
		if n, ok := extract.ParseCompactInt(m[1]); ok {
			p.FollowerCount = n
		}
		if n, ok := extract.ParseCompactInt(m[2]); ok {
			p.FollowingCount = n
		}
		if n, ok := extract.ParseCompactInt(m[3]); ok {
			p.PostCount = n
		}
	}

	return p, nil
}
