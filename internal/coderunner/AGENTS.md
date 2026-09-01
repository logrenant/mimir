# AGENTS.md — internal/coderunner

Runs a `claude` coding session inside one registered project directory and
reports what it does as it happens. Introduced by `tasks/task-21` for
Track B / M2.

## Two `claude` profiles — do not conflate them

| | `internal/refine.Distil` | here |
|---|---|---|
| Purpose | context-isolation firewall over untrusted scraped text | do work in the operator's own repo |
| Tools | all force-denied (`--restricted`) | file tools enabled, scoped to one directory |
| Output | `--output-format json`, one blob at the end | `--output-format stream-json --verbose`, incremental |
| Input | untrusted page markdown | the operator's own prompt |
| Model | `cfg.ClaudeModel` (haiku) | `cfg.CodingModel` |

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
  `is_error` result beats a zero exit code.

## Lifecycle

`New` takes an explicit `base` context because a run must outlive the HTTP
request that started it — deriving from the request would kill the run when the
response is written. `Start` returns immediately; `Wait` joins every in-flight
run and is what lets tests satisfy `goleak` and the daemon shut down cleanly.

## Testing

Never spend tokens here. `writeFakeClaude` emits a canned stream, which covers
the runner end to end. Anything needing the real CLI belongs behind
`//go:build integration`.

Tests that assert on live bus delivery must make the fake stall before its
first line, or they race the run and pass vacuously.

## Reviewer focus

SD-3: the run goroutine outlives `Start` by design — confirm it is bounded by
both `base` and `CodingRunTimeout`, and that `Wait` actually joins it. SD-4:
the CLI's stdout is consumed here, never forwarded to ours. SD-6: "CLI missing
or not logged in" and "the run itself failed" are different errors with
different fixes. Security: `grep` for `cmd.Dir` and `--add-dir` and confirm
both trace back to `Resolve`.
