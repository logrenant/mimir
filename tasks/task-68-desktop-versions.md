# task-68 — Desktop: the version timeline

- **Status:** done
- **Owner agent:** desktop
- **Prerequisites:** task-67 (the routes)
- **Primary paths:** `desktop/src/screens/Brain.tsx`, `desktop/src/components/BrainConsole.tsx`, `desktop/src/lib/brainGraph.ts`, `desktop/src/lib/brainGraph.test.ts`
- **Roadmap bucket:** B.9 extension

## Context

task-67 gives every scanned file a history and the scan console a "this changed,
so it was re-read" event. Neither is visible. The node panel shows one
assessment — the current one — with nothing saying it ever had another.

## Scope (do exactly this)

1. `Brain.tsx`'s node panel gains a version timeline: date, short hash, size, and
   the title and assessment that version carried. Selecting one shows it in the
   detail area.
2. `BrainConsole.tsx` distinguishes the changed-file event from a plain scan
   line, the way it already colours by `ScanEvent.kind`.
3. Formatting helpers go in `lib/brainGraph.ts` with tests.

## Out of scope (do NOT do here)

- Any Go change.
- A diff view. There is no stored content to diff.

## Definition of Done

- [x] Selecting a file node with more than one version shows the timeline and an
      older assessment can be read.
- [x] `make desktop-check` green
- [x] Status set to `done` with changelog

## Notes for the reviewer (Opus)

`desktop/AGENTS.md`: daemon access only through `daemon.ts`, and no logic in JSX
that a test cannot reach.

## Changelog

- `desktop/src/lib/daemon.ts` — `BrainNodeVersion`, `versions` on the
  `brainNode` reply, `api.brainNodeVersions`, and `"changed"` added to the scan
  event's kinds.
- `desktop/src/lib/brainGraph.ts` — `versionLines` and `formatBytes`: the date
  format and the "which one is current" decision are both things a test can pin
  and neither is visible in a rendered panel until it is wrong.
- `desktop/src/screens/Brain.tsx` — `VersionTimeline` in the node panel, shown
  only when there is more than one reading; a row expands to the assessment that
  version carried.
- `desktop/src/components/BrainConsole.tsx` — a re-read is drawn apart from a
  first read.
- `desktop/src/lib/brainGraph.test.ts` — six cases.

**Decision:** history rides node detail rather than a second request. A node
with one version is the common case, and a per-node fetch would almost always
answer "nothing to show".

`make desktop-check` green.
