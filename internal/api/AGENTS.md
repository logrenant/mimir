# AGENTS.md — internal/api

The daemon's HTTP surface. Introduced by `tasks/task-22` (REST + `/mcp`),
extended by `tasks/task-25` (`GET /ws/runs/{id}`) and `tasks/task-34`
(`POST /maps/leadgen`, `POST /maps/emails/status`).

This package is a **door, not a floor**. Every route is a thin translation
between JSON and a runtime package that already enforces its own rules —
`internal/project` decides whether a path may become a project,
`internal/coderunner` decides how a session is scoped, `internal/mcp` decides
what a tool response may contain. If a handler here starts making one of those
decisions, it is in the wrong package.

## Rules for this directory

- **No route bypasses the chain.** `Handler()` wraps the whole mux —
  `/mcp` included — in recover → log → loopback → token → body cap. Adding a
  route means adding it to that mux, never mounting a second handler beside it.
- **`/mcp` is the same registry `cmd/goat-mcp` serves over stdio.** Never build
  a parallel tool path here; that would put a tool response outside
  `finalize.go`'s choke-point (SD-2). Two transports, one engine.
- **A filesystem path is accepted at exactly one route** — `POST /projects` —
  and it is handed straight to `project.Registry.Register`. Every other route
  takes the opaque `project_id`. Do not add a path parameter anywhere else, and
  do not "helpfully" join, clean, or default one.
- **No CORS headers, ever.** A browser page on another origin may be able to
  *send* a request to loopback; without CORS it cannot read the reply. Adding a
  permissive header would trade that away for nothing — the only intended
  client is the Tauri shell, which is not subject to it.
- **Echo only errors written for a human.** The path guards in
  `internal/project` explain what is wrong and what to do, so they are returned
  as-is. Anything unrecognised is logged and answered with a generic 500; an
  internal failure message belongs in the operator's log, not a response body.
- **`POST /coding-tasks` answers 202 and returns.** It must not wait for the
  run: a session lasts minutes, and the run's lifetime belongs to the daemon
  (`coderunner.New(base, …)`), not to the request context.
- **stderr only** (SD-4). The resolved listen address is logged, never printed.
  goat v1's parent scraped `GOAT_PORT=<n>` off the child's stdout; here the
  parent already knows the port because the parent chose it.

## The run socket

`GET /ws/runs/{id}` has four rules, and each one is load-bearing:

- **Resolve the run before upgrading.** After the handshake there is no status
  code left to send, so an unknown id has to fail while it can still be a 404.
- **Subscribe before replaying.** Subscribing after the transcript read would
  lose whatever was published in between.
- **Two sources, stitched by `Seq`.** The bus is live but lossy by contract; the
  transcript is complete but not a stream. Replay the file, follow the bus, and
  treat a `Seq` jump as "the bus dropped something, go read the file" — never as
  permission to show the watcher a hole. Never send a `Seq` twice.
- **The socket is a window, not a dependency.** A watcher that hangs up, stalls,
  or never connects must make no difference to the run. Nothing in the handler
  may block the runner, and the run's outcome is recorded whether or not anyone
  is looking.

It is one-directional. Incoming frames are read only to notice the client is
gone (`conn.CloseRead`); a control channel for cancelling or steering a run
would be a different endpoint with a different threat model.

## The `/maps/*` routes (task-34)

- **Registered only when a Places key is configured.** `Handler()` mounts them
  behind `s.deps.LeadGen != nil`, the same availability-follows-credential rule
  the `maps_search` tool uses. `cmd/goat-daemon` sets the interface field only
  when it has a real `*leadgen.Pipeline`, because a nil pointer in a non-nil
  interface would still pass the guard.
- **`POST /maps/leadgen` returns the pipeline's `Report` verbatim.** The handler
  translates JSON to a `leadgen.RunRequest` and back; `internal/leadgen` owns
  every decision about caching, cost, and degradation. `Report.Notes` already
  carries the per-stage diagnostics, so the handler does not synthesise its
  own. The only hard failure, `leadgen.ErrNoData`, maps to 502 — an upstream
  source failed, not the daemon.
- **`POST /maps/emails/status` takes the prompt version from the server, not the
  client.** A client marking "sent" means the draft it is looking at, which is
  the current `config.LeadgenEmailVersion`. `store.ErrEmailStatusInvalid` → 400,
  `store.ErrEmailNotFound` → 404.
- **No new path parameter.** Both routes take their identifiers in the JSON
  body, keeping "a filesystem path is accepted at exactly one route" and "no
  path parameter anywhere else" intact.

The library's Origin check is disabled deliberately: it defends servers whose
auth is ambient (a cookie the browser attaches for you). Ours is a bearer token
a foreign page cannot obtain, already checked before the handler runs — and the
real client is a Tauri WebView whose origin is not this host, so the check would
reject exactly the caller this exists for.

## Auth model

One operator, one machine, one process. The parent (Tauri) mints a per-launch
token and passes it via `GOAT_DAEMON_TOKEN`; `config.ValidateDaemon` refuses to
start without one. Comparison is constant-time.

The token arrives in `Authorization: Bearer …`, or — for a WebSocket, where the
browser API cannot set headers — as the `goat.bearer.<token>` entry in
`Sec-WebSocket-Protocol`, which the handshake echoes back. **Never accept it
from the query string**: URLs reach access logs, history, and referrers, and
this token starts coding sessions in the operator's own repositories. There is no user model, no
session, no TLS, and no "development mode" that skips the check — an
unauthenticated local server with the coding-task runner behind it is a shell
on the operator's machine for anything that can open the port.

## Testing

`httptest.NewServer` gives a loopback peer for free, so the guard is exercised
rather than mocked. Handlers take interfaces (`ProjectRegistry`, `CodeRunner`,
`Healther`), so a test needs neither a database nor a subprocess; only the test
that starts a real run needs the fake `claude` script, and it must call
`Runner.Wait()` before finishing or goleak will catch the run goroutine.

## Reviewer focus

SD-1: the two daemon env vars are plumbing (where to listen, what secret to
accept) — confirm nothing about behaviour became reachable from the
environment. SD-2: no route returns page-derived text, and `/mcp` still goes
through the registry. SD-3: `Serve` does not leak the listener or its goroutine
and drains within `DaemonShutdownTimeout`. Security: `grep` the middleware
chain and confirm both the loopback guard and the token check sit ahead of
every route.
