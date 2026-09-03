# task-55 — Region search without a Google credential

- **Status:** done
- **Owner agent:** daemon
- **Prerequisites:** task-28 (the Playwright sidecar), task-29 (the lead-gen pipeline)
- **Primary paths:** `internal/regionsearch/` (new), `internal/mapscrape/ensure.go` (new), `internal/leadgen/pipeline.go`, `internal/tools/{register.go,maps_search.go}`, `internal/api/{api.go,handlers.go}`, `cmd/mimir-daemon/main.go`, `internal/config/config.go`, `scripts/install-agent.sh`
- **Roadmap bucket:** B.4 extension

## Context

The operator asked for regional company discovery with **no Google API key**.
Every piece of that already existed — `internal/mapscrape` reads the public Maps
results feed through `deploy/playwright-maps` and returns the same
`[]maps.Company` the Places client does — but none of it was reachable:
`cmd/mimir-daemon` built the lead-gen pipeline only `if cfg.PlacesAPIKey != ""`,
so a machine without a key had no `/maps/*` routes, no `maps_search`, and no
lead-gen at all. The free provider existed and was never the answer to anything.

The order was also written in two places that disagreed — the pipeline's
`regionSearch` (Places, then scrape) and the tool's registration (Places or
nothing) — which is why "no key" meant two different things depending on where
you asked.

## Scope (do exactly this)

1. **`internal/regionsearch`** — one `Router` that asks the providers in order
   and reports which answered: `Search`, `Available`, `Sources`, `Free`. **The
   free source is first**, on every machine, so the keyless path is the tested
   path rather than a branch only a keyless machine reaches. A failed provider
   is a note; only "nobody answered" is an error; a cancelled caller is neither.
2. **`internal/mapscrape.EnsureRunning`** — the daemon starts the sidecar
   itself: health probe, `docker compose up -d`, wait for `/health`. `Search`
   calls it once, only after a refused connection, then retries once. A
   capability that needs no credential must not need a terminal either.
3. **`internal/config`** — `MapScrapeStartTimeout` and `MapScrapeComposeFile`,
   the latter derived: the copy beside the installed binary first, a repository
   checkout second (`MIMIR_MAPSCRAPE_COMPOSE` for tests).
   `scripts/install-agent.sh` copies `deploy/playwright-maps` next to the
   daemon, because a launchd-started process has no repository to find it in.
4. **`internal/leadgen`** — the pipeline holds one `RegionSource` seam instead
   of a searcher plus a scraper, and keeps only the cache around it. `Report`
   gains `source`.
5. **`internal/tools`** — `maps_search` takes the router, is registered on every
   machine, and its description names the source that will actually answer
   ("spends nothing" vs "billed"). The response carries `source` and the notes.
6. **`cmd/mimir-daemon`** — the scraper is always built, Places only with a key,
   and the pipeline is no longer conditional. `/diagnostics` reports
   `region_sources` and `region_search_free` beside `places_configured`.

## Out of scope (do NOT do here)

- Defeating a consent wall or a captcha. `ErrBlocked` stays what it is.
- A second scraping strategy for Maps. `internal/gmaps` (one business, via
  Crawl4AI) and this (a region feed, via Playwright) answer different questions
  and stay separate.
- Auto-starting Crawl4AI the same way. Its failure mode is different — it is a
  hard dependency of the research tools, not an on-demand one — and folding both
  into one mechanism is its own decision.
- Per-request source selection. The order is a policy, not a knob (SD-1).

## Definition of Done

- `make check` green.
- With no `MIMIR_GOOGLE_PLACES_API_KEY`: `maps_search` is registered, the
  `/maps/*` routes exist, and a region search returns companies with
  `source: "mapscrape"`.
- A search with the sidecar stopped starts it and still answers.
- With a key present, the scrape still answers first and Places is not called.

## Changelog

- `internal/regionsearch/{regionsearch.go,regionsearch_test.go,AGENTS.md}` — new.
- `internal/mapscrape/{ensure.go,ensure_test.go,client.go,AGENTS.md}` — on-demand
  start, one retry, and the message that says who starts the container.
- `internal/config/{config.go,AGENTS.md}` — two values, one derived path.
- `internal/leadgen/{pipeline.go,pipeline_test.go,AGENTS.md}` — one seam, plus
  `Report.Source`.
- `internal/tools/{register.go,maps_search.go,maps_search_test.go}` — registered
  without a credential; the description follows the source.
- `internal/api/{api.go,handlers.go,integration_test.go}`,
  `cmd/mimir-daemon/main.go`, `scripts/install-agent.sh`.
