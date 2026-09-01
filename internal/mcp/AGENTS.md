# AGENTS.md — internal/mcp

The MCP protocol layer: transport, tool registry, JSON schemas, error mapping, and
the **response choke-point** that enforces SD-2 and SD-7.

## Rules for this directory
- Wraps `github.com/modelcontextprotocol/go-sdk` (pinned exact tag, SD-5). Transport
  is **stdio**. This package is the *only* code permitted to write MCP frames to
  `os.Stdout` (SD-4). Everything else — logs, diagnostics — is `slog` → `stderr`.
- Owns the `Tool` interface (`Name`, `Description`, `InputSchema`, `Handle`) and the
  `Registry`. Duplicate tool names are rejected at registration.
- **Every** successful `Handle` result passes through `finalizeResponse` before it
  becomes an MCP result. No tool serialises around it. `finalizeResponse` is
  fail-closed:
  - non-metadata tools must carry a truthy `refined` marker (or implement the
    refined/size interface) → else `ErrIsolationViolation`;
  - payload over the tool's token budget → `ErrResponseTooLarge` (reject, do not
    silently truncate — upstream refiner owns truncation);
  - raw-HTML / giant-blob signatures in any field → `ErrIsolationViolation`.
- `Handle` panics are recovered at this edge, logged with stack to stderr, and
  mapped to MCP errors. **No panic escapes to the process** (SD-4).
- Known sentinel errors from lower layers (`crawl.ErrDockerUnavailable`,
  `refine.ErrClaudeUnavailable`, `refine.ErrRefineRejected`,
  `search.ErrSearchUnavailable`, `pipeline.ErrNoUsableSources`) map to MCP errors
  that carry the actionable fix text (SD-6).
- This package does **not** call DuckDuckGo, Crawl4AI, or the `claude` CLI. It
  calls `internal/tools`, which calls `internal/pipeline`/`internal/search`.
- Per-call: generate a `request_id`, put it on the context, emit `tool.start` /
  `tool.end` with duration.

## Reviewer focus
SD-2/SD-7 (choke-point is single, structural, fail-closed), SD-4 (stdout only here,
no escaping panic), SD-6 (sentinel → actionable message).
