# task-54 — Desktop: discovered slots, and the background account

- **Status:** done
- **Owner agent:** desktop
- **Prerequisites:** task-53 (the routes and the two new fields)
- **Primary paths:** `desktop/src/lib/daemon.ts`, `desktop/src/components/AccountsProvider.tsx`, `desktop/src/components/AccountPicker.tsx`
- **Roadmap bucket:** B.2 extension

## Context

task-53 makes `~/.claude-accounts` the authority for which credential slots
exist. The app has to stop presenting those slots as things it owns: a
discovered row cannot be forgotten here, a slot created since the daemon
started should appear on refresh rather than on a restart, and the account the
daemon's own model calls spend is now a choice somebody has to be able to make.

## Scope (do exactly this)

1. **`lib/daemon.ts`** — `discovered` and `is_background` on `Account`;
   `api.scanAccounts()` and `api.setBackgroundAccount(id)`. Both POST: the Rust
   proxy allowlists GET, POST and DELETE.
2. **`AccountsProvider`** — `refresh()` scans before it lists. A failed scan is
   not a failed refresh: the list still loads, and `load()` reports whatever
   went wrong with that.
3. **`AccountManager`** — a discovered row shows "taramadan" where "unut" was;
   every row gets an "arka plan" toggle, marked when it is the background slot
   and clearing when clicked again. The footer says where slots come from and
   how to make one; the empty state says the scan came back empty rather than
   that nothing has been registered.

## Out of scope (do NOT do here)

- A second place to see accounts. The Dashboard's account load already reads
  the same provider.
- Any change to `AccountSelect`. "Otomatik" stays first and stays the default.
- Component-render tests. This front end tests pure functions and the daemon
  client; the row is neither.

## Definition of Done

- `make desktop-check` green.
- Clearing the background slot sends `{"account_id":""}` — the empty id is the
  answer "the CLI's own slot", not a missing field.

## Changelog

- `desktop/src/lib/daemon.ts` + `daemon.test.ts` — two fields, two calls, two
  tests.
- `desktop/src/components/AccountsProvider.tsx` — scan-then-load refresh.
- `desktop/src/components/AccountPicker.tsx` — the row controls and the copy.
