package brain

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrRepoUnavailable means GitHub could not be read. It is a typed sentinel so
// a caller can tell "that repository is private or gone" apart from "the store
// is broken" (SD-6).
var ErrRepoUnavailable = errors.New("brain: github repository unavailable")

const (
	githubTimeout = 15 * time.Second
	// A README is the one file worth reading unattended, and a very large one
	// is a book rather than a description. The cap is generous enough to keep
	// a real project's whole README and mean enough to bound the read.
	githubReadmeMaxBytes = 256 << 10
)

// IngestRepo stores a public (or, with a token, private) repository's README as
// a node.
//
// It is a global node — project_path stays empty — because a repository is not
// about any one checkout on this machine, and scoping it to whichever directory
// happened to be current would hide it from every other project.
func (c *Core) IngestRepo(ctx context.Context, repo string) (IngestResult, error) {
	if !c.Available() {
		return IngestResult{}, ErrNoStore
	}

	slug, err := normalizeRepo(repo)
	if err != nil {
		return IngestResult{}, err
	}

	readme, err := c.fetchReadme(ctx, slug)
	if err != nil {
		return IngestResult{}, err
	}

	return c.Ingest(ctx, Input{
		Source:  slug,
		Kind:    KindRepo,
		Content: "Repository: " + slug + "\n\nREADME:\n" + readme,
		// ProjectPath deliberately empty — see the doc comment.
	})
}

// normalizeRepo reduces the several shapes a person pastes to owner/name.
func normalizeRepo(repo string) (string, error) {
	s := strings.TrimSpace(repo)
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "www.")
	s = strings.TrimPrefix(s, "github.com/")
	s = strings.TrimSuffix(s, ".git")
	s = strings.Trim(s, "/")

	parts := strings.Split(s, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("brain: %q is not an owner/name repository", repo)
	}
	// Anything after owner/name (a tree path, a branch) is not part of the
	// identity and would otherwise mint a second node for the same repository.
	return parts[0] + "/" + parts[1], nil
}

func (c *Core) fetchReadme(ctx context.Context, slug string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, githubTimeout)
	defer cancel()

	url := "https://api.github.com/repos/" + slug + "/readme"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github.v3.raw")
	// The credential is read once, in config.Load, and arrives here as a field
	// — never as an os.Getenv in the middle of a request path (SD-1).
	if c.cfg.GitHubToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.GitHubToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %s (cause: %v)", ErrRepoUnavailable, slug, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return "", fmt.Errorf("%w: %s not found, or private with no MIMIR_GITHUB_TOKEN set", ErrRepoUnavailable, slug)
	case http.StatusForbidden, http.StatusTooManyRequests:
		return "", fmt.Errorf("%w: %s rate-limited — set MIMIR_GITHUB_TOKEN to raise the limit", ErrRepoUnavailable, slug)
	default:
		return "", fmt.Errorf("%w: %s returned HTTP %d", ErrRepoUnavailable, slug, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, githubReadmeMaxBytes))
	if err != nil {
		return "", fmt.Errorf("%w: %s (cause: %v)", ErrRepoUnavailable, slug, err)
	}
	if len(body) == 0 {
		return "", fmt.Errorf("%w: %s has an empty README", ErrRepoUnavailable, slug)
	}
	return string(body), nil
}
