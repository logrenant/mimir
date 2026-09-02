# AGENTS.md — internal/coderunner

Runs a `claude` coding session inside one registered project directory and
reports what it does as it happens. Introduced by `tasks/task-21` for
Track B / M2; given a full task lifecycle (backlog, queue, stop, attachments)
by `tasks/task-35`.

## Two `claude` profiles — do not conflate them

| | `internal/refine.Distil` | here |
|---|---|---|
| Purpose | context-isolation firewall over untrusted scraped text | do work in the operator's own repo |
| Tools | all force-denied (`--restricted`) | file tools enabled, scoped to one directory |
| Output | `--output-format json`, one blob at the end | `--output-format stream-json --verbose`, incremental |
| Input | untrusted page markdown | the operator's own prompt |
| Model | `cfg.ClaudeModel` (haiku) | the run's own, from `cfg.CodingModels` |

If you find yourself copying flags between the two packages, stop: their threat
models are opposite. The refiner must not be able to act; this runner exists in
order to act.

## Rules for this directory

- **A directory only ever comes from `ProjectResolver.Resolve`.** Never accept a
  path parameter, never join one from caller input, never fall back to the
  process working directory. `cmd.Dir` and `--add-dir` must always name the
  same resolved path — one without the other is a scoping hole.
- **`Resolve`, never `Get`.** Resolve re-runs every path guard against the
  filesystem as it is now. A row recorded weeks ago is not a permission.
- **A slot is marked busy before the goroutine starts, not inside it.**
  `launch` registers the `inflight` handle synchronously and then spawns;
  `busyAccounts` reads that map and `pump` keeps calling `dispatchOne` until no
  slot is free. Marking it from the child leaves a window in which the very
  next iteration sees the same account free and claims a second run against one
  identity — which is the entire invariant this package exists to hold. The
  dispatch mutex does not help: the window is between `launch` returning and
  the child being scheduled.
- **The model comes from the row, not from config.** `args` takes it as an
  argument because the choice was made when the task was written down: a card
  released from the backlog next week must run as the model the operator picked
  then, not as whatever the default has become. `cfg.CodingModel` is only the
  value `create` stamps on a request that named none, and the offer list is
  checked there so an unknown model is a 400 rather than a run that dies three
  seconds in.
- **The parser is pure.** `parseLine` does no I/O so the whole translation is
  testable without spending a token. Keep it that way: if you need the clock or
  the filesystem in there, pass the value in instead.
- **Deltas are tracked per `message.id`, not per run.** An `assistant` line
  carries one message's blocks and the same id recurs as it grows, so a delta
  is the suffix beyond what *that message* already emitted. goat v1 kept one
  counter for the whole run, which silently swallows the opening of every
  message after the first — and any session with tool calls has several.
  `TestParseLine_SecondMessageDeltasAreNotSwallowed` fails if you regress it.
- **A `tool_use` block is announced once per block id.** It reappears in later
  lines for the same message.
- **Unparseable lines are skipped, never fatal.** One malformed line must not
  end a run that is otherwise doing useful work.
- **The transcript is written before the bus is published.** The JSONL file is
  the complete record; bus delivery is explicitly allowed to drop
  (`internal/events/AGENTS.md`). Reversing that order would make a dropped
  event a lost event.
- **A run with no `result` line is a failure.** Exiting cleanly without
  reporting an outcome is not success, and reporting it as one would hide real
  breakage.
- **`execute` never returns an error.** By the time it runs there is no caller
  left; every failure becomes a `run.failed` event plus a `failed` row.
- **Terminal state comes from the CLI when it reported one.** Its own
  `is_error` result beats a zero exit code — unless the run was stopped, which
  is decided by the in-flight flag and not by the exit status. An interrupted
  CLI exits exactly like a crashed one; only that flag knows the difference, and
  reporting a cancellation as a failure sends the operator to fix something they
  chose.
- **This is the only thing that starts a run.** `pump` is a method here, not a
  scheduler beside it: callers write rows and ask (`Create`, `Enqueue`, `Stop`),
  and the decision that a subprocess may begin is made in one place. That is the
  single-engine rule from `docs/ROADMAP.md` §B.2.1 — the failure it prevents is
  goat v1's DAG/kanban split, where two things could dispatch the same work.
- **Capacity is one run per credential slot, and that is not a number.** Two
  runs sharing a Claude Code identity share its rate limit and its session
  state, so the second is contention rather than throughput. There is
  deliberately no `MaxConcurrentRuns` constant to raise: more capacity means
  registering another account (`internal/account`), which is a fact about the
  operator, not a setting.
- **The queue is walked, not popped.** A run pinned to a busy account is
  stepped over so one behind it that can run does. Popping the head would let
  one long task on one identity hold up every other identity's work.
- **`pump` is called, never looped.** On release, at startup, and by each run as
  it frees its slot. A permanent dispatcher goroutine would need a lifetime to
  manage and would outlive `Wait`; this owns nothing between calls.
- **A result is persisted under `context.WithoutCancel`.** The run's context is
  already cancelled by then, and during shutdown so is `base`. Deriving from
  `base` meant every run finishing as the daemon exited failed to write its row
  and stayed `running` forever — which is precisely what `Resume` now has to
  clean up. The error is logged, never discarded.
- **A stop signals the process group, not the process.** `claude` spawns
  children that inherit the stdout pipe; signalling the parent alone leaves them
  holding it open and the run sits there "stopping" until they finish on their
  own. `Setpgid` plus `kill(-pid)` is what makes stop mean stop.
- **The kill watchdog lives beside the read, not after it.** `cmd.WaitDelay`
  only escalates inside `Wait`, and `Wait` cannot be called until `consume` has
  drained the pipes — which a child ignoring SIGINT never allows. Hence the
  explicit goroutine.
- **stdout and stderr merge into one consumer.** `Event.Seq` is what stitches
  the transcript to the bus; a second goroutine stamping sequence numbers would
  make that ordering a race.
- **The child's environment is built, never inherited.** `account.Environ`
  chooses the credential slot and strips the launching Claude Code session's
  variables. Assigning `cmd.Env` from `os.Environ()` directly would hand a
  nested CLI somebody else's session id.
- **An attachment's extension comes from `http.DetectContentType`.** Never from
  the client's filename: the id plus that extension *is* a path, and a filename
  is the client's to choose.

## Lifecycle

`New` takes an explicit `base` context because a run must outlive the HTTP
request that started it — deriving from the request would kill the run when the
response is written. `Start` returns immediately; `Wait` joins every in-flight
run and is what lets tests satisfy `goleak` and the daemon shut down cleanly.

A task now has a life before and after that:

```
Create ─→ backlog ──Enqueue──→ queued ──pump/claim──→ running ─→ completed
             ↑                    │                      │     ├→ failed
             └────── Stop ────────┘                      └Stop─→ stopped
```

A run also carries two account fields, and they answer different questions:
`RequestedAccountID` is the slot the operator pinned (empty means "any free
one", which is the default), and `AccountID` is the slot the dispatcher actually
gave it. Collapsing them would lose the pin the moment a run started.

`Resume` is called once at daemon startup, before the API serves. It fails every
row still marked `running` — at startup this process owns no subprocess, so such
a row is the residue of a daemon that was killed — and then pumps whatever the
queue still holds. Queued rows are deliberately untouched by the reconcile:
surviving a restart is what makes the queue durable rather than a property of
one process's memory.

## Testing

Never spend tokens here. `writeFakeClaude` emits a canned stream, which covers
the runner end to end. Anything needing the real CLI belongs behind
`//go:build integration`.

Tests that assert on live bus delivery must make the fake stall before its
first line, or they race the run and pass vacuously.

`writeScriptedClaude` (in `lifecycle_test.go`) is for the three behaviours the
canned fake cannot express: stalling, writing to stderr, and trapping SIGINT.
A test about interrupting a live process must wait for the CLI's *first line*,
not merely for the row to say `running` — stopping before the spawn is a
different path.

`newHarnessWith` exists for the concurrency limit: a queue only forms when there
are fewer slots than runs.

## Reviewer focus

SD-3: the run goroutine outlives `Start` by design — confirm it is bounded by
both `base` and `CodingRunTimeout`, and that `Wait` actually joins it. SD-4:
the CLI's stdout is consumed here, never forwarded to ours. SD-6: "CLI missing
or not logged in" and "the run itself failed" are different errors with
different fixes. Security: `grep` for `cmd.Dir` and `--add-dir` and confirm
both trace back to `Resolve`.
