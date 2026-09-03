# task-64 — Desktop: the saved businesses view

- **Status:** done
- **Owner agent:** desktop
- **Prerequisites:** task-63 (the routes)
- **Primary paths:** `desktop/src/screens/Leadgen.tsx`, `desktop/src/lib/leadgen.ts`, `desktop/src/lib/leadgen.test.ts`, `desktop/src/lib/daemon.ts`
- **Roadmap bucket:** B.4 extension

## Context

The lead-gen screen holds its report in `useState`, so opening it shows an empty
panel and the only way to see anything is to pay for a search. task-63 gives the
daemon a durable ledger; this task makes the screen read it, so the operator's
own accumulated businesses are what the screen opens on.

## Scope (do exactly this)

1. `daemon.ts`: `api.listLeads`, `api.leadCategories`, `api.leadRuns` beside the
   existing lead-gen calls, with their wire types.
2. `Leadgen.tsx`: two sources for one set of views — the last run (today's
   behaviour) and the ledger. With no run in state the screen loads the ledger
   rather than rendering the empty state, and a source selector switches between
   them. In ledger mode the run history is a filter.
3. Reuse `CategoryRail`, `CompanyTable`, `CompanyDetail`, `Th`, `Field`,
   `Check`, `Card` — no new component family, no new table style.
4. Counting and filtering live in `lib/leadgen.ts` with tests. Ledger category
   counts come from `/maps/leads/categories`; the client does not recount them.

## Out of scope (do NOT do here)

- Any Go change.
- A new export path. `ExportBar` still exports a run.
- Editing a lead.

## Definition of Done

- [x] Opening the screen with no search shows saved businesses and their
      category rail.
- [x] Marking a draft `sent`/`skipped` still works from the ledger row.
- [x] `make desktop-check` green
- [x] Status set to `done` with changelog

## Notes for the reviewer (Opus)

`desktop/AGENTS.md`: all daemon access through `daemon.ts`, decisions out of JSX
and into a tested module, and the daemon's own notes rendered as written.

## Changelog

- `desktop/src/lib/daemon.ts` — `SavedLead`, `LeadCategoryCount`, `LeadRun`,
  `LeadsQuery`; `api.listLeads`, `api.leadCategories`, `api.leadRuns`; a
  `leadsQuery` builder that omits unset fields so the daemon's page bounds stay
  the daemon's.
- `desktop/src/lib/leadgen.ts` — `CategoryBucket` now carries counts rather than
  companies, so one rail serves both sources; `railFromCounts`, `ledgerTotals`,
  `leadsQueryFrom`, `describeRun`, and a shared `sortBuckets` so a run's rail and
  the ledger's order identically.
- `desktop/src/screens/Leadgen.tsx` — a `Ledger` view in place of the empty
  state, reusing `CategoryRail`, `CompanyTable`, `CompanyDetail`, `Field`,
  `Check` and `Th`. `CompanyDetail` takes either `onOpenDrafts` (a run has a
  drafts view) or `onDecide` (the ledger does not), and the results header gains
  a way back to the ledger.
- Tests: 12 new cases across `leadgen.test.ts` and `daemon.test.ts`.

**Decision:** the ledger filters and counts on the server. Doing either in the
browser would have silently scoped both to the page in hand.

`make desktop-check` green.
