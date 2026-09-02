# task-40 — Home becomes a real dashboard, and the model picker

- **Status:** done
- **Owner:** desktop
- **Prerequisites:** task-36, task-38, task-39
- **Primary paths:**
  - `desktop/src/App.tsx`
  - `desktop/src/screens/Dashboard.tsx`, `desktop/src/screens/Home.tsx`
  - `desktop/src/components/RunsProvider.tsx`, `desktop/src/components/ModelPicker.tsx`
  - `desktop/src/lib/dashboard.ts`, `desktop/src/lib/daemon.ts`,
    `desktop/src/lib/terminals.ts`

## Context

The home screen was a brochure: a heading, a sentence, and two cards linking to
the modules. It rendered nothing the daemon knew and refreshed nothing — the
one screen the app opens on was the one screen with no live state. The answer
to "is anything running?" was two clicks away on the board, and the answer to
"why did that fetch fail?" was buried in a diagnostics overlay nobody opens.

That last one is not hypothetical. Crawl4AI was down on this machine for the
whole session; `/diagnostics` had been saying so, in a message that even names
the fix (`run \`make crawl-up\``), and the shell showed it nowhere.

Two pollers were also about to exist: the board fans out one request per
registered project, and a home screen that shows runs would have done the same
thing again on its own timer.

## Scope

- `RunsProvider`: one poll loop for the whole app, mounted inside
  `TerminalsProvider` so it can hand each refresh to both consumers. The board
  and the dashboard read the same array.
- Auto-adopt: a `running` run opens a terminal session by itself. This is the
  other half of "every running job gets a terminal" — before, that only held
  for jobs the operator had clicked.
- A closed tab stays closed. The provider remembers dismissals, so adopt cannot
  reopen what the operator just shut, and forgets them when the run ends.
- `screens/Home.tsx`: the dashboard. A stat strip, live mini-terminals for the
  running sessions, the queue, recent finishes, dependency health with the
  daemon's own remedy line, account load, and a `+ Yeni task` that opens the
  same composer the board uses.
- `ModelPicker`: `useModels()` over `GET /coding-models`, and a `ModelSelect`
  used by the composer, the Workspace runner and the menu-bar quick task. The
  model shows on the card and in the run detail.
- `lib/dashboard.ts`: `summarize`, `dependencyRows`, `accountLoad` — pure and
  tested, per `desktop/AGENTS.md`.
- `lib/terminals.ts`: `runsToAdopt`, `tailLines` — same rule.

## Out of scope

- Retiring the rest of `Dashboard.tsx`'s inline-style engine. The new screen
  uses the shared palette; the board and overlays keep their `css()` strings
  until that is its own task.
- A cross-project runs route. The fan-out stays, it is just done once.
- Charts. A sparkline of spend over time is a nice thing to want and needs a
  history route that does not exist.
- Starting Docker or the sidecars from the app. The dashboard reports the
  dependency and quotes the command; running it is the operator's.

## Interfaces

```ts
// lib/dashboard.ts
export function summarize(runs: Run[] | null): Summary;          // counts + today's spend
export function dependencyRows(d: Diagnostics | null): DepRow[]; // name, ok, optional, detail
export function accountLoad(accounts: Account[], runs: Run[]): AccountLoad[];

// lib/terminals.ts
export function runsToAdopt(runs: Run[], open: SessionMap, dismissed: Set<string>): Run[];
export function tailLines(session: Session, n: number): TerminalLine[];

// components/RunsProvider.tsx
useRuns(): { runs: BoardRun[] | null; error: string | null; loading: boolean; refresh: () => void }
```

## Definition of Done

- `make desktop-check` green.
- Home shows live output for every running job without the operator opening
  anything.
- Crawl4AI being down is visible on the first screen, with the command to fix
  it.
- One poll loop: the board and home together issue one round of
  `GET /coding-tasks` per interval, not two.
- Closing a terminal on a still-running job does not reopen it.
- A task can be created with an explicit model, and the card shows which.

## Notes for reviewer

`runsToAdopt` deliberately takes only `running`. Adopting `queued` would open a
console per backlog release and fill the screen with panels that have nothing
to print; the queue is a list on this screen, not a terminal.

The mini-terminal is a tail, not a second Terminal component: it renders the
last lines of the same `Session` the Terminals screen draws in full, so there
is one session store and one socket per run either way.

`dismissed` is cleared when a run reaches a terminal status, so a job the
operator dismissed while it ran does not stay suppressed for the next job — the
set is about this run, not this run id forever.
