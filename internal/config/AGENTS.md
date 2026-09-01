# AGENTS.md — internal/config

The single source of every operational value. Enforces **SD-1**.

## Rules for this directory
- Values are **constants** in this package. `Load() Config` returns them fully
  populated. `Validate() error` sanity-checks.
- **No exported setters. No functional options. No config file. No CLI flags.**
  Nothing outside this package may mutate a `Config`.
- The **only** permitted `os.Getenv` reads are the test/CI plumbing overrides
  below, parsed exclusively inside `Load()`. Each defaults to its production value;
  an unparseable value is ignored (keep the default) — never panic, never error.

  | Env var | Overrides | Default |
  |---------|-----------|---------|
  | `GOAT_CRAWL4AI_URL` | `Crawl4AIBaseURL` | `http://127.0.0.1:11235` |
  | `GOAT_CLAUDE_CLI_PATH` | `ClaudeCLIPath` | `claude` |
  | `GOAT_DDG_HTML_URL` | `DuckDuckGoHTMLURL` | `https://html.duckduckgo.com/html/` |
  | `GOAT_DDG_LITE_URL` | `DuckDuckGoLiteURL` | `https://lite.duckduckgo.com/lite/` |
  | `GOAT_MAPSCRAPE_URL` | `MapScrapeBaseURL` | `http://127.0.0.1:11236` |
  | `GOAT_STORE_PATH` | `StorePath` | `<os.UserConfigDir()>/goat-mcp/goat.db` |
  | `GOAT_CLAUDE_PROJECTS_DIR` | `ClaudeProjectsDir` | `<os.UserHomeDir()>/.claude/projects` |

  There is **no** override for timeouts, concurrency limits, result caps, output
  ceilings, politeness interval, `ClaudeModel`, `PageCacheTTL`,
  `RefinePromptVersion`, the `Leadgen*` values, the `Memory*` values, the
  `MapScrape*` bounds, or any Stage-F scraper constant (`*MaxTokens`, `*MaxChars`, `GMapsPageTimeout`,
  `GMapsWaitForSelector`). Adding one is an SD-1 violation.

  Note the split in the two scraper rows: the container **address** is
  overridable (a test points it at an `httptest` server), while
  `MapScrapeTimeout`, `MapScrapeMaxResults`, `MapScrapeMaxScrolls` and
  `MapScrapeWaitSelector` are not. The address says where a dependency lives;
  the rest say how hard we lean on someone else's site, and that is behaviour.

- **Cache-version constants carry a rule, not just a number.**
  `RefinePromptVersion`, `LeadgenCategoryVersion`, `LeadgenGapVersion` and
  `LeadgenEmailVersion` are cache keys: they exist so invalidation is a
  deliberate, reviewable act instead of silent staleness.
  `LeadgenCategoryVersion` covers **both** halves of the categorization
  taxonomy — `internal/leadgen`'s Google-type rule table *and* the classify
  prompt. `LeadgenGapVersion` (stage 3) and `LeadgenEmailVersion` (stage 4) are
  **separate** from it and from each other: each invalidates only its own
  prompt, and the three stages are meant to be tuned independently. A change to
  a prompt or rule table that does not bump its matching version serves answers
  derived from a contract that no longer exists, and that is a review-stopper.

- **Process plumbing, parent-provided** — a second, narrower category, added by
  task-22 for `cmd/goat-daemon`. It is *not* a loophole in SD-1; a value belongs
  here only if all four hold:

  1. it is knowledge the process cannot derive, only be told — a port its
     parent already reserved, a secret its parent minted;
  2. it changes **nothing** about what the process does with a request;
  3. it is set by the parent that spawns the daemon (the Tauri shell, per
     `docs/ROADMAP.md` §B.2.1), never by a human editing a config file;
  4. absent, the daemon has a defined, fail-closed posture.

  | Env var | Sets | Default | Absent |
  |---------|------|---------|--------|
  | `GOAT_DAEMON_PORT` | `DaemonPort` | `0` (kernel-assigned) | fine — bind ephemeral, log the resolved address to stderr |
  | `GOAT_DAEMON_TOKEN` | `DaemonAuthToken` | *(none)* | **refuses to start** (`ValidateDaemon`) |

  `DaemonHost` is deliberately **not** in this table. Binding loopback-only is a
  security property of the daemon, not a deployment detail, so it stays a
  constant. A timeout, limit, model, or permission mode never qualifies —
  those are behaviour, and behaviour is SD-1's subject.

- **Operator-provisioned credential** — a third, narrower category, added by
  task-24 for the Google Places key. It is the one documented exception to
  `docs/SECURITY.md`'s "zero API keys configured by GOAT-MCP itself" stance,
  and `docs/ROADMAP.md` §B.6 (M4) is where it was agreed. A value belongs here
  only if all four hold:

  1. it is a secret for a **third-party provider**, minted outside this repo by
     the operator, in the provider's own console;
  2. it decides **whether** a provider can be reached — never what the process
     does with a request, and never how a response is shaped;
  3. it is read exactly once, in `Load()`, and is never logged, never echoed in
     an error, and never passed per call by the consumer;
  4. absent, the capability is **omitted, not broken**: the binaries start
     normally and the tool it powers is simply not registered
     (`internal/tools.RegisterAll`), so a keyless install never advertises a
     tool whose every answer would be "no key".

  | Env var | Sets | Default | Absent |
  |---------|------|---------|--------|
  | `GOAT_GOOGLE_PLACES_API_KEY` | `PlacesAPIKey` | *(none)* | `maps_search` is not registered; everything else runs |

  `Validate()` deliberately does **not** require it. The caps that surround it
  (`MapsSearchDefaultCount`, `MapsSearchMaxCount`, `MapsSearchMaxTokens`) are
  ordinary SD-1 constants with no override — the credential is the only part
  that comes from outside. A second provider key would extend this table
  rather than invent a third pattern (`docs/ROADMAP.md` §A.3's credential
  vault is the same shape).
- Stage F (`internal/ecommerce`, `internal/tiktok`, `internal/gmaps`,
  `internal/instagram`) needs **no API keys** — every field they read comes
  from the two rows above (Crawl4AI, DDG). `gmaps_business_lookup` in
  particular is the *free, self-scraped* one-business lookup and shares
  nothing with `maps_search`, which is the paid Places-API region search
  behind the credential row above.
- `ClaudeModel` is a fully-qualified pinned model id
  (`claude-haiku-4-5-20251001`), never a bare alias like `haiku` (SD-5). The
  refiner is the local `claude` CLI invoked headless via `exec` — no API key,
  no HTTP endpoint; it rides the operator's existing Claude Code login.
- New fields for Track A's paid providers (`docs/ROADMAP.md` §A.3) do not belong
  here until that track is promoted.

## Reviewer focus
Grep for setters/options/flags/file readers and for `os.Getenv` outside the
three tables above (six test/CI rows, two daemon-plumbing rows, one
credential row). Any hit → `CHANGES REQUIRED`.
