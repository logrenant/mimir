# AGENTS.md — internal/account

Owns the one Claude Code identity Mimir spends. Introduced by `tasks/task-37`
as a multi-slot registry; reduced to a single account, signed in and out by
Mimir itself, at the operator's direction.

## What the account is here

**One, and Mimir's own.** The CLI keeps its credentials in the macOS keychain
and derives the entry it uses from `CLAUDE_SECURESTORAGE_CONFIG_DIR`, so a
directory path is the entire handle. The path this package hands the CLI is
`cfg.ClaudeSessionDir` — a directory Mimir derived for itself beside its store
— and never the empty string, which would mean the CLI's own default slot: the
login the operator uses in their own terminal.

That separation is the whole design. `Reset` signs the slot out, so it *must*
not be a slot anybody else is using.

Mimir still never sees, stores or moves a credential. It runs `claude auth
login`, points it at its own directory, and lets the keychain keep what comes
back.

## The lifecycle

**The slot outlives the app; signing out is an act, not a side effect.**
Connecting is a one-time thing. Quitting Mimir leaves the slot directory and the
keychain entry alone, so the operator signs in once and stays signed in across
launches and reboots. `Reset` — logout, directory, row — runs only on
`POST /accounts/reset`, which is the app's "çıkış yap" button and also how the
operator switches to a different Anthropic account.

This used to be the other way round, and the reversal was deliberate: `Reset`
ran at startup, at shutdown and from the shell's `RunEvent::Exit`, and the cost
was a login on every single launch. Nothing about the safety story depended on
it — the slot is Mimir's own either way, and the operator's terminal login is a
different keychain entry that was never in reach.

**A launch reconciles rather than resets.** `Restore` runs at daemon startup and
asks `Probe` who is in the slot, because a login can lapse or be revoked between
launches and a row the keychain no longer backs would advertise capacity that is
not there. Three answers, three actions:

| `Probe` says | `Restore` does | Why |
|---|---|---|
| `LoggedIn` | `record` — keep the slot, ensure the row | Idempotent by directory, so it also repairs a lost database |
| not logged in, no `Error` | full `Reset` | The CLI answered: the slot is empty, and the directory is a handle to nothing |
| `Error` set | forget the row, **keep the directory** | "We could not ask" is not "nobody is there". The directory is the only thing that can address the keychain entry again — removing it would orphan a good login for good |

**Connecting is a login, not a registration.** `StartLogin` runs `claude auth
login` on a pty — over pipes the CLI takes its non-interactive path and there
is nothing to complete — and opens the authorization page in a *private* Chrome
window. The row in the store is written only once the login has landed and
`Probe` confirms it; a row with no login behind it would advertise capacity the
keychain does not back.

## Rules for this directory

- **The slot directory is never a client's to name.** There is no path on the
  HTTP surface any more. `cfg.ClaudeSessionDir` is derived, not configured, and
  a knob for it would let a misconfigured launch sign the operator's own slot
  out at quit.
- **`Environ` expresses the CLI's default slot as the variable's *absence*.**
  Setting `CLAUDE_SECURESTORAGE_CONFIG_DIR=""` would hash the empty string into
  a third, nameless slot. Mimir does not use the default slot, but the
  distinction is what makes "" mean something, so keep it.
- **`Environ` also strips the launching session's variables.** A daemon started
  from inside a Claude Code session passes `CLAUDECODE=1`, a session id, a
  messaging socket and a bridge token to its children; a nested CLI reading
  those believes it is resuming somebody else's session. The strip list is
  `isInheritedSessionVar` — add to it, never remove.
- **The browser shim exits 0, and Mimir opens the window itself.** Left alone
  the CLI opens the *default* browser, carrying whatever Claude session is
  already signed in there — and the page then never asks which account is
  connecting, which is the one thing this flow exists to make it ask. The shim
  must still *succeed*: the CLI picks a `http://localhost:<port>/callback`
  redirect when it believes a browser opened, and falls back to asking for a
  pasted code when it does not. Both work here, but only the first finishes
  without the operator retyping anything.
- **`Probe` never returns an error for "not signed in", or for a slot it cannot
  read.** Both are answers about the slot and belong on the account row. An
  error from `Probe` means the probe itself could not run at all.
- **`Probe` spends nothing.** `claude auth status` reads a keychain entry and
  prints JSON — no API call, no tokens. That is why the desktop app asks live
  rather than caching a guess. Do not add anything here that costs money.
- **The CLI's exit code does not decide whether a login worked.** It exits
  non-zero when the timeout kills it, and the operator may well have finished
  in the browser first. The keychain is the authority, so `readLogin` probes.
- **`List` returns one account or none, never a stand-in.** The dispatcher
  reads it as capacity. A default slot conjured for an empty list would drain a
  queue through the operator's terminal login, right after a reset had signed
  Mimir's own slot out.
- **One identity for everything the daemon does.** The daemon's own model calls
  — refine, distil, recap — spend this same account, wired in
  `cmd/mimir-daemon` as `Environ(os.Environ(), accounts.Dir())`. There used to
  be a "background slot" pointing them somewhere else; it is gone and must not
  come back. "Which account paid for this?" has one answer.
  `internal/llm` still cannot import this package (account → store → refine →
  llm is a cycle), so that one builder remains the only path.

## Testing

No keychain and no real CLI. `Probe`, `logout` and `Restore` are exercised
against shell stubs — one that prints not-JSON, one that exits non-zero, one
that records the slot it was pointed at, and `statusStub` for the three answers
`Restore` branches on. The login flow is tested where it is deterministic: the
URL is read out of the exact escape sequences the CLI paints it with, and
`loginEnviron` is a pure function that must put the shim ahead of `/usr/bin`.

## Reviewer focus

- Any path to a credential slot other than `cfg.ClaudeSessionDir` — above all
  an empty `ConfigDir`, which is the operator's own login.
- A `Reset` that stops short of all three of logout, directory and row — it is
  now also the account-switch path, and a leftover would connect the next login
  as the account the operator just asked to leave.
- A `Reset` creeping back into a startup or shutdown path. Quitting is not
  signing out; `Restore` is what a launch runs.
- A `Restore` that removes the slot directory on a probe it could not run.
- A row written before `Probe` confirmed the login.
- A `List` that invents an account when none is connected.
- A browser shim that fails instead of exiting 0, or a flow that lets the CLI
  open the default browser.
- Anything that reads, writes, copies or stores a credential rather than naming
  a slot.
- A new `CLAUDE_*` variable reaching a child without passing
  `isInheritedSessionVar`.
- `Probe` growing a token-spending call.
