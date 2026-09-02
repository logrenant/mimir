# task-38 — Desktop: account picker, account manager, Recents

- **Status:** done
- **Owner:** desktop
- **Prerequisites:** task-37
- **Primary paths:**
  - `desktop/src/components/AccountPicker.tsx` (new)
  - `desktop/src/lib/daemon.ts`, `desktop/src/lib/terminals.ts`
  - `desktop/src/screens/Workspace.tsx`, `Dashboard.tsx`, `Terminals.tsx`
- **Roadmap bucket:** B / M3.

## Context

task-37 gave the daemon credential slots; nothing in the shell could see them.
Separately, the Terminals screen only ever showed runs opened in the current
session — a finished run's transcript was reachable from the board's detail
overlay but not as a console, so "show me that job again" had no answer.

## Scope

- An account manager on the Coding runner screen: register by directory picker,
  a live `claude auth status` row per slot, forget a slot.
- An account select on both composers, "Otomatik" first and default.
- The account a card ran on, shown on the board — but only when more than one is
  registered, since a single-account board would just repeat itself.
- A collapsed **Recents** section in the Terminals sidebar listing past runs;
  opening one replays its transcript through the same socket a live run uses.

## Out of scope

- A login flow. Signing into a slot is a shell command and the manager prints
  it.
- Showing a credential, or anything derived from one beyond what
  `claude auth status` already reports.
- Per-account filtering or grouping on the board.

## Definition of Done

`make desktop-check` green, including `recentRuns` excluding what is already
open and what never started. Manually: two accounts registered, both showing
their identity; four automatic tasks running two at a time across both; a pinned
task waiting for its own account.

## Notes for reviewer

- All daemon access is still `src/lib/daemon.ts`.
- The probe is called live rather than cached: a slot whose login has lapsed
  must not read as healthy.
- Recents fetches on expand, not on a timer — it is an N+1 fan-out, because the
  daemon has no cross-project run route.
- **Follow-up (2026-09-02):** `lib/accounts.ts` was added after the operator
  pointed out their own `claude-a` / `claude-b` shell functions. Those set
  exactly the variable this feature sets, which confirmed the mechanism — and
  probing both on this machine showed both resolving to the same address, with
  two real keychain entries behind them. Registering a directory is not what
  splits an identity; signing into it is. `identityClashes` now says so in the
  manager, because nothing in the rows could.
