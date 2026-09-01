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
3. **`claude` CLI (local subprocess)**:
   - The refiner is the operator's own `claude` CLI (Claude Code), invoked headless via `exec`, not an HTTP call Mimir makes itself.
   - That subprocess makes its own request to the Anthropic API (model `claude-haiku-4-5-20251001`), authenticated with the operator's own `claude login` session — **scraped page content is sent to Anthropic** to be distilled. There is no separate API key configured by Mimir; it rides the existing Claude Code login.
   - Every refine call runs with `--restricted`, `--strict-mcp-config`, and every built-in tool force-denied via `--disallowedTools` — the subprocess can only emit text, never take an action, read local files, or reach the network itself.
4. **Google Places API (HTTPS, Track B, optional)**:
   - Reached only by `mimir-daemon`, and only when `MIMIR_GOOGLE_PLACES_API_KEY` is set. Structured business facts in, no free-text field requested (`maps.FieldMask`), so nothing here needs refining. The key travels only in the `X-Goog-Api-Key` header and never appears in a log line or an error.
5. **Maps scrape sidecar (HTTP localhost, Track B, optional fallback)**:
   - `deploy/playwright-maps/` on `127.0.0.1:11236`, pinned image. Used only when Places coverage/cost is not worth it. It renders `google.com/maps` pages and is host-allowlisted so it cannot be pointed at the machine's own network or a metadata endpoint.

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
- All scraped content is routed through the local, tool-less `claude` CLI call to extract key points and synthesize brief summaries before it can reach the parent session.
- The Track B `/maps/*` routes return only structured facts and already-refined text (each gap analysis and email body passed `clampOutput` once when it was generated). The Maps stages that touch a model are fed deterministically-computed scalars, never a raw page; categorization additionally constrains the model to a closed vocabulary so no model-written free text leaves that package.

## Secrets & Credentials

`mimir-mcp` (Track A) requires **zero API keys or credentials configured by Mimir itself**.
- No Bing API key. No paid proxies or scraping APIs.
- No `ANTHROPIC_API_KEY` — the refiner authenticates via the operator's existing `claude login` session, not a key Mimir holds or reads.

**One documented exception (Track B):** `MIMIR_GOOGLE_PLACES_API_KEY`. It is
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
scraped content ever left the machine. It is now the Anthropic API, reached via
the local `claude` CLI — see the Egress Policy above.

## Telemetry

The binaries contain no telemetry, analytics, or auto-update mechanisms of their own. They log structured JSON to `stderr` only. The `claude` CLI subprocess (headless refiner, and the folder-scoped coding-task runner) is subject to Anthropic's own logging/telemetry as it would be in any other invocation. The desktop app ships no crash reporter or analytics; packaging/notarization (M7) does not add one.
