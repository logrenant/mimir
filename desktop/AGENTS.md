# AGENTS.md — desktop/

The Tauri shell and its screens. The Rust half finds the daemon and owns the
menu-bar surface; the React half is a client of one HTTP surface. Neither is a
place to put logic that belongs in Go.

Two windows, one bundle: **main** (`Connection` → `Dashboard`, whose sidebar
switches between the board, `Terminals`, `Brain`, `Settings` and the modules —
`Workspace` and `Leadgen`) and **quick** (`QuickTask`, the menu-bar window).
`src/main.tsx` picks the root component from the window label.

The app is a menu-bar app, and its activation policy follows the main window:
**accessory** (no Dock icon) whenever that window is away, **regular** while it
is on screen. Fixed at accessory it had no menu bar at all, so a fullscreen
window revealed nothing at the top of the screen — no clock, no menu-bar items
(`src-tauri/src/macos.rs`). Closing a window hides it; only the tray's "Quit
Mimir" exits, and quitting never stops the daemon — it does sign Mimir's Claude
account out, which is a different thing and is described below.

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
- **Quitting signs the Claude account out** (`daemon::sign_out`, on
  `RunEvent::Exit`). The daemon does the same on its own shutdown and again on
  its next start; this is the third of the three, and the only one that covers a
  launchd-owned daemon that never stops when the window does. It is a blocking
  `POST /accounts/reset` on the way out and its failure is swallowed — there is
  nowhere left to show an error, and the daemon's start-up reset makes a missed
  call harmless.
- **REST goes through Rust, not `fetch`.** This is forced, not stylistic. A
  cross-origin `fetch` carrying `Authorization` is preflighted, and the daemon
  answers `OPTIONS` with 401 because it has no CORS headers on purpose ("No CORS
  headers, ever" — `internal/api/api.go`). Adding CORS would let any web page
  that guesses the port read the replies. So `daemon_request` performs the call
  from Rust, and the token stays out of the WebView for every REST path.
- **`daemon_request` must stay a daemon proxy.** It accepts a path (not a URL),
  one of five verbs, and attaches a live credential. Without the path check it
  is an open proxy the WebView can point at anything on the machine's network —
  the same lesson as `deploy/playwright-maps`'s host allowlist. The verb list is
  an allowlist, not a pass-through: GET, POST, DELETE (a board card can be
  thrown away), PUT (the routes that replace a whole document — the scan policy,
  the settings values, a rule file) and PATCH (the one that amends a card). It
  was GET/POST/DELETE for longer than it should have been, which made
  `editCodingTask` and `saveBrainScanPolicy` fail at the proxy with "unsupported
  method" while the TypeScript side had already declared both — a verb added to
  `RequestOptions` and not to the Rust match is a route that cannot be called.
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
  emails" is checked. Marking a draft `sent`/`skipped` is optimistic: the row
  updates locally after `POST /maps/outreach/status` returns, and the server
  owns the prompt version — the screen never sends one.
- **The model picker is a view of `GET /llm/providers`, never a second copy of
  it.** The daemon owns the provider/model allow-list and rejects a pair that is
  not in it, so a list hard-coded here would drift the first time a generation
  ships and start offering combinations the run route then refuses. If the route
  does not answer, the picker is not rendered at all — an empty dropdown would
  suggest there is nothing to choose, while its absence correctly says the
  choice is not on offer and the run still works. Two controls rather than one
  flat list of pairs, because the provider is the decision that matters (free
  tier or your Claude quota) and burying it makes the expensive choice as easy
  to make by accident as the free one. Switching provider sets the model to that
  provider's `default_model` explicitly: the model control has no empty option,
  so a cleared value would display whichever model is listed first while the run
  spent a different one.
- **The lead-gen screen opens on the ledger, not on a run (task-64).** A run
  lives for as long as the tab does; the ledger is what every earlier run left
  behind, so with nothing in state the screen reads `/maps/leads` rather than
  drawing an empty panel. Its filtering happens on the daemon, not in the
  browser: the ledger is bigger than a page, and filtering the rendered page
  would quietly mean "search the two hundred rows you happen to be looking at".
  The category rail is counted by the daemon for the same reason — a rail
  derived from the page would describe the page.
- **A node's history rides its detail (task-68).** `GET /brain/nodes/{id}`
  carries a bounded `versions` array, so the panel does not fetch a list that is
  empty for most files on the machine. The timeline is hidden below two entries:
  a file read once has a history of exactly what is already on screen above it.
  There is no diff — no file content is stored, and inventing one from two
  assessments would be a picture of something that never happened.
- **AppKit gaps are `src-tauri/src/macos.rs`, and there are two.** A window only
  enters native fullscreen when it carries `FullScreenPrimary`, which tao never
  sets — without it the green traffic light silently falls back to zoom. And the
  menu bar a fullscreen window reveals is the *active app's*, which an accessory
  app does not have — hence the policy following the main window. Both are
  reached through the NSWindow/NSApplication underneath because Tauri exposes
  neither; `objc2-app-kit` is a macOS-only dependency and stays that narrow.
- **A pasted image reaches the shell terminal as a keystroke, not an upload
  (`lib/shellPaste.ts`).** xterm cannot type an image, but the pty is on this
  same Mac and `claude` reads the system pasteboard itself on ^V — so ⌘V with an
  image forwards that keystroke and the CLI picks the bytes up. Text pastes are
  left to xterm: to a shell that is not reading an image, ^V is quoted-insert.
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
Does any screen offer a choice of Claude account, or open the login page itself
instead of letting the daemon do it?

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
- **"devam et" is offered against a session id, never against a status.** A run
  that failed before the CLI announced itself has nothing for `--resume` to
  attach to, so `canContinue` (board) and `isResumable` (terminal) both check
  `session_id` and the bar says "sürdürülemez" rather than showing a button that
  would quietly start the task over. The id is learned from the first event that
  carries one and never unset by a later event that does not — most of them do
  not, so a plain assignment would erase it one line after it arrived.
- **A retry says whether it will start or wait, before the click.** Capacity is
  one run at a time, so pressing either button while something is in flight
  lands the card in Queued. That is correct behaviour and invisible behaviour —
  a card that jumps to Queued and sits there reads as a failure to start — so
  `retryOutcome`/`isBusy` drive a line in the bar and a hint on the buttons.
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

## The account and Recents (task-38, single-account since)

- **There is one Claude account and the app never lets you pick.** No account
  select in a composer, no list, no pin: `AccountSelect` is gone, and
  `account_id` is not sent. A picker over one thing is a choice that is not
  really on offer, and the daemon would refuse anything else anyway.
- **It is Mimir's own credential slot, not the operator's.** That is what makes
  signing out on quit safe — the `claude` in their own terminal is a different
  keychain entry and keeps its login. The panel says so, because "Mimir logged
  me out" is the wrong conclusion to leave available.
- **Connecting is a login the daemon runs, and the app follows it.** `AccountPanel`
  POSTs `/accounts/login`, then polls `GET /accounts/login` while the state is
  `opening`/`waiting`/`code`. It does not open a browser itself and must not
  start to: the daemon opens the private window, because it is the side that
  knows the URL and owns the CLI process waiting on the callback.
- **The panel joins a login in flight rather than starting a second.** The flow
  lives on the daemon, so mounting the screen reads `GET /accounts/login`
  first — two `claude auth login` processes on one slot race for the same
  keychain entry.
- **The `code` state is a fallback, not the normal path.** It appears only when
  the CLI could not open a browser itself. Do not make it the primary shape of
  the UI; the ordinary flow finishes in the browser with nothing to type.
- **The CLI's own output is shown verbatim**, like every other daemon message.
  A login that failed says why in words already written for a human.
- **`claude auth status` is free, so the identity shown is live.** No API call,
  no tokens. Caching it at login would mean an account whose session has lapsed
  still reads as healthy, which is exactly the failure the row exists to
  surface.
- **The connect flow lives on the first screen, in `HESAP`.** Nothing runs
  without an account and the account is gone at every launch, so burying it
  behind a tab would make the app's first state one the operator cannot leave.
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

## Outreach and the settings screen

- **Ticking companies is the point; drafting for everyone is the fallback.**
  `POST /maps/outreach` takes place ids, so the screen's job is to let the
  operator build that list and to say what it will cost before they spend it.
  The search bar's "bulunan herkese e-posta taslağı yaz" checkbox still exists
  and still means every company the search returned — it is named that way now
  because next to a checkbox column the old label read as "draft for the ones I
  picked".
- **The selection is a set of place ids, never a flag on a row.** The table is
  filtered and paged on the daemon, so a row that scrolls out of the filter is
  still a company the operator chose. A boolean on the row would silently drop
  those the moment the filter changed. The cost of that choice is that the
  selection can outrun what is on screen, so the bar states the off-screen count
  rather than hiding it — `summarize` in `lib/leadgen.ts`.
- **The header box acts on the rows in the table, never on the ledger.** It sits
  above those rows, and an "all" that quietly meant four thousand rows is the
  most expensive misreading available on this screen. It is tri-state for the
  same reason: a box that could only be on or off would show "off" while forty
  rows below it were ticked.
- **The bar prints messages, not companies.** One model call per company per
  channel is what is actually spent, and `12 şirket · 24 mesaj` is the number
  worth putting on the button. `alreadyDrafted` is what protects against paying
  twice — not clearing the selection, which is kept on purpose because the
  common next move is "now do WhatsApp for the same twenty".
- **A decision is per channel.** `sent` on the email and `skipped` on the
  WhatsApp line is an ordinary thing to decide, so the drafts view has channel
  tabs, `draftQueue` takes a channel, and the status routes carry one.
- **Nothing is sent from this app.** There is no mail account and no WhatsApp
  session here. "WhatsApp'ta aç" and "E-postada aç" hand the text to whatever
  the operator already uses; marking "gönderildi" stays their decision rather
  than a side effect of a click. `whatsappHref` returns `undefined` for any
  number shape it cannot be sure about — a wrong guess opens a chat with a
  stranger, and the plain number the panel already shows is a fine fallback.
- **Configuration lives on the settings screen, not on the screen that spends
  it.** The model picker was on the lead-gen search bar, where it was a per-run
  choice whose state nobody could see afterwards; which model a campaign spends
  is a decision about the campaign. `LeadgenRequest` therefore carries no
  `provider`/`model` at all — the daemon reads the saved default behind the
  route (`api.leadgenSelection`), and leaving the fields off the type is what
  stops a second picker reappearing on the search bar. The Brain scan keeps its
  own picker: that one routes a single sweep, which is a different decision.
- **One save for the whole settings page.** Electric is the CTA colour and a
  screen gets one — but the real reason is that a settings page with three
  separate saves is a page where you find out later which one you forgot. The
  bar appears only when something is unsaved, names what it is about to write,
  and ⌘S reaches it from inside the textarea.
- **The rule editors say what saving costs.** A rule file is part of the
  drafting prompt and therefore part of the cache key, so editing one
  invalidates every draft written under the old text — including ones already
  marked "sent". That is the honest trade (the alternative is showing a draft
  the current rules never produced) and it is stated on the screen where the
  edit happens, in `RULE_CACHE_WARNING`, rather than discovered afterwards.
- **The rule file's path is shown.** The operator may well prefer their own
  editor, and a rule file whose location is a secret is a rule file nobody
  trusts. "Varsayılana dön" is `POST /settings/rules/reset` rather than this
  client sending a copy of the shipped text — a client holding that copy is
  exactly the drift the published lists exist to avoid.

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
