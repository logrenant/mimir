package catalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/logrenant/mimir/internal/llm"
)

// The operations the HTTP routes and the MCP tools both call. They are here
// rather than in internal/api because a handler is a door, not a floor: it
// translates JSON to one of these and back, and nothing else.

// Get returns one import, rebuilt from its row.
func (s *Studio) Get(ctx context.Context, id string) (Import, error) {
	if s.store == nil {
		return Import{}, ErrUnknownImport
	}
	row, ok, err := s.store.GetCatalogImport(ctx, id)
	if err != nil {
		return Import{}, err
	}
	if !ok {
		return Import{}, fmt.Errorf("%w: %s", ErrUnknownImport, id)
	}
	return row.value()
}

// List returns imports newest first, without their file bodies.
func (s *Studio) List(ctx context.Context, limit int) ([]Import, error) {
	if s.store == nil {
		return nil, nil
	}
	rows, err := s.store.ListCatalogImports(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Import, 0, len(rows))
	for _, r := range rows {
		imp, err := r.value()
		if err != nil {
			// One unreadable row is not a reason to have no list. It is left
			// out and the rest of the screen still opens (SD-6).
			continue
		}
		out = append(out, imp)
	}
	return out, nil
}

// StatusCounts is how many products each import holds in each status.
//
// It exists for the catalogs list, which says what state each file is in
// ("1 taslak · 50 bekliyor") rather than only how many rows it has. One query
// covers every import: a read per row is the fan-out this codebase keeps
// removing, and the list is polled.
//
// A store that cannot answer yields nothing rather than an error. The list is
// still a list without the counts — that is SD-6, and it is the difference
// between a screen that degrades and a screen that refuses to open.
func (s *Studio) StatusCounts(ctx context.Context) map[string]map[Lang]map[string]int {
	if s.store == nil {
		return nil
	}
	counts, err := s.store.CatalogStatusCounts(ctx)
	if err != nil {
		return nil
	}
	return counts
}

// Delete removes an import and everything derived from it.
func (s *Studio) Delete(ctx context.Context, id string) error {
	if s.store == nil {
		return nil
	}
	return s.store.DeleteCatalogImport(ctx, id)
}

// SetMapping records the operator's own column map for a file no profile
// matched, and re-derives the products and the brand kit under it.
//
// Re-deriving is the point: before the mapping there was no title column and
// therefore no titles, so an import that has just been mapped has to be read
// again to become anything at all.
func (s *Studio) SetMapping(ctx context.Context, id string, mapping map[LangField]string, sel llm.Selection) (Import, error) {
	imp, err := s.Get(ctx, id)
	if err != nil {
		return Import{}, err
	}
	clean := map[Field]string{}
	locales := map[Lang]map[Field]string{}
	for lf, col := range mapping {
		if col == "" {
			continue
		}
		if !knownField(lf.Field) {
			return Import{}, fmt.Errorf("catalog: unknown field %q", lf.Field)
		}
		if !KnownLang(lf.Lang) {
			return Import{}, fmt.Errorf("catalog: unknown language %q", lf.Lang)
		}
		if imp.File.headerIndex(col) < 0 {
			return Import{}, fmt.Errorf("catalog: the file has no column named %q", col)
		}
		if lf.Lang == LangSource {
			clean[lf.Field] = col
			continue
		}
		if locales[lf.Lang] == nil {
			locales[lf.Lang] = map[Field]string{}
		}
		locales[lf.Lang][lf.Field] = col
	}
	// The completeness test is the source language's alone. A file is readable
	// when this package can find a title or a description in it; a target
	// language is an addition to a readable file, never the thing that makes
	// one readable, and requiring one would refuse every single-language
	// import there has ever been.
	if !enoughToRead(clean) {
		return Import{}, ErrMappingIncomplete
	}
	imp.File.Mapping = clean
	imp.File.Translations = nil
	if len(locales) > 0 {
		imp.File.Translations = locales
	}
	return s.reread(ctx, imp, sel)
}

// SetDialect records the profile an operator picked, overriding detection, and
// re-reads the file under it.
//
// Detection is a subset match on a signature, so it answers "which platform
// wrote this" and not "which platform is this store on". A store that renamed a
// column, or a platform that changed one, produces a file the operator can see
// is an IKAS export while Detect cannot. Before this, their only way out was
// the manual column form — retyping a map that already exists in the table.
//
// An empty key clears the choice and returns the file to detection, which is
// how an operator undoes a wrong pick without re-uploading.
//
// The profile is bound to the file's own header spelling, exactly as Detect
// binds it. Writing the table's spelling instead would export a column name the
// file never had, and the operator's admin panel would refuse the result.
func (s *Studio) SetDialect(ctx context.Context, id, key string, sel llm.Selection) (Import, error) {
	imp, err := s.Get(ctx, id)
	if err != nil {
		return Import{}, err
	}

	if strings.TrimSpace(key) == "" {
		imp.File.Dialect = Dialect{}
		if d, ok := Detect(imp.File.Header); ok {
			imp.File.Dialect = d
		}
		return s.reread(ctx, imp, sel)
	}

	d, ok := bindDialect(key, imp.File.Header)
	if !ok {
		return Import{}, fmt.Errorf("%w: %q", ErrUnknownDialect, key)
	}
	if len(d.Columns) == 0 {
		return Import{}, fmt.Errorf(
			"%w: %q reads none of this file's columns", ErrMappingIncomplete, d.Name)
	}
	imp.File.Dialect = d
	// A profile and a hand-made map are two answers to the same question, and
	// the one the operator just picked is the newer one.
	imp.File.Mapping = nil
	return s.reread(ctx, imp, sel)
}

// SetWrite records which fields a rewrite may change on this import.
//
// It exists because a product export does not have a fixed field set. This
// store's export has a custom Arabic body and no SKU filled in; the next one
// has an SEO description and no Arabic at all. A tool that assumed a shape
// would either refuse files it can read or quietly write into columns the
// merchant did not want touched — and the second is the expensive one, because
// it ships.
//
// The configuration is stored on the file beside the operator's column map,
// which is the same kind of answer about the same file, and it survives every
// re-read and every rewrite. An empty set is not "nothing selected": it is "not
// configured", and it means everything the file offers, which is what this tool
// did before the configuration existed.
//
// A field the file cannot carry is dropped rather than refused. The
// configuration outlives the export it was made against — re-exporting with one
// column removed should give the operator their other switches back, not an
// error about a column they did not remove on purpose.
func (s *Studio) SetWrite(ctx context.Context, id string, want []LangField) (Import, error) {
	imp, err := s.Get(ctx, id)
	if err != nil {
		return Import{}, err
	}
	for _, lf := range want {
		if !knownField(lf.Field) || !KnownLang(lf.Lang) {
			return Import{}, fmt.Errorf("catalog: unknown field %q", lf.String())
		}
		if !lf.Field.Writable() {
			return Import{}, fmt.Errorf("%w: %s", ErrNotWritable, lf.Field)
		}
	}
	clean := normalizeWrite(imp.File, want)
	if len(want) > 0 && len(clean) == 0 {
		return Import{}, ErrNothingToWrite
	}
	imp.File.Write = clean

	// Stored, not re-read. This changes nothing about how the file is *read*,
	// and re-deriving a thousand products and a brand kit because somebody
	// flipped a switch would make the panel feel broken.
	if s.store != nil {
		if err := s.store.PutCatalogImport(ctx, imp.stored()); err != nil {
			return Import{}, err
		}
	}
	return imp, nil
}

// SetTargetLang answers the question IKAS's translations export cannot answer
// about itself: which language its "Çevrilecek …" columns hold.
//
// The operator picked it in the admin panel when they pressed export and the
// file came back without it. Detecting it from the content would be a language
// detector this package does not have and should not grow, and detecting it
// wrong writes Arabic into the German column — silently, into a thousand rows.
func (s *Studio) SetTargetLang(ctx context.Context, id string, lang Lang, sel llm.Selection) (Import, error) {
	imp, err := s.Get(ctx, id)
	if err != nil {
		return Import{}, err
	}
	if !KnownLang(lang) {
		return Import{}, fmt.Errorf("catalog: unknown language %q", lang)
	}
	if len(imp.File.Dialect.TargetColumns) == 0 {
		return Import{}, ErrNoTargetColumns
	}
	imp.File.TargetLang = lang
	// This one *is* a re-read: the target columns only become readable now, so
	// every product's translated content has to be picked up out of them.
	return s.reread(ctx, imp, sel)
}

// Reread rebuilds an import's products and brand kit under the profile table as
// it stands now.
//
// It exists because a dialect profile is code: one added after a file was
// uploaded reads that file correctly, but the products stored at upload time
// were built with no columns and are still blank. StoredImport.value re-detects
// so the screen stops saying "unrecognised"; this is the other half, and it is
// an explicit operation rather than a side effect of a read — a GET that
// rewrites a thousand rows is a GET nobody can reason about.
func (s *Studio) Reread(ctx context.Context, id string, sel llm.Selection) (Import, error) {
	imp, err := s.Get(ctx, id)
	if err != nil {
		return Import{}, err
	}
	if !imp.File.Readable() {
		return Import{}, ErrMappingIncomplete
	}
	return s.reread(ctx, imp, sel)
}

// reread is the shared half: before a mapping there was no title column and
// therefore no titles, and the same is true of a profile that did not exist
// yet, so both paths have to read the file again rather than patch a row.
func (s *Studio) reread(ctx context.Context, imp Import, sel llm.Selection) (Import, error) {
	products := Products(imp.ID, imp.File)
	imp.ProductCount = len(products)
	imp.Brand = s.DeriveBrand(ctx, imp.File, products, sel)

	if err := s.save(ctx, imp, products); err != nil {
		return Import{}, err
	}
	return imp, nil
}

// SetBrand stores the operator's edit of the brand kit.
//
// The vocabulary is not theirs to widen: it is derived from their own past
// HTML and it is the gate every rewrite is measured against, so an edit that
// arrived carrying one is ignored rather than obeyed. The voice is theirs
// entirely — internal/settings' argument, applied here.
func (s *Studio) SetBrand(ctx context.Context, id string, voice Voice) (Import, error) {
	imp, err := s.Get(ctx, id)
	if err != nil {
		return Import{}, err
	}
	checked, verr := validateVoice(voice)
	if verr != nil {
		return Import{}, verr
	}
	imp.Brand.Voice = checked
	imp.Brand.VoiceNote = ""
	imp.Brand.Version = imp.Brand.hash(s.cfg.CatalogBrandVersion)

	if s.store != nil {
		if err := s.store.PutCatalogImport(ctx, imp.stored()); err != nil {
			return Import{}, err
		}
	}
	return imp, nil
}

// RescanBrand re-derives both halves from the file already stored.
func (s *Studio) RescanBrand(ctx context.Context, id string, sel llm.Selection) (Import, error) {
	imp, err := s.Get(ctx, id)
	if err != nil {
		return Import{}, err
	}
	products := Products(imp.ID, imp.File)
	imp.Brand = s.DeriveBrand(ctx, imp.File, products, sel)
	if s.store != nil {
		if err := s.store.PutCatalogImport(ctx, imp.stored()); err != nil {
			return Import{}, err
		}
	}
	return imp, nil
}

// ProductView is one product with whatever has been written for it.
type ProductView struct {
	StoredProduct
	Draft *StoredDraft `json:"draft,omitempty"`
	// Vocabulary travels with the product because the editor on the other end
	// has to be constrained by it. A client that had to fetch the import
	// separately to know which tags are allowed would be a client that
	// sometimes does not.
	Vocabulary Vocabulary `json:"vocabulary,omitzero"`
}

// Products lists a page of an import's products with their current drafts.
func (s *Studio) Products(ctx context.Context, f ProductFilter, version string) ([]ProductView, error) {
	if s.store == nil {
		return nil, nil
	}
	if f.Limit <= 0 || f.Limit > s.cfg.CatalogProductsPageMax {
		f.Limit = s.cfg.CatalogProductsPageMax
	}
	if f.Status != "" && !ValidStatus(f.Status) {
		return nil, fmt.Errorf("catalog: unknown status %q", f.Status)
	}
	if !KnownLang(f.Lang) {
		return nil, fmt.Errorf("catalog: unknown language %q", f.Lang)
	}

	rows, err := s.store.ListCatalogProducts(ctx, f)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	drafts, err := s.store.GetCatalogDrafts(ctx, ids, version)
	if err != nil {
		return nil, err
	}
	// One read for the whole page, not one per product: the decision table is
	// the second of this listing's two reads, the same shape Outputs uses.
	decided, err := s.store.CatalogProductLangStatuses(ctx, ids)
	if err != nil {
		return nil, err
	}

	out := make([]ProductView, 0, len(rows))
	for _, r := range rows {
		v := ProductView{StoredProduct: r}
		v.Statuses = statusesOf(r, decided[r.ID])
		if d, ok := drafts[r.ID]; ok {
			copied := d
			v.Draft = &copied
		}
		out = append(out, v)
	}
	return out, nil
}

// statusesOf composes one product's decisions across every language.
//
// The source language is read off the row rather than out of the decision
// table, because that is where it lives: every status stored before languages
// existed is a decision about the file's own language, and moving them would
// leave two homes for one fact.
func statusesOf(r StoredProduct, decided map[Lang]string) map[Lang]string {
	source := r.Status
	if r.Lang != LangSource {
		source = r.SourceStatus
	}
	out := make(map[Lang]string, len(decided)+1)
	out[LangSource] = source
	for lang, status := range decided {
		out[lang] = status
	}
	return out
}

// Product returns one product, its draft, and the vocabulary any edit of it is
// held to.
func (s *Studio) Product(ctx context.Context, productID string, lang Lang, version string) (ProductView, error) {
	if s.store == nil {
		return ProductView{}, ErrUnknownProduct
	}
	row, ok, err := s.store.GetCatalogProduct(ctx, productID, lang)
	if err != nil {
		return ProductView{}, err
	}
	if !ok {
		return ProductView{}, fmt.Errorf("%w: %s", ErrUnknownProduct, productID)
	}
	view := ProductView{StoredProduct: row}

	// Propagated, unlike the two reads below it. Those degrade because what
	// they add is context — a vocabulary the editor can do without, a draft
	// that may genuinely not exist. This one answers "where does this product
	// stand", and a read that failed would report every language except the one
	// asked for as undecided: an operator would see copy they approved in
	// Arabic offered to them as untouched.
	decided, err := s.store.CatalogProductLangStatuses(ctx, []string{productID})
	if err != nil {
		return ProductView{}, err
	}
	view.Statuses = statusesOf(row, decided[productID])

	if imp, err := s.Get(ctx, row.ImportID); err == nil {
		view.Vocabulary = imp.Brand.Vocab
	}
	if d, ok, err := s.store.GetCatalogDraft(ctx, productID, version); err == nil && ok {
		view.Draft = &d
	}
	return view, nil
}

// SaveDraft records the operator's own edit.
//
// Every field is put back through Render against the product's own vocabulary
// before it is stored, for the same reason a rewrite is: the editor on the
// other end is a convenience, and the server is the authority. An editor with
// a bug, or a client that is not the editor at all, cannot widen the brand's
// markup by posting here.
func (s *Studio) SaveDraft(
	ctx context.Context, productID, version string, lang Lang, in Content, fields []Field,
) (StoredDraft, error) {
	if s.store == nil {
		return StoredDraft{}, ErrUnknownProduct
	}
	if !KnownLang(lang) {
		return StoredDraft{}, fmt.Errorf("catalog: unknown language %q", lang)
	}
	row, ok, err := s.store.GetCatalogProduct(ctx, productID, lang)
	if err != nil {
		return StoredDraft{}, err
	}
	if !ok {
		return StoredDraft{}, fmt.Errorf("%w: %s", ErrUnknownProduct, productID)
	}
	imp, err := s.Get(ctx, row.ImportID)
	if err != nil {
		return StoredDraft{}, err
	}

	content, notes, err := s.sanitize(in, row.Product, imp.Brand, lang, operatorURLs)
	if err != nil {
		return StoredDraft{}, err
	}

	names := make([]string, 0, len(fields))
	for _, f := range fields {
		if !f.Writable() {
			return StoredDraft{}, fmt.Errorf("%w: %s", ErrNotWritable, f)
		}
		names = append(names, string(f))
	}

	d := StoredDraft{
		ProductID: productID, Version: version,
		Content: content, Fields: names, Notes: notes,
		CreatedAt: s.now().UTC(),
		// This is the flag that stops a later bulk pass from overwriting it.
		EditedByOperator: true,
	}
	if err := s.store.PutCatalogDraft(ctx, d); err != nil {
		return StoredDraft{}, err
	}
	// Only the language this edit was in. An operator fixing the Arabic must
	// not move the Turkish copy out of whatever the reviewer left it in.
	if err := s.store.SetCatalogProductStatus(ctx, productID, lang, StatusDrafted, "", s.now()); err != nil {
		return StoredDraft{}, err
	}
	return d, nil
}

// urlPolicy decides which links survive sanitizing.
//
// The two callers of sanitize are not the same and must not be treated as
// though they were. A rewrite is model output, and a URL it produced that was
// not in the source was invented — a broken link the merchant would ship to
// customers. An operator's edit is a person's decision: they typed that link
// because they meant to, and refusing it would make the editor's link button a
// button that silently does nothing, which is the exact failure this package
// says it exists to prevent.
type urlPolicy int

const (
	// sourceURLs allows only what the product's own description already
	// contained. The posture for anything a model wrote.
	sourceURLs urlPolicy = iota
	// operatorURLs trusts the submission's own links.
	operatorURLs
)

// sanitize is the one gate every stored draft passes, whoever wrote it. What
// differs between callers is the url policy, and nothing else.
//
// The language it is sanitizing FOR is not a detail. Three things went wrong
// while this function did not take one, and all three were silent:
//
//   - It rendered through Render rather than RenderLang, so the one
//     `<div dir="rtl" lang="ar">` this package writes was parsed into an
//     envelope on the way in and dropped by the vocabulary on the way out. An
//     operator correcting an Arabic description saved it left to right and
//     nothing anywhere said so. stripDirectionWrapper was written for exactly
//     this and had no caller.
//   - It compared against p.Original — the Turkish body — so an Arabic edit's
//     allowed-URL set came from a document it has nothing to do with, and a
//     link the merchant already shipped in the Arabic cell survived only if
//     the operator happened to re-type it.
//   - It never reached the language gate, although rewrite.go's own comment
//     claims the gate "runs on this path and on the operator's, so there is no
//     way to store a draft that skipped it".
//
// The gate's findings are notes here and never an error. That is the same rule
// operatorURLs states: refusing a person's Arabic because a ratio heuristic
// disagreed would make the editor's save button silently do nothing.
func (s *Studio) sanitize(
	in Content, p Product, kit BrandKit, lang Lang, policy urlPolicy,
) (Content, []string, error) {
	out := in
	var notes []string

	if strings.TrimSpace(in.DescriptionHTML) != "" {
		// The document this edit is an edit OF, which is the target language's
		// own cell when there is one.
		src, err := ParseHTML(p.Content(lang).DescriptionHTML)
		if err != nil {
			return Content{}, nil, err
		}
		edit, err := ParseHTML(in.DescriptionHTML)
		if err != nil {
			return Content{}, nil, err
		}
		allowed := src.URLs
		if policy == operatorURLs {
			// Their own links, plus whatever the product already had — an edit
			// that left an existing link alone must not lose it either.
			allowed = map[string]bool{}
			for u := range src.URLs {
				allowed[u] = true
			}
			for u := range edit.URLs {
				allowed[u] = true
			}
		}
		// Strip before rendering, add back inside RenderLang. Without the
		// strip, every save of an RTL draft nests one wrapper deeper than the
		// last — and it only shows up when the brand's own HTML contains a
		// div, which most do.
		//
		// Only for a language that gets one back. RenderLang writes a wrapper
		// for an RTL language and nothing for any other, so stripping
		// unconditionally is a one-way door: a brand whose own descriptions are
		// wrapped in `<div dir="ltr">` has both the tag and the attribute in its
		// vocabulary, so that wrapper survives Render today — and an operator
		// opening such a description and saving it unchanged would have watched
		// it disappear from the draft and then from the export.
		blocks := edit.Blocks
		if lang.RTL() {
			blocks = stripDirectionWrapper(blocks)
		}
		blocks, repairs := NormalizeBlocks(blocks, lang)
		rendered := RenderLang(blocks, kit.Vocab, allowed, lang)
		if rendered != in.DescriptionHTML {
			notes = append(notes, "Açıklama markanın etiket sözlüğüne göre sadeleştirildi.")
		}
		notes = appendFindings(notes, repairs)
		out.DescriptionHTML = rendered
	}

	if n := clampRunes(&out.SEOTitle, s.cfg.CatalogSEOTitleMaxChars); n {
		notes = append(notes, fmt.Sprintf("SEO başlık %d karaktere kısaltıldı.", s.cfg.CatalogSEOTitleMaxChars))
	}
	if n := clampRunes(&out.SEODescription, s.cfg.CatalogSEODescMaxChars); n {
		notes = append(notes, fmt.Sprintf("SEO açıklama %d karaktere kısaltıldı.", s.cfg.CatalogSEODescMaxChars))
	}
	out.Title = strings.Join(strings.Fields(out.Title), " ")

	// The plain-text repairs, and then the deterministic gate, over what is
	// actually going to be stored. Only for a target language: the source
	// language is the one this package has no opinion about.
	if lang != LangSource {
		var repairs []LangFinding
		out, repairs = NormalizeForLang(out, lang)
		notes = appendFindings(notes, repairs)
		report := CheckLanguage(out, p.Content(LangSource), lang, s.cfg, nil)
		notes = appendFindings(notes, report.Findings)
	}
	return out, notes, nil
}

// appendFindings turns the gate's findings into the sentences an operator
// reads. They are notes, never a refusal — see sanitize.
func appendFindings(notes []string, found []LangFinding) []string {
	for _, f := range found {
		if f.Note == "" {
			continue
		}
		notes = append(notes, f.Note)
	}
	return notes
}

func clampRunes(s *string, max int) bool {
	r := []rune(strings.Join(strings.Fields(*s), " "))
	if len(r) <= max {
		*s = string(r)
		return false
	}
	*s = strings.TrimSpace(string(r[:max]))
	return true
}

// SetStatus records a decision on one product, in one language.
//
// A decision is per language because an export is: approving the Turkish copy
// must not ship an Arabic one nobody read, and approving the Arabic must not
// re-open the Turkish. The store decides where the row lives; this layer never
// branches on the language to read or write one.
func (s *Studio) SetStatus(ctx context.Context, productID string, lang Lang, status, reason string) error {
	if s.store == nil {
		return nil
	}
	if !ValidStatus(status) {
		return fmt.Errorf("catalog: unknown status %q", status)
	}
	if !KnownLang(lang) {
		return fmt.Errorf("catalog: unknown language %q", lang)
	}
	err := s.store.SetCatalogProductStatus(ctx, productID, lang, status, reason, s.now())
	if err != nil && strings.Contains(err.Error(), "no rows") {
		return fmt.Errorf("%w: %s", ErrUnknownProduct, productID)
	}
	return err
}

// Outputs lists generated content across every import at once.
//
// It is the screen's answer to a question no per-import read can answer: what
// has been written lately, in which language, and does it still need somebody
// to look at it. Filtering by platform profile only means anything here, which
// is why this listing is not a fourth tab inside one file.
//
// Exactly two store reads, whatever the catalogue looks like: the imports, and
// the drafts. The version composition in between is pure Go over what is
// already in memory — one call per import per language, no I/O — because a
// version is this package's own cache key and asking the store to resolve one
// would put a second implementation of that format in a second package.
//
// Changed is computed here rather than by the caller. Four reasons and the
// first is enough on its own: a client that diffed for itself would need the
// original, which means shipping every product's description HTML beside every
// draft's. The others are that changedNames already IS the definition and a
// second implementation of it is the drift this package has had once; that the
// comparison is language-aware in a way a client gets backwards (an Arabic
// draft equal to the Turkish cell is a change, per Export); and that the set of
// writable fields is closed and lives in Go.
func (s *Studio) Outputs(ctx context.Context, f OutputFilter) (OutputPage, error) {
	limit := f.Limit
	if limit <= 0 || limit > s.cfg.CatalogProductsPageMax {
		limit = s.cfg.CatalogProductsPageMax
	}
	page := OutputPage{Outputs: []DraftRow{}, Limit: limit, Offset: max(f.Offset, 0)}
	if s.store == nil {
		return page, nil
	}
	if f.Status != "" && !ValidStatus(f.Status) {
		return OutputPage{}, fmt.Errorf("catalog: unknown status %q", f.Status)
	}

	langs := f.Langs
	if len(langs) == 0 {
		// Empty means every language, not the source language. This is the one
		// filter in this package where the zero Lang cannot double as "unset":
		// "" is a language here and a real answer.
		langs = Langs()
	}
	for _, l := range langs {
		if !KnownLang(l) {
			return OutputPage{}, fmt.Errorf("catalog: unknown language %q", l)
		}
	}

	imports, err := s.List(ctx, s.cfg.CatalogOutputsImportMax)
	if err != nil {
		return OutputPage{}, err
	}
	sel, skill := s.currentSelection(), s.currentSkillVersion()
	keys := make([]DraftKey, 0, len(imports)*len(langs))
	for _, imp := range imports {
		// The profile filter is applied here, before the keys are composed, so
		// it narrows the query rather than the result. Reading it off the
		// import's own dialect is safe because a draft can only exist under the
		// profile the file was read with: products are written by Studio.save,
		// which is reached from Import and from Reread, and both write the
		// import row in the same call. A profile added later produces no
		// products — and therefore no drafts — until somebody presses "read it
		// again", and that press rewrites the column.
		if f.Dialect != "" && imp.File.Dialect.Key != f.Dialect {
			continue
		}
		for _, l := range langs {
			keys = append(keys, DraftKey{
				ImportID: imp.ID,
				Version:  s.DraftVersion(imp.Brand, sel, skill, l),
				Lang:     l,
			})
		}
	}
	if len(keys) == 0 {
		return page, nil
	}

	rows, err := s.store.ListCatalogDrafts(ctx, DraftFilter{
		Keys: keys, Status: f.Status, Limit: limit, Offset: page.Offset,
	})
	if err != nil {
		return OutputPage{}, err
	}
	// The store asks for one more than the page so this can be answered without
	// a second count over the same join.
	if len(rows) > limit {
		page.HasMore = true
		rows = rows[:limit]
	}
	for i := range rows {
		rows[i].Changed = changedNames(rows[i].Product.Content(rows[i].Lang), rows[i].Content)
		rows[i].Title = outputTitle(rows[i].Product, rows[i].Lang)
	}
	// Never nil. A nil slice serialises as `null`, and a client that was
	// promised a list and handed null reads `.length` off it and takes the
	// screen down — which is what happened the first time this shipped.
	if rows != nil {
		page.Outputs = rows
	}
	return page, nil
}

// outputTitle is what to call this row.
//
// The product's title in the language the draft is in, falling back to the
// source language's. The fallback is not the usual "show the Turkish where the
// Arabic is missing" — that is exactly what contentFor refuses to do, and for a
// good reason — it is a label on a list row: a file whose Arabic surface is one
// description column has no Arabic title anywhere, and a page of blank labels
// would be unusable.
func outputTitle(p Product, lang Lang) string {
	if t := strings.TrimSpace(p.Content(lang).Title); t != "" {
		return t
	}
	if t := strings.TrimSpace(p.Original.Title); t != "" {
		return t
	}
	return p.Key
}

// ExportResult is one written file.
type ExportResult struct {
	Path     string `json:"path"`
	Products int    `json:"products"`
	Changed  int    `json:"changed"`
	Bytes    int    `json:"bytes"`
}

// Export writes the catalog back out, applying only approved drafts.
//
// Only approved: a drafted-but-unreviewed product is a suggestion, and a tool
// that shipped suggestions to a live storefront because somebody clicked
// "export" would be a tool nobody could leave running.
// The version is resolved here rather than passed in: an export covers every
// language the file carries, so there is no single version a caller could name,
// and a caller that named one would be composing a cache key again.
func (s *Studio) Export(ctx context.Context, importID string) (ExportResult, error) {
	imp, err := s.Get(ctx, importID)
	if err != nil {
		return ExportResult{}, err
	}
	products := Products(imp.ID, imp.File)

	// Keyed by product and then by language, and collected once per language
	// this file can actually carry. Each language resolves its own version —
	// the language is a suffix on the draft key — and its own approvals, which
	// is what makes "approving the Turkish copy does not ship an Arabic one
	// nobody read" true rather than merely stated. The Export function below
	// has been general over languages since task-103 and is tested that way;
	// this loop is the wiring that was missing.
	//
	// Two queries per language, never one per product: the widest file this
	// binary knows carries three languages.
	approved := map[string]map[Lang]Content{}
	if s.store != nil {
		sel, skill := s.currentSelection(), s.currentSkillVersion()
		for _, lang := range imp.File.Langs() {
			version := s.DraftVersion(imp.Brand, sel, skill, lang)
			rows, err := s.store.ListCatalogProducts(ctx, ProductFilter{
				ImportID: importID, Lang: lang,
				Status: StatusApproved, Limit: len(products) + 1,
			})
			if err != nil {
				return ExportResult{}, err
			}
			ids := make([]string, 0, len(rows))
			for _, r := range rows {
				ids = append(ids, r.ID)
			}
			drafts, err := s.store.GetCatalogDrafts(ctx, ids, version)
			if err != nil {
				return ExportResult{}, err
			}
			for id, d := range drafts {
				if approved[id] == nil {
					approved[id] = map[Lang]Content{}
				}
				approved[id][lang] = d.Content
			}
		}
	}

	data, err := Export(imp.File, products, approved)
	if err != nil {
		return ExportResult{}, err
	}
	path, err := WriteExport(s.cfg.ExportDir, imp.Filename, data, s.now())
	if err != nil {
		return ExportResult{}, err
	}
	return ExportResult{
		Path: path, Products: len(products), Changed: len(approved), Bytes: len(data),
	}, nil
}

func knownField(f Field) bool {
	for _, k := range Fields() {
		if k == f {
			return true
		}
	}
	return false
}

// headerIndex is the position of a named column, or -1.
func (f File) headerIndex(col string) int {
	for i, h := range f.Header {
		if normalizeHeader(h) == normalizeHeader(col) {
			return i
		}
	}
	return -1
}

// DraftVersion is the cache key a rewrite is stored under.
//
// It composes four things that each independently change what a rewrite should
// say: the prompt constant, which model wrote it, the brand kit it was written
// against, and the skill body that instructed it. Change any one and the stored
// copy is an answer to a question nobody is asking any more.
//
// What is deliberately *not* in here is anything about the market research a
// rewrite consumed. That has its own key (task-87), without the brand hash, so
// an operator who corrects the voice and re-runs has every description
// rewritten and no competitor research thrown away.
// CurrentDraftVersion is DraftVersion with the two halves this package cannot
// know filled in from the wiring: the operator's standing model choice and the
// version of the catalog agent's skills.
//
// It exists because three callers store and read the same drafts — the HTTP
// routes, the MCP tools and the board card — and a draft is only findable if
// all three compose the identical key. They used to compose it separately. The
// API passed an empty skill version long after the skill shipped, so every
// draft a bulk rewrite paid for was written under a key no read looked up and
// the products screen showed nothing at all. One method, called from every
// side, is the fix; three correct implementations of one format is the bug.
//
// A studio with no wiring falls back to the zero selection and no skill
// version, which is exactly what the key looked like before either existed.
func (s *Studio) CurrentDraftVersion(ctx context.Context, importID string, lang Lang) (string, error) {
	imp, err := s.Get(ctx, importID)
	if err != nil {
		return "", err
	}
	return s.DraftVersion(imp.Brand, s.currentSelection(), s.currentSkillVersion(), lang), nil
}

func (s *Studio) currentSelection() llm.Selection {
	if s.selection == nil {
		return llm.Selection{}
	}
	return s.selection()
}

func (s *Studio) currentSkillVersion() string {
	if s.skillVersion == nil {
		return ""
	}
	return s.skillVersion()
}

func (s *Studio) DraftVersion(kit BrandKit, sel llm.Selection, skillVersion string, lang Lang) string {
	var b strings.Builder
	b.WriteString(s.cfg.CatalogContentVersion)
	if k := sel.Key(); k != "" {
		b.WriteString("@" + k)
	}
	if kit.Version != "" {
		b.WriteString(":" + kit.Version)
	}
	if skillVersion != "" {
		b.WriteString("#" + skillVersion)
	}
	// A suffix, and only for a target language.
	//
	// Every draft stored before languages existed is a source-language draft
	// under a key with no suffix, and an operator's approved copy has to stay
	// findable. Any other placement — a prefix, a segment in the middle —
	// orphans all of it: the read returns no draft for a product still marked
	// approved, and the export writes nothing for it. That is a data-shaped
	// failure, not a cost.
	if lang != LangSource {
		b.WriteString("/" + string(lang))
		// The reviewer's own prompt constant, and it is in this key and only
		// this key: the reviewer runs for a target language and never for the
		// source, so a source key carrying it would claim a pass that did not
		// happen. Changing the reviewer's prompt changes the stored text, and a
		// key that did not say so would serve an answer to a question nobody
		// is asking any more.
		b.WriteString("+" + s.cfg.CatalogReviewVersion)
	}
	return b.String()
}
