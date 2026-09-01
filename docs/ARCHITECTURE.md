# ARCHITECTURE.md — GOAT

Reference for the design. Rules live in [`AGENT_RULES.md`](AGENT_RULES.md); this
file explains the shape so task implementations are not guesswork.

The repo is built in two tracks (see [`ROADMAP.md`](ROADMAP.md)). §1–§6 below
describe **Track A** — the research MCP server, `bin/goat-mcp`, shipped and
stable. §7 describes **Track B** — the GOAT product built on top of it:
`bin/goat-daemon`, the `internal/{store,project,coderunner,events,api,maps,
mapscrape,leadgen}` packages, and the Tauri desktop app. Track B extends this
foundation; it does not replace it.

---

## 1. One process, stdio MCP (Track A)

`bin/goat-mcp` is a single macOS binary launched by the end-user's Claude Code as
an MCP server over **stdio**. It has no HTTP server, no daemon, and no persisted
state of its own — a repeat query is served from the SQLite cache (`internal/store`,
Track B / M1) when one is present, and uncached otherwise.

```
Claude Code (consumer)
   │  MCP / stdio (JSON-RPC frames on stdout/stdin)
   ▼
┌──────────────────────────────────────────────────────────┐
│ goat-mcp                                                  │
│                                                          │
│  cmd/goat-mcp  ── wiring + lifecycle only                 │
│      │                                                    │
│  internal/mcp ── transport, tool registry, schemas,       │
│      │           error mapping, response choke-point      │
│      ▼                                                    │
│  internal/tools ── web_search · fetch_page · research ·   │
│      │             diagnostics                            │
│      ▼                                                    │
│  internal/pipeline ── orchestration + bounded concurrency │
│      │        │        │                                  │
│      ▼        ▼        ▼                                  │
│  search/    crawl/    refine/                             │
└────┼─────────┼──────────┼────────────────────────────────┘
     │         │          │
     ▼         ▼          ▼
 DuckDuckGo   Crawl4AI    claude CLI
 (html/lite) (127.0.0.1: (exec, headless,
             11235)      claude-haiku-4-5)
```

`internal/config` is imported by everything and holds every operational constant.

---

## 2. Package responsibilities

| Package | Responsibility | Must not |
|---------|----------------|----------|
| `cmd/goat-mcp` | build `Config`, construct clients, register tools, start MCP server, handle SIGINT/SIGTERM → context cancel | contain business logic or external calls |
| `internal/config` | `Load() Config`; all constants; enumerated test-only env overrides | expose setters / read a config file / parse flags |
| `internal/mcp` | wrap the MCP Go SDK, stdio transport, `Tool` interface, JSON-schema registration, panic recovery, error mapping, the SD-2/SD-7 response choke-point | call DDG/Crawl4AI/`claude` directly |
| `internal/search` | DuckDuckGo free search (`html.duckduckgo.com/html/`, fallback `lite.duckduckgo.com/lite/`); parse to `[]Result{Title,URL,Snippet}`; polite rate-limit + backoff | use a paid API or an API key |
| `internal/crawl` | HTTP client to local Crawl4AI; POST a URL, receive clean Markdown + metadata; typed `ErrDockerUnavailable` | fetch arbitrary URLs itself without going through Crawl4AI |
| `internal/refine` | headless `claude` CLI subprocess (`exec`), pinned model, no tools; deterministic prompt assembly; injection firewall; output ceiling; typed `ErrClaudeUnavailable` | return content it did not size-check |
| `internal/pipeline` | `Research(ctx, Query) (Brief, error)` and `Fetch(ctx, url) (RefinedPage, error)`; errgroup fan-out with `SetLimit`; deadlines; partial-result merge + dedup | hold shared mutable state without synchronisation; spawn unbounded goroutines |
| `internal/tools` | thin MCP handlers: parse args → call pipeline/search → shape compact response | implement orchestration or HTTP logic |

Stage F (free, self-written scrapers) adds `internal/{ecommerce,tiktok,gmaps,
instagram}` + the shared `internal/extract` (JSON-LD / embedded-SPA-JSON /
OpenGraph helpers). Those tools return small structured facts and skip
`internal/refine` entirely (`MetadataOnly()`), the same shape `web_search` uses.

---

## 3. The refine pipeline (SD-2 in practice)

1. **search** — `search.Client.Search(ctx, query, n)` → `[]Result`.
2. **select** — pipeline takes the top `TopNForResearch` (config, default 5) URLs.
3. **crawl** — `errgroup` with `SetLimit(MaxConcurrentCrawls)` (default 4); each
   worker calls `crawl.Client.Markdown(ctx, url)` with `CrawlTimeout` (45s).
   Failures are recorded, not fatal.
4. **refine** — `errgroup` with `SetLimit(MaxConcurrentRefines)` (default 2); each
   worker calls `refine.Client.Distil(ctx, RefineInput{Query, PageMarkdown, MaxTokens})`.
   The refiner is instructed to output factual bullet points scoped to the query,
   drop navigation/boilerplate, and stay within `MaxTokens`.
5. **merge** — pipeline concatenates per-source distillates, dedups near-identical
   points, builds the `Brief{Summary, KeyPoints, Sources, Gaps}` within the
   `research` ceiling (~2000 tokens). A final short refine pass may compress the
   summary.
6. **choke-point** — `internal/mcp` verifies size + `refined: true` provenance and
   fails closed on violation.

`fetch_page` is steps 3–4–6 for a single URL.
`web_search` is step 1 only, plus the choke-point.

---

## 4. Concurrency model (SD-3)

- One `context.Context` per MCP request, carrying a request ID and an overall
  deadline (`ResearchTimeout`, 120s).
- Two bounded stages (crawl, refine) via `errgroup.WithContext` + `SetLimit`.
- A process-wide semaphore (`tasks/task-14`) caps total in-flight crawls/refines
  across concurrent MCP calls, so ten parallel `research` calls cannot open forty
  crawls.
- Politeness: a per-host minimum interval for DuckDuckGo and for repeated hits to
  the same target host within one request.
- Tests: `go test -race`, plus `goleak.VerifyNone` in pipeline tests; a table test
  with slow/failing fake sources asserting the deadline is honoured and no
  goroutine outlives the call.

---

## 5. External service contracts (MVP)

### DuckDuckGo (free)
- `POST https://html.duckduckgo.com/html/` with form `q=<query>`; parse result
  anchors + snippets from the HTML. Fallback: `https://lite.duckduckgo.com/lite/`.
- No key. Set a normal browser `User-Agent`. Respect a small delay between calls.

### Crawl4AI (local Docker, pinned tag)
- `docker-compose` in `deploy/crawl4ai/`, published on `127.0.0.1:11235`.
- Request: `POST /crawl` (or the image's documented markdown endpoint) with the
  target URL; response includes cleaned Markdown. Exact endpoint/shape is fixed by
  the pinned image and recorded in `internal/crawl`.
- Health: `GET /health` (or a cheap known route) used by `diagnostics` and at
  first use.

### claude CLI (local, headless, pinned model)
- `exec.CommandContext(ctx, cliPath, "-p", "--model", "claude-haiku-4-5-20251001", "--output-format", "json", "--no-session-persistence", "--strict-mcp-config", "--restricted", "--effort", "low", "--system-prompt", system, "--disallowedTools", "...")`, page content piped over stdin, response parsed from the `result` field of the CLI's JSON stdout.
- Health: `claude --version` succeeds; no API key required — auth rides the operator's existing `claude login` session.

---

## 6. MCP tools (MVP surface)

| Tool | Input | Output (compact) | Ceiling |
|------|-------|------------------|---------|
| `web_search` | `query: string`, `count?: int (≤30, default 8)` | `results: [{title, url, snippet}]` | 30 results, metadata only |
| `fetch_page` | `url: string` | `{ url, title, refined: true, markdown }` | ~1500 tokens |
| `research` | `query: string`, `depth?: int (≤ TopNForResearch)` | `{ summary, key_points: [], sources: [{n, title, url}], gaps: [], refined: true }` | ~2000 tokens |
| `diagnostics` | none | `{ crawl4ai: {ok, detail}, claude: {ok, detail}, duckduckgo: {ok, detail}, maps_scraper: {ok, detail, optional}, versions: {…} }` | small, fixed |
| `maps_search` | `query: string`, `count?: int (≤60)`, `language_code?`, `region_code?`, `near?: {lat, lng, radius_meters}` | `{ query, returned, total_found, truncated, companies: [{place_id, name, address, …}] }` | ~2000 tokens; **billed** — registered only when `GOAT_GOOGLE_PLACES_API_KEY` is set |
| Stage F: `ecommerce_product_lookup`, `tiktok_profile_lookup`, `gmaps_business_lookup`, `instagram_profile_lookup` | one URL / handle each | small structured facts, no prose | 300–400 tokens; no refine call |

Track A's remaining planned tools are named (no schemas yet) in
[`ROADMAP.md`](ROADMAP.md) §A.3–§A.4.

---

## 7. Track B — the GOAT product (M1–M7 shipped)

Track A is a capability. Track B is the desktop product that consumes it: a
local store, a folder-scoped coding-task runner with live streaming, and a
Google Maps lead-generation pipeline, all behind a Tauri app.

### 7.1 Two binaries, one engine

```
  Claude Code (MCP client)                Tauri desktop app (React + shadcn/ui)
        │ stdio                                 │ Tauri IPC ── REST via Rust shell
        ▼                                       ▼           ── WebSocket (browser)
  cmd/goat-mcp                            cmd/goat-daemon  (spawned as a sidecar;
   → internal/mcp (stdio)                   parent-provided 127.0.0.1 port + token)
                                            → internal/mcp  (StreamableHTTPHandler on /mcp)
                                            → internal/api  (REST + /ws + /maps/*)
        └──────────────┬──────────────────────────┘
                       ▼   both import the same packages — one runtime engine
   internal/{store · project · coderunner · events · pipeline · leadgen}
   internal/{search · crawl · refine · maps · mapscrape · config}
```

`cmd/goat-mcp` is unchanged: a thin stdio entrypoint whose lifetime an MCP
client owns. `cmd/goat-daemon` is the only new orchestration surface — it owns
the coding-task runner, the event bus, and the Maps pipeline, and re-exposes the
same `internal/mcp.Registry` over HTTP for parity. Exactly one runtime engine,
two transports (the direct fix for goat v1's dual DAG/kanban orchestrators).

### 7.2 Track B package responsibilities

| Package | Responsibility | Must not |
|---------|----------------|----------|
| `cmd/goat-daemon` | `ValidateDaemon`; open the store (fatal if it cannot — projects *are* the store); build the runtime engine; serve `internal/api` until SIGTERM, then drain in-flight runs | scrape its own stdout; start unauthenticated |
| `internal/store` | SQLite (`modernc.org/sqlite`, no CGO), WAL; append-only embedded migrations; caches + local records: `crawl_pages`, `refined_pages`, `projects`, `coding_runs`, `companies`, `region_searches`, `company_categorization`, `category_gap_analysis`, `outreach_emails`, and the M8 memory tables (`memory_episodes`, `memory_notes`, `memory_ingest_state`, `memory_fts`) | be a source of truth the consumer sees; fail the process on a bad DB (SD-6) |
| `internal/project` | folder registry; canonicalize (`Abs`+`EvalSymlinks`), reject `/`, `$HOME`, denylisted roots; opaque `project_id` after registration — no default project, ever | accept or re-join a raw path anywhere but registration |
| `internal/coderunner` | scoped streaming `claude` sessions: `-p --output-format stream-json`, `cmd.Dir` + `--add-dir` = project path, fixed `--permission-mode`; incremental parse → typed events; JSONL transcript + `coding_runs` row | hand-roll a tool loop; derive a run's context from the request |
| `internal/events` | in-process pub/sub: `run_id → chan Event`; per-subscriber buffered non-blocking sends; a slow watcher drops/coalesces, never stalls the runner | block the producer on any consumer |
| `internal/api` | the daemon's HTTP surface: recover→log→loopback→token→body-cap around one mux; `/healthz`, `/diagnostics`, `/projects`, `/coding-tasks`, `/ws/runs/{id}`, `/mcp`, `/maps/*`; REST called from the Rust shell (no CORS, by design) | make a decision a runtime package already owns; add a path parameter outside `POST /projects` |
| `internal/maps` | Google Places API (New) `places:searchText`; field-mask cost control; bounded pagination; typed sentinels; key only in `X-Goog-Api-Key` | read the environment; touch the store; request a free-text field (would need a clamp) |
| `internal/mapscrape` | Playwright docker sidecar (`deploy/playwright-maps/`, pinned tag) — the Places fallback; same in/out types as `internal/maps`; scraped ids namespaced so they cannot overwrite a billed row | be the primary source; return anything a billed row would |
| `internal/leadgen` | the lead-gen stages and their `Pipeline` orchestrator (§7.4); each stage cache-first, degrade-never-fail | let a stage failure escape as an error (only `ErrNoData` / cancellation do); send model-written text past its package (categorization) |
| `internal/sessionlog` | parse Claude Code and coding-run JSONL transcripts into `Episode` values; segment on human prompts only; score significance before any model call | touch the network, a model, or the store; fail an ingest on a malformed line (skip it) |
| `internal/memory` | long-lived per-project memory (§7.7): bounded two-phase ingest, per-episode recap via `refine.Recap`, read-time assembly of the brief | let a model rewrite an aggregate; materialize the brief; return a response over its ceiling; validate a path itself |

### 7.3 Live streaming (`internal/coderunner` + `internal/events` + `internal/api`)

Ports goat v1's `claude-code.js` `stream-json` translation to Go. A goroutine
reads `cmd.StdoutPipe()` line by line, tracks cumulative text/thinking length
**per `message.id`** (goat v1's per-run counter silently swallowed the opening
of every message after the first), and emits only deltas as typed events
(`RunStarted`, `TextDelta`, `ReasoningDelta`, `ToolCall`, `ToolResult`,
`RunCompleted`, `RunFailed`, plus `rate_limit`). Each event is published on the
bus *and* appended to a JSONL transcript. `GET /ws/runs/{id}` replays the
transcript then follows the bus, stitched on `Event.Seq` so a lossy bus never
shows a watcher a hole.

### 7.4 Maps lead-gen pipeline (`internal/leadgen`)

Threaded end to end by `place_id`, four stages, cost rising left to right:

1. **Region search** — `internal/maps` (primary) or `internal/mapscrape`
   (fallback); cached in `companies` + `region_searches` (all-or-nothing on
   read). Zero tokens.
2. **Categorize** — a static Google-type → normalized-`Category` rule table
   answers the common case for free; `internal/refine.Classify` (closed
   vocabulary, no prose out) handles the residue in batches. Cached in
   `company_categorization`, keyed by `config.LeadgenCategoryVersion`.
3. **Per-category gap analysis** — `internal/refine.AnalyzeGaps` synthesizes the
   common gaps from five deterministically-computed scalars per company (never a
   raw page). Prose out, SD-7 clamp. Cached in `category_gap_analysis`, keyed by
   `(region, category, LeadgenGapVersion, company_set_hash)`.
4. **Outreach email** — `internal/refine.DraftEmail`, one per company, fed the
   company's facts + its category's gap analysis. Cached in `outreach_emails`
   with a `status` (draft/sent/skipped) a region re-run respects — enforced in
   SQL, not just the caller.

`Pipeline.Run(ctx, RunRequest)` runs 1–2 always and 3–4 on request
(`WithEmails` implies `WithGapAnalysis`), with the two model phases as bounded
`errgroup` fan-outs limited to `MaxConcurrentRefines`. It returns an error only
for `ErrNoData` (no source produced a list) or a cancelled context; every other
problem is a `Report.Notes` string.

### 7.7 Project memory (`internal/sessionlog` + `internal/memory`)

**Why.** Every new session rediscovers the same repository: the same `AGENTS.md`
files, the same package layout, the same decisions. That work is already
recorded — Claude Code writes a JSONL transcript of every session under
`~/.claude/projects/<slug>/`, and the runner writes one per coding run — but it
is not reachable. This turns those transcripts into something a session can be
told.

**Flow.**

```
~/.claude/projects/<slug>/*.jsonl ─┐
                                   ├─→ internal/sessionlog   (inert: parse + segment + score)
cfg.TranscriptDir/<run_id>.jsonl ──┘             │ Episode
                                                 ↓
                                        internal/memory
                                   phase 1: store facts        (free, always completes)
                                   phase 2: refine.Recap       (bounded, per episode)
                                                 ↓
                              internal/store  (memory_* tables + FTS5)
                                                 ↓
                      project_context · context_recall · context_remember
```

**The shape is a reaction to a real failure.** goat v1 kept one project-memory
document and had a model rewrite it each pass. A rewrite degenerated and *became*
the memory — that file is still on disk in the v1 repo, still unreadable, and a
slice of it is checked in as `internal/refine/testdata/degenerate_recap.txt`.

So: a model only ever sees one episode and only ever writes a couple of lines;
its output is checked (`validateRecap` plus `clampOutput`'s "must not exceed its
input") before it is believed; a rejection costs the recap and nothing else; and
the brief is assembled from rows at read time, so there is no document to rot.

**Two phases, and the split is the point.** Phase 1 — parse and store — costs
nothing but I/O and always runs to completion, so search and the timeline work
from the first pass, before a single model call. Phase 2 spends at most
`MemoryIngestBatch` calls and yields, so an interrupted backfill loses one batch
rather than a session's worth of work.

**Who runs it.** `goat-daemon` runs `Memory.Run` for its lifetime — catching up
first, then ticking on `MemoryIngestInterval` — and re-lists projects each pass
so one registered after start-up is picked up without a restart. `goat-mcp` is a
short-lived stdio process with no daemon behind it, so its memory tools fold a
small `MemoryLazyCatchup` ingest into each read; phase 1 being free is what keeps
that honest.

**Two boundaries it must not cross.** Responses are scrubbed of the signatures
`internal/mcp/finalize.go` fails closed on — one scraping transcript would
otherwise take the memory permanently dark — and fitted to their ceiling before
they leave, because the choke-point rejects an over-budget response rather than
truncating it. Paths are resolved by `internal/project.Canonicalize` and never
registered: reading a project's history does not mint the permission a coding run
needs.

### 7.5 Desktop app (`desktop/`)

Tauri (Rust shell) + React + shadcn/ui + Tailwind, macOS/ARM64. The Rust shell
binds `127.0.0.1:0`, reads and drops the port, mints a 32-byte per-launch bearer
token, and spawns `goat-daemon` with exactly two env vars
(`GOAT_DAEMON_PORT`, `GOAT_DAEMON_TOKEN`). The WebView never parses subprocess
output. **REST goes through Rust** (`daemon_request`), because a cross-origin
`fetch` carrying `Authorization` is preflighted and the daemon answers `OPTIONS`
with 401 by design — so the token stays out of the WebView for every REST path.
The one exception is the run WebSocket, whose handshake is preflight-exempt and
which carries the token as `Sec-WebSocket-Protocol: goat.bearer.<token>`. Three
screens: `Connection` (the handshake), `Workspace` (coding-task runner + live
run view), `Leadgen` (the Maps pipeline).

### 7.6 The operator-provisioned credential

`GOAT_GOOGLE_PLACES_API_KEY` is the single exception to `docs/SECURITY.md`'s
"zero API keys configured by GOAT itself" stance — narrow, documented, and read
once at startup in `config.Load`. It decides *whether* the Places provider is
reachable, never what the process does with a request. Empty is a normal
install: `maps_search` and the `/maps/*` routes are simply not offered.
