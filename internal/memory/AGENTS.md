# internal/memory — AGENTS.md

Long-lived per-project memory: what earlier sessions in a repository
established, kept so the next one is told instead of rediscovering it.

Joins three things that already exist — `internal/sessionlog` (transcripts →
episodes), `internal/refine` (episode → a couple of lines, via haiku), and
`internal/store` (keep and search). It opens no socket and spawns no process of
its own.

## The rule this package exists to enforce

**A model never rewrites an aggregate. Ever.**

The predecessor of this feature, in goat v1, asked a model to rewrite a whole
project-memory file on every pass. One rewrite degenerated — hundreds of
repetitions of one fragment, drift through half a dozen languages, loops of
self-correction — and because the file *was* the memory, that output became the
project's permanent memory. It is still on disk in that repo, still unreadable.
`internal/refine/testdata/degenerate_recap.txt` is a slice of it, kept as a
regression fixture.

Everything below follows from refusing to repeat that:

- **Scope is one episode.** A generation can spoil one row, never the memory.
- **Nothing is materialized.** `Brief` is assembled from rows at read time, on
  every call. There is no document to rot and none for a model to be pointed at.
- **Output is checked before it is believed.** `refine.validateRecap` plus
  `clampOutput`'s "must not exceed its input" rule. A rejected recap is not an
  error to handle away: the episode keeps its deterministic facts, stays
  searchable, and simply reads less well than its neighbours.
- **The repository always wins.** `RepoFacts` is re-read from disk every call
  and never cached, and `Guidance` travels with every response saying so.

## Rules

- **Phase 1 is free and must always complete.** Parsing and storing costs
  nothing but I/O, which is why search and the timeline work from the first pass
  — before a single model call. Phase 2 is bounded by `MemoryIngestBatch` so an
  interrupted backfill loses one batch, not a session.

- **`PutEpisode` must not clobber a recap.** Phase 1 re-derives the
  deterministic half of every episode it sees. If the upsert touched
  `title`/`summary`/`recap_attempts`, every pass would discard the model calls
  the last one paid for and the backfill would never converge. `UpdateRecap` is
  the only writer of those three columns.

- **A rejection consumes an attempt; an outage does not.** `refine.ErrRefineRejected`
  is this episode's fault and is recorded, so a hopeless episode stops costing a
  call on every future pass. `ErrClaudeUnavailable` is not — absorbing it would
  burn every episode's attempt budget and leave the memory permanently blank
  once the CLI came back. Both cases have tests.

- **An in-flight episode is stored but not recapped.** A transcript written to
  within `MemoryOpenEpisodeGrace` is a session running right now; its trailing
  episode is mid-task. It is stored with `significance = 0` so it is searchable
  but out of the recap queue, and the next pass over a cold file restores the
  real score in place.

- **Responses must fit their budget before they leave.** `internal/mcp`'s
  choke-point *rejects* an over-budget response rather than truncating it, so a
  brief that outgrew its ceiling would not arrive shortened — it would not
  arrive at all, and only on the projects with the most history. `fitToBudget`
  is not politeness. `estimateTokens` must keep matching `finalize.go`'s
  `len(json)/4`.

- **`scrub` runs on everything that reaches a response.** The choke-point fails
  closed on `<html`, `<script` and `data:` image URIs. One transcript from a
  scraping session, or one pinned note quoting markup, would otherwise make
  every future `project_context` call fail with an isolation violation — the
  memory going dark for a reason nothing in the error would explain. Both
  spellings matter: `encoding/json` escapes `<` to `<` and the choke-point
  looks for that form too.

- **This package never validates a path.** `Project` arrives already resolved by
  `internal/project`. See that package's `Canonicalize`, and note that the tools
  deliberately do *not* call `Register`: reading a project's history is not
  grounds for minting the registration a coding run needs.

- **`Brief` is deterministic.** Two identical calls produce identical JSON. A
  brief that shifted between calls is indistinguishable from an unstable memory,
  and a consumer would learn to distrust it. `hotFiles` breaks ties on the path
  for this reason.

## Reviewer focus

SD-2/SD-7 (every response fits and is gateable), SD-3 (`recapPending` is
`errgroup` + `SetLimit`, `Run` exits on ctx), SD-6 (every failure degrades),
SD-1 (every bound is a `config` constant, no knobs).

Grep for anything that writes a summary file, or that passes more than one
episode's facts to `refine.Recap`. Either one is the v1 failure coming back.

## The chat archive (task-65)

`UseArchive` installs a second, optional writer. The distinction it draws is the
whole point of the feature:

- **A row is an index entry; a turn is the record.** `toRow` clips the prompt
  and the assistant text, and `archiveTurn` stores them whole. The clipping
  moved here from `internal/sessionlog` when the archive arrived — see that
  package's `AGENTS.md` — because a parser that clipped would have made the
  archive impossible to write without reading every transcript twice.
- **One parse feeds both.** That is the only reason the archive is affordable.
  There is no second discovery walk, no second cursor: `memory_ingest_state`
  already says how far each transcript was read.
- **No model call.** Archiving is deterministic, like `internal/brain`'s
  capture loop, which is what makes running it over months of backlog a
  migration rather than a bill. `TestIngest_ArchiveSpendsNoModelCall` is what
  keeps that true.
- **A nil archive is the package as it was.** `mimir-mcp` gets one: a
  short-lived stdio process has no backlog to keep.

