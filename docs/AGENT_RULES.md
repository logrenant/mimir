# AGENT_RULES.md — GOAT

Detailed rules for every agent that touches this repo. Root [`AGENTS.md`](../AGENTS.md)
is the short version and the repo map; this file is the authority when the two
disagree.

---

## 1. Role model (the "model vs agent" clarification)

The project involves **four** distinct LLM/agent roles. The original Gemini-authored
rules blurred them; keep them separate.

### 1.1 Coder Agent — Gemini (build time)
- Writes the initial Golang implementation, **one `tasks/task-NN` file per run**.
- Scope is the task file, full stop. If the task looks wrong or blocked, stop and
  report; do not "helpfully" expand.
- Must leave `make check` green and the task `Status: done`.
- May not touch other task files, roadmap, or rule docs.

### 1.2 Reviewer Agent — Claude Opus via Claude Code (build time)
- Runs **after** a task is marked done.
- Checks the diff against: the task's Definition of Done, the Strict Directives
  (§3), and the nearest `AGENTS.md`.
- May refactor, tighten tests, and fix directive violations directly.
- Output: a verdict — `APPROVED`, `APPROVED WITH FOLLOW-UPS` (list them), or
  `CHANGES REQUIRED` (list them). Follow-ups that are real work become new task
  files proposed to the user — the reviewer does not silently grow scope either.

### 1.3 Runtime Consumer — Claude Code (run time, not a contributor)
- The finished binary is registered as an MCP server in an end-user Claude Code
  session. That session calls `web_search`, `fetch_page`, `research`,
  `diagnostics`, the four Stage F lookups, and `maps_search` (only when a Places
  key is present). `goat-daemon` re-exposes the same registry over HTTP at
  `/mcp`.
- It never sees this repo. It **only** sees tool responses — which is why every
  response must be compact and refined (§3.2, §3.7).
- The Tauri desktop app is a second runtime consumer of `goat-daemon`'s REST +
  WebSocket surface (`/projects`, `/coding-tasks`, `/ws/runs/{id}`, `/maps/*`).
  It is not a contributor either, and the same "door, not a floor" rule applies:
  a handler translates JSON to a runtime-package call and back, nothing more.

### 1.4 Refiner Model — local `claude` CLI (run time dependency)
- The operator's own **`claude` CLI** (Claude Code), invoked headless via
  `exec` — `-p --output-format json --no-session-persistence
  --strict-mcp-config --restricted --effort low`, every built-in tool
  force-denied. Model **`claude-haiku-4-5-20251001`** (pinned constant in
  `internal/config`). No API key, no HTTP endpoint — it rides the operator's
  existing Claude Code login (`claude login`).
- Called only by `internal/refine`. It distils scraped Markdown into compact,
  factual output. It has **no** decision-making authority over the build and is
  never referred to as "the agent" in code, comments, or docs. Call it "the
  refiner" or "the claude CLI refine call".
- Swapping the model tag later is a config change made in code by a task, never a
  user setting.

---

## 2. Architecture contract (summary — full detail in [`ARCHITECTURE.md`](ARCHITECTURE.md))

- **Two binaries, one engine.** `bin/goat-mcp` (Track A) is a stdio MCP server, no
  daemon, no service of its own. `bin/goat-daemon` (Track B) is a long-running
  loopback-only HTTP service for the Tauri app; it imports the same runtime
  packages and re-exposes the same `internal/mcp.Registry` at `/mcp`. Not two
  orchestrators — one engine, two transports.
- **Transport:** `goat-mcp` speaks MCP over **stdio** — `stdout` carries protocol
  frames and nothing else. `goat-daemon` binds `127.0.0.1` only, behind a
  per-launch bearer token (`config.ValidateDaemon` refuses to start without one)
  and a second loopback guard. Both log structured JSON to `stderr` only.
- **Egress policy:** `goat-mcp` may reach exactly two network destinations —
  `duckduckgo.com` and `127.0.0.1:11235` (Crawl4AI) — plus the local `claude`
  CLI subprocess, which makes its own call to the Anthropic API under the
  operator's login. `goat-daemon` adds the Google Places API (only with
  `GOAT_GOOGLE_PLACES_API_KEY`) and the `127.0.0.1:11236` Playwright sidecar
  (optional fallback). Crawl4AI / the sidecar are what fetch arbitrary target
  sites; our processes do not.
- **Data flow, mandatory:**
  `query → search → (top-N) crawl via Crawl4AI → refine via claude CLI → merge → compact response`.
  The refine step is not optional for any tool that carries scraped page text.
  The Maps lead-gen pipeline (`internal/leadgen`) is cache-first at every stage
  and degrade-never-fail: only `ErrNoData` or a cancelled context is an error.

---

## 3. Strict Directives

Numbered so task files and reviews can cite them (e.g. "violates SD-2"). A task is
**not done** if it introduces a violation, even outside its stated scope.

### SD-1 — Configuration is standardized in code, never user-tunable
- All operational values (endpoints, ports, model tag, timeouts, concurrency
  limits, result caps, prompt templates) are **constants** in `internal/config`.
- The `config` package exposes `Load() Config` returning a fully-populated value.
  **No exported setters, no functional options, no config file, no CLI flags** for
  behaviour.
- The only permitted environment overrides are for **test/CI plumbing** and must be
  enumerated in `internal/config/AGENTS.md` (e.g. `GOAT_CRAWL4AI_URL`,
  `GOAT_CLAUDE_CLI_PATH` pointing at mock servers/fake binaries). They default
  to the production localhost values / `claude` on `$PATH` and are ignored in
  the shipped happy path.
- The single decision made once, at bootstrap, is the Go **module path**
  (`github.com/logrenant/goat-mcp`). Not a runtime knob.
- **Two narrow, documented categories of non-constant value exist (Track B),
  both in `internal/config/AGENTS.md`:** (a) *process plumbing, parent-provided*
  — `GOAT_DAEMON_PORT` / `GOAT_DAEMON_TOKEN`, which answer "where do I listen,
  what secret do I accept" and nothing about behaviour; (b) the
  *operator-provisioned credential* — `GOAT_GOOGLE_PLACES_API_KEY`, read once,
  which gates *whether* the Places provider is reachable, never what a request
  does. Neither is a behaviour knob, and no third category may be added without
  the same justification.

### SD-2 — Context isolation: raw scraped data never reaches the consumer
- No MCP tool may return raw Crawl4AI output, raw HTML, or unbounded page text.
- Every tool whose response carries scraped page text must route that text through
  `internal/refine` first.
- A single enforcement choke-point (`internal/mcp/finalize.go`) wraps every tool
  response and **fails closed**: it asserts the response is within its size
  ceiling and that any
  field carrying page-derived text is marked `refined: true`. Unmarked or oversized
  → the tool returns an error, not the payload.
- `web_search` is the one exception: it returns only title/URL/snippet metadata
  from the search engine, which is already compact. It still passes the choke-point
  and still has a size ceiling.

### SD-3 — Concurrency is bounded, cancellable, and deadlock-free
- Fan-out uses `golang.org/x/sync/errgroup` with `SetLimit`; the limit comes from
  `internal/config` (`MaxConcurrentCrawls`, `MaxConcurrentRefines`). No unbounded
  `go` loops over search results.
- Every external call takes a `context.Context` derived from the MCP request, with
  a timeout from config. The whole `research` call has an overall deadline.
- Partial results are tolerated: if 2 of 5 crawls fail or time out, the pipeline
  continues with the 3 that succeeded and notes the gaps.
- No blocking send/receive on a channel without a `select { case …: case <-ctx.Done(): }`.
- `make race` (`go test -race`) is part of `make check`. Goroutine-leak checks use
  `go.uber.org/goleak` in pipeline tests.

### SD-4 — MCP transport hygiene
- Nothing writes to `os.Stdout` except the MCP SDK's transport. `fmt.Print*` to
  stdout is banned repo-wide (add a lint/grep check in `make check`).
- Logging is structured (`log/slog`, JSON handler) to `stderr`, with a request ID
  per MCP call and per-stage timings.
- Never `panic` across the MCP boundary. Recover at the tool handler edge and map
  to an MCP error response.

### SD-5 — Dependencies, images, and model tags are pinned
- `go.mod` uses exact tagged versions; no `latest`, no pseudo-versions from
  `main`.
- The Crawl4AI Docker image is pinned to an explicit tag in
  `deploy/crawl4ai/docker-compose.yml` (record the exact tag used).
- The Claude model id is the constant `claude-haiku-4-5-20251001` in
  `internal/config`.
- Bumping any of these is its own task, never a side effect.

### SD-6 — Failure posture
- Every external call: explicit timeout, typed error (`errors.Is`-friendly
  sentinels like `crawl.ErrDockerUnavailable`, `refine.ErrClaudeUnavailable`),
  bounded retry with backoff where safe (idempotent GETs, refine).
- "Docker not running" / "claude CLI not authenticated" produce a **clear,
  actionable** error message naming the fix (`make crawl-up`, `claude login`),
  surfaced through the `diagnostics` tool and as the tool error.
- Degrade, don't crash: an empty/failed refine on one source drops that source
  from the brief; it does not fail the whole `research` call unless **every**
  source failed.

### SD-7 — Output compactness is a contract, not a hope
- Each tool documents a hard max response size (token estimate) in its schema
  description and enforces it:
  - `web_search`: default 8 results, ceiling 30; metadata only.
  - `fetch_page`: refined Markdown, ≤ ~1500 tokens.
  - `research`: one brief — `summary` (≤ 5 sentences) + `key_points` (bullets) +
    `sources` (numbered, title + URL) — ≤ ~2000 tokens total.
- Over-ceiling output is truncated by the refiner with a `truncated: true` flag,
  never passed through raw.

### SD-8 — Every task ships tests
- Unit tests for new logic, with all three external dependencies (DDG,
  Crawl4AI via `httptest`; the `claude` CLI via a fake executable script)
  mocked.
- Deterministic prompt assembly (`internal/refine`) is covered by golden-file
  tests.
- Integration tests that need real services are behind `//go:build integration`
  and excluded from `make check`.
- `make check` stays green. A task that leaves it red is not done.

---

## 4. Task file schema

Every `tasks/task-NN-*.md` uses this structure. Every new task must follow it.

```
# task-NN — <title>

- **Status:** todo            (todo | in-progress | done)
- **Owner agent:** Coder (Gemini)
- **Prerequisites:** task-XX, task-YY   (or "none")
- **Primary paths:** <dirs/files this task may create or edit>
- **Roadmap bucket:** <the ROADMAP.md section or milestone this serves>

## Context
Why this exists; where it sits in the pipeline; links to ARCHITECTURE / rules.

## Scope (do exactly this)
Numbered, concrete steps.

## Out of scope (do NOT do here)
Bulleted. Adjacent work that belongs to other tasks.

## Interfaces / contracts
Go signatures, JSON schemas, endpoints, pinned versions — enough that the
implementation is not guesswork.

## Definition of Done
- [ ] concrete, checkable outcomes
- [ ] `make check` green
- [ ] Status set to `done` with changelog

## Notes for the reviewer (Opus)
Which Strict Directives to scrutinise for this task.
```

---

## 5. Reviewer workflow (Opus)

1. Confirm the task's `Status: done` and read its changelog.
2. Read the diff. Map each hunk to a Scope item; anything unmapped is scope creep —
   flag it.
3. Walk the Definition of Done line by line against the actual code/tests.
4. Run the Strict Directive checklist:
   - SD-1: grep for exported setters, flags, config files, `os.Getenv` outside the
     enumerated test overrides.
   - SD-2: is there a path by which a tool returns unrefined page text? Is the
     choke-point applied?
   - SD-3: `errgroup.SetLimit` present? contexts plumbed? `make race` clean?
     goleak in pipeline tests?
   - SD-4: `grep -rn "fmt.Print" ` / writes to `os.Stdout`; slog to stderr?
   - SD-5: any `latest` / pseudo-version / unpinned image or model tag?
   - SD-6: timeouts + typed errors + actionable messages on every external call?
   - SD-7: size ceilings enforced with a test that exceeds them?
   - SD-8: tests present, external services mocked, `make check` green?
5. Verdict: `APPROVED` / `APPROVED WITH FOLLOW-UPS` / `CHANGES REQUIRED`, with a
   concrete list. Propose follow-ups as new task files; do not implement
   out-of-scope work under the current task number.
