# task-67 — File version history: what a file used to mean

- **Status:** done
- **Owner agent:** daemon
- **Prerequisites:** task-47 (the hash skip), task-51 (the resident scan)
- **Primary paths:** `internal/store/migrations/0018_brain_node_versions.sql`, `internal/store/brain.go`, `internal/store/brain_test.go`, `internal/brain/scan.go`, `internal/brain/brain.go`, `internal/brain/supervisor.go`, `internal/api/brain.go`, `internal/config/config.go`
- **Roadmap bucket:** B.9 extension

## Context

Detection already works: `scan.go` hashes a file's raw bytes and skips it when
the hash matches the stored `content_hash`, and the `Supervisor` sweeps
`~/development` and `~/Documents` every `BrainScanIdleInterval`, so a file the
operator edits is re-read and re-distilled on the next pass. What is missing is
the history. `PutBrainNode` upserts, so each new assessment overwrites the last,
and "what did this file mean last week" has no answer.

Two smaller things are missing with it: a scan cannot tell a *changed* file from
a *new* one — both land in `Scanned` — so the console never says a re-read
happened, which is the one visible sign the detection is working at all.

## Scope (do exactly this)

1. `0018_brain_node_versions.sql` — `brain_node_versions`, append-only, one row
   per distinct content hash a node has had: hash, `seen_at`, size, file mtime,
   and the title/assessment/tags/provider/model that hash produced. No file
   content is stored. Index by `(node_id, seen_at DESC)`.
2. `store.PutBrainNode` becomes one transaction: read the previous row, upsert,
   and append a version when the content hash changed or the node has no
   versions yet. Written here rather than in a caller because the old hash is
   only visible here, and because every ingest path then gets history without a
   second write path existing. Bounded by `config.BrainVersionsPerNode`; the
   oldest are pruned on insert.
3. `internal/brain`: carry the file's size and mtime from `scan.go`'s existing
   `os.Stat` through `Input` into the row. Add `ScanResult.Changed`, counting
   candidates whose hash was known and different.
4. `internal/brain/supervisor.go`: emit a scan event when a pass re-read changed
   files, so `GET /brain/scan/log` and the console show it.
5. `internal/api/brain.go`: a bounded `versions` array on `GET /brain/nodes/{id}`
   and the full list at `GET /brain/nodes/{id}/versions`.

## Out of scope (do NOT do here)

- Storing file contents or diffs.
- A file watcher. The sweep interval is the detection latency and it stays.
- Changing `BrainPromptVersion` or any cache-invalidation lever.
- Anything under `desktop/` — that is task-68.

## Definition of Done

- [x] Scanning a file, editing it, and scanning again leaves two version rows;
      scanning again unchanged leaves two.
- [x] A failed distil still blanks `content_hash` and writes no version row.
- [x] `BrainVersionsPerNode` is enforced with a test.
- [x] `make check` green
- [x] Status set to `done` with changelog

## Notes for the reviewer (Opus)

SD-1 (the bound is a constant), and `internal/brain/AGENTS.md`'s rule that there
is one ingest path — check that no second place writes a version row.

## Changelog

- `internal/store/migrations/0018_brain_node_versions.sql` (new) — append-only,
  one row per distinct content hash, no file content.
- `internal/store/brain.go` — `UpsertBrainNode` is one transaction that reads
  the previous hash, upserts, and appends a version when it moved; the prune
  runs in the same transaction. `BrainNodeVersions` reads the history.
  `BrainNodeRow` gains `SizeBytes` / `ModifiedAt`.
- `internal/store/store.go` — the bound is read once at `Open` rather than
  threaded through four packages that have no opinion about it, which also kept
  `UpsertBrainNode`'s signature.
- `internal/brain` — `Input.SizeBytes` / `ModifiedAt` from the scan's existing
  `os.Stat`; `ScanResult.Changed` / `ChangedFiles`; `EventChanged` and a pass
  line that says how many of the distilled files had moved.
- `internal/api/brain.go` / `api.go` — bounded `versions` on
  `GET /brain/nodes/{id}` and the full list at `GET /brain/nodes/{id}/versions`.
- `internal/config/config.go` — `BrainVersionsPerNode` (50),
  `BrainNodeVersionsInline` (10), validated; no env override.
- `internal/store/AGENTS.md`, `internal/brain/AGENTS.md`.

**Decision:** the version is written by the store's upsert rather than by the
scanner. The old hash is only visible there, and every ingest path then gets
history without a second writer existing.

`make check` green.
