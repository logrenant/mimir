# task-56 — Desktop: which source paid for these companies

- **Status:** done
- **Owner agent:** desktop
- **Prerequisites:** task-55 (`report.source`, the diagnostics fields)
- **Primary paths:** `desktop/src/lib/daemon.ts`, `desktop/src/screens/Leadgen.tsx`, `desktop/src/components/ui/badge.tsx`
- **Roadmap bucket:** B.4 extension

## Context

task-55 makes region search work with no Google credential, and gives a report
the provider that answered it. The two providers do not return the same columns
— a scrape has no phone and no Google category — so a results table that does
not say which one it came from looks like missing data rather than a different
source.

## Scope (do exactly this)

1. **`lib/daemon.ts`** — `source` on `LeadgenReport`; `places_configured`,
   `region_sources` and `region_search_free` on the diagnostics daemon block.
2. **`screens/Leadgen.tsx`** — a badge beside "from cache": `scrape · ücretsiz`
   or `places · faturalı`, each with the sentence behind it as a tooltip.
3. **`components/ui/badge.tsx`** — an optional `title`, since a badge is two
   words and the reason is a sentence.

## Out of scope (do NOT do here)

- A source picker. The order is the daemon's policy, not a per-run choice.
- A "start the sidecar" button. The daemon starts it on demand; a button would
  be a second way to do what already happens.

## Definition of Done

- `make desktop-check` green.
- A keyless run shows the free badge, and the table's empty phone column is
  explained rather than mysterious.

## Changelog

- `desktop/src/lib/daemon.ts` — three diagnostics fields, one report field.
- `desktop/src/screens/Leadgen.tsx` — the provenance badge.
- `desktop/src/components/ui/badge.tsx` — optional `title`.
