# tasks/ — task index

> **task-01 … task-34 are shipped and their files have been retired.** For what
> the built system does today, see
> [`../docs/CAPABILITIES.md`](../docs/CAPABILITIES.md)
> (Türkçe: [`../docs/CAPABILITIES.tr.md`](../docs/CAPABILITIES.tr.md)).
>
> **New work starts with a new `task-NN` file** proposed to the user, written to
> the schema in [`../docs/AGENT_RULES.md`](../docs/AGENT_RULES.md) §4 and driven
> by the `do task-NN` protocol in [`../AGENTS.md`](../AGENTS.md) §4.

## Live tasks

| Task | Status | What |
|---|---|---|
| [task-35](task-35-coding-task-lifecycle.md) | done | Coding-task lifecycle in the daemon: backlog + durable queue, stop, restart reconciliation, image attachments, live stderr. |
| [task-36](task-36-desktop-board-terminals.md) | done | Desktop: five-column board with create/run/stop, per-run terminals, image composer, and the run-stream failures that were invisible. |
| [task-37](task-37-coding-accounts.md) | done | Claude Code credential slots as first-class accounts, and capacity measured in accounts rather than a number: one run per identity. |
| [task-38](task-38-desktop-accounts-recents.md) | done | Desktop: account manager with a live auth probe, account picker on both composers, and a collapsed Recents list of past terminals. |
| [task-39](task-39-coding-model-choice.md) | done | Per-task model choice: an allow-list in `internal/config`, published at `GET /coding-models`, carried on the row and into `--model`. |
| [task-40](task-40-desktop-dashboard.md) | done | Desktop: the home screen becomes a dashboard (live consoles, queue, dependency health, account load), one shared run poll, and the model picker. |
| [task-41](task-41-brain-core.md) | done | Brain v2: the node core rebuilt on the store (`0013_brain.sql`), and `internal/llm` — one exit point for model calls, routed by class of work. |
| [task-43](task-43-mcp-enforcement.md) | done | `make install-mcp`: registration on every MCP client on the machine, plus a session preflight hook that checks the binary and brings the daemon back. |
| [task-45](task-45-autonomous-capture.md) | done | Brain records itself: Claude Code episodes and their files promoted to nodes, agy sessions drained from a hook spool, commits captured — all without a model call. |
| [task-47](task-47-repo-scan.md) | done | One pass over a whole repository through agy, hash-skipped so a re-scan is free and an interrupted scan resumes. |
| [task-49](task-49-machine-scan.md) | done | `bin/mimir-scan`: every project under a root read into Brain — discovery, untracked files included, undistilled files retried. |
| [task-51](task-51-resident-scan.md) | done | The distil tier is agy-only; the daemon keeps one scan running over ~/development and ~/Documents, reads PDFs, and serves the graph. |
| [task-52](task-52-desktop-brain.md) | done | Desktop: the Brain tab — scan control and a force-directed picture of the knowledge base. |
| [task-53](task-53-account-discovery.md) | done | `~/.claude-accounts` is the authority for credential slots: scanned at startup, discovered slots cannot be forgotten, and the daemon's own model calls spend a chosen account. |
| [task-54](task-54-desktop-account-slots.md) | done | Desktop: discovered slots are marked rather than forgettable, refresh rescans the directory, and the background account is a row control. |
| [task-55](task-55-keyless-region-search.md) | done | Region search with no Google credential: one router that asks the free scrape first, a sidecar the daemon starts itself, and lead-gen no longer gated on a Places key. |
| [task-56](task-56-desktop-region-source.md) | done | Desktop: the results header says which provider answered — free scrape or billed Places — so a missing phone column reads as a source difference. |
| [task-57](task-57-contacts-and-export.md) | done | A model fallback for a feed the selectors cannot read, a third region-search provider, website-based contact enrichment, and the per-category Excel workbook. |
| [task-58](task-58-desktop-export.md) | done | Desktop: "Excel'e aktar" with an enrichment toggle, and a reveal command scoped to the exports directory. |
| [task-59](task-59-classify-fallback.md) | done | Classification, and only classification, falls back to claude haiku when the distil tier is signed out — otherwise every scraped company is `unknown`. |
| [task-60](task-60-leadgen-workspace.md) | done | Desktop: the lead-gen workspace — a category rail, a sortable company table with a detail panel, and a draft queue reviewed one letter at a time. |
| [task-61](task-61-gemini-38.md) | done | The distil tier moves to `gemini-3.8-flash-high` — newer, and the tag the account actually serves. |
| [task-63](task-63-lead-ledger.md) | done | The lead ledger: a run outlives its response — one row per business, a run history beside it, and three reads that cost nothing. |
| [task-64](task-64-desktop-leads.md) | done | Desktop: the lead-gen screen opens on saved businesses instead of an empty panel. |
| [task-65](task-65-chat-archive.md) | done | The chat archive: Claude Code, Terminals and agy conversations stored verbatim, searchable, model-free. |
| [task-67](task-67-file-versions.md) | done | File version history: what a scanned file used to mean, and a scan that says out loud when it re-read one. |
| [task-68](task-68-desktop-versions.md) | done | Desktop: the node version timeline. |
| [task-69](task-69-scan-permissions.md) | done | Scan permissions: the operator owns the roots and the exclusion list; credential files are never read, whatever the policy says. |
| [task-70](task-70-outreach-and-settings.md) | done | Outreach for the companies the operator ticked, on two channels, under rule files they wrote — and a settings screen that owns the model choice the search bar used to. |

They are split by file, not by package: the odd-numbered tasks own
`internal/**` and `cmd/**`, the even-numbered ones own `desktop/**`, so a
daemon task and a desktop task can never conflict.

Each task was one file. The Coder Agent (Gemini) implemented **exactly one per
run**, triggered by `do task-NN`; `make check` had to be green and `Status: done`
set before a task was finished. Every task belonged to one **track** in
[`../docs/ROADMAP.md`](../docs/ROADMAP.md):

- **Track A** — the research MCP capability. Shipped: task-01 … task-16, plus
  Stage F (the free scrapers), which shipped without task files.
- **Track B** — Mimir desktop & agent orchestration, milestones M1 … M7, all
  shipped (task-17 … task-34).

---

## Stages

Stage letters were historical build phases of Track A; Track B tasks were grouped
by roadmap milestone instead.

| Group | Tasks | Roadmap |
|---|---|---|
| **A — Skeleton & MCP handshake** | task-01 … task-04 | A.1 |
| **B — Data collection** | task-05 … task-07 | A.1 |
| **C — Refine pipeline (context isolation)** | task-08 … task-13 | A.1 |
| **D — Hardening & delivery** | task-14 … task-16 | A.1 |
| **M1 — persistence** | task-17, task-18 | B / M1 |
| **M2 — runtime & daemon** | task-19 … task-22 | B / M2 |
| **M3 — live run streaming + desktop shell** | task-25, task-26, task-27 | B / M3 |
| **M4 — Maps data layer** | task-23, task-24 | B / M4 |
| **M5 — scrape fallback + categorization** | task-28, task-29 | B / M5 |
| **M6 — lead-gen synthesis + desktop leadgen** | task-30, task-31, task-33, task-34 | B / M6 |
| **M7 — hardening & packaging** | task-32 | B / M7 |

## Index & dependency order

| Task | Title | Prerequisites |
|------|-------|---------------|
| task-01 | Repo bootstrap & build system | none |
| task-02 | Standardized `config` package (SD-1) | task-01 |
| task-03 | MCP server skeleton — stdio, `initialize`, `tools/list` | task-01, task-02 |
| task-04 | Internal tool framework — registry, schema, error mapping, slog (SD-4) | task-03 |
| task-05 | DuckDuckGo free search client | task-02 |
| task-06 | `web_search` MCP tool | task-04, task-05 |
| task-07 | Crawl4AI HTTP client & docker | task-02 |
| task-08 | Refine client — pinned model, deterministic prompt (SD-5, SD-8) | task-02 |
| task-09 | Refine hardening — injection firewall, output ceiling, boilerplate strip (SD-2, SD-7) | task-08 |
| task-10 | Pipeline orchestrator — bounded concurrency, deadlines, partial results (SD-3) | task-05, task-07, task-09 |
| task-11 | `fetch_page` MCP tool + context-isolation guard test (SD-2) | task-04, task-10 |
| task-12 | `research` MCP tool — flagship brief (SD-7) | task-11 |
| task-13 | Context-isolation enforcement middleware — single choke-point (SD-2, SD-7) | task-06, task-11, task-12 |
| task-14 | Concurrency & resource guardrails — global semaphore, politeness, `-race`, goleak (SD-3) | task-13 |
| task-15 | Structured logging + `diagnostics` MCP tool (SD-4, SD-6) | task-04, task-07, task-08 |
| task-16 | E2E smoke tests, `make check`/`make e2e`, packaging & Claude Code registration (SD-8) | task-13, task-14, task-15 |
| task-17 | `internal/store` SQLite foundation + page cache tables | task-02, task-16 |
| task-18 | Pipeline cache wiring — skip crawl + refine on a hit | task-17 |
| task-19 | `internal/events` typed run events + in-process bus | task-18 |
| task-20 | `internal/project` folder registry with hard path scoping | task-17 |
| task-21 | `internal/coderunner` streaming, folder-scoped `claude` sessions | task-19, task-20 |
| task-22 | `cmd/mimir-daemon` + `internal/api` loopback HTTP surface | task-17, task-20, task-21 |
| task-23 | `internal/maps` Places API client + `store.companies` | task-17 |
| task-24 | `maps_search` MCP tool + operator-provisioned Places credential | task-22, task-23 |
| task-25 | `GET /ws/runs/{id}` live run fan-out over WebSocket | task-19, task-21, task-22 |
| task-26 | `desktop/` Tauri shell: sidecar lifecycle + daemon handshake | task-22, task-25 |
| task-27 | `desktop/` product screens: picker, coding task, live run view | task-25, task-26 |
| task-28 | `internal/mapscrape` + `deploy/playwright-maps/` Places fallback | task-23, task-24 |
| task-29 | `internal/leadgen` rule-table categorization + batched model fallback | task-23, task-24 |
| task-30 | `internal/leadgen` per-category gap analysis (stage 3) | task-29 |
| task-33 | `internal/leadgen` outreach email drafting (stage 4) | task-30 |
| task-34 | `internal/leadgen` full-pipeline orchestrator + `/maps/*` routes | task-33 |
| task-31 | `desktop/` Maps lead-gen screen | task-27, task-34 |
| task-32 | M7: hardening, docs sync, diagnostics, packaging | task-31 |

## Status board — final

All 34 tasks reached `done`. Track A: task-01 … task-16 (MVP) + Stage F (no task
files). Track B: task-17 … task-34 (M1–M7). Landing order deviated from the
numbering in two places, both recorded below.

## History notes

- **M8 (project memory) shipped without a task file**, at the user's explicit
  direction, and is recorded here so the chain of record stays honest. It added
  `internal/sessionlog`, `internal/memory`, migration `0008_memory.sql`, a fifth
  `internal/refine` prompt profile (`Recap`), and three MCP tools
  (`project_context`, `context_recall`, `context_remember`). Scope and design:
  [`../docs/ROADMAP.md`](../docs/ROADMAP.md) §B.9 and
  [`../docs/ARCHITECTURE.md`](../docs/ARCHITECTURE.md) §7.7. `make check` green.
  It touched the usual hot files — `internal/config/config.go`,
  `internal/tools/register.go`, both `cmd/` mains — which is exactly why the
  parallelism rule below would have put all of them in one task.

- **The first Brain layer (commit `013f7d7`) also shipped without a task file**,
  and unlike M8 it was not recorded here at the time. It did not work: all three
  of its tools failed the SD-2 choke-point on every call, and the suite stayed
  green because the canonical-list test asserts names without ever invoking one.
  `task-41` replaces it and is the file that should have existed first. Recorded
  so the chain of record stays honest.

- **M3 was split in two** because the shell and the screens are different risks:
  the handshake is process plumbing with a security boundary (port, token, child
  lifecycle), the screens are UI over a transport that by then already works. The
  split paid for itself — running the shell alone surfaced the CORS preflight
  that would otherwise have looked like a broken screen.
- **task-28 and task-29 landed out of numerical order** because both are Go-only
  and touch no file the desktop tasks hold, so they proceeded while task-26
  waited on a Rust toolchain.
- **task-30 / task-33 / task-34 / task-31** completed M6 (gap analysis, email
  drafting, the stages-1–4 orchestrator + `/maps/*` routes, the desktop lead-gen
  screen); **task-32** completed M7 (docs sync, `/diagnostics` cost/health, the
  Places integration test, Tauri signing/notarization scaffolding).

**Deferred, recorded, not scheduled:** unifying `internal/leadgen`'s per-call
refine fan-out limit with `internal/pipeline`'s process-wide semaphore (a change
to `internal/pipeline`, its own task); the parked Track A tools in
[`../docs/ROADMAP.md`](../docs/ROADMAP.md) §A.3–§A.4; `linkedin_company_lookup`
(Stage F, §A.2).

## Writing a task so two agents can work at once

Still the rule for any future task. A task is safe to run in parallel with
another only when their **Primary paths** do not intersect — the file, not the
package, is the unit of conflict.

1. **Put every shared file in one task, not both.** `internal/config/config.go`,
   `internal/tools/register.go`, `internal/mcp/*`, the `Makefile`, and this file
   are the hot ones — almost every feature wants a line in them. Group those
   edits into the *wiring* task and keep the *data/logic* task free of them.
2. **A new package with a constructor argument conflicts with nothing.**
   task-23 took the Places key as a parameter instead of reading config, which is
   why it could land while task-22 held `config.go` open. The credential wiring
   was a later, smaller task — not a compromise on SD-1, since the value still
   never becomes a runtime knob.
