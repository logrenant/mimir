package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/logrenant/mimir/internal/config"
)

// Ollama is a local model runner as a provider.
//
// It is the only provider here that costs nothing and needs no login: the
// models are files on this machine. That makes it the right answer for exactly
// the work the operator would otherwise think twice about — a machine-wide
// Brain scan is thousands of calls — and the wrong answer for anything that
// needs a schema.
//
// ---------------------------------------------------------------------------
// Two things were measured rather than assumed.
// ---------------------------------------------------------------------------
// **stdout is clean.** `ollama run` draws a spinner and ANSI cursor
// escapes while it works, and a first probe (which merged the streams) showed
// them wrapped around the answer — which would have justified writing an escape
// stripper. Discarding stderr instead showed stdout carrying exactly `OK\n\n`:
// the decoration is all on stderr, so the answer is the trimmed stdout and
// nothing has to be unpicked. Observing beat guessing by one command.
//
// **`--hidethinking` is not optional.** `qwen3:8b` is installed here and its
// capabilities include `thinking`; without the flag the model's reasoning is
// part of the output, and every caller in this package parses what it gets.
type Ollama struct {
	cliPath string
	model   string
	timeout time.Duration
	health  time.Duration
}

// NewOllama builds the provider. An empty path is not defaulted to "ollama",
// for the reason NewAgy gives about tests reaching their own fake CLI.
func NewOllama(cfg config.Config) *Ollama {
	return &Ollama{
		cliPath: cfg.OllamaCLIPath,
		model:   cfg.OllamaModel,
		timeout: cfg.OllamaTimeout,
		health:  cfg.LLMHealthTimeout,
	}
}

func (o *Ollama) Name() string  { return "ollama" }
func (o *Ollama) Model() string { return o.model }

// Capabilities: `ollama run --format json` exists but takes a format name, not
// a schema, so it cannot honour Request.Schema — and a provider that returns
// *some* JSON rather than *this* JSON is exactly the silent failure
// Capabilities exists to prevent. Not agentic: this is a model runner, not an
// agent loop.
func (o *Ollama) Capabilities() Capabilities {
	return Capabilities{StructuredOutput: false, Agentic: false}
}

func (o *Ollama) WithModel(model string) Provider {
	if model == "" || model == o.model {
		return o
	}
	cp := *o
	cp.model = model
	return &cp
}

func (o *Ollama) unavailable(cause error) error {
	return fmt.Errorf("%w: ollama: %v", ErrProviderUnavailable, cause)
}

func (o *Ollama) healthBudget() time.Duration {
	if o.health > 0 {
		return o.health
	}
	return 5 * time.Second
}

// Health runs `ollama list`, which answers two questions at once: the binary is
// there, and the server it talks to is up. `--version` would only answer the
// first, and a stopped server is the failure an operator actually hits.
func (o *Ollama) Health(ctx context.Context) error {
	if o.cliPath == "" {
		return o.unavailable(errors.New("no ollama CLI path configured"))
	}
	_, stderr, err := runCLI(ctx, cliRun{
		path:    o.cliPath,
		args:    []string{"list"},
		timeout: o.healthBudget(),
	})
	if err != nil {
		return o.unavailable(fmt.Errorf("%w: %s", err, strings.TrimSpace(string(stderr))))
	}
	return nil
}

func (o *Ollama) Complete(ctx context.Context, r Request) (Response, error) {
	if o.cliPath == "" {
		return Response{}, o.unavailable(errors.New("no ollama CLI path configured"))
	}
	// The router refuses this pairing before anything is spent; a direct caller
	// that went around it gets the same answer rather than JSON-shaped prose it
	// will fail to parse.
	if len(r.Schema) > 0 {
		return Response{}, fmt.Errorf("%w: ollama", ErrNoStructuredOutput)
	}

	content := r.User
	if r.System != "" {
		content = r.System + "\n\n---\n\n" + r.User
	}

	stdout, stderr, err := runCLI(ctx, cliRun{
		path: o.cliPath,
		// `--hidethinking` because a thinking model's reasoning would otherwise
		// be part of the answer; `--nowordwrap` so the text is the model's line
		// breaks and not the terminal's.
		args:    []string{"run", o.model, "--hidethinking", "--nowordwrap"},
		stdin:   content,
		timeout: o.timeout,
	})
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return Response{}, err
	}
	if err != nil {
		return Response{}, o.unavailable(fmt.Errorf("%w: %s", err, strings.TrimSpace(string(stderr))))
	}

	text := strings.TrimSpace(string(stdout))
	if text == "" {
		return Response{}, o.unavailable(errors.New("empty answer"))
	}
	return Response{Text: text, Provider: o.Name(), Model: o.model}, nil
}

// Models is what this machine has pulled, filtered to the ones that can answer.
//
// A fixed list in `config.LLMProviders` would be wrong here in a way it is not
// wrong for the other providers: the others' models are a vendor's catalogue,
// and these are files the operator downloaded. So the table lists none and this
// answers instead.
//
// The filter is not a name heuristic. `ollama show <model>` reports a
// Capabilities block — `embedding` for `nomic-embed-text`, `completion` for
// `qwen3` — so an embedding model is excluded because it says it is one, not
// because its name looked like one.
func (o *Ollama) Models(ctx context.Context) ([]string, error) {
	if o.cliPath == "" {
		return nil, o.unavailable(errors.New("no ollama CLI path configured"))
	}
	stdout, stderr, err := runCLI(ctx, cliRun{
		path:    o.cliPath,
		args:    []string{"list"},
		timeout: o.healthBudget(),
	})
	if err != nil {
		return nil, o.unavailable(fmt.Errorf("%w: %s", err, strings.TrimSpace(string(stderr))))
	}

	var out []string
	for _, name := range parseOllamaList(string(stdout)) {
		if o.canComplete(ctx, name) {
			out = append(out, name)
		}
	}
	return out, nil
}

// parseOllamaList takes the first column of `ollama list`, minus its header.
func parseOllamaList(out string) []string {
	var names []string
	for i, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		// The header row is "NAME ID SIZE MODIFIED". Skipping by index rather
		// than by matching the word means a localised or reordered header does
		// not smuggle a model called "NAME" into the list.
		if i == 0 {
			continue
		}
		names = append(names, fields[0])
	}
	return names
}

// canComplete asks the model what it can do. A failure to answer is read as
// "no": an unusable entry in a picker is worse than a missing one.
func (o *Ollama) canComplete(ctx context.Context, model string) bool {
	stdout, _, err := runCLI(ctx, cliRun{
		path:    o.cliPath,
		args:    []string{"show", model},
		timeout: o.healthBudget(),
	})
	if err != nil {
		return false
	}
	return hasCompletionCapability(string(stdout))
}

// hasCompletionCapability reads the Capabilities block of `ollama show`.
//
// It looks for the word on its own line rather than anywhere in the output,
// because the model's own licence text and parameter names are in there too.
func hasCompletionCapability(out string) bool {
	inBlock := false
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.EqualFold(trimmed, "Capabilities") {
			inBlock = true
			continue
		}
		if inBlock {
			if trimmed == "" {
				continue
			}
			// The block ends at the next heading — a line that is not indented.
			if !strings.HasPrefix(line, "    ") {
				return false
			}
			if strings.EqualFold(trimmed, "completion") {
				return true
			}
		}
	}
	return false
}
