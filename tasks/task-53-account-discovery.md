# task-53 — The accounts directory is the authority for credential slots

- **Status:** done
- **Owner agent:** daemon
- **Prerequisites:** task-37 (accounts), task-41 (`internal/llm`)
- **Primary paths:** `internal/account/discover.go` (new), `internal/account/account.go`, `internal/store/migrations/0015_account_slots.sql` (new), `internal/store/accounts.go`, `internal/llm/{claude,llm}.go`, `internal/api/{api,handlers,middleware}.go`, `cmd/mimir-daemon/main.go`, `internal/config/config.go`
- **Roadmap bucket:** B.2 extension

## Context

The operator has two Claude Code identities and a shell that already switches
between them: `~/.claude-accounts/<name>` is handed to the CLI as
`CLAUDE_SECURESTORAGE_CONFIG_DIR`, and the name `default` means the variable is
not set at all (`claude-acct` / `claude-who` in `~/.zshrc`). Mimir shipped the
same mechanism in task-37 — `account.Environ` is character-for-character the
same rule — but not the same *registry*: a slot only existed here once somebody
picked its directory in the app.

Nobody ever did. The `accounts` table was empty on the live machine, so the
composer's account picker offered only "Otomatik", `coderunner.slots` fell back
to a single synthetic default, and capacity was one lane instead of two: the
second identity was never spent. Separately, the daemon's own model calls
(`internal/llm/claude.go`) set no environment at all, so they spent whichever
identity the daemon inherited — the operator's own session, when it was started
from a dev shell.

## Scope (do exactly this)

1. **`internal/config`** — `ClaudeAccountsDir`, defaulting to
   `<home>/.claude-accounts`, with the test-only override
   `MIMIR_CLAUDE_ACCOUNTS_DIR`. The shell's own `CLAUDE_ACCOUNTS_DIR` is
   deliberately **not** read: the daemon runs under launchd, which hands it
   `PATH` and `HOME` and nothing else, so honouring it would make which accounts
   exist depend on who started the daemon.
2. **`0015_account_slots.sql`** — `discovered` and `is_background` on
   `accounts`, plus a partial unique index so at most one row is the background
   slot. Store methods: `MarkAccountDiscovered`, `SetBackgroundAccount` (one
   transaction, because the unique index makes the intermediate state illegal),
   `GetBackgroundAccount`.
3. **`internal/account/discover.go`** — `Discover(accountsDir) []Slot`: the
   CLI's own slot always first, then every subdirectory sorted by name; dotted
   entries and files skipped; a subdirectory named `default`, `a` or
   `salihdevran` collapses onto the default slot, which is the same mapping
   `_claude_acct_dir` makes. `Registry.Sync` registers them (idempotent by
   directory) and marks them discovered. `Background` / `SetBackground` read and
   move the background mark.
4. **`Delete` refuses a discovered slot** (`ErrAccountDiscovered`, 409). The
   filesystem decides those exist, so forgetting one in the app would promise a
   removal the next scan takes back.
5. **`internal/llm`** — `Claude.UseEnviron(func() []string)` and
   `Router.UseEnviron`, applied to `Complete` and `Health`. A function, and
   injected rather than imported, because `internal/account` cannot be imported
   from `internal/llm` (account → store → refine → llm is a cycle) and because
   the account may change while the daemon runs.
6. **`internal/api`** — `POST /accounts/scan` and `POST /accounts/background`
   (empty id clears the mark). `discovered` / `is_background` on the account
   JSON. POST rather than PUT for the second: the Tauri shell's proxy allowlists
   GET, POST and DELETE, and adding a verb there to spell one route differently
   would widen that allowlist for nothing.
7. **`cmd/mimir-daemon`** — scan once at startup, log the slots found, and wire
   `router.UseEnviron` to `account.Environ(os.Environ(), background.ConfigDir)`.
   A failed scan is logged, not fatal.

## Out of scope (do NOT do here)

- Removing rows. `Sync` adds only: a slot whose directory has since gone may
  still have a run pinned to it, and the honest report for it is a failing
  probe on its row, not a row that disappears from under a queue.
- Logging a slot in, or touching a credential. Mimir names slots; the keychain
  holds them.
- Per-account model or cost policy. An account is an identity, not a preference
  (task-39).
- `internal/llm/agy.go`. `agy` is a different CLI with its own login.

## Definition of Done

- `make check` green.
- On the live machine: `Default` and `eziode` registered without anybody picking
  a directory, and two coding tasks run at once.
- The default slot is still the *absence* of `CLAUDE_SECURESTORAGE_CONFIG_DIR`,
  in the runner and in the background calls alike.

## Changelog

- `internal/config/config.go`, `internal/config/AGENTS.md` — `ClaudeAccountsDir`
  + the override row and why the bare shell variable is not read.
- `internal/store/migrations/0015_account_slots.sql`,
  `internal/store/accounts.go` — two columns, three methods.
- `internal/account/{discover.go,account.go,AGENTS.md}` + `discover_test.go`.
- `internal/llm/{claude.go,llm.go}` + `llm_test.go` — the environment builder.
- `internal/api/{api.go,handlers.go,middleware.go}` + `lifecycle_test.go`.
- `cmd/mimir-daemon/main.go` — startup scan, background wiring.
