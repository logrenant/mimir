package refine

import (
	"context"
	"errors"
	"fmt"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/llm"
)

var (
	// ErrClaudeUnavailable means the local `claude` CLI could not be run,
	// is not authenticated, or returned an error — the refiner run-time
	// dependency (AGENT_RULES §1.4).
	ErrClaudeUnavailable = errors.New("refine: claude CLI unavailable")
)

type Input struct {
	Query        string
	PageMarkdown string
	SourceURL    string
	MaxTokens    int
}

type Output struct {
	Text          string
	Refined       bool
	Truncated     bool
	TokenEstimate int
}

// Client is the five prompt profiles. It no longer owns a subprocess: the
// exec, the flags and the retry live in internal/llm, which is also where the
// second provider is, so the profiles here choose a *class of work* and let
// the router decide which CLI answers it.
type Client struct {
	router *llm.Router
}

func New(cfg config.Config) *Client {
	return &Client{router: llm.NewRouter(cfg)}
}

// unavailable keeps this package's own sentinel over whatever the router
// reported. Callers across the repo test for ErrClaudeUnavailable and turn it
// into the "run `claude login`" remedy line, and a provider swap underneath is
// not a reason for those branches to stop matching.
func (c *Client) unavailable(cause error) error {
	return fmt.Errorf("%w: %v", ErrClaudeUnavailable, cause)
}

// Health checks that the refiner's providers are usable. It reports healthy if
// the distil tier answers, because that is the tier every profile but the gap
// analysis rides.
//
// It used to accept the reason tier answering as good enough, on the grounds
// that the distil tier could fall back to it. Since task-51 it cannot: the
// distil class has no fallback, so claude being reachable says nothing about
// whether a page can be summarised. Reporting healthy on that basis would send
// an operator looking for the fault everywhere except where it is.
func (c *Client) Health(ctx context.Context) (bool, error) {
	p := c.router.Provider(llm.Distill)
	if p == nil {
		return false, c.unavailable(errors.New("no distil provider configured"))
	}
	if err := p.Health(ctx); err != nil {
		alt := c.router.Fallback(llm.Distill)
		if alt == nil {
			return false, c.unavailable(err)
		}
		if altErr := alt.Health(ctx); altErr != nil {
			return false, c.unavailable(err)
		}
	}
	return true, nil
}

// Distil sends the markdown and query to the distil provider (headless,
// single-turn) to be distilled. This is the context-isolation firewall (SD-2):
// untrusted scraped Markdown goes in, compact factual text comes out.
func (c *Client) Distil(ctx context.Context, in Input) (Output, error) {
	cleanMd, mdTruncated := sanitizePage(in.PageMarkdown, in.MaxTokens*8)
	in.PageMarkdown = cleanMd

	systemPrompt, userContent := buildPrompt(in)

	result, err := c.run(ctx, systemPrompt, userContent)
	if err != nil {
		return Output{}, err
	}

	clampedText, clampTruncated, err := clampOutput(result, in.MaxTokens, len(cleanMd))
	if err != nil {
		return Output{}, err
	}

	tokens := len(clampedText) / 4

	return Output{
		Text:          clampedText,
		Refined:       true,
		Truncated:     mdTruncated || clampTruncated,
		TokenEstimate: tokens,
	}, nil
}

// run is the default route: distil work.
//
// It returns the model's raw text. Deciding whether that text is acceptable
// belongs to the caller's profile — prose is clamped and validated, JSON is
// parsed against a closed set — which is why nothing is checked here.
func (c *Client) run(ctx context.Context, systemPrompt, userContent string) (string, error) {
	return c.runClass(ctx, llm.Distill, systemPrompt, userContent)
}

// runClass is the one place a prompt profile becomes a model call.
//
// The class, not the profile, is what picks the provider: compressing one page
// or one episode is distil work and goes to the cheap tier, while synthesis
// across sources is not (ROADMAP §B.1). Deciding it here rather than in each
// profile is what keeps that mapping legible in one screen.
func (c *Client) runClass(ctx context.Context, class llm.Class, systemPrompt, userContent string) (string, error) {
	resp, err := c.router.Complete(ctx, class, llm.Request{
		System: systemPrompt,
		User:   userContent,
	})
	if err != nil {
		if errors.Is(err, llm.ErrProviderUnavailable) {
			return "", c.unavailable(err)
		}
		return "", err
	}
	return resp.Text, nil
}
