# AGENTS.md — cmd/mimir-daemon

`main` package. **Wiring and process lifecycle only** — the same rule as
`cmd/mimir-mcp`, for the long-running half of the system.

## Rules for this directory

- Allowed here: `config.Load()` + `ValidateDaemon()`, opening the store,
  constructing clients and the pipeline, building the MCP server and registering
  tools via `tools.RegisterAll`, constructing the bus/registry/runner, building
  `api.Server`, signal → `context` cancel, mapping a startup error to a non-zero
  exit.
- **Not** allowed here: routes, handlers, business logic, prompt text, response
  shaping. Those live in `internal/api` and the runtime packages.
- **Tools come from `tools.RegisterAll`, never a list written out here.** This
  binary and `cmd/mimir-mcp` are two transports over one engine; a second
  hand-maintained registration list is exactly how that stops being true.
- **The store is required here.** `cmd/mimir-mcp` degrades to an uncached
  pipeline if it cannot open the database, because caching is a nice-to-have
  there. For the daemon, projects and runs *are* the store — fail at startup
  with the path named rather than serve routes that all 500.
- **Shutdown order matters:** `Serve` returns, then `runner.Wait()`, then the
  deferred `db.Close()`. A run still writing its result row needs the database
  open. Reordering these loses the outcome of the last run.
- **stdout is sacred** (SD-4). Startup, the resolved listen address, and fatal
  errors all go to stderr via `slog`.
- No flags, no config file. The only environment this binary reads is the
  process plumbing its parent provides — see `internal/config/AGENTS.md`.

## Reviewer focus

SD-1 (nothing behavioural from the environment), SD-4 (no stdout), SD-3 (signal
cancel actually stops `Serve`, and `Wait` joins in-flight runs before close).
