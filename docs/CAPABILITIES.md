# CAPABILITIES.md — what Mimir can do today

Türkçe: [`CAPABILITIES.tr.md`](CAPABILITIES.tr.md).

Every milestone across both tracks is shipped. This document is the complete,
current inventory of what the built system does, what each feature needs to run,
and where the code lives. For *why* it is built this way see
[`ARCHITECTURE.md`](ARCHITECTURE.md); for the plan and its history see
[`ROADMAP.md`](ROADMAP.md).

---

## 1. The two binaries

| Binary | Transport | Lifetime owner | Purpose |
|---|---|---|---|
| `bin/mimir-mcp` | stdio MCP | the Claude Code session that registered it | Give a Claude Code session web search, page scraping, refined research, and free no-login scrapers. |
| `bin/mimir-daemon` | loopback HTTP (`127.0.0.1` + per-launch bearer token) | the Tauri desktop app (spawns it as a sidecar) | Folder-scoped coding-task runner with live streaming, the Google Maps lead-gen pipeline, and the **same** MCP tools re-exposed at `/mcp`. |

Both import the same runtime packages — one engine, two transports. Nothing in
`mimir-mcp` writes to stdout except the MCP transport; the daemon never prints its
port (the parent chose it).

---

## 2. MCP tools (`bin/mimir-mcp`, also at `mimir-daemon` `/mcp`)

All responses are **compact and refined** — raw scraped text can never leave a
tool (enforced at a single choke-point, `internal/mcp/finalize.go`). Token
ceilings below are approximate.

### Always available

| Tool | Input | Output | Ceiling | Needs |
|---|---|---|---|---|
| `web_search` | `query: string`, `count?: int` (≤30, default 8) | `results: [{title, url, snippet}]` | 30 results, metadata only | Network (DuckDuckGo) |
| `fetch_page` | `url: string` | `{ url, title, refined: true, markdown }` | ~1500 tokens | Crawl4AI Docker + `claude` CLI |
| `research` | `query: string`, `depth?: int` | `{ summary, key_points[], sources[{n,title,url}], gaps[], refined: true }` | ~2000 tokens | DuckDuckGo + Crawl4AI + `claude` CLI |
| `diagnostics` | none | `{ crawl4ai, claude, duckduckgo, maps_scraper{…,optional}, versions }` | small, fixed | none (it *is* the health check) |

### Project memory (M8)

What earlier sessions in a repository already established, so the next one is
told instead of rediscovering it. Distilled by the pinned haiku model, one
episode at a time, from Claude Code's own session transcripts and this daemon's
coding runs. Registered only when the local store opened — a memory with nowhere
to remember is none, and a tool whose only answer is "there is no memory" would
spend part of every session's context advertising a dead end.

| Tool | Input | Output | Ceiling | Needs |
|---|---|---|---|---|
| `project_context` | `project_path?: string` (defaults to cwd) | `{ project, repo{summary,top_level[],rule_files[]}, pinned_notes[], recent_work[], hot_files[], coverage, guidance, refined: true }` | ~1400 tokens | store; `claude` CLI to distil (searchable without it) |
| `context_recall` | `query: string`, `limit?: int` (≤20), `project_path?: string` | `{ query, hits[{at,title,summary,files[]}], notes[], guidance, refined: true }` | ~1100 tokens | store |
| `context_remember` | `text: string`, `kind: decision\|convention\|trap\|todo`, `project_path?: string` | `{ stored{at,kind,text}, metadata_only: true }` | ~400 tokens | store |

### The knowledge base (task-41)

Where the project memory answers "what happened in this checkout", these answer
"what do we know about this thing" — across projects, and across every model
that writes into it. Availability follows the store, exactly as above.

| Tool | Input | Output | Ceiling | Needs |
|---|---|---|---|---|
| `brain_ingest_data` | `source: string`, `content: string`, `kind?: note\|research\|decision\|session\|file\|commit`, `project_path?: string` | `{ node{id,kind,source,title,assessment,tags[],neighbors[]}, distilled, note?, linked, refined: true }` | ~1400 tokens | store; a distil provider (stored without an assessment if none answers) |
| `brain_ingest_github` | `repo: string` (owner/name or URL) | same as above, stored globally | ~1400 tokens | store; `api.github.com`; `MIMIR_GITHUB_TOKEN` for private repos |
| `brain_query_nodes` | `query: string`, `limit?: int` (≤20), `project_path?: string` | `{ query, nodes[…], refined: true }` | ~1400 tokens | store |
| `brain_related` | `node_id: string`, `limit?: int` (≤40) | `{ node{…,neighbors[]}, refined: true }` | ~1400 tokens | store |
| `brain_scan_repo` | `project_path?: string`, `limit?: int` (≤50), `dry_run?: bool` | `{ scanned, skipped_unchanged, failed, remaining, eligible_total, files[], metadata_only: true }` | ~1400 tokens | store; a distil provider |

**What records itself.** The daemon promotes distilled memory episodes into
`session` nodes plus a `file` node per path they touched, drains agy sessions
from a hook spool, and turns new commits into `commit` nodes — all on a five
minute tick and **without a model call**. `brain_scan_repo` is the opposite: the
one deliberately expensive operation, a distil per file, bounded to a batch per
call and hash-skipped so a second pass over an unchanged repository is free.
Run it with `dry_run` first to see the size of the bill.

Identity is `(project_path, kind, source_key)`, so re-ingesting one source
updates a row rather than minting another. A node's `aliases` — synonyms and
adjacent terms the distil writes into the FTS index — are what let a search
match a node whose text does not contain the query's words; there is no vector
index. Nodes with an empty `project_path` are global and visible from every
project.

`context_recall` returns **pointers, not file contents** — the titles, dates and
file paths an answer lives in. Re-reading the named files is cheap; rediscovering
*which* files they are is what costs a context window.

Every path goes through `internal/project.Canonicalize` (symlinks resolved,
denylist applied). These tools deliberately never call `Register`: reading a
project's history is not grounds for minting the registration a coding run needs.

### Stage F — free, self-written, no-login scrapers

Deterministic HTML/JSON extraction (`internal/extract`); **no refine call**, so
they spend zero Claude tokens. Small structured facts, no prose.

| Tool | Input | Output | Ceiling |
|---|---|---|---|
| `ecommerce_product_lookup` | one product URL | name, price, currency, availability, rating, image | 300–400 tokens |
| `tiktok_profile_lookup` | a handle / profile URL | display name, follower/following/like counts, bio, verified | 300–400 tokens |
| `gmaps_business_lookup` | one Google Maps business URL | name, address, phone, rating, review count, hours, category — **one named business** | 300–400 tokens |
| `instagram_profile_lookup` | a handle / profile URL | display name, follower/following/post counts, bio, verified | 300–400 tokens |

Parked (not built): `linkedin_company_lookup` and the paid-provider tools in
`ROADMAP.md` §A.3–§A.4.

### Behind the operator-provisioned Places key

| Tool | Input | Output | Ceiling |
|---|---|---|---|
| `maps_search` | `query: string`, `count?: int` (≤60), `language_code?`, `region_code?`, `near?: {lat,lng,radius_meters}` | `{ query, returned, total_found, truncated, companies[{place_id,name,address,…}] }` | ~2000 tokens; **billed** |

`maps_search` is registered **only** when `MIMIR_GOOGLE_PLACES_API_KEY` is set. A
keyless install is the normal install — the tool is simply absent, not a
dead-end stub. This enumerates **every** business in a region (billed Places
API), which is different from `gmaps_business_lookup` (one free fact).

---

## 3. The daemon HTTP surface (`bin/mimir-daemon`)

Every route sits behind the same chain: panic-recover → request log → loopback
guard → bearer-token check → body-size cap. REST is meant to be called from the
Tauri Rust shell (the daemon sends no CORS headers by design); the run
WebSocket's handshake is preflight-exempt and carries the token as a
subprotocol.

| Route | Does |
|---|---|
| `GET /healthz` | `{ ok, version, uptime_ms }` |
| `GET /diagnostics` | daemon health + store status + project count + `places_configured` + coding-run stats (total / running / failed / summed `cost_usd`) + the MCP `diagnostics` payload |
| `GET /projects` | list registered project folders |
| `POST /projects` | `{ path }` → canonicalize (`Abs`+`EvalSymlinks`), reject `/`, `$HOME`, denylisted roots, must be an existing dir → returns an opaque `project_id`. **No default project ever.** |
| `POST /coding-tasks` | `{ project_id, prompt }` → starts a folder-scoped streaming `claude` session, returns a `run_id` |
| `GET /coding-tasks/{id}` | run metadata / status / cost |
| `GET /ws/runs/{id}` | WebSocket: replays the run's JSONL transcript, then follows the live event bus — `RunStarted`, `TextDelta`, `ReasoningDelta`, `ToolCall`, `ToolResult`, `RunCompleted`, `RunFailed`, `rate_limit`; stitched on `Event.Seq` so a lossy bus never shows a hole |
| `POST /maps/leadgen` | `{ query, region, count, language_code, region_code, near, gap_analysis, emails }` → runs the lead-gen pipeline (§5); registered only with a Places key |
| `POST /maps/emails/status` | `{ place_id, status }` where status ∈ draft / sent / skipped — SQL blocks regeneration of a `sent`/`skipped` draft on a region re-run; registered only with a Places key |
| `/mcp`, `/mcp/` | the full MCP tool set over StreamableHTTP — same registry, same choke-point as stdio |

---

## 4. Folder-scoped coding-task runner

**What you can do:** pick a project folder, then have `claude` complete a coding
task inside it while you watch its full thought/action stream live,
second-by-second.

- **Scoping is hard.** `internal/coderunner` sets `cmd.Dir = project.Path` *and*
  passes `--add-dir <project.Path>`, plus a fixed `--permission-mode` constant.
  The runner can only see the folder you picked.
- **It reaches Mimir the way any other session does.** Through the client's own
  MCP registration, not a nested `--mcp-config` — that was planned in
  `docs/ROADMAP.md` §B.2.1, parked in `task-35`'s *Out of scope*, and this
  document previously claimed it as shipped. It is not, and the runner passes no
  such flag.
- **Live streaming.** stdout is parsed line-by-line (`stream-json`), cumulative
  text/thinking length tracked **per `message.id`**, and only deltas are emitted
  as typed events — published on an in-process bus *and* appended to a JSONL
  transcript indexed by a `coding_runs` row.
- **Graceful shutdown.** On SIGTERM the daemon drains in-flight runs before
  exiting.

Needs: the `claude` CLI on `$PATH` and logged in.

---

## 5. Google Maps lead-gen pipeline

`internal/leadgen`, threaded end-to-end by `place_id`. Four stages, cost rising
left to right; every stage is cache-first and **degrades rather than fails** —
`Pipeline.Run` returns an error only for `ErrNoData` (no source produced a list)
or a cancelled context. Everything else is a `Report.Notes` string.

| # | Stage | LLM? | What it produces | Cache table |
|---|---|---|---|---|
| 1 | **Region search** | no | Every business in a region. Primary: Places API Text Search (`internal/maps`). Fallback: a Playwright docker sidecar with deterministic DOM extraction (`internal/mapscrape`), ids namespaced so a scraped row can never overwrite a billed one. | `companies`, `region_searches` (all-or-nothing on read) |
| 2 | **Categorize** | mostly no | A normalized `Category` per company. A static Google-`types[]` → category table answers the common case for free; `claude` classifies only the ambiguous residue, in batches, against a closed vocabulary (no prose out). | `company_categorization` (keyed by `LeadgenCategoryVersion`) |
| 3 | **Per-category gap analysis** | yes | The common gaps/needs across a category, synthesized from five deterministically-computed scalars per company (never a raw page). SD-7 hard token ceiling. | `category_gap_analysis` (keyed by `region, category, LeadgenGapVersion, company_set_hash`) |
| 4 | **Outreach email** | yes | One drafted marketing email per company, fed that company's facts + its category's stage-3 gap analysis. | `outreach_emails` (keyed by `place_id, prompt_version`, with a `status` the region re-run respects) |

`Pipeline.Run` always does stages 1–2. Stage 3 runs when `gap_analysis: true`;
stage 4 when `emails: true` (which implies gap analysis). The two model phases
are bounded `errgroup` fan-outs limited to `MaxConcurrentRefines`.

Because every stage writes a cache keyed by a `prompt_version` constant, a cache
hit means **no API call, no `claude` subprocess, zero tokens**. Re-running a
region only spends tokens on companies/categories that are genuinely new or
whose `prompt_version` was deliberately bumped.

Needs: `MIMIR_GOOGLE_PLACES_API_KEY` for the primary path; `make maps-up` (the
Playwright sidecar) only for the fallback; the `claude` CLI for stages 3–4.

---

## 6. The desktop app (`desktop/`)

Tauri (Rust shell) + React + shadcn/ui + Tailwind, macOS / Apple Silicon. The
Rust shell picks a free loopback port, mints a 32-byte per-launch bearer token,
spawns `mimir-daemon` with exactly those two env vars, and reaps it on quit. The
WebView never parses subprocess output; REST goes through Rust (`daemon_request`)
so the token never enters the WebView.

Seven screens:

| Screen | What you do there |
|---|---|
| **Connection** | The daemon handshake — confirms the sidecar is up, authenticated, and healthy. |
| **Workspace** | Native folder picker (`NSOpenPanel`) → register a project → type a coding-task prompt → watch the run stream in a live "terminal": text/reasoning deltas append, tool calls/results render as collapsible cards with a risk badge. |
| **Leadgen** | The Maps pipeline: a region search form, per-category gap-analysis cards, and a company list with an expandable draft email and mark-sent / mark-skip buttons wired to `POST /maps/emails/status`. `report.notes` is shown verbatim. |

Packaging: `tauri.conf.json` builds `app` + `dmg` with a hardened runtime and
`entitlements.plist`. Signing/notarization are env-driven at build time
(`APPLE_SIGNING_IDENTITY` + Apple-ID / API-key creds); a keyless build still
produces an ad-hoc app.

---

## 7. Local persistence (`internal/store`)

SQLite via `modernc.org/sqlite` (pure Go, no CGO), WAL mode + `busy_timeout` so
both binaries share one DB file. Migrations are embedded and append-only. It is a
**cache and a local record**, never a source of truth the MCP consumer sees.

| Table | Holds | Invalidation |
|---|---|---|
| `crawl_pages` | raw crawl cache (markdown + HTML), keyed by `sha256(url)` | `config.PageCacheTTL` |
| `refined_pages` | refine cache (refined text + token estimate) | same TTL; `RefinePromptVersion` bump |
| `projects` | canonicalized folder path for the coding-task runner | re-pick to change |
| `coding_runs` | run metadata, cost, session id, transcript pointer | none (history) |
| `companies` | normalized Places/scrape result, keyed by `place_id` | caller-supplied long TTL (~30d) |
| `region_searches` | the ordered `place_id` list a region search returned | same TTL; all-or-nothing on read |
| `company_categorization` | normalized category + which tier answered | `LeadgenCategoryVersion` bump |
| `category_gap_analysis` | Claude-synthesized gaps/needs per category | `LeadgenGapVersion` bump; a changed company set misses |
| `outreach_emails` | drafted email + `status` (draft/sent/skipped) | `LeadgenEmailVersion` bump; "sent"/"skipped" blocks regeneration (SQL-enforced) |
| `memory_episodes` | one distilled iteration: deterministic facts always, a short recap when one was accepted | `MemoryPromptVersion` bump re-derives recaps; facts survive |
| `memory_notes` | facts pinned through `context_remember` | none — never rewritten by an ingest |
| `memory_ingest_state` | how far each transcript has been parsed | reset when a transcript shrinks (replaced, not appended) |
| `memory_fts` | FTS5 index over episode titles, summaries, files and commands | kept in sync by triggers |

Everything that spends Claude Code tokens goes through the one `claude -p`
headless subprocess in `internal/refine` — `Distil` for pages, and since M8
`Recap` for one episode of project memory at a time. Every call site checks the
store first, and every ceiling is a `config` constant.

The memory is the one that spends in order to *save*: a bounded one-off cost per
episode, against the repeated cost of a session rediscovering the same repository
from scratch.

---

## 8. What it needs to run

| Feature | Docker | `claude` CLI | Places key | Node/Rust toolchain |
|---|---|---|---|---|
| `web_search` | — | — | — | — |
| `fetch_page`, `research` | Crawl4AI (`make crawl-up`) | yes | — | — |
| `diagnostics` | — | — | — | — |
| Stage F scrapers | — | — | — | — |
| `maps_search` | — | — | **yes** (`MIMIR_GOOGLE_PLACES_API_KEY`) | — |
| `project_context`, `context_recall`, `context_remember` | — | to distil (searchable without) | — | — |
| Coding-task runner + live stream | — | yes | — | — |
| Lead-gen (primary path) | — | yes (stages 3–4) | **yes** | — |
| Lead-gen (scrape fallback) | Playwright sidecar (`make maps-up`) | yes (stages 3–4) | — | — |
| Desktop app | (as per the feature used) | yes | for the Leadgen screen | yes (`make desktop-dev`) |

Refinement rides your existing `claude login` — **no Anthropic API key**, no
other LLM provider. The pinned refine model is `claude-haiku-4-5-20251001`
(`internal/config`).

Full setup and troubleshooting: [`INSTALL.md`](INSTALL.md).

---

## 9. Verification status

As of the last full run on a clean checkout:

- `make check` (build + `go vet` + lint + unit tests + `-race`) — **green**
- `make e2e` (end-to-end MCP smoke test vs. mock services) — **pass**
- `desktop/` TypeScript: `tsc --noEmit` clean, `vitest` 22/22 pass
- `desktop/` Rust (`cargo fmt`/`clippy`/`test`) — runs via `make desktop-check`;
  needs a local Rust toolchain
- Integration tests (real Docker / `claude` CLI / Places API) live behind
  `//go:build integration` and are **not** part of `make check`
