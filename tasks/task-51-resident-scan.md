# task-51 — The distil tier is agy-only, and the scan moves into the daemon

- **Status:** done
- **Owner agent:** daemon
- **Prerequisites:** task-47, task-49 (`Core.Scan`, `DiscoverProjects`, the content-hash skip)
- **Primary paths:** `internal/llm/`, `internal/refine/refine.go`, `internal/brain/{supervisor,pdf,scan,brain}.go`, `internal/store/brain.go`, `internal/api/brain.go` (new), `internal/tools/diagnostics.go`, `internal/config/config.go`, `cmd/mimir-daemon/main.go`
- **Roadmap bucket:** B.9 extension

## Context

task-49 made a machine-wide scan possible; the operator ran the numbers and
asked for two changes to what it costs and who starts it.

**It must never spend claude.** `DistillFallback: "claude"` meant that the
moment agy's free quota ran out, some three thousand files' worth of distils
moved to a paid tier silently, at a moment nobody was watching.

**It must not need starting.** A scan the operator has to remember to run is a
scan that runs once. The daemon should keep one going for as long as it lives,
over `~/development` and `~/Documents`, and the desktop should be able to see
it — which is task-52.

`~/Documents` is six technical manuals in PDF, so without an extractor that root
contributes three files and nothing worth reading.

## Scope (do exactly this)

1. **agy-only.** `cfg.DistillFallback` is empty and `llm.NewRouter` reads an
   unnamed fallback as *no* fallback — `pick` answers claude for anything it
   does not recognise, so emptying the constant alone would resolve to the very
   provider it exists to keep out. `refine.Client.Health` stops accepting the
   reason tier as a second opinion. The mechanism stays, and stays tested.
2. **`internal/brain/supervisor.go`** — a `Supervisor` type: sweep the roots,
   run each project to `remaining == 0`, idle, repeat. Pause (which cancels the
   pass in flight), resume, scan-now, a `ScanStatus` snapshot under one mutex,
   and a probe-driven exponential backoff for a provider that is not answering.
   Durable summary in `brain_capture_state`; **no migration**.
3. **PDFs** — `internal/brain/pdf.go` reads them through `pdftotext` with a
   timeout, an output ceiling, an empty environment and a scratch cwd.
   `ErrNoPDFExtractor` and `ErrNoTextLayer` are separate errors. `ScanResult`
   gains `Unreadable`, which is deliberately not `Failed`.
4. **Store** — `BrainGraphIDs` (ranked by degree), `BrainEdgesAmong` (chunked,
   both endpoints guaranteed present), `BrainProjects`. No migration, no index.
5. **`internal/api/brain.go`** — `GET /brain/scan`, `POST /brain/scan/{pause,
   resume,now}`, `GET /brain/graph`, `GET /brain/projects`,
   `GET /brain/nodes/{id}`. `?project=` is `brain.ProjectID`, never a path.
6. **Diagnostics** — `agy` and `pdftotext` rows; `versions.distill_model`.
7. **Wiring** — the supervisor gets its own context, goroutine and drain in
   `cmd/mimir-daemon/main.go`, like the two loops already there.

## Out of scope (do NOT do here)

- Any desktop change (task-52).
- Raising `BrainScanConcurrency`. Three is task-47's number and nothing here is
  evidence to change it.
- A websocket for scan progress. The scan has one current state, not a stream.
- A knob for the roots, the interval or the backoff. They are constants (SD-1).

## Definition of Done

- `make check` green, `make e2e` green.
- With agy unavailable, no claude subprocess is spawned for distil work, and the
  supervisor backs off instead of hot-looping.
- A second sweep over an unchanged machine makes zero model calls.
- A PDF with no text layer is `Unreadable`, never `Failed`.
- The graph never returns an edge whose endpoints are not both in its node list.
- Zero migrations.

## Follow-up: the scan has a console

The Terminals tab answers "where is that thing running?", and the one job it
could not answer for was the one that never stops. The supervisor now keeps a
bounded ring buffer of its own lines — a file per name, a summary per pass, the
operator's own pause and resume, and the backoff — read by
`GET /brain/scan/log?after=<seq>`.

- **Not `internal/events`.** That bus is keyed by run id and stitched to a
  transcript by `Seq`, because a run emits things that must not be lost. The
  scan has no run id and no transcript; what a person watching it wants is
  narrower, and a poll over a ring buffer is the whole of it.
- **`ScanResult.FailedFiles`** names what `Failed` counted. A console that can
  only say "three files failed" is a progress bar with extra steps.
- **600 lines, then the oldest go.** The permanent record is one node per file
  in the store; keeping a whole sweep in memory for a console nobody may open is
  the wrong trade.

## Changelog

- `internal/config/config.go` — `DistillFallback: ""`; `BrainScanRoots` (derived
  from the home directory), `BrainScanIdleInterval`, `BrainScanBackoffMin/Max`,
  the five `BrainScanPDF*` values, `BrainGraphDefaultNodes/MaxNodes`;
  `MIMIR_PDFTOTEXT_PATH`; validation for all of them.
- `internal/llm/{llm.go,AGENTS.md}` — no fallback unless one is named;
  `Router.Fallback`.
- `internal/refine/refine.go` — `Health` no longer accepts the reason tier.
- `internal/brain/` — `supervisor.go`, `pdf.go`, `ScanResult.Unreadable`,
  `.pdf` is scannable, `ProjectID`, `ErrNodeNotFound`, `AGENTS.md`.
- `internal/store/brain.go` — the three graph reads.
- `internal/api/` — `brain.go` (including `GET /brain/scan/log`), three `Deps`
  fields, the route block, five error mappings, `AGENTS.md`.
- `internal/tools/diagnostics.go` — `agy`, `pdftotext`, `distill_model`.
- `cmd/mimir-daemon/main.go` — the supervisor's goroutine and its drain.
- `test/e2e/e2e_test.go` — a fake agy, since there is no fallback to a fake
  claude any more.
- Docs: `docs/{SECURITY,CAPABILITIES,CAPABILITIES.tr}.md`, `cmd/mimir-scan/`.

## Amendment (2026-09-04): the distil fallback is back on

`DistillFallback` is `"claude"` again. The decision above is not being
overruled — its objection was to a *silent* hand-off, and the silence is what
changed.

- **A run the operator routed by hand suppresses the fallback outright.** The
  brain scan now takes an `llm.Selection` (`POST /brain/scan/now`, the picker in
  the Brain tab), and `Router.CompleteWith` drops the fallback the moment a
  selection names a provider. A deliberate choice gets that provider or an
  error, never a substitute — which is exactly what this task asked for, now
  expressed as a control rather than as an absent config value.
- **The unselected case says so.** The distil answer carries the provider that
  actually served it (`llm.Response.Provider` → `IngestResult.Provider` →
  `ScanResult.Provider`), and the supervisor emits `EventProvider` into the scan
  console the first time a sweep's answers stop coming from the provider it
  asked for. The bill still moves; nobody has to find out afterwards.

What forced the change is the first mount of a large repository: one free pool
rarely covers it, and a scan that stops half way is not a cheaper outcome, only
a later one. The operator picks the provider that will cover it, and the loop
falls back only when nobody picked.

- `internal/config/config.go` — `DistillFallback: "claude"`, rationale rewritten.
- `internal/brain/` — `ScanOptions.Selection`, `Input.Selection`,
  `Completer.CompleteWith`, `IngestResult.Provider`, `ScanResult.Provider`,
  `Supervisor.ScanNow(sel)`, `EventProvider`, `noteProvider`.
- `internal/api/` — `scanNowRequest`, `decodeOptionalJSON`, `llmSelection`
  (was `leadgenSelection`, now shared with the brain route).
- `desktop/src/` — `ProviderModelPicker` moved out of `Leadgen.tsx` into
  `components/ModelPicker.tsx` and reused by the Brain tab.
