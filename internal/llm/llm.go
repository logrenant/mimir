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
	Complete(ctx context.Context, r Request) (Response, error)
	Health(ctx context.Context) error
}

// Router resolves a Class to a Provider, and falls back when the first choice
// cannot run.
//
// The fallback is not a retry policy — a provider that answered badly is not
// retried anywhere else — it is availability only. `agy` not being installed,
// or its free quota being spent, must not take the whole distil path down with
// it, because every caller of Distill has a claude login already.
type Router struct {
	byClass  map[Class]Provider
	fallback map[Class]Provider
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

	return &Router{
		byClass: map[Class]Provider{
			Distill: pick(cfg.DistillProvider),
			Reason:  pick(cfg.ReasonProvider),
		},
		fallback: map[Class]Provider{
			Distill: pick(cfg.DistillFallback),
		},
	}
}

// Provider returns the primary provider for a class, for callers that need to
// name it (diagnostics, provenance columns).
func (r *Router) Provider(c Class) Provider {
	if r == nil {
		return nil
	}
	return r.byClass[c]
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
// unavailable. A cancelled or expired context is returned as-is and never
// triggers the fallback: the caller went away, and starting a second
// subprocess on its behalf would be work nobody is waiting for.
func (r *Router) Complete(ctx context.Context, c Class, req Request) (Response, error) {
	if r == nil {
		return Response{}, fmt.Errorf("%w: no router configured", ErrProviderUnavailable)
	}

	primary := r.byClass[c]
	if primary == nil {
		return Response{}, fmt.Errorf("%w: no provider for class %q", ErrProviderUnavailable, c)
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

	alt := r.fallback[c]
	if alt == nil || alt.Name() == primary.Name() {
		return Response{}, err
	}

	resp, altErr := alt.Complete(ctx, req)
	if altErr != nil {
		return Response{}, fmt.Errorf("%w (fallback %s also failed: %v)", err, alt.Name(), altErr)
	}
	return resp, nil
}
