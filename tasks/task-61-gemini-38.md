# task-61 — The distil tier moves to gemini-3.8-flash-high

- **Status:** done
- **Owner agent:** daemon
- **Prerequisites:** task-41 (`internal/llm`), task-51 (the distil tier is agy-only)
- **Primary paths:** `internal/config/config.go`, `internal/llm/AGENTS.md`, `docs/{ARCHITECTURE,SECURITY,CAPABILITIES,CAPABILITIES.tr}.md`, `cmd/mimir-scan/main.go`, `test/e2e/e2e_test.go`
- **Roadmap bucket:** B.1 maintenance

## Context

Gemini 3.8 shipped, and the operator asked for it. Bumping a pinned model tag is
its own task (SD-5, `AGENTS.md` §4) precisely so it is a deliberate, reviewable
act rather than a drive-by edit.

There is a second reason this one mattered. On this machine the distil tier had
been failing every call with `exit status 1` and an empty stderr — the failure
that made `internal/leadgen`'s classify fall back to claude (task-59) and, before
that, left every scraped company `unknown`. `agy models` listed 3.7 and 3.8;
running the daemon's exact invocation by hand, in its own scratch directory,
with `MIMIR_NESTED=1` and the same `--json-schema`, answered `SUCCESS` on 3.8.
So the bump is not only newer, it restores the primary path.

## Scope (do exactly this)

1. `cfg.DistillModel` → `gemini-3.8-flash-high`. The effort suffix moves with
   the model rather than being reopened: the reason for `-high` (a node is
   distilled once and read by every later session) did not change.
2. Every document that states the *live* tag: `internal/llm/AGENTS.md`'s routing
   table, `docs/ARCHITECTURE.md`'s invocation line, `docs/SECURITY.md`'s
   statement about what is sent to Google, both CAPABILITIES files, and
   `cmd/mimir-scan`'s package doc.
3. `test/e2e`'s fake `agy models` output, so the stub matches what the CLI now
   lists.

## Out of scope (do NOT do here)

- Invalidating the caches. `refined_pages`, the memory recaps and the Brain's
  nodes were distilled by 3.7 and stay: they are still true summaries of their
  sources, and re-deriving a machine-wide corpus to change which model's name is
  attached would cost an afternoon and buy nothing. Provenance is not lost —
  `brain_nodes` records the provider and model that wrote each row, so a node
  can always say which tier produced it.
- Reconsidering the fallback. task-59's classify fallback stays exactly as it
  is: a tier that answers today is not a reason to remove the path that covers
  it when it does not.
- `docs/ROADMAP.md`'s historical `gemini-3.7-flash-low` line, the retired task
  files, and past CHANGELOG entries. Those record what was true when they were
  written, and a task may not edit them (`AGENTS.md` §4).

## Definition of Done

- `make check` green.
- `agy --model gemini-3.8-flash-high` answers `SUCCESS` for the daemon's exact
  invocation, schema included — verified by hand, not assumed.
- A lead-gen run categorises through `agy` again, without the claude fallback
  line appearing in the daemon log.
