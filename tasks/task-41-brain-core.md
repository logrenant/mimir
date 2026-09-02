# task-41 — Brain v2: node core on the store, and the provider layer

- **Status:** done
- **Owner agent:** daemon
- **Prerequisites:** none
- **Primary paths:** `internal/llm/**`, `internal/brain/**`, `internal/store/migrations/0013_brain.sql`, `internal/store/brain.go`, `internal/refine/refine.go`, `internal/config/config.go`, `internal/tools/{brain.go,register.go,chokepoint_test.go}`, `cmd/mimir-mcp/main.go`, `cmd/mimir-daemon/main.go`
- **Roadmap bucket:** B.9 (project memory) extension — the Brain layer, plus the provider split recorded in §B.1

## Context

Commit `013f7d7` shipped a Brain layer without a task file. Three things are wrong with it and all three are load-bearing.

It does not work at all. `internal/tools/brain.go`'s three handlers return a plain `string`, which implements neither `mcp.MetadataResponse` nor `mcp.RefinedResponse`, so `internal/mcp/finalize.go`'s fail-closed choke-point rejects every call with `ErrIsolationViolation`. The suite stayed green because `TestLive_MCPRouteExposesTheCanonicalToolSet` asserts tool *names* and never invokes one, and because the three response types were never added to `internal/tools/chokepoint_test.go` — the test that exists precisely to catch this.

Its architecture is the shape `docs/ROADMAP.md` §B.9 says M8 was built to make unreachable: a node per Markdown file, `ClusterEngine.LinkNode` rewriting neighbour files in place on every ingest (swallowing the error), `QueryNodes` an O(N) directory scan with `strings.Contains` that searches only the id and the tags, no node dedup (`GenerateID` salts with `UnixNano`, so re-ingesting one repo yields two nodes), no project scoping, and a storage root of `filepath.Join("data","brain")` — a relative path that follows the process CWD.

It sits outside the house rules: no tests (SD-8), a `GITHUB_TOKEN` read straight from `os.Getenv` (SD-1's third category, ungranted), an unsynchronised package-level `brainCore` singleton where every other tool takes its collaborators through `tools.Deps`, and a hand-copied duplicate of `internal/refine`'s subprocess machinery.

This task replaces the Markdown store with rows in the existing SQLite store, and introduces the provider layer the owner directed in `docs/ROADMAP.md` §B.1: cheap distillation runs on `agy`, reasoning and coding stay on `claude`.

## Scope (do exactly this)

1. **`internal/llm`** — one exit point for every non-coding LLM call. `Provider` interface (`Complete`, `Health`), `Request`/`Response`, a `Router` that maps a `Class` (`distill` | `reason`) to a provider with a fallback. Two implementations:
   - `internal/llm/claudecli` — the exec/args/retry logic moved out of `internal/refine/refine.go` unchanged: `-p --model … --output-format json --no-session-persistence --strict-mcp-config --restricted --effort low --system-prompt … --disallowedTools …`, content on stdin, 2 attempts with 100ms backoff.
   - `internal/llm/agycli` — `agy --model … --output-format json --input-format text --sandbox --disable-slash-commands --print-timeout … [--json-schema <tmpfile>]`, content on **stdin** (`-p` takes the prompt as its value and ARG_MAX is 1 MiB), `cmd.Dir` a scratch directory, `MIMIR_NESTED=1` in the environment. Read `structured_output` when a schema was given, else `response`; `status != "SUCCESS"` is an error.
2. **`internal/refine`** keeps its public API and its `ErrClaudeUnavailable` sentinel; only `Client.run` changes, to delegate to the claudecli provider.
3. **`cmd/mimir-mcp`** short-circuits to a zero-tool server when `MIMIR_NESTED=1`, so an `agy` subprocess that loads Mimir as a global MCP server cannot recurse into it.
4. **Config** — the constants in §Interfaces. `MIMIR_GITHUB_TOKEN` replaces the bare `GITHUB_TOKEN` read and is documented as an operator-provisioned credential.
5. **Migration `0013_brain.sql`** — `brain_nodes`, `brain_edges`, `brain_fts` (external-content FTS5 with the three triggers, following `0008_memory.sql` exactly). No vector table.
6. **`internal/store/brain.go`** — all Brain SQL: `UpsertNode`, `GetNode`, `SearchNodes` (bm25), `UpsertEdges`, `NeighborsOf`, `NodesByIDs`.
7. **`internal/brain`** rewritten: `Core` over the store, `distill.go` (one node in, validated title/assessment/tags/aliases out), `relate.go` (tag-Jaccard edges free, one `agy` relation pass over the FTS candidates for semantic edges), `github.go`. `storage.go`, `cluster.go`, `llm_summarizer.go`, `node.go`'s Markdown serialisation all go.
8. **`internal/tools/brain.go`** rewritten with real response types; `brain_related` added. All four registered through `tools.Deps` and added to `chokepoint_test.go`.
9. Tests per SD-8.

## Out of scope (do NOT do here)

- MCP client registration and the session preflight hook (task-43).
- Promoting memory episodes into nodes, the agy conversation run-source, commit nodes, and the daemon's `/brain/*` routes (task-45).
- Any desktop surface.
- Touching `internal/coderunner` — its stream-json/permission-mode/process-group semantics are a different contract.
- A vector index or any local embedding model.

## Interfaces / contracts

```go
// internal/llm
type Class string // "distill" | "reason"
type Request  struct { System, User string; Schema json.RawMessage; MaxTokens int }
type Response struct { Text string; Structured json.RawMessage; Provider, Model string }
type Provider interface {
    Name() string
    Model() string
    Complete(ctx context.Context, r Request) (Response, error)
    Health(ctx context.Context) error
}
func NewRouter(cfg config.Config) *Router
func (r *Router) Complete(ctx context.Context, c Class, req Request) (Response, error)
```

```go
// internal/config additions
DistillProvider          = "agy"
DistillModel             = "gemini-3.7-flash-low"
DistillFallback          = "claude"
ReasonProvider           = "claude"
AgyCLIPath               = "agy"                  // MIMIR_AGY_CLI_PATH
AgyPrintTimeout          = 90 * time.Second
GitHubToken                                       // MIMIR_GITHUB_TOKEN, may be empty
BrainRelateCandidates    = 20
BrainRelateMinWeight     = 0.5
BrainTagJaccardMin       = 0.34
BrainNeighborCap         = 8
BrainSearchLimit         = 8
BrainAssessmentMaxTokens = 160
BrainBodyMaxChars        = 8000
BrainSearchMaxTokens     = 1400
BrainPromptVersion       = "brain-v2"
```

Tools: `brain_ingest_data {source, content, kind?, project_path?}`, `brain_ingest_github {repo, project_path?}`, `brain_query_nodes {query, project_path?, limit?}`, `brain_related {node_id, limit?}`.

## Definition of Done

- `make check` green.
- `gofmt -l internal/brain internal/llm` prints nothing.
- All four brain response types present in `internal/tools/chokepoint_test.go`; a test proves a brain response survives `finalizeResponse`.
- Re-ingesting one source twice leaves exactly one row in `brain_nodes`.
- A query term that appears only in a node's `aliases` still matches.
- With `agy` unavailable, an ingest still writes the node and its tag edges.
- No `os.Getenv` outside `config.Load()`; no writes to stdout outside the MCP transport.

## Notes for the reviewer (Opus)

The `agy` invocation shape was verified live before any code was written: `-p` is not a boolean, `--input-format text` + stdin + `--json-schema` work together, and `structured_output` comes back conforming to the schema while `response` carries extra keys. The provider reads `structured_output`.

`agy` has no `--disallowedTools` or `--strict-mcp-config` equivalent. The mitigation is threefold — `--sandbox`, an empty scratch working directory, and `MIMIR_NESTED=1` making `mimir-mcp` open with no tools — and it is weaker than the claude path's guarantee. `docs/SECURITY.md` must say so plainly rather than carry the old sentence unchanged.

## Changelog

- 2026-09-02 — Implemented. `internal/llm` (router + claude + agy providers),
  `internal/brain` rewritten onto `0013_brain.sql`, four MCP tools with real
  response types, `internal/refine` delegating to the router, `MIMIR_NESTED`
  short-circuit in `cmd/mimir-mcp`. `make check` green.
- Found on the way: every test that faked the `claude` CLI was reaching the real
  `agy` on PATH once routing changed. `e2e` dropped from 29s to 7s after
  `MIMIR_AGY_CLI_PATH` was pinned and an empty `AgyCLIPath` stopped defaulting.
