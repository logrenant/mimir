# task-49 — Read every project on this machine into Brain

- **Status:** done
- **Owner agent:** daemon
- **Prerequisites:** task-47 (`Core.Scan`, the content-hash skip)
- **Primary paths:** `cmd/mimir-scan/` (new), `internal/brain/discover.go` (new), `internal/brain/scan.go`, `internal/brain/brain.go`, `internal/config/config.go`, `Makefile`
- **Roadmap bucket:** B.9 extension

## Context

task-47 shipped one repository at a time, driven by an MCP tool, because its
caller is a session with a request timeout. Reading a machine that way is
hundreds of round-trips through a model that is not doing the work: the work is
one `gemini-3.7-flash-low` call per file through `agy`, and the session in the
middle pays for every batch boundary out of its own context.

The operator asked for the opposite shape: start it themselves, let it run.

Two gaps came out of measuring the real tree (`~/development`, 16 projects):
`git ls-files` alone missed 69 of CozyFarm's 87 files because they were never
committed, and `personal/goat` — 400 loose files and a checkout nested inside it
— was not a project at all as far as discovery was concerned.

## Scope (do exactly this)

1. **`internal/brain/discover.go`** — `DiscoverProjects(root, maxDepth)`: a git
   checkout is a project and owns everything beneath it; a directory that never
   became one is a project if it holds files of its own, and claims its subtree
   except for checkouts nested inside it. Bounded by `cfg.BrainScanDepth`, which
   is a guard against a mis-typed root, not a tuning knob.
2. **`listScannable` includes untracked-but-not-ignored files**
   (`--cached --others --exclude-standard`). Being uncommitted says nothing
   about whether a file belongs to the work; `.gitignore` still decides what
   does not.
3. **The walk fallback skips a nested checkout**, which is the other half of
   rule 1: those files are scanned under their own project path, once.
4. **A node that was not distilled keeps no content hash** (`Core.Ingest`), and
   `Scan` counts it as failed. Otherwise one provider hiccup marks a file read
   forever, silently — which a three-hour run makes likely rather than
   theoretical.
5. **`cmd/mimir-scan`** — wiring and a loop: discover, then run each project to
   `remaining == 0`, logging each pass to stderr (SD-4). `-n` for a dry run. No
   default root. SIGINT ends the pass in flight and the next run resumes.
6. **`make scan` / `make scan-dry`** with `ROOT ?= $(HOME)/development`.

## Out of scope (do NOT do here)

- A daemon job or a desktop screen. A scan is something an operator starts,
  watches and interrupts; it does not need to survive a logout.
- Raising `BrainScanConcurrency`. Three concurrent `agy` calls is task-47's
  number and this task has no evidence to change it.
- Scanning anything but a root the operator named.

## Definition of Done

- `make check` green.
- `mimir-scan -n ~/development` reports every project without spending anything.
- A run that is interrupted and restarted skips what it already distilled.
- A file that failed to distil comes back on the next run.

## Changelog

- `internal/brain/discover.go` — `DiscoverProjects` + `holdsOwnFiles`.
- `internal/brain/scan.go` — untracked files listed, nested checkouts skipped by
  the walk, `.build` added to `skipDir`, undistilled files counted as failed.
- `internal/brain/brain.go` — `Ingest` drops the content hash when the distil
  failed.
- `internal/config/config.go` — `BrainScanDepth` (6), validated.
- `cmd/mimir-scan/` — the binary and its `AGENTS.md`.
- `Makefile` — `build`/`release` cover the third binary; `scan`, `scan-dry`.
- `docs/CAPABILITIES.md`, `docs/CAPABILITIES.tr.md` — the machine-wide scan.
- Measured on `~/development`: 16 projects, 2 876 files pending, ~11 s per file
  at concurrency 3 → about three hours for the first full pass.
