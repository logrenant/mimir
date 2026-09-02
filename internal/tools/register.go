package tools

import (
	"fmt"

	"github.com/logrenant/mimir/internal/brain"
	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/crawl"
	"github.com/logrenant/mimir/internal/maps"
	"github.com/logrenant/mimir/internal/mcp"
	"github.com/logrenant/mimir/internal/memory"
	"github.com/logrenant/mimir/internal/pipeline"
	"github.com/logrenant/mimir/internal/refine"
	"github.com/logrenant/mimir/internal/search"
)

// Deps are the collaborators every tool is built from. They are constructed by
// whichever binary is starting up, because their lifetimes (and the store
// behind the pipeline) belong to that process.
type Deps struct {
	Search   *search.Client
	Crawl    *crawl.Client
	Refine   *refine.Client
	Pipeline *pipeline.Pipeline

	// Maps is optional. cmd/mimir-daemon builds one Places client and shares it
	// with the lead-gen pipeline, so it passes it here rather than have
	// RegisterAll build a second. cmd/mimir-mcp leaves it nil and RegisterAll
	// builds its own from the credential, exactly as before.
	Maps *maps.Client

	// Memory is optional and is nil whenever the local store could not be
	// opened, because a memory with nowhere to remember is not a degraded
	// memory, it is none. Projects is the lookup the memory tools use to pick
	// up an already-registered project id; nil is fine and simply means they
	// read interactive sessions only.
	Memory   *memory.Memory
	Projects ProjectFinder

	// Brain is optional for the same reason Memory is, and follows the same
	// store: a knowledge base with nowhere to keep nodes is not a degraded
	// knowledge base, it is none.
	Brain *brain.Core

	// BrainHashes is what lets a repository scan skip a file it has already
	// read. Satisfied by *store.Store; nil simply means every scan re-distils,
	// which is correct but expensive.
	BrainHashes brain.HashStore
}

// RegisterAll registers the canonical Mimir tool set on reg.
//
// It exists so there is exactly one list. cmd/mimir-mcp (stdio) and
// cmd/mimir-daemon (HTTP) are two transports over one engine; two
// hand-maintained registration lists is precisely how that claim would quietly
// stop being true — a tool added for the desktop app and never reaching Claude
// Code, or the reverse.
func RegisterAll(reg *mcp.Registry, cfg config.Config, d Deps) error {
	toolSet := []mcp.Tool{
		NewWebSearch(cfg, d.Search),
		NewFetchPage(cfg, d.Pipeline),
		NewResearch(d.Pipeline, cfg),
		NewDiagnostics(cfg, d.Crawl, d.Refine, d.Search, d.Memory),

		// Stage F — free, self-scraped providers. No refine call (see each
		// package's doc comment), so the pipeline is used only for FetchRaw.
		NewEcommerceLookup(cfg, d.Pipeline),
		NewTikTokProfileLookup(cfg, d.Pipeline),
		NewGMapsBusinessLookup(cfg, d.Pipeline),
		NewInstagramProfileLookup(cfg, d.Pipeline),
	}

	// M4 — the one tool with an operator-provisioned credential behind it, and
	// the one that costs money. A keyless install is the normal install, and a
	// tool that can only answer "no key" is worse than absent: it spends the
	// consumer's context in every session to advertise a dead end. So
	// availability, not behaviour, follows the credential.
	//
	// The client is built here rather than in tools.Deps because both binaries
	// would otherwise repeat the same construction — the exact divergence this
	// function exists to prevent — and a maps.Client has no lifetime to own.
	// M8 — the project memory. Availability follows the store for the same
	// reason maps_search's follows its credential: a tool whose only possible
	// answer is "there is no memory" would spend part of every session's
	// context advertising a dead end.
	if d.Memory != nil {
		toolSet = append(toolSet,
			NewProjectContext(cfg, d.Memory, d.Projects),
			NewContextRecall(cfg, d.Memory, d.Projects),
			NewContextRemember(cfg, d.Memory, d.Projects),
		)
	}

	// task-41 — the node core. Availability follows the store, exactly as the
	// memory tools' does, and for the same reason.
	if d.Brain != nil && d.Brain.Available() {
		toolSet = append(toolSet,
			NewBrainIngestData(cfg, d.Brain),
			NewBrainIngestGitHub(cfg, d.Brain),
			NewBrainQueryNodes(cfg, d.Brain),
			NewBrainRelated(cfg, d.Brain),
			NewBrainScanRepo(cfg, d.Brain, d.BrainHashes),
		)
	}

	if mapsClient := d.Maps; mapsClient != nil {
		toolSet = append(toolSet, NewMapsSearch(cfg, mapsClient))
	} else if cfg.PlacesAPIKey != "" {
		mapsClient, err := maps.New(cfg, maps.Options{APIKey: cfg.PlacesAPIKey})
		if err != nil {
			return fmt.Errorf("building the places client: %w", err)
		}
		toolSet = append(toolSet, NewMapsSearch(cfg, mapsClient))
	}

	for _, t := range toolSet {
		if err := reg.Register(t); err != nil {
			return fmt.Errorf("registering tool %s: %w", t.Name(), err)
		}
	}
	return nil
}
