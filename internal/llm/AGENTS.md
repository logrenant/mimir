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
| `Distill` | one-shot compression: a page summary, an episode recap, a node's assessment and tags, a closed-vocabulary classification | `agy` | `gemini-3.8-flash-low` |
| `Reason` | synthesis across sources, where the extra capability is worth paying for | `claude` | `claude-haiku-4-5-20251001` |

### Two fallbacks, and why they are different fields

`DistillFallback` moves work to another **provider**. It is empty on purpose
(task-51): `agy` running dry must not hand the work — and the bill — to the
operator's `claude` login behind their back.

`DistillModelChain` moves work to another **model on the same provider**, and
that is why it may be non-empty where the other may not. `agy` meters its
models in two independent free pools: the Gemini tiers draw on one, the Claude
and GPT-OSS tiers on a second. Exhausting the first says nothing about the
second, so trying it reaches no paid login — it is the same free `agy`, spending
a bucket that was already full. Shipped as
`["gpt-oss-120b-medium", "claude-sonnet-4-6"]`: cheapest first, GPT-OSS leading
because it is the only one of the three that does not think before answering.
Opus is deliberately absent — it is the most expensive model `agy` offers, and
the chain exists to protect the reserve rather than to spend it.

The chain runs before the provider fallback, and both are suppressed by an
explicit `Selection` for the same reason.

The table is the *default*, not a law. `Router.CompleteWith` takes an
`llm.Selection` — a provider name and a model name — and an operator can send
one per lead-gen run. The class routing still answers everything the daemon
starts on its own; the override exists for the one case the routing cannot
serve, which is a run somebody is watching and paying for.

Three properties of a selection are load-bearing, and all three are tested:

- **It never mutates the router.** `Provider.WithModel` returns a copy. One
  router serves every concurrent call in the daemon, so a per-request model
  written through it would decide what somebody else's run spends.
- **It suppresses the fallback.** A fallback is availability, and availability
  is the daemon's policy; once an operator has named a provider, running
  somewhere else spends a budget they did not choose.
- **It is allow-listed, not free text.** Both names become argv to a
  subprocess. `config.LLMProviders` is the whole vocabulary, `Config.HasLLMModel`
  is the gate, and `GET /llm/providers` publishes it so the desktop picker is a
  view of that table rather than a second copy of it.

**There is no distil fallback (task-51).** There was one, and the reasoning was
sound as far as it went: a provider that answered *badly* is never retried
elsewhere, but one that cannot run at all is an availability problem, and every
caller has a claude login. What that missed is what "availability" costs. The
operator asked for a machine-wide scan — some three thousand files, two calls
each — through the free tier; a silent hand-off the moment agy's quota ran out
turns that into a bill nobody chose, at a moment nobody is watching.

So `cfg.DistillFallback` is empty, `NewRouter` reads an unnamed fallback as *no*
fallback, and the distil tier fails loudly instead. The mechanism is left
standing and still tested: putting `"claude"` back in one config line restores
the old behaviour, which is the right shape for a decision that could be
revisited.

The consequence is worth stating plainly, because it changes shipped tools:
when agy is down, `fetch_page` and `research` **error** rather than quietly
working, lead-gen's email drafting produces nothing, and every Brain ingest
stores a titled node with no assessment and no content hash — which is the
designed degradation, and is retried on the next pass.

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
  one is its own task. The effort suffix is part of the tag and part of the
  decision: `-high` because a node is distilled once and read by every later
  session, so the better answer is worth having permanently, and the tier costs
  the same either way. What it costs is wall clock — a sweep of a whole machine
  takes noticeably longer at `-high` than at `-low`.

- **Which account a claude call spends is injected, never imported.**
  `Claude.UseEnviron` / `Router.UseEnviron` take a `func() []string`, and
  `cmd/mimir-daemon` passes one that reads the marked account and calls
  `account.Environ`. Two reasons for the shape: `internal/account` cannot be
  imported here (account → store → refine → llm is a cycle), and the account can
  change while the daemon runs, so the answer is read per call rather than
  captured at construction. Unset means "inherit this process", which is what
  `cmd/mimir-mcp` — with no account registry — can honestly say. Only the claude
  provider takes it: `agy` is a different CLI with its own login.

- **`Request.Schema` is always safe to set.** A provider that cannot enforce it
  leaves `Response.Structured` nil and the caller parses `Text` instead. That is
  the documented signal, not a failure.

## Reviewer focus

SD-1 (every flag and model is a `config` constant), SD-5 (pinned tags), SD-6
(`ErrProviderUnavailable` everywhere a CLI can fail).

Grep for `exec.Command` outside this package and `internal/coderunner` — a third
one is the drift this package was created to stop. Grep also for a test that
sets `ClaudeCLIPath` and then expects distil work to happen: that test is
asserting a fallback which no longer exists, and it will pass for the wrong
reason only until somebody hand-builds a `Config` with an empty
`DistillProvider`. Grep for a test that sets
`ClaudeCLIPath` without also neutralising `AgyCLIPath`; that test is talking to
the network.
