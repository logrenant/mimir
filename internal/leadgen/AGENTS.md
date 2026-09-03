# AGENTS.md — internal/leadgen

Turns companies into a lead list. Three stages live here — **categorization**
(`Categorize`, stage 2), **per-category gap analysis** (`AnalyzeCategory`,
stage 3, M6) and **outreach email drafting** (`DraftFor`, stage 4, M6) — plus
`Pipeline` (task-34), the orchestrator that threads region search and all three
stages into one call.

## Rules for this directory

- **This package does not decide which region-search source to ask.**
  `internal/regionsearch` owns that order (free scrape first, billed Places
  behind it) and the pipeline holds one `RegionSource` seam. The pipeline owns
  the *cache* around that call and nothing else about provenance; a second
  source-ordering decision here is exactly the split that existed before
  task-55, where this file and the `maps_search` registration each knew half of
  it.
- **The export is a view, not a stage.** `Export` writes what a `Report`
  already says, plus contact details from `internal/contacts` when the caller
  asked for them. It runs no model of its own, and enrichment is off by default
  because it is one page fetch per company — a run nobody exports must not
  fetch sixty websites.
- **One sheet per category, and the summary first.** The question the file
  answers is asked one category at a time ("who are the dentists, and which of
  them has no website"), and a spreadsheet that has to be filtered first answers
  it worse. Excel's own limits — 31 characters, no `:\/?*[]`, no duplicate
  names — are `sheetName`'s job, and a category must never cost a sheet.
- **The rule table is the primary tier; the model is the exception.** Three
  tiers run in order, cheapest first: the `company_categorization` cache, the
  static Google-type table, then `internal/refine.Classify` for what is left. A
  change that sends more companies to the model than the previous version did is
  a regression, not a refinement — `TestCategorize_RuleTierCostsNothing` uses a
  classifier that fails the test if it is called at all, and it should stay that
  way.
- **Generic Google types stay out of the table.** `point_of_interest`,
  `establishment`, `store`, `food`, `premise` carry no business meaning. Mapping
  them would turn "we do not know" into a confident wrong answer for free, and
  the model tier exists precisely for those rows.
- **The model chooses from a closed vocabulary; it never writes.** `Category`'s
  values are the whole answer space. Anything else that comes back — an invented
  category, an id we never sent, prose — is dropped, and the company is left
  unresolved. No model-generated free text leaves this package, which is why
  categorization does not need the SD-2 refine ceiling that page text does.
- **Only resolved answers are cached, and a failure never is.** An explicit
  `unknown` from the model is a real answer and is stored: re-asking costs the
  same tokens for the same shrug. An `unknown` that came from a broken
  subprocess, an unparseable reply, or an answer outside the vocabulary is
  **not** stored — otherwise one bad batch becomes a permanent fact for this
  taxonomy version.
- **`config.LeadgenCategoryVersion` is the only invalidation.** It covers both
  halves of the taxonomy: the rule table *and* the classify prompt. Edit either
  and bump it in the same change, or the cache serves answers derived from a
  taxonomy that no longer exists. There is no TTL here on purpose — a business's
  category does not go stale on a clock; ours goes stale when we change it.
- **Degrade, never fail (SD-6).** A cache that cannot be read or written, and a
  classify batch that fails, all become entries in the returned `gaps` slice.
  The only error `Categorize` returns is the caller's own cancellation.
- **Bounded fan-out (SD-3).** Batches go through `errgroup.SetLimit(
  cfg.MaxConcurrentRefines)`; no goroutine outlives `Categorize`. Note the known
  limitation: this limit is per-call and separate from `internal/pipeline`'s
  process-wide refine semaphore, so a categorization running beside a `research`
  call can exceed the global intent. Unifying them is a hardening task, not a
  silent change here.
- **Results are positional.** One `Result` per input company, in input order,
  including duplicates — which are classified once and answered twice.

## Stage 3 — gap analysis (`gaps.go`)

- **The model is fed facts, never pages.** `gapFacts` reduces each company to
  five deterministic signals (has_website, has_phone, rating, review_count,
  business_status). No scraped text reaches `refine.AnalyzeGaps`, so the only
  injection vector is a business name — `internal/refine` fences it exactly as
  it fences a classify batch. This is why the synthesis is safe to return as
  prose while categorization must not.
- **Prose output, so the SD-7 ceiling applies.** `AnalyzeGaps` runs through the
  same `clampOutput` as `Distil`, bounded by `config.LeadgenGapMaxTokens`. The
  one difference is the "output must not exceed input" check is off: this
  profile is fed a tiny structured block on purpose and a real synthesis is
  meant to be longer than it.
- **The cache key is total.** A gap analysis is a function of region, category,
  `config.LeadgenGapVersion`, *and the exact company set* — `companySetHash`
  (sha256 over sorted place_ids) carries the last one. A different set of
  companies is a different analysis and misses. `LeadgenGapVersion` is its own
  key half, independent of `LeadgenCategoryVersion`.
- **Deterministic regardless of input order.** `AnalyzeCategory` sorts a copy of
  the companies before hashing and before building the prompt, so the same set
  in any order produces the same cache key and the same subprocess input.
- **A floor, not a guess.** Below `config.LeadgenGapMinCompanies` the category
  is left unresolved with a gap — "these three share a weakness" is a claim
  about noise.
- **Degrade, never fail (SD-6).** Too few companies, a failing cache, a failing
  subprocess, and an unrefined/empty answer all become `gaps` entries and an
  unresolved `GapResult`. Only the caller's cancellation returns an error. An
  unusable answer is never cached.

## Stage 4 — outreach email drafting (`email.go`)

- **One draft per company, keyed by `place_id`.** A scrape-fallback row with no
  `place_id` cannot be keyed, so `DraftFor` skips it with a gap rather than
  generating a draft that has nowhere to live.
- **The `status` column is the whole point.** `draft` is refreshable on a region
  re-run; `sent` and `skipped` are human decisions and are replayed from the
  cache untouched — the model is never called for them. The store's
  `PutOutreachEmail` enforces the same rule in SQL (`WHERE status = 'draft'`) so
  a race cannot clobber a sent row.
- **The drafter is fed derived facts plus stage 3's output.** `refine.EmailInput`
  carries the company's own scalars and the category's gap-analysis paragraph —
  which already passed `clampOutput` once, so it is refined text, not raw page
  text. The only provider-controlled string is the business name, fenced.
- **Prose output, SD-7 ceiling.** `refine.DraftEmail` runs through `clampOutput`
  bounded by `config.LeadgenEmailMaxTokens`; `inputLen 0`, same reason as
  `AnalyzeGaps`.
- **`config.LeadgenEmailVersion`** is its own cache-key half, independent of the
  categorization and gap-analysis versions.
- **Degrade, never fail (SD-6).** No `place_id`, an empty gap analysis, a
  failing cache, a failing subprocess, an unrefined answer → a `gaps` entry and
  an unresolved `EmailResult`. Only the caller's cancellation returns an error.
  An unusable answer is never cached.

## The pipeline (`pipeline.go`)

- **`Run` fails only when there is no data at all.** `ErrNoData` (neither Places
  nor the scrape fallback produced a company list) and a cancelled context are
  the only errors. Every other problem — a cache miss-by-error, a failed
  category, an unusable email — is a string in `Report.Notes`, aggregated from
  the stage runners' own `gaps` slices.
- **Region search: cache → Places → scrape.** A cached region is a hit (all
  companies present and fresh, per `store.GetRegionSearch`'s all-or-nothing
  contract). A Places failure falls back to the scraper *only if one was
  wired*; both failing is `ErrNoData`. A successful non-cached search is written
  back.
- **Stages 1–2 always run; 3–4 are opt-in per `RunRequest`.** `WithEmails`
  implies `WithGapAnalysis` — an email needs its category's gap analysis. This
  keeps the default call near-zero-token.
- **`CategoryUnknown` gets no gap analysis.** Unclassified companies share
  nothing to synthesize a pattern from; the category is reported with a count
  and an empty analysis.
- **Bounded fan-out (SD-3).** Gap analysis (across categories) and email
  drafting (across companies) each run as one `errgroup` phase limited to
  `cfg.MaxConcurrentRefines`; no goroutine outlives `Run`. Each email goroutine
  writes a distinct `leads` index, so the slice needs no lock. The known
  per-call vs. process-wide limitation from task-29/30 still applies — this is
  where a global bound belongs if one is added.
- **The ledger is what happens *to* a finished run, not a stage of it.** It is
  installed with `UseLedger` after construction, like `UseContacts`, so
  `NewPipeline`'s parameters stay the stages. `record` runs last, skips a lead
  with no `place_id` (the ledger is keyed by it, exactly as the outreach drafts
  are), and turns any failure into a `Report` note. A run that answered is a run
  that succeeded — losing the record costs a row in a table, never the answer on
  the screen (SD-6). The run id is random rather than derived from the query:
  the same search run twice is two runs, which is the whole point of keeping a
  history.
- **The model selection binds once, at the top of `Run`.** `RunRequest.Selection`
  is the operator's provider/model choice for this run, and `Run` calls
  `.With(sel)` on all three model stages before any of them executes — so a run
  cannot end up half on one model and half on another. `With` returns a *copy*
  rather than taking a parameter on `Categorize`/`AnalyzeCategory`/`DraftFor`:
  the stage runners are built once and shared by every concurrent run, and a
  field written per request would decide what somebody else's run spends. The
  zero selection returns the receiver unchanged, so a caller that offers no
  picker behaves exactly as it did before this existed.
- **A selection namespaces the caches it reads.** Each stage folds
  `Selection.Key()` into its own version string (`…@agy/gemini-3.1-pro-high`).
  Without it a run switched to another model is served the previous model's
  answers and never calls the one that was chosen — the picker would look like
  it did nothing. The zero selection contributes nothing to the string, so every
  entry cached before the picker existed is still a hit. The cost is on the
  email stage and is worth being deliberate about: a draft a human marked
  "sent" under one model is not replayed for another, so switching model
  mid-region can re-draft a letter that has already gone out.
- **The selection does not reach the region search.** `internal/mapsllm` — the
  model fallback used when the scrape's selectors fail — is wired at
  construction and shared by every caller of `maps_search`, so a per-run
  override there would need a seam this pipeline does not own. Stages 2, 3 and 4
  are what the picker controls, and they are where a run spends its tokens.
- **`nil` collaborators degrade, they do not panic.** A nil categorizer,
  gap runner, email runner, region store or scraper each disables its stage or
  its cache; only a nil searcher is fatal.

## Reviewer focus

Can a value outside `Categories()` reach a caller or the database? Does anything
other than a cancelled context make `Categorize`, `AnalyzeCategory`, `DraftFor`
or `Pipeline.Run` return an error (`Run` may also return `ErrNoData`)? Is a
failed batch, an unrefined gap analysis, or an unrefined email ever cached? Does
a rule-table edit without a version bump slip through review? For stage 3: is
`companySetHash` still total, and does any raw provider text reach
`refine.AnalyzeGaps` past `gapFacts`? For stage 4: can a region re-run overwrite
or regenerate an email a human marked `sent`? For the pipeline: does any stage
failure escape as an error instead of a note, and do the two fan-out phases
stay inside `cfg.MaxConcurrentRefines`?
