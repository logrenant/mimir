# task-43 — Registration on every client, and a session preflight

- **Status:** done
- **Owner agent:** daemon
- **Prerequisites:** task-41
- **Primary paths:** `scripts/install-mcp.sh` (new), `scripts/mimir-preflight.sh` (new), `scripts/AGENTS.md`, `Makefile`, `docs/INSTALL.md`
- **Roadmap bucket:** B.1 requirement 2 — keeping repetitive work off the token bill

## Context

Nothing in this repository registers `mimir-mcp` with anything. `claude mcp add`
appears once, in `docs/INSTALL.md`, as a command a person is expected to type,
and on this machine the result is a *project-scoped* entry in `~/.claude.json`
that silently stops existing the moment work moves to another directory. The
other three clients installed here — `agy` (which shares its configuration
directory with Antigravity IDE), the `gemini` CLI, and VS Code — have no entry at
all.

That makes the memory and the knowledge base opt-in per directory, which is the
one thing they cannot be: a memory nothing thinks to consult costs strictly more
than no memory, because it is paid for and never read.

The second half is availability. `scripts/install-agent.sh` already writes
`KeepAlive=true`, so launchd keeps the daemon up across crashes and logins.
What is missing is the session side: nothing checks, when a session opens, that
the binary is where the client's config says it is and that the daemon is
answering — and nothing tells the model that a memory exists before it starts
re-reading the repository.

## Scope (do exactly this)

1. **`scripts/install-mcp.sh`** — idempotent registration on every client present:

   | Client | Command | Config it writes |
   |---|---|---|
   | `claude` | `claude mcp add -s user mimir -- <bin>` | `~/.claude.json`, **user** scope |
   | `agy` + Antigravity IDE | `agy mcp add mimir <bin>` | `~/.gemini/config/mcp_config.json`, shared by both |
   | `gemini` | `gemini mcp add -s user mimir <bin>` | `~/.gemini/settings.json` |
   | VS Code | `code --add-mcp '{"name":"mimir",…}'` | the user MCP config |

   Every one of the four has its own CLI for this, so the script calls those
   rather than hand-merging their JSON — their file shapes are theirs to change.

2. **The binary is installed, not referenced in place.** `bin/mimir-mcp` is
   copied to `~/Library/Application Support/mimir/bin/`, the way
   `install-agent.sh` already does for the daemon, and every client is pointed
   there. A config naming a path inside a checkout breaks when the checkout
   moves, and it would break for four clients at once.

3. **`scripts/mimir-preflight.sh`** — one script, two output envelopes, wired to:
   - `~/.claude/settings.json` → `hooks.SessionStart`
   - `~/.gemini/config/hooks.json` → `PreInvocation` (agy has no `SessionStart`
     event; `invocationNum > 1` is the guard that makes this fire once per
     conversation)

   It checks the installed binary, re-registers if the client's config has lost
   the entry, health-checks the daemon and `launchctl kickstart`s it if it is
   down, then injects a short line telling the model to call `project_context`
   first.

4. **Makefile**: `install-mcp`, `uninstall-mcp`.
5. **`docs/INSTALL.md`**: the manual `claude mcp add` becomes `make install-mcp`.

## Out of scope (do NOT do here)

- Anything in `internal/**` or `cmd/**` — this task is scripts and docs only, so
  it cannot collide with a Go task (`tasks/README.md`'s parallelism rule).
- Ollama or any client that does not speak MCP.
- Promoting memory episodes into brain nodes, the agy conversation run-source,
  the `/brain/*` daemon routes (task-45).

## Interfaces / contracts

```
scripts/install-mcp.sh   [--no-build] [--no-hooks] [--hooks-only] [--uninstall]
scripts/mimir-preflight.sh --format claude|agy      # hook payload on stdin
```

Claude Code hook output (verified against the installed CLI):
```json
{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"…"}}
```
agy hook output (verified against the installed CLI's embedded docs):
```json
{"injectSteps":[{"ephemeralMessage":"…"}]}
```

## Definition of Done

- `make install-mcp` twice in a row leaves the same configuration (idempotent).
- All four clients list `mimir` afterwards.
- With the daemon stopped, opening a session brings it back and the session is
  never blocked; with everything already healthy the hook adds one short line.
- The preflight exits 0 on every path, including a missing `jq`, a missing
  `endpoint.json`, and a `launchctl` that refuses.
- `make lint` passes (`sh -n` on both new scripts).

## Notes for the reviewer (Opus)

`agy` has no `SessionStart` event. Its documented lifecycle is `PreToolUse`,
`PostToolUse`, `PreInvocation`, `PostInvocation`, `Stop`; the binary carries a
`SessionStartHookArgs` proto but does not expose it. `PreInvocation` plus the
`invocationNum` guard is the closest honest equivalent, and it costs one `sh`
spawn per turn that exits before doing anything on turns after the first.

The preflight deliberately does **not** use `set -e`. A hook that aborts before
printing its envelope is worse than one that reports a problem: the client sees
no output at all, and on the agy side an empty stdout is not a valid response.

## Changelog

- 2026-09-02 — Implemented and verified on this machine. All four clients
  registered and confirmed listing `mimir`; both hooks installed; a second run
  left the configuration byte-identical; `--uninstall` removed everything it
  wrote and left the user's other settings intact.
- Verified end to end rather than by inspection: a real `agy` session lists all
  seven Mimir tools, and `MIMIR_NESTED=1` makes `mimir-mcp` serve **zero** tools
  against fifteen normally — so the refiner's own agy subprocess cannot recurse
  into the server that spawned it.
- The degraded paths were exercised in an isolated `HOME`: missing binary,
  missing `endpoint.json`, and a daemon that cannot be recovered. All three exit
  0 with a valid envelope, and the worst case is bounded at five seconds.
