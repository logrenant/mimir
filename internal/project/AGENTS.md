# AGENTS.md — internal/project

**This package is a security boundary.** Introduced by `tasks/task-20` for
Track B / M2.

## What it is protecting against

`internal/coderunner` starts a `claude` session with file-editing tools
enabled. The only thing between that session and the entire disk is the
directory it is pointed at. This package decides what that directory may be.

goat v1 is the cautionary tale: it shipped `"tools": {"roots": ["/"]}` as its
default, and its own README conceded the combination of web fetch, whole-disk
read, and shell was a complete exfiltration primitive. The fix here is
structural rather than a safer default — **there is no default project at
all**. A run cannot start until a directory has been explicitly registered
through the picker, and every caller afterwards passes an opaque `id`, never a
path.

## Rules for this directory

- **A path is only ever accepted by `canonicalize`.** No other function may
  turn caller input into a directory to run in. If you add an entry point that
  takes a path, it goes through `canonicalize` first — no exceptions, no
  "internal use only" shortcuts.
- **Resolve symlinks before consulting the denylist.** `~/shortcut -> /` must
  not smuggle the root past a string comparison. `EvalSymlinks` runs first,
  and its failure on a nonexistent path is load-bearing.
- **The denylist is resolved through `EvalSymlinks` too.** On macOS `/etc`,
  `/var`, and `/tmp` are symlinks into `/private`, so a candidate canonicalises
  to `/private/etc` while the literal `"/etc"` would not match it. Both forms
  are in the list; `initDeny` builds them.
- **Comparison is case-insensitive.** macOS's default filesystem is
  case-insensitive, so `/ETC` *is* `/etc`. A denylist that can be bypassed by
  shouting is not a denylist. Being slightly over-strict on a case-sensitive
  filesystem is the correct direction to err.
- **Two lists, and the difference matters.** `rawExactDenied` are containers
  that legitimately hold projects (`/Users`, `/private`, a home directory) —
  matched exactly, never as a prefix, or every real project would be refused.
  `rawTreeDenied` are system trees (`/etc`, `/usr`, `/System`) — denied at any
  depth, because nothing inside them is somebody's project.
- **Consumers call `Resolve`, not `Get`.** `Get` returns what was recorded;
  `Resolve` re-runs every guard against the filesystem as it is *now*. A path
  recorded weeks ago may have been deleted, replaced by a file, or re-pointed
  at something far broader. A row is not a permission.
- **Registration is idempotent by resolved path**, so symlinks, `..`, and case
  variants collapse to one project instead of quietly creating several
  half-tracked ones.

## Testing

Be hostile. The existing tests already try: symlink to `/`, `..` walked past
the filesystem root, `/ETC` case variants, trailing slashes, a file instead of
a directory, and a directory deleted or swapped after registration. When you
change `canonicalize`, add the bypass you were tempted by.

Never write to a real home or system directory in a test — `t.TempDir()` only.
Tests that inspect real system paths must `t.Skip` when the path is absent
rather than assume a macOS layout.

## Reviewer focus

Try to defeat `canonicalize`, then check that nothing else in the tree
constructs a run directory without it — `grep` for `--add-dir` and `cmd.Dir`
and confirm both trace back to `Registry.Resolve`.

## Reading without minting a permission (M8)

`Canonicalize` and `Find` exist for readers that need this package's path
discipline but must **not** create a registration.

A registration is a durable row the coding-task runner can later be pointed at,
so creating one as a side effect of reading would quietly hand out the thing the
"no default project, ever" rule is built to withhold. The project memory
(`internal/memory`, and the three tools in `internal/tools/memory_tools.go`)
therefore calls `Canonicalize` — same `Abs`+`EvalSymlinks`+denylist as
registration — and calls `Find` only to *pick up* an id the picker already
minted, carrying on without one when it has not.

Anything that will **act** inside a directory still goes through `Register` and
`Resolve`. Reviewer check: grep the callers of `Canonicalize` and confirm none
of them starts a process, writes a file, or passes the path to `--add-dir`.
