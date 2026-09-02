# task-37 — Per-account credential slots and one run per account

- **Status:** done
- **Owner:** daemon
- **Prerequisites:** task-35
- **Primary paths:**
  - `internal/account/` (new package)
  - `internal/store/accounts.go`, `internal/store/runs.go`,
    `internal/store/migrations/0012_accounts.sql`
  - `internal/coderunner/runner.go`
  - `internal/api/api.go`, `internal/api/handlers.go`, `internal/api/middleware.go`
  - `internal/config/config.go`, `cmd/mimir-daemon/main.go`
- **Roadmap bucket:** B / M3.

## Context

The operator has two Claude Code accounts and no way to say which one a task
spends. Worse, the dispatcher's capacity was a number (`CodingMaxConcurrentRuns
= 2`), which is the wrong unit: two runs sharing one identity share its rate
limit and its session state, so the second is contention, not throughput.

The mechanism turned out not to be `CLAUDE_CONFIG_DIR`. Claude Code stores
credentials in the macOS keychain and derives the entry from
`CLAUDE_SECURESTORAGE_CONFIG_DIR` — verified on this machine, which already
carries two entries (`Claude Code-credentials` and
`Claude Code-credentials-7664e09f`, the latter for `~/.claude-accounts/b`). So
an account is a directory path used as a hash input, and nothing more: Mimir
never reads a credential.

`claude auth status` reports `email`, `orgName` and `subscriptionType` per slot
for free — no API call — which makes a live identity probe cheap enough to run
on demand rather than caching a guess.

## Scope

- `internal/account`: a registry mirroring `internal/project` (path in once, id
  out), plus `Probe` and `Environ`.
- Migration 0012: an `accounts` table, and `requested_account_id` /
  `account_id` on `coding_runs`.
- Capacity becomes one run per registered slot. With no account registered
  there is exactly one implicit slot — the CLI's own — which is the behaviour
  Mimir had before this task.
- A task may pin an account or leave it automatic (the default). The dispatcher
  walks the queue rather than popping it, so a run pinned to a busy account is
  stepped over instead of blocking every other slot's work.
- `cmd.Env` is built rather than inherited: the slot is selected, and the
  launching Claude Code session's variables are stripped.

## Out of scope

- Logging in or out of a slot. That is `claude auth login` with the variable
  set, and the desktop app says so rather than wrapping it.
- Reading, copying or storing a credential anywhere.
- Per-account rate-limit accounting or cost attribution beyond the existing
  per-run `cost_usd`.
- More than one run per account, under any setting.

## Interfaces

```go
// internal/account
func (r *Registry) Register(ctx context.Context, label, configDir string) (Account, error)
func (r *Registry) List(ctx context.Context) ([]Account, error)
func (r *Registry) Delete(ctx context.Context, id string) error
func Probe(ctx context.Context, cliPath, configDir string) (Status, error)
func Environ(parent []string, configDir string) []string
```

Routes: `GET|POST /accounts`, `DELETE /accounts/{id}`,
`GET /accounts/{id}/status`. `POST /coding-tasks` gains `account_id`.

## Definition of Done

`make check` green, including: registration idempotent by directory; a relative
path and a file both refused; `Environ` selects a slot, expresses the default as
the variable's absence, strips the nested session's variables and keeps `PATH`
and `HOME`; one run per account observed under load; a pinned run waits for its
own account while an unpinned one behind it takes the free slot; a pin to an
unknown account refused; deleting an account with work refused.

## Notes for reviewer

- **Follow-up (2026-09-02):** the one-run-per-slot invariant had a hole. `launch`
  marked the slot busy from inside the goroutine it started, so `pump`'s next
  `dispatchOne` could see the same account free and claim a second run against
  it. Registration is now synchronous in `launch`, before the spawn, and
  `TestDispatch_OneSlotNeverRunsTwoAtOnce` fills the queue and releases it with
  a single `Resume` so the window is hit deterministically rather than 40% of
  the time.

- `internal/api/AGENTS.md`'s "a filesystem path is accepted at exactly one
  route" is now "exactly two". The rule it was protecting — validated once, at
  registration, by the package that owns the decision — is unchanged, and a
  credential slot has no other shape. This was the one documented boundary this
  task moved, and it was moved deliberately rather than worked around.
- `CodingMaxConcurrentRuns` is deleted rather than defaulted. Capacity is a fact
  about the accounts the operator registered, not a number to tune.
