# ROADMAP.md — Mimir

The single roadmap for this repository. It merges what used to be two documents
(`docs/ROADMAP.md` and `docs/PHASE2_ROADMAP.md`), which described two different
initiatives that both called themselves "Phase 2". That naming collision is
resolved here: the word *phase* is gone from the top level. There are two
**tracks**, and every task file, status board, and milestone names one of them.

| Track | What it is | State |
|---|---|---|
| **[Track A](#track-a--the-research-mcp-capability)** | The research MCP server: search, scrape, refine, return compact results to a Claude Code session | MVP + Stage F **shipped**; A.3/A.4 parked |
| **[Track B](#track-b--mimir-desktop--agent-orchestration)** | The Mimir product built on top of it: local store, coding-task runner, live streaming, Maps lead-gen, desktop app, project memory | **M1–M8 shipped** |

Track A is a capability. Track B is the product that consumes it. **Both tracks
are shipped through their planned milestones** (Track A MVP + Stage F; Track B
M1–M7). Track A's §A.3–§A.4 items remain parked and gain task files only if
promoted; new work anywhere starts with a new task file.

> **All planned task files (task-01 … task-34) have been retired** now that every
> one reached `done`. [`../tasks/README.md`](../tasks/README.md) keeps the status
> board as the record; [`CAPABILITIES.md`](CAPABILITIES.md) documents what the
> built system does today.

Scope rule for both tracks: only work with a task file in [`../tasks/`](../tasks/)
is in scope. A section here is a boundary and a plan, never permission to build.

---

# Track A — the research MCP capability

**Goal:** a Claude Code session can search the open web, scrape arbitrary pages,
and get back compact, refined results — no new API key, riding the operator's
existing Claude Code login for the refine step.

## A.1 / A.2 — shipped

- **A.1 MVP** (task-01 … task-16): Go single binary · MCP over stdio ·
  DuckDuckGo search · Crawl4AI in local Docker · headless `claude` CLI refine.
  Tools: `web_search`, `fetch_page`, `research`, `diagnostics`.
- **A.2 Stage F** (no task files — shipped as product work): free, self-written
  scrapers with deterministic extraction (`internal/extract`), no refine call.
  Tools: `ecommerce_product_lookup`, `tiktok_profile_lookup`,
  `gmaps_business_lookup`, `instagram_profile_lookup`.
  Deferred: `linkedin_company_lookup`.

`gmaps_business_lookup` looks up **one** named business (a free scraper returning
a fact); Track B's Maps pipeline enumerates **every** business in a region (a
billed API feeding lead-gen). They share only a subject.

## A.3 — Paid platform scrapers (parked)

For data that genuinely isn't reachable by a free, self-written, no-login
scraper — LinkedIn people profiles and job posts, and ad-transparency data —
where a paid provider may eventually be worth it. Not started; no task files.

Each tool would reuse the **same pipeline**: `collect (provider) → refine
(claude CLI) → isolate (choke-point) → compact response`, with a per-source
prompt profile.

| Planned MCP tool | Purpose | Candidate provider(s) |
|------------------|---------|-----------------------|
| `linkedin_scrape` | company pages, people profiles, job posts | Bright Data LinkedIn dataset · Apify LinkedIn actor · Proxycurl |
| `google_ads_transparency` | advertiser creatives + spend ranges from Google Ads Transparency Center | SerpApi Google Ads Transparency · Bright Data |
| `meta_ads_library` | Facebook/Instagram ad creatives, targeting, run dates | Meta Ad Library API + Apify Meta Ad Library actor (fallback) |

**Cross-cutting components this track would need (still SD-1 compliant — no user
knobs):**

- **Provider credential vault** — credentials injected by the parent process,
  read once at startup. Track B / M4 builds the first instance of this pattern
  for the Places key; A.3 would extend it.
- **Cost & rate budget guard** — per-provider request/credit ceilings enforced
  in code; a call that would exceed budget returns a typed error.
- **Normalized schemas** — `SocialPost`, `AdCreative`, `CompanyProfile`,
  `JobPost`.
- **Per-source refine profiles** — prompt templates in `internal/refine` keyed
  by source type.
- **Provider fallback chain** — ordered providers per tool, automatic failover.

**Promotion checklist (before writing A.3 task files):**

1. MVP `make check` and `make e2e` green on a clean macOS checkout.
2. `research` produces a usable brief on 10 varied real queries (manual eval).
3. The context-isolation choke-point has never been bypassed in testing.
4. Concurrency guardrails hold under 20 parallel `research` calls.
5. A provider is chosen and contractually available for at least one A.3 tool.

## A.4 — Scale & intelligence tools (parked)

Tool ideas that only make sense once A.3 exists and the corpus is large.

| Planned MCP tool | Purpose |
|------------------|---------|
| `serp_multi` | multi-engine SERP aggregation (Google + Bing + DDG) via a paid SERP API |
| `dataset_job` | async long-running scrape jobs (submit → poll/webhook → refined result) for large crawls |
| `competitor_watch` | scheduled diff-monitoring of a target set (ad creatives, pricing pages, job posts) with change summaries |
| `enrich_entity` | join LinkedIn + ads + social signals into one refined entity profile |
| `trend_digest` | periodic refined digest across TikTok/Instagram/Meta Ads for a brand or keyword |

Supporting components still unbuilt:

- **Local embeddings + vector index** — for semantic dedup and recall, fully
  local. An embedding model would be this repo's only local-model dependency
  and needs its own justification.
- **Multi-model refine routing** — `claude-haiku-4-5` for fast distillation, a
  Sonnet-tier model for cross-source synthesis. Track B / M2's second pinned
  model constant (`config.CodingModel`) is the seed; routing would generalize it.
- **Job queue** — `asynq`/NATS for `dataset_job` and scheduled
  `competitor_watch`. Only if in-process orchestration (`internal/events` +
  `internal/coderunner`) provably stops being enough.
- **Budget & usage reporting** — via an extended `diagnostics` tool; Track B /
  M7 already extends `diagnostics` and is the natural home.

(The local store this track assumed is **shipped** in Track B / M1
(`internal/store`); A.4 would extend it, not build it.)

---

# Track B — Mimir desktop & agent orchestration

Status: **M1 through M7 done.** M6 landed gap analysis (task-30), outreach
drafting (task-33), the stages-1–4 `Pipeline` orchestrator + `/maps/*` routes
(task-34), and the desktop lead-gen screen (task-31); M7 (task-32) did the docs
sync, the `/diagnostics` cost/health extension, the Places integration test, and
the Tauri signing/notarization scaffolding.

## B.0 Why, and what this replaces

The owner has a first version of this tool, `goat` — the retired predecessor,
named before the Mimir rebrand — (Node/Electron + an abandoned
SwiftUI rewrite, `GoatNative/`), that they no longer want to build on. It got
into a genuinely bad state:

- No top-level git repo, mid-refactor limbo (`packages/` vs. still-referenced
  `src/`), a half-finished rename (`goat` → `hermes`), a self-inconsistent test
  count.
- Two orchestration mechanisms never unified: a JSON-DAG `orchestrator.js` and a
  bolted-on `kanbanQueue.js` autopilot loop.
- A hand-rolled tool-calling loop (`tools.js` + `tool-loop.js`) with
  `"tools":{"roots":["/"]}` (whole-disk file access) as the *default* — a large
  exfiltration surface acknowledged in its own README.
- A working Google Maps scraper (`scripts/gmaps-scraper.js`, Puppeteer) never
  wired into anything.
- A declared Svelte+shadcn-svelte frontend that doesn't exist on disk.
- One good idea worth keeping: its `claude-code.js` provider streams
  `claude -p --output-format stream-json --verbose` incrementally and translates
  it into typed events for a live "thinking" panel over SSE. That pattern is the
  one piece of v1 this plan ports forward.

This repo (`mimir`) is a clean-room build that already avoids most of
that: constants-only config (SD-1), a fail-closed context-isolation choke-point
(SD-2/SD-7, `internal/mcp/finalize.go`), bounded/cancellable concurrency (SD-3),
typed sentinel errors (SD-6), tests on every task (SD-8). It also already
satisfies requirement 1 — `internal/refine.Client.Distil()` shells out to the
local `claude` CLI, no Anthropic SDK, no API key, no Ollama.

## B.1 Requirements (the owner's ask)

1. LLM: exclusively Claude Code models, no other provider.
2. Minimize Claude Code token spend on repetitive operations via dedicated
   scraper/data APIs, with the data managed inside this tool.
3. Pick a project folder (Finder-style picker), then run agents/APIs scoped to
   that folder to complete coding tasks.
4. A Google Maps scraper: find all companies in a region, categorize them,
   analyze each category's gaps/needs, draft marketing outreach emails.
   **Decision:** Google Places API as primary data source, headless-browser
   scraping as fallback only.
5. A macOS desktop app optimized for Apple Silicon, shadcn-standard design.
   **Decision:** Tauri (Rust shell, native ARM64, WKWebView) + React +
   shadcn/ui + Tailwind — not Electron, not native SwiftUI.
6. While Claude Code runs, watch its full thought/action stream live,
   second-by-second, like a terminal.

## B.2 Layered architecture

```
┌───────────────────────────────────────────────────────────────────────────┐
│ DESKTOP APP LAYER  (desktop/, Tauri + React + shadcn/ui)                  │
│  - Rust shell (src-tauri): spawns/owns mimir-daemon as a sidecar process,  │
│    allocates its port + a per-launch bearer token, exposes both to JS via │
│    Tauri IPC (never stdout-scraped — see B.2.1), hosts the native folder  │
│    picker (plugin-dialog → NSOpenPanel)                                  │
│  - React: Project picker, live "terminal" run view, Maps lead-gen         │
│    dashboard — shadcn/ui components only                                 │
└───────────────────────────────────────────────────────────────────────────┘
                 │ HTTP (via Rust)                 │ WebSocket (live events)
                 ▼                                  ▼
┌───────────────────────────────────────────────────────────────────────────┐
│ TRANSPORT LAYER                                                            │
│  cmd/mimir-mcp (existing, untouched entrypoint)                            │
│    → internal/mcp.Server + mcp.StdioTransport   (Claude-Code-as-consumer) │
│  cmd/mimir-daemon (long-running, spawned by Tauri)                         │
│    → internal/mcp.Server + mcp.StreamableHTTPHandler on the SAME          │
│      internal/mcp.Registry tool set (reuses finalize.go verbatim)        │
│    → internal/api: REST+WS for non-tool-shaped surfaces:                  │
│      /projects, /coding-tasks, /ws/runs/{id}, /maps/*, /diagnostics      │
└───────────────────────────────────────────────────────────────────────────┘
                 │                                  │
                 ▼                                  ▼
┌───────────────────────────────────────────────────────────────────────────┐
│ ORCHESTRATION / RUNTIME LAYER  (both binaries import these — one engine)  │
│  internal/project    — folder registry, path canonicalization/guards     │
│  internal/coderunner — scoped, streaming `claude` CLI sessions           │
│  internal/events     — in-process pub/sub bus (run_id → chan Event)      │
│  internal/leadgen    — Maps pipeline orchestrator (search→categorize→    │
│                         gap-analysis→email), sibling to internal/pipeline│
│  internal/pipeline   — research/fetch_page orchestrator, EXTENDED to     │
│                         consult internal/store before crawl+refine      │
└───────────────────────────────────────────────────────────────────────────┘
                 │
                 ▼
┌───────────────────────────────────────────────────────────────────────────┐
│ DATA / CAPABILITY LAYER                                                    │
│  internal/search    — DuckDuckGo client (reused as-is)                   │
│  internal/crawl     — Crawl4AI sidecar client (reused as-is)            │
│  internal/refine    — `claude -p` headless Distil() (reused, EXTENDED    │
│                        with Maps prompt profiles)                       │
│  internal/maps      — Google Places API client (primary)                 │
│  internal/mapscrape — Playwright-sidecar client (fallback)              │
│  internal/store     — SQLite persistence/cache (B.3)                     │
│  internal/config    — constants-only config, EXTENDED (SD-1)            │
└───────────────────────────────────────────────────────────────────────────┘
```

**Why two binaries, one engine — not two orchestrators.** `cmd/mimir-mcp` stays a
thin stdio entrypoint whose lifecycle Claude Code owns. `cmd/mimir-daemon` is the
*only* new orchestration surface — it owns the coding-task runner, event bus,
and Maps pipeline, and re-exposes the same `internal/mcp.Registry` tools over
HTTP for parity. This is the direct fix for goat v1's dual-orchestrator mistake:
exactly one runtime engine, two transports.

### B.2.1 End-to-end live streaming (requirement 6)

Ports goat v1's `claude-code.js` `stream-json`/`translateStreamEvent` logic to
Go.

1. **Spawn.** `internal/coderunner.Runner.StartCodingTask(ctx, project, prompt)`
   runs `claude -p --output-format stream-json --verbose --model <const>
   --permission-mode <const> --add-dir <project.Path> --mcp-config <tmpfile>`
   with `cmd.Dir = project.Path`. The `--mcp-config` tmpfile names the existing
   `mimir-mcp` binary as a nested MCP server, so the coding agent calls back into
   Mimir's own tools instead of its own web access (requirement 2 on the
   coding-task path).
2. **Incremental parse.** A goroutine reads `cmd.StdoutPipe()` line-by-line
   (`bufio.Scanner`); a per-run state struct tracks block ids and cumulative
   text/thinking length to emit only the delta.
3. **Typed events** (`internal/events`): `RunStarted`, `TextDelta`,
   `ReasoningDelta`, `ToolCall`, `ToolResult`, `RunCompleted`, `RunFailed`.
4. **Publish.** Per-subscriber buffered channels + non-blocking sends — a slow
   WS client drops/coalesces rather than stalling the read loop.
5. **Persist.** Each event is appended to a JSONL transcript and indexed by a
   `coding_runs` row in `internal/store`.
6. **Fan out.** `GET /ws/runs/{run_id}` upgrades to WebSocket, subscribes to the
   bus, writes each event as one JSON frame.
7. **Render.** React opens that socket after `POST /coding-tasks` returns a
   `run_id`. Deltas append to a streaming pane; tool calls/results render as
   collapsible shadcn cards with a risk badge.

**Local IPC:** Tauri's Rust shell allocates the port, generates a per-launch
random bearer token, and passes both to `mimir-daemon` via flags/env it controls.
The frontend asks Tauri's own IPC (`invoke('get_daemon_endpoint')`), never
parses subprocess text. The daemon binds `127.0.0.1` only and rejects
unauthenticated requests. (M3 correction: REST calls are made from Rust, not the
WebView — an `Authorization` `fetch` from `tauri://localhost` is cross-origin and
the daemon has no CORS headers by design. The WebSocket handshake is exempt from
preflight, so its subprotocol auth is unaffected.)

## B.3 Local persistence / cache (`internal/store`)

**Engine:** SQLite via `modernc.org/sqlite` (pure Go, no CGO).
`PRAGMA journal_mode=WAL` + `busy_timeout` so `mimir-mcp` and `mimir-daemon` share
one DB file safely. Migrations: embedded `.sql` files run once via `embed.FS`,
append-only.

| Table | Key | Purpose | Invalidation | State |
|---|---|---|---|---|
| `crawl_pages` | `sha256(url)` | raw crawl cache: markdown + raw HTML | `config.PageCacheTTL` | ✅ M1 |
| `refined_pages` | content+query+ceiling+prompt hash | refine cache: refined text + token estimate | same TTL; `RefinePromptVersion` bump | ✅ M1 |
| `projects` | `id` | canonicalized folder path for the coding-task runner | none (re-pick to change) | ✅ M2 |
| `coding_runs` | `id` | run metadata, cost, session id, transcript pointer | none (history) | ✅ M2 |
| `companies` | `place_id` | normalized Places/scrape result | caller-supplied long TTL (~30d) | ✅ M4 |
| `region_searches` | `maps.Query.Key()` | the ordered `place_id` list one region search returned | same TTL; all-or-nothing on read | ✅ M4 |
| `company_categorization` | `place_id` + `version` | normalized category + which tier answered | bump `config.LeadgenCategoryVersion` | ✅ M5 |
| `category_gap_analysis` | `(region, category, prompt_version, company_set_hash)` | Claude-synthesized gaps/needs | `LeadgenGapVersion` bump; a changed company set misses | ✅ M6 (task-30) |
| `outreach_emails` | `place_id` + `prompt_version` | drafted email + `status` (draft/sent/skipped) | `LeadgenEmailVersion` bump; "sent"/"skipped" blocks regeneration (SQL-enforced) | ✅ M6 (task-33) |

**How this cuts token spend.** The only thing that spends Claude Code tokens is
`internal/refine.Client.Distil()`. Every call site that reaches it does a `store`
lookup *first*. A cache hit means no Crawl4AI request, no `claude` subprocess,
zero tokens. `prompt_version` constants make invalidation a deliberate, tracked
action.

## B.4 Coding-task runner: folder-scoped permissions

**Registry (`internal/project`).** `Project{ID, Path, DisplayName, CreatedAt,
LastUsedAt}`. On registration the path is canonicalized (`filepath.Abs` +
`filepath.EvalSymlinks`), must `os.Stat` as an existing directory, and is
rejected if it equals `/`, the home directory, or other denylisted roots.
**There is no default project** — a coding task cannot start until one is
explicitly registered via the picker.

**Picker → daemon flow.** Tauri's `plugin-dialog` `open({directory: true})`
invokes native `NSOpenPanel`. React gets an absolute path, `POST /projects
{path}`; the daemon canonicalizes/validates/guards and returns a `project_id`.
**Every subsequent call takes only `project_id`**, re-validated on each use.

**Runner scoping.** `internal/coderunner` sets `cmd.Dir = project.Path` *and*
passes `--add-dir project.Path`, plus a fixed `--permission-mode` constant from
`internal/config` (SD-1). This is a different `claude` invocation profile than
`Distil()` — `Distil` stays headless/tool-less/`--restricted`; the coding-task
runner is interactive/tool-enabled/streaming, trusting Claude's native
tool-calling loop instead of hand-rolling one.

## B.5 Maps lead-gen pipeline

Threaded end-to-end by `place_id`, orchestrated by `internal/leadgen`.

1. **Region search — no LLM.** `internal/maps` (Places API Text Search) is
   primary: deterministic HTTP in, `Company` rows out, cached in `companies` +
   `region_searches`. Fallback: `internal/mapscrape` drives a Playwright docker
   sidecar, deterministic DOM extraction — zero tokens either way.
2. **Categorization — mostly no LLM.** A static Google `types[]` →
   normalized-category lookup table handles the common case. Claude is invoked
   only for the residual ambiguous companies, in batches. Cached in
   `company_categorization`.
3. **Per-category gap/needs analysis — Claude's real job.** Deterministic
   pre-processing computes compact structured facts per company; only those go
   into a bounded refine call synthesizing patterns across the category. Same
   SD-7 hard-ceiling discipline as `research`. Cached in `category_gap_analysis`.
4. **Marketing email drafting — genuinely generative.** One (or small-batched)
   refine call per company, fed the category's gap analysis + that company's
   facts. Cached in `outreach_emails`; a `status` field means a region re-run
   never regenerates something a human marked sent.

Stages 1–2 are near-zero-token by construction; stage 3 (**M6 / task-30, done**)
and stage 4 (**M6 / task-33, done**) are where Claude Code tokens are
deliberately spent, made a one-time cost per `(entity, prompt_version)` by the
store. `leadgen.Pipeline.Run` threads all four into one call, over the
`/maps/leadgen` route (**M6 / task-34, done**).

## B.6 Milestones

M1–M7 all shipped (former task files `task-17` … `task-34`, since retired).
Carry-forward notes:

- **M2** — the stream schema was captured from the live CLI (v2.1.251), not
  assumed from goat v1: it has events the old code never saw (`rate_limit_event`,
  `system/thinking_tokens`) and uses `system/subtype:init` as run-start. goat v1's
  delta tracking was broken (one text-length counter per *run*; the CLI
  restarts each message at zero) — tracking is now per `message.id`,
  mutation-verified. The daemon's listen address and auth token are process
  plumbing, parent-provided — documented in `internal/config/AGENTS.md`, not an
  SD-1 knob.
- **M3** — the frontend cannot call the daemon's REST API directly (cross-origin
  `Authorization` `fetch` → 401 on the CORS-header-less `OPTIONS`). The shell
  performs REST from Rust instead, which also keeps the token out of the WebView.
- **M4** — **credential decision:** a narrow, documented exception to
  `docs/SECURITY.md`'s "zero API keys configured by Mimir itself" for the
  operator-provisioned Places key — read once at startup from
  `MIMIR_GOOGLE_PLACES_API_KEY`, never runtime-tunable, documented in
  `internal/config/AGENTS.md` under "operator-provisioned credential".
- **M5** — `internal/mapscrape` takes/returns the same types as the Places
  client (drop-in fallback), with ids namespaced so a scraped row can never
  overwrite a billed one.

### M6 — Gap analysis + email drafting + leadgen UI (L). ✅ done

The Maps pipeline end to end, surfaced in the app.

- **Stage 3 — gap analysis (`task-30`, done).** `internal/leadgen` (extended —
  `AnalyzeCategory`, two-tier cache→model), `internal/refine` (extended — the
  third prompt profile `AnalyzeGaps`, Distil-shaped, same `run` call site, SD-7
  ceiling), `internal/store` (extended — `category_gap_analysis`, keyed by
  `(region, category, prompt_version, company_set_hash)`), `internal/config`
  (`LeadgenGapVersion`, `LeadgenGapMaxTokens`, `LeadgenGapMinCompanies`). The
  model is fed five locally-computed scalars per company, never a raw page.
- **Stage 4 — email drafting (`task-33`, done).** `internal/leadgen` (extended —
  `DraftFor`, two-tier cache→model), `internal/refine` (extended — the fourth
  prompt profile `DraftEmail`), `internal/store` (extended — `outreach_emails`
  keyed by `(place_id, prompt_version)`, with a `status` of draft/sent/skipped
  the region re-run respects), `internal/config` (`LeadgenEmailVersion`,
  `LeadgenEmailMaxTokens`). One draft per company, fed the company's facts + its
  category's stage-3 gap analysis.
- **Orchestrator + routes (`task-34`, done).** `internal/leadgen` (extended —
  `Pipeline.Run`, threading region search + stages 2–4 by `place_id`, bounded
  fan-out, `ErrNoData` the only hard failure), `internal/api` (extended —
  `POST /maps/leadgen`, `POST /maps/emails/status`, registered only with a
  Places key), `internal/tools` (`Deps.Maps` shares one Places client),
  `cmd/mimir-daemon` (wiring), `internal/config` (`LeadgenRegionTTL`).
- **UI (`task-31`, done).** `desktop/src/screens/Leadgen.tsx` — a third screen
  beside `Connection` and `Workspace`, tab-switched in `App.tsx`: the region
  search form, per-category gap analysis cards, and a company list with an
  expandable draft email and mark-sent/skip buttons wired to
  `POST /maps/emails/status`. `report.notes` shown verbatim. No Rust change —
  `daemon_request` already proxies the `/maps/*` paths.

### M7 — Hardening, docs sync, packaging (M). ✅ done (`task-32`)

- **Docs sync** — `AGENTS.md`, `docs/ARCHITECTURE.md` (new §7 — Track B),
  `docs/AGENT_RULES.md` (§1.3, §2, SD-1) and `docs/SECURITY.md` now describe
  both binaries, every Track B package, the `/maps/*` routes, the daemon token,
  and the operator-provisioned Places credential. (The Ollama references were
  already gone; the one that remains in `docs/SECURITY.md` is a deliberate
  history note.)
- **`/diagnostics` extended** — `internal/store.CodingRunStats` (total / running
  / failed / summed `cost_usd`, one aggregate query), surfaced on the HTTP
  `/diagnostics` alongside `places_configured`. A live Places probe was
  deliberately not added — it would be a billed request per health check.
- **Places integration test** — `internal/maps/integration_test.go`
  (`//go:build integration`, skips without a key). The Playwright-sidecar one
  shipped with task-28.
- **Tauri packaging** — `tauri.conf.json` builds `app` + `dmg` with hardened
  runtime and `entitlements.plist`; signing/notarization are env-driven at
  build time (`APPLE_SIGNING_IDENTITY` + Apple-ID/API-key creds), so a keyless
  build still produces an ad-hoc app.

**Deferred, recorded, not scheduled:** unifying `internal/leadgen`'s per-call
refine fan-out limit with `internal/pipeline`'s process-wide semaphore (a change
to `internal/pipeline`, its own task).

(The Track A / Track B naming collision this document used to list as an M7
to-do is resolved by the merge and needs no further work.)

---

**Every roadmap milestone across both tracks is now shipped.** Track A: MVP +
Stage F, with §A.3–§A.4 parked. Track B: M1–M7. New work starts with a new task
file.

## B.7 Reuse / extend / net-new

**Reused as-is:** `internal/mcp` (Tool interface, Registry, `finalize.go`
choke-point, logging, `StreamableHTTPHandler` from go-sdk v1.7.0);
`internal/search`; `internal/crawl`; the exec/parse/clamp machinery in
`internal/refine`; `cmd/mimir-mcp` as an entrypoint.

**Extended:** `internal/pipeline` (store lookups before crawl/refine);
`internal/refine` (Maps prompt profiles); `internal/config` (new constants —
same constants-only pattern, no new user-tunable knobs); `docs/*` (sync pass).

**Net-new:** `internal/store` ✅, `internal/project` ✅, `internal/coderunner` ✅,
`internal/events` ✅, `internal/api` ✅, `cmd/mimir-daemon` ✅, `internal/maps` ✅,
`internal/mapscrape` ✅, `internal/leadgen` ✅ (stages 2–4 + `Pipeline`
orchestrator done), `deploy/playwright-maps/` ✅, `desktop/` ✅ (lead-gen screen
shipped — task-31).

## B.8 From goat v1 — what to port vs. leave behind

**Port:** the three-way agent/workflow/config split as a *concept*
(tier/capability abstraction over hardcoded model names); the `claude-code.js`
stream-json translation logic (B.2.1); the Markdown+frontmatter agent-definition
shape if a broader agent library is needed; the `gitDiff.js` snapshot/revert
idea for gating write-capable steps behind human approval, if manual-approval
mode is ever added.

**Ported, and deliberately reshaped — the project-memory pattern (M8, §B.9).**
The need arrived, so this one moved from "if" to shipped. What was ported is the
*intent* (a project should not be re-explained every session) and one sentence of
v1's framing (memory is background, not instruction; the repository wins). What
was explicitly **not** ported is its mechanism. See §B.9.

**Leave behind:** the hand-rolled tool-calling loop; the dual DAG/kanban
orchestration split; whole-disk default file access (no default project, ever);
the disconnected `gmaps-scraper.js` script; vendoring an unrelated project's
files into the repo for RAG indexing; the `MIMIR_PORT=<n>` stdout-scrape IPC hack.

---

# B.9 Project memory (M8) — shipped

**Requirement.** B.1's requirement 2 is "minimize Claude Code token spend on
repetitive operations". M1–M7 did that for *external* data: a page fetched once
is not fetched again. The largest repeated cost left is internal — every session
rediscovering the same repository from scratch. M8 closes it.

**What shipped.** `internal/sessionlog` (inert transcript parser) and
`internal/memory` (bounded two-phase ingest, per-episode recap, read-time brief),
migration `0008_memory.sql` with an FTS5 index, a fifth `internal/refine` prompt
profile (`Recap`), and three MCP tools — `project_context`, `context_recall`,
`context_remember` — registered in the same single list as every other tool, so
both binaries serve them. Full inventory: [`CAPABILITIES.md`](CAPABILITIES.md)
§2. Design: [`ARCHITECTURE.md`](ARCHITECTURE.md) §7.7.

**The v1 lesson, made structural.** goat v1 kept one project-memory document and
had a model rewrite it each pass. One rewrite degenerated — a stuck decoder,
drift through several languages, loops of self-correction — and because the
document *was* the memory, that output became the project's permanent memory. It
is still on disk in the v1 repo, still unreadable; a slice is checked in as
`internal/refine/testdata/degenerate_recap.txt` and is the fixture for
`refine.validateRecap`'s regression test.

So M8 has **no document**. A model sees one episode and writes at most a title
and three bullets; its output is validated before it is believed; a rejection
costs that recap and nothing else; the briefing is assembled from rows on every
call. The failure v1 had is not guarded against — it is unreachable.

**Cost, measured.** The first backfill over this repository's own history — 15
transcripts, ~50 MB, 51 episodes — filtered to 34 worth distilling and took
about 3.7 minutes of haiku, with zero rejections. Phase 1 (parse and store) took
325 ms and costs nothing, which is why search and the timeline work before any
model call.

**Deliberately not built.** No embeddings or vector index (§A.4 still parks
that); FTS5 + bm25 is enough for a few hundred episodes per project and adds no
local-model dependency. No cross-project memory: the store is keyed by project
path and stays that way. No desktop surface yet — the memory is reached through
MCP, which is where the token spend it targets actually happens.

---

# Task ↔ roadmap map

Full index, prerequisites, and status board: [`../tasks/README.md`](../tasks/README.md).
The individual `task-NN` files have been removed; the mapping below is retained
as the record.

| Tasks | Roadmap home | State |
|---|---|---|
| task-01 … task-16 | A.1 — MVP | ✅ shipped |
| *(no task files)* | A.2 — free scrapers (Stage F) | ✅ shipped |
| task-17, task-18 | B / M1 — store + page cache | ✅ shipped |
| task-19 … task-22 | B / M2 — registry, coderunner, daemon | ✅ shipped |
| task-25, task-26, task-27 | B / M3 — WS streaming + desktop shell | ✅ shipped |
| task-23, task-24 | B / M4 — Maps data layer | ✅ shipped |
| task-28, task-29 | B / M5 — scrape fallback + categorization | ✅ shipped |
| task-30 | B / M6 — lead-gen gap analysis (stage 3) | ✅ shipped |
| task-33 | B / M6 — outreach email drafting (stage 4) | ✅ shipped |
| task-34 | B / M6 — stages-1–4 orchestrator + `/maps/*` routes + daemon wiring | ✅ shipped |
| task-31 | B / M6 — desktop lead-gen screen | ✅ shipped |
| task-32 | B / M7 — hardening, docs sync, `/diagnostics`, packaging | ✅ shipped |

**All milestones shipped.**
