# AGENTS.md — internal/api

The daemon's HTTP surface. Introduced by `tasks/task-22` (REST + `/mcp`),
extended by `tasks/task-25` (`GET /ws/runs/{id}`) and `tasks/task-34`
(`POST /maps/leadgen`, `POST /maps/outreach`).

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
- **`/mcp` is the same registry `cmd/mimir-mcp` serves over stdio.** Never build
  a parallel tool path here; that would put a tool response outside
  `finalize.go`'s choke-point (SD-2). Two transports, one engine.
- **A filesystem path is accepted at exactly one route** — `POST /projects` —
  and it immediately stops being a path: the route hands back an opaque id, and
  every later call carries that id. `POST /accounts` used to be the second,
  because a Claude Code credential slot *is* a directory; it is gone. Mimir has
  one account, in a slot it derived for itself, so there is nothing for a client
  to name — connecting is `POST /accounts/login`, which takes no body at all.
  The rule to enforce is unchanged: a path is validated once, at registration,
  by the package that owns that decision (`internal/project`). Do not add a path
  parameter to a second route.

- **The body cap is a table, not a constant.** Everything gets
  `DaemonMaxRequestBytes`; `POST /coding-tasks/attachments` gets
  `CodingAttachmentMaxBytes`, because an image is the one payload here that is
  not text. Raising the ceiling for a route must be a line in `bodyLimits`,
  never a relaxation of the default.
- **A route is registered only when its dependency is present.** A nil `Runner`
  in the `Deps` struct used to mean a nil-interface call inside the handler,
  which `recoverPanics` turned into a 500 for a route that honestly does not
  exist. `/ws/runs/{id}` needs `Transcripts` in that guard too, or it serves a
  socket with no history that cannot fill a dropped-event gap.
- **Stopping a run is a route, not a frame on the socket.** The socket stays
  one-directional; `POST /coding-tasks/{id}/stop` carries the same threat model
  as every other route — loopback, bearer token — and its whole authority is to
  interrupt a process this daemon started itself.
- **A conflict is a 409.** `ErrNotStoppable` / `ErrNotDeletable` mean the card
  the operator clicked was a moment out of date. A 400 would blame the request
  and a 500 would blame us; neither is true.
- **No CORS headers, ever.** A browser page on another origin may be able to
  *send* a request to loopback; without CORS it cannot read the reply. Adding a
  permissive header would trade that away for nothing — the only intended
  client is the Tauri shell, which is not subject to it.
- **Echo only errors written for a human.** The path guards in
  `internal/project` explain what is wrong and what to do, so they are returned
  as-is. Anything unrecognised is logged and answered with a generic 500; an
  internal failure message belongs in the operator's log, not a response body.
- **The model list is published, not mirrored.** `GET /coding-models` exists so
  the desktop picker is a view of `cfg.CodingModels` rather than a second copy
  of it — a copy would drift the first time a generation ships and start
  offering options the create route rejects.
- **`POST /coding-tasks` answers 202 and returns.** It must not wait for the
  run: a session lasts minutes, and the run's lifetime belongs to the daemon
  (`coderunner.New(base, …)`), not to the request context.
- **stderr only** (SD-4). The resolved listen address is logged, never printed.
  goat v1's parent scraped `MIMIR_PORT=<n>` off the child's stdout; here the
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
would be a different endpoint with a different threat model — which is exactly
what `POST /coding-tasks/{id}/stop` is.

The socket closes on a *terminal* status, not on "not running". A backlog or
queued task has no events yet but will have, and the desktop app opens a
terminal for a card the moment it is released rather than polling for the run to
begin.

## The `/maps/*` routes (task-34)

- **Registered only when a Places key is configured.** `Handler()` mounts them
  behind `s.deps.LeadGen != nil`, the same availability-follows-credential rule
  the `maps_search` tool uses. `cmd/mimir-daemon` sets the interface field only
  when it has a real `*leadgen.Pipeline`, because a nil pointer in a non-nil
  interface would still pass the guard.
- **`POST /maps/leadgen` returns the pipeline's `Report` verbatim.** The handler
  translates JSON to a `leadgen.RunRequest` and back; `internal/leadgen` owns
  every decision about caching, cost, and degradation. `Report.Notes` already
  carries the per-stage diagnostics, so the handler does not synthesise its
  own. The only hard failure, `leadgen.ErrNoData`, maps to 502 — an upstream
  source failed, not the daemon.
- **`POST /maps/outreach` takes place ids, never a filter.** That is the whole
  difference between it and `POST /maps/leadgen`: a search is "find me
  companies", this is "write to these ones". A filter would let a client spend a
  region's worth of tokens with one short string, and nobody — least of all the
  operator — would have seen beforehand how many companies that was. The ids are
  resolved against the ledger, so an id it does not hold is simply not written
  to, and the count is capped at `cfg.LeadsPageMax`.
- **`POST /maps/outreach/status` takes the prompt version from the server, not
  the client.** A client marking "sent" means the draft it is looking at, and a
  draft's version is now composed from the model *and* the rule file it was
  written under — so the row the client means is the newest draft that company
  has on that channel, which the store resolves.
  `store.ErrOutreachStatusInvalid` → 400, `store.ErrOutreachNotFound` → 404.
- **The `/settings` routes serve the operator, not the machine.** `GET
  /coding-models` and `GET /llm/providers` publish constants the binary ships;
  `GET`/`PUT /settings` and the two rule routes read and write files the
  operator owns. A route that let a client change a timeout or a token ceiling
  would be an SD-1 violation — one that lets them choose which model their own
  outreach spends, and how it is written, is the opposite.
- **A saved model selection goes through the same allow-list as a per-run one**
  (`leadgenSelection` → `llmSelection`). More carefully, not less: both strings
  become argv to a subprocess, and a saved value is spent by every run
  afterwards without anybody re-reading it. A saved pair that a later build
  retired is dropped back to class routing rather than failed at the subprocess.
- **A card's model is written onto the card, not re-read per call**
  (`POST /catalog/rewrite`). The pass used to resolve the settings file at each
  model call, so a settings change made while it ran could move it mid-pass —
  the schema gate cleared under the old value and the first draft call landed
  on the new one. The pair is stored in the card's `params`, and a card that
  names none still follows the saved choice when it starts, which is what keeps
  a week-old backlog card answering to today's default. Absent, deliberately,
  is lead-gen's trick of folding the saved pair in at the door: a search is over
  in a minute and a catalog card is not.
- **The refusal is asked about the selection that will actually be spent**
  (`catalogModelRefusal(effectiveSelection(sel))`). A gate that asked about the
  machine default while the card named a provider of its own would refuse the
  very choice the operator made to get past it — and the import view's
  `rewrite_blocked` stays the *saved* model's answer, because that is what a
  card with no pair of its own gets.
- **No new path parameter.** Every one of these routes takes its identifiers in
  the JSON body, keeping "a filesystem path is accepted at exactly one route"
  and "no path parameter anywhere else" intact.

The library's Origin check is disabled deliberately: it defends servers whose
auth is ambient (a cookie the browser attaches for you). Ours is a bearer token
a foreign page cannot obtain, already checked before the handler runs — and the
real client is a Tauri WebView whose origin is not this host, so the check would
reject exactly the caller this exists for.

## Auth model

One operator, one machine, one process. The parent (Tauri) mints a per-launch
token and passes it via `MIMIR_DAEMON_TOKEN`; `config.ValidateDaemon` refuses to
start without one. Comparison is constant-time.

The token arrives in `Authorization: Bearer …`, or — for a WebSocket, where the
browser API cannot set headers — as the `mimir.bearer.<token>` entry in
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


## The brain routes (task-51)

Seven routes behind three separately-gated `Deps` fields, because they fail
separately: `BrainScan` is a daemon-lifetime loop, `BrainGraph` is three store
reads, `Brain` is the node core.

- **`?project=` is an opaque id, never a path.** The rule above — a filesystem
  path is accepted at exactly two routes, both of which mint an id on the spot —
  is the whole reason `brain.ProjectID` exists. A value that looks like a path
  is a 400 with a message saying where ids come from, and there is a test whose
  only job is to keep that true. Returning a path in a response is fine;
  `GET /projects` already does.
- **Progress is polled, not streamed.** `/ws/runs/{id}` exists to stitch a lossy
  bus to a complete transcript by `Seq`, because a run emits events that must
  not be lost. A scan emits none: it has one current state, fully described by
  the latest snapshot, and nothing to replay. A socket here would spend its life
  idle and would need either a synthetic run id or a second bus.
- **The three scan controls take no body.** `decodeJSON` sets
  `DisallowUnknownFields`, which turns an empty body into an EOF and would 400
  every click. There is nothing to configure about a scan (SD-1).
- **`scan/now` while paused is a 409**, not a silent start: the operator stopped
  it on purpose and the button was drawn before that.
- **The graph never returns an edge whose endpoints are not both in its node
  list.** The store guarantees it and the handler checks it anyway — the thing
  that breaks otherwise is a canvas in the desktop app, where the failure is a
  blank screen with no message.
