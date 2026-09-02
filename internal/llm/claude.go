package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/logrenant/mimir/internal/config"
)

// disallowedTools are force-denied on every headless claude call so the
// subprocess can never act on injected content — it only ever produces text.
var disallowedTools = []string{
	"Bash", "Read", "Write", "Edit", "Grep", "Glob",
	"WebFetch", "WebSearch", "Task", "NotebookEdit", "TodoWrite",
}

// Claude is the `claude` CLI as a provider. The flags below are the ones that
// make the subprocess harmless — headless, restricted, every built-in tool
// denied, no session persistence, no MCP config to recurse into — and they are
// chosen in exactly one place so they cannot drift apart between callers.
type Claude struct {
	cliPath string
	model   string
	timeout time.Duration
}

// NewClaude builds the claude provider. cfg.ClaudeModel is pinned (SD-5).
func NewClaude(cfg config.Config) *Claude {
	cliPath := cfg.ClaudeCLIPath
	if cliPath == "" {
		cliPath = "claude"
	}
	return &Claude{cliPath: cliPath, model: cfg.ClaudeModel, timeout: cfg.RefineTimeout}
}

func (c *Claude) Name() string  { return "claude" }
func (c *Claude) Model() string { return c.model }

func (c *Claude) unavailable(cause error) error {
	return fmt.Errorf("%w: `%s` CLI not usable (model %s) — run `claude login` to authenticate, or check it is on PATH (cause: %v)",
		ErrProviderUnavailable, c.cliPath, c.model, cause)
}

// Health checks that the CLI is installed and runnable.
func (c *Claude) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	if err := exec.CommandContext(ctx, c.cliPath, "--version").Run(); err != nil {
		return c.unavailable(err)
	}
	return nil
}

// claudeResult is the shape of `claude -p --output-format json`'s stdout.
type claudeResult struct {
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
	Subtype string `json:"subtype"`
}

// Complete runs one headless turn.
//
// Request.Schema is ignored: this CLI has no structured-output flag in the
// restricted profile, so a caller that wants JSON asks for it in the prompt and
// parses Response.Text. Leaving Structured nil is the documented signal for
// that, not a failure.
func (c *Claude) Complete(ctx context.Context, r Request) (Response, error) {
	args := []string{
		"-p",
		"--model", c.model,
		"--output-format", "json",
		"--no-session-persistence",
		"--strict-mcp-config",
		"--restricted",
		"--effort", "low",
		"--system-prompt", r.System,
		"--disallowedTools", strings.Join(disallowedTools, " "),
	}

	var stdout, stderr bytes.Buffer
	var lastErr error

	for attempt := 1; attempt <= 2; attempt++ {
		stdout.Reset()
		stderr.Reset()

		runCtx, cancel := context.WithTimeout(ctx, c.timeout)
		cmd := exec.CommandContext(runCtx, c.cliPath, args...)
		cmd.Stdin = strings.NewReader(r.User)
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		lastErr = cmd.Run()
		cancel()

		if lastErr == nil {
			break
		}
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return Response{}, ctx.Err()
		}
		if attempt < 2 {
			time.Sleep(100 * time.Millisecond)
			continue
		}
	}

	if lastErr != nil {
		return Response{}, c.unavailable(fmt.Errorf("%w: %s", lastErr, stderr.String()))
	}

	var raw claudeResult
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		return Response{}, fmt.Errorf("failed to parse claude CLI JSON output: %w", err)
	}
	if raw.IsError {
		return Response{}, c.unavailable(fmt.Errorf("claude CLI reported an error (subtype %s)", raw.Subtype))
	}

	return Response{Text: raw.Result, Provider: c.Name(), Model: c.model}, nil
}
