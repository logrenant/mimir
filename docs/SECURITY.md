# Security & Privacy

Mimir is designed with strict security boundaries and data isolation in mind.
`mimir-mcp` operates as a local subprocess to Claude Code; `mimir-daemon` (Track B)
is a long-running loopback-only HTTP service the Tauri desktop app talks to.
Both are a heavily constrained bridge to the open internet.

## Egress Policy

`mimir-mcp` itself limits outbound network connections to exactly two targets,
plus one local subprocess that makes its own outbound call. `mimir-daemon` adds
one more (the Google Places API) when an operator key is present, and the Maps
scrape fallback container when it is running.

1. **DuckDuckGo (HTTPS)**:
   - Uses `lite.duckduckgo.com` (for diagnostics) and `html.duckduckgo.com` (for searches).
   - We scrape standard HTML; no proprietary search APIs or telemetry is used.
2. **Crawl4AI (HTTP localhost)**:
   - Must be running locally (e.g. `127.0.0.1:11235`).
   - Arbitrary URLs requested by the user are **delegated to Crawl4AI**. Mimir itself does not fetch arbitrary internet addresses; it only communicates with the local Crawl4AI container, which handles the risks associated with rendering untrusted third-party DOMs.
3. **`claude` CLI (local subprocess)** — the *reason* tier:
   - Invoked headless via `exec`, not an HTTP call Mimir makes itself.
   - That subprocess makes its own request to the Anthropic API (model `claude-haiku-4-5-20251001`), authenticated with the operator's own `claude login` session — **scraped page content is sent to Anthropic** to be distilled. There is no separate API key configured by Mimir; it rides the existing Claude Code login.
   - Every call runs with `--restricted`, `--strict-mcp-config`, and every built-in tool force-denied via `--disallowedTools` — the subprocess can only emit text, never take an action, read local files, or reach the network itself.
4. **`agy` CLI (local subprocess, task-41)** — the *distil* tier, and the one most page content now goes through:
   - The operator's own Antigravity CLI, invoked headless via `exec`, riding its existing sign-in. Model `gemini-3.8-flash-high`, pinned. **Scraped page content is sent to Google** to be distilled; before task-41 that content went to Anthropic instead. No API key is configured by Mimir either way.
   - **This subprocess is less tightly bounded than the claude one, and that is a real difference, not a wording change.** `agy` has no `--disallowedTools` and no `--strict-mcp-config`. Three things stand in for them: `--sandbox`; a working directory that is an empty scratch dir under the store's parent rather than any repository (`agy` reads `AGENTS.md` and `.agents/rules` from wherever it starts, and this subprocess's entire input is untrusted text); and `MIMIR_NESTED=1`, which `cmd/mimir-mcp` answers by serving **no tools**, so the globally-registered Mimir cannot be recursed into. All three are asserted by tests in `internal/llm`.
   - What has not changed: the fail-closed choke-point in `internal/mcp/finalize.go` still decides what reaches the consumer, whichever provider produced it.
   - **There is no fallback.** If `agy` is absent or out of quota the distil path is down: page content reaches no model at all and the affected tools return an error. That is a deliberate narrowing (task-51) and it is a security property as much as a billing one — the set of destinations scraped text can reach shrinks from {Google, Anthropic} to {Google}.
5. **GitHub REST API (HTTPS, optional)**:
   - `api.github.com/repos/<owner>/<name>/readme`, reached only by `brain_ingest_github` and only for a repository the caller named. Read-only, one endpoint, capped at 256 KiB. `MIMIR_GITHUB_TOKEN` is sent as a bearer token when set and is never logged; without it only public repositories are readable.
6. **Google Places API (HTTPS, Track B, optional)**:
   - Reached only by `mimir-daemon`, and only when `MIMIR_GOOGLE_PLACES_API_KEY` is set. Structured business facts in, no free-text field requested (`maps.FieldMask`), so nothing here needs refining. The key travels only in the `X-Goog-Api-Key` header and never appears in a log line or an error.
7. **Maps scrape sidecar (HTTP localhost, Track B, optional fallback)**:
   - `deploy/playwright-maps/` on `127.0.0.1:11236`, pinned image. Used only when Places coverage/cost is not worth it. It renders `google.com/maps` pages and is host-allowlisted so it cannot be pointed at the machine's own network or a metadata endpoint.

8. **`pdftotext` (poppler, local subprocess, optional)**:
   - Reached only by the repository scan, and only for a file the operator's own directory walk found. It makes **no network call**: a PDF goes in by path, text comes out on stdout. The posture is the one a C++ parser fed arbitrary files needs — a timeout, an output ceiling, an empty environment (`cmd.Env = []string{}`) and a working directory that is a scratch dir rather than a repository — and a crash is a skipped file, never a failed scan. Absent, PDFs are simply not indexed.

### The coding-task runner (Track B)

`internal/coderunner` runs the operator's own `claude` CLI **with file tools
enabled**, deliberately — it does work in a folder the operator chose. It is not
the headless refiner. Its blast radius is one directory: `internal/project`
canonicalizes and guards every path (rejecting `/`, `$HOME`, denylisted roots),
there is **no default project**, and every call after registration carries only
an opaque `project_id`. The invocation is `cmd.Dir` + `--add-dir` = that path,
plus a fixed `--permission-mode` constant (SD-1) — `acceptEdits`, which accepts
file edits but still withholds Bash; `bypassPermissions` is never used.

## Context Isolation Guarantee

Mimir guarantees that the parent Claude Code session is protected from data pollution and token exhaustion:
- Raw HTML, script payloads, and unrefined bulk text are **never** returned to the Claude session.
- Output from tools (`fetch_page`, `research`) passes through a strict choke-point (`internal/mcp/finalize.go`). It enforces maximum token ceilings and inspects the payload for raw signatures. `mimir-daemon`'s `/mcp` serves the **same** registry, so an HTTP tool call goes through the same choke-point as a stdio one.
- All scraped content is routed through a local, headless CLI call — the distil tier (`agy`) by default, the `claude` tier when it is unavailable — to extract key points and synthesize brief summaries before it can reach the parent session. The choke-point is what enforces this, not the provider, so the guarantee does not change with the routing.
- **The lead-gen model picker is an allow-list, not a passthrough.** `POST
  /maps/leadgen` accepts an optional `provider`/`model` pair, and both strings
  end up as `--model` argv to a local CLI subprocess. They are checked against
  `config.LLMProviders` — a constant in the binary — before they reach
  `internal/llm`; anything else is a 400, never a fall back to the default. A
  client can therefore choose *among the tiers this build ships*, and can never
  name a binary, a flag, or a model the build does not know about.
- The Track B `/maps/*` routes return only structured facts and already-refined text (each gap analysis and email body passed `clampOutput` once when it was generated). The Maps stages that touch a model are fed deterministically-computed scalars, never a raw page; categorization additionally constrains the model to a closed vocabulary so no model-written free text leaves that package.

## Secrets & Credentials

`mimir-mcp` (Track A) requires **zero API keys or credentials configured by Mimir itself**.
- No Bing API key. No paid proxies or scraping APIs.
- No `ANTHROPIC_API_KEY` and no Google API key for the model — both providers authenticate via the operator's existing CLI sign-in, not a key Mimir holds or reads.

**Two documented exceptions (Track B):** `MIMIR_GOOGLE_PLACES_API_KEY` and
`MIMIR_GITHUB_TOKEN`. Both are optional, both are read once in `config.Load`,
and an empty value is a defined state rather than an error — `maps_search` is
simply not registered, and `brain_ingest_github` reads public repositories only.
The first of them is
operator-provisioned, injected by the parent process (the Tauri shell / the
operator's environment), and read once at startup in `config.Load` — never a
runtime setting a consumer can pass, and never written to a log or an error. It
gates *whether* the Places provider exists; an empty value is a normal install
in which `maps_search` and `/maps/*` are simply absent. This is the "credential
vault" pattern `docs/ROADMAP.md` §A.3 anticipated, built here for its first use.

The `mimir-daemon` HTTP surface is authenticated by a **32-byte bearer token**
its parent mints (`MIMIR_DAEMON_TOKEN`); `config.ValidateDaemon` refuses to start
without one. The listener binds `127.0.0.1` only, a second loopback guard sits
ahead of every route, and comparison is constant-time. There is no user model,
no TLS, and no "development mode" that skips the check — an unauthenticated
local server with the coding-task runner behind it is a shell on the operator's
machine for anything that can open the port.

### The always-on install: a token at rest

There are two legitimate parents, and they make different trade-offs:

| Parent | Token | Lives |
|--------|-------|-------|
| The Tauri shell (dev) | minted per launch, in memory, passed in the child's environment | as long as the window |
| launchd (`scripts/install-agent.sh`) | minted at install, **on disk in two files** | across logins |

The installed path is the one the operator normally runs, and it deliberately
weakens one property to gain another: a daemon that survives the app being
closed cannot have a token that only ever existed in the app's memory. So the
token is written twice — `~/Library/Application Support/mimir/endpoint.json`
(what the app reads) and `~/Library/LaunchAgents/studio.mimir.daemon.plist` (what
launchd hands the child) — both created under `umask 077` and `chmod 0600`,
both inside the operator's own home directory.

What did **not** change: the daemon still binds loopback only, still refuses to
start without a token, still answers no route without one, and still sends no
CORS headers. What is new is that a process running as this user can read the
token off disk instead of having to be the desktop app. On a single-user
machine that is the same trust boundary the store (`mimir.db`, which holds every
registered project path and run transcript) already sits in.

Two guards keep that honest:

- The desktop shell **refuses** an `endpoint.json` that is group- or
  world-readable, rather than reading it anyway (`daemon.rs`,
  `EndpointError::Permissive`) — an exposed credential must fail loudly.
- It also refuses a `base_url` that is not `http://127.0.0.1:` — an endpoint
  file is an input, and a rewritten one would otherwise redirect the token off
  the machine.

Rotation is `scripts/install-agent.sh --rotate-token`, which rewrites both files
and restarts the agent; any app holding the old token gets a 401 and re-reads
the file on its next launch. `scripts/uninstall-agent.sh --purge` removes both.

Note the refiner history: it was originally a fully local Ollama model, and no
scraped content ever left the machine. It then became the Anthropic API, reached
via the local `claude` CLI. Since task-41 most page content goes to Google
instead, via the local `agy` CLI, and only there — see the Egress
Policy above. Each move traded a little confidentiality for capability or cost,
and none of them was reversed by accident: the current routing is stated in
`docs/ROADMAP.md` §B.1.

## Telemetry

The binaries contain no telemetry, analytics, or auto-update mechanisms of their own. They log structured JSON to `stderr` only. The `claude` CLI subprocess (headless refiner, and the folder-scoped coding-task runner) is subject to Anthropic's own logging/telemetry as it would be in any other invocation. The desktop app ships no crash reporter or analytics; packaging/notarization (M7) does not add one.
