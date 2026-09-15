package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
	health  time.Duration

	// environ answers what environment these subprocesses get — which
	// credential slot they spend, and which of the launching session's
	// variables they must not inherit. Read at each call so a change made in
	// the app takes effect without a restart. Nil means "whatever this process
	// has", which is what a binary with no account registry can honestly say.
	environFn func() []string
}

// NewClaude builds the claude provider. cfg.ClaudeModel is pinned (SD-5).
func NewClaude(cfg config.Config) *Claude {
	cliPath := cfg.ClaudeCLIPath
	if cliPath == "" {
		cliPath = "claude"
	}
	return &Claude{
		cliPath: cliPath,
		model:   cfg.ClaudeModel,
		timeout: cfg.RefineTimeout,
		health:  cfg.LLMHealthTimeout,
	}
}

// UseEnviron decides what environment these subprocesses run with.
//
// It exists because the daemon's own model calls are not coding runs: nothing
// dispatched them, so before this they spent whichever identity the daemon
// happened to inherit — in a dev shell, the operator's own session. The daemon
// passes a builder that reads the marked account and calls account.Environ.
//
// A function rather than a slot string for two reasons: the account can change
// while the daemon runs, and `internal/account` cannot be imported here
// (account → store → refine → llm is a cycle), so the rule stays owned by that
// package and is handed in at the wiring point.
func (c *Claude) UseEnviron(fn func() []string) { c.environFn = fn }

// environ is the child's environment, or nil to inherit this process's.
func (c *Claude) environ() []string {
	if c.environFn == nil {
		return nil
	}
	return c.environFn()
}

func (c *Claude) Name() string  { return "claude" }
func (c *Claude) Model() string { return c.model }

// Capabilities: the claude CLI honours a schema through its prompt envelope,
// and it is the agentic one — `internal/coderunner` runs the same binary for a
// board card. This package's use of it is deliberately not agentic
// (`--disallowedTools`), but the capability describes the CLI, not one caller's
// flags, because task-95's runner reads the same table.
func (c *Claude) Capabilities() Capabilities {
	return Capabilities{StructuredOutput: true, Agentic: true}
}

// WithModel returns the same provider bound to a different model.
//
// A copy rather than a mutation, for the reason Agy.WithModel gives: the
// router is shared, and a per-request model must not leak into a concurrent
// call. The copy carries environFn with it, so the chosen credential slot is
// still the one the daemon marked.
func (c *Claude) WithModel(model string) Provider {
	if c == nil || model == "" || model == c.model {
		return c
	}
	cp := *c
	cp.model = model
	return &cp
}

// healthBudget is how long a liveness probe may take — short, because the
// answer to "is this CLI runnable" arrives promptly or not at all.
func (c *Claude) healthBudget() time.Duration {
	if c.health > 0 {
		return c.health
	}
	return c.timeout
}

func (c *Claude) unavailable(cause error) error {
	return fmt.Errorf("%w: `%s` CLI not usable (model %s) — run `claude login` to authenticate, or check it is on PATH (cause: %v)",
		ErrProviderUnavailable, c.cliPath, c.model, cause)
}

// ErrRateLimited means the CLI ran and authenticated fine but the account's
// quota is spent. It wraps ErrProviderUnavailable so existing callers keep
// their fallback behaviour, while a caller that wants to say "try again later"
// rather than "log in" can tell the two apart.
var ErrRateLimited = fmt.Errorf("%w: rate limited", ErrProviderUnavailable)

// reportedError turns a CLI turn that ran but failed into an error a reader can
// act on. The CLI puts the human-readable reason in Result and the upstream
// status in APIErrorStatus; the old message reported neither, so a spent quota
// arrived as "run `claude login`" — advice that cannot fix it.
func (c *Claude) reportedError(raw claudeResult) error {
	detail := strings.TrimSpace(raw.Result)
	if detail == "" {
		detail = "subtype " + raw.Subtype
	}
	if raw.APIErrorStatus == http.StatusTooManyRequests {
		return fmt.Errorf("%w: `%s` quota is spent (model %s) — %s",
			ErrRateLimited, c.cliPath, c.model, detail)
	}
	if raw.APIErrorStatus != 0 {
		return fmt.Errorf("%w: `%s` CLI turn failed with HTTP %d (model %s) — %s",
			ErrProviderUnavailable, c.cliPath, raw.APIErrorStatus, c.model, detail)
	}
	return c.unavailable(fmt.Errorf("claude CLI reported an error: %s", detail))
}

// Health checks that the CLI is installed and runnable.
func (c *Claude) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.healthBudget())
	defer cancel()

	probe := exec.CommandContext(ctx, c.cliPath, "--version")
	probe.Env = c.environ()
	if err := probe.Run(); err != nil {
		return c.unavailable(err)
	}
	return nil
}

// claudeResult is the shape of `claude -p --output-format json`'s stdout.
type claudeResult struct {
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
	Subtype string `json:"subtype"`
	// APIErrorStatus is the upstream HTTP status when the CLI itself ran fine
	// but the API refused the turn. A 429 is a spent quota, not a broken
	// install, and the two need different advice — see reportedError.
	APIErrorStatus int `json:"api_error_status"`
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

	stdout, stderr, lastErr := runCLI(ctx, cliRun{
		path:    c.cliPath,
		args:    args,
		env:     c.environ(),
		stdin:   r.User,
		timeout: c.timeout,
	})
	if errors.Is(lastErr, context.Canceled) || errors.Is(lastErr, context.DeadlineExceeded) {
		return Response{}, lastErr
	}

	if lastErr != nil {
		// A non-zero exit is not the end of the story: the CLI still prints its
		// result JSON on stdout, and that is the only place the reason lives —
		// a spent quota exits 1 with an empty stderr, so reporting stderr alone
		// turned "you've hit your session limit" into "run `claude login`".
		var raw claudeResult
		if json.Unmarshal(stdout, &raw) == nil && raw.IsError {
			return Response{}, c.reportedError(raw)
		}
		return Response{}, c.unavailable(fmt.Errorf("%w: %s", lastErr, string(stderr)))
	}

	var raw claudeResult
	if err := json.Unmarshal(stdout, &raw); err != nil {
		return Response{}, fmt.Errorf("failed to parse claude CLI JSON output: %w", err)
	}
	if raw.IsError {
		return Response{}, c.reportedError(raw)
	}

	return Response{Text: raw.Result, Provider: c.Name(), Model: c.model}, nil
}
