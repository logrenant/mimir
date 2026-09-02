# task-36 — Desktop: working board, per-run terminals, image composer

- **Status:** done
- **Owner:** desktop
- **Prerequisites:** task-35
- **Primary paths:**
  - `desktop/src/lib/daemon.ts`, `board.ts`, `terminals.ts`, `attachments.ts`, `runStream.ts`
  - `desktop/src/components/Terminal.tsx`, `TaskComposer.tsx`
  - `desktop/src/screens/Terminals.tsx`, `Dashboard.tsx`, `Workspace.tsx`, `QuickTask.tsx`
  - `desktop/src/App.tsx`
  - `desktop/src-tauri/src/daemon.rs`, `desktop/src-tauri/tauri.conf.json`
- **Roadmap bucket:** B / M3.

## Context

The shell presented a task flow that the daemon never had, and hid its own
failures while doing it:

- `openRunStream`'s rejection was dropped on the floor (`void … .then(…)`), so a
  socket that never opened left the badge reading "running" over an empty panel.
- `socket.onerror` set a close reason that `socket.onclose` immediately erased,
  so the one message explaining the failure could never render.
- Nothing polled `GET /coding-tasks/{id}`; a dropped socket had no recovery.
- The board was read-only, its "new task" button appeared only while the list was
  empty, and a card could not be started, stopped, or watched.
- The connection dot, the "ready" pill and the per-module "bağlı" label were all
  hardcoded and reported health nobody had checked.
- There was no terminal and no Terminals screen.

## Scope

- `lib/board.ts`, `lib/terminals.ts`, `lib/attachments.ts` — pure logic with
  vitest coverage, per `desktop/AGENTS.md:82-89`.
- `runStream.ts`: keep the first close reason, poll `getCodingTask` as a fallback
  when the socket ends without a terminal event, handle `stderr` and
  `run.stopped`.
- A `Terminal` component and a `Terminals` screen; sessions held in a provider
  above `Dashboard` so sockets survive screen switches.
- A five-column board (Backlog · Queued · Running · Done · Failed) with create,
  drag between the two operator-owned columns, run, stop, delete and "open
  terminal".
- `TaskComposer` with paste / drag-drop / file-input image attachment.

## Out of scope

- Migrating the rest of `Dashboard.tsx`'s inline style engine to the design
  system.
- Manual ordering inside a column.
- xterm.js: the lines are synthesised from typed events, there is no ANSI to
  parse, and the existing palette and fonts are the house style.

## Definition of Done

`make desktop-check` green. Manually: a card created on the board reaches
Backlog, dragging it to Queued runs it, a terminal tab opens by itself, STOP ends
it, and killing the daemon mid-run drops the card into Failed rather than leaving
it "running".

## Notes for reviewer

- All daemon access is still `src/lib/daemon.ts` only.
- Drag-and-drop needs `"dragDropEnabled": false` on the main window: Tauri's
  native file-drop handler otherwise swallows the event and yields a path the
  WebView could not read anyway (there is no `fs` plugin, and none is added).
- Attachment previews are `data:` URIs built from base64 the daemon returns. The
  CSP is unchanged — it already allows `data:` images and does not allow
  `http://127.0.0.1` ones.
- The only Rust change is `DELETE` in the `daemon_request` method allowlist. The
  path guard is untouched, and no command, plugin or capability is added.
