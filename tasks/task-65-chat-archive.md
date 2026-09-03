# task-65 — The chat archive: conversations stored verbatim, terminals included

- **Status:** done
- **Owner agent:** daemon
- **Prerequisites:** task-45 (capture), M8 (the ingest loop)
- **Primary paths:** `internal/store/migrations/0017_chat_archive.sql`, `internal/store/chat.go`, `internal/store/chat_test.go`, `internal/memory/{memory,ingest}.go`, `internal/sessionlog/{sessionlog,claudecode,mimirrun,antigravity}.go`, `internal/api/chat.go`, `internal/api/api.go`
- **Roadmap bucket:** B.9 extension

## Context

`internal/sessionlog` already parses all three transcript sources — Claude Code
sessions under `~/.claude/projects`, this daemon's own coding runs (the
Terminals screen), and agy conversations from the spool — into `Episode` values
carrying `UserPrompt` and `AssistantText` in full. `memory_episodes` has neither
column: only the distilled `summary` and `facts_json` are kept. The verbatim
conversation exists solely in the source JSONL, which rotates, moves, and gets
deleted. The distillation is a memory of a conversation, not the conversation.

This task stores the turns themselves. It is deterministic work — no model call,
like `capture.go` — which is what makes running it over an existing backlog a
migration rather than a bill.

## Scope (do exactly this)

1. `0017_chat_archive.sql` — `chat_sessions`, `chat_turns` (keyed by the same
   `episode_key` the memory uses, so a growing session updates rather than
   duplicates), and `chat_fts`: external-content FTS5 over the two text columns
   with the `ai`/`ad`/`au` triggers, `'delete'` row before every re-insert.
2. `internal/store/chat.go`: `PutChatTurn` (upserting its session row in the
   same transaction), `ListChatSessions`, `ChatTurns`, `SearchChatTurns` —
   reusing the existing `ftsQuery` sanitiser, never aliasing `chat_fts`.
3. `internal/memory/ingest.go`: one archive write beside each `PutEpisode`
   call, through a nil-able `ChatArchive` seam. No new parser, no new discovery
   walk, no new cursor.
4. `internal/api/chat.go`: `GET /chat/sessions`, `GET /chat/sessions/{id}`,
   `GET /chat/search`. Projects are named by opaque id, never a raw path.
5. Move `MaxPromptChars` / `MaxAssistantChars` from the three parsers to
   `internal/memory.toRow`. Not optional and not scope creep: the parsers clip
   a prompt to 600 characters, so without this the "archive" would store a
   summary of a conversation and the feature would not exist. The four
   accumulation bounds stay in the parsers.

## Out of scope (do NOT do here)

- A `chat_search` MCP tool. It needs its own budget and choke-point work (SD-7).
- Retention, pruning or deletion.
- Any change to `memory_episodes` or the recap path.
- Anything under `desktop/`.

## Interfaces / contracts

```go
type ChatArchive interface {
    PutChatTurn(ctx context.Context, t store.ChatTurnRow) error
}
```

## Definition of Done

- [x] A Claude Code session and a Terminals run both appear in
      `GET /chat/search` with their prompts intact.
- [x] Re-reading a growing transcript updates turns rather than duplicating them.
- [x] A test asserts the archive path makes no model call.
- [x] Tests cover FTS reindex-on-update, the same way
      `TestSearchEpisodes_ReindexesOnUpdate` does.
- [x] `make check` green
- [x] Status set to `done` with changelog

## Notes for the reviewer (Opus)

The pinned FTS5 trap (never alias the virtual table), SD-6 (a nil archive leaves
ingest exactly as it is), and that nothing here reaches a model.

## Changelog

- `internal/store/migrations/0017_chat_archive.sql` (new) — `chat_sessions`,
  `chat_turns`, `chat_fts` with the external-content triggers.
- `internal/store/chat.go` / `chat_test.go` (new) — `PutChatTurn` (one
  transaction, session span and count recomputed rather than incremented),
  `ListChatSessions`, `ChatSession`, `ChatTurns`, `SearchChatTurns` over the
  existing `ftsQuery` sanitiser. The FTS table is never aliased.
- `internal/sessionlog` — the two text ceilings are no longer applied at parse;
  `Clip` is exported so the consumer can apply them. `antigravity.go` no longer
  stops accumulating assistant text at the ceiling.
- `internal/memory` — `ChatArchive` seam, `UseArchive`, `archiveTurn` beside
  both `PutEpisode` calls, and the clipping in `toRow`. Tests cover the seam,
  that the archive keeps what the row drops, and that it spends no model call.
- `internal/api/chat.go` (new) + `api.go` — `ChatArchiveReader` dep and the
  three read routes; project comes in as an opaque id.
- `internal/config/config.go` — five page bounds, validated; no env override.
- `cmd/mimir-daemon/main.go` — `mem.UseArchive(db)` and `Chat: db`.
- `internal/store/AGENTS.md`, `internal/sessionlog/AGENTS.md`,
  `internal/memory/AGENTS.md`.

**Decision:** the clipping moved rather than the archive getting its own parser.
A second parse path would have read every transcript twice and drifted from the
first; moving the ceiling to the one consumer that wants it costs four lines.

`make check` green.
