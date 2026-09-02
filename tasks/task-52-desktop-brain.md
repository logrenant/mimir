# task-52 — Desktop: the Brain tab and the knowledge graph

- **Status:** done
- **Owner agent:** desktop
- **Prerequisites:** task-51 (the routes)
- **Primary paths:** `desktop/src/screens/Brain.tsx` (new), `desktop/src/lib/brainGraph.ts` (new), `desktop/src/lib/daemon.ts`, `desktop/src/screens/Dashboard.tsx`
- **Roadmap bucket:** B.9 extension

## Context

The daemon now keeps a scan running for as long as it lives, and the operator
asked to see it: what it is doing, the ability to stop it, and a picture of what
it has collected — a force-directed graph of the nodes and their edges.

## Scope (do exactly this)

1. **`lib/daemon.ts`** — one type per Go struct (`BrainScanStatus`,
   `BrainGraph*`, `BrainProject`, `BrainNodeDetail`) and the six calls. No Rust
   change: `daemon_request` is already a path-based proxy.
2. **`lib/brainGraph.ts`** — the layout, pure and tested: a seeded Fruchterman–
   Reingold with grid-approximated repulsion, `degrees`, `radiusFor`, `hitTest`,
   `kindStyle`, `pollInterval`. **No new npm dependency.**
3. **`screens/Brain.tsx`** — the scan panel (phase, counts, current project,
   pause / resume / scan-now, the daemon's error text verbatim), the canvas, a
   project filter, a kind legend, and a node panel with the assessment and
   clickable neighbours.
4. **`screens/Dashboard.tsx`** — `"brain"` in the `Screen` union and a fourth
   button in the Mimir group. Brain is not a module: it is the store the modules
   write into.
5. **Diagnostics** — `agy` and `pdftotext` in `DEP_NAMES`, agy first.

## Out of scope (do NOT do here)

- A graph library. Five dependencies is the whole front end and the layout is
  arithmetic this app can own and test.
- A hue per node kind. Four colours, no fifth (`desktop/AGENTS.md`): the kinds
  are separated by role and by weight instead.
- A websocket. The status is polled, adaptively — 2 s while scanning, 30 s while
  idle.
- Editing nodes. The tab reads.

## Definition of Done

- `make desktop-check` green.
- The layout is deterministic for a seed, keeps every node in the viewport, and
  ignores an edge whose endpoints are not both present.
- The project filter travels as an opaque id, never a path.

## Follow-up (same task, after the first look at it on screen)

Six hundred nodes laid out inside the panel was a ball of overlapping dots with
labels written on top of each other, and a border made of a regular lattice of
loose nodes — which is what a clamp against a small box produces.

- **The layout has a world, the panel is a window onto it.** `worldSize(count)`
  is `√count · 130`, clamped to 1 200–7 000, so the picture is pannable by
  construction. Measured on the live graph: 648 nodes, a 3 309 px world, a
  3 177 px span, laid out in 468 ms with **zero overlapping nodes**.
- **A separation pass** after the forces settle. The forces decide the
  structure; they do not promise two dots are not on the same spot, and two
  dots on the same spot are one dot to a reader.
- **Pan and zoom**: drag to move, wheel or pinch to zoom about the pointer,
  `−` / `+` / `sığdır`, and the current zoom shown as a percentage. A drag past
  four pixels is a pan, not a click, so panning never opens a node.
- **The opening view is readable, not complete.** Fitting this graph is scale
  0.25 and two-pixel dots; `READABLE_SCALE` (0.55) is the floor, and `sığdır`
  is there for the overview.
- **Labels are placed, not just drawn**: on-screen only, most-connected first,
  and skipped where the text box would land on one already placed.
- **Full screen** is an in-app overlay with the node panel beside it, Escape to
  leave — not the OS fullscreen, which would animate a new space and take the
  title bar with it.

## Follow-up: fluency, and the scan in Terminals

Watching somebody use it found three things.

- **A selection could not be undone.** Clicking the selected node again, or
  clicking the background, now clears it — a selection with no way out is a mode
  the operator did not ask to be in. The id is held separately from the fetched
  detail so the highlight lands on the click rather than on the response.
- **Scroll zoomed.** On this platform a two-finger scroll means *move the
  thing*; a canvas that zoomed instead fought the hand on every gesture. Scroll
  pans, pinch and ⌘/ctrl + wheel zoom, and the gesture hint is printed next to
  the zoom percentage.
- **The scan was not in Terminals.** It is now its own row above the run
  sessions — `BrainConsole`, not a `Session`: there is no run row, no transcript
  and no socket behind it, and teaching TerminalsProvider a second kind of thing
  would be worse than one screen owning this one. It tails
  `GET /brain/scan/log`, follows the bottom only while the reader is already
  there, and carries the same pause / resume / scan-now controls.

The layout was also tightened — a smaller world (`√count · 95`) and a stronger
pull to the centre for nodes with no edges, so the unlinked half of a knowledge
base stops drifting into a halo around the structure.

## Changelog

- `desktop/src/lib/daemon.ts` — brain types and `api.brain*`; `agy` and
  `pdftotext` in the diagnostics type.
- `desktop/src/lib/brainGraph.ts` + `brainGraph.test.ts` — the layout, the world,
  the view transform (`fitView`/`initialView`/`zoomAt`/`screenToWorld`), the
  separation pass and `visibleLabels`.
- `desktop/src/lib/daemon.test.ts` — the controls send no body; the filter is an id.
- `desktop/src/screens/Brain.tsx` — the tab.
- `desktop/src/screens/Dashboard.tsx` — the sidebar entry, the screen branch,
  `DEP_NAMES`.
- `desktop/src/components/BrainConsole.tsx` + its test — the scan as a terminal.
- `desktop/src/screens/Terminals.tsx` — the BRAIN row above the run sessions.
