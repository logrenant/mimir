# task-47 — Scan a whole repository into Brain with agy

- **Status:** todo
- **Owner agent:** daemon
- **Prerequisites:** task-45 (needs `brain_nodes.content_hash` from `0014`)
- **Primary paths:** `internal/brain/scan.go` (new), `internal/tools/brain.go`, `internal/api/brain.go`, `internal/config/config.go`, `Makefile`
- **Roadmap bucket:** B.9 extension

## Context

task-45 records what *happens*. This records what *is*: one pass over a
repository so that a session's first question about an unfamiliar file is
answered from the store instead of by reading it. It is the one operation here
that is deliberately expensive up front — hundreds of `agy` calls — because it
is paid once per repository and amortised over every later session.

It must not be startable by accident, and it must be resumable: an interrupted
scan that has to start over is a scan nobody runs twice.

## Scope (do exactly this)

1. **`internal/brain/scan.go`** — `Scan(ctx, projectPath, opts)`:
   - walk the project with `git ls-files` when the directory is a repository
     (that is the allowlist: it already excludes `.gitignore`d paths, build
     output and vendored trees), else a filtered `filepath.WalkDir`;
   - skip binaries by content sniff, and anything over `BrainScanMaxFileBytes`;
   - for each file compute `sha256(content)`; **skip it when a node already has
     that hash and the current `BrainPromptVersion`** — this is what makes a
     re-scan cheap and an interrupted scan resumable;
   - distil each survivor into a `kind="file"` node through the existing
     `Core.Ingest` path, so the choke-point, the validator and the linker are
     the same ones everything else uses;
   - bounded by `errgroup.SetLimit(BrainScanConcurrency)` and cancellable
     (SD-3); progress is logged, never printed to stdout (SD-4).
2. **Reuse, do not fork, `Core.Ingest`.** The only new logic is *which files*
   and *whether to skip*. A second ingest path would drift from the first.
3. **Two ways to start it**, because it has two audiences:
   - `make brain-scan PROJECT=<path>` for a person;
   - `POST /brain/scan` on the daemon for the app, returning immediately with a
     job id and streaming progress over the existing event bus.
4. **A dry run that costs nothing**: `--dry-run` reports how many files would be
   distilled and how many are already current, so the size of the bill is
   knowable before it is spent.

## Out of scope (do NOT do here)

- Scanning anything but a directory the operator named. There is no default
  project, ever (`internal/project`).
- Re-distilling on every run. A file whose hash and prompt version match is
  skipped, and that is the whole point.
- A desktop screen.

## Definition of Done

- `make check` green.
- A second scan of an unchanged repository makes **zero** model calls and says so.
- Interrupting a scan and re-running it resumes: already-distilled files are skipped.
- `--dry-run` makes zero model calls and reports a file count.
- A binary file, a 10 MB file and a `.gitignore`d file are all skipped.

## Notes for the reviewer (Opus)

The cost model is the thing to check. `git ls-files` on this repository is a few
hundred files; at roughly 25k input tokens per `agy` call that is a real amount
of somebody's free quota, and the `--dry-run` and the hash skip are what keep it
from being spent twice. If either regresses, the feature becomes one people run
once and then avoid.
