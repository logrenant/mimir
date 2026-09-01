# AGENTS.md — desktop/

The Tauri shell and its screens. The Rust half is a process supervisor; the
React half is a client of one HTTP surface. Neither is a place to put logic that
belongs in Go. Screens: `Connection` (the handshake), `Workspace` (the
coding-task runner), `Leadgen` (the Maps pipeline). `App` gates on the handshake
and then tab-switches between the last two.

## Rules for this directory

- **The parent owns the plumbing.** The Rust shell picks the port (binds
  `127.0.0.1:0`, reads it, drops it), mints a 32-byte token per launch, and
  passes both to `goat-daemon` in its environment. **Nothing reads either value
  back out of the child's output.** goat v1 scraped `GOAT_PORT=<n>` from stdout;
  the daemon side made that impossible (SD-4), and this side must not undo it.
  The child's stdout is drained and dropped; its stderr is logged and kept only
  as a failure message.
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
- macOS only. Packaging (M7): `tauri.conf.json` builds `app` + `dmg` with
  hardened runtime and `entitlements.plist` (JIT + loopback client/server + a
  narrow library-validation allowance for the signed sidecar). Signing and
  notarization are **env-driven, never in config** — `APPLE_SIGNING_IDENTITY`,
  and one of (`APPLE_ID` + `APPLE_PASSWORD` + `APPLE_TEAM_ID`) or
  (`APPLE_API_KEY` + `APPLE_API_ISSUER` + key file) at `tauri build` time.
  `signingIdentity` is `null` in config so a build with no certs still produces
  an ad-hoc-signed app. No auto-update.

## Reviewer focus

Does anything parse the child's stdout? Does the spawn environment still carry
exactly two variables? Can `daemon_request` be pointed at a non-daemon host? Is
the token anywhere but Tauri IPC and the socket subprotocol? Does any screen
keep a filesystem path after registration? Does the reducer still dedupe on
`seq` and match results by `call_id`? Does the lead-gen screen force
`gap_analysis` on when `emails` is set, and does it ever send a prompt version?
