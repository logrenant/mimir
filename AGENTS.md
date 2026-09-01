# AGENTS.md — Mimir

This repository builds **two macOS binaries over one runtime engine**
([`docs/ROADMAP.md`](docs/ROADMAP.md)):

- **`bin/mimir-mcp`** (Track A) — a local, zero-cost **MCP server** that gives an
  end-user Claude Code session the ability to search the web, scrape pages, and
  receive **compact, refined** results — never raw dumps. The MVP is website
  scraping; Stage F adds free, self-written, no-login scrapers for
  e-commerce/TikTok/Google Maps/Instagram (`internal/{ecommerce,tiktok,gmaps,
  instagram}` + `internal/extract`). Paid providers (LinkedIn, Meta Ads, Google
  Ads) remain **out of scope** — §A.3.
- **`bin/mimir-daemon`** (Track B) — a long-running, loopback-only HTTP service
  the Tauri desktop app (`desktop/`) talks to. It owns the folder-scoped
  coding-task runner with live streaming, and the Google Maps lead-gen pipeline,
  and re-exposes the same MCP registry at `/mcp`. New packages:
  `internal/{store,project,coderunner,events,api,maps,mapscrape,leadgen}`.
  **M1–M7 all shipped.**
- **M8 — project memory** (`internal/{sessionlog,memory}`) spans both binaries:
  it distils Claude Code's own session transcripts and this daemon's coding runs
  into per-project context, served back through three MCP tools so a session is
  told what the repository already established instead of rediscovering it.

**Current state:** every planned task (task-01 … task-34) is `done` and the
`task-NN` files have been retired; M8 (project memory) shipped afterwards
without a task file, at the user's direction — see [`tasks/README.md`](tasks/README.md). [`tasks/README.md`](tasks/README.md) keeps the
record; [`docs/CAPABILITIES.md`](docs/CAPABILITIES.md) — Türkçe
[`docs/CAPABILITIES.tr.md`](docs/CAPABILITIES.tr.md) — is the full inventory of
what the built system does today. New work starts with a new task file (§4).

---

## 1. The four roles — do not confuse them

| Role | Who | When | Mandate |
|------|-----|------|---------|
| **Coder Agent** | Gemini | build time | Implements **exactly one `tasks/task-NN` file at a time**. May not invent scope. Must satisfy that task's Definition of Done and `make check`. |
| **Reviewer Agent** | Claude Opus (via Claude Code) | build time | Reviews a finished task against its DoD + the Strict Directives. May refactor. Produces a verdict. |
| **Runtime Consumer** | Claude Code (the end user's session) | run time | Not part of the build. Only calls the finished MCP tools. It is the **audience** for every tool response — keep responses compact. |
| **Refiner Model** | local `claude` CLI, model `claude-haiku-4-5-20251001` | run time | A **headless subprocess dependency**, not a developer agent. Called by `internal/refine` via `exec` to distil scraped text — no API key, rides the operator's existing Claude Code login. Never call it "the agent". |

Full contract: [`docs/AGENT_RULES.md`](docs/AGENT_RULES.md).

---

## 2. Repo map

Directories marked *(task-01)* are created by `tasks/task-01`; the `AGENTS.md`
files inside them already exist and must not be deleted.

```
AGENTS.md                     ← you are here (root rules + task protocol)
docs/
  AGENT_RULES.md              ← detailed rules, strict directives, workflows
  ARCHITECTURE.md             ← pipeline + package design reference
  ROADMAP.md                  ← the one roadmap: Track A (research MCP) + Track B (Mimir product)
  CAPABILITIES.md             ← what the built system does today (EN)
  CAPABILITIES.tr.md          ← same, Türkçe
  INSTALL.md · SECURITY.md    ← setup + the security model
tasks/
  README.md                  ← archived task index + status board (all task files retired)
cmd/mimir-mcp/        (task-01) ← Track A entrypoint: stdio MCP, wiring + lifecycle only
  AGENTS.md
cmd/mimir-daemon/     (task-22) ← Track B entrypoint: loopback HTTP, drains runs on SIGTERM
  AGENTS.md
internal/
  config/           (task-01) ← standardized constants; two narrow non-constant categories (AGENTS.md)
    AGENTS.md
  mcp/              (task-01) ← MCP protocol layer (SDK, stdio + StreamableHTTP, schema, choke-point)
    AGENTS.md
  search/           (task-01) ← DuckDuckGo free search client
  crawl/            (task-01) ← Crawl4AI Docker HTTP client
  refine/           (task-01) ← claude CLI refine client — the context-isolation firewall (4 prompt profiles)
    AGENTS.md
  pipeline/         (task-01) ← Track A orchestration: search → crawl → refine → merge
    AGENTS.md
  tools/            (task-01) ← MCP tool handlers (web_search, fetch_page, research, diagnostics, Stage F, maps_search, M8 memory tools)
  extract/          (Stage F) ← network-free HTML/JSON structured-data helpers
  ecommerce/ tiktok/ gmaps/ instagram/  (Stage F) ← free, no-login scrapers
  store/            (task-17) ← SQLite cache + local records (no CGO), append-only migrations
    AGENTS.md
  project/          (task-20) ← folder registry + hard path scoping (the security boundary)
  events/           (task-19) ← in-process run-event pub/sub bus
  coderunner/       (task-21) ← folder-scoped streaming `claude` coding sessions
    AGENTS.md
  api/              (task-22) ← the daemon's HTTP surface (REST + /ws + /mcp + /maps/*)
    AGENTS.md
  maps/             (task-23) ← Google Places API (New) client — operator-provisioned key
    AGENTS.md
  mapscrape/        (task-28) ← Playwright-sidecar client, the Places fallback
  leadgen/          (task-29) ← Maps lead-gen: categorize → gap analysis → email + the Pipeline orchestrator
    AGENTS.md
  sessionlog/       (M8)      ← inert parser: Claude Code / coding-run JSONL transcripts → Episodes
    AGENTS.md
  memory/           (M8)      ← long-lived per-project memory: bounded ingest, per-episode recap, read-time brief
    AGENTS.md
deploy/crawl4ai/    (task-07) ← pinned docker-compose for the local scraper
deploy/playwright-maps/ (task-28) ← pinned Playwright sidecar for the Maps fallback
desktop/            (task-26) ← Tauri + React + shadcn/ui shell; screens: Connection, Workspace, Leadgen
  AGENTS.md
```

---

## 3. Build & test commands (defined by task-01, stable thereafter)

| Command | Meaning |
|---------|---------|
| `make build`   | compile `bin/mimir-mcp` and `bin/mimir-daemon` |
| `make test`    | unit tests, external services mocked |
| `make race`    | `go test -race ./...` |
| `make check`   | `build` + `go vet` + lint + `test` + `race` — **must be green to finish any task** (Go only) |
| `make crawl-up` / `make crawl-down` | start/stop the local Crawl4AI container |
| `make maps-up` / `make maps-down` | start/stop the Playwright Maps sidecar (slow first build) |
| `make e2e`     | end-to-end MCP smoke test against mock external services |
| `make desktop-check` | `desktop/` gate: `npm run typecheck` + `npm test`, then `cargo fmt`/`clippy`/`test` |

Integration tests that need real Docker / the `claude` CLI / the Places API live behind the `//go:build integration` tag and are **not** part of `make check`.

---

## 4. The "do task-N" protocol (Coder Agent)

No `task-NN` file is currently open — all shipped. This protocol governs the
**next** task once one is written to the [`docs/AGENT_RULES.md`](docs/AGENT_RULES.md)
§4 schema and proposed to the user.

When the user says **`do task-15`** (or `task-15`, `run task 15`):

1. Open `tasks/task-15-*.md`. Read **Context, Prerequisites, Scope, Out of scope, Definition of Done**.
2. Read this file, [`docs/AGENT_RULES.md`](docs/AGENT_RULES.md), and the nearest `AGENTS.md` for every directory you will touch.
3. Verify every prerequisite task's `Status: done`. If not, **stop and report** — do not implement prerequisites yourself.
4. Implement **only** what is in *Scope*. Nothing from *Out of scope*, no adjacent tasks, no roadmap items.
5. Write tests as the task requires. Run `make check` until green.
6. Set the task file `Status: done` and append a short changelog (files changed, decisions).
7. Report to the user: what changed, `make check` result, any deviations.

**Never**, in any task:
- edit another `task-NN` file, `docs/ROADMAP.md`, `docs/AGENT_RULES.md`, or this file (except the one status line in step 6);
- add a user-facing config knob (see Strict Directive 1);
- return raw scraped content from an MCP tool (see Strict Directive 2);
- write to **stdout** from anywhere but the MCP transport (see Strict Directive 4);
- use `latest` / floating versions for a dependency, Docker image, or model tag.
