package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/logrenant/mimir/internal/llm"
)

// rewriteSchema is what the model is asked to return.
//
// The description comes back as a list of typed blocks carrying *text*, never
// as HTML. That is the whole design: the markup never left this package, so a
// rewrite cannot bring any back. A provider that cannot enforce a schema leaves
// Structured empty and the text is parsed as JSON instead, which fails loudly
// rather than silently.
var rewriteSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"title": {"type": "string"},
		"seo_title": {"type": "string"},
		"seo_description": {"type": "string"},
		"tags": {"type": "array", "items": {"type": "string"}},
		"blocks": {
			"type": "array",
			"items": {
				"type": "object",
				"properties": {
					"kind": {"type": "string", "enum": ["paragraph", "heading", "listitem", "quote"]},
					"level": {"type": "integer"},
					"text": {"type": "string"}
				},
				"required": ["kind", "text"],
				"additionalProperties": false
			}
		}
	},
	"required": ["title", "seo_title", "seo_description", "tags", "blocks"],
	"additionalProperties": false
}`)

type rewritten struct {
	Title          string   `json:"title"`
	SEOTitle       string   `json:"seo_title"`
	SEODescription string   `json:"seo_description"`
	Tags           []string `json:"tags"`
	Blocks         []struct {
		Kind  string `json:"kind"`
		Level int    `json:"level"`
		Text  string `json:"text"`
	} `json:"blocks"`
}

// RewriteRequest is one bulk pass, as the card describes it.
//
// ProductIDs is an explicit list and never a filter. The rule is
// internal/api's, learned on outreach: a filter would let one short string
// spend a whole catalog's worth of tokens.
type RewriteRequest struct {
	ImportID   string
	ProductIDs []string
	Fields     []Field
	Research   bool
	Selection  llm.Selection
	// SkillVersion is the version of the standing instructions this pass runs
	// under. It is composed into the draft key, so editing the skill
	// invalidates copy written under the old one and nothing else.
	SkillVersion string
	// Lang is the language this pass writes. LangSource is the file's own
	// language and is what every pass before this field existed was, so the
	// zero value keeps meaning what it meant.
	Lang  Lang
	Skill string
}

// Report is what a pass did.
type Report struct {
	Version   string   `json:"version"`
	Requested int      `json:"requested"`
	Written   int      `json:"written"`
	Cached    int      `json:"cached"`
	Skipped   int      `json:"skipped"`
	Failed    int      `json:"failed"`
	Notes     []string `json:"notes,omitempty"`
}

// Sink is how a bulk pass narrates itself. It is this package's own two-method
// shape rather than coderunner's four, so nothing here imports the runner.
type Sink interface {
	Step(name string, args any) func(ok bool, output string)
	Say(text string)
}

// nopSink is what a caller with nowhere to narrate gets.
type nopSink struct{}

func (nopSink) Step(string, any) func(bool, string) { return func(bool, string) {} }
func (nopSink) Say(string)                          {}

// Rewrite runs one bulk pass, product by product.
//
// Deliberately serial over products rather than fanned out. Every product here
// is a search, a crawl, a refine and a reason call, and all four already run
// behind their own bounded pools (`MaxConcurrentCrawls`, `MaxConcurrentRefines`,
// the crawl politeness interval). A second fan-out on top would not make the
// work faster, it would only queue more of it behind the same doors and make
// the narration arrive out of order — and the narration is how an operator
// watches a run they cannot otherwise see.
//
// What makes a stopped pass cheap to resume is not concurrency, it is that
// every product checks two caches before it spends anything.
func (s *Studio) Rewrite(ctx context.Context, req RewriteRequest, out Sink) (Report, error) {
	if out == nil {
		out = nopSink{}
	}
	rep := Report{Requested: len(req.ProductIDs)}

	imp, err := s.Get(ctx, req.ImportID)
	if err != nil {
		return rep, err
	}
	version := s.DraftVersion(imp.Brand, req.Selection, req.SkillVersion, req.Lang)
	rep.Version = version

	// The operator's saved configuration is the gate, and it is a gate rather
	// than a default: a card that names fields still cannot write one the
	// operator switched off, or one this file has no column for. A toggle that
	// only chose a default would be a toggle an old card silently overrode —
	// and the operator would find out from the exported file.
	fields := req.Fields
	if len(fields) == 0 {
		fields = imp.File.Offered(req.Lang)
	}
	var write []Field
	for _, f := range fields {
		if !f.Writable() {
			return rep, fmt.Errorf("%w: %s", ErrNotWritable, f)
		}
		if imp.File.Writes(LangField{Field: f, Lang: req.Lang}) {
			write = append(write, f)
		}
	}
	if len(write) == 0 {
		return rep, fmt.Errorf("%w: %s", ErrNothingToWrite, req.Lang.Label())
	}
	if len(write) < len(fields) {
		rep.Notes = append(rep.Notes, skippedNote(fields, write, imp.File, req.Lang))
	}
	fields = write

	// Before the first search, not before the first model call.
	//
	// llm.Router already refuses a schema-carrying request to a provider that
	// cannot serve one, and its comment says "before anything is spent" — which
	// is true of that package and false of this one. Every product here is a
	// search, a crawl, a refine and *then* the schema call, so the refusal
	// arrived after six minutes of somebody's afternoon and one product marked
	// `failed` with a sentence about JSON. Measured against an operator's own
	// catalogue on `ollama/qwen3:8b`, which is how this gate was found.
	//
	// The whole pass fails, not the product: the answer is the same for every
	// remaining one, and rediscovering it four hundred times is task-87's
	// isFatal argument arriving through a different door.
	if err := s.checkStructured(req.Selection); err != nil {
		return rep, err
	}

	if imp.Brand.Vocab.IsEmpty() {
		rep.Notes = append(rep.Notes,
			"Bu katalogda okunabilir bir biçimlendirme yok; açıklamalar düz metin olarak yazılıyor.")
	}
	if req.Research && s.research == nil {
		rep.Notes = append(rep.Notes,
			"Pazar araştırması yapılamadı: bu daemon'da araştırma hattı yok. İçerikler ürünün kendi verisinden yazıldı.")
		req.Research = false
	}

	for _, id := range req.ProductIDs {
		if err := ctx.Err(); err != nil {
			// A cancelled pass is a stop, not a failure. Everything already
			// written stays written and a retry picks up from here.
			return rep, err
		}
		note, err := s.rewriteOne(ctx, id, imp, version, fields, req, out, &rep)
		if err != nil {
			return rep, err
		}
		if note != "" {
			rep.Notes = append(rep.Notes, note)
		}
	}

	// A pass that wrote nothing and read nothing from the cache did not
	// degrade, it failed — and reporting it as completed would put a green card
	// on the board above a catalog nobody touched. SD-6's own wording: one
	// failed source drops that source, "unless every source failed".
	if rep.Requested > 0 && rep.Written == 0 && rep.Cached == 0 && rep.Failed > 0 {
		return rep, fmt.Errorf("hiçbir ürün yazılamadı (%d ürünün hepsi başarısız)", rep.Failed)
	}
	return rep, nil
}

// shortReason keeps a step's label readable. The full text is on the product
// row, where somebody can read the whole sentence.
func shortReason(err error) string {
	s := strings.Join(strings.Fields(err.Error()), " ")
	if r := []rune(s); len(r) > 120 {
		return string(r[:120]) + "…"
	}
	return s
}

// rewriteOne is one product. A returned error is fatal to the whole pass —
// reserved for a rate limit and a cancellation. Everything else marks this one
// product failed and the pass carries on (SD-6).
func (s *Studio) rewriteOne(
	ctx context.Context, id string, imp Import, version string,
	fields []Field, req RewriteRequest, out Sink, rep *Report,
) (string, error) {
	if s.store == nil {
		return "", fmt.Errorf("catalog: no store")
	}
	row, ok, err := s.store.GetCatalogProduct(ctx, id, req.Lang)
	if err != nil {
		return "", err
	}
	if !ok {
		rep.Skipped++
		return fmt.Sprintf("%s: ürün bulunamadı.", id), nil
	}
	label := row.Original.Title
	if label == "" {
		label = row.Key
	}

	// The read that makes a resumed pass cheap. A product that already has a
	// draft under this exact version has been paid for, and the answer would be
	// the same one — the version is composed of everything that could change it.
	if _, ok, err := s.store.GetCatalogDraft(ctx, id, version); err == nil && ok {
		rep.Cached++
		done := out.Step("draft", map[string]any{"product": label, "cached": true})
		done(true, "önbellekten — model harcanmadı")
		return "", nil
	} else if err != nil {
		return "", err
	}

	var findings Findings
	if req.Research {
		done := out.Step("research", map[string]any{"product": label})
		f, cached, rerr := s.researchFor(ctx, row.Product, req.Selection)
		switch {
		case isFatal(rerr):
			done(false, shortReason(rerr))
			return "", rerr
		case rerr != nil:
			// Research is not the product. One that could not be researched is
			// written from its own data and says so, rather than failing.
			done(false, rerr.Error())
			rep.Notes = append(rep.Notes,
				fmt.Sprintf("%s: pazar araştırması alınamadı, ürün kendi verisinden yazıldı.", label))
		default:
			findings = f
			if cached {
				done(true, "önbellekten")
			} else {
				done(true, fmt.Sprintf("%d kaynak", len(f.Sources)))
			}
		}
	}

	done := out.Step("draft", map[string]any{"product": label})
	content, notes, err := s.draftOne(ctx, row.Product, imp.Brand, findings, fields, req)
	if err != nil {
		if isFatal(err) {
			done(false, shortReason(err))
			return "", err
		}
		done(false, shortReason(err))
		rep.Failed++
		// Why it failed, in the narration as well as on the product row. A card
		// that says "1 ürün başarısız" and nothing else sends the operator to
		// look for a reason that is one screen away; a language failure in
		// particular is one they can act on — it names the rule that refused it.
		rep.Notes = append(rep.Notes, label+": "+shortReason(err))
		rep.Notes = append(rep.Notes, notes...)
		// The language this pass was for, and no other. An Arabic product that
		// the language gate refused is not a failed Turkish product: marking it
		// one would overwrite a decision an operator already made, wipe its
		// reason with a complaint about a different language, and take it out
		// of their approved filter.
		_ = s.store.SetCatalogProductStatus(ctx, id, req.Lang, StatusFailed, clampReason(err.Error()), s.now())
		return "", nil
	}

	names := make([]string, 0, len(fields))
	for _, f := range fields {
		names = append(names, string(f))
	}
	werr := s.store.PutCatalogDraft(ctx, StoredDraft{
		ProductID: id, Version: version,
		Content: content, Fields: names, Notes: notes,
		Provider: req.Selection.Provider, Model: req.Selection.Model,
		CreatedAt: s.now().UTC(),
	})
	switch {
	case werr == nil:
		rep.Written++
		_ = s.store.SetCatalogProductStatus(ctx, id, req.Lang, StatusDrafted, "", s.now())
		done(true, strings.Join(changedNames(row.Content(req.Lang), content), " · "))
	case isDraftLocked(werr):
		// A person has already written this product's copy by hand. Not a
		// failure of the pass: the product keeps the words somebody chose.
		rep.Skipped++
		done(true, "elle düzenlenmiş — dokunulmadı")
	default:
		return "", werr
	}
	return "", nil
}

// draftOne is the one model call, and what it is allowed to produce.
func (s *Studio) draftOne(
	ctx context.Context, p Product, kit BrandKit, findings Findings,
	fields []Field, req RewriteRequest,
) (Content, []string, error) {
	if s.llm == nil {
		return Content{}, nil, fmt.Errorf("%w: no provider configured", llm.ErrProviderUnavailable)
	}
	src, err := ParseHTML(p.Original.DescriptionHTML)
	if err != nil {
		return Content{}, nil, err
	}

	system, user := buildRewritePrompt(p, kit, findings, src, fields, req.Lang, req.Skill, s.cfg)
	resp, err := s.llm.CompleteWith(ctx, llm.Reason, req.Selection, llm.Request{
		System: system, User: user, Schema: rewriteSchema,
		MaxTokens: s.cfg.CatalogRewriteMaxTokens,
	})
	if err != nil {
		return Content{}, nil, err
	}

	raw := resp.Structured
	if len(raw) == 0 {
		raw = json.RawMessage(extractJSONObject(resp.Text))
	}
	if len(raw) == 0 {
		return Content{}, nil, fmt.Errorf("catalog: the model returned no JSON object")
	}
	var out rewritten
	if err := json.Unmarshal(raw, &out); err != nil {
		return Content{}, nil, fmt.Errorf("catalog: unparseable rewrite output: %v", err)
	}

	content, notes, rep, err := s.assemble(p, kit, src, out, fields, req.Lang)
	if err != nil {
		return Content{}, notes, err
	}
	notes = append(notes, rep.Notes()...)

	// The source language gets no reviewer: there is nothing to review it
	// against, and this package does not know what language the file is in.
	if req.Lang == LangSource {
		settled, err := settle(content, p, req.Lang, rep)
		return settled, notes, err
	}

	reviewed, reviewNotes, reviewRep, err := s.reviewOne(
		ctx, p, kit, src, content, fields, rep, req)
	notes = append(notes, reviewNotes...)
	if err != nil {
		if isFatal(err) {
			// A signed-out CLI is the pass's problem, not this product's.
			return Content{}, notes, err
		}
		// SD-6, degrade rather than crash — but the deterministic gate is the
		// floor and it does not degrade. A draft the machine check already
		// refused is not stored just because the reviewer could not be reached.
		notes = append(notes, "Anadil kontrolü yapılamadı: "+err.Error())
		settled, serr := settle(content, p, req.Lang, rep)
		return settled, notes, serr
	}

	settled, err := settle(reviewed, p, req.Lang, reviewRep)
	return settled, notes, err
}

// assemble turns the model's answer back into a product, and is where every
// claim this package makes is enforced.
// The language report is returned rather than acted on, because the caller is
// the only one that knows whether a reviewer is still going to get a turn: a
// fatal finding on the first pass is what the reviewer is *for*, and failing
// the product here would throw away the draft before anything tried to fix it.
func (s *Studio) assemble(
	p Product, kit BrandKit, src Doc, out rewritten, fields []Field, lang Lang,
) (Content, []string, LangReport, error) {
	// The starting point is this language's own current copy, not the source
	// language's: a field the model was not asked to write keeps what the file
	// already said in *this* language, and for a language the file does not
	// carry yet that is correctly empty.
	content := p.Content(lang)
	var notes []string

	want := map[Field]bool{}
	for _, f := range fields {
		want[f] = true
	}

	if want[FieldDescriptionHTML] {
		blocks := make([]Block, 0, len(out.Blocks))
		for _, b := range out.Blocks {
			if strings.TrimSpace(b.Text) == "" {
				continue
			}
			kind := BlockKind(b.Kind)
			blocks = append(blocks, Block{
				Kind: kind, Level: b.Level, Text: b.Text,
				// The envelope is never the model's to choose. It comes from
				// the nearest block of the same kind in the store's own past
				// HTML, so a paragraph the model added looks like this brand's
				// other paragraphs.
				Envelope: envelopeFor(src, kind, b.Level),
			})
		}
		if len(blocks) == 0 {
			return Content{}, nil, LangReport{}, fmt.Errorf("catalog: the rewrite produced no description")
		}
		// Repaired while it is still text. A pass over the rendered HTML would
		// rewrite the inside of a tag.
		blocks, repairs := NormalizeBlocks(blocks, lang)
		notes = append(notes, notesOf(repairs)...)
		// sourceURLs, not the operator policy: this is model output, and a URL
		// it produced that the product never had was invented.
		content.DescriptionHTML = RenderLang(blocks, kit.Vocab, src.URLs, lang)
		if strings.TrimSpace(content.DescriptionHTML) == "" {
			return Content{}, nil, LangReport{}, fmt.Errorf("catalog: nothing survived the brand's vocabulary")
		}
	}

	if want[FieldTitle] && strings.TrimSpace(out.Title) != "" {
		content.Title = strings.Join(strings.Fields(out.Title), " ")
	}
	if want[FieldSEOTitle] {
		v := strings.Join(strings.Fields(out.SEOTitle), " ")
		if v != "" {
			if over(v, s.cfg.CatalogSEOTitleMaxChars) {
				// A rejected field costs that field, not the product. An SEO
				// title cut mid-word is worse than the one the store already
				// had, so the old one stays and the note says why.
				notes = append(notes, fmt.Sprintf(
					"SEO başlık %d karakteri aştı, eskisi korundu.", s.cfg.CatalogSEOTitleMaxChars))
			} else {
				content.SEOTitle = v
			}
		}
	}
	if want[FieldSEODescription] {
		v := strings.Join(strings.Fields(out.SEODescription), " ")
		if v != "" {
			if over(v, s.cfg.CatalogSEODescMaxChars) {
				notes = append(notes, fmt.Sprintf(
					"SEO açıklama %d karakteri aştı, eskisi korundu.", s.cfg.CatalogSEODescMaxChars))
			} else {
				content.SEODescription = v
			}
		}
	}
	if want[FieldTags] && len(out.Tags) > 0 {
		content.Tags = strings.Join(clampList(out.Tags, 24, 40), ",")
	}

	// The language gate. It runs last because it measures the finished copy,
	// and it runs on this path *and* on the operator's, so there is no way to
	// store a draft that skipped it.
	content, repairs := NormalizeForLang(content, lang)
	notes = append(notes, notesOf(repairs)...)

	return content, notes, CheckLanguage(content, p.Original, lang, s.cfg, fields), nil
}

// settle turns a language report into a decision, once nothing else is going to
// improve the draft.
//
// The line it draws is assemble's own: a rejected SEO field costs that field
// and keeps the old one, a rejected description costs the product. An Arabic
// description that is 30% Arabic is not a draft with a caveat — it is wrong
// copy, and the cell it would be written into was empty, so there is no older
// value being protected by keeping it.
func settle(content Content, p Product, lang Lang, rep LangReport) (Content, error) {
	if rep.OK() {
		return content, nil
	}
	if !rep.OnlySEO() {
		return Content{}, fmt.Errorf("catalog: %s", strings.Join(reasonsOf(rep.Fatal()), "; "))
	}
	for _, f := range rep.Fatal() {
		content.Set(f.Field, p.Content(lang).Get(f.Field))
	}
	return content, nil
}

func notesOf(found []LangFinding) []string {
	out := make([]string, 0, len(found))
	for _, f := range found {
		out = append(out, f.Note)
	}
	return out
}

func reasonsOf(found []LangFinding) []string {
	out := make([]string, 0, len(found))
	for _, f := range found {
		out = append(out, string(f.Rule)+": "+f.Note)
	}
	return out
}

func over(s string, max int) bool { return len([]rune(s)) > max }

// envelopeFor finds the markup a block of this kind sat in, in the store's own
// description. Falling back to the first block's envelope keeps a rewrite that
// added a heading to a store that had none inside that store's wrapper rather
// than at the document root.
func envelopeFor(src Doc, kind BlockKind, level int) []Elem {
	var sameKind, any []Elem
	for _, b := range src.Blocks {
		if any == nil {
			any = b.Envelope
		}
		if b.Kind != kind {
			continue
		}
		if kind == BlockHeading && level > 0 && b.Level == level {
			return b.Envelope
		}
		if sameKind == nil {
			sameKind = b.Envelope
		}
	}
	if sameKind != nil {
		return sameKind
	}
	return any
}

func writableFields() []Field {
	out := make([]Field, 0, 5)
	for _, f := range Fields() {
		if f.Writable() {
			out = append(out, f)
		}
	}
	return out
}

func changedNames(before, after Content) []string {
	var out []string
	for _, f := range writableFields() {
		if before.Get(f) != after.Get(f) {
			out = append(out, string(f))
		}
	}
	if len(out) == 0 {
		return []string{"değişiklik yok"}
	}
	return out
}

func clampReason(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 300 {
		return string(r[:300])
	}
	return s
}

// isFatal is the failure that stops a whole pass rather than one product.
//
// A provider that cannot be run is not this product's problem, it is the
// machine's: the CLI is not authenticated, or the account's window is spent.
// Carrying on would rediscover the same thing once per remaining product,
// which on a four-hundred-product catalog is four hundred subprocess launches
// to learn one fact — and it would end with a card marked completed and
// nothing written, which is the worst of both.
//
// llm.ErrRateLimited wraps llm.ErrProviderUnavailable, so one predicate covers
// both. Which of the two it was decides only the sentence the card carries, and
// that choice belongs to internal/catalogjob, where the card is written.
func isFatal(err error) bool {
	return err != nil && errorsIs(err, llm.ErrProviderUnavailable)
}

func isDraftLocked(err error) bool {
	return err != nil && errorsIs(err, ErrDraftLocked)
}

// errorsIs is errors.Is, wrapped so this file's intent reads at the call site.
func errorsIs(err, target error) bool { return errors.Is(err, target) }

// skippedNote names what the pass will not touch and why. Silence here would be
// the operator reading a green card and a file that did not change.
func skippedNote(asked, writing []Field, f File, lang Lang) string {
	kept := map[Field]bool{}
	for _, x := range writing {
		kept[x] = true
	}
	var noColumn, switchedOff []string
	for _, x := range asked {
		if kept[x] {
			continue
		}
		if f.index(LangField{Field: x, Lang: lang}) < 0 {
			noColumn = append(noColumn, string(x))
			continue
		}
		switchedOff = append(switchedOff, string(x))
	}
	var parts []string
	if len(noColumn) > 0 {
		parts = append(parts, "bu dosyada sütunu yok: "+strings.Join(noColumn, ", "))
	}
	if len(switchedOff) > 0 {
		parts = append(parts, "alan ayarlarında kapalı: "+strings.Join(switchedOff, ", "))
	}
	return "Bazı alanlar yazılmadı — " + strings.Join(parts, "; ") + "."
}

// checkStructured refuses a selection that cannot return the typed blocks a
// rewrite is built out of.
//
// A Completer that cannot answer the question is taken at its word rather than
// doubted: this package's tests pass a counting stub, and a check that failed
// closed against one would make every test in the file a capability test.
func (s *Studio) checkStructured(sel llm.Selection) error {
	reader, ok := s.llm.(CapabilityReader)
	if !ok {
		return nil
	}
	caps, err := reader.Capabilities(llm.Reason, sel)
	if err != nil {
		// Not our failure to report. An unroutable selection is
		// llm.ErrProviderUnavailable and the pass fails on it either way, with
		// the router's own sentence rather than a paraphrase.
		return err
	}
	if caps.StructuredOutput {
		return nil
	}
	// llm's own error, not a second one of this package's: the handler above
	// and the board card below both already know how to read it, and two
	// spellings of one fact is how a message ends up saying the wrong thing in
	// one of the two places.
	name := sel.Key()
	if name == "" {
		name = "seçili model"
	}
	return fmt.Errorf("%w: %s", llm.ErrNoStructuredOutput, name)
}
