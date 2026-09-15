# AGENTS.md — internal/catalog

The product content studio (task-85). It reads an e-commerce product export,
learns the brand's own markup and voice from the file itself, and writes the
file back without disturbing anything it was not asked to change.

Two claims hold this package up, and both are tests rather than intentions:

1. **A cell nobody approved a change for is copied, not re-derived.** Import
   then export, with nothing approved, is byte-identical — BOM, encoding,
   delimiter, line ending, trailing newline, and the quoting of every
   individual cell.
2. **A rewrite cannot introduce markup the brand does not use.** A model never
   sees a tag here, so it cannot invent one.

## Rules for this directory

- **A model never sees markup.** `ParseHTML` splits a description into `Block`s:
  the text a rewrite may touch, and an opaque `Envelope` recording exactly what
  that text sat inside. `Render` replays the envelopes. Handing a model HTML and
  asking for HTML back is the failure this whole design exists to prevent —
  the invented markup is what an operator then pastes into a live storefront.
- **`Render` fails closed.** Every tag, attribute, class and style declaration is
  checked against the `Vocabulary` and dropped when it is not the brand's. An
  unknown *wrapper* is unwrapped rather than dropped with its contents: losing
  text because its container was unfamiliar would be worse than the markup it
  was meant to prevent.
- **A URL that was not in the source is not a URL.** `Render` takes the source
  document's own href/src set as an allow-list. A link whose target is absent
  becomes plain text; an image whose source is absent disappears. An invented
  URL on a product page is a broken link the merchant ships to customers, and
  it is the single most likely thing a rewrite gets wrong.
- **The two halves of the brand kit are separate, and only one costs anything.**
  `DeriveVocabulary` reads every description and makes no model call — it is the
  gate `Render` measures output against, and a gate that needed a model call
  would be a gate that opens when the model is signed out. `distilVoice` is one
  `llm.Distill` call over a sample; a rejection costs the voice and nothing else
  (SD-6). The test for the first half runs against a **nil** `Completer`, so a
  broken claim panics rather than quietly starting a subprocess.
- **The vocabulary is not the operator's to widen.** `SetBrand` takes the voice
  and ignores anything else that arrived with it. The voice is their own writing
  — `internal/settings`' argument — but the vocabulary is derived from their own
  past HTML and is what every rewrite is measured against.
- **This package owns its own CSV reader and writer.** `encoding/csv` cannot
  report which cells were quoted, and its writer owns three of the framing
  decisions this package exists to preserve. Sixty lines against a file the
  operator's admin panel would reject on the way back in.
- **Product ids are derived, never random.** `productID` hashes the import id and
  the product's key, so re-uploading a corrected export addresses the same
  products and the drafts an operator approved are still found.
- **`FieldHandle` and `FieldSKU` are read and never written.** A handle is the
  product's URL. Rewriting it turns every link and every ranking that pointed
  at the old one into a 404 — that is a migration, and this package does not do
  migrations. `Field.Writable()` is the gate and `SaveDraft` refuses past it.
- **The server is the authority, the editor is a convenience.** `SaveDraft` puts
  whatever a client posted back through `Render` before storing it. A client
  that is not the editor, or an editor with a bug, cannot widen the brand's
  markup by posting here.
- **Only approved drafts are exported.** A drafted-but-unreviewed product is a
  suggestion, and a tool that shipped suggestions to a live storefront because
  somebody clicked "export" is a tool nobody could leave running.
- **A cache key says what it is an answer to.** `DraftVersion` composes the
  prompt constant, the model selection, the brand hash and the skill version.
  Research (task-87) gets its own key **without** the brand hash, so correcting
  the voice rewrites every description and throws away no competitor research.
- **The store is an interface here, not `*store.Store`.** `internal/store`
  imports this package for its row shapes; this package must never import it
  back. A nil store means every read misses and every write is a no-op.

### The rewrite half (task-87)

- **A model never sees markup here either, and that is what makes the guarantee
  hold on this path too.** `rewriteSchema` asks for a list of typed blocks
  carrying *text*. The envelope is chosen by `envelopeFor` from the store's own
  past HTML, so a paragraph the model added looks like this brand's other
  paragraphs — and a tag it asked for that the brand does not use is not
  refused, it never had a way to be requested.
- **The rewrite path uses `sourceURLs`, the operator path uses `operatorURLs`.**
  A person typed that link because they meant to; a model that produced one the
  product never had invented it. The two policies are the same function with a
  different allow-list and the difference is the whole point — pinned by
  `TestSanitize_UnderTheSourcePolicyRefusesAURLTheProductNeverHad`.
- **Two cache keys, and the brand hash is in exactly one.** `DraftVersion`
  carries the prompt constant, the model, the brand hash and the skill version.
  `researchVersion` carries the prompt constant and the model, and nothing else.
  What a competitor's page says about a category does not change because this
  store decided to address its readers as "siz", and throwing it away when the
  voice is edited would make correcting the voice the most expensive thing an
  operator can do. If a brand hash ever appears in the research key, the
  layer's main claim is gone.
- **A product's failure is that product's; a provider's failure is the pass's.**
  `isFatal` stops the whole run on `llm.ErrProviderUnavailable` — which
  `llm.ErrRateLimited` wraps. Carrying on would rediscover one fact once per
  remaining product: on a four-hundred-product catalog, four hundred subprocess
  launches to learn that the CLI is signed out. Measured against a real
  unauthenticated CLI, which is how this rule was found.
- **A pass that wrote nothing and cached nothing failed.** SD-6's own wording is
  "unless every source failed", and a green card above a catalog nobody touched
  is worse than a red one. A pass where everything came from the cache is the
  opposite case and is a success — that is the resumed run this design is for.
- **Rewrite is serial over products, deliberately.** Each product is a search, a
  crawl, a refine and a reason call, and all four already run behind bounded
  pools and the crawl politeness interval. A second fan-out would not make the
  work faster, only queue more of it behind the same doors, and it would scramble
  the narration — which is how an operator watches a run they cannot otherwise
  see.
- **`internal/catalog` still imports neither the pipeline nor the runner.**
  `Researcher` is stated in this package's own types because `internal/store`
  imports this package and `internal/pipeline` imports `internal/store`; the
  adapter lives in `internal/catalogjob`, where the two already meet. `Sink` is
  this package's own two-method shape for the same reason.

### The language half (task-103, task-105, task-107)

- **The operator's column map outranks the profile that matched.**
  `ColumnsFor` reads `File.Mapping` first and falls back to `Dialect.Columns`.
  `SetDialect` already said the converse — picking a profile clears a hand-made
  map, because the newer answer is the operator's — and for a long time this
  said nothing in the other direction: a correction saved over a recognised file
  was validated, stored and re-read, and then ignored on every read, so the
  "bir sütun yanlış eşlendiyse buradan düzeltin" form did nothing at all and
  said nothing about it. It **replaces** rather than merges, for the same reason
  `SetDialect` replaces: a field the operator cleared has to come back unmapped,
  and a merge would hand it back the profile's column. The undo is `SetDialect`
  with the same key, which re-binds from the header and drops the map.

- **A language is a dimension, not a kind of field.** `Field` is untouched and
  its wire strings are unchanged; `Lang` sits beside it and `LangField` pairs
  them. The composite spelling `description_html@ar` exists only on the wire,
  where the source language is still the bare field name so every column map an
  operator already saved keeps parsing. Inside the package it is a struct,
  because `Field` is matched by `switch` in a dozen places and every one of them
  ends in a `default` that ignores what it does not recognise — a composite
  `Field` value would fall silently through all of them.
- **`Dialect.Columns` is never retyped, only added to.** A `Dialect` is
  serialised whole into `catalog_imports.file_json`. Changing the shape of
  `Columns` fails `json.Unmarshal` on every stored row, and `Studio.List`
  swallows an unreadable row on purpose — so the symptom would be every
  operator's catalog screen quietly going empty, with no error anywhere.
- **A language is offered only when the file has a column for it.** `File.Langs`
  is the gate and `POST /catalog/rewrite` enforces it. A pass whose output has
  nowhere to be written is a pass the operator paid for and cannot export.
- **The language is a *suffix* on the draft key, and only for a target.** Every
  draft stored before languages existed is a source-language draft under a key
  with no suffix. Any other placement orphans all of them: the read returns no
  draft for a product still marked approved, and the export writes nothing for
  it. For the same reason the source-language prompt is byte-identical and
  `CatalogContentVersion` stayed at `content-v1` — the golden file is what makes
  bumping it a decision rather than an accident.
- **Direction is the language's, not the brand's and not the model's.**
  `Vocabulary` is counted from the store's own Turkish HTML, so `sanitizeAttrs`
  drops `dir` — silently, which is what makes it dangerous. `RenderLang` writes
  one `<div dir="rtl" lang="ar">` itself, carrying no class, no style and no
  visual opinion, and the vocabulary is **not** widened: widening it would let
  `dir` through on every element a model chose and would make the vocabulary a
  function of the target language rather than of the file. `stripDirectionWrapper`
  is why an operator's edit does not nest a second wrapper on every save — and
  it only matters when the brand's own HTML contains a `div`, which most do.
  It is called **only for an RTL language** (task-113): `RenderLang` puts a
  wrapper back for those and for nothing else, so stripping unconditionally was
  a one-way door — a brand whose own descriptions are wrapped in
  `<div dir="ltr">` has both the tag and the attribute in its own vocabulary,
  and an operator saving such a description unchanged watched it disappear.
- **`DeriveVocabulary` must never read a target-language column.** If it did, an
  exported Arabic body that was re-imported would teach the brand's vocabulary
  the `div` and the `dir` this package invented, permanently. It does not today
  because `Products()` fills `Original` from `Columns` only, and that is now
  load-bearing rather than incidental — there is a test.
- **The language gate is deterministic and it is the floor.** `CheckLanguage`
  makes no model call, for the reason `DeriveVocabulary` makes none. The
  reviewer pass is a second `llm.Reason` call on top of it, never instead of it:
  a draft the machine check refused is not stored because the reviewer could not
  be reached. It is shown what the gate found, which is what turns the second
  call from a coin flip into a repair.
- **The gate is model-facing; the operator is the authority.** On the rewrite
  path a fatal finding fails that product-language. On `SaveDraft` the findings
  are notes and the draft is stored: refusing a person's Arabic because a ratio
  heuristic disagreed would make the editor's save button silently do nothing —
  the same argument as `operatorURLs`. This paragraph described a code path that
  did not exist until task-113: `CheckLanguage` and `NormalizeForLang` were
  reached only from `rewrite.go`, so an operator's edit was stored without ever
  meeting the gate that `rewrite.go`'s own comment says it cannot skip. The same
  hole is why `stripDirectionWrapper` had no production caller and an operator's
  Arabic edit came back left-to-right.
- **`Voice.Address` stays Turkish.** It is an observation about Turkish text,
  read from the store's own descriptions. Deriving a per-language enum would
  mean deriving it from text that does not exist yet, and changing this one
  would move `BrandKit.hash` and discard every draft in the catalogue.
  `addressClause` renders it per language instead.
- **Which skills a catalog pass runs under depends on the card, not the agent.**
  `agents.CatalogSkills(lang)` exists because coderunner composes an *agent's*
  declared skills into one version string, and that string is half a draft's
  cache key. Declaring the two language files unconditionally would change the
  version a Turkish pass runs under and orphan every approved draft — task-101's
  bug arriving through a different door.

### A decision per language (task-113)

- **Approval is per language, because an export is.** `Studio.Export` resolves a
  version per language the file carries and collects each language's own
  approvals. Before this it resolved one — the source language's — so a target
  draft could be written, reviewed and approved and still had no way of reaching
  the file. The comment that said so is gone because the thing it apologised for
  is gone.
- **The source language stays in `catalog_products.status`; a target language
  lives in `catalog_product_langs`.** Same argument as the draft key: the
  language is a suffix, and only for a target. Every stored row is already a
  decision about the file's own language, so moving them would mean a backfill
  that leaves two homes for one fact, or rewriting the one `GROUP BY` the
  catalogs list is built on. The `CHECK (lang <> '')` is what keeps that true
  against a hand-written row.
- **The language branch lives in `internal/store` and nowhere else.** This
  package never branches on the language to read or write a status: two
  spellings of "where does a decision live" is the drift this file documents
  twice already.
- **Absence is pending.** A product nobody has judged in Arabic has no row, so
  every read is a `LEFT JOIN` and every count of what is waiting counts rows that
  are not there. An `INNER JOIN` would answer "nothing is waiting" for a
  catalogue nobody has touched.
- **A language appears in `CatalogStatusCounts` only once something has been
  decided in it.** `ListCatalogImports` leaves `file_json` on disk on purpose, so
  the catalogs list cannot know which languages a file carries; synthesising a
  pending count per language would draw an Arabic chip over a Shopify export with
  no Arabic column, which is the opposite of what `File.Langs` exists to say.
- **`rewriteOne` writes status for the language it wrote, both times.** It used
  to write `StatusFailed` and `StatusDrafted` without one, so an Arabic product
  the language gate refused marked the *Turkish* product failed, overwrote its
  reason with a complaint about another language, and took it out of the
  operator's approved filter. The first of those destroys a decision, not a
  suggestion.

### Every language's decision in one read

- **`Products` reads the decision table once for the page, and composes the
  source language itself.** A screen that draws one status column per language
  needs them all at once; asking per language is what made the language a *mode*
  on that screen, and a mode that re-reads the same rows is a control whose
  effect an operator cannot see. `CatalogProductLangStatuses` is the listing's
  second store read, the same shape `Outputs` already uses — a read per product
  would be the 1+N fan-out task-80 removed from the board.
- **The source language is not in that read, and is not invented into it.** It
  lives on `catalog_products`, for the reason written twice above: every status
  stored before languages existed is a decision about the file's own language.
  `statusesOf` seeds the map from the row and overlays the table, so there is
  still exactly one place that knows where a decision lives.
- **Absence stays absence.** A target language nobody has decided in has no
  entry, and the client reads a missing key as pending. Filling one in for every
  language this binary can write would put an Arabic reading on a file with no
  Arabic column — the claim `File.Langs` exists to prevent, arriving through the
  wire shape instead of through a count.
- **`Product` fills the same map as `Products`.** A field that is populated on
  the listing and empty on the single read is a contract that lies to whichever
  caller meets the second one first.

### The storefront scan (task-116)

- **The browser resolves the cascade; this package never parses CSS.** The probe
  runs inside the crawler's own browser and reports `getComputedStyle` values.
  Fetching stylesheets and parsing them here would be a second CSS
  implementation — wrong about specificity, custom properties, media queries and
  `@import` on the first real theme it met — and every stylesheet would be
  another outbound fetch, which is another SSRF surface for no gain. That
  `js_code` runs and that its answer can be read back off the document element
  were both **verified against the pinned Crawl4AI image**, not assumed;
  `crawl.FetchOptions.JSCode`'s zero value reproduces the old request byte for
  byte, and there is a test that says so.
- **Computed values travel; markup does not.** The first design took the
  description container's outer HTML as a "mockup" to wrap the body in. It could
  not have worked: those class names are inert without the theme's stylesheet,
  the preview frame loads no external stylesheet (the app's CSP allows none and
  a `srcdoc` frame inherits it), and copied markup is markup this package would
  then own, sanitise and store. Colour, font stack, size, line-height and
  measure are small, portable, and are what actually makes a preview look like
  the shop.
- **The scan is beside the brand kit, never inside it.** `Import.Site` is its own
  field and its own column because `BrandKit.hash` is half of a draft's cache
  key. A colour folded into that hash would make re-measuring a shop discard
  every approved draft in the catalogue — the same argument `researchVersion`
  makes about the brand hash, arriving through a different door. `ScanSite` also
  writes with `PutCatalogImport` rather than `save`: `save` rebuilds every
  product row and resets each to pending, so measuring a colour would have
  thrown away the catalogue's decisions.
- **The vocabulary is the file's, and a storefront is not evidence about it.**
  `ScanSite` does not touch `Brand` at all. Widening `Render`'s allow-list from a
  live page would let a rewrite emit markup the export never had, which is the
  guarantee this whole package is built on. There is a test.
- **The address is checked before the fetch and the landing is checked after.**
  An operator types the URL, so it is untrusted input that becomes an outbound
  request from a container with a route to the host's network: scheme allowlist,
  then every address the host resolves to, refusing loopback, private,
  link-local, unspecified and RFC 6598's `100.64.0.0/10` — that last one because
  Go reports it as global unicast and not private, so the four stdlib predicates
  wave it through.
  The crawler then **follows redirects**, which is verified behaviour, so the
  address that was approved is not necessarily the page that was read. Both
  fetches carry a landing probe and both are re-judged by the same rule. This
  cannot stop the request being made — only the crawler could — and what it
  stops is the answer being parsed, stored or shown. The residual is DNS
  rebinding between the check and the crawler's own resolution, which is written
  down in the security report rather than pretended away.
- **A failed scan stores nothing.** Not an empty theme: a preview painted from a
  half-read scan would tell an operator their shop is a blank white page.
  `SiteScan.Usable()` is the gate, and the preview keeps its own readable
  default whenever it is false.

### The outputs listing (task-114)

- **The caller composes the version; the store never does.** `DraftFilter.Keys`
  is a list of `(import, version, language)` triples joined as an inline table.
  A store that composed a version would be the second implementation of a format
  that has already drifted here once.
- **The constraint is on the pair.** `AND p.import_id = k.import_id` is what
  keeps a superseded draft out: one version can be current for one import and
  stale for another, because two imports share a brand hash until one of their
  voices is edited. `version IN (…)` is wrong in exactly that case and in no
  other, which is why it is written down and pinned by a test.
- **Two store reads, whatever the catalogue looks like.** The imports, then the
  drafts. Version composition in between is pure Go over what is already in
  memory.
- **`OutputFilter.Langs` empty means every language, not the source one.** It is
  the only filter in this package where the zero `Lang` cannot double as "unset",
  because `""` is a language here and a real answer.
- **A page is never a nil slice.** A nil slice serialises as `null`, and a client
  promised a list reads `.length` off it and takes the screen down — which is how
  this was found.

### The field configuration (task-111)

- **A product export does not have a fixed field set, so the field set is read
  off the file.** `File.Offered` is writable ∧ this file has a column for it.
  The real exports say why: this store's IKAS product export has a store-named
  `Satış Kanalı:meletiorient` column, an empty SKU and a custom `Html:Detay-AR`
  body; the custom-fields export has a title and one body and nothing else.
  Offering a switch for a field with nowhere to write is offering a switch that
  does nothing.
- **An empty configuration is "not configured", not "nothing".** It means
  everything the file offers, which is what this package did before the
  configuration existed. `SetWrite` therefore refuses a selection that
  normalises to empty — storing it would silently turn every switch back on.
- **The configuration is a gate, not a default.** `Rewrite` intersects the
  card's field list with it, so a card written before a switch was flipped
  cannot write past it, and the pass says in its notes what it did not touch and
  why. A green card above a file that did not change is the failure this avoids.
- **A field the file cannot carry is dropped from the configuration, not
  refused.** The configuration outlives the export it was made against:
  re-exporting with one column removed should give the operator their other
  switches back, not an error about a column they did not remove on purpose.

### What a pass refuses before it spends anything (task-112)

- **A pass that needs structured output says so at its own front door.**
  `internal/llm`'s router already refuses a schema-carrying request to a
  provider that cannot serve one, and its comment says "before anything is
  spent" — which is true of *that* package and false of this one. Every product
  here is a search, a crawl, a refine and *then* the schema call, so an operator
  on `ollama/qwen3:8b` waited **six minutes**, watched a crawl run, and got one
  product marked `failed` with a sentence about JSON. `checkStructured` runs
  beside the `ErrNothingToWrite` gate, before the product loop, and it fails the
  whole pass rather than the product: the answer is the same for every remaining
  one, and rediscovering it four hundred times is task-87's `isFatal` argument
  arriving through a different door.
- **The capability is asked for through the same resolution the call uses.**
  `llm.Router.Capabilities` is `resolve` plus `Capabilities()`, and `resolve` is
  what `CompleteWith` calls. A check that resolved differently from the call it
  is checking would be worse than no check, because it would clear a pairing the
  call then refuses.
- **`CapabilityReader` is optional, and a Completer that cannot answer is taken
  at its word.** This package's tests pass a counting stub; a gate that failed
  closed against one would make every test in the file a capability test.

### Translation exports whose language is not in the file (task-111)

- **`Dialect.TargetColumns` is a translation surface whose language the file
  does not record.** IKAS's Çeviriler export is the case: its header is
  "İsim, Açıklama, … , Çevrilecek İsim, Çevrilecek Açıklama, …" and nothing in
  it says what "Çevrilecek" was translated into — the operator picked the
  language in the admin panel when they pressed export.
- **This package asks rather than detects.** Detecting the language from the
  content would be a language detector this package does not have and should not
  grow, and detecting it wrong writes Arabic into the German column across every
  row of the file. `File.PendingTarget` is the state; `SetTargetLang` is the
  answer, and it is the operator's.
- **`Çevrilecek Meta Slug` is not mapped.** It is the product's URL, `FieldHandle`
  is read and never written, and the real export has it empty on all 1013 rows —
  IKAS does not translate a slug either.
- **Profile order is load-bearing.** `ikas-fields`' signature is a subset of the
  variant custom-fields header, so `ikas-fields-variant` has to come first;
  detection takes the first match and the product-level profile would silently
  drop the variant identity that file carries.

## Testing

No database and no model. `testdata/` holds seven exports whose **header rows
are copied verbatim from real ones** — and whose line endings are a real
export's too: two of them carried a stray `\r\r\n` for a while, which is a
sequence no exporter emits, and it kept them out of the byte-identity test
rather than being noticed. `TestExport_EveryRealExportShapeComesBackByteForByte`
now runs over all seven — a Shopify one with variant rows and
CRLF, an IKAS product export (comma, UTF-8 BOM, CRLF, every cell quoted), the
same shape re-saved as Windows-1254, an IKAS export with two variant rows under
one group id, IKAS's custom-fields export, its variant-level custom-fields
export, and IKAS's Çeviriler export (the translation surface, whose target
columns are named "Çevrilecek …" and whose language the file does not record). The lossless claim is asserted
against those rather than against a hand-written three-line fixture, because a
hand-written fixture is a fixture somebody wrote to pass.

The header row is the part that must be real, and task-91 is why the rule is
written down: the IKAS profile shipped in task-85 was written from memory
(`Ürün Adı`, `Stok Kodu`, `Kategori`), passed its own fixture, and matched
nothing anyone actually downloads. **A dialect profile is only ever added from
an export somebody has in front of them** — the data rows may be invented, the
column names may not.

`fakeCompleter` counts calls: "this pass costs no model call" is only worth
claiming if something is counting. `memStore` is the persistence, in memory —
what this package enforces is a contract about content, and a contract about
content should not need a schema to demonstrate.

## Reviewer focus

- Is there a path by which markup reaches a model, or by which model output
  reaches HTML without passing `Render`? Either one and the package's claim is
  gone.
- Does `Render` **drop** what is outside the vocabulary, or pass it through?
- Is the byte-identity test run over a real export, or over three lines somebody
  wrote to pass it? Is it run over **every** fixture, or over the subset that
  happens to pass?
- Can anything reach an SKU, a group id or a handle on the way out?
  `TestExport_NeverWritesIdentity` is the pin: those columns must be identical
  before and after a pass that rewrote every writable field.
- Does an operator's saved column map still change what is read on a file a
  profile matched?
- `DeriveVocabulary` — does anything in its call graph touch `s.llm`?
- Does the brand hash change when the voice is edited, and *not* change when a
  product is added? Both directions matter: the first keeps stale copy out, the
  second keeps a growing catalog from invalidating everything nightly.
- Any import of `internal/store`, `internal/pipeline` or `internal/coderunner`
  from here is a cycle or a layering mistake — the first two the compiler finds,
  the third it does not.
- Does the brand hash appear anywhere in `researchVersion`? That is the one
  change that would quietly undo the layer's main claim.
- Does anything but `llm.ErrProviderUnavailable` stop a whole pass? A product
  that failed for its own reason must not take the other three hundred with it.
- Is the source-language rewrite prompt still byte-identical to the golden? If
  it is not, `CatalogContentVersion` has to move, and moving it discards every
  draft an operator has approved.
- Does `RenderLang` still return exactly `Render`'s bytes for a left-to-right
  language, and does the vocabulary still refuse `dir` everywhere except the one
  wrapper this package writes?
- Does `DeriveVocabulary` still read `Columns` only? A path from a target-language
  column into the vocabulary widens the brand's markup with our own invention.
- Does `CheckLanguage` touch `s.llm` anywhere in its call graph?
- Is the language still a suffix on the draft key, and still absent from the
  source language's?
- Does anything decide what a file can rewrite from a constant rather than from
  the file's own columns?
- Does an empty field configuration still mean "everything"? Reading it as
  "nothing" turns every untouched import into a pass that changes no field.
- Is the configuration still intersected with the card's fields rather than only
  used as a default when the card names none?
- Does anything guess which language a translation export's target columns hold?
- Does a pass reach its first search before it has checked that the selected
  model can return the typed blocks the whole pass is built out of?
- Does anything outside `internal/store` branch on the language to decide where
  a status lives?
- Does `Studio.Export` still produce the same bytes for a single-language file,
  and does approving the source language still ship nothing in a target one?
- Does `sanitize` still return `Render`'s exact bytes for the source language?
- Does the outputs listing read a file body, or a product per import?
- Does the HTTP view still report the field list, or has `File.WriteSet` gone
  back to being dead code? The symptom of its absence was not a missing feature:
  it was every screen saying "hiçbir alan yazılmayacak" over a pass that then
  wrote all five fields.
