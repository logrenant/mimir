package llm

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// Availability is what a provider looks like on *this machine*.
//
// The registry is not a static list, and that is the consequence of the
// transport decision (task-93): providers are installed CLIs riding their own
// logins, so which ones exist is a property of the machine rather than of the
// build. `internal/account` scans `~/.claude-accounts` for the same reason, and
// `graphify` reports itself absent rather than pretending to be there.
//
// Three states, not two, because they need different answers from the operator.
// Not installed: install it. Installed but signed out: log in — which is exactly
// where `gemini` sits on the machine this was written on, and it says so in its
// own words rather than in ours.
type Availability struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// Installed is whether the binary answered `--version` at all. Free.
	Installed bool `json:"installed"`
	// SignedIn is whether it could actually reach a model. It is only
	// meaningful when Probed is true — establishing it costs a model call, so
	// it is not something a screen opening does on the operator's behalf.
	SignedIn bool `json:"signed_in"`
	Probed   bool `json:"probed"`
	// Detail is the CLI's own sentence about why not, verbatim. It names the
	// cause ("Please set an Auth method in your …/settings.json or specify one
	// of the following environment variables") far better than anything here
	// could paraphrase.
	Detail string `json:"detail,omitempty"`
	// Capabilities travel with availability because a picker needs both: a
	// provider that is installed, signed in, and cannot return structured
	// output is still the wrong choice for Brain's distil.
	StructuredOutput bool `json:"structured_output"`
	Agentic          bool `json:"agentic"`
	// Label, Vendor and Transport come from the connection's spec rather than
	// from the provider, because they are what it *is* rather than what it
	// does. Empty when the router was never told (`cmd/mimir-mcp` has no
	// registry and still routes).
	Label     string    `json:"label,omitempty"`
	Vendor    string    `json:"vendor,omitempty"`
	Transport Transport `json:"transport,omitempty"`
	// Models is what this machine actually holds, for the providers whose model
	// list is not a vendor catalogue but a folder of files the operator pulled.
	// Empty for everyone else, whose models are in `config.LLMProviders`.
	Models []string `json:"models,omitempty"`
}

// ModelLister is the optional half of a provider: it knows what it can run on
// *this* machine.
//
// Only `ollama` implements it, and the reason is the difference between a
// vendor's catalogue and a folder: `claude` and `gemini` offer what their
// vendor offers, and that belongs in the pinned table (SD-5). Ollama offers
// what was downloaded, and a pinned list would be a list of somebody else's
// machine.
type ModelLister interface {
	Models(ctx context.Context) ([]string, error)
}

// probePrompt is the smallest thing worth asking. It exists to find out whether
// the login works, so it must cost as close to nothing as a call can.
const probePrompt = "Reply with the single word: ok"

// Discover probes every registered provider and reports what it found.
//
// `probe` is the difference between the two questions, and they are separated
// because they have different prices. "Is it installed" is `--version`: free,
// and the right thing for a screen to ask when it opens. "Does the login work"
// is a real completion: cheap, but not free, and not something to spend on
// every poll of a settings page. The operator asks for it.
//
// Bounded concurrency (SD-3) rather than one goroutine per provider: this is a
// small set today and the bound is what stops it becoming a fan-out later.
func Discover(ctx context.Context, r *Router, probe bool, probeTimeout time.Duration) []Availability {
	if r == nil {
		return nil
	}

	providers := r.Providers()
	out := make([]Availability, len(providers))

	var wg sync.WaitGroup
	limit := make(chan struct{}, discoverConcurrency)

	for i, p := range providers {
		wg.Add(1)
		go func(i int, p Provider) {
			defer wg.Done()
			limit <- struct{}{}
			defer func() { <-limit }()
			spec, _ := r.Spec(p.Name())
			out[i] = probeOne(ctx, p, probe, probeTimeout, spec)
		}(i, p)
	}
	wg.Wait()

	sort.Slice(out, func(i, j int) bool { return out[i].Provider < out[j].Provider })
	return out
}

// Discover is the router's own method, so a caller that holds a router does not
// have to hold the package too. `internal/api` takes it as a one-method
// interface for the reason that package states about itself: a handler is a
// door, not a floor.
func (r *Router) Discover(ctx context.Context, probe bool, probeTimeout time.Duration) []Availability {
	return Discover(ctx, r, probe, probeTimeout)
}

// ProbeOne asks about a single connection.
//
// Its own entry point rather than a filter over Discover, because it is a
// different question with a different price: Discover is "what is on this
// machine" and this is "does *this* login work", asked because somebody pressed
// a button on one row and is waiting for that row.
func (r *Router) ProbeOne(ctx context.Context, id string, probeTimeout time.Duration) (Availability, bool) {
	if r == nil {
		return Availability{}, false
	}
	p, ok := r.byID[id]
	if !ok || p == nil {
		return Availability{}, false
	}
	spec, _ := r.Spec(id)
	return probeOne(ctx, p, true, probeTimeout, spec), true
}

// discoverConcurrency bounds the fan-out. Three providers today; the bound is
// here so a fourth does not turn a settings screen into a thundering herd of
// subprocesses.
const discoverConcurrency = 3

func probeOne(ctx context.Context, p Provider, probe bool, probeTimeout time.Duration, spec ConnectionSpec) Availability {
	caps := p.Capabilities()
	a := Availability{
		Provider:         p.Name(),
		Model:            p.Model(),
		StructuredOutput: caps.StructuredOutput,
		Agentic:          caps.Agentic,
		Label:            spec.Label,
		Vendor:           spec.Vendor,
	}
	if spec.ID != "" {
		a.Transport = spec.Transport()
	}

	if err := p.Health(ctx); err != nil {
		a.Detail = reason(err)
		return a
	}
	a.Installed = true

	if lister, ok := p.(ModelLister); ok {
		// A best effort: a provider that cannot say what it holds is still a
		// provider, and the table's own list is what a picker falls back to.
		if models, err := lister.Models(ctx); err == nil {
			a.Models = models
		}
	}

	if !probe {
		return a
	}
	a.Probed = true

	// Bounded, and separately from the provider's own timeout. A completion may
	// legitimately take minutes; "is this login usable" may not, because
	// somebody is watching the button they pressed.
	if probeTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, probeTimeout)
		defer cancel()
	}

	// No schema: this asks whether the login works, and a provider that cannot
	// serve a schema would be refused for the wrong reason.
	if _, err := p.Complete(ctx, Request{User: probePrompt}); err != nil {
		a.Detail = reason(err)
		return a
	}
	a.SignedIn = true
	return a
}

// reason is the provider's message with this package's own prefix taken off.
//
// The operator is being shown the CLI's sentence, and "llm: provider
// unavailable: gemini: " in front of it is our bookkeeping, not their
// information.
func reason(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	msg = strings.TrimPrefix(msg, ErrProviderUnavailable.Error()+": ")
	return strings.TrimSpace(msg)
}
