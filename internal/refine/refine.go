package refine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/logrenant/goat-mcp/internal/config"
)

var (
	// ErrClaudeUnavailable means the local `claude` CLI could not be run,
	// is not authenticated, or returned an error — the refiner run-time
	// dependency (AGENT_RULES §1.4).
	ErrClaudeUnavailable = errors.New("refine: claude CLI unavailable")
)

// disallowedTools are force-denied on every headless refine call so the
// refiner can never act on injected content — it only ever produces text.
var disallowedTools = []string{
	"Bash", "Read", "Write", "Edit", "Grep", "Glob",
	"WebFetch", "WebSearch", "Task", "NotebookEdit", "TodoWrite",
}

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

type Client struct {
	cliPath string
	model   string
	cfg     config.Config
}

func New(cfg config.Config) *Client {
	cliPath := cfg.ClaudeCLIPath
	if cliPath == "" {
		cliPath = "claude"
	}
	return &Client{
		cliPath: cliPath,
		model:   cfg.ClaudeModel,
		cfg:     cfg,
	}
}

func (c *Client) unavailable(cause error) error {
	return fmt.Errorf("%w: `%s` CLI not usable (model %s) — run `claude login` to authenticate, or check it is on PATH (cause: %v)",
		ErrClaudeUnavailable, c.cliPath, c.model, cause)
}

// Health checks that the `claude` CLI is installed and runnable.
func (c *Client) Health(ctx context.Context) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.RefineTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.cliPath, "--version")
	if err := cmd.Run(); err != nil {
		return false, c.unavailable(err)
	}
	return true, nil
}

// cliResult is the shape of `claude -p --output-format json`'s stdout.
type cliResult struct {
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
	Subtype string `json:"subtype"`
}

// Distil sends the markdown and query to the local `claude` CLI (headless,
// tool-less, single-turn) to be distilled. This is the context-isolation
// firewall (SD-2): untrusted scraped Markdown goes in, compact factual text
// comes out.
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

// run is the one place the refiner's subprocess is invoked. Both prompt
// profiles — Distil's page summary and Classify's closed-vocabulary choice —
// go through it, so the flags that make the subprocess harmless (headless,
// `--restricted`, every built-in tool force-denied, no session persistence, no
// MCP config to recurse into) are chosen once and cannot drift apart.
//
// It returns the model's raw `result` string. Deciding whether that string is
// acceptable belongs to the caller's profile: prose is clamped, JSON is parsed
// against a closed set.
func (c *Client) run(ctx context.Context, systemPrompt, userContent string) (string, error) {
	args := []string{
		"-p",
		"--model", c.model,
		"--output-format", "json",
		"--no-session-persistence",
		"--strict-mcp-config",
		"--restricted",
		"--effort", "low",
		"--system-prompt", systemPrompt,
		"--disallowedTools", strings.Join(disallowedTools, " "),
	}

	var stdout, stderr bytes.Buffer
	var lastErr error

	for attempt := 1; attempt <= 2; attempt++ {
		stdout.Reset()
		stderr.Reset()

		runCtx, cancel := context.WithTimeout(ctx, c.cfg.RefineTimeout)
		cmd := exec.CommandContext(runCtx, c.cliPath, args...)
		cmd.Stdin = strings.NewReader(userContent)
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		lastErr = cmd.Run()
		cancel()

		if lastErr == nil {
			break
		}
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", ctx.Err()
		}
		if attempt < 2 {
			time.Sleep(100 * time.Millisecond)
			continue
		}
	}

	if lastErr != nil {
		return "", c.unavailable(fmt.Errorf("%w: %s", lastErr, stderr.String()))
	}

	var raw cliResult
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		return "", fmt.Errorf("failed to parse claude CLI JSON output: %w", err)
	}

	if raw.IsError {
		return "", c.unavailable(fmt.Errorf("claude CLI reported an error (subtype %s)", raw.Subtype))
	}

	return raw.Result, nil
}
