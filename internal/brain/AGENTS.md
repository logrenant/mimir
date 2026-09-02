# internal/brain — AGENTS.md

The node core: what we know about a *thing* — a repository, a decision, a piece
of research, a file — and how it connects to everything else we know.

`internal/memory` answers "what happened in this checkout". Brain answers "what
do we know about this", across projects and across every model that writes into
it. Both live in the same store, and Brain is the derived half: nothing here is
a second source of truth for what memory already records.

## The rule this package exists to enforce

**No ingest ever rewrites another node.**

The first version of this layer kept one Markdown file per node and had the
clustering engine rewrite each neighbour's file on every ingest, swallowing the
errors. That is the shape `docs/ROADMAP.md` §B.9 exists to forbid — see
`internal/memory/AGENTS.md` for what it cost in goat v1.

Everything below follows from refusing to repeat it:

- **A node is a row**, keyed by `(project_path, kind, source_key)`. The previous
  id was salted with `UnixNano`, so ingesting one repository twice produced two
  unrelated nodes that then linked to each other.
- **Edges are stored once, in one direction**, and read with `src = ? OR dst = ?`.
  Storing both directions is what forced the neighbour rewrite, and the
  lost-edge race lived in the middle of it.
- **A failed distil costs the assessment, never the node.** `assessment = ''` is
  a normal state: the node is still findable by title, tags and body, and still
  linkable. The old code returned early when the summarizer failed and stored
  nothing at all.
- **Scope is one node.** Never an aggregate, for the reason `refine.Recap`
  spells out at length.

## Rules

- **Write the node before linking it.** `Ingest` upserts, *then* relates. A
  failure in the relation pass leaves a stored, searchable node with fewer
  edges. The first version's commit message claimed this invariant and the code
  did not hold it.

- **Ingest costs one FTS query and at most two model calls.** The candidate set
  comes from one `SearchBrainNodes`; tag edges are computed locally and free;
  exactly one relation pass decides the rest, and it sees only titles, kinds and
  tags — never a body — so it stays small however large the nodes are. The old
  `LinkNode` was O(N) reads *and* O(N) writes per ingest.

- **A semantic verdict replaces a tag edge on the same pair unconditionally**,
  not on weight. The two numbers are not on one scale: a Jaccard of 1.0 only
  means two nodes chose the same words, and comparing them lets the cheap
  approximation outvote the judgement it approximates.

- **Ids the model returns are checked against the candidate list.** An invented
  id creates an edge to a node that does not exist, which the neighbour resolver
  then silently drops forever.

- **`sanitizeBody` runs on everything stored.** `internal/mcp`'s choke-point
  fails closed on `<html`, `<script` and `data:` image URIs. One node ingested
  from a scraped page would otherwise make every future brain call fail with an
  isolation violation, for a reason nothing in the error would explain.

- **A zero-tag distil is rejected outright.** A node with no tags is invisible to
  tag linking and carries no vocabulary into the index. That was the silent
  failure mode of the old `SUMMARY:`/`TAGS:` line parser, which stored the raw
  response as the assessment whenever it found neither prefix.

- **Aliases are what stand in for a vector index.** The distil writes each node's
  synonyms, expansions and adjacent terms into `aliases_text`, so bm25 matches a
  query whose words appear nowhere in the node. `ROADMAP` §A.4 keeps local
  embeddings parked and this layer did not need to unpark them. If recall gets
  worse, the lever is `BrainPromptVersion` — bump it and the nodes re-distil.

- **The assessment is Turkish and the tags are English.** The assessment is read
  by a person scanning a result list in this repository's own language; the tags
  are a retrieval vocabulary that has to line up with identifiers, file names and
  the English text of everything else in the index.

- **This package never validates a path.** `Input.ProjectPath` arrives already
  resolved, by the same `canonicalProject` the memory tools use.

- **Responses must fit their budget before they leave.** `internal/mcp`'s
  choke-point *rejects* an over-budget response rather than truncating it, so a
  result set that outgrew its ceiling would not arrive shortened — it would not
  arrive at all, and only on the queries that matched the most. Eight nodes with
  real assessments and their neighbours come to roughly three times the ceiling,
  so the default limit alone is enough to trigger it; `fitToBudget` is not
  politeness. `estimateTokens` must keep matching `finalize.go`'s `len(json)/4`.

- **The body never leaves the package.** `NodeView` omits it deliberately: a
  consumer that wanted the body is better served reading the source the node
  names. There is a test for this.

## Capture: what records itself (task-45)

Three sources feed Brain without anybody asking. All three are read-only over
things already on disk, and **none of them calls a model** — that property is
what makes running them over an existing backlog a migration rather than a
bill, and it is worth re-checking every time `promote.go`, `gitlog.go` or
`capture.go` is touched. `capture_test.go` builds its `Core` with a nil
Completer for exactly this reason: a model call added there panics rather than
quietly starting to spend quota.

- **Promotion has no cursor, on purpose.** A recap arrives *after* its episode
  row is stored, so a bookmark that had moved past an episode would never come
  back for it. Instead a bounded recent window is re-read every pass and
  upserted; node identity is derived from the episode key, so this updates rows
  rather than adding them.
- **Tags come from relative paths only.** Two things were wrong here and both
  reached the real store before a test caught them. `facts.commands` holds
  prose descriptions ("Run full make check"), so its first word is a verb —
  tagging on it put `check`, `find` and `read` on nearly every session, terms
  that match everything and link everything to everything. And the paths in an
  episode are absolute, so deriving from them made the first two segments the
  machine's directory layout (`repo-internal`) instead of the package
  (`internal-store`).
- **Promotion never downgrades a file node.** A scan gives one a real
  assessment; a later session touching that file must not overwrite it with a
  bare path. There is a test.
- **A commit does not mint file nodes.** History is full of paths that no longer
  exist, and every one would become a node nothing ever looks at. It links only
  to file nodes that already exist.
- **The git cursor is a stop condition, not a range.** `since..HEAD` fails when
  the cursor's commit is gone — a rebase, a reset, a branch that went away — and
  the fix would be a special case for every way history can be rewritten.
  Reading a bounded window and stopping at the known SHA degrades to
  re-capturing the window, which is free because the upsert is idempotent.
- **The agy spool is a directory, not an endpoint.** The Stop hook writes its
  payload to `<store dir>/spool/agy/`; the daemon drains it. The hook then needs
  no token, cannot block a session on the network, and a conversation that ended
  while the daemon was down is still recorded. The spool file is removed only
  after the ingest state is written, so a crash re-reads rather than loses.

## The repository scan (task-47)

`scan.go` is the one deliberately expensive thing in this package: a distil per
file, paid once per repository and amortised over every later session that would
otherwise have opened the file to find out what it is.

- **It reuses `Core.Ingest`.** The only new logic is which files and whether to
  skip. A second ingest path would drift from the first, and the choke-point,
  the validator and the linker have to be the same ones everything else uses.
- **`content_hash` is what makes it affordable.** A file whose hash and prompt
  version match is skipped, so a second scan of an unchanged repository makes no
  model call and an interrupted scan resumes. If that regresses, the feature
  becomes one people run once and then avoid.
- **Batches, not a job.** A full pass here is about fifty minutes, which no
  synchronous call survives. The tool distils a bounded batch and reports
  `remaining`; the caller keeps going. This is why there is no async job, no job
  id and no progress stream.
- **`git ls-files` is the allowlist**, not a convenience: it already encodes the
  judgement somebody made about what belongs to the project.
- **The exclusions are not about size.** A lock file, a golden fixture and a
  minified bundle are all perfectly readable and all worth nothing to a later
  session, and every one costs the same as a file that matters.
- **`Store` and `Completer` implementations must be concurrency-safe**, because
  this runs several ingests at once. Both interfaces say so; `make race` is what
  enforces it.

## Known asymmetry

A global node (`project_path = ''`, e.g. a repository) searches only global
nodes for its candidates, so ingesting the repository *after* a project node
will not discover that pair — ingesting it before will, and the edge is
undirected once written. Widening the global candidate search would put every
project's node titles into one relation prompt, which is a bigger decision than
this asymmetry is a problem.

## Reviewer focus

SD-2/SD-7 (all four responses are gateable and budgeted — see
`tools/chokepoint_test.go`), SD-6 (every failure degrades: no store, no provider,
no GitHub), SD-1 (every bound is a `config` constant; the GitHub token is read in
`config.Load` and nowhere else).

Grep for anything that writes a node file, or that passes more than one node to
the distil. Either one is the shape this rewrite removed.


## The resident scan (task-51)

`supervisor.go` is the scan with the session taken out of it: the daemon starts
one `Supervisor`, it sweeps `cfg.BrainScanRoots` for as long as the process
lives, and there is always an agy working.

- **It is a type, not a method on `Core`.** It owns mutable runtime state —
  paused, in flight, how far — and `Core` is shared with `cmd/mimir-mcp`, a
  binary that has no lifetime to own.
- **The loop condition is a pass's own `remaining`**, never a count taken
  before the first pass, for the reason `cmd/mimir-scan/AGENTS.md` gives.
- **A pass that distilled nothing while files failed is checked *before*
  `remaining`.** A batch where every file failed reports nothing left to do,
  because the failures were counted rather than left pending; reading that as
  "this project is finished" is exactly how a signed-out agy would look like a
  completed sweep.
- **Backoff is not politeness.** One distil is about eleven seconds and a pass
  is twelve of them, so retrying a dead provider immediately is on the order of
  a thousand failed subprocesses an hour. It doubles from `BackoffMin` to
  `BackoffMax`, and the probe — `agy models`, one subprocess — ends it early
  when the provider comes back.
- **Pause reaches the pass in flight.** A pass is a minute of subprocesses; a
  pause that waited for it would look broken to the person who pressed it.
  Files already distilled keep their hashes, so resuming costs nothing.
- **`Unreadable` is not `Failed`.** A PDF with no text layer, or a machine with
  no poppler, must never look like the provider being down — `Failed` is what
  drives the backoff. There is a test.
- **The durable state is a display hint.** The summary in `brain_capture_state`
  under `scan:supervisor` exists so the tab does not report a fresh machine
  after every restart. The real progress is `content_hash` on `brain_nodes`, and
  a second table that can disagree with the nodes is worse than a hint that can
  be thrown away — which is why this shipped with no migration.

**Known asymmetry, recorded rather than fixed:** `brain_scan_repo` and the
supervisor can scan the same project at the same time, which is up to six
concurrent agy processes and a duplicated distil. It is idempotent — `Ingest`
upserts by `NodeID` — so it wastes quota and never corrupts. A global semaphore
would be the fix if it ever costs anything measurable.

## PDFs (task-51)

`pdf.go` reads a PDF through poppler's `pdftotext`, which is why `~/Documents`
is worth scanning at all: on the machine this was built for it is six technical
manuals and nothing else.

- **Optional by construction.** No poppler means PDFs are skipped and every
  other file is still read. `diagnostics` reports it the way it reports the Maps
  sidecar — `optional: true` — so a deliberately-poppler-less machine does not
  read as broken.
- **The two absences are different errors.** `ErrNoPDFExtractor` is fixed by
  installing poppler; `ErrNoTextLayer` is a scan of paper and cannot be fixed at
  all. Reporting them as one thing sends an operator to install what they have.
  `-nopgbrk` is what makes the second detectable: without it a scanned manual
  extracts to a page of form feeds instead of nothing.
- **The hash is of the file, never of the extracted text**, and it is taken
  before the extractor runs. That is what makes a second sweep over
  `~/Documents` spawn neither pdftotext nor agy.
- **The subprocess is the untrusted-input boundary.** A timeout, an output
  ceiling, `cmd.Env = []string{}`, and a scratch working directory rather than a
  repository — the same posture `internal/llm` gives agy, for the same reason.
