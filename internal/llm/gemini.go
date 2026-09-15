package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/logrenant/mimir/internal/config"
)

// Gemini is Google's `gemini` CLI as a provider.
//
// ---------------------------------------------------------------------------
// Every flag here was read off the installed binary, not remembered.
// ---------------------------------------------------------------------------
// `gemini --help` on this machine reports `-m/--model`, `-p/--prompt` (headless,
// appended to stdin), `-s/--sandbox`, `-o/--output-format text|json|stream-json`
// and `--approval-mode default|auto_edit|yolo|plan`. That is the whole surface
// this file uses. task-91 is why: the IKAS dialect profile written from memory
// passed its own fixture and matched nothing anyone actually downloads, and a
// CLI's argv is the same kind of claim.
//
// ---------------------------------------------------------------------------
// Why `-o text` and not `-o json`.
// ---------------------------------------------------------------------------
// The JSON envelope's *error* shape was observed directly —
// `{"session_id":…,"error":{"type","message","code"}}` — because the CLI is
// signed out on this machine and said so in that shape. The *success* shape was
// not: it could not be produced, and reading it out of a minified bundle is
// guessing with extra steps. A parser that guesses between `response` and
// `text` is a parser that silently returns nothing.
//
// `-o text` needs no envelope: stdout is the answer. It costs the token counts
// that the JSON form would have carried, which nothing here reads anyway.
//
// ---------------------------------------------------------------------------
// It cannot do structured output, and that is the important thing about it.
// ---------------------------------------------------------------------------
// There is no `--json-schema` flag. `agy` has one; Brain's distil and relation
// passes send a schema and parse what comes back. So this provider declares
// `StructuredOutput: false` and the router refuses to send it schema-carrying
// work rather than letting a JSON parse fail into a node with no assessment.
type Gemini struct {
	cliPath    string
	model      string
	timeout    time.Duration
	health     time.Duration
	scratchDir string
}

// NewGemini builds the gemini provider. An empty path is not defaulted to
// "gemini", for the reason NewAgy gives: a test with a hand-built Config must
// reach its own fake CLI and not whatever is installed on the machine.
func NewGemini(cfg config.Config) *Gemini {
	return &Gemini{
		cliPath:    cfg.GeminiCLIPath,
		model:      cfg.GeminiModel,
		timeout:    cfg.AgyPrintTimeout,
		health:     cfg.LLMHealthTimeout,
		scratchDir: filepath.Join(filepath.Dir(cfg.StorePath), "scratch"),
	}
}

func (g *Gemini) Name() string  { return "gemini" }
func (g *Gemini) Model() string { return g.model }

// Capabilities: no `--json-schema`, so no structured output. Agentic is true —
// the same binary runs an agent loop with `-o stream-json`, which is task-95's
// business and not this package's.
func (g *Gemini) Capabilities() Capabilities {
	return Capabilities{StructuredOutput: false, Agentic: true}
}

func (g *Gemini) WithModel(model string) Provider {
	if model == "" || model == g.model {
		return g
	}
	cp := *g
	cp.model = model
	return &cp
}

func (g *Gemini) unavailable(cause error) error {
	return fmt.Errorf("%w: gemini: %v", ErrProviderUnavailable, cause)
}

func (g *Gemini) healthBudget() time.Duration {
	if g.health > 0 {
		return g.health
	}
	return 5 * time.Second
}

// Health asks the CLI for its version. It deliberately does not ask a model
// anything: the point is whether the binary is there, and a signed-out CLI
// still answers `--version`. Whether it can actually reach a model is what
// Complete reports, and what Discover reads out of it.
func (g *Gemini) Health(ctx context.Context) error {
	if g.cliPath == "" {
		return g.unavailable(errors.New("no gemini CLI path configured"))
	}
	_, stderr, err := runCLI(ctx, cliRun{
		path:    g.cliPath,
		args:    []string{"--version"},
		timeout: g.healthBudget(),
	})
	if err != nil {
		return g.unavailable(fmt.Errorf("%w: %s", err, strings.TrimSpace(string(stderr))))
	}
	return nil
}

// geminiError is the one envelope shape this file parses, because it is the one
// that was actually observed:
//
//	{"session_id":"…","error":{"type":"Error","message":"Please set an Auth
//	 method …","code":41}}
//
// It arrives on stdout even under `-o text` when the CLI refuses before
// reaching a model, which is why the text path looks for it.
type geminiError struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error"`
}

// workdir is an empty scratch directory, never a repository.
//
// The same argument as agy's: the subprocess's entire input is untrusted text,
// and `gemini` reads its own context files (GEMINI.md, settings) from wherever
// it starts. Starting it in a repo hands that text the repo's instructions.
func (g *Gemini) workdir() (string, error) {
	if g.scratchDir == "" {
		return os.TempDir(), nil
	}
	if err := os.MkdirAll(g.scratchDir, 0o700); err != nil {
		return "", err
	}
	return g.scratchDir, nil
}

func (g *Gemini) Complete(ctx context.Context, r Request) (Response, error) {
	if g.cliPath == "" {
		return Response{}, g.unavailable(errors.New("no gemini CLI path configured"))
	}
	// Belt as well as the router's braces. The router refuses this pairing
	// before anything is spent (CompleteWith), and a direct caller that went
	// around it gets the same answer rather than prose it will fail to parse.
	if len(r.Schema) > 0 {
		return Response{}, fmt.Errorf("%w: gemini", ErrNoStructuredOutput)
	}

	dir, err := g.workdir()
	if err != nil {
		return Response{}, g.unavailable(err)
	}

	content := r.User
	if r.System != "" {
		content = r.System + "\n\n---\n\n" + r.User
	}

	// `--approval-mode plan` is read-only mode, and `--sandbox` is the second
	// wall. Neither is decoration: this package's contract is one-shot text,
	// and a CLI that can edit files is a CLI that can edit files.
	args := []string{
		"--model", g.model,
		"--output-format", "text",
		"--approval-mode", "plan",
		"--sandbox",
		"--prompt", "",
	}

	// The prompt rides stdin. `-p ""` puts the CLI in headless mode and the
	// flag's own help says the prompt is *appended to* stdin, so stdin carries
	// the content and the flag carries nothing — content never goes in argv.
	//
	// MIMIR_NESTED, for the reason agy gets it: a globally registered Mimir MCP
	// server must not be recursable from inside a model call.
	stdout, stderr, lastErr := runCLI(ctx, cliRun{
		path:    g.cliPath,
		args:    args,
		env:     append(os.Environ(), "MIMIR_NESTED=1"),
		dir:     dir,
		stdin:   content,
		timeout: g.timeout + 15*time.Second,
	})
	if errors.Is(lastErr, context.Canceled) || errors.Is(lastErr, context.DeadlineExceeded) {
		return Response{}, lastErr
	}

	text := strings.TrimSpace(string(stdout))

	// A refusal before the model is reached lands on stdout as the observed
	// error envelope, with or without a non-zero exit. Read it first: its
	// message names the cause ("Please set an Auth method…") far better than
	// an exit code.
	if reported := geminiReported(text); reported != nil {
		return Response{}, g.unavailable(reported)
	}
	if lastErr != nil {
		return Response{}, g.unavailable(fmt.Errorf("%w: %s", lastErr, strings.TrimSpace(string(stderr))))
	}
	if text == "" {
		return Response{}, g.unavailable(errors.New("empty answer"))
	}

	return Response{Text: text, Provider: g.Name(), Model: g.model}, nil
}

// geminiReported returns the CLI's own error, or nil when the output is an
// answer rather than an envelope.
func geminiReported(out string) error {
	if !strings.HasPrefix(strings.TrimSpace(out), "{") {
		return nil
	}
	var raw geminiError
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil
	}
	if raw.Error.Message == "" {
		return nil
	}
	return fmt.Errorf("%s (code %d)", raw.Error.Message, raw.Error.Code)
}
