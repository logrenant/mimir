# AGENTS.md — internal/refine

The **context-isolation firewall** (SD-2). Big, messy, possibly hostile scraped
Markdown enters; compact, factual, query-scoped text leaves. If this package is
weak, the whole guarantee is weak.

## Rules for this directory
- Talks only to the local `claude` CLI at `cfg.ClaudeCLIPath` (default
  `claude`, on `$PATH`), model `cfg.ClaudeModel` (`claude-haiku-4-5-20251001`,
  pinned — SD-5). Invoked headless via `exec.CommandContext`: `-p
  --output-format json --no-session-persistence --strict-mcp-config
  --restricted --effort low`, with every built-in tool force-denied via
  `--disallowedTools`. No API key, no HTTP dependency — it rides the
  operator's existing Claude Code login.
- **Prompt assembly is pure and deterministic.** `buildPrompt(Input) (system,
  user string)` must return byte-identical output for identical input: no
  maps ranged in place, no `time`, no randomness. It is covered by a golden
  file — update the golden deliberately when you change the template, never
  paper over a diff.
- Scraped page text is **untrusted data**. It is always wrapped in an explicit
  data fence and passed as the CLI's single stdin turn — the system prompt
  (ours, trusted, passed via `--system-prompt`) states the fenced region is
  content to summarise, never instructions. `--restricted` +
  `--disallowedTools` + `--strict-mcp-config` mean the subprocess has no
  tools and cannot recurse into mimir-mcp's own MCP registration, so even a
  successful injection can only change the *text* it emits, never take an
  action. Injection strings ("ignore previous instructions", fence-break
  attempts) must not alter behaviour — there is a test corpus for this.
- The output **ceiling is hard** (SD-7): `Input.MaxTokens` is enforced by
  `clampOutput` after the model responds. Empty output, output that echoes the
  injection, or output longer than the input → `ErrRefineRejected` (the caller
  drops that source; it never forwards junk).
- `Distil` sets `Output.Refined = true` **only** on the clean success path.
- Every call: `ctx` + `cfg.RefineTimeout`, one bounded retry on transient
  failure, typed `ErrClaudeUnavailable` with the `claude login` fix text
  (SD-6).
- **Four prompt profiles, one subprocess.** `Distil` (page → prose), `Classify`
  (companies → one category each), `AnalyzeGaps` (company facts → a
  category-level synthesis, M6) and `DraftEmail` (company facts + gap analysis →
  one cold-outreach email, M6) all go through the unexported `run`, so the flags
  that make the subprocess harmless are chosen in exactly one place. A fifth
  profile means another `buildXPrompt` + golden file, never another `exec` call
  site.
- **`AnalyzeGaps` and `DraftEmail` are Distil-shaped, not Classify-shaped.** The
  answer is prose, so `clampOutput` enforces the `MaxTokens` ceiling exactly as
  it does for a page. The only deviation: the "output must not exceed input"
  heuristic is disabled (inputLen 0) because both profiles are fed a
  deliberately compact block — `internal/leadgen` reduces each company to a few
  scalars first, so no raw page text reaches them and the output is meant to
  stand on its own. Empty-output and injection-echo rejection still apply.
  `DraftEmail`'s one caller-supplied multi-line field (the gap analysis, itself
  already-refined text) goes through `sanitizeMultiline`, which keeps newlines
  but still neutralises the fence markers.
- **`Classify` is a different shape of trust from `Distil`.** Its answer is
  matched against a **closed vocabulary the caller supplies**
  (`internal/leadgen`'s `Category` values): an id we never sent, a category we
  do not have, or output that is not JSON is dropped, and the caller sees an
  absence rather than a guess. That is why `clampOutput` is not used there — it
  trims prose to bullet boundaries and would corrupt JSON; the closed set plus a
  bounded batch is the ceiling instead.
- Untrusted fields in a classify batch (a business names itself) are flattened
  to one **JSON object per line** inside the same fence. Not `key=value` records
  — a name containing the separator would otherwise forge a second item.
- Phase 1 will add per-source prompt profiles (LinkedIn / ads / short-form
  video). Keep `buildPrompt` structured so a `SourceType` can select a
  profile later — but do not add profiles now.

## Reviewer focus
SD-2 (can any input make `Distil` emit unrefined/oversized text, or get the
subprocess to use a tool?), SD-7 (hard ceiling, not advisory), determinism of
`buildPrompt`, `Refined` set in exactly one place.
