# internal/llm — AGENTS.md

The one exit point for every non-coding model call. Two providers, both a local
CLI riding an existing login, and a router that picks between them by *class of
work* rather than by taste.

Callers: `internal/refine` (five prompt profiles) and `internal/brain` (the
distil and the relation pass). `internal/coderunner` is deliberately **not** a
caller — see below.

## Why this package exists

Before it there were two hand-written copies of the same subprocess dance, one
in `internal/refine` and one in the first Brain layer, and they had already
drifted: the second re-invented the exec, the flags, the retry loop and the JSON
envelope, and got the parsing wrong in a way that silently produced nodes with
no tags at all. One exit point is the fix, for the same reason
`tools.RegisterAll` is one list.

## The routing contract (ROADMAP §B.1, amended 2026-09-02)

| Class | What it is | Provider | Model |
|---|---|---|---|
| `Distill` | one-shot compression: a page summary, an episode recap, a node's assessment and tags, a closed-vocabulary classification | `agy` | `gemini-3.7-flash-low` |
| `Reason` | synthesis across sources, where the extra capability is worth paying for | `claude` | `claude-haiku-4-5-20251001` |

`DistillFallback` is **availability, not a retry policy.** A provider that
answered *badly* is never retried elsewhere. `agy` missing, or its free quota
spent, must not take the distil path down, because every caller already has a
claude login.

## Rules

- **The coding runner does not belong here.** Its stream-json transport,
  permission mode and process-group signalling are a different contract, and
  folding them in would mean this package owned two unrelated things. It keeps
  its own `exec` in `internal/coderunner`.

- **Content goes over stdin, never argv.** Page bodies and node contents run
  past what an argument list can hold (ARG_MAX is 1 MiB), and `agy`'s `-p` takes
  the prompt as the flag's *value* rather than being a boolean the way claude's
  is. `--input-format text` plus stdin is the shape that works for both.

- **An empty `AgyCLIPath` is not defaulted to `"agy"`.** `config.Validate`
  rejects an empty one, so the only way to reach the constructor with it is a
  hand-built `Config` — which is what tests do — and defaulting would send those
  tests to whatever `agy` happens to be installed on the machine instead of the
  fake CLI they just wrote. The provider reports itself unavailable and the
  router falls back. This is not a nicety: it was found by an `e2e` run that
  took 29 seconds because it was making real network calls, and by a refine test
  that failed on a real model's mood.

- **`agy` has no `--disallowedTools` and no `--strict-mcp-config`.** Three
  things stand in for them and all three are asserted by tests, because losing
  any one silently gives the subprocess a tool surface back:
  1. `--sandbox`,
  2. a working directory that is an empty scratch dir under the store's parent —
     never a repo, because `agy` reads `AGENTS.md` and `.agents/rules` from
     wherever it starts, and the subprocess's entire input is untrusted text,
  3. `MIMIR_NESTED=1`, which `cmd/mimir-mcp` answers by serving **no tools**, so
     a globally-registered Mimir cannot be recursed into.

  This is weaker than the claude path's guarantee. `docs/SECURITY.md` says so
  rather than carrying the old sentence unchanged.

- **Read `structured_output`, not `response`.** When a schema was given, `agy`
  returns both, and the `response` string additionally carries the CLI's own
  `toolAction`/`toolSummary` keys. Those are not ours to hand on.

- **A cancelled context never triggers the fallback.** The caller went away, and
  starting a second subprocess on its behalf is work nobody is waiting for.

- **Every model tag is pinned (SD-5).** No `latest`, no floating alias. Bumping
  one is its own task.

- **`Request.Schema` is always safe to set.** A provider that cannot enforce it
  leaves `Response.Structured` nil and the caller parses `Text` instead. That is
  the documented signal, not a failure.

## Reviewer focus

SD-1 (every flag and model is a `config` constant), SD-5 (pinned tags), SD-6
(`ErrProviderUnavailable` everywhere a CLI can fail).

Grep for `exec.Command` outside this package and `internal/coderunner` — a third
one is the drift this package was created to stop. Grep for a test that sets
`ClaudeCLIPath` without also neutralising `AgyCLIPath`; that test is talking to
the network.
