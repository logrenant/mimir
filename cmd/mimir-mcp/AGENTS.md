# AGENTS.md — cmd/mimir-mcp

`main` package. **Wiring and process lifecycle only.**

## Rules for this directory
- Allowed here: `config.Load()` + `Validate()`, constructing clients
  (`search`, `crawl`, `refine`), building `pipeline.Pipeline`, constructing the
  `mcp.Server`, registering tools, installing the SIGINT/SIGTERM → `context`
  cancel, calling `server.Run(ctx)`, mapping a startup error to a non-zero exit.
- **Not** allowed here: business logic, HTTP calls, parsing, orchestration,
  prompt text, response shaping. If you are tempted to write an `if` about search
  results here, it belongs in `internal/`.
- **stdout is sacred** (SD-4): nothing in `main` writes to `os.Stdout`. Startup and
  fatal errors go to `stderr` via `slog`. The MCP SDK owns stdout.
- No flags, no env parsing, no config file (SD-1). `main` takes no arguments that
  change behaviour.
- Tool registration list is the one place that enumerates every tool — keep it in
  sync as tasks add `web_search`, `fetch_page`, `research`, `diagnostics`.

## Reviewer focus
SD-1 (no knobs), SD-4 (no stdout), SD-3 (ctx cancel actually stops `Run` cleanly).
