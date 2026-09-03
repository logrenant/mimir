# task-57 — A model fallback for the feed, contact enrichment, and the workbook

- **Status:** done
- **Owner agent:** daemon
- **Prerequisites:** task-55 (the region-search router and the sidecar)
- **Primary paths:** `internal/refine/places.go` (new), `internal/mapsllm/` (new), `internal/contacts/` (new), `internal/mapscrape/model.go` (new), `internal/leadgen/export.go` (new), `internal/regionsearch/regionsearch.go`, `internal/api/maps.go`, `cmd/mimir-daemon/main.go`, `internal/config/config.go`
- **Roadmap bucket:** B.4 extension

## Context

Three things the operator asked for, all of them about the keyless path being
usable rather than merely present:

1. **A model fallback for the scrape.** The selectors in `internal/mapscrape`
   are anchored on markup Google changes without notice, and the package doc
   has always said so. When they read nothing, the rendered page is still in
   hand.
2. **Contact details.** A scraped result has no phone number and never an email
   address. The company's own website usually has both.
3. **A workbook.** One sheet per category, with every way to reach a company and
   whether it has a website at all — the file is the deliverable, not the JSON.

## Scope (do exactly this)

1. **`internal/refine`** — two profiles, both on the Reason class (claude
   haiku): `ExtractFeed` (a rendered feed → the businesses in it) and
   `ExtractContacts` (a company's site → phone/email/address). Reason, not
   distil, because both are reached only when something cheaper already failed
   and a fallback must not depend on the tier most likely to be down.
2. **`internal/mapscrape`** — a `FeedExtractor` seam and `UseExtractor`. Only a
   feed that parsed *nothing* reaches it; every recovered row carries
   `PlaceIDPrefix`, and `TrimFeedHTML` drops the script and style a feed is
   mostly made of before anything is paid for.
3. **`internal/mapsllm`** — implements that seam, and is a region-search
   provider in its own right: the same URL through Crawl4AI, read the same way,
   for the machine where the sidecar cannot run. Its honest expectation is that
   it often finds nothing, and it says so where an operator can read it.
4. **`internal/regionsearch`** — the router takes an ordered provider list, and
   `Standard` builds the shipped order in one place: sidecar → model → Places.
5. **`internal/contacts`** — patterns first (`tel:`/`mailto:`/footer), the model
   only where they found nothing, and the model's answer validated by the same
   patterns. One page per company; nothing inferred; every row records which
   tier answered.
6. **`internal/leadgen.Export`** — the workbook: a summary sheet, then one per
   category, with contact columns, "web sitesi var mı", provenance and the
   enrichment method. `POST /maps/leadgen/export`, written into `cfg.ExportDir`.

## Out of scope (do NOT do here)

- Asking a model for companies it was not shown. That is fabrication, and no
  prompt or provider in this task produces a business that was not on a page.
- A crawl of each company's site. One page, or nothing.
- Enrichment during a run. It belongs to the export, which is what needs it.
- A new store table for enriched contacts. The export is a view; caching it
  would make a stale phone number outlive the page it came from.

## Definition of Done

- `make check` and `make desktop-check` green.
- A feed with unfamiliar markup still produces companies when an extractor is
  wired, and a feed that parsed normally never reaches the model.
- An export of a keyless run writes one sheet per category, and a company with
  no website has empty contact cells and `no_website` in the method column.

## Changelog

- `internal/refine/{places.go,AGENTS.md}` — two profiles, Reason class.
- `internal/mapscrape/{model.go,model_test.go,client.go,AGENTS.md}`.
- `internal/mapsllm/` — the adapter and the third provider.
- `internal/contacts/` — the two-tier enricher.
- `internal/leadgen/{export.go,export_test.go,pipeline.go,AGENTS.md}`.
- `internal/regionsearch/regionsearch.go` — provider list + `Standard`.
- `internal/api/{api.go,maps.go,maps_test.go}`, `cmd/mimir-daemon/main.go`,
  `internal/config/config.go`, `go.mod` (excelize, pinned).
