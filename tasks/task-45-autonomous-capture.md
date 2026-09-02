# task-45 — Brain records itself

- **Status:** done
- **Owner agent:** daemon
- **Prerequisites:** task-41, task-43
- **Primary paths:** `internal/brain/promote.go` (new), `internal/brain/gitlog.go` (new), `internal/sessionlog/antigravity.go` (new), `internal/store/migrations/0014_brain_capture.sql`, `internal/store/brain.go`, `internal/api/{api.go,brain.go}`, `internal/config/config.go`, `cmd/mimir-daemon/main.go`, `scripts/mimir-preflight.sh`, `scripts/install-mcp.sh`
- **Roadmap bucket:** B.9 extension (§7.8 in ARCHITECTURE)

## Context

Brain works, and it is entirely hand-fed: something is only remembered if a
model remembers to call `brain_ingest_data`, which is exactly the thing not to
rely on. Until the recording is derived rather than requested, every session
pays roughly 900 tokens for Mimir's tool definitions and gets back only what
somebody thought to write down.

Three sources are already on disk and cost nothing to read.

## Scope (do exactly this)

### 45.1 Claude Code episodes → nodes (`internal/brain/promote.go`)

`internal/memory` already distils `~/.claude/projects/**.jsonl` into
`memory_episodes` with a title and a summary. Promotion turns each *distilled*
episode into:

- one `kind="session"` node — `source_key` = the episode key, `title`/`assessment`
  = the episode's, body = its facts;
- one `kind="file"` node per path in `facts.files` — `source_key` = the
  repo-relative path, title = the path;
- a `provenance` edge from the session node to each file node.

**No model call.** Tags are derived deterministically: the top-level package or
directory of each touched path (`internal/brain` → `internal-brain`), file
extensions, and command names from `facts.commands`. That is real retrieval
vocabulary at zero cost, and it is what makes promotion safe to run over the
whole backlog. Aliases stay empty; a node that later wants them can be
re-distilled by bumping `BrainPromptVersion`.

File nodes accumulate edges: after a few sessions, `brain_related` on a file
node answers "what has happened to this file", which no other surface does.

### 45.2 agy sessions (`internal/sessionlog/antigravity.go` + a spool)

agy transcripts are clean JSONL —
`{step_index, source, type, status, created_at, content}` — under
`~/.gemini/antigravity-{cli,ide}/brain/<conversation-id>/.system_generated/logs/transcript.jsonl`.
The **conversation DB next to them is protobuf blobs; do not parse it.**

The transcript does not reliably say which project it belongs to, so the
attribution comes from the hook instead of being guessed:

- add a `Stop` handler to `~/.gemini/config/hooks.json` (installed by
  `scripts/install-mcp.sh`, same as the existing `PreInvocation` one) that
  writes its stdin payload verbatim to
  `~/Library/Application Support/mimir/spool/agy/<conversationId>.json`.
  The payload carries `workspacePaths` and `transcriptPath` — both documented.
- the daemon's ingest loop drains that spool, parses the transcript into
  `sessionlog.Episode` values, and stores them the way Claude Code episodes are
  stored, with `source_kind = "antigravity"`.

A spool file rather than an HTTP POST: the hook then needs no token, cannot
block a session on a network call, and a conversation that ended while the
daemon was down is still recorded when it comes back.

### 45.3 Commits (`internal/brain/gitlog.go`)

For each registered project, `git log` since the last seen SHA →
`kind="commit"` nodes: `source_key` = the SHA, title = the subject, body =
the message, tags from the touched directories. Deterministic, no model call.
This is what captures work done in a terminal without a shell hook — the part
that lasts, without recording every keystroke.

### 45.4 The loop and the state

Migration `0014_brain_capture.sql`:

- `brain_nodes.content_hash TEXT NOT NULL DEFAULT ''` — so a re-scan can skip a
  node whose source has not changed (task-47 depends on this).
- `brain_capture_state (source_key TEXT PRIMARY KEY, project_path TEXT, cursor TEXT, updated_at INTEGER)`
  — the last promoted episode key per project, the last git SHA per project, the
  drained spool files.

`cmd/mimir-daemon` extends the existing memory ingest tick: promote, drain the
spool, scan git. All three bounded per tick by config constants, all three
resumable, none of them fatal.

### 45.5 Routes — **dropped**

Planned as `GET /brain/search|nodes|related`, and not built. Nothing consumes
them: the desktop surface is deliberately out of scope here (the M8 precedent),
and `desktop/src/lib/modules.ts` states the rule this would have broken —
"there is no placeholder here, and adding one would be the fastest way to make
the shell lie about what the daemon does." The same argument applies to the
daemon's own route table. They belong to whichever task first has a screen that
needs them.

## Out of scope (do NOT do here)

- The whole-repository scan through agy — that is task-47, and it depends on
  `content_hash` from here.
- Any desktop screen.
- Recording raw shell history. Commits are the durable part; a keystroke log is
  a different decision with a different privacy answer, and it is not being made
  here.
- Parsing agy's protobuf conversation database.

## Definition of Done

- `make check` green.
- A distilled Claude Code episode appears as a `session` node with file nodes
  linked to it, with no model call made (assert the fake refiner/router records
  zero calls).
- Promotion is idempotent: running the tick twice does not duplicate nodes or
  edges.
- An agy `Stop` payload dropped into the spool is drained, parsed and stored,
  and the spool file is removed only after the episodes are committed.
- A commit already promoted is not promoted again after a daemon restart.
- With the store closed, every capture path degrades quietly (SD-6).

## Notes for the reviewer (Opus)

The promotion path must make **no model call at all**. If it grows one, running
it over an existing backlog becomes a bill rather than a migration, and the
"phase 1 is free and must always complete" rule that `internal/memory` holds
would be broken by its own consumer.

Watch the edge count. A file touched by fifty sessions gets fifty provenance
edges; `BrainNeighborCap` bounds what is *returned*, not what is stored, and
that is deliberate — but check that `brain_related` on such a node still fits
its budget.

## Changelog

- 2026-09-02 — Implemented and verified against the operator's real store. One
  tick captured 147 session nodes, 371 file nodes, 16 commits and 1015
  provenance edges across two projects, with **zero model calls**. The agy spool
  drained on the same tick: 6 Antigravity episodes alongside 234 Claude Code
  and 4 coding-run ones.
- Two bugs the tests found before a person did. Session tags were derived from
  `facts.commands`, which `internal/sessionlog` stores as prose descriptions —
  so the first word is a verb, and nearly every session was tagged `check`,
  `find`, `read`. And they were derived from *absolute* paths, which made the
  first two segments the machine's directory layout (`repo-internal`) instead
  of the package (`internal-store`). Tags now come from relative paths only,
  and commands are not a source at all.
- 45.5 was dropped rather than built; see above.
