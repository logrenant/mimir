# AGENTS.md — internal/store

The local SQLite persistence layer. Introduced by `tasks/task-17` for Track B /
M1; it becomes the shared substrate for the rest of Track B (companies,
categorization, outreach emails, projects, coding runs).

## What this package is for

Not paying twice for work already done — a Crawl4AI fetch already made, or a
`claude` CLI refine already distilled. That is the whole mandate.

## Rules for this directory

- **The store is a cache, never a source of truth the consumer sees.** Losing
  the database must cost time, never correctness. Every accessor tolerates a
  nil `*Store` and returns a miss rather than an error where it can (SD-6).
  `Open` returning an error means the caller continues with `nil`, not that the
  process aborts.

- **SD-2 — `crawl_pages` holds RAW scraped markdown (and, since 0003, raw
  HTML).** That is deliberate: markdown is the input to `internal/refine`,
  and re-reading it is what makes a repeat query free. It is also the single
  most dangerous thing in this package. `GetCrawl`'s result may flow **only**
  into `refine.Distil` — its SELECT deliberately omits `raw_html` so this is
  structural, not conventional. `GetCrawlFull` is the one sanctioned
  exception: it feeds `pipeline.FetchRaw`, whose only callers are the Stage F
  tools (`internal/ecommerce`/`tiktok`/`gmaps`/`instagram`) parsing structured
  fields via `internal/extract` — never returning HTML verbatim. If you add
  another accessor that returns raw page text, say so in its doc comment and
  justify the call site.

- **Only successful refines are cached.** `PutRefined` drops any output with
  `Refined == false`; replaying one would hand the consumer text that never
  passed the refiner. A cached hit always replays `Refined: true` plus the
  stored `Truncated` flag, so the choke-point's ceiling check still applies
  normally (SD-7).

- **Cache keys are total.** A refine result is a function of page content,
  query, token ceiling, and prompt template — `RefineKey` carries all four
  (`ContentHash` covers the first). Adding an input to the prompt without
  adding it to the key is a correctness bug: stale text replays under a new
  contract. `PromptVersion` comes from `config.RefinePromptVersion`; bump that
  constant to invalidate every refined row at once (SD-5 pinning discipline).

- **A region search is all-or-nothing (task-23).** `region_searches` records
  the ordered `place_id` list a Places search returned; `GetRegionSearch`
  returns a hit only when every company it named is still present and fresh.
  A partial region would quietly shrink a lead list, which is worse than
  paying for the search again. The inverse also holds: a search that
  legitimately found **nothing** is a hit, not a miss — re-asking Places for
  an empty region is a billed request for an answer already known.

- **`category_gap_analysis` has a real content key (0006, M6).** Unlike
  `company_categorization`, whose only invalidation is a version bump, a gap
  analysis is a synthesis across a *specific* set of companies. The key is
  `(region, category, prompt_version, company_set_hash)` — `company_set_hash`
  (sha256 over the sorted place_id list) is computed by `internal/leadgen`, and
  a changed set misses. No TTL, same reasoning as 0005. `PutGapAnalysis` drops
  an empty analysis, like `PutRefined` drops an unrefined output.

- **`outreach_emails` carries a human decision, not just a cache entry (0007,
  M6).** The `status` column is `draft` / `sent` / `skipped`. `PutOutreachEmail`
  (a region re-run) overwrites the body **only** where `status = 'draft'` — the
  guard is in the SQL `ON CONFLICT ... WHERE`, not just the caller, so a
  concurrent write cannot resurrect a sent email. `SetOutreachEmailStatus` is
  the only way to leave `draft`; it validates against the closed set and returns
  `ErrEmailNotFound` if there is no row to act on. Key is
  `(place_id, prompt_version)`, `prompt_version` = `config.LeadgenEmailVersion`.

- **`leads` is a record, not a cache (0016, task-63).** Everything else in this
  package exists so work is not paid for twice; the ledger exists so a run
  outlives its own HTTP response. That is why it is beside `companies` rather
  than inside it: `GetCompany` deletes expired rows as it reads them and
  `GetRegionSearch` misses a whole region when one member ages out, which is
  correct for a cache and would silently lose businesses from a record. The
  ledger has no TTL and no reader that deletes. `PutLeadRun` is one transaction
  — run row, lead upserts, memberships — so a run can never name businesses the
  ledger does not hold, and its `ON CONFLICT` never overwrites a populated
  column with an empty one: the free scrape returns no phone where the billed
  Places call did, and a later re-run through the cheaper provider must not
  erase a number already known. `lead_run_members` carries its own `category`
  because a taxonomy bump rewrites `leads.category`, and what a past run
  answered should stay readable.

- **`chat_turns` is the conversation; `memory_episodes` is the index (0017,
  task-65).** They are written from one parse, keyed by the same
  `episode_key`, and they trade in opposite directions: the episode row clips
  the prompt and the assistant text (`sessionlog.MaxPromptChars` /
  `MaxAssistantChars`, applied by `internal/memory`, not by the parser) so a
  recap prompt stays cheap, and the archive keeps exactly what that clipping
  drops. Nothing here is distilled and no model ever reads it whole, which is
  what makes running it over a backlog of months a migration rather than a
  bill. `PutChatTurn` recomputes its session's span and turn count from the
  turns rather than incrementing them: a transcript still being written is
  re-read from its offset every pass, and an incremented count would climb
  forever. `chat_fts` is external-content like the other two indexes, with the
  same `'delete'`-before-re-insert triggers and the same rule about aliases.

- **`brain_node_versions` is appended by the upsert, not by a caller (0018,
  task-67).** The old content hash is only visible inside `UpsertBrainNode`,
  before it writes — a caller would have to read the row first and race itself
  — and putting it there means every ingest path (the resident scan,
  `mimir-scan`, `brain_scan_repo`, the capture loop) gets history without a
  second writer existing. A version is written when the hash moved or the node
  is new, never when `ContentHash` is empty: a failed distil deliberately
  arrives with an empty hash so the file is offered again, and recording that
  would write a row claiming the file became unreadable. The table is keyed by
  rowid, not `(node_id, content_hash)`, because reverting a file to a previous
  version is a real event and deserves its own row. `BrainVersionsPerNode` is
  read once at `Open` and pruned on insert — a file edited every minute for a
  year must not become the largest table here, and this package has no
  background sweeper to trim it later.

- **`rate_limit_log` is a record, not a cache (0021).** Three phases — a run
  the token budget cut off, a queued task held behind it, the window rolling
  over — appended and never updated. Durable rather than an in-memory ring
  (which is what the brain scan's console is) because the two ends of one pause
  can be hours and a daemon restart apart: the runner rebuilds the pauses still
  in force from these rows at startup, and an operator asking in the morning
  what happened overnight is asking about rows. A pause and the resume that
  ended it are two facts about two different times; collapsing them into one
  mutable row would lose exactly the history the table exists for.

- **Migrations are append-only.** `migrations/NNNN_*.sql`, applied in lexical
  filename order and recorded in `schema_migrations`. Never edit a file that
  has shipped — add the next number. `Open` is idempotent.

**The memory index is external-content FTS5, kept honest by triggers.**
`memory_fts` stores no copy of its own; `memory_episodes_ai/ad/au` are the only
thing making it true. The `'delete'` row before each re-insert is required by
FTS5's external-content contract — without it an upsert leaves the previous
terms matching forever, and the memory answers with text it no longer holds
(`TestSearchEpisodes_ReindexesOnUpdate`). FTS5 can only index real columns,
which is why `files_text`/`commands_text` are denormalized beside `facts_json`.

**An FTS query is a syntax, not a string.** A bare `"`, `*`, `:` or the word
`OR` from a natural-language question is a syntax error or, worse, a silent
change of meaning. `ftsQuery` reduces caller text to quoted alphanumeric terms
joined with `OR` and lets `bm25` do the ranking. An unparseable query is a miss,
never an error: "no results" is a truthful answer, while an error implies the
memory is broken.

**Do not alias the FTS table.** SQLite resolves `MATCH` and `bm25()` against the
virtual table's real name; an alias fails with `no such column`.

- **Every query takes a `context.Context`** and is bounded by the caller's
  deadline (SD-3). No background sweepers, no goroutines in this package: TTL
  is enforced on read, and an expired row is deleted opportunistically by the
  reader that found it.

- **No user-facing knob** enables, disables, or tunes the cache (SD-1). The
  path, TTL, and prompt version are constants in `internal/config`;
  `MIMIR_STORE_PATH` exists for tests only.

## Testing

SQLite is an in-process library, not an external service, so it is **not**
mocked — tests run against a real database under `t.TempDir()`. Cover, at
minimum: migration idempotency, put/get round-trip fidelity, and a miss for
each independent key component (TTL, content hash, query, max tokens, prompt
version).

## Reviewer focus

Trace every caller of `GetCrawl` to its terminus and confirm the raw markdown
reaches only `refine`. Trace every caller of `GetCrawlFull` separately and
confirm RawHTML terminates in `internal/extract`-style structured parsing,
never a verbatim MCP response. Then check that no new `os.Getenv` appeared,
that migrations were added rather than edited, and that `make race` is
clean — the store is shared across the pipeline's crawl and refine fan-outs.

## Coding-run statuses

`IsTerminalStatus` is the single definition of "this run is over". The
websocket, the dispatcher and the memory ingest loop all ask it rather than each
keeping their own list — which is how the previous three-value set drifted into
three different opinions about what `running` meant.

`ReconcileRunningRuns` is called once, at daemon startup, before anything is
dispatched. A `running` row at that moment belongs to a daemon that is gone, and
leaving it alone is how the board came to show work that was not happening.
Queued rows are deliberately untouched: they are the durable queue and are meant
to survive exactly this.

`ClaimNextQueuedRun` is a compare-and-swap, not a transaction. The dispatcher
calls it from several goroutines as slots free up, and a read-then-write
transaction would have to upgrade a deferred lock, which SQLite answers with
`SQLITE_BUSY` rather than by waiting. The conditional `UPDATE` is the atomic
step; the loser sees zero rows affected and looks again.

`UpdateRunStatus` takes the state the caller believes the row is in. That guard
is what makes two clicks on the same card resolve to one winner without a lock.

`ParkRun` is the one transition that starts from `running` and ends in the
queue: the token budget ran out mid-task. It is deliberately not `RequeueRun`
(which starts from a terminal state, because a retry is about something that
finished) and not `UpdateRunStatus` (which cannot carry a session id). Writing
that session is the point: a run only learns it from the CLI's first line and
the row does not see it until the run ends, so a run parked mid-flight is
holding one the row has never been told — and it is what makes the attempt after
the reset a `--resume` of the same work rather than the task done twice. An
empty session id leaves the row's own alone.
