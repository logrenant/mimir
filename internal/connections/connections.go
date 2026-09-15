// Package connections is the operator's list of ways to reach models, and the
// gate that decides whether a (connection, model) pair may be spent.
//
// It sits between `internal/config` — which ships the closed set of adapters
// and the built-in catalogue — and `internal/llm`, which routes. It is its own
// package for the reason `internal/settings` is: what lives here is written by
// the operator at runtime and persisted, which is exactly what `internal/config`
// must never hold (SD-1).
//
// It is also the package `internal/llm` may not import. The cycle is
// connections → store → refine → llm, the same one `internal/account` lives
// with, and the answer is the same: the router is *told* about connections
// through `Router.UseConnections` rather than reaching for them.
package connections

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/llm"
)

var (
	// ErrUnknown means no connection carries that id.
	ErrUnknown = errors.New("connections: no such connection")
	// ErrNotOffered means the connection exists but does not offer that model.
	ErrNotOffered = errors.New("connections: that connection does not offer that model")
	// ErrBuiltin means a caller tried to delete something that ships with the
	// binary. Built-ins are disabled, never removed: deleting one would leave a
	// saved selection naming an id nothing can resolve.
	ErrBuiltin = errors.New("connections: a built-in connection cannot be deleted")
	// ErrComingSoon means the catalogue knows this provider and this build
	// cannot run it yet.
	//
	// A distinct error rather than a generic refusal, because it is a different
	// answer: the operator did not make a mistake, and the fix is not on their
	// side. The API turns it into a sentence naming the provider.
	ErrComingSoon = errors.New("connections: this provider is not connectable yet")
)

// Registry is every connection this daemon knows.
//
// The built-in half comes from `config.LLMProviders` and is not the operator's
// to invent; the rest is theirs. Both halves answer the same two questions,
// which is the whole reason this type exists rather than two call sites asking
// two different tables.
type Registry struct {
	cfg config.Config
}

func New(cfg config.Config) *Registry { return &Registry{cfg: cfg} }

// Specs is every connection, in a stable order, for the router and the API.
//
// Built-ins are derived rather than stored: their CLI path and their pinned
// model catalogue stay in `internal/config`, and a row would only be able to
// contribute a label. Deriving them is what keeps this table from becoming a
// back door around SD-1.
func (r *Registry) Specs() []llm.ConnectionSpec {
	if r == nil {
		return nil
	}
	out := make([]llm.ConnectionSpec, 0, len(r.cfg.LLMProviders))
	for _, p := range r.cfg.LLMProviders {
		out = append(out, builtinSpec(p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// builtinSpec turns one shipped provider entry into a connection.
//
// The adapter is derived from the id rather than stored beside it, because the
// shipped table has exactly one adapter per entry and a second field would be a
// second place for them to disagree.
func builtinSpec(p config.LLMProviderChoice) llm.ConnectionSpec {
	models := make([]llm.ModelSpec, 0, len(p.Models))
	for _, m := range p.Models {
		models = append(models, llm.ModelSpec{ID: m.ID, Label: m.Label})
	}
	return llm.ConnectionSpec{
		ID:           p.ID,
		Label:        p.Label,
		Vendor:       builtinVendor(p.ID),
		Adapter:      builtinAdapter(p.ID),
		DefaultModel: p.DefaultModel,
		Models:       models,
		Discovered:   p.Discovered,
		Enabled:      true,
		Builtin:      true,
	}
}

func builtinAdapter(id string) llm.Adapter {
	switch id {
	case "claude":
		return llm.AdapterClaudeCLI
	case "agy":
		return llm.AdapterAgyCLI
	case "gemini":
		return llm.AdapterGeminiCLI
	case "ollama":
		return llm.AdapterOllamaCLI
	}
	return ""
}

// builtinVendor is who bills for it — which is not the same as which binary
// runs. `agy` is Google's: Antigravity has no subscription of its own and rides
// a Google AI plan, so an operator looking at the two Google rows is looking at
// one company's bill.
func builtinVendor(id string) string {
	switch id {
	case "claude":
		return "anthropic"
	case "agy", "gemini":
		return "google"
	case "ollama":
		return "local"
	}
	return ""
}

// Catalogue is every way to reach models this product knows about, whether or
// not this build can run it.
//
// Published so the picker is the final one: an operator sees what is coming and
// what is here, and adding an adapter later changes a status rather than a
// screen.
func (r *Registry) Catalogue() []llm.CatalogueEntry { return llm.Catalogue() }

// Add is the extension point, and today it refuses everything it is offered.
//
// That is not a placeholder — it is the shape working. Every entry an operator
// could add is an API provider, and none of those adapters exists yet
// (`internal/llm/catalogue.go` says why). So this route validates, resolves the
// catalogue entry, and returns ErrComingSoon naming it. When an adapter is
// written, this function starts succeeding **with no change to its signature,
// its route, or the screen that calls it**, which is the whole reason for
// writing it now rather than when the first adapter lands.
func (r *Registry) Add(catalogueID, label string) (llm.ConnectionSpec, error) {
	if r == nil {
		return llm.ConnectionSpec{}, ErrUnknown
	}
	entry, ok := llm.CatalogueEntryByID(catalogueID)
	if !ok {
		return llm.ConnectionSpec{}, fmt.Errorf("%w: %s", ErrUnknown, catalogueID)
	}
	if !entry.Adapter.Implemented() || entry.Status != llm.StatusAvailable {
		return llm.ConnectionSpec{}, fmt.Errorf("%w: %s", ErrComingSoon, entry.Label)
	}
	// An available entry is a built-in CLI, and those are seeded rather than
	// added: there is exactly one Claude Code CLI on a machine, and a second
	// row for it would be two names for one login.
	return llm.ConnectionSpec{}, fmt.Errorf("%w: %s", ErrBuiltin, entry.Label)
}

// Get returns one connection.
func (r *Registry) Get(id string) (llm.ConnectionSpec, error) {
	if r == nil {
		return llm.ConnectionSpec{}, ErrUnknown
	}
	for _, spec := range r.Specs() {
		if spec.ID == id {
			return spec, nil
		}
	}
	return llm.ConnectionSpec{}, fmt.Errorf("%w: %s", ErrUnknown, id)
}

// Allows reports whether this daemon will run that model on that connection.
//
// This is where the allow-list moved to, and the argument splits honestly by
// transport rather than being one rule stretched over two:
//
//   - **CLI adapters**: the model name becomes argv to a subprocess, so it must
//     be a name this build shipped — or, for a connection whose models are
//     files on the machine, a name of the right *shape* (`config.HasLLMModel`
//     already draws exactly this distinction).
//   - **API adapters**: there is no command. The name lands in a JSON body over
//     TLS, so the remaining risk is spending on a model the operator did not
//     pick, and list membership answers that.
//
// An empty model is "that connection's default" and is valid everywhere.
func (r *Registry) Allows(id, model string) bool {
	spec, err := r.Get(id)
	if err != nil {
		return false
	}
	if !spec.Enabled {
		return false
	}
	if strings.TrimSpace(model) == "" {
		return true
	}
	if spec.Builtin {
		// The shipped half keeps its own gate, unchanged.
		return r.cfg.HasLLMModel(id, model)
	}
	for _, m := range spec.Models {
		if m.ID == model {
			return true
		}
	}
	return spec.Discovered && config.IsModelName(model)
}

// DefaultModel is what a connection runs when the operator names no model.
func (r *Registry) DefaultModel(id string) string {
	spec, err := r.Get(id)
	if err != nil {
		return ""
	}
	return spec.DefaultModel
}
