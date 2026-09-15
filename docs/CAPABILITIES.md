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

**A resident scan.** `mimir-daemon` keeps one sweep running for as long as it
lives: every project under `~/development` and `~/Documents`, read to completion
through `agy`, then an idle interval and around again — so there is always an
agy working and a file written this afternoon is in Brain tonight. PDFs are read
through poppler's `pdftotext` when it is installed and skipped without
complaint when it is not. A provider that stops answering backs the sweep off
(one minute, doubling, thirty at most) rather than spawning a thousand failed
subprocesses an hour, and a file that could not be distilled keeps no content
hash, so it is retried rather than skipped forever. The desktop's **Brain** tab
shows it — `GET /brain/scan`, with pause / resume / scan-now — alongside a
force-directed picture of the nodes and their edges (`GET /brain/graph`,
`/brain/projects`, `/brain/nodes/{id}`).

**The structural layer, when it is there.** Every node above is the result of a
model reading something. If [Graphify](https://github.com/Graphify-Labs/graphify)
is installed (`pip install graphifyy` — Apache-2.0, free), each sweep also runs
its tree-sitter pass over the same files the scan is allowed to read, and writes
what it finds — functions, classes, and the `calls`, `imports`, `defines`,
`inherits` and `uses` edges between them — **without a single model call**. The
symbols hang off the `file` nodes the scan already made, so "what is this file
about" and "what does it contain" are one graph. On a machine without Graphify
nothing changes and nothing is reported: the layer is offered, not required.
`GET`/`PUT /brain/structural` is the switch and the "is it installed" answer;
the Brain tab shows both. It is off in the picture by default (`?kinds=all`
turns it on): symbols outnumber and out-connect everything else, so a graph
ranked by degree would become nothing but symbols.

One limit worth knowing, measured rather than assumed: in Go, calls through a
receiver resolve poorly. Over 300 files of this repository the parser produced
3154 call edges into 2359 functions but only 325 into 738 methods, so
`c.relate(...)` shows no caller while `writeJSON(...)` shows all 47 of them.
**Absence of a caller is not evidence there is none.** Package-level functions,
imports and containment are reliable; methods are partial.

**Moving and forgetting.** A repository that is renamed or moved leaves its old
path behind as a separate project, and until now nothing could remove it — the
capture loop went on re-recording it from transcripts that still named the old
directory. `POST /brain/projects/{id}/move` re-files everything under a new path
(every node id is derived from it, so the ids and every edge are rewritten in one
transaction, merging what the destination already knew), and
`DELETE /brain/projects/{id}` forgets a project entirely. Both are on the Brain
tab's configuration half, where a project whose folder is gone is marked as such.
The transcripts on disk are never touched.

**A whole machine at once.** `bin/mimir-scan ~/development` (`make scan`) is the
same scan without a session in the middle: it finds every project under a root —
git checkouts, and the directories that never became one but hold files of their
own — and runs each to completion, logging progress to stderr. One
`gemini-3.8-flash-high` call per file through `agy`; `-n` reports the bill and
spends nothing. A node that could not be distilled keeps no content hash, so a
provider that was down for part of a run is retried by the next one rather than
skipped forever.

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

### The catalog (task-87)

Two reads, and deliberately nothing that writes. Queuing a bulk rewrite is a
route (`POST /catalog/rewrite`) rather than a tool: it would be the one tool in
this registry whose entire effect is to spend money — a catalog's worth of
searches, crawls and reason calls — from a sentence somebody typed. The route
takes an explicit list of products for that reason, and it sits on a screen
where the operator has just looked at them.

| Tool | Input | Output | Ceiling | Needs |
|---|---|---|---|---|
| `catalog_products` | `import_id?: string`, `status?: enum`, `category?: string`, `limit?: int` (≤ page max) | `{ imports[] \| products[] { id, title, sku, category, status, has_draft }, total, metadata_only: true }` | ~1200 tokens | store |
| `catalog_product` | `product_id: string` | `{ id, status, current, draft?, notes[], refined: true }` — the listing as prose, never the store's HTML | ~1500 tokens | store |

Both resolve the draft version themselves. It is a cache key the daemon
composes from the prompt constant, the model, the brand hash, the skill version
and — for a target language — the language and the reviewer's prompt constant.
A caller that could name one could serve itself copy written under a brand voice
that no longer exists, with no way of knowing it had. The composition lives in
one place (`Studio.CurrentDraftVersion`) because it used to live in three and
they drifted: the HTTP layer passed an empty skill version long after the skill
shipped, so every draft a bulk rewrite paid for was written under a key no read
looked up.

#### Languages (task-103, task-105, task-107)

A catalog can carry more than one language, and which ones is a property of the
operator's own export rather than of this binary: the real IKAS custom-fields
export has `Html:Detay` and `Html:Detay-AR` side by side, and a store whose file
has no Arabic column is not offered an Arabic pass — there would be nowhere to
write the answer.

One card is one language. English is written in plain American English; Arabic
in Modern Standard Arabic (الفصحى) as written for Gulf e-commerce, with Arabic
punctuation, Western digits, no tatweel and no tashkeel, and brand names, model
numbers and units left in Latin script.

Two things stand between a target-language draft and the storefront, and they
are different in kind:

- **A deterministic gate** (`internal/catalog/lint.go`), which makes no model
  call. It repairs what is fixable without judgement — tatweel, bidi control
  characters, Arabic-Indic digits, an ASCII comma inside an Arabic sentence —
  and says so rather than doing it silently. It refuses what is decidable: copy
  that is not in the language it claims to be, source-language letters left in,
  a long untranslated Latin span, and **a number the source never stated**. That
  last one is the cheapest anti-hallucination check available, and it is why the
  check is given the source as well as the draft.
- **A native-reviewer pass** (`internal/catalog/review.go`), the first verifier
  pass in this repository. A second `llm.Reason` call in the role of an editor
  who reads the target language natively, shown the source, the draft and what
  the deterministic gate already found — a reviewer told "the number 500 is not
  in the source" fixes that, while one asked to look for problems in general
  finds a different one each time. Its output goes back through the gate.

The gate is the floor and it does not degrade: a draft it refused is not stored
because the reviewer could not be reached. It runs on the rewrite path and on
the operator's edit path both, so there is no way to store a draft that skipped
it — though on the operator's path its findings are notes rather than a refusal,
because a person's Arabic is not ours to reject over a ratio heuristic.

An Arabic body ships wrapped in one `<div dir="rtl" lang="ar">`. Without a
paragraph direction the bidirectional algorithm resolves punctuation and numbers
against nothing, and `50 مل، يدوم` renders in the wrong order — and `Render`
would have dropped a `dir` attribute silently, because a Turkish store's own
HTML has never contained one.

#### A decision per language (task-113)

Approval is per language, because an export is. Approving the Turkish copy does
not ship an Arabic one nobody read, and approving the Arabic does not re-open
the Turkish; `Studio.Export` resolves a version per language the file carries
and collects each language's own approvals. Until this existed a target-language
draft could be written, reviewed and approved and still had no way of reaching
the file — the studio only ever resolved the source language's version.

The source language's decision stays in `catalog_products.status` and a target
language's lives in `catalog_product_langs`. That asymmetry is the same argument
`DraftVersion` makes about the draft key: the language is a suffix, and only for
a target. Every row already stored is a decision about the file's own language,
so moving them would have meant either a backfill that leaves two homes for one
fact, or rewriting the one `GROUP BY` the catalogs list is built on.

**Absence is pending.** A product nobody has judged in Arabic has no row there,
so every read is a `LEFT JOIN` and every count of what is waiting is a count of
rows that are not there. Per-language counts appear on `GET /catalog/imports`
as `counts_by_lang`, and a language shows up only once something has been
decided in it: that listing does not read file bodies, so it cannot know which
languages a file carries, and drawing an Arabic chip over a Shopify export with
no Arabic column would state the opposite of the gate above.

#### The outputs listing (task-114)

`GET /catalog/outputs` is every generated draft across every import, filterable
by language, by platform profile and by status. It is a different question from
`GET /catalog/products`, which is always about one file — and filtering by
profile only means anything from above all of them.

A draft is current for exactly one (import, language) pair, and the version
string cannot be read backwards. So the studio composes the keys — one per
import per language, in memory, no I/O — and the store joins against them as an
inline table. The constraint is on the **pair**: one version can be current for
one import and superseded for another, because two imports share a brand hash
until one of their voices is edited, and a filter written as `version IN (…)`
would surface the stale one as current.

Two store reads, whatever the catalogue looks like: the imports, then the
drafts. Which fields a draft changed is computed on the daemon, against the cell
that draft would be written into — for a target language that is that language's
own cell, so an Arabic draft equal to the Turkish one is a change. A client that
diffed for itself would need every product's original HTML shipped alongside,
and would get the direction of that comparison backwards.


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
| `GET /accounts` | the connected Claude account, or an empty list — there is at most one |
| `POST /accounts/login` · `GET /accounts/login` | start the login and follow it. The daemon runs `claude auth login` against its own credential slot and opens the authorization page in a private Chrome window; the GET reports `opening` / `waiting` / `code` / `done` / `failed` |
| `POST /accounts/login/code` | `{ code }` → the paste-a-code fallback, for when the CLI could not open a browser itself |
| `POST /accounts/reset` | sign out, remove the slot, forget the row. The "çıkış yap" button — the only thing that disconnects, and how you switch accounts |
| `GET /accounts/{id}/status` | live `claude auth status` — who is signed in, and on what plan. Costs nothing |
| `POST /coding-tasks` | `{ project_id, prompt, account_id? }` → starts a folder-scoped streaming `claude` session, returns a `run_id` |
| `GET /coding-tasks/{id}` | run metadata / status / cost |
| `PATCH /coding-tasks/{id}` | `{ title?, prompt?, model?, attachment_ids? }` → rewrites what a card asks for. A patch: an absent field is untouched. Allowed while the card has spent nothing — `backlog`, `queued`, `failed`, `stopped` — and a 409 otherwise, because the prompt of a running or finished run is the record of what was asked. Images may be attached long after the card was written; one dropped by an edit is reclaimed there and then |
| `POST /coding-tasks/queue/kick` | asks the dispatcher to look at the queue again. The queue is pumped when work is released and when a run frees its slot, never when the *account* changes — so a card queued while nothing was connected would wait forever. 409 with the reason when there is still no identity to spend, or when the token budget is spent and the queue is waiting for its window |
| `GET /coding-tasks/queue/limits` | `?limit=` → why the queue is not moving and when it will be: `holds` is the credential slot the dispatcher is waiting on right now with its reset time, `log` is the durable record — `run` (a task the budget cut off, put back in the queue), `dispatch` (a queued task that could not be claimed), `resumed` (the window rolled over and the queue restarted itself) |
| `POST /coding-tasks/{id}/retry` | `{ fresh? }` → puts a `failed` or `stopped` run back in the queue. Omitted or `false` keeps the row's `session_id`, so the dispatcher re-launches the CLI with `--resume` and the session carries on from where it broke; `true` drops the session and does the task over. Any other state is a 409 |
| `GET /ws/runs/{id}` | WebSocket: replays the run's JSONL transcript, then follows the live event bus — `RunStarted`, `TextDelta`, `ReasoningDelta`, `ToolCall`, `ToolResult`, `RunCompleted`, `RunFailed`, `rate_limit`; stitched on `Event.Seq` so a lossy bus never shows a hole |
| `POST /maps/leadgen` | `{ query, region, count, language_code, region_code, near, gap_analysis, emails, provider?, model? }` → runs the lead-gen pipeline (§5). **No Google credential needed** — the free scrape is the primary source. `provider`/`model` override which tier the model stages spend; omit both and the operator's saved default (`PUT /settings`) answers, or class routing if they saved none |
| `POST /maps/leadgen/export` | the same body plus `{ enrich, dir }` → writes an `.xlsx` (summary sheet + one per category) and returns its path and counts |
| `GET /llm/providers` | the provider/model allow-list the `provider`/`model` fields are checked against, plus what a run gets when it sends neither. Answers on any daemon — the desktop picker is a view of this, not a copy of it |
| `POST /maps/outreach` | `{ place_ids, channels, region?, provider?, model? }` → writes to the companies the operator *picked*: one draft per channel (`email`, `whatsapp`). It takes ids, not a filter — a filter is the one thing that would let a short string spend a region's worth of tokens. The ids are resolved against the ledger and capped at `LeadsPageMax` |
| `POST /maps/outreach/status` | `{ place_id, channel, status }` where status ∈ draft / sent / skipped — SQL blocks regeneration of a `sent`/`skipped` draft on a region re-run. The decision is per channel: sending the email and skipping the WhatsApp line is an ordinary thing to decide |
| `GET`/`PUT /settings` | The operator's own configuration: the default `provider`/`model` lead-gen's model stages spend, and each channel's rule file. `PUT` runs the pair through the same allow-list — both strings become argv to a subprocess |
| `PUT /settings/rules` | `{ channel, body }` → replaces one channel's rule file. An empty body is a reset, not an empty prompt |
| `POST /settings/rules/reset` | `{ channel }` → puts the shipped default back. A route, so no client has to hold a copy of the default |
| `GET /maps/leads` | the lead ledger: every business a run has ever returned, with its category and the status of its outreach draft. Filters: `category`, `run_id`, `q`, `without_website`, `limit`, `offset`. Costs nothing and searches nothing |
| `GET /maps/leads/categories` | the category rail, counted over the whole ledger under the same filter |
| `GET /maps/leads/runs` | the run history: which search found what, and when |
| `GET /chat/sessions` · `GET /chat/sessions/{id}` | the verbatim conversation archive — Claude Code sessions, this daemon's own coding runs, and agy conversations, turn by turn |
| `GET /chat/search` | full-text over what was actually said, scoped by project id |
| `GET /brain/nodes/{id}/versions` | a node's history: one entry per distinct content hash the source has carried, with the assessment that version produced |
| `GET`/`PUT /brain/structural` | the structural layer: whether Graphify may be used, whether it is installed, and the command that would install it |
| `POST /brain/projects/{id}/move`, `DELETE /brain/projects/{id}` | move a project's whole history onto a new path, or forget it — the only two writes in the system that remove |
| `POST /catalog/imports` | `{ filename, data_base64 }` → reads a Shopify or IKAS product export (products or custom fields): sniffs BOM, encoding (UTF-8 / Windows-1254), delimiter (`,` / `;`) and line ending, groups variant rows into products (Shopify by handle, IKAS by product group id), and derives the brand kit. Base64 rather than multipart for the same reason attachments are — the desktop reaches the daemon through a Rust proxy that forwards a string body — and it earns something extra here: the bytes arrive undisturbed, so the encoding sniff sees what the exporter actually wrote. This is the one route whose payload is a document, so it has its own cap (`CatalogCSVMaxBytes`) rather than a raised cap for the whole surface |
| `GET /catalog/imports` · `GET`/`DELETE /catalog/imports/{id}` | list, read, forget. Each row of the listing carries the profile that matched and `counts` — products by status — because "which file do I open" is answered by what state a file is in, not by its name; the counts are one `GROUP BY` for the whole list rather than a read per row, and a store that cannot answer leaves the summary off instead of failing the screen. The listing still leaves the file body out: a menu that carried every CSV would cost as much to open as opening every import at once, which is why the framing stays on the file's own screen |
| `PUT /catalog/imports/{id}/mapping` | `{ mapping }` → the operator's own column map, for a header no platform profile matched — or a correction to one that did. The file is re-read and the brand kit re-derived under it, because before the mapping there was no title column and therefore no titles. A map naming neither a title nor a description is refused with a sentence rather than accepted into an import full of blank products. The import view carries `readable` (a platform matched **or** the mapping names enough — this is what a screen gates on, not `dialect`), the saved `mapping`, a deterministic `suggested` map when nothing matched, and a `sample` value per column from the first data row |
| `GET /catalog/profiles` | the platform profiles this binary ships, with the columns each reads. It exists because a client that kept its own copy of a closed set that lives in Go went stale: the desktop still offered a profile task-91 deleted, and would have missed every profile added since |
| `PUT /catalog/imports/{id}/fields` | `{ fields }` → which fields a rewrite may change, per language, as the operator configured them. It exists because a product export has no fixed field set: one real IKAS export here carries a store-named sales-channel column, an empty SKU and a custom Arabic body, and the next store's carries none of those. So the switchable set is read off the file — a field with no column is not offered. It is a **gate, not a default**: a card that names fields still cannot write one the operator switched off, and the pass says in its notes what it skipped. An empty set means "not configured", which means everything, so a selection that normalises to empty is refused rather than stored — storing it would silently turn every switch back on |
| `PUT /catalog/imports/{id}/target-lang` | `{ lang }` → which language a translation export's target columns hold. IKAS's Çeviriler export has `İsim, Açıklama, …` beside `Çevrilecek İsim, Çevrilecek Açıklama, …` and records nothing about what "Çevrilecek" was translated into — the operator picked it in the admin panel when they pressed export. Detecting it from the content would be a language detector this system does not have, and detecting it wrong writes Arabic into the German column across every row |
| `PUT /catalog/imports/{id}/dialect` | `{ key }` → the profile the operator picked, overriding detection; an empty key returns the file to detection. Detection answers "which platform wrote this file", not "which platform is this store on", so a store that renamed a column produces a file the operator can see is an IKAS export while `Detect` cannot — and their only way out used to be retyping a map the profile table already holds. The profile binds to the file's **own** header spelling, because writing the table's spelling would export a column name the file never had and the admin panel would refuse it |
| `POST /catalog/imports/{id}/reread` | Rebuilds an import's products and brand kit under the dialect table as it stands now. A dialect profile is code: one added after a file was uploaded reads that file correctly, while the products written at upload time were built with no columns and are still blank. Explicit rather than a side effect of a read — a GET that rewrites a thousand rows is a GET nobody can reason about |
| `GET`/`PUT /catalog/imports/{id}/brand` | the brand kit: the **vocabulary** (which tags, classes and style properties this store's own descriptions use, counted — derived with no model call) and the **voice** (address, tone, recurring patterns, avoided clichés, lexicon — one distil call). `PUT` takes the voice and ignores anything else: the voice is the operator's writing, the vocabulary is what every rewrite is measured against |
| `POST /catalog/imports/{id}/brand/rescan` | re-derive both halves from the stored file |
| `GET /catalog/products` | `?import_id=&status=&category=&limit=&offset=` → a page of products in file order, each with its original content, its current draft, and the vocabulary any edit of it is held to. The draft version is resolved **on the server**: a client that could name a cache key could serve itself copy written under a brand voice that no longer exists, and would have no way of knowing |
| `GET /catalog/products/{id}` | one product, its draft, and its vocabulary |
| `PUT /catalog/products/{id}/draft` | `{ content, fields }` → the operator's own edit. Every field goes back through the same render-and-sanitize gate a machine rewrite does, and the response says what was simplified: the editor is a convenience, the server is the authority. The row is marked as hand-edited, and SQL then stops any later pass from overwriting it |
| `POST /catalog/products/{id}/status` | `?lang=` + `{ status, reason }` where status ∈ pending / researched / drafted / approved / rejected / failed. The decision is about that language's copy: approving the Arabic leaves the Turkish where the reviewer left it, and an absent `lang` means the file's own language — which is what every decision stored before languages existed is |
| `GET /catalog/outputs` | `?lang=&dialect=&status=&limit=&offset=` → every generated draft across every import, with the file it came from, the profile that file was read with, the language, that language's status, and which fields it actually changed. An absent `lang` means **every** language here and the file's own language everywhere else — the one place that asymmetry exists, because `""` is the source language and a real answer, so nothing is left over to spell "unset" with |
| `POST /catalog/imports/{id}/export` | writes the catalog back to `ExportDir` as CSV in the file's own framing, applying **only approved** drafts, **in every language the file carries** — each language's own version, each language's own approvals. A cell nobody approved a change for is copied, not re-derived — down to the quoting of each individual cell — so an import with nothing approved produces the file it was given, byte for byte |
| `POST /catalog/rewrite` | `{ import_id, product_ids[], fields[]?, research?, title?, lang?, provider?, model? }` → queues a bulk pass as a board card and returns its `run_id`. The `provider`/`model` pair is pinned to the card and read by the pass, so a settings change cannot move a pass mid-run and a re-run next week still spends what was picked; omitting both means the operator's saved choice, resolved when the pass starts. The pair goes through the same allow-list every per-run selection does, and the structured-output gate is asked about *it* rather than about the saved model. It takes the products you picked, **never a filter** — the rule `POST /maps/outreach` established, and it matters more here because a filter would spend a catalog's worth of searches, crawls and model calls from one short string. Capped at `CatalogProductsPageMax` per card. Registered only where a queue exists; every other `/catalog/*` route answers on a daemon with no runner |
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

**One Claude account, in a slot of Mimir's own, and you connect it once.** A
credential slot is a directory the CLI hashes into a keychain entry name
(`CLAUDE_SECURESTORAGE_CONFIG_DIR`); Mimir derives one beside its store and
signs into it itself, so it is never the operator's terminal login. Connecting
runs `claude auth login` on a pty and opens the authorization page in a private
Chrome window — private because a normal one carries whatever Claude session the
browser already has and would never ask which account is connecting. The slot
then survives quits and reboots: the keychain keeps the login, and a launch
reconciles the slot against it (`Restore`) rather than signing it out. A login
that has lapsed or been revoked is dropped there, so a connected row always
means capacity the keychain actually backs. Signing out is `POST
/accounts/reset` — the "çıkış yap" button — which is also how you switch to a
different Anthropic account.

Capacity is therefore one run at a time, and a task created with nothing
connected is refused rather than queued: there would be no identity to spend,
and falling back to the CLI's own login would drain the queue through the
account Mimir deliberately does not touch. Mimir never reads, moves or stores a
credential — the keychain keeps it.

The daemon's own model calls — refine, distil, recap — are not dispatched runs,
but they spend the same account: "which account paid for this?" has one answer.

**A spent token budget pauses the pipeline; it does not fail it.** When a run
hits the account's limit mid-task the card is not marked `failed`: it goes back
to `queued` carrying its `session_id`, the slot is held so nothing else is
dispatched into an account with nothing to spend, and every step is written to a
durable log (`GET /coding-tasks/queue/limits`) — the run that ran out, each
queued task held behind it, and the moment the window rolled over. One wake-up
is armed for the reset time the CLI itself reported (its `rate_limit_event`, or
the `…|<unix>` suffix on its usage-limit message; 15 minutes when it reported
neither), and when it fires the queue is pumped exactly as any other release
pumps it. Nobody presses anything, and the resumed run continues under
`--resume` rather than starting the task over. The pause outlives a restart: the
daemon rebuilds it from the log at startup rather than rediscovering it by
spending another CLI invocation.

Needs: the `claude` CLI on `$PATH` and logged in.

---

### Region search needs no Google credential

`internal/regionsearch` asks the providers in one fixed order, **free first**:

| Order | Provider | Costs | Gives |
|---|---|---|---|
| 1 | `internal/mapscrape` — the public Maps results feed, rendered by a local Playwright container | nothing; no credential | name, coordinates, rating, review count, sometimes a website and an address |
| 2 | `internal/mapsllm` — the same page through Crawl4AI, read by claude haiku | model tokens; no Google money | the same fields, when the page renders at all |
| 3 | `internal/maps` — Google Places API | a billed request each | the above plus phone, a formatted address and Google's `types[]` |

The model is also the scraper's own recovery path: when the selectors read
nothing — Google owns that markup and changes it without notice — the rendered
page is re-read by the same profile rather than the region being reported empty.
A feed that parsed anything never reaches the model, and no prompt here is
allowed to produce a business that was not on the page.

The container is not the operator's problem: a search that finds it down runs
`docker compose up -d` against the compose file installed beside the daemon,
waits for its health check, and retries once. `make maps-up` still starts it by
hand. Provenance travels with every row — a scraped `place_id` carries the
`mapscrape:` prefix so it can never overwrite a billed one — and the report, the
`maps_search` response and the desktop badge each name the source that answered.

### The workbook

`POST /maps/leadgen/export` runs the search and writes an `.xlsx` into
`~/Library/Application Support/mimir/exports/`: a summary sheet, then **one
sheet per category**. Each row carries the company, whether it has a website at
all, the website, phone, email, address, rating, coordinates, which source found
it, and which tier found the contact details.

With `enrich: true` the daemon opens each company's own site once and reads the
contact details off it — a `tel:`/`mailto:` link or a footer number first, and
claude haiku only on the pages where that found nothing, with its answer held to
the same patterns. Nothing is inferred: an empty phone cell means the lookup
found none, and the method column says which tier looked.

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
| 4 | **Outreach message** | yes | One draft per company per channel, fed that company's facts + its category's stage-3 gap analysis + that channel's rule file. Channels: `email`, `whatsapp`. | `outreach_emails` (keyed by `place_id, channel, prompt_version` — the version carries both the model and the rule file's hash — with a `status` the region re-run respects) |

`Pipeline.Run` always does stages 1–2. Stage 3 runs when `gap_analysis: true`;
stage 4 when `emails: true` (which implies gap analysis). The two model phases
are bounded `errgroup` fan-outs limited to `MaxConcurrentRefines`.

### Choosing the model

Stages 2–4 route by *class* — one-shot compression to the free `agy` tier,
synthesis to `claude` — and that is what a run gets when it asks for nothing.
A run may instead name a provider and a model (`provider` / `model`, or the
picker on the desktop's lead-gen screen), and then all three model stages spend
that one. The pair is checked against `config.LLMProviders` before it goes
anywhere, because both strings become argv to a subprocess; an unknown pair is
a 400, never a silent fall back to the default. A selection also suppresses the
availability fallback, and namespaces the caches it reads — a run on a different
model calls that model rather than replaying the previous one's answers. The
region search's own model fallback (`internal/mapsllm`) is not covered: it is
wired at construction and shared by every caller of `maps_search`.

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
| **Leadgen** | The Maps pipeline: a region search form, per-category gap-analysis cards, and a company table with a checkbox column. The bar under the ticked rows states what a draft run will spend (companies × channels) and calls `POST /maps/outreach`; the drafts are then read one at a time behind channel tabs, with mark-sent / mark-skip wired to `POST /maps/outreach/status`. `report.notes` is shown verbatim. |
| **Settings** | The operator's own configuration on one screen: the default model lead-gen spends, and editors for the email and WhatsApp rule files (path, "restore the default", ⌘S). A rule file is part of the drafting prompt, so saving one invalidates the drafts written under the old text — the screen says so. |

Packaging: `tauri.conf.json` builds `app` + `dmg` with a hardened runtime and
`entitlements.plist`. Signing/notarization are env-driven at build time
(`APPLE_SIGNING_IDENTITY` + Apple-ID / API-key creds); a keyless build still
produces an ad-hoc app.

- **Katalog** — a product catalog imported from a Shopify or IKAS CSV (products
  or custom fields), its brand vocabulary read out of the store's own HTML, and
  its descriptions edited inside that vocabulary: old and new rendered side by
  side in a sandboxed frame, and a rich-text editor whose schema is built from
  the vocabulary rather than from a fixed toolbar. Opening one product takes the
  full width — editor on the left, live preview on the right, a `önce · sonra`
  switch above it. The column map is reachable whether or not the file was
  recognised, and opens filled in with the file's own sample values. Loaded on
  demand — it is the one screen heavy enough to be worth splitting out of the
  bundle.

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
| `accounts` | the connected Claude account: Mimir's own slot directory | cleared at daemon start and stop — the account never outlives the app |
| `coding_runs` | run metadata, cost, session id, transcript pointer | none (history) |
| `companies` | normalized Places/scrape result, keyed by `place_id` | caller-supplied long TTL (~30d) |
| `region_searches` | the ordered `place_id` list a region search returned | same TTL; all-or-nothing on read |
| `company_categorization` | normalized category + which tier answered | `LeadgenCategoryVersion` bump |
| `category_gap_analysis` | Claude-synthesized gaps/needs per category | `LeadgenGapVersion` bump; a changed company set misses |
| `outreach_emails` | drafted message per channel + `status` (draft/sent/skipped) | `LeadgenEmailVersion` bump, a different model, **or an edited rule file**; "sent"/"skipped" blocks regeneration (SQL-enforced) |
| `memory_episodes` | one distilled iteration: deterministic facts always, a short recap when one was accepted | `MemoryPromptVersion` bump re-derives recaps; facts survive |
| `memory_notes` | facts pinned through `context_remember` | none — never rewritten by an ingest |
| `memory_ingest_state` | how far each transcript has been parsed | reset when a transcript shrinks (replaced, not appended) |
| `memory_fts` | FTS5 index over episode titles, summaries, files and commands | kept in sync by triggers |
| `leads` | the lead ledger: one row per business ever found, with its category. **A record, not a cache** — no TTL, and no reader deletes from it | none; a re-run refreshes fields but never blanks a populated one |
| `lead_runs` · `lead_run_members` | which search found which business, and when | none (history) |
| `chat_sessions` · `chat_turns` | conversations stored verbatim — the text `memory_episodes` clips. Written from the same parse, keyed by the same episode key | none; a re-read updates a turn rather than duplicating it |
| `chat_fts` | FTS5 index over prompts and assistant replies | kept in sync by triggers |
| `catalog_imports` · `catalog_products` | one product catalog as it was uploaded: the header, the framing, and **every raw cell of every row**, including the columns Mimir has no name for. A record, not a cache — it is what makes export lossless, and re-deriving it from a file the operator may have deleted is not something the daemon will attempt | none; a re-upload of the same file addresses the same product ids and keeps their approvals |
| `catalog_research` · `catalog_drafts` | what a rewrite already paid for, per product: the market research and the copy, under **two deliberately different keys**. The brand hash is in the draft key and absent from the research key, so correcting the brand voice rewrites every description and throws away no competitor research | a bumped `CatalogContentVersion`, a different model, an edited brand kit or skill; a hand-edited draft is never overwritten (SQL-enforced) |
| `brain_node_versions` | one entry per distinct content hash a node's source has carried: when, how big, and what it meant then. No file content | pruned to `BrainVersionsPerNode` on insert |

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
| Catalog import / export | — | — | — | — |
| Catalog brand voice | — | to distil (the vocabulary needs nothing) | — | — |
| Catalog rewrite (a board card) | Crawl4AI for the market half | yes | — | — |
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
