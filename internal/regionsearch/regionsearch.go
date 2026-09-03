// Package regionsearch decides which provider answers "which companies are in
// this region", and in which order.
//
// It exists because that order is a policy, not a detail, and it was written
// twice: once in internal/leadgen's pipeline and once, implicitly, in the
// registration of the maps_search tool. The policy is one sentence — **the free
// source is tried first** — and this is the only place it is written down.
//
//   - The scrape provider (internal/mapscrape, the Playwright sidecar) needs no
//     credential and costs nothing, so it is primary. That also means the
//     key-free path is the path every machine exercises, rather than an
//     untested branch that only a machine without a key ever reaches.
//   - The model-assisted provider (internal/mapsllm) is next: the same page,
//     fetched through Crawl4AI and read by a model, for the machine where the
//     sidecar cannot run. It spends model tokens, not Google money.
//   - The Places provider (internal/maps) is last: richer fields — phone,
//     Google's types[], a real address — but it bills per request, so it
//     answers when nothing free could.
//
// Any provider may be absent, and none at all is a defined state: the daemon
// still runs and says region search is unavailable, exactly as it used to when
// there was no Places key.
package regionsearch

import (
	"context"
	"errors"
	"fmt"

	"github.com/logrenant/mimir/internal/maps"
)

// ErrNoSource means neither provider is configured, so there is nothing to ask.
var ErrNoSource = errors.New("regionsearch: no region-search provider is configured")

// ErrNoData means every configured provider was asked and none produced a
// company list.
var ErrNoData = errors.New("regionsearch: no provider produced a result")

// Scraper is a provider that takes a query and answers with companies:
// internal/mapscrape's client shape, and internal/mapsllm's.
type Scraper interface {
	Search(ctx context.Context, q maps.Query) ([]maps.Company, error)
}

// Places is the billed provider: internal/maps' client shape. The method name
// differs because the two packages were written against different vocabularies
// and renaming either would be churn for its own sake.
type Places interface {
	SearchText(ctx context.Context, q maps.Query) ([]maps.Company, error)
}

// Provider is one source in the order.
//
// Name is what a note, a report and a UI badge say; Free is whether asking it
// spends Google money. A provider that is absent is simply not in the list —
// there is no nil to guard, which is what keeps a nil pointer inside a non-nil
// interface from ever looking like a working source.
type Provider struct {
	Name   string
	Free   bool
	Search func(ctx context.Context, q maps.Query) ([]maps.Company, error)
}

// FromScraper builds a provider from a Search-shaped client.
func FromScraper(name string, free bool, s Scraper) Provider {
	return Provider{Name: name, Free: free, Search: s.Search}
}

// FromPlaces builds the billed provider from internal/maps' client.
func FromPlaces(p Places) Provider {
	return Provider{Name: maps.SourcePlacesAPI, Free: false, Search: p.SearchText}
}

// Router asks the providers in the order they were given.
type Router struct {
	providers []Provider
}

// New takes the providers in priority order — free first, by convention this
// package documents and its callers follow. Entries with no Search function are
// dropped, so a caller can build a list without branching.
func New(providers ...Provider) *Router {
	kept := make([]Provider, 0, len(providers))
	for _, p := range providers {
		if p.Search == nil {
			continue
		}
		kept = append(kept, p)
	}
	return &Router{providers: kept}
}

// Sources are the providers a binary has, before they are put in order.
//
// A field left nil is a provider that machine does not have. Assign an
// interface field only when the concrete value exists: a nil pointer inside a
// non-nil interface is indistinguishable from a working provider until it is
// called.
type Sources struct {
	// Sidecar is the Playwright scrape: free, no credential, primary.
	Sidecar Scraper
	// Model is the Crawl4AI-plus-model reading of the same page: free of
	// Google money, spends model tokens, and only asked when the sidecar could
	// not answer.
	Model Scraper
	// Places is the Google Places API: billed, and last for that reason.
	Places Places
}

// Standard builds the shipped order — free first, billed last.
//
// It exists so the order is constructed in one place as well as decided in one
// place: the daemon and the MCP tool registry both call this, and a second
// hand-assembled New(...) elsewhere would be the drift this package was created
// to stop.
func Standard(s Sources) *Router {
	var providers []Provider
	if s.Sidecar != nil {
		providers = append(providers, FromScraper(maps.SourceScrape, true, s.Sidecar))
	}
	if s.Model != nil {
		providers = append(providers, FromScraper(maps.SourceModel, true, s.Model))
	}
	if s.Places != nil {
		providers = append(providers, FromPlaces(s.Places))
	}
	return New(providers...)
}

// Available reports whether any provider can be asked at all.
func (r *Router) Available() bool {
	return r != nil && len(r.providers) > 0
}

// Sources names the providers in the order they will be tried. Used by the
// diagnostics surface and by the maps_search tool's description, so what a
// client is told matches what will actually happen.
func (r *Router) Sources() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, p.Name)
	}
	return out
}

// Free reports whether the first source spends nothing. It is what a
// description or a UI badge should say — "this costs money" is a claim that has
// to follow the actual order, not the build.
func (r *Router) Free() bool {
	return r != nil && len(r.providers) > 0 && r.providers[0].Free
}

// Search asks each configured provider in order and returns the first answer,
// along with a note for every provider that could not give one.
//
// A provider that fails is a note, not an error: the whole point of two sources
// is that one of them being down is survivable. Only "nobody answered" is an
// error, and a cancelled caller is never reported as a provider failure.
func (r *Router) Search(ctx context.Context, q maps.Query) ([]maps.Company, string, []string, error) {
	if !r.Available() {
		return nil, "", nil, ErrNoSource
	}

	var notes []string
	for _, p := range r.providers {
		cs, err := p.Search(ctx, q)
		if err == nil {
			if !p.Free {
				// Worth saying out loud: this answer was billed, and every
				// free source ahead of it had its turn first.
				notes = append(notes, fmt.Sprintf("answered by %s (billed)", p.Name))
			}
			return cs, p.Name, notes, nil
		}
		// A caller that went away is not a provider failure, and must not send
		// the next — possibly billed — source any work nobody is waiting for.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, "", notes, ctxErr
		}
		notes = append(notes, fmt.Sprintf("%s failed: %v", p.Name, err))
	}

	return nil, "", notes, fmt.Errorf("%w: %d source(s) tried", ErrNoData, len(r.providers))
}
