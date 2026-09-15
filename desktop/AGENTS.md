# AGENTS.md — desktop/

The Tauri shell and its screens. The Rust half finds the daemon and owns the
menu-bar surface; the React half is a client of one HTTP surface. Neither is a
place to put logic that belongs in Go.

Two windows, one bundle: **main** (`Connection` → `Dashboard`, whose sidebar
switches between the board, `Terminals`, `Brain`, `Settings` and the modules —
`Workspace` and `Leadgen`) and **quick** (`TrayPanel`, the menu-bar panel).
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
- **Quitting does not sign the Claude account out.** `daemon::shutdown` signals
  the child and nothing else; the `sign_out` that used to `POST /accounts/reset`
  on `RunEvent::Exit` is gone. The slot is Mimir's own, the keychain keeps the
  login, and the daemon reconciles the slot against it on its next start
  (`Restore`), so the operator connects once. Do not add a sign-out back here:
  it would put the login behind a window close again, which is exactly the cost
  this removed.
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
- **Six dependencies, and the sixth was argued for.** This file used to say
  five was the whole front end, and Brain's layout was hand-written rather than
  take a sixth. `framer-motion` is the sixth (pinned `13.2.0`), taken for the
  three things CSS cannot do here: a shared element that *travels* between two
  positions (`layoutId` — the sidebar rail, the tab indicator), an exit
  animation for something being unmounted (`AnimatePresence` — overlays, screen
  swaps), and a board card settling into a column it was dropped in (`layout`).
  Every one of those is motion answering an action. The rule the five stood for
  still holds: it did not buy a component library, the graph layout is still
  ours, and the bundle came out *smaller* than before it, because deleting
  `hover.tsx` and its inline-style objects cost more bytes than it added.
- **`make check` stays Go-only.** The desktop gate is `make desktop-check`
  (typecheck, vitest, `cargo fmt --check`, `cargo clippy -D warnings`,
  `cargo test`), so a Go contributor with no Node or Rust can still work here.
- **The menu-bar panel is anchored to the icon, never centred (task-94).** It
  is the app's one permanent surface, and it is *one* surface: the native
  `NSMenu` that used to carry six rows is gone (it draws in the system's grey,
  the system's face and the system's separators, and can draw nothing else),
  and the 680×460 box that appeared in the middle of the screen is gone with
  it. A panel in the centre of the display belongs to nothing and takes
  everything behind it out of context; a panel hanging off the icon that
  summoned it is the whole idiom. `quick.rs` positions it from the tray
  event's rect, caches that anchor so ⌘⇧G lands in the same place, and clamps
  it to the screen.
- **The lifeboat menu on right-click is not decoration.** Two rows — Restart
  daemon, Quit Mimir — drawn by AppKit. This app is an accessory: no Dock
  tile, no window of its own. If the panel's WebView wedges (and `liveness.rs`
  exists because they do), a Quit drawn by that WebView is a Quit that cannot
  be reached, and the app cannot be quit at all. Never remove it to tidy up.
- **The panel closes when you look away.** A menu dismisses on the first click
  outside it, and an `alwaysOnTop` panel that did not would hang over whatever
  the operator turned to next. The click on the icon is the case that reveals
  the bug: macOS blurs the window *before* the tray event arrives, so a
  visibility check alone reads "closed, open it" and the icon refuses to close
  its own panel. `quick::hide_from_blur` timestamps the dismissal and
  `toggle_at` reads it.
- **The handshake is silent when it succeeds (task-94).** `Connection`'s
  argument still holds — nothing that talks to the daemon renders until the
  daemon answers — but the daemon answers on the loopback in about two hundred
  milliseconds, and a full-screen splash with a Continue button was standing in
  front of it every launch. `lib/shell.ts` grades the wait: nothing, then one
  line, then the honest failure surface with stderr and a retry. The brand's
  own surface is spent on the failure, which is the state that actually has
  nothing to do on it.
- **A card that owns an object's lifecycle owns its own writes; the save bar
  owns values (task-100).** "One save for the whole settings page" survives
  contact with "add a connection" only if the boundary is stated. Adding,
  removing, probing or re-keying a connection hits the daemon on click — the
  same exception `SkillsCard` already takes — because those create or destroy a
  resource and cannot be drafted: a "test" button testing something not yet
  saved would be testing nothing. The lead-gen model choice, the two class
  defaults and the rule bodies stay on the bar. The section rail marks only the
  sections the bar owns.
- **Two rules for one question is how the save bug happened (task-100).**
  `anyDirty` lived in a tested lib file while the class-default comparison was
  four inline expressions in JSX, so the bar armed on one rule and `save()`
  branched on the other: editing only a class default wrote nothing and reported
  success. Screen logic belongs in `lib/` **and there is only one copy of it.**
- **One provider picker, and it knows what this machine has (task-98).**
  `ProviderModelPicker` is built from `GET /llm/providers`, and that route
  publishes both the pinned table *and* what is installed here. A provider whose
  CLI is missing is not offered — offering it is a failure with an extra click
  in front of it — and one that is installed but signed out is offered with the
  CLI's own sentence against it. Availability being **absent** (a daemon with no
  router) is not the same as "nothing is installed", so nothing goes dark over a
  missing field. Four copies of this control would be four screens sending four
  different requests.
- **The expensive question is only asked when somebody asks it.** "Is it
  installed" is `--version` and free, so a screen opening sends it. "Does the
  login work" is one completion per provider, so it is behind a button.
- **The panel is a conversation, and the order is the argument (task-96).** The
  composer was a form — a text box and two dropdowns — and its problem was never
  the layout, it was the *order*: folder and model are two questions asked
  before the work has been written down, and both usually have an answer that
  falls out of the work itself. The wizard asks for the work first, reads the
  folder out of the sentence, and asks only for what it genuinely cannot know.
- **An inference is always said out loud.** `inferProject` names the folder it
  matched *and the word it matched on* ("mimir-agent'ta çalışacağım"), because
  an inference nobody can see is an inference nobody can correct. It is
  deterministic and costs no model call: spending a call to work out what a task
  should cost is spending before the operator has agreed to spend anything.
  The matching rule is Turkish-shaped — `toLocaleLowerCase("tr")` so `İçerik`
  matches, an apostrophe ends a name so `api'de` matches, and a bare letter does
  not, so a project called `test` is not dragged in by "testleri düzelt".
- **Two questions are deliberately not asked.** The *agent* is answered by the
  daemon's own router (task-77); asking here would be a second, worse copy of
  that decision. The *model*'s answer is almost always the default — but it is
  the operator's money, so it is offered as a chip on the confirm step rather
  than hidden. `defaultProject` was retired rather than replaced: it landed
  every summon on the most recent folder silently, and the most recent folder is
  now simply the first chip.
- **One keystroke spends money, so it says what it is spending.** The confirm
  step's sentence names the agent, the folder and the model, and it is
  assembled in `lib/wizard.ts` — a promise that drifts from what the request
  actually sends is worse than no promise. Picking a different model restates
  it.
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
  feature, not a failed launch: clicking the menu-bar icon opens the same
  panel. The login-item toggle is now *in* the panel (`autostart_enabled` /
  `set_autostart`), and it reports what the write actually produced rather than
  what was asked for — a toggle that lies is worse than one that does nothing.
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

## The elevation, motion and focus layer (task-76)

The app was drawn in two incompatible dialects: Tailwind against the `@theme`
tokens on about half the files, and inline style strings full of hand-typed hex
through `components/hover.tsx` on the other half. That is where nine button
implementations, six badges, six cards, three hand-built modals, five `<select>`
recipes and fourteen off-palette colours came from — and where two copies of the
console palette came to disagree about what colour a line of model output is.
`hover.tsx` is gone; there is one dialect now, and it is the tokens.

- **Four colours, still — but Lime leads now.** Carbon, Mist, Electric, Lime,
  and no fifth: `#e5a23d` in eleven places and three off-palette hues in the
  Brain graph are gone, and the graph's fills are *derived* from the four by
  `mix()` rather than typed, so there is nowhere left to put one.
  What changed is which accent carries. Electric at `#2547e8` on Carbon reads as
  dark grey — a filled primary button, a 2px rail and a tab underline in it were
  all, in practice, invisible, so a palette with two accents produced screens
  with none. **Lime is the operator's colour**: the one filled button, the
  selected row's rail, a live run, a meter's head, a ticked box. **Electric is
  the machine's**: the focus ring, `Stream`, a queued card. Electric is also
  lifted to `#3d63ff` so that when it does appear it survives the ground.
  Proportions: Carbon 72, Mist 18, Lime 7, Electric 3.
- **Aldrich is display-only, and it does not set Turkish.** It sets a screen's
  name, an overlay's title, the wordmark — 20px and up, and nothing else. Three
  separate things it must not be asked to do, each found by looking at the
  rendered pixels rather than the source:
  - **It never sets a number.** Its zero is a plain rounded rectangle with no
    counter, so an idle dashboard rendered its hero figure and all six cells of
    the day's strip as what look like tofu boxes — the single most common state
    this product has. Numbers take `.figure`: Open Sans, semibold, real `tnum`.
  - **Its `unicode-range` may not claim accented Latin.** The subset does not
    draw it, and it fails the dangerous way: not a fallback, not a missing-glyph
    box, but *the base letter with the mark removed*. Ü and U measure
    identically at 100px; so do Ö/O and Ç/C, while Ş/Ğ/İ fell through to
    whatever the system had, so one word was set in three faces. The range is
    now exactly what the file measurably draws — ASCII plus `· × – … ‘ ’ “ ”` —
    and Open Sans sits directly behind it so every Turkish character is set by a
    face we chose.
  - **Nothing is `text-transform: uppercase` any more.** Blink does not apply
    Turkish casing for it even under `lang="tr"` (verified in this app's own
    WebView), so `i` uppercases to `I` and not `İ`, and the module screen titled
    "Katalog · Ürün içeriği" painted "KATALOG · ÜRÜN IÇERIĞI" — not a Turkish
    word, on the largest text on the screen, produced by a stylesheet silently
    rewriting copy the code had authored correctly. There is no CSS-side fix.
    Display text now says what its source string says, and both `.display` and
    `.label` get their voice from face, size, weight and tracking instead.
- **The corner ramp is 6/10/14/18/full.** Not 4px everywhere. The old rule was
  "infrastructure has no pill buttons", and what it produced was an interface of
  small hard rectangles that read as unfinished rather than as engineered. Pills
  are now correct for one thing — a *track*: the segmented control and the
  status pill, both of which are readings rather than buttons.
- **Depth is tone; elevation is light, not shadow.** The surface ramp is
  `ground` → `panel` → `raised` → `overlay`, and the steps are wide enough that
  **a panel separates by its own tone and does not need a border to be a panel**.
  That is the rule that replaced "every element gets a hairline", which was the
  same as no element getting one. `bordered` survives for the single case tone
  cannot handle: a card on a surface of its own tone.
  On top of that, `--shadow-elev-1` is the inset highlight a real surface
  catches on its top edge — no drop shadow, no glow, the guide's ban intact.
  `elev-2` casts, for a popover; `elev-3` casts further, for a modal over a
  scrim. Nothing else casts.
- **`ui/popover` is for readings; `ui/overlay` is for decisions.** The modal used
  to be the only floating surface, so it was also carrying jobs that are not
  decisions — the title bar's connection pill dimmed the whole application and
  had to be dismissed before work resumed, for four lines of daemon status. A
  status check is not a decision and must not be staged like one. Placement is
  arithmetic in `lib/popover.ts` and tested there, because a panel half off an
  edge is a failure you cannot see from the screen you are looking at. When a
  panel fits on neither side it takes the roomier one: honouring the asked-for
  side literally gave a control near an edge a `maxHeight` of a few pixels,
  which draws as a sliver and reads as a menu that does nothing when clicked.
- **Motion answers an action or reports a real value.** Nothing loops
  decoratively. `Pulse` ticks once per successful heartbeat rather than on a
  timer, so a dot that has gone *still* is the reading — which is why nothing
  else in the app breathes: if everything did, stillness would mean nothing.
  `Stream` exists only while a socket does. `Meter` takes a real fraction. The
  two exceptions are named where they live: the spinner on a busy button and
  the crawl under a running card, both for calls the daemon reports no progress
  on, and both unmounted when the work ends. There is no fade-and-slide-up on
  page load; a screen whose sections animate in on arrival is claiming something
  happened when the data was simply there.
- **A selected thing says so more than once.** The old nav row marked itself
  with `bg-raised` and a 2px Electric rail — six luminance points and a dark
  grey line, so in practice the sidebar did not show which screen you were on.
  Selection now carries a filled surface, a 3px Lime rail *and* the label going
  from `muted` to Mist at medium weight. "Where am I" is the one question a
  sidebar exists to answer and it should not have to be squinted at. The rail is
  still one element shared by `layoutId`, so it slides between rows rather than
  blinking out of one and into another.
- **…and it is a primitive, not a recipe.** The rule above was written and then
  held in eight hand-written copies with four different DOM shapes: `border-lime`
  in Katalog and Lead-gen, `border-l-lime` in Settings, a positioned 3px `<span>`
  in the sidebar and Terminals, `ui/card`'s `accent` prop that nothing called,
  and — in Katalog's product table — **`border-l-electric`**, the exact palette
  mistake this section says was fixed, still standing eight months later.
  `ui/rail` is `Rail` + `RailItem` and it is the only one of those left. Rails in
  different lists take different `layoutId`s, or the travelling element flies
  across the screen from one list to the other.
- **A held-down button is `active`, and the ARIA is not the same as the look.**
  `ui/button` grew `active` and `activeAria`. Before it, "on" existed twice in
  the whole application — `Brain`'s Semboller and Lead-gen's channel pair, each
  hand-writing `variant="secondary"` + `icon="check"` + `aria-pressed` — and
  nowhere else, so Katalog's `sütunlar`, `alanlar` and `marka kimliği` looked
  identical whether their panel was open or shut, which is what an operator
  reported. `active` fills the surface, takes the label to Mist and draws a 2px
  Lime bar along the bottom, the same mark `ui/tabs` puts under the tab that is
  on; a bar rather than a rail because a rail belongs to a row in a vertical
  list and a row of buttons is horizontal. It overrides the variant's own
  surface deliberately — `quiet` is what a toolbar button wears and `quiet` has
  no surface, so an active `quiet` that kept its own background would be saying
  "on" in the label alone. `activeAria` is `"pressed"` for a setting that stays
  on and `"expanded"` for a control that opens something: the look is one thing
  and the announcement is two, and conflating them is how a disclosure ends up
  telling a screen reader it is a toggle.
- **A controlled field never normalises on a keystroke.** The brand panel held
  the *stored* voice — two `string[]`s — and rebuilt it from the textarea on
  every change, so `"Kesinliği "` went through `split`, `trim`, `join` and came
  back `"Kesinliği"`: the space was erased by the keystroke that typed it, and
  `filter(Boolean)` did the same to Enter. The operator's report was "the space
  bar does not work" and they were describing exactly what happened. A form
  holds text (`voiceDraft`), and the split runs once, in `save` (`voiceFromDraft`).
  The same rule guards the key bindings: `lib/keys.ts`'s `isTypingTarget` is
  what a pane binding bare letters or Space has to check, because "the pane
  happens to contain no text field" is where a binding stands, not a rule.
- **Icons are drawn here, not installed.** `ui/icon` is one file, one map, a 16
  unit grid, 1.5 units of stroke, butt caps and mitred joins — the terminals
  Aldrich has. A library would have handed this product the same glyphs as
  several hundred other dashboards. An icon-only control is `IconButton`, whose
  type makes its `label` mandatory, and it is wrapped in `ui/tooltip` so the
  name is there for the eye as well as the screen reader.
  **A glyph replaces a word only when the control acts on the *view*** — scroll
  to bottom, copy the transcript, close the tab. Anything that acts on the
  *run* keeps its word: "Devam et" and "Baştan dene" are one resume and one
  discard, and a guess between them costs real tokens.
- **One primary per context, not per screen.** `primary` is the only filled
  control in the system, and the rule it obeys is "one answer to *what am I
  here to do*". A screen is one context and a dialog is another — and so is a
  card in a list of independent things, which is why the board carries one
  filled "Çalıştır" per runnable card. The test is whether the operator is
  choosing *between* them: they choose one card, then its verb.
- **A card gets one verb and a menu.** `RunCard` had up to eight buttons in a
  wrapping flex, which on a 210px column wrapped to three rows — nine cards
  meant about forty controls, all the same weight, so "sil" looked as likely a
  destination as "run". `primaryAction()` ranks the legal verbs and the rest go
  to `ui/menu` behind a `more` glyph. `lib/board.ts#actionsFor` is still the
  only authority on which verbs are *legal*; the ranking never adds one.
- **`ui/picker` where an option has something to say; `ui/field`'s `Select`
  where it does not.** A native `<select>` option holds a string and nothing
  else, so every picker in this app that had a description attached it
  *underneath* the control (`AgentSelect`) or glued it onto a label
  (`"Opus 5 — varsayılan"`). Those are one missing feature worked around three
  ways. A `Picker` trigger is styled as a field, not a button, because it
  stands in for one.
- **The composer is one surface, not a form.** `TaskComposer` was a title
  input, a textarea and a button row as three stacked rectangles. Writing a
  task is not filling in a form — it is saying one thing to an agent — so it is
  a single recessed box with the mark at its head and its tools on its bottom
  edge, inside the boundary of what is about to be sent. `NewTaskOverlay` puts
  it *above* the routing choices for the same reason: all three have defaults
  that are right most of the time, and asking for them first made the operator
  classify work they had not written down yet.
- **A screen either fits or scrolls, and it is the screen that decides.** The
  shell hands each screen a pane with `overflow-hidden` and `h-full`; what the
  screen does inside that is its own choice, and there are exactly two right
  answers. A fixed-height layout — `grid-rows-[auto_1fr]`, `min-h-0`, panes with
  their own scrollers — is for a screen whose parts are all viewports onto
  something: `Terminals`, `Catalog`, the Board. Everything else is a document
  and takes `h-full min-h-0 overflow-y-auto` with its content at natural height,
  the way `Home` does. `Brain` was the first kind pretending to be the second,
  and the pretence is what the operator saw: a console answer cut through the
  middle of a row, a rail off the right edge of the window, a project list that
  stopped at eleven of eighteen. Nothing was shortened; it was clipped.
- **`1fr` is not `minmax(0,1fr)`, and the difference is a bug you will not see
  until something inside gets wide.** A `1fr` track takes the larger of its
  share and its content's min-content width, so one card header with a picker
  and two buttons in it can refuse to shrink and push its neighbour off the
  screen. Same rule one level down: a grid or flex *item* needs `min-w-0` or its
  children's `truncate` never engages and it sizes itself to the longest string
  it happens to be holding. If a column has a fixed sibling, it is
  `minmax(0,1fr)`; if an item holds text that truncates, it is `min-w-0`.
- **Two columns are a decision about width, so give them a breakpoint.** Below
  `xl` Brain's graph and its rail stack instead. Four hundred pixels of graph is
  not a smaller graph — it is a card whose own toolbar does not fit inside it.
- **Cap the lists that live inside a scrolling page.** On a fixed-height screen
  an over-long list overflows and you notice; on a scrolling one it pushes
  everything under it half a screen down and you do not. `max-h-*` plus
  `overflow-y-auto` on the list, and print the count beside it — a capped list's
  last row is cut, which is the correct affordance for "there is more" and looks
  exactly like the thing that was actually broken.
- **Count the cells rather than fitting as many as go.**
  `repeat(auto-fit,minmax(96px,1fr))` solved the words and left the arithmetic
  to the window, which put Brain's ten scan figures on one line by truncating
  their own labels — "değişme…", "okunama…", "tavana ta…". A counted grid with
  breakpoints gives two even rows. When such a grid wraps, every cell draws its
  own left hairline and the row is shifted a pixel out of a hidden overflow, so
  the leading one of each row falls outside; `first:` only reaches the first
  cell of the first row.
- **A control the operator cannot reach does not work, and reports as broken.**
  Brain's scan routing lived in a `CardHeader` `aside`, which is `shrink-0` and
  did not wrap: badge, two dropdowns and two buttons wider than the room left
  over ran off the right edge of the card and were clipped by the screen. The
  model dropdown was last in the row *and* was drawn only after a provider had
  been picked, so the one control the operator was reaching for was the one that
  went over the edge — reported, correctly, as "the model cannot be changed".
  A header aside wraps now instead of overflowing, and routing that takes two
  dropdowns gets a row of its own.
- **A module with four rhythms is four screens, not one screen with four
  modes** (`screens/catalog/`). Katalog carried the file list, the column map,
  the field switches, the brand kit, the status rail, the product table and one
  product's before/after in a single window under six bands of chrome, and an
  operator called it suffocating. The split follows how often each job is done
  — the catalogs once in a while, the table every day, one product at a time,
  the setup once per file — and each screen owns its own state in its own file.
  The shared parts (the draft hook, the error line, the run strip) live in
  `catalog/shared.tsx`; a helper kept inside whichever screen drew it first is
  a helper the other three import across the module.
- **A control that spends money belongs beside what it will spend it on — and
  "beside" is not "only sometimes".** The model picker was first permanent
  furniture above the table, where it read as a setting, and was then made to
  appear with the selection it applies to and leave with it. That second version
  overshot: the picker vanished the moment the operator unticked a row, so "what
  will this spend" became a question you had to make a selection in order to
  ask, and the bar itself jumped the table up and down as rows were ticked.
  The bar is permanent and the *spending* is what is gated — which is the rule
  directly below this one, applied here: the button is disabled with a sentence
  saying why, the accent rail on the bar's edge appears only with a selection so
  an empty bar does not read as the thing to press, and choosing a model, which
  costs nothing, stays available while the operator reads the table.
- **A model control must be wired to the pass the operator is looking at.**
  Every card's panel offered the *coding* model dropdown, including a catalog
  card, whose pass spends the daemon's own provider and never reads that field.
  The control worked; it just was not connected to anything the card would do —
  reported, correctly, as "I changed the model and nothing changed". There are
  three answers now (`modelControlFor`): a claude session gets the coding list,
  a catalog card gets `ProviderModelPicker` writing into its own `params`, and a
  card with no per-card choice gets a sentence naming where that decision lives
  rather than a control that would silently do nothing. A card's own model is
  read from its `params` and never from `run.model` — the column is rewritten
  with whatever the *last* attempt spent, so a re-pointed card would keep
  showing the model it just failed on.
- **Draw a dependent control disabled, never absent.** The same picker's model
  half did not exist until a provider was chosen, under a single label reading
  "Model" that in fact named the *provider* control beside it. Nothing on the
  screen said a model choice existed, and nothing said what would reveal it. It
  is always drawn, disabled, showing the model the daemon would route to, and
  each control carries its own name.
- **A `className` cannot set an `Input`'s width.** The base in `ui/field` carries
  `w-full`, and `cn()` is a join rather than a merge, so a caller's `w-20` and
  that `w-full` both reach the class list and the cascade decides — `w-full`
  wins. Lead-gen's search row rendered as three stacked full-width boxes for
  exactly this reason, including a "count" field a thousand pixels across, with
  its button orphaned on a line below. Widths go on a wrapper; `Select` and
  `Picker` already take their `className` that way.
- **A repeated action loses the right to be loud.** `danger` is outlined red and
  correct for one irreversible control; eighteen of them, one per project row,
  is a card of red rectangles where the colour has stopped meaning anything. A
  row in a list gets one quiet glyph and puts its verbs in a menu — the same
  shape the Board uses, for the same reason — and inside that menu "Unut" can be
  as loud as it needs to be, exactly once.
- **A disabled fill stops being the accent; it does not fade.** The shared
  `disabled:opacity-40` is right for an outline and wrong for Lime: forty per
  cent of it over Carbon is olive, which reads as a colour that has gone wrong
  rather than as a control that is off.
- **Say once what matters once.** `AccountPanel` drew the connected identity
  directly under the same identity its container had already drawn, and ended
  with an eight-line paragraph about the private Chrome window and
  `claude auth logout` — permanently, on the first screen the app opens, for
  something done roughly once. The paragraph is behind a `Popover` now, word
  for word. A panel reports what changes; the container reports what *is*.
- **`.focus-ring` is an outline, and everything interactive has one.** Before
  this, `ui/checkbox` was the only control in the entire application that marked
  focus — a keyboard user tabbing through the new-task dialog could not see
  where they were. It is an outline rather than a box-shadow ring because every
  raised surface here already spends its `box-shadow` on elevation.
- **Overlays are dialogs.** `ui/overlay` is the only modal: `role="dialog"`,
  `aria-modal`, a focus trap that wraps, focus returned to whatever opened it,
  and Escape. The three hand-built ones had none of that.
- **Grain appears exactly twice.** The gate (`screens/Connection`) and the band
  over the day's reading (`screens/Home`), both through `ui/grain`. It is the
  brand's signature and it is rationed on purpose: everywhere is wallpaper, and
  the working panes keep the dot grid instead. The component is two layers, and
  the split is the point — the *gradient* ships as a 1200px WebP at 16 kB and is
  stretched, because a blur has no proportions to distort; the *grain* is
  generated by the compositor at the display's real pixel density, because
  high-frequency noise is exactly what a codec discards and what resizing
  destroys. At full resolution the single-image version cost 114 kB and still
  went soft.
- **Durations live in two places that must agree.** `--dur-fast/base/slow` in
  `index.css` and `DUR` in `lib/motion.ts`, in milliseconds and seconds
  respectively; `motion.test.ts` asserts they are the same numbers. Reduced
  motion is handled on both sides — the CSS collapses durations, `useMotion()`
  strips the displacement from a variant and keeps the cross-fade.
- **The document is Turkish.** `index.html` said `lang="en"` while the UI is
  Turkish, so every `.label` and `.display` uppercased "bilgi grafiği" to
  "BILGI GRAFIĞI" — dotless, and wrong. It is `lang="tr"` now, and the three
  screens that really are in English (`Workspace`)
  carry `lang="en"` on their root so their own caps stay Latin. Translating
  those three is still a separate job.
- **`lib/lineColors.ts` is the only console palette.** `Home`, `Terminal` and
  `BrainConsole` each had one, and they had drifted: the same stream rendered at
  a different contrast depending on which screen you watched it from. Class
  names, not colour strings, so it cannot become a fourth palette.
- **Where a primitive exists, use it.** `ui/` covers button, badge, card,
  overlay, tabs, input/textarea/select/radio/checkbox, pulse, meter, stream,
  skeleton, empty and masthead. A screen that needs a shape one of those nearly
  makes should widen the primitive, not write a tenth copy — that is exactly how
  nine buttons happened, and the reason was always that this component was short
  of a variant.

## Katalog and the HTML source surface (task-86)

- **The editing surface is the description's own HTML, and the rich-text editor
  it replaced is the reason.** TipTap was here for a real argument — a
  schema-driven editor is the only kind that cannot produce a tag the daemon
  will strip — and the argument was right about *marks* and silently wrong about
  *structure*. Its document model had no node for a wrapper, so opening this
  store's own description, which begins `<div class="flex flex-nowrap
  gap-4"><div class="flex-none w-3/5">`, and saving it posted the same words
  with both divs gone. Nothing warned anybody: from inside the editor the text
  was intact, and the loss only showed up in the export. A surface that destroys
  the merchant's layout by the act of looking at it is worse than one that
  offers a button the server ignores.
- **`HTMLSource` only ever *inserts* whitespace, and only between two tags.**
  `formatHTML` in `lib/catalog.ts` indents the source so a person can read it;
  `flattenHTML` is the inverse it is tested against, so what is shown is what is
  stored, byte for byte — nothing re-quoted, no entity rewritten, no attribute
  reordered. A break is never put inside a text run, because the daemon's parser
  turns a newline there into a `<br>`: an inline break is not cosmetic, it is a
  tag the operator never asked for landing in their storefront. An element with
  no block-level descendant stays on one line.
- **Four dependencies left with it** (`@tiptap/react`, `@tiptap/core`,
  `@tiptap/pm`, `@tiptap/starter-kit`), and so did `editorSchema`,
  `starterKitOptions` and the `.rte-surface` stylesheet. The vocabulary is still
  derived and still shown — it is what the daemon measures a save against — it
  just no longer builds a toolbar.
- **Katalog is lazy, and it is the only screen that is.** It was split when the
  editor cost **+130 kB gzipped** — more than half the bundle again — and it
  stays split now that the editor is gone: it is the largest screen here by some
  way, most launches never open it, and nothing else is big enough that
  splitting it would buy more than a spinner.
- **The preview is a sandboxed `srcdoc` iframe, never `dangerouslySetInnerHTML`.**
  Two locks, and both are wanted: `sandbox=""` grants nothing (no
  `allow-scripts`), and a `srcdoc` frame inherits this document's CSP, whose
  `script-src 'self'` this task did not touch. The containment matters as much
  as the isolation — a merchant's description carrying a `<style>` block or a
  wide table cannot reach out and relayout the app around it.
- **`img-src` gained `https:` and nothing else did.** A product description
  links its photos on the store's own CDN, and an operator judging a rewrite has
  to see the product rather than a grey box where it was. The cost is stated
  plainly: opening a preview contacts that CDN. `script-src`, `connect-src` and
  `default-src` are untouched.
- **The screen filters in memory, and that is not the ledger's mistake being
  repeated.** The lead ledger is unbounded, so filtering the rendered page there
  would mean "search the two hundred rows you are looking at" (task-64). A
  catalog import is **one file, counted at upload**, and the rail has to count
  the catalog rather than count the filter already applied to it — ask the
  daemon for the approved products and every other row of the rail reads zero.
  Where one page is not the whole import, `pageCoversImport` is false and the
  panel says so rather than letting the rail speak for rows it never saw.
- **The mapping gate reads `readable`, never `dialect` (task-90).** Saving a
  column map does not invent a platform — there is no platform — so a screen
  that asks "is there a dialect?" shows the mapping form again the moment it is
  filled in. An operator hit exactly that with their own catalogue: they mapped
  thirty-seven columns, pressed save, and got the form back. The daemon says
  `readable`; the screen asks that and nothing else.
- **The column map is always reachable, and it opens on the columns the file is
  actually being read with.** A detected dialect is a good guess about someone
  else's export format, not a fact about this file, so "sütunlar" is in the bar
  whether or not one matched. `initialMapping` seeds from `languages[].columns`
  — the daemon's `File.ColumnsFor`, which is the operator's saved map when they
  have made one and the matched profile's columns when they have not — and falls
  back to the deterministic `suggested` map only when nothing matched at all.
  Reading `mapping` alone was the bug an operator reported: `mapping` carries
  their own map and nothing else, so a fully recognised IKAS export with a
  thousand products read opened this form with every dropdown on "—", and the
  only way to keep a mapping that was already working was to retype it. Each
  column's own first value sits underneath its dropdown, because two columns
  called "Açıklama" and "Metadata Açıklama" are told apart by what is in them.
- **The workbench is a mode of the Katalog screen, not a screen.** Editor left,
  live preview right, `önce · sonra` above the preview. It does not enter
  `modules.ts`: it has no place in the sidebar, it is one product inside one
  import. The compact three-tab panel stays for glancing; the workbench exists
  because at twenty-six rems an editor and a preview side by side are two
  columns nobody can read, and "is this better" is a question that needs both.
- **The draft version is never sent.** It is a cache key, the daemon composes it
  from the prompt constant, the model, the brand hash and the skill version, and
  a client that could name one could serve itself copy written under a brand
  voice that no longer exists — the same rule the outreach screen follows about
  prompt versions.
- **The vocabulary is shown and never edited; the voice is edited and never
  derived here.** The brand panel draws them as two different kinds of thing on
  purpose. An editable vocabulary field would offer a decision the daemon
  ignores; the voice is the operator's own writing, and it is the only thing
  that panel sends.
- **The file is read as bytes, not as text.** `FileReader.readAsDataURL`, then
  base64 to the daemon. A Windows-1254 export decoded in the WebView as UTF-8
  arrives already broken, and every Turkish character in the catalog with it —
  the daemon's encoding sniff has to see what the exporter actually wrote.
- **What the daemon simplified is shown, not swallowed.** `PUT
  /catalog/products/{id}/draft` returns the notes its own sanitize gate wrote
  ("Açıklama markanın etiket sözlüğüne göre sadeleştirildi"), and the panel
  renders them. The editor is a convenience; the server is the authority, and
  the screen has to say when the two differed.

- **The preview paints from the shop, when there is one (task-116).** The frame
  used to render on a readable dark default, which is this app's look and not the
  merchant's — and "is this better" is a question about the page the copy will
  live on. `Marka` gained a storefront address; the daemon measures a product
  page and stores resolved values, and `previewDocument` takes them as a third
  argument. Not the shop's CSS and not its markup: the frame loads no external
  stylesheet (the CSP allows none, and a `srcdoc` frame inherits it), so class
  names would be inert and a theme's rules are written for a page this is not.
  Colour, font stack, size and measure are what travel. A scan too thin to paint
  from leaves the default alone rather than rendering a shop as a blank page.
- **A scanned value is somebody else's string on its way into a stylesheet.**
  `cssValue` allowlists the characters a computed value can contain and drops
  anything else whole — `;`, `{`, `}`, `<`, `:` and `\` are all outside it, so a
  value cannot end its declaration, open a rule or close the `<style>` element.
  Two locks behind it, both unchanged: React escapes the `srcDoc` attribute, and
  `PREVIEW_SANDBOX` is still `""`.
- **The colour chips stand on the shop's own ground.** An ink swatch painted
  straight onto this app's panel is the storefront's text colour on Carbon,
  which for a black-on-cream shop measured **1.27:1** against the card and read
  as nothing at all. Standing each ink on the scanned background is both legible
  (13.63:1 for the plate, 17.32:1 for the ink on it) and the truer picture — it
  is the pairing the shop actually uses.

### Languages in Katalog (task-102, task-104)

- **The profile table is the daemon's.** `dialectLabel` takes the fetched list.
  This file used to hold its own copy and it went stale — it still offered a
  profile task-91 deleted. A key the daemon did not name is shown as the key
  rather than invented into a label.
- **The platform is a picker, not a badge.** Detection answers "which platform
  wrote this file", not "which platform is this store on", and the operator can
  see the difference when detection cannot. The empty option is not "none": it
  hands the file back to detection, which is how a wrong pick is undone without
  re-uploading a thousand products.
- **Which languages a screen offers comes from the import, never from a
  constant here.** A file with no Arabic column has nowhere to put an Arabic
  answer, and the daemon refuses the pass — offering the tab would let somebody
  pay for copy that cannot be written back.
- **Two different questions, two different lists (task-115).** `languages` is
  what this file carries and draws the tabs; `writable_languages` is what the
  daemon can write at all and fills the *mapping form*, which exists precisely
  so an operator can point at a column no profile names. The form read the first
  list for a while, which made the `Html:Detay-EN` a store created for itself
  permanently unreachable — and its own doc comment claimed otherwise.
- **The language is a column of the product table, not a mode over it.** The
  language bar was a `Segmented` on the filter line, and selecting a language
  re-read the same rows under it. For a language nothing had been written in yet
  — which is every language on the day an operator first meets this feature —
  the screen was identical before and after, so a control that genuinely decides
  which draft row a pass writes to read as decorative. An operator reported it
  as having no function at all, and they were describing what they could see.
  Each language the file carries now has its own status column: decided where,
  waiting where, in one reading. The table draws the file's own language,
  because that is the one every row of every export has; the workbench draws the
  target language, where a blank body means "not translated yet" and is the
  distinction `contentFor` refuses to blur.
- **A single-language file is still told that this module writes others.** That
  was the language bar's second job and it is the half worth keeping: an
  operator whose export has no Arabic column would otherwise see nothing and
  never learn the column is theirs to add. It is a `+ dil` control in the status
  column's header, on the table — the screen they are on every day — and not
  only in Kurulum, which they open when something is already wrong.
- **Which language a pass writes is a property of the run.** It is a select in
  the rewrite bar beside the model, because that is where the other decisions
  about the run are. It is *not* the same state as the language the workbench is
  open in: the product list is read under the workbench's language, since
  `useDraft` reads the draft the list carried and a list read in Turkish would
  put the Turkish draft in the Arabic panel. The table is indifferent to which
  — it draws source titles and the per-language decision map, and neither
  changes with the read.
- **A decision belongs to a language (task-113).** `setCatalogStatus` carries
  one, and a row shows the source language's decision beside a target one's when
  they differ. Approving the Arabic must not approve a Turkish draft nobody
  looked at.
- **One vocabulary for a language's name.** `langLabel` in `lib/catalog.ts`
  prefers the label the daemon sent and falls back to a single map. There were
  two maps, both two entries long, and a third language would have shown up as a
  raw "de" on whichever of them was forgotten.
- **A target language never falls back to the source one.** `contentFor` reads
  empty for a language the file does not carry. Showing the Turkish body under
  the Arabic tab would make "already translated" and "not translated yet" look
  identical, which is the one distinction the operator opened the tab for.
- **Direction is on the content, not on the app.** The document stays Turkish
  and left to right; `RichPreview` and the field column take a `dir` and put it
  on the writing surface. `HTMLSource` is the exception and stays left to right
  on purpose: source is not prose, the tags are Latin and the indentation is a
  left margin, and a right-to-left textarea would put `<div` at the right edge
  of every line. The Arabic runs inside it still render right to left, which is
  the part that has to be right. The preview
  frame writes its own `lang`/`dir` and lays out with `padding-inline-start`
  and `text-align: start`, or a right-to-left list indents away from its bullets.
- **The column form offers every language, not only the resolved ones, and it
  does not tab between them.** `view.languages` answers "what does this file
  carry today"; the form is how an operator makes it carry one more, so it reads
  `writable_languages`. Offering only what already resolves would make the
  Arabic column unreachable for every store whose export the profile table does
  not happen to name.
  The language tabs are gone. This form edits one table's columns — it is not a
  place where content is written, so a control that looked like a filter over
  that table was answering a question nobody had asked, and it hid rows behind
  it. `mappingRows` returns every slot, grouped under a language heading that
  draws only when there is more than one to tell apart — the same test the field
  switches below it already apply, and the same `.label` class, because a
  heading set like the row labels it governs is not a heading.
  What the tabs did **not** do is drop data, and it is worth writing down
  because the shape looks exactly like the trap `MAPPABLE_FIELDS` describes for
  `handle`: the form posts the whole map, so an undrawn slot looks like a slot a
  save unmaps. It was not, because `initialMapping` seeds from the daemon's own
  `languages[].columns` — every language, drawn or not — and the tabs shared one
  `mapping` record rather than one per tab. So the fix here is legibility, not
  data loss; claiming otherwise would put a bug in this file that never
  existed.
- **The field configuration is switches, and they do not apply on flip.** That
  breaks the usual rule about switches and it is deliberate: the configuration
  is one set, and a half-applied set is a rewrite writing into a column nobody
  meant. The cost of breaking the rule is that somebody can walk away believing
  a flip stuck — so `FieldsPanel` states its unsaved state **in words**
  ("kaydedilmedi"), not in a button colour, and the save is the only primary
  action in that panel.
- **`ui/switch` is a second control, not a widened `Checkbox`.** A checkbox is a
  selection inside a set — ours carries a shift-range gesture and a third
  indeterminate state for exactly that, and it lives in a table header over
  rows. A switch is a property of one thing that stays that way. Overloading the
  box would give a settings row a range gesture and a dash state that mean
  nothing to it.
- **A field with no column in the file is not drawn as an off switch — it is not
  drawn.** A product export has no fixed field set, so the panel is built from
  what the daemon says *this file* offers. A switch that cannot do anything is
  worse than an absent one, because the operator flips it and waits.
- **An empty configuration cannot be saved from here.** On the daemon an empty
  set means "not configured", which means everything — saving one would turn
  every switch the operator just switched off back on while they watched.
- **A draft edit is per language.** `useDraft` resets on a language change as
  well as on a product change: a half-typed Turkish edit must not follow the
  operator onto the Arabic tab, and the save carries the language so it lands on
  the right draft row.

- **The board's park badge reads the daemon's holds, never the row's error
  text (task-88).** A parked card and a card that has simply not started are
  both `queued`, and an operator who cannot tell them apart reads an overnight
  pause as a hang. `GET /coding-tasks/queue/limits` is the answer, and it rides
  the same poll as the cards — a second loop for it would be a second answer
  that can disagree with the first about the moment a pause lifted. The row's
  `error` is prose written for a person and is not a data source.
- **The two lanes wait on different things, and the badge says which.** A
  worker-lane card (catalog, lead-gen) waits on the daemon's own model identity,
  held under `worker-lane`; an account-lane card waits on its credential slot.
  Reporting one as blocked by the other would send the operator to look at a
  limit that has no bearing on it.
- **A card body is read from `params`, never from a second request.** task-80
  removed the board's 1+N fan-out; a card that fetched its own import would put
  it back one card at a time. A card whose params will not decode draws nothing
  — the daemon is about to refuse it with a reason, and a guess here would be a
  second, wronger reason on the same card.

## The design harness (task-115)

`npm run harness` serves this app in a browser against a running daemon, so a
visual claim can be measured rather than asserted. It exists because nothing
could: `main.tsx` calls `getCurrentWindow()` at module scope and every REST call
goes through the Rust shell, so `design-review` had no page to open and every
finding stayed an informed guess.

- **It is dev-only and lives outside `src/`.** `harness/` and
  `vite.harness.config.ts` are not referenced by `vite.config.ts`, by
  `tauri.conf.json` or by anything under `src/`, so the shipped bundle cannot
  reach them. `lib/daemon.ts` stays the one door; what changes is only what sits
  behind it while somebody is looking at the page.
- **The app's own entry runs unmodified.** Two `resolve.alias` entries point
  `@tauri-apps/api/*` at a shim; `index.html` and `main.tsx` are untouched.
  Pointing Vite's `root` at `harness/` instead moved Tailwind v4's source
  detection with it, every utility class in `src/` went ungenerated, and the page
  rendered as one 1670px logo.
- **The token never reaches the browser.** The hop to the daemon runs in Node,
  where the `Authorization` header is added — which is also the only thing that
  works, for the reason `lib/daemon.ts` gives: the daemon carries no CORS headers
  on purpose and answers the preflight with 401.
- **The proxy is same-origin only, and the check runs before the token goes
  on.** Without it the harness is an open proxy signing whatever any page the
  developer happens to have open decides to send — `POST /daemon/coding-tasks`
  with a `text/plain` body is a simple request, needs no preflight, and does not
  need its reply read to have done its work.
- **REST only; no sockets.** `ws: true` was declared once and measured as inert —
  Vite does not route an upgrade through this proxy, so the hook that would have
  signed it never fires. A card's live output is read in the real app. Adding
  socket support means guarding the upgrade separately, because `bypass` runs in
  the HTTP middleware and an upgrade never reaches it, and a WebSocket is not
  subject to CORS.

## Reviewer focus

Does anything under `src/` import from `harness/`, and does any production
config reference `vite.harness.config.ts`? Does anything parse the child's stdout? Does the spawn environment still carry
exactly two variables (the launchd plist's `PATH` is launchd's, not this
shell's)? Can an attached shell kill a daemon it did not spawn? Can `daemon_request` be pointed at a non-daemon host? Is
the token anywhere but Tauri IPC and the socket subprotocol? Does any screen
keep a filesystem path after registration? Does the reducer still dedupe on
`seq` and match results by `call_id`? Does the lead-gen screen force
`gap_analysis` on when `emails` is set, and does it ever send a prompt version?
Does any screen offer a choice of Claude account, or open the login page itself
instead of letting the daemon do it? Does `flattenHTML(formatHTML(x))` still
return `x`, and does anything insert a line break inside a text run? Does the
preview iframe still carry `sandbox` without `allow-scripts`? Does the column
form still open on the columns the file is actually read with, rather than on
the operator's own map alone? Is there a ninth hand-written selected row
anywhere, or does it go through `ui/rail`? Does any toggle say "on" without
`active`, and does any disclosure announce itself with `aria-pressed`? Does any
`onChange` split, trim or filter its own value before storing it? Does a pane
that binds a bare letter or Space check `isTypingTarget` first? Does Katalog
still know what the card it queued is doing, and does the rewrite button say
why it is disabled rather than only being disabled? Did the CSP grow
anywhere but `img-src`? Does Katalog still load lazily? Does anything in Katalog
still keep its own copy of a closed set the daemon owns? Does a target-language
tab ever fall back to showing the source language's copy?

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
- **It is Mimir's own credential slot, not the operator's, and it persists.**
  The `claude` in their own terminal is a different keychain entry and is never
  in reach of anything this panel does. Connecting is a one-time act: the slot
  survives a quit, so the panel is usually just reporting who is connected. The
  copy says so, because "why does it ask me again every launch" was the
  question the old behaviour left.
- **"çıkış yap" is the only thing that disconnects, and it is also how you
  switch accounts.** There is no account picker (see above), so signing out and
  signing back in with the other one is the switch. The panel also offers
  "yeniden bağlan" when the row exists but `status.logged_in` is false — a
  lapsed login is the one state persistence introduces, and without it the
  connect button stays hidden behind the surviving row.
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
