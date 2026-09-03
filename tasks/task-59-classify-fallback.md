# task-59 — Classification survives a signed-out distil tier

- **Status:** done
- **Owner agent:** daemon
- **Prerequisites:** task-55, task-57
- **Primary paths:** `internal/refine/classify.go`, `internal/refine/classify_test.go`, `internal/refine/AGENTS.md`
- **Roadmap bucket:** B.4 extension

## Context

On the keyless path every company arrives without Google's `types[]`, so
`internal/leadgen`'s rule tier cannot classify any of them and the whole batch
falls to the model. That model is the distil tier, which since task-51 is
agy-only with no fallback — so on a machine where agy is signed out, *every*
company is `unknown`. The categorized view and the per-category workbook both
become one bucket called "we don't know", which is the one outcome that makes
them worthless.

task-51's reasoning still holds for what it was about: a machine-wide scan
silently moving to a billed provider is a bill nobody chose. Classification is
the opposite shape of work — a batch of twenty companies, a few hundred tokens,
inside a run an operator started and is watching.

## Scope (do exactly this)

1. `runClassify`: try the distil class; on `ErrClaudeUnavailable` /
   `ErrProviderUnavailable` only, retry once on the reason class (claude haiku).
   A cancelled caller never falls back. A provider that *answered badly* is
   never retried — that is a second opinion, not availability.
2. `internal/refine/AGENTS.md`: say which profile has a fallback and why it is
   the only one.

## Out of scope (do NOT do here)

- Re-enabling `cfg.DistillFallback`. That is the machine-wide switch task-51
  turned off, and this task is deliberately narrower than it.
- A fallback for any other profile.

## Definition of Done

- `make check` green, including a test that classifies through the reason tier
  with the distil CLI failing, and one asserting a bad answer is not retried.
