# AGENTS.md — internal/events

Typed run events and the in-process bus that fans them out. Introduced by
`tasks/task-19` for Track B / M2.

## The delivery contract — read this before changing anything

**Live delivery is best-effort. The on-disk JSONL transcript is the record.**

The publisher is the goroutine reading the `claude` subprocess's stdout. If a
slow watcher could block it, a stalled UI would stall the actual run. So
`Publish` never blocks: a subscriber whose buffer is full loses events.

That is only acceptable because two other things are true, and they must stay
true:

1. `internal/coderunner` writes **every** event to the run's JSONL transcript
   before/independently of publishing. Nothing is lost, only delayed viewing.
2. `Event.Seq` is monotonic per run, so a watcher can see it missed something
   and go read the transcript.

If you ever make the bus the only record, you have made dropped events into
lost data. Don't.

## Rules for this directory

- **The JSON tags are a wire format.** These events are written to disk,
  replayed by the daemon's HTTP API, and (M3) pushed over a WebSocket to a
  TypeScript client. Renaming a field or a `Kind` value is a breaking change
  for all three at once.
- **Deltas are suffixes, never accumulations.** `KindTextDelta` /
  `KindReasoningDelta` carry only the new text. A consumer appends; it never
  replaces. The producer is responsible for diffing per message id — see
  `internal/coderunner/AGENTS.md` for why that is per-message and not per-run.
- **Unknown tools are `RiskExec`.** Risk classification fails dangerous, not
  safe: a tool nobody has classified is assumed to be able to do anything.
- **No goroutines live here.** The bus is a mutex and some maps. If you find
  yourself adding a background goroutine, the lifecycle question ("who stops
  it?") has to be answered first — `Close` is currently a complete answer
  because there is nothing to stop.
- **Every channel close goes through `closeSubLocked`.** `cancel`, `CloseRun`,
  and `Close` can all race for the same subscription; the `closed` flag read
  and written under `b.mu` is the only thing preventing a double-close panic.
- **`OK` is a `*bool` on purpose.** For a tool result, "absent" and "false"
  mean different things, and `omitempty` on a plain bool would collapse them.

## Reviewer focus

SD-3: no blocking send without a `default` arm; no goroutine leak (tests run
`goleak.VerifyTestMain` plus a concurrent publish/subscribe/cancel/CloseRun
stress test under `-race`). SD-4: nothing here writes to stdout.
