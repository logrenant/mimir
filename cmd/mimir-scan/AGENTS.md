# cmd/mimir-scan — AGENTS.md

Reads every project on this machine into Brain. Wiring and a loop, nothing else:
the work is `brain.DiscoverProjects` and `brain.Core.Scan`, both of which live in
`internal/brain` and are tested there.

## Why a third binary

`brain_scan_repo` is the same scan behind an MCP tool, and it is deliberately
one bounded batch per call — its caller is a session with a request timeout and
a context budget. Driving a whole machine through it means hundreds of
round-trips through a model that is not the one doing the work: the actual cost
is one `agy` call per file (`internal/llm` routes the distil class), and the
session in the middle pays for every batch boundary. This binary is the same
work with the session taken out of it.

**Since task-51 the daemon does run one**, continuously, over
`cfg.BrainScanRoots` — that is `internal/brain/supervisor.go`, and it is the
same `Core.Scan` again. This binary did not become redundant: the supervisor
watches two fixed roots and answers to a button in the desktop app, while this
one takes any root, has a dry run, and needs no daemon at all. What it must not
become is a second scan implementation.

## Rules for this directory

- **No default root.** `mimir-scan` with no argument is an error, the same way
  `brain.Core.Scan` refuses an empty project path. "Scan everything" is not a
  thing anyone means by accident.
- **stderr only** (SD-4). `mimirmcp.InitLogging(os.Stderr)`, then `slog`. The
  stdout lint covers this directory too.
- **The loop condition is the pass's own `remaining`**, never a count taken
  before the first pass. A file that changes mid-scan is picked up by the next
  pass rather than missed by arithmetic decided in advance.
- **A pass that scanned nothing and still reports work left ends the project.**
  Whatever it could not read now, it will not be able to read next time round,
  and the alternative is a loop that never terminates.
- **Interrupting is a supported ending**, not a failure: SIGINT cancels the pass
  in flight, everything already distilled is in the store, and the next run
  resumes from the content hashes.
