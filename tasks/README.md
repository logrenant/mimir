# tasks/ — task index (archived)

> **Every planned task is shipped and every `task-NN` file has been retired.**
> This directory now holds only this record. For what the built system does
> today, see [`../docs/CAPABILITIES.md`](../docs/CAPABILITIES.md)
> (Türkçe: [`../docs/CAPABILITIES.tr.md`](../docs/CAPABILITIES.tr.md)).
>
> **New work starts with a new `task-NN` file** proposed to the user, written to
> the schema in [`../docs/AGENT_RULES.md`](../docs/AGENT_RULES.md) §4 and driven
> by the `do task-NN` protocol in [`../AGENTS.md`](../AGENTS.md) §4. Until then,
> the rules below describe how this directory worked.

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
