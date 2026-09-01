package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/crawl"
	"github.com/logrenant/mimir/internal/instagram"
	"github.com/logrenant/mimir/internal/mcp"
)

// InstagramProfileLookupTool looks up a public Instagram profile — free,
// self-scraped, no API key, no login. This is the least reliable of the
// four free-scraper tools in this tree: Instagram frequently serves a
// login wall or rate-limits unauthenticated requests. Treat this as
// best-effort, not a guaranteed-available data source.
type InstagramProfileLookupTool struct {
	fetcher RawFetcher
	cfg     config.Config
}

// NewInstagramProfileLookup creates a new InstagramProfileLookupTool.
func NewInstagramProfileLookup(cfg config.Config, fetcher RawFetcher) *InstagramProfileLookupTool {
	return &InstagramProfileLookupTool{fetcher: fetcher, cfg: cfg}
}

func (t *InstagramProfileLookupTool) Name() string { return "instagram_profile_lookup" }

func (t *InstagramProfileLookupTool) Description() string {
	return "Look up a public Instagram profile by username and return normalized fields: display name, follower/following/post counts. Free, self-scraped, no API key, no login. Best-effort and frequently unavailable: Instagram often serves a login wall or rate-limits unauthenticated requests — a failure here does not necessarily mean the account doesn't exist."
}

func (t *InstagramProfileLookupTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"username": { "type": "string", "minLength": 1, "description": "Instagram username, with or without the leading @" }
		},
		"required": ["username"],
		"additionalProperties": false
	}`)
}

type instagramLookupArgs struct {
	Username string `json:"username"`
}

type instagramLookupResponse struct {
	ProfileURL     string `json:"profile_url"`
	Username       string `json:"username,omitempty"`
	DisplayName    string `json:"display_name,omitempty"`
	FollowerCount  int    `json:"follower_count,omitempty"`
	FollowingCount int    `json:"following_count,omitempty"`
	PostCount      int    `json:"post_count,omitempty"`

	budget int `json:"-"`
}

func (r instagramLookupResponse) MetadataOnly() bool    { return true }
func (r instagramLookupResponse) SizeBudgetTokens() int { return r.budget }

func (t *InstagramProfileLookupTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var input instagramLookupArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	username := stripLeadingAt(input.Username)
	if username == "" {
		return nil, errors.New("username must not be empty")
	}

	profileURL := instagram.ProfileURL(username)
	page, err := t.fetcher.FetchRaw(ctx, profileURL, crawl.FetchOptions{})
	if err != nil {
		if errors.Is(err, crawl.ErrDockerUnavailable) {
			return nil, errors.New("crawl4ai not reachable — run `make crawl-up`")
		}
		return nil, err
	}

	profile, err := instagram.Extract(t.cfg, username, page)
	if err != nil {
		if errors.Is(err, instagram.ErrLoginWall) {
			return nil, errors.New("could not extract profile data — likely a login wall, rate limit, or the account doesn't exist (best-effort tool, see its description)")
		}
		return nil, err
	}

	return instagramLookupResponse{
		ProfileURL:     profileURL,
		Username:       profile.Username,
		DisplayName:    profile.DisplayName,
		FollowerCount:  profile.FollowerCount,
		FollowingCount: profile.FollowingCount,
		PostCount:      profile.PostCount,
		budget:         t.cfg.InstagramProfileMaxTokens,
	}, nil
}

var _ mcp.Tool = (*InstagramProfileLookupTool)(nil)
