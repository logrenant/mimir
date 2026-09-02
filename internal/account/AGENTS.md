# AGENTS.md — internal/account

Owns which Claude Code identity a coding run spends. Introduced by
`tasks/task-37`.

## What an account is here

**A credential slot, not a login.** The CLI keeps its credentials in the macOS
keychain and derives the entry it uses from `CLAUDE_SECURESTORAGE_CONFIG_DIR`,
so a directory path is the entire handle: no directory means the CLI's own
default entry, and any other directory means a second, separately authenticated
identity. The directory's *contents* are irrelevant — it is a hash input.

Mimir never sees, stores, moves or invalidates a credential. It decides which
slot a subprocess is pointed at, and nothing else. `Delete` forgets a slot; it
does not log anybody out, and it must never grow the ability to.

## Rules for this directory

- **A path is accepted once, at `Register`, and never again.** Everything
  afterwards carries the opaque id. This is the same discipline as
  `internal/project` and it is deliberate: the shape is copied because the
  reason is the same.
- **Registration is idempotent by directory, not by label.** The same path is
  the same keychain entry. A second row for it would let the dispatcher believe
  one identity could run two tasks at once, which is the thing this package
  exists to prevent.
- **The empty config dir is a real, valid account** — the CLI's own slot, and
  what a single-account machine has always been using. It is not "unset".
- **`Environ` expresses the default slot as the variable's *absence*.** Setting
  `CLAUDE_SECURESTORAGE_CONFIG_DIR=""` would hash the empty string into a third,
  nameless slot. If you touch that function, keep the distinction.
- **`Environ` also strips the launching session's variables.** A daemon started
  from inside a Claude Code session passes `CLAUDECODE=1`, a session id, a
  messaging socket and a bridge token to its children; a nested CLI reading
  those believes it is resuming somebody else's session. The strip list is
  `isInheritedSessionVar` — add to it, never remove.
- **`Probe` never returns an error for "not signed in", or for a slot it cannot
  read.** Both are answers about that slot and belong on the account row. An
  error from `Probe` means the probe itself could not run at all.
- **`Probe` spends nothing.** `claude auth status` reads a keychain entry and
  prints JSON — no API call, no tokens. That is why the desktop app asks live
  rather than caching a guess at registration. Do not add anything here that
  costs money to ask.
- **`Delete` refuses while queued or running work points at the slot.**
  Forgetting it would strand a queue nothing can drain.
- **`List` is oldest first**, and the dispatcher tries slots in that order.
  Automatic assignment is meant to be predictable, not arbitrary.

## Testing

No keychain and no real CLI. `Probe` is exercised against shell stubs that
print not-JSON and that exit non-zero, which are the two failure shapes that
actually happen. `Environ` is a pure function and is tested as one — including
that `PATH` and `HOME` survive the strip.

## Reviewer focus

- Any second place a config directory is read, or any path parameter outside
  `Register`.
- Anything that reads, writes, copies or deletes a credential rather than
  naming a slot.
- A new `CLAUDE_*` variable reaching a child without passing `isInheritedSessionVar`.
- `Probe` growing a token-spending call.
