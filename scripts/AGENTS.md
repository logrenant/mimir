# AGENTS.md — scripts/

Install-time plumbing: the always-on daemon, and the MCP registration every
client on the machine reads. Shell only — no logic that belongs in Go or Rust.

## What lives here

| Script | Does |
|--------|------|
| `install-agent.sh` | Builds `mimir-daemon`, installs it under `~/Library/Application Support/mimir/bin/`, mints (or reuses) a port + token, writes `endpoint.json` and `~/Library/LaunchAgents/studio.mimir.daemon.plist`, bootstraps the agent, then proves `/healthz` answers. |
| `uninstall-agent.sh` | Boots the agent out and removes the plist; `--purge` also drops the installed binary and `endpoint.json`. |
| `install-mcp.sh` | Installs `mimir-mcp` under the same `bin/`, registers it with every MCP client present (claude, agy + Antigravity IDE, gemini, VS Code), and installs the session preflight hook. `--uninstall` reverses it. |
| `mimir-preflight.sh` | Runs at the start of a session: checks the installed binary, re-registers if a client's config lost the entry, health-checks the daemon and restarts it if it is down, then injects one line telling the model a memory exists. |

## Rules for this directory

- **The daemon's contract does not change here.** `mimir-daemon` still learns
  only two things from its environment — `MIMIR_DAEMON_PORT` and
  `MIMIR_DAEMON_TOKEN` (`internal/config/config.go`). This script is a second
  legitimate *parent*, not a new configuration surface. Adding a third
  behaviour variable to the plist is an SD-1 violation exactly as it would be
  in the Tauri shell.
- **`PATH` is plumbing, not behaviour, and it is mandatory.** launchd starts a
  process with `/usr/bin:/bin:/usr/sbin:/sbin`. `internal/refine` and
  `internal/coderunner` both resolve the `claude` CLI off `PATH`, so an agent
  without a widened `PATH` starts cleanly and then fails every refine and every
  coding run. The plist prepends the directory `claude` actually resolves to at
  install time, plus the usual `~/.local/bin` / Homebrew locations.
- **Two files hold the token, both `0600`**: `endpoint.json` (what the desktop
  app reads) and the plist (what launchd hands the child). Anything written
  here goes through `umask 077` and an explicit `chmod`. See `docs/SECURITY.md`
  for the trade-off this makes against the shell-spawned, per-launch, in-memory token.
- **Re-running is not re-provisioning.** An install reuses the existing port and
  token unless `--rotate-token` is passed; rotating on every install would
  silently disconnect a desktop app holding the old one.
- **The store is never touched.** `mimir.db` holds projects, run history and the
  project memory. Not by install, not by uninstall — only `--purge` removes
  anything, and even then not the database.
- **Registration goes through each client's own CLI, never its JSON.** All four
  have one (`claude mcp add`, `agy mcp add`, `gemini mcp add`, `code --add-mcp`),
  and their file shapes are theirs to change. Four hand-written mergers would be
  four things to keep in step with four release cycles. The two hook files are
  the exception, because neither client has a command for those; they are merged
  with `jq` into a temp file and moved into place, and they must never disturb a
  key they did not write.

- **Clients are pointed at the support directory, never at a checkout.** A
  config naming a path inside a repository breaks the moment the repository
  moves, and it would break for four clients at once. `install-mcp.sh` copies
  the binary the same way `install-agent.sh` does — to a temp name, then `mv`.

- **The preflight has no `set -e`, and always exits 0.** A hook that aborts
  before printing its envelope is worse than one that reports a problem: the
  client gets nothing at all, and on the agy side an empty stdout is not a valid
  response. Every path ends in an envelope. Its only stdout is that envelope;
  diagnostics go to stderr, which both clients log and neither parses.

- **`agy` has no `SessionStart` event.** Its documented lifecycle is
  `PreToolUse`, `PostToolUse`, `PreInvocation`, `PostInvocation`, `Stop`. The
  preflight rides `PreInvocation` and guards on `invocationNum`, which is what
  turns a per-turn event into a per-conversation one. Removing that guard would
  run a health check and a `curl` before every model call.

- **Bounded recovery.** The daemon health retry is ten half-second looks and
  then it gives up with an honest note. A session must never wait on a daemon
  that is not coming back.

- **POSIX `sh`, no bashisms.** Checked with `sh -n` in `make lint`;
  formatted with `shfmt` conventions (tabs).
