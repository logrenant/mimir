# AGENTS.md — desktop/

The Tauri shell and its screens. The Rust half finds the daemon and owns the
menu-bar surface; the React half is a client of one HTTP surface. Neither is a
place to put logic that belongs in Go.

Two windows, one bundle: **main** (`Connection` → `Dashboard`, whose sidebar
switches between `Workspace` and `Leadgen`) and **quick** (`QuickTask`, the
menu-bar window). `src/main.tsx` picks the root component from the window label.

The app is an **accessory**: no Dock icon, a menu-bar item instead. Closing a
window hides it; only the tray's "Quit Mimir" exits, and quitting never stops the
daemon.

## Rules for this directory

- **Attach before spawning; the two modes never overlap.** If
  `~/Library/Application Support/mimir/endpoint.json` exists, launchd owns a
  daemon and this shell connects to it — it never spawns, and never kills it on
  exit. Only with no endpoint file does the shell become the parent. Two daemons
  against one SQLite store would contend for its write lock, so the presence of
  that file decides and nothing races it.
- **The endpoint file is an input, not a fact.** It carries a live credential,
  so a mode that is group- or world-readable is refused rather than read, and a
  `base_url` that is not `http://127.0.0.1:` is refused rather than called —
  the same lesson as `daemon_request`'s path guard.
- **The parent owns the plumbing.** When this shell *is* the parent it picks the
  port (binds `127.0.0.1:0`, reads it, drops it), mints a 32-byte token per
  launch, and passes both to `mimir-daemon` in its environment. **Nothing reads
  either value back out of the child's output.** goat v1 scraped `MIMIR_PORT=<n>`
  from stdout; the daemon side made that impossible (SD-4), and this side must
  not undo it. The child's stdout is drained and dropped; its stderr is logged
  and kept only as a failure message.
- **The spawn environment is exactly two variables** — `MIMIR_DAEMON_PORT` and
  `MIMIR_DAEMON_TOKEN`. Every other value the daemon uses is a constant in
  `internal/config`. Adding a third here would create the runtime knob SD-1 says
  we do not have, and `MIMIR_STORE_PATH` in particular is a test-only override:
  the store path is computed, not configurable.
- **REST goes through Rust, not `fetch`.** This is forced, not stylistic. A
  cross-origin `fetch` carrying `Authorization` is preflighted, and the daemon
  answers `OPTIONS` with 401 because it has no CORS headers on purpose ("No CORS
  headers, ever" — `internal/api/api.go`). Adding CORS would let any web page
  that guesses the port read the replies. So `daemon_request` performs the call
  from Rust, and the token stays out of the WebView for every REST path.
- **`daemon_request` must stay a daemon proxy.** It accepts a path (not a URL),
  GET and POST only, and attaches a live credential. Without the path check it
  is an open proxy the WebView can point at anything on the machine's network —
  the same lesson as `deploy/playwright-maps`'s host allowlist.
- **The WebSocket is the one thing the WebView does itself**, because a browser
  socket cannot set headers and its handshake is exempt from preflight. The
  token rides `Sec-WebSocket-Protocol` as `mimir.bearer.<token>`, never a query
  parameter: URLs land in logs, history and referrers, and this token starts
  coding sessions with file tools.
- **A path crosses to the daemon exactly once.** The picker's absolute path goes
  to `POST /projects` and is not retained afterwards; every later call carries
  only `project_id` (`docs/ROADMAP.md` §B.4). A screen that keeps the path and
  re-sends it has quietly reopened goat v1's `roots: ["/"]` hole.
- **All daemon access lives in `src/lib/daemon.ts`.** One place that knows the
  token exists, one place that decodes the error envelope. No other module may
  call `invoke("daemon_request")` or open a socket by hand.
- **Deltas are suffixes.** `internal/events` sends the new fragment, not the
  accumulated message, and every event carries a per-run `seq`. `reduceRun`
  appends and drops `seq <= lastSeq`; a duplicated delta is a corrupted message,
  not a cosmetic glitch. Tool results pair with calls by `call_id`, never by
  position.
- **Show what the daemon says.** `internal/project`'s path guards and the
  diagnostics dependency details are already written for a human; render them
  verbatim rather than paraphrasing them into something vaguer. The lead-gen
  screen does the same with `Report.notes` — the pipeline's per-stage
  diagnostics are shown as written, not summarised away.
- **The lead-gen screen mirrors the pipeline's contract, it does not reinvent
  it.** `emails` implies `gap_analysis` in `internal/leadgen`, so the "Analyze
  category gaps" checkbox is forced on and disabled whenever "Draft outreach
  emails" is checked. Marking an email `sent`/`skipped` is optimistic: the row
  updates locally after `POST /maps/emails/status` returns, and the server owns
  the prompt version — the screen never sends one.
- **Versions are pinned exactly** in `package.json` and `Cargo.toml` — no `^`,
  no `~` (SD-5). The Tauri image and library versions must match each other.
- **`make check` stays Go-only.** The desktop gate is `make desktop-check`
  (typecheck, vitest, `cargo fmt --check`, `cargo clippy -D warnings`,
  `cargo test`), so a Go contributor with no Node or Rust can still work here.
- **The quick window is a front door, not a second app.** It reuses
  `lib/daemon.ts`, `lib/runStream.ts` and the same reducer; what is specific to
  it — which project a summon lands on, which keystroke sends, what the
  finishing notification says — lives in `lib/quickTask.ts` as pure functions
  with tests, not inside the component. The window is hidden and shown, never
  created and destroyed: it must appear instantly, and a half-typed prompt has
  to survive a dismissal. Dismissing is not cancelling — the run keeps
  streaming, and the notification is what closes the loop.
- **The global shortcut and the login item are Rust-side and not exposed to the
  WebView.** ⌘⇧G failing to register (another app owns it) is a degraded
  feature, not a failed launch: the tray menu opens the same window.
- macOS only. Packaging (M7): `tauri.conf.json` builds `app` + `dmg` with
  hardened runtime and `entitlements.plist` (JIT + loopback client/server + a
  narrow library-validation allowance for the signed sidecar). Signing and
  notarization are **env-driven, never in config** — `APPLE_SIGNING_IDENTITY`,
  and one of (`APPLE_ID` + `APPLE_PASSWORD` + `APPLE_TEAM_ID`) or
  (`APPLE_API_KEY` + `APPLE_API_ISSUER` + key file) at `tauri build` time.
  `signingIdentity` is `null` in config so a build with no certs still produces
  an ad-hoc-signed app. No auto-update. The bundle target is `app` only — a
  `.dmg` needs Finder automation permission for its AppleScript layout step, and
  Mimir is installed by copying the app, not by distributing an image.

## Reviewer focus

Does anything parse the child's stdout? Does the spawn environment still carry
exactly two variables (the launchd plist's `PATH` is launchd's, not this
shell's)? Can an attached shell kill a daemon it did not spawn? Can `daemon_request` be pointed at a non-daemon host? Is
the token anywhere but Tauri IPC and the socket subprotocol? Does any screen
keep a filesystem path after registration? Does the reducer still dedupe on
`seq` and match results by `call_id`? Does the lead-gen screen force
`gap_analysis` on when `emails` is set, and does it ever send a prompt version?

## Terminals, the board, and attachments (task-36)

- **Terminal sessions live in `TerminalsProvider`, mounted above `Dashboard`.**
  A run's socket has to outlive the screen that opened it: switching from
  Terminals to the Board and back must show what happened in between, not a
  panel that starts again. Putting the subscription in the Terminals screen's
  own lifetime would lose it on every navigation.
- **The terminal is not a PTY and must not pretend to be one.** The daemon runs
  `claude` over pipes and parses its stream-json, so what a console here can
  honestly show is that typed stream plus the CLI's stderr. The lines are
  synthesised in `lib/terminals.ts`, which is also why there is no xterm.js:
  there is no ANSI to parse, and the house palette and fonts are the point.
- **Two board columns belong to the operator and three to the runner.** Backlog
  and Queued accept a drop because they are decisions; Running, Done and Failed
  do not because they are facts. `allowedMove` is that rule, and its refusals
  carry a reason — a drop that silently springs back reads as a broken board.
- **`"dragDropEnabled": false` on the main window is load-bearing.** Tauri's
  native file-drop handler otherwise swallows the event and hands back a path
  the WebView could not read anyway: there is no `fs` plugin and none is added.
  With it off, paste, drop and `<input type="file">` all yield a `File`, which
  needs no plugin, no capability and no Rust command.
- **An attachment preview is a `data:` URI.** The CSP allows those and does not
  allow an `http://127.0.0.1` image, so a stored attachment is rebuilt from the
  base64 the daemon returns rather than linked to.
- **`openRunStream` must never fail silently.** Two ways it used to: a browser
  fires `error` then `close`, so the reason set by the first was erased by the
  second; and a socket that ended without a terminal event had no recovery. The
  first non-empty reason now survives, and the watcher falls back to polling
  `GET /coding-tasks/{id}`. Both are covered by `openRunStream.test.ts` — the
  socket had zero tests before, and both bugs were invisible without them.

## Accounts and Recents (task-38)

- **An account is a credential slot, and the app says so.** Registering one is
  pointing at a directory — the same shape as registering a project, and the
  path leaves the app exactly once. The app never sees a credential, and
  "unut" forgets a slot rather than logging anybody out. Do not add a login
  flow here: signing into a slot is `CLAUDE_SECURESTORAGE_CONFIG_DIR=<dir>
  claude auth login`, and the manager says that in plain text.
- **`claude auth status` is free, so the identity shown is live.** No API call,
  no tokens. Caching it at registration would mean a slot whose login has
  lapsed still reads as healthy, which is exactly the failure the row exists to
  surface.
- **Two slots can be the same person, and the rows will not say so.** A slot is
  a directory hashed into a keychain entry name; registering one and signing it
  into the account already in use gives two entries, two green rows, and one
  rate limit. `identityClashes` compares the live probes and says it out loud —
  verified on this machine, where both slots resolved to the same address.
- **"Otomatik" is the default and stays first in the select.** A pin is the
  exception; automatic assignment across slots is what makes a second account
  worth registering at all.
- **Recents is not a second store.** A finished run still has its transcript
  and the socket replays it, so opening one is the same operation as opening a
  live one — `recentRuns` is the board's own list minus what is already open.
  Backlog and queued runs are excluded: there is nothing to replay, and an
  empty console reads as broken. It fetches on expand, not on a timer, because
  the daemon has no cross-project run route and this is an N+1 fan-out.

## The dashboard and the model picker (task-40)

- **One poll loop for the whole app: `RunsProvider`.** The daemon has no
  cross-project run route, so a board is one `GET /coding-tasks` per registered
  project. That fan-out is affordable once and wasteful twice — the dashboard
  and the board read the same array. A new screen that shows runs subscribes to
  it; it does not start a timer.
- **A running job gets a terminal without being asked.** `adopt` runs from the
  poll loop, because that is the only thing that sees a card the dispatcher
  released or a task started from the menu bar. Only `running` is adopted:
  adopting `queued` would open a console per released card with nothing to
  print.
- **Closing a tab has to stick.** The provider remembers dismissals or the next
  poll reopens them and the close button appears not to work. The set is keyed
  by run and cleared when the run ends — a dismissal is about that run, not
  about that id forever.
- **The mini console is a tail of the same `Session`, not a second
  subscription.** One socket per run whichever screen is drawing it.
- **Dependency health belongs on the first screen.** Crawl4AI was down for a
  whole session while `/diagnostics` had been saying so — in a sentence that
  even names the command. The strip shows that sentence verbatim; do not
  paraphrase it, and do not add a button that runs it. Starting a container is
  the operator's call on their own machine.
- **The model list comes from `GET /coding-models`.** Never hardcode model ids
  here: the daemon validates against its own allow-list and answers 400, so a
  local copy would start offering options the daemon rejects the first time a
  generation ships.
- **The model select has no "automatic" entry.** The operator asked to be able
  to tell the models apart, and a card that says "default" tells them apart
  from nothing; the daemon's default is the option that starts selected. The
  empty string still means "you decide" on the wire, for clients written before
  the picker.

## Brain tab (task-52)

- **Brain is a tab, not a module.** `MODULES` is "one entry per screen wired to
  a real daemon route", and Brain qualifies — but it is the store every module
  writes into rather than one more thing the daemon can do, so it sits with
  Genel / Board / Terminals.
- **The layout is ours.** `lib/brainGraph.ts` is a seeded Fruchterman–Reingold
  with grid-approximated repulsion, and it is pure: positions in, positions out,
  tested. Five dependencies is the whole front end; a graph library would be a
  sixth, and a layout nobody can unit-test is one that drifts. The repulsion is
  approximated because the honest O(n²) pass at three thousand nodes is nine
  million pairs a tick, which is a frozen window.
- **Four colours here too.** The obvious thing is a hue per node kind, the way
  every knowledge-graph screenshot does it. The kinds are separated by *role*
  instead — Electric for what a person decided, Lime for what the machine
  recorded, Mist for files — and by size, which is degree. Eleven hues would say
  these categories are unrelated; the picture is one body of knowledge.
- **Labels are for hubs only.** Every node labelled is the screenshot everyone
  has seen and nobody can read.
- **The status is polled, adaptively** (2 s scanning, 30 s idle) in the screen's
  own hook. `RunsProvider` is the board's shared poll and nothing else reads the
  scan, so joining it would be coupling for its own sake.
- **The scan's error text is shown verbatim**, like every other daemon message.
- **The project filter is the id the daemon handed over**, never a path — the
  daemon rejects a path here on purpose.
