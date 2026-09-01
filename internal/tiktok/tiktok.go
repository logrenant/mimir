// Package tiktok extracts a normalized profile from an already-crawled
// TikTok profile page. TikTok server-renders a large JSON state blob
// (id="__UNIVERSAL_DATA_FOR_REHYDRATION__") for many public profiles without
// requiring login — that undocumented, version-fragile shape is walked
// best-effort first; Open Graph meta tags are the fallback. Expect this
// package to occasionally need updating as TikTok's internal JSON shape
// drifts — that is a known, accepted maintenance cost, not a design flaw.
package tiktok

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/crawl"
	"github.com/logrenant/mimir/internal/extract"
)

// ErrProfileNotFound means neither the embedded state blob nor Open Graph
// metadata yielded anything usable — the page is likely a "not found"/error
// page, or TikTok's internal JSON shape has drifted.
var ErrProfileNotFound = errors.New("tiktok: could not extract profile data from page")

// Profile is the normalized shape returned regardless of extraction path.
type Profile struct {
	Username       string
	Nickname       string
	Bio            string
	Verified       bool
	FollowerCount  int
	FollowingCount int
	LikeCount      int
	VideoCount     int
	AvatarURL      string
}

// ProfileURL builds the canonical public profile URL for a username.
func ProfileURL(username string) string {
	return "https://www.tiktok.com/@" + url.PathEscape(username)
}

// Extract pulls a Profile out of an already-crawled page's RawHTML.
func Extract(cfg config.Config, page crawl.Page) (Profile, error) {
	doc, err := extract.Document(page.RawHTML)
	if err != nil {
		return Profile{}, fmt.Errorf("tiktok: parse html: %w", err)
	}

	if raw, ok := extract.ScriptJSON(doc, "#__UNIVERSAL_DATA_FOR_REHYDRATION__"); ok {
		var v any
		if json.Unmarshal(raw, &v) == nil {
			if p, ok := fromUniversalData(cfg, v); ok {
				return p, nil
			}
		}
	}

	title := extract.Meta(doc, "og:title")
	desc := extract.Meta(doc, "og:description")
	if title == "" && desc == "" {
		return Profile{}, ErrProfileNotFound
	}
	return Profile{
		Nickname: title,
		Bio:      extract.ClampText(desc, cfg.BioMaxChars),
	}, nil
}

// fromUniversalData walks TikTok's undocumented server-rendered state blob.
// ok=false on any miss — the caller falls back to Open Graph, this is not
// itself an error condition.
func fromUniversalData(cfg config.Config, v any) (Profile, bool) {
	userVal, ok := extract.Dig(v, "__DEFAULT_SCOPE__", "webapp.user-detail", "userInfo", "user")
	if !ok {
		return Profile{}, false
	}
	userMap, ok := userVal.(map[string]any)
	if !ok {
		return Profile{}, false
	}
	statsVal, _ := extract.Dig(v, "__DEFAULT_SCOPE__", "webapp.user-detail", "userInfo", "stats")
	statsMap, _ := statsVal.(map[string]any)

	p := Profile{}
	if s, ok := userMap["uniqueId"].(string); ok {
		p.Username = s
	}
	if s, ok := userMap["nickname"].(string); ok {
		p.Nickname = s
	}
	if s, ok := userMap["signature"].(string); ok {
		p.Bio = extract.ClampText(s, cfg.BioMaxChars)
	}
	if b, ok := userMap["verified"].(bool); ok {
		p.Verified = b
	}
	if s, ok := userMap["avatarLarger"].(string); ok {
		p.AvatarURL = s
	}
	if statsMap != nil {
		if n, ok := statsMap["followerCount"].(float64); ok {
			p.FollowerCount = int(n)
		}
		if n, ok := statsMap["followingCount"].(float64); ok {
			p.FollowingCount = int(n)
		}
		if n, ok := statsMap["heartCount"].(float64); ok {
			p.LikeCount = int(n)
		}
		if n, ok := statsMap["videoCount"].(float64); ok {
			p.VideoCount = int(n)
		}
	}

	if p.Username == "" && p.Nickname == "" {
		return Profile{}, false
	}
	return p, true
}
