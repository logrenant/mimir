# task-35 — Coding-task lifecycle: queue, stop, attachments, stderr

- **Status:** done
- **Owner:** daemon
- **Prerequisites:** none
- **Primary paths:**
  - `internal/config/config.go`
  - `internal/events/event.go`
  - `internal/store/runs.go`, `internal/store/migrations/0011_coding_task_lifecycle.sql`
  - `internal/coderunner/runner.go`, `internal/coderunner/attachments.go`, `internal/coderunner/stream.go`
  - `internal/api/api.go`, `internal/api/handlers.go`, `internal/api/middleware.go`, `internal/api/ws.go`
  - `cmd/mimir-daemon/main.go`
- **Roadmap bucket:** B / M3 (live run streaming) — repairs, not a new milestone.

## Context

`POST /coding-tasks` was `insert(status='running') + go execute(...)`: a coding
task had no life before it ran and no handle once it did. Five consequences,
each reported by the operator:

1. A run interrupted by a daemon restart stays `running` forever. Nothing sweeps
   the table — `UpdateRunResult` was the only `UPDATE coding_runs` in the
   codebase — and `record()` persisted under `r.base`, the daemon root context,
   which is already cancelled during shutdown; the returned error was discarded.
   The board then shows work that is not happening.
2. There is no way to create a task without running it, so a board can only ever
   be a read-only log.
3. There is no way to stop a run. The only cancellation was the 30-minute
   timeout.
4. A prompt is a bare string on stdin, so an image cannot be attached.
5. The CLI's stderr — where "not logged in" is reported — was buffered into a
   string and surfaced only inside a failure message, so a run that could never
   work looked identical to one that was thinking.

### Why the queue is not a second orchestrator

`docs/ROADMAP.md:462-465` puts goat v1's DAG/kanban orchestration split on the
leave-behind list. The queue added here is not one: `internal/coderunner` remains
the only thing that spawns a `claude` process, and the dispatcher (`pump`) is a
method on the same `Runner` that already owned `execute`. Callers — HTTP, board,
quick task — only write rows and call `Enqueue`/`Stop`. Nothing outside this
package decides that a run may begin. The `docs/ROADMAP.md:108-110` deferral is
about an external broker (asynq/NATS); this is an in-process semaphore.

## Scope

- Statuses `backlog`, `queued`, `stopped` beside the existing three, with
  `store.IsTerminalStatus` as the single definition of "over".
- Migration `0011` adding `title`, `created_at`, `queued_at`, `attachments`.
- `Runner.Create` / `Enqueue` / `Stop` / `Delete` / `Resume`, an atomic
  `ClaimNextQueuedRun`, and a `CodingMaxConcurrentRuns` semaphore.
- Restart reconciliation of orphaned `running` rows.
- `context.WithoutCancel` for the result write, with the error logged.
- Live `stderr` events and a `run.stopped` terminal event.
- Image attachments: upload/fetch routes, on-disk store beside the transcripts,
  path injection into the prompt.
- The nil-`Runner` panic on `/coding-tasks`, and `Transcripts` missing from the
  `/ws/runs/{id}` route guard.

## Out of scope

- Interactive PTY, or any write path on the run socket. It stays one-directional
  (`internal/api/AGENTS.md:57-60`); stop is a separate route.
- An external job broker.
- `--resume`, retry, `parent_run_id`.
- `--mcp-config` for the coding agent (`docs/ROADMAP.md:236-242`).
- A cross-project run listing route.
- Non-image attachments.

## Interfaces

```go
// internal/coderunner
func (r *Runner) Create(ctx context.Context, req CreateRequest) (Run, error)
func (r *Runner) Enqueue(ctx context.Context, runID string) (Run, error)
func (r *Runner) Start(ctx context.Context, req CreateRequest) (Run, error)
func (r *Runner) Stop(ctx context.Context, runID string) (Run, error)
func (r *Runner) Delete(ctx context.Context, runID string) error
func (r *Runner) Resume(ctx context.Context) error
func (r *Runner) SaveAttachment(filename string, data []byte) (Attachment, error)
func (r *Runner) LoadAttachment(id string) (Attachment, []byte, error)

// internal/store
func (s *Store) ClaimNextQueuedRun(ctx context.Context, at time.Time) (RunRow, bool, error)
func (s *Store) UpdateRunStatus(ctx context.Context, id, status string, ...) error
func (s *Store) ReconcileRunningRuns(ctx context.Context, reason string, at time.Time) (int, error)
func (s *Store) DeleteRun(ctx context.Context, id string) error
func IsTerminalStatus(status string) bool
```

Routes added to the one mux:

```
POST   /coding-tasks/attachments
GET    /coding-tasks/attachments/{id}
POST   /coding-tasks/{id}/enqueue
POST   /coding-tasks/{id}/stop
DELETE /coding-tasks/{id}
```

`POST /coding-tasks` grows `title`, `attachment_ids` and `start` (default true),
so an existing client is unaffected.

## Definition of Done

`make check` green, including: reconciliation leaves `queued` rows alone;
migration 0011 idempotent; `Create` spawns nothing; the semaphore is not
exceeded; a stopped run ends `stopped` and publishes `run.stopped`; a queued run
stopped returns to `backlog`; attachment sniffing rejects a non-image; stderr
events keep the sequence monotonic; the result row is written after base is
cancelled; goleak clean.

## Notes for reviewer

- No new environment variable and no new user-facing setting (SD-1). The five
  new values are constants in `internal/config`.
- `limitBody` now takes a per-route table. The default is unchanged at 1 MiB;
  only `POST /coding-tasks/attachments` is larger, and its ceiling is a constant.
- The attachment directory is derived from `StorePath`, in the same place and the
  same way as `TranscriptDir`.
- An attachment's extension comes from `http.DetectContentType`, never from the
  client-supplied filename.
