# AGENTS.md — internal/pipeline

Orchestration: `search → crawl → refine → merge`. Home of **SD-3** (bounded,
cancellable, deadlock-free concurrency) and of partial-failure tolerance (SD-6).

## Rules for this directory
- Fan-out uses `golang.org/x/sync/errgroup` with `g.SetLimit(n)` where `n` comes
  from `internal/config` (`MaxConcurrentCrawls`, `MaxConcurrentRefines`) — never a
  literal. No bare `go` loop over a slice of results.
- A process-wide weighted semaphore (`GlobalCrawlSlots`, `GlobalRefineSlots`) caps
  crawls/refines across *all* concurrent `Research`/`Fetch` calls. Acquire with the
  request `ctx`; **always `defer release()`**.
- Every external call gets a `context.Context` derived from the MCP request.
  `Research` wraps it with `cfg.ResearchTimeout`. **No `context.Background()` or
  `context.TODO()`** in non-test code here — `make lint` checks this.
- Partial failure is normal: a crawl or refine that fails/times out drops that one
  source and adds a line to `Brief.Gaps`. It does **not** cancel the group. Only
  when *every* source fails → `ErrNoUsableSources`.
- Channel operations are always `select { case …: case <-ctx.Done(): }`. No
  goroutine may outlive the call — pipeline tests run `goleak.VerifyNone` and
  `go test -race`.
- `Brief.Refined` / `RefinedPage.Refined` is true only when every included piece of
  content came through `internal/refine`. The final `Brief` must fit
  `cfg.ResearchBriefMaxTokens`; `RefinedPage` must fit `cfg.FetchPageMaxTokens`.
- Merge/dedup compares normalized bullet text to collapse duplicates across
  sources. `Sources` are numbered from 1 in brief order.
- This package does not know about MCP. It returns typed values and typed errors;
  `internal/tools` + `internal/mcp` do the protocol mapping.

## Read-through cache (task-18, extended for FetchRaw)

Every crawl and every refine goes through `cachedCrawl` / `cachedRefine` —
never call `p.crawl.Markdown` or `p.refine.Distil` directly. `FetchRaw` has
its own `cachedFetchRaw`, caching the full page (RawHTML included) via
`store.GetCrawlFull` instead of the refine-only `GetCrawl` — see its doc
comment for why the cache key is URL-only (opts excluded) and
`internal/store/AGENTS.md`'s SD-2 note for why RawHTML is allowed to leave
the refine path at all here. These three helpers own the whole contract:

- **Slot acquisition lives inside them, and only on a miss.** A cache hit takes
  no global semaphore slot and makes no external call. Acquiring before the
  cache check hands the cache straight back to contention under exactly the load
  it exists to relieve — `TestCachedCrawl_HitTakesNoGlobalSlot` /
  `TestCachedFetchRaw_HitTakesNoGlobalSlot` fail if you do.
- **A cache error is a miss, never a failure.** Log at debug and fall through to
  the live path. No cache error may reach the caller (SD-6).
- **`p.cache` may be nil** and must then behave identically to the uncached
  pipeline. Existing tests pass `nil` for exactly this reason.
- **The refine cache key is total** (`store.RefineKey`): URL, query, token
  ceiling, content hash, prompt version. If you add an input to what the refiner
  sees, add it to the key too — otherwise stale text replays under a new
  contract.
- Raw markdown from a crawl hit is untrusted scraped text like any other. It
  flows only into `refine` (SD-2).

## Reviewer focus
SD-3 (`SetLimit` from config, ctx everywhere, `-race` + goleak, every slot
released, no deadlock), SD-6 (one failure ≠ abort; `ErrNoUsableSources` only when
all fail), SD-7 (`Brief` within ceiling).
