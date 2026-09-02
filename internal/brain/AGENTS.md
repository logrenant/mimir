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
