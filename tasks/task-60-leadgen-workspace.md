# task-60 — Desktop: the lead-gen workspace

- **Status:** done
- **Owner agent:** desktop
- **Prerequisites:** task-57 (the export), task-59 (categories that exist)
- **Primary paths:** `desktop/src/screens/Leadgen.tsx`, `desktop/src/lib/leadgen.ts` (new), `desktop/src/lib/leadgen.test.ts` (new)
- **Roadmap bucket:** B.4 extension

## Context

The screen was one flat scroll: gap-analysis blocks, then every company in one
list, then the run's notes. Categories were printed on each row but the list
was never *organised* by them, so "show me the companies you found, by
category, before I export" could not be answered without opening the workbook.
Drafted emails were a `<details>` per row containing a `<pre>` — a letter set
as a log line, reviewed by scrolling a list looking for the next one.

## Scope (do exactly this)

1. **`lib/leadgen.ts`** — the counting, pure and tested: `bucketByCategory`
   (largest first, `unknown` always last), `filterCompanies` (Turkish-folded
   text match, "no website only"), `sortCompanies` (stable ties), `totals`
   (including `unclassified`), `classifyNote`, `draftQueue` (undecided first),
   `contactLines`.
2. **Companies view** — a category rail that is the filter and shows where the
   mass is, a dense sortable table (website presence gets the one warm colour,
   because "who has no site" is what the operator is here for), a detail panel
   for the row in hand, and the selected category's gap analysis beneath.
3. **Drafts view** — a queue with per-draft state, one letter at a time at a
   reading measure, marked with buttons or `g`/`a`, `↑↓` to move. Marking
   advances, because the queue is a decision list.
4. **An honest failure state** — when nothing was classified, the header says so
   and prints the run's own reason rather than showing one `unknown` bucket as
   though it were a result.
5. Copy is Turkish throughout, matching Brain and Dashboard.

## Out of scope (do NOT do here)

- A second top-level screen for drafts. A draft is only meaningful next to the
  company it is for, and switching screens would drop the search.
- New colours or components. The rail, table and queue are built from the four
  brand colours and the existing Card/Badge/Button primitives.
- Editing a draft in place. The daemon owns the text; this reviews it.

## Definition of Done

- `make desktop-check` green.
- The rail, the table and the drafts queue were reviewed on screen at 1440×900
  before shipping — not inferred from the code.
