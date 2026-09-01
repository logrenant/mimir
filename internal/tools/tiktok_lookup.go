package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/logrenant/goat-mcp/internal/config"
	"github.com/logrenant/goat-mcp/internal/crawl"
	"github.com/logrenant/goat-mcp/internal/mcp"
	"github.com/logrenant/goat-mcp/internal/tiktok"
)

// TikTokProfileLookupTool extracts a normalized profile from a public
// TikTok profile page — free, self-scraped, no API key. TikTok's internal
// JSON shape is undocumented and can drift without notice; this tool is
// best-effort and degrades to Open Graph metadata when the richer path
// misses.
type TikTokProfileLookupTool struct {
	fetcher RawFetcher
	cfg     config.Config
}

// NewTikTokProfileLookup creates a new TikTokProfileLookupTool.
func NewTikTokProfileLookup(cfg config.Config, fetcher RawFetcher) *TikTokProfileLookupTool {
	return &TikTokProfileLookupTool{fetcher: fetcher, cfg: cfg}
}

func (t *TikTokProfileLookupTool) Name() string { return "tiktok_profile_lookup" }

func (t *TikTokProfileLookupTool) Description() string {
	return "Look up a public TikTok profile by username and return normalized fields: nickname, bio, verified, follower/following/like/video counts. Free, self-scraped, no API key, no login. Best-effort: TikTok's internal page structure can change without notice, and some profiles may not be extractable."
}

func (t *TikTokProfileLookupTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"username": { "type": "string", "minLength": 1, "description": "TikTok username, with or without the leading @" }
		},
		"required": ["username"],
		"additionalProperties": false
	}`)
}

type tikTokLookupArgs struct {
	Username string `json:"username"`
}

type tikTokLookupResponse struct {
	ProfileURL     string `json:"profile_url"`
	Username       string `json:"username,omitempty"`
	Nickname       string `json:"nickname,omitempty"`
	Bio            string `json:"bio,omitempty"`
	Verified       bool   `json:"verified,omitempty"`
	FollowerCount  int    `json:"follower_count,omitempty"`
	FollowingCount int    `json:"following_count,omitempty"`
	LikeCount      int    `json:"like_count,omitempty"`
	VideoCount     int    `json:"video_count,omitempty"`
	AvatarURL      string `json:"avatar_url,omitempty"`

	budget int `json:"-"`
}

func (r tikTokLookupResponse) MetadataOnly() bool    { return true }
func (r tikTokLookupResponse) SizeBudgetTokens() int { return r.budget }

func (t *TikTokProfileLookupTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var input tikTokLookupArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	username := stripLeadingAt(input.Username)
	if username == "" {
		return nil, errors.New("username must not be empty")
	}

	profileURL := tiktok.ProfileURL(username)
	page, err := t.fetcher.FetchRaw(ctx, profileURL, crawl.FetchOptions{})
	if err != nil {
		if errors.Is(err, crawl.ErrDockerUnavailable) {
			return nil, errors.New("crawl4ai not reachable — run `make crawl-up`")
		}
		return nil, err
	}

	profile, err := tiktok.Extract(t.cfg, page)
	if err != nil {
		if errors.Is(err, tiktok.ErrProfileNotFound) {
			return nil, errors.New("could not extract profile data — the account may not exist, or TikTok blocked/altered the page")
		}
		return nil, err
	}

	return tikTokLookupResponse{
		ProfileURL:     profileURL,
		Username:       profile.Username,
		Nickname:       profile.Nickname,
		Bio:            profile.Bio,
		Verified:       profile.Verified,
		FollowerCount:  profile.FollowerCount,
		FollowingCount: profile.FollowingCount,
		LikeCount:      profile.LikeCount,
		VideoCount:     profile.VideoCount,
		AvatarURL:      profile.AvatarURL,
		budget:         t.cfg.TikTokProfileMaxTokens,
	}, nil
}

func stripLeadingAt(s string) string {
	if len(s) > 0 && s[0] == '@' {
		return s[1:]
	}
	return s
}

var _ mcp.Tool = (*TikTokProfileLookupTool)(nil)
