// Package llm is the single exit point for every non-coding model call.
//
// Before this package there were two hand-written copies of the same
// subprocess dance — one in internal/refine, one in internal/brain — and they
// had already drifted: the second re-invented the retry loop, the JSON envelope
// and the flag set, and got the parsing wrong in a way that silently produced
// nodes with no tags. One exit point is the fix, for the same reason
// tools.RegisterAll is one list.
//
// The routing decision here is by *class of work*, not by taste (ROADMAP §B.1,
// amended 2026-09-02). Distilling one page or tagging one node is the majority
// of the calls and the cheapest half of the work; framing a task or
// synthesising across sources is not. They get different providers, both of
// them a local CLI riding an existing login — no SDK, no API key, every model
// pinned to an exact version (SD-5).
//
// The coding runner is deliberately not a client of this package. Its
// stream-json transport, permission mode and process-group signalling are a
// different contract, and folding them in here would mean this package owned
// two unrelated things.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/logrenant/mimir/internal/config"
)

// ErrProviderUnavailable means a provider's CLI could not be run, is not
// authenticated, or reported an error. Callers decide whether that is fatal:
// a refine cannot proceed without it, an ingest can (SD-6).
var ErrProviderUnavailable = errors.New("llm: provider unavailable")

// Class is what kind of work a call is, which is what decides the provider.
type Class string

const (
	// Distill is one-shot compression: a page summary, an episode recap, a
	// node's assessment and tags, a closed-vocabulary classification.
	Distill Class = "distill"

	// Reason is synthesis across sources, where the extra capability is worth
	// paying for.
	Reason Class = "reason"
)

// Request is one turn. User goes over stdin rather than argv: page content and
// node bodies routinely run past what an argument list can hold, and one of
// the two providers takes its prompt as a flag value.
type Request struct {
	System string
	User   string

	// Schema, when set, asks the provider for structured output. Providers
	// that cannot enforce it leave Response.Structured nil and the caller
	// falls back to parsing Text — so setting it is always safe.
	Schema json.RawMessage

	// MaxTokens is advisory. Clamping the result is the caller's job, because
	// only the caller knows whether an over-long answer should be cut or
	// rejected.
	MaxTokens int
}

// Response carries whichever of the two shapes the provider produced.
type Response struct {
	Text       string
	Structured json.RawMessage
	Provider   string
	Model      string
}

// Provider is one CLI-backed model.
type Provider interface {
	Name() string
	Model() string
	// WithModel returns the same provider bound to a different model, or
	// itself when the name is empty or already current. It returns a copy
	// rather than mutating, because one provider value is shared by every
	// concurrent call in the daemon.
	WithModel(model string) Provider
	Complete(ctx context.Context, r Request) (Response, error)
	Health(ctx context.Context) error
}

// Selection is an operator's per-request override of the class routing.
//
// It exists because the class → provider mapping answers "what kind of work is
// this", which is the right question for the daemon's own background passes and
// the wrong one for a run somebody is watching: a lead-gen run costs real money
// and real minutes, and which tier spends them is a decision the operator is
// entitled to make per run. The zero value means "route it normally", so every
// caller that has no opinion keeps the behaviour it had.
//
// Both fields are names from an allow-list the daemon publishes, never free
// text from a client: they end up as argv to a subprocess.
type Selection struct {
	// Provider is a registered provider name ("agy", "claude"). Empty keeps
	// the class's own provider.
	Provider string
	// Model is the model that provider should run. Empty keeps its configured
	// model.
	Model string
}

// IsZero reports whether the selection expresses no preference at all.
func (s Selection) IsZero() bool { return s.Provider == "" && s.Model == "" }

// Key is a stable identity for the selection, for callers that cache a model's
// answer and must not serve it for a different one. The zero value's key is
// empty, so an unselected run keeps hitting the cache entries it already wrote.
func (s Selection) Key() string {
	if s.IsZero() {
		return ""
	}
	return s.Provider + "/" + s.Model
}

// Router resolves a Class to a Provider, and falls back when the first choice
// cannot run — if a fallback was configured at all.
//
// The fallback is not a retry policy — a provider that answered badly is not
// retried anywhere else — it is availability only. It is also, since task-51,
// switched off: `cfg.DistillFallback` is empty, so `agy` being signed out or
// out of quota stops the distil tier instead of quietly moving the work (and
// the bill) to claude. That is the operator's decision, taken after a
// machine-wide scan made the size of the bill concrete, and the mechanism is
// left standing so putting one word back in config restores it.
type Router struct {
	byClass  map[Class]Provider
	fallback map[Class]Provider
	byName   map[string]Provider

	// modelChain is the same provider tried again on a different model, per
	// class. It runs before the provider-level fallback because it is the
	// cheaper thing to be wrong about: staying on `agy` cannot move the bill
	// to a paid login, it can only reach a second free pool that the first
	// one's exhaustion says nothing about.
	modelChain map[Class][]string

	// claude is kept by concrete type so the daemon can tell it which
	// credential slot to spend. Nothing else reaches past the interface.
	claude *Claude
}

// NewRouter builds the providers named by cfg. It never fails: an unusable
// provider is discovered by Health or by the first call, and a router that
// refused to construct would take the binary down over a CLI that may never be
// asked for.
func NewRouter(cfg config.Config) *Router {
	claude := NewClaude(cfg)
	agy := NewAgy(cfg)

	byName := map[string]Provider{
		claude.Name(): claude,
		agy.Name():    agy,
	}

	pick := func(name string) Provider {
		if p, ok := byName[name]; ok {
			return p
		}
		return claude
	}

	// An unnamed fallback is no fallback. This is deliberately not `pick`,
	// which answers claude for anything it does not recognise: routed through
	// pick, an empty DistillFallback would resolve to the very provider the
	// empty string exists to keep out.
	fallback := map[Class]Provider{}
	if p, ok := byName[cfg.DistillFallback]; ok {
		fallback[Distill] = p
	}

	return &Router{
		byClass: map[Class]Provider{
			Distill: pick(cfg.DistillProvider),
			Reason:  pick(cfg.ReasonProvider),
		},
		fallback:   fallback,
		modelChain: map[Class][]string{Distill: cfg.DistillModelChain},
		byName:     byName,
		claude:     claude,
	}
}

// UseEnviron decides what environment the claude provider's subprocesses run
// with — which credential slot they spend, and which of the launching
// session's variables they must not inherit.
//
// Set after construction rather than through config because the answer is
// operator state the daemon reads at call time, not a pinned value (SD-1), and
// because a router built before the account registry exists must still be
// valid. Only the claude provider takes it: `agy` is a different CLI with its
// own login.
func (r *Router) UseEnviron(fn func() []string) {
	if r == nil || r.claude == nil {
		return
	}
	r.claude.UseEnviron(fn)
}

// Provider returns the primary provider for a class, for callers that need to
// name it (diagnostics, provenance columns).
func (r *Router) Provider(c Class) Provider {
	if r == nil {
		return nil
	}
	return r.byClass[c]
}

// Fallback returns the configured fallback for a class, or nil when there is
// none. A caller that wants to know whether a class can still be served with
// its primary down needs this rather than Provider, which answers about the
// primary of some other class.
func (r *Router) Fallback(c Class) Provider {
	if r == nil {
		return nil
	}
	return r.fallback[c]
}

// Providers returns each distinct provider once, for health reporting.
func (r *Router) Providers() []Provider {
	if r == nil {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]Provider, 0, 2)
	for _, m := range []map[Class]Provider{r.byClass, r.fallback} {
		for _, p := range m {
			if p == nil {
				continue
			}
			if _, dup := seen[p.Name()]; dup {
				continue
			}
			seen[p.Name()] = struct{}{}
			out = append(out, p)
		}
	}
	return out
}

// Complete runs req on the provider for c, falling back once if the primary is
// unavailable and a fallback was configured. A cancelled or expired context is
// returned as-is and never triggers the fallback: the caller went away, and
// starting a second subprocess on its behalf would be work nobody is waiting
// for.
func (r *Router) Complete(ctx context.Context, c Class, req Request) (Response, error) {
	return r.CompleteWith(ctx, c, Selection{}, req)
}

// CompleteWith is Complete with an operator's override applied first.
//
// The fallback is deliberately dropped the moment a selection names a
// provider. A fallback is availability, and availability is the daemon's own
// policy; when an operator has said "run this on agy", quietly running it on
// claude instead spends a different budget than the one they chose. An
// unselected call keeps the fallback it always had.
//
// An unknown provider name is an error rather than a silent fall back to the
// class default, for the same reason: a typo must not become a bill.
func (r *Router) CompleteWith(ctx context.Context, c Class, sel Selection, req Request) (Response, error) {
	if r == nil {
		return Response{}, fmt.Errorf("%w: no router configured", ErrProviderUnavailable)
	}

	primary := r.byClass[c]
	if primary == nil {
		return Response{}, fmt.Errorf("%w: no provider for class %q", ErrProviderUnavailable, c)
	}

	if sel.Provider != "" {
		chosen, ok := r.byName[sel.Provider]
		if !ok || chosen == nil {
			return Response{}, fmt.Errorf("%w: no provider named %q", ErrProviderUnavailable, sel.Provider)
		}
		primary = chosen
	}
	if sel.Model != "" {
		primary = primary.WithModel(sel.Model)
	}

	resp, err := primary.Complete(ctx, req)
	if err == nil {
		return resp, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Response{}, ctxErr
	}
	if !errors.Is(err, ErrProviderUnavailable) {
		return Response{}, err
	}

	// A selection is still honoured to the letter: an operator who named a
	// provider or a model gets that one or an error, never a substitute.
	if sel.IsZero() {
		for _, model := range r.modelChain[c] {
			if model == "" || model == primary.Model() {
				continue
			}
			resp, chainErr := primary.WithModel(model).Complete(ctx, req)
			if chainErr == nil {
				return resp, nil
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				return Response{}, ctxErr
			}
			// A model that is unavailable means this pool is spent too, so the
			// next one is worth trying. Anything else is the model answering
			// badly, which no other model is a remedy for.
			if !errors.Is(chainErr, ErrProviderUnavailable) {
				return Response{}, chainErr
			}
		}
	}

	alt := r.fallback[c]
	if !sel.IsZero() || alt == nil || alt.Name() == primary.Name() {
		return Response{}, err
	}

	resp, altErr := alt.Complete(ctx, req)
	if altErr != nil {
		return Response{}, fmt.Errorf("%w (fallback %s also failed: %v)", err, alt.Name(), altErr)
	}
	return resp, nil
}
