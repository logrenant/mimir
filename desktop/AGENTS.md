# AGENTS.md — desktop/

The Tauri shell and its screens. The Rust half finds the daemon and owns the
menu-bar surface; the React half is a client of one HTTP surface. Neither is a
place to put logic that belongs in Go.

Two windows, one bundle: **main** (`Connection` → `Dashboard`, whose sidebar
switches between `Workspace` and `Leadgen`) and **quick** (`QuickTask`, the
menu-bar window). `src/main.tsx` picks the root component from the window label.

The app is an **accessory**: no Dock icon, a menu-bar item instead. Closing a
window hides it; only the tray's "Quit GOAT" exits, and quitting never stops the
daemon.

## Rules for this directory

- **Attach before spawning; the two modes never overlap.** If
  `~/Library/Application Support/goat-mcp/endpoint.json` exists, launchd owns a
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
  launch, and passes both to `goat-daemon` in its environment. **Nothing reads
  either value back out of the child's output.** goat v1 scraped `GOAT_PORT=<n>`
  from stdout; the daemon side made that impossible (SD-4), and this side must
  not undo it. The child's stdout is drained and dropped; its stderr is logged
  and kept only as a failure message.
- **The spawn environment is exactly two variables** — `GOAT_DAEMON_PORT` and
  `GOAT_DAEMON_TOKEN`. Every other value the daemon uses is a constant in
  `internal/config`. Adding a third here would create the runtime knob SD-1 says
  we do not have, and `GOAT_STORE_PATH` in particular is a test-only override:
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
  token rides `Sec-WebSocket-Protocol` as `goat.bearer.<token>`, never a query
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
  GOAT is installed by copying the app, not by distributing an image.

## Reviewer focus

Does anything parse the child's stdout? Does the spawn environment still carry
exactly two variables (the launchd plist's `PATH` is launchd's, not this
shell's)? Can an attached shell kill a daemon it did not spawn? Can `daemon_request` be pointed at a non-daemon host? Is
the token anywhere but Tauri IPC and the socket subprotocol? Does any screen
keep a filesystem path after registration? Does the reducer still dedupe on
`seq` and match results by `call_id`? Does the lead-gen screen force
`gap_analysis` on when `emails` is set, and does it ever send a prompt version?
