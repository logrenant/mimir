package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/logrenant/mimir/internal/config"
)

// Agy is the Antigravity CLI as a provider — the distil tier.
//
// Three things about it differ from the claude path and all three are
// load-bearing:
//
//   - Its `-p` is not a boolean; it takes the prompt as the flag's value. So
//     content goes over stdin with `--input-format text`, which is also the
//     only shape that survives a page-sized body (ARG_MAX is 1 MiB).
//   - It has no `--system-prompt`, so the system text is prepended to the user
//     content rather than passed separately.
//   - It has no `--disallowedTools` and no `--strict-mcp-config`. Three things
//     stand in for them: `--sandbox`, a working directory that is an empty
//     scratch dir rather than a repo, and MIMIR_NESTED=1 in the environment,
//     which makes mimir-mcp open with no tools so a globally-registered Mimir
//     cannot be recursed into. This is weaker than the claude path's guarantee
//     and docs/SECURITY.md says so.
type Agy struct {
	cliPath    string
	model      string
	timeout    time.Duration
	health     time.Duration
	scratchDir string
}

// NewAgy builds the agy provider. cfg.DistillModel is pinned (SD-5).
//
// An empty cfg.AgyCLIPath is not defaulted to "agy". config.Validate rejects it,
// so the only way to get here with one is a hand-built Config — which is what
// tests do — and defaulting would send those tests to whatever agy happens to
// be on the machine's PATH instead of the fake CLI they set up. The provider
// reports itself unavailable instead, and the router falls back.
func NewAgy(cfg config.Config) *Agy {
	return &Agy{
		cliPath:    cfg.AgyCLIPath,
		model:      cfg.DistillModel,
		timeout:    cfg.AgyPrintTimeout,
		health:     cfg.LLMHealthTimeout,
		scratchDir: filepath.Join(filepath.Dir(cfg.StorePath), "scratch"),
	}
}

func (a *Agy) Name() string  { return "agy" }
func (a *Agy) Model() string { return a.model }

// WithModel returns the same provider bound to a different model.
//
// A copy rather than a mutation: one router is shared by every concurrent call
// in the daemon, and a per-request model that wrote through to it would decide
// which model somebody else's run spends. Empty means "keep the configured
// one", so a caller with no preference passes its selection through untouched.
func (a *Agy) WithModel(model string) Provider {
	if a == nil || model == "" || model == a.model {
		return a
	}
	cp := *a
	cp.model = model
	return &cp
}

func (a *Agy) unavailable(cause error) error {
	return fmt.Errorf("%w: `%s` CLI not usable (model %s) — run `agy` once to sign in, or check it is on PATH (cause: %v)",
		ErrProviderUnavailable, a.cliPath, a.model, cause)
}

// Health asks the CLI to list models, which is the cheapest call that proves
// both that the binary runs and that it is signed in.
func (a *Agy) Health(ctx context.Context) error {
	if a.cliPath == "" {
		return a.unavailable(errors.New("no agy CLI path configured"))
	}
	ctx, cancel := context.WithTimeout(ctx, a.healthBudget())
	defer cancel()

	if err := exec.CommandContext(ctx, a.cliPath, "models").Run(); err != nil {
		return a.unavailable(err)
	}
	return nil
}

// healthBudget is how long a liveness probe may take. It falls back to the
// call budget only for a hand-built Config that never set one — the daemon's
// own always does.
func (a *Agy) healthBudget() time.Duration {
	if a.health > 0 {
		return a.health
	}
	return a.timeout
}

// agyResult is the shape of `agy --output-format json`'s stdout. structured_output
// is present only when --json-schema was given, and unlike `response` it carries
// exactly the schema's fields — the response string additionally holds the CLI's
// own toolAction/toolSummary keys, which are not ours to hand on.
type agyResult struct {
	Status           string          `json:"status"`
	Response         string          `json:"response"`
	StructuredOutput json.RawMessage `json:"structured_output"`
}

// workdir returns the scratch directory, creating it if needed.
//
// The working directory is a security boundary here, not a convenience: agy
// reads AGENTS.md and .agents/rules from wherever it is started, and starting
// it inside the repo would feed the repo's own instructions to a subprocess
// whose entire input is untrusted scraped text.
func (a *Agy) workdir() (string, error) {
	if a.scratchDir == "" {
		return os.TempDir(), nil
	}
	if err := os.MkdirAll(a.scratchDir, 0o700); err != nil {
		return "", err
	}
	return a.scratchDir, nil
}

func (a *Agy) Complete(ctx context.Context, r Request) (Response, error) {
	if a.cliPath == "" {
		return Response{}, a.unavailable(errors.New("no agy CLI path configured"))
	}

	dir, err := a.workdir()
	if err != nil {
		return Response{}, a.unavailable(err)
	}

	args := []string{
		"--model", a.model,
		"--output-format", "json",
		"--input-format", "text",
		"--sandbox",
		"--disable-slash-commands",
		"--print-timeout", a.timeout.String(),
	}
	if len(r.Schema) > 0 {
		args = append(args, "--json-schema", string(r.Schema))
	}

	content := r.User
	if r.System != "" {
		content = r.System + "\n\n---\n\n" + r.User
	}

	var stdout, stderr bytes.Buffer
	var lastErr error

	for attempt := 1; attempt <= 2; attempt++ {
		stdout.Reset()
		stderr.Reset()

		// The subprocess gets a little longer than its own --print-timeout so
		// that a timeout surfaces as agy's own message rather than a killed
		// process with an empty stderr.
		runCtx, cancel := context.WithTimeout(ctx, a.timeout+15*time.Second)
		cmd := exec.CommandContext(runCtx, a.cliPath, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "MIMIR_NESTED=1")
		cmd.Stdin = strings.NewReader(content)
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
		return Response{}, a.unavailable(fmt.Errorf("%w: %s", lastErr, strings.TrimSpace(stderr.String())))
	}

	var raw agyResult
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		return Response{}, fmt.Errorf("failed to parse agy CLI JSON output: %w", err)
	}
	if !strings.EqualFold(raw.Status, "SUCCESS") {
		return Response{}, a.unavailable(fmt.Errorf("agy CLI reported status %q", raw.Status))
	}

	return Response{
		Text:       raw.Response,
		Structured: raw.StructuredOutput,
		Provider:   a.Name(),
		Model:      a.model,
	}, nil
}
