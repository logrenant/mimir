# internal/sessionlog — AGENTS.md

Turns the JSONL transcripts Claude Code and `internal/coderunner` leave behind
into `Episode` values: one prompt, and the work that answered it.

## What this package is

Pure parsing. **No network, no model, no store, no filesystem writes** — a
reader goes in, values come out. That is what lets `internal/memory` be tested
against fixtures instead of a live transcript tree, and it is why a transcript
format change can only ever cost episodes, never correctness anywhere else.

## Rules

- **Fail soft, always.** The Claude Code transcript format is Anthropic's
  private business and carries no compatibility promise. An unreadable line, an
  unknown record type, a field that changed shape — every one of them is
  skipped. A format drift must degrade this package to producing *fewer*
  episodes, never to producing wrong ones and never to failing an ingest. Same
  posture as `internal/coderunner/stream.go`'s `parseLine`.

- **Only a human prompt starts an episode.** A transcript's `user` records are
  mostly tool results; the rest include slash-command echoes, hook output and
  background task notifications. `humanPrompt` is the single gate. Wrapper
  echoes are matched **by shape** (`isWrapperEcho`), not against a hardcoded tag
  list — the list is not fixed, and the next wrapper Claude Code adds would
  otherwise silently become an episode titled with its own plumbing.

- **A `tool_use` block names itself with `id`; the `tool_result` refers back
  with `tool_use_id`.** Reading only one of the two loses every failure, and a
  failed step is the most reusable thing an episode records. `ccBlock` reads
  both. Covered by `TestParseClaudeCode_FailedToolCallIsMarked`.

- **Token counts dedupe by `message.id` and exclude cache reads.** One assistant
  message is written out once per content block, repeating its usage each time.
  Cache reads re-report the whole live context every turn. Counting either way
  naively inflated a single real episode past nine million tokens.

- **`Target` is never an argument body.** A `Bash` command line can carry a
  secret; its `description` cannot, and the description is what a later search
  would look for anyway. Same reasoning for every other tool.

- **Project-relative first, junk filter second.** `normalizeFile` relativizes a
  path inside the project *before* applying any filter, because a repository has
  every right to contain a `.claude` directory or live under a temp-looking
  prefix. Only paths from outside the project face the agent-plumbing filters.
  Getting this order wrong silently erases real work; there is a test for both
  halves.

- **`BuildFacts` output is byte-identical for identical input.** The recap is
  cached against the episode, so a map iteration leaking into the block would
  make every cached recap look stale. `Facts()` and `internal/memory`'s
  `episodeFacts` both go through it, so there is one definition of the format.

- **`Significance` runs before any model call.** It is the cost filter: a
  session is mostly short exchanges that changed nothing, and recapping those
  would spend the ingest budget on the least useful half of the transcript.

## Reviewer focus

Grep for `http`, `exec`, `os.Create`/`os.Write`, and any import of
`internal/store` or `internal/refine`. Any hit means this package stopped being
inert and the whole test strategy above it is weaker for it.

## Antigravity transcripts (task-45)

`antigravity.go` parses the `agy` CLI and Antigravity IDE transcripts:
`{step_index, source, type, status, created_at, content}`, one JSON object per
line, under
`~/.gemini/antigravity-{cli,ide}/brain/<conversation-id>/.system_generated/logs/`.

- **Do not parse the conversation database sitting next to them.** It is
  protobuf blobs with no published schema; reading it would be a reverse
  engineering exercise that breaks on their next release, and it holds nothing
  the transcript does not.
- **The transcript does not say which project it belongs to**, so this package
  never guesses. Attribution arrives from the agy `Stop` hook's payload
  (`workspacePaths`), through the spool `internal/memory` drains. Filing a
  session under the wrong repository is worse than not filing it.
- **Steps carry no tool name.** `read`, `write`, `command` and `search` are
  inferred from what a step says it did (`File Path:`, `Created file `,
  `The command exited with code N`). A step whose wording changes becomes
  `step`, which is the fail-soft this package already requires everywhere else.
- The prompt is fenced in `<USER_REQUEST>`; everything after it in a
  `USER_INPUT` record is machinery — local time, open editors, settings changes
  — that would otherwise become the episode's prompt.

## The two text ceilings live with the consumer (task-65)

`MaxPromptChars` and `MaxAssistantChars` are declared here and applied by
`internal/memory.toRow`, not by the parsers. That looks backwards until you
count the consumers: since task-65 the same parse feeds two tables with
opposite requirements — an episode row that must stay small enough to put in a
recap prompt, and a chat archive whose only job is to keep the text. A parser
that clipped would have forced the archive to read every transcript a second
time.

The other four bounds are accumulation caps and stay in the parsers: they limit
what a builder gathers while reading, and no consumer can recover what was
never gathered.
