# AGENTS.md — scripts/

Install-time plumbing for the always-on daemon. Two shell scripts, no logic
that belongs in Go or Rust.

## What lives here

| Script | Does |
|--------|------|
| `install-agent.sh` | Builds `goat-daemon`, installs it under `~/Library/Application Support/goat-mcp/bin/`, mints (or reuses) a port + token, writes `endpoint.json` and `~/Library/LaunchAgents/com.goat.daemon.plist`, bootstraps the agent, then proves `/healthz` answers. |
| `uninstall-agent.sh` | Boots the agent out and removes the plist; `--purge` also drops the installed binary and `endpoint.json`. |

## Rules for this directory

- **The daemon's contract does not change here.** `goat-daemon` still learns
  only two things from its environment — `GOAT_DAEMON_PORT` and
  `GOAT_DAEMON_TOKEN` (`internal/config/config.go`). This script is a second
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
  for the trade-off this makes against goat's per-launch, in-memory token.
- **Re-running is not re-provisioning.** An install reuses the existing port and
  token unless `--rotate-token` is passed; rotating on every install would
  silently disconnect a desktop app holding the old one.
- **The store is never touched.** `goat.db` holds projects, run history and the
  project memory. Not by install, not by uninstall — only `--purge` removes
  anything, and even then not the database.
- **POSIX `sh`, no bashisms.** Checked with `sh -n` in `make lint-scripts`;
  formatted with `shfmt` conventions (tabs).
