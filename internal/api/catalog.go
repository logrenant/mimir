package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/logrenant/mimir/internal/catalog"
	"github.com/logrenant/mimir/internal/catalogjob"
	"github.com/logrenant/mimir/internal/coderunner"
	"github.com/logrenant/mimir/internal/llm"
)

// CatalogStudio is the engine behind /catalog/*. An interface here for the
// reason every other dependency in this package is one: a handler is a door,
// and a door does not need to know what a brand vocabulary is.
type CatalogStudio interface {
	Import(ctx context.Context, filename string, data []byte, sel llm.Selection) (catalog.Import, error)
	Get(ctx context.Context, id string) (catalog.Import, error)
	List(ctx context.Context, limit int) ([]catalog.Import, error)
	// StatusCounts is per-import product counts by status, for the list. It
	// returns no error: a store that cannot answer leaves the rows without
	// their summary rather than failing the screen (SD-6).
	StatusCounts(ctx context.Context) map[string]map[catalog.Lang]map[string]int
	Delete(ctx context.Context, id string) error
	SetMapping(ctx context.Context, id string, mapping map[catalog.LangField]string, sel llm.Selection) (catalog.Import, error)
	SetDialect(ctx context.Context, id, key string, sel llm.Selection) (catalog.Import, error)
	SetWrite(ctx context.Context, id string, want []catalog.LangField) (catalog.Import, error)
	SetTargetLang(ctx context.Context, id string, lang catalog.Lang, sel llm.Selection) (catalog.Import, error)
	Reread(ctx context.Context, id string, sel llm.Selection) (catalog.Import, error)
	SetBrand(ctx context.Context, id string, voice catalog.Voice) (catalog.Import, error)
	RescanBrand(ctx context.Context, id string, sel llm.Selection) (catalog.Import, error)
	ScanSite(ctx context.Context, importID, url string) (catalog.SiteScan, error)
	Products(ctx context.Context, f catalog.ProductFilter, version string) ([]catalog.ProductView, error)
	Product(ctx context.Context, productID string, lang catalog.Lang, version string) (catalog.ProductView, error)
	SaveDraft(ctx context.Context, productID, version string, lang catalog.Lang, in catalog.Content, fields []catalog.Field) (catalog.StoredDraft, error)
	SetStatus(ctx context.Context, productID string, lang catalog.Lang, status, reason string) error
	Export(ctx context.Context, importID string) (catalog.ExportResult, error)
	// Outputs is the cross-import listing of generated content. It is here and
	// not composed from Products because a version has to be resolved per
	// import per language, and a handler that composed one would be composing
	// a cache key.
	Outputs(ctx context.Context, f catalog.OutputFilter) (catalog.OutputPage, error)
	DraftVersion(kit catalog.BrandKit, sel llm.Selection, skillVersion string, lang catalog.Lang) string
	// CurrentDraftVersion is DraftVersion with the operator's model choice and
	// the agent's skill version already filled in. This layer asks rather than
	// composes: see catalogVersion.
	CurrentDraftVersion(ctx context.Context, importID string, lang catalog.Lang) (string, error)
}

// --- wire shapes ------------------------------------------------------------

type catalogImportRequest struct {
	Filename   string `json:"filename"`
	DataBase64 string `json:"data_base64"`
}

type catalogImportResponse struct {
	Import  catalog.Import `json:"import"`
	Dialect string         `json:"dialect"`
	Header  []string       `json:"header"`
	// Readable is whether this file can be turned into products at all — a
	// platform matched, or the operator's own mapping names enough. It is the
	// screen's gate. Dialect is not: a mapped file has no dialect and is
	// perfectly readable, and reading the gate off Dialect is what left an
	// operator staring at the mapping form they had just saved.
	Readable bool `json:"readable"`
	// Mapping is the operator's saved column map, Suggested this package's
	// guess at one. Suggested is only ever filled when no platform matched;
	// offering a guess beside a known profile would suggest the profile is one.
	Mapping   map[string]string `json:"mapping,omitempty"`
	Suggested map[string]string `json:"suggested,omitempty"`
	// Sample is one trimmed value per column from the first data row, so a
	// dropdown of thirty-seven Turkish header names can be told apart.
	Sample   map[string]string `json:"sample,omitempty"`
	Framing  catalogFraming    `json:"framing"`
	Fields   []string          `json:"fields"`
	Statuses []string          `json:"statuses"`
	// Languages is what THIS file can carry, source language first. A client
	// draws its language tabs from this and not from a constant, because the
	// answer depends on the operator's own export.
	Languages []catalogLangView `json:"languages"`
	// WritableLanguages is every language this binary can write, whether or not
	// this file resolves a column for it. It is a different question from
	// Languages and it has a different reader: the mapping form, which exists
	// precisely so an operator can point at the `Html:Detay-EN` column their own
	// store created and that no profile names. Offering only what already
	// resolves made that column unreachable and the form's own doc comment
	// describe something it did not do.
	WritableLanguages []catalogLangView `json:"writable_languages"`
	// PendingTarget is set when this file carries a translation surface whose
	// language nobody has named. It is the whole gate on the one screen that
	// calls PUT .../target-lang, and until it crossed the wire that screen
	// could not draw: IKAS's Çeviriler export says "Çevrilecek İsim" and
	// nothing anywhere in it says what it was translated into.
	PendingTarget bool `json:"pending_target,omitempty"`
	// TargetLang is the operator's answer to that question, once given.
	TargetLang string `json:"target_lang,omitempty"`
	// RewriteBlocked is why a rewrite cannot be started right now, or empty
	// when it can. It is one sentence in the operator's own language, and it is
	// here so the button can be drawn disabled *with its reason beside it*
	// rather than accepting a press that fails.
	RewriteBlocked string `json:"rewrite_blocked,omitempty"`
}

// catalogFraming is what the import screen shows to say the file was
// understood: an operator who exported semicolon-delimited Windows-1254 needs
// to see those two words back before they trust anything else on the screen.
type catalogFraming struct {
	Delimiter string `json:"delimiter"`
	Encoding  string `json:"encoding"`
	HasBOM    bool   `json:"has_bom"`
	CRLF      bool   `json:"crlf"`
}

type catalogMappingRequest struct {
	Mapping map[string]string `json:"mapping"`
}

type catalogBrandRequest struct {
	Voice catalog.Voice `json:"voice"`
}

type catalogDraftRequest struct {
	Content catalog.Content `json:"content"`
	Fields  []string        `json:"fields"`
}

// catalogRewriteRequest takes an explicit list, never a filter.
//
// handleDraftOutreach's rule, and it matters more here: a filter would let one
// short string spend a whole catalog's worth of searches, crawls and model
// calls, and the operator would find out from the bill rather than from the
// board.
type catalogRewriteRequest struct {
	ImportID   string   `json:"import_id"`
	ProductIDs []string `json:"product_ids"`
	Fields     []string `json:"fields,omitempty"`
	Research   *bool    `json:"research,omitempty"`
	Title      string   `json:"title,omitempty"`
	// Lang is the language this card writes. Absent is the file's own. One
	// card is one language: it is the unit an operator watches and resumes.
	Lang string `json:"lang,omitempty"`
	// Provider and Model are which model this card spends, and they are stored
	// on the card rather than read from the settings file at each call.
	//
	// Neither is required: absent is "whatever the operator's saved choice is
	// when the pass runs", which is what every client written before this
	// asked for. Given, they are pinned to the card — an operator who picked a
	// provider for one pass keeps it when they re-run the card next week under
	// different settings, and a settings change made while the pass is in
	// flight can no longer move it mid-pass.
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

type catalogStatusRequest struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// --- handlers ---------------------------------------------------------------

func (s *Server) handleCatalogImport(w http.ResponseWriter, r *http.Request) {
	var req catalogImportRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	// Base64 in JSON rather than multipart, for the reason
	// handleUploadAttachment gives: the desktop app reaches the daemon through
	// a Rust proxy that forwards a string body, so one encoding for the whole
	// surface is worth the framing. It earns something extra here — the bytes
	// arrive undisturbed, so the encoding sniff sees what the exporter wrote.
	data, err := base64.StdEncoding.DecodeString(req.DataBase64)
	if err != nil {
		writeError(w, http.StatusBadRequest, codeBadRequest, "data_base64 is not valid base64")
		return
	}
	if len(data) == 0 {
		writeError(w, http.StatusBadRequest, codeBadRequest, "the file is empty")
		return
	}

	imp, err := s.deps.Catalog.Import(r.Context(), req.Filename, data, s.catalogSelection())
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.importView(imp))
}

// catalogImportRow is one file on the catalogs screen.
//
// It carries more than the stored import because that screen answers "which
// file do I open", and the answer is not a filename: it is what state the file
// is in (how many products are drafted, approved, failed) and whether it was
// read correctly at all (encoding, delimiter, line endings). Both used to be
// reachable only by opening the file, which made the list a list of names.
//
// The counts come from one query for every import rather than a read per row —
// see catalog.Studio.StatusCounts.
type catalogImportRow struct {
	catalog.Import
	// Dialect is the profile that matched, which survives a listing even
	// though the file body does not.
	Dialect string `json:"dialect"`
	// Counts is the SOURCE language's products by status. Same key, same shape
	// and same absence rule it has always had — a client can tell "no products"
	// from "not known", and one that suddenly found an object of objects here
	// would render nothing.
	Counts map[string]int `json:"counts,omitempty"`
	// CountsByLang is the same fact per language, "" for the source. A target
	// language appears only once something has been decided in it: this listing
	// does not read file bodies, so it cannot know which languages a file
	// carries, and drawing an Arabic chip over a Shopify export would state the
	// opposite of the one gate File.Langs exists to be.
	CountsByLang map[string]map[string]int `json:"counts_by_lang,omitempty"`
}

func (s *Server) handleListCatalogImports(w http.ResponseWriter, r *http.Request) {
	limit, ok := intQuery(w, r, "limit", 50)
	if !ok {
		return
	}
	imports, err := s.deps.Catalog.List(r.Context(), limit)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	counts := s.deps.Catalog.StatusCounts(r.Context())

	// No framing here, deliberately. It would mean reading every file body off
	// disk to name three words per row, and a listing that costs as much as
	// opening every import is the fan-out `ListCatalogImports` exists to avoid.
	// The framing belongs to the file's own screen, where it is already drawn.
	rows := make([]catalogImportRow, 0, len(imports))
	for _, imp := range imports {
		byLang := map[string]map[string]int{}
		for l, byStatus := range counts[imp.ID] {
			byLang[string(l)] = byStatus
		}
		row := catalogImportRow{
			Import:  imp,
			Dialect: imp.File.Dialect.Key,
			// Derived from the per-language map rather than counted again, so
			// there is one source for the number and no way for the two to
			// disagree.
			Counts: byLang[string(catalog.LangSource)],
		}
		if len(byLang) > 0 {
			row.CountsByLang = byLang
		}
		rows = append(rows, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"imports": rows})
}

func (s *Server) handleGetCatalogImport(w http.ResponseWriter, r *http.Request) {
	imp, err := s.deps.Catalog.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.importView(imp))
}

func (s *Server) handleDeleteCatalogImport(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Catalog.Delete(r.Context(), r.PathValue("id")); err != nil {
		writeDomainError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// A dialect profile is code, and one added after a file was uploaded reads that
// file correctly while the products stored at upload time stay blank. This is
// the operator's "read it again" — explicit, because a GET that rewrites a
// thousand rows is a GET nobody can reason about.
func (s *Server) handleRereadCatalogImport(w http.ResponseWriter, r *http.Request) {
	imp, err := s.deps.Catalog.Reread(r.Context(), r.PathValue("id"), s.catalogSelection())
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.importView(imp))
}

// catalogDialectRequest carries an operator's explicit profile pick. An empty
// key is meaningful: it returns the file to detection.
type catalogDialectRequest struct {
	Key string `json:"key"`
}

// catalogWriteRequest is the operator's field configuration. The keys are the
// same wire spellings the column map uses — "title", "description_html@ar" —
// so a person reading the request can tell what it will change.
//
// An empty list is meaningful and is not the same as an absent one: it means
// "not configured", which is every field this file offers. The client sends the
// full set every time rather than a delta, because a delta over a set the file
// itself can change is a delta that silently applies to a different set.
type catalogWriteRequest struct {
	Fields []string `json:"fields"`
}

// catalogTargetLangRequest names the language a translation export's columns
// hold — the question the file cannot answer about itself.
type catalogTargetLangRequest struct {
	Lang string `json:"lang"`
}

// catalogProfileView is one shipped platform profile. Columns is the profile's
// own spelling, not a file's — a client shows it to say what the profile would
// read, and what it actually read is in the import's own response.
// catalogLangView is one language an import can actually carry, and the
// columns it is carried in.
type catalogLangView struct {
	Lang    string            `json:"lang"`
	Label   string            `json:"label"`
	Dir     string            `json:"dir"`
	Columns map[string]string `json:"columns"`
	// Fields is what a rewrite could change in this language, and whether the
	// operator has left it switched on.
	//
	// It was missing, and its absence was not a missing feature — it was a
	// screen saying the opposite of the truth. `File.Writes` reads an empty
	// configuration as "not configured", which means *everything the file
	// offers*, and `catalog.Rewrite` honours that; but this layer sent no
	// field list at all, so the client had nothing to read and said "hiçbir
	// alan yazılmayacak" over every import in existence, greyed out the panel
	// that would have let anybody fix it, and then queued a pass that wrote
	// all five fields anyway.
	Fields []catalogWriteField `json:"fields"`
}

// catalogWriteField is one togglable field in one language.
//
// Key is the wire spelling the configuration is saved under — the bare field
// name for the source language, "field@lang" for a target — so a client posts
// back exactly what it was given.
type catalogWriteField struct {
	Key   string `json:"key"`
	Field string `json:"field"`
	Lang  string `json:"lang"`
	// Column is where this field lives in this file. Sent because "Açıklama"
	// and "Çevrilecek Açıklama" are told apart by where they live long before
	// they are told apart by their label.
	Column string `json:"column"`
	Write  bool   `json:"write"`
}

type catalogProfileView struct {
	Key     string            `json:"key"`
	Name    string            `json:"name"`
	GroupBy string            `json:"group_by"`
	Columns map[string]string `json:"columns"`
}

func (s *Server) handleSetCatalogMapping(w http.ResponseWriter, r *http.Request) {
	var req catalogMappingRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	// The wire is still map[string]string and a source-language key is still
	// the bare field name, so a mapping saved before languages existed posts
	// and parses unchanged. A target language is the "@lang" suffix.
	mapping := make(map[catalog.LangField]string, len(req.Mapping))
	for k, v := range req.Mapping {
		lf, ok := catalog.ParseLangField(k)
		if !ok {
			writeError(w, http.StatusBadRequest, codeBadRequest,
				fmt.Sprintf("unknown field %q", k))
			return
		}
		mapping[lf] = v
	}
	imp, err := s.deps.Catalog.SetMapping(r.Context(), r.PathValue("id"), mapping, s.catalogSelection())
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.importView(imp))
}

// handleListCatalogProfiles answers with the platform profiles this binary
// ships, so a client stops keeping its own copy of a closed set that lives in
// Go. The desktop had one — it kept a profile task-91 deleted, and it would
// have missed any profile added since.
//
// catalog.Dialects() is read as a package function rather than through the
// CatalogStudio port for the same reason catalog.Fields() and
// catalog.Statuses() are: it is a fact about this binary, not about the
// operator's data, and putting it behind the interface would make a test fake
// responsible for a constant table.
func (s *Server) handleListCatalogProfiles(w http.ResponseWriter, _ *http.Request) {
	ds := catalog.Dialects()
	out := make([]catalogProfileView, 0, len(ds))
	for _, d := range ds {
		cols := make(map[string]string, len(d.Columns))
		for f, col := range d.Columns {
			cols[string(f)] = col
		}
		out = append(out, catalogProfileView{
			Key: d.Key, Name: d.Name, GroupBy: string(d.GroupBy), Columns: cols,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": out})
}

// handleSetCatalogDialect records the profile an operator picked over the one
// detection found, or clears the choice when the key is empty.
func (s *Server) handleSetCatalogDialect(w http.ResponseWriter, r *http.Request) {
	var req catalogDialectRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	imp, err := s.deps.Catalog.SetDialect(
		r.Context(), r.PathValue("id"), req.Key, s.catalogSelection())
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.importView(imp))
}

// handleSetCatalogWrite stores which fields a rewrite may change.
//
// A product export does not have a fixed field set, so this is configuration
// rather than a constant: one store's export carries a custom Arabic body and
// no SKU, the next carries an SEO description and no Arabic at all. It is a
// gate and not a default — see catalog.Studio.SetWrite.
func (s *Server) handleSetCatalogWrite(w http.ResponseWriter, r *http.Request) {
	var req catalogWriteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	want := make([]catalog.LangField, 0, len(req.Fields))
	for _, raw := range req.Fields {
		lf, ok := catalog.ParseLangField(raw)
		if !ok {
			writeError(w, http.StatusBadRequest, codeBadRequest,
				fmt.Sprintf("unknown field %q", raw))
			return
		}
		want = append(want, lf)
	}
	imp, err := s.deps.Catalog.SetWrite(r.Context(), r.PathValue("id"), want)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.importView(imp))
}

// handleSetCatalogTargetLang answers, for a translations export, which language
// its target columns hold. IKAS's export does not record it — the operator
// chose it in the admin panel and the file came back without the answer.
func (s *Server) handleSetCatalogTargetLang(w http.ResponseWriter, r *http.Request) {
	var req catalogTargetLangRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	lang := catalog.Lang(req.Lang)
	if !catalog.KnownLang(lang) || lang == catalog.LangSource {
		writeError(w, http.StatusBadRequest, codeBadRequest,
			fmt.Sprintf("unknown target language %q", req.Lang))
		return
	}
	imp, err := s.deps.Catalog.SetTargetLang(
		r.Context(), r.PathValue("id"), lang, s.catalogSelection())
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.importView(imp))
}

func (s *Server) handleGetCatalogBrand(w http.ResponseWriter, r *http.Request) {
	imp, err := s.deps.Catalog.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"brand": imp.Brand})
}

func (s *Server) handleSaveCatalogBrand(w http.ResponseWriter, r *http.Request) {
	var req catalogBrandRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	imp, err := s.deps.Catalog.SetBrand(r.Context(), r.PathValue("id"), req.Voice)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"brand": imp.Brand})
}

func (s *Server) handleRescanCatalogBrand(w http.ResponseWriter, r *http.Request) {
	imp, err := s.deps.Catalog.RescanBrand(r.Context(), r.PathValue("id"), s.catalogSelection())
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"brand": imp.Brand})
}

// catalogSiteRequest is the shop an operator points at.
type catalogSiteRequest struct {
	URL string `json:"url"`
}

// handleScanCatalogSite measures the operator's storefront.
//
// The URL is theirs to type and becomes an outbound request from this machine,
// so the refusal for a private or loopback address comes back as a 400 with the
// daemon's own sentence rather than as a failed fetch: "10.0.0.5 is not a shop"
// is an answer, and a timeout is not.
func (s *Server) handleScanCatalogSite(w http.ResponseWriter, r *http.Request) {
	var req catalogSiteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	scan, err := s.deps.Catalog.ScanSite(r.Context(), r.PathValue("id"), req.URL)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"site": scan})
}

func (s *Server) handleListCatalogProducts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	importID := q.Get("import_id")
	if importID == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "import_id is required")
		return
	}
	limit, ok := intQuery(w, r, "limit", 0)
	if !ok {
		return
	}
	offset, ok := intQuery(w, r, "offset", 0)
	if !ok {
		return
	}

	lang, ok := catalogLang(w, r)
	if !ok {
		return
	}
	version, err := s.catalogVersion(r.Context(), importID, lang)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	products, err := s.deps.Catalog.Products(r.Context(), catalog.ProductFilter{
		ImportID: importID, Lang: lang,
		Status: q.Get("status"), Category: q.Get("category"),
		Limit: limit, Offset: offset,
	}, version)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	if products == nil {
		products = []catalog.ProductView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"products": products, "version": version})
}

func (s *Server) handleGetCatalogProduct(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "product id is required")
		return
	}
	lang, ok := catalogLang(w, r)
	if !ok {
		return
	}
	version, err := s.catalogVersion(r.Context(), r.URL.Query().Get("import_id"), lang)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	view, err := s.deps.Catalog.Product(r.Context(), id, lang, version)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"product": view, "version": version})
}

func (s *Server) handleSaveCatalogDraft(w http.ResponseWriter, r *http.Request) {
	var req catalogDraftRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	id := r.PathValue("id")
	lang, ok := catalogLang(w, r)
	if !ok {
		return
	}
	// One lookup to learn which import this product belongs to, so the version
	// can be resolved for it. It asks in the language being saved rather than
	// in the source one: the answer is the same import either way, and asking
	// twice in two languages would be two reads for one fact.
	view, err := s.deps.Catalog.Product(r.Context(), id, lang, "")
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	version, err := s.catalogVersion(r.Context(), view.ImportID, lang)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}

	fields := make([]catalog.Field, 0, len(req.Fields))
	for _, f := range req.Fields {
		fields = append(fields, catalog.Field(f))
	}
	draft, err := s.deps.Catalog.SaveDraft(r.Context(), id, version, lang, req.Content, fields)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"draft": draft, "version": version})
}

func (s *Server) handleSetCatalogProductStatus(w http.ResponseWriter, r *http.Request) {
	var req catalogStatusRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !catalog.ValidStatus(req.Status) {
		writeError(w, http.StatusBadRequest, codeBadRequest,
			"status must be one of: "+joinStrings(catalog.Statuses()))
		return
	}
	lang, ok := catalogLang(w, r)
	if !ok {
		return
	}
	if err := s.deps.Catalog.SetStatus(r.Context(), r.PathValue("id"), lang, req.Status, req.Reason); err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": req.Status})
}

// handleRewriteCatalog queues a bulk rewrite as a card on the board.
//
// A card rather than a synchronous route, and the split is the same one
// task-81 drew for lead-gen: the interactive edits on this screen answer "what
// does this one product look like", and a pass over two hundred products
// answers "tell me when it is done". Holding an HTTP request open for the
// second is how a queue comes to exist in the first place.
func (s *Server) handleRewriteCatalog(w http.ResponseWriter, r *http.Request) {
	var req catalogRewriteRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.ImportID) == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "import_id is required")
		return
	}
	if len(req.ProductIDs) == 0 {
		writeError(w, http.StatusBadRequest, codeBadRequest,
			"product_ids is required — this route takes the products you picked, never a filter")
		return
	}
	if len(req.ProductIDs) > s.cfg.CatalogProductsPageMax {
		writeError(w, http.StatusBadRequest, codeBadRequest, fmt.Sprintf(
			"product_ids is capped at %d per card; split the selection",
			s.cfg.CatalogProductsPageMax))
		return
	}
	for _, f := range req.Fields {
		if !catalog.Field(f).Writable() {
			writeError(w, http.StatusBadRequest, codeBadRequest,
				"not a rewritable field: "+f)
			return
		}
	}

	lang := catalog.Lang(req.Lang)
	if !catalog.KnownLang(lang) {
		writeError(w, http.StatusBadRequest, codeBadRequest,
			fmt.Sprintf("unknown language %q", req.Lang))
		return
	}

	// Resolved here so a card that names an import nobody has is refused now
	// rather than in a dispatcher nobody is watching.
	imp, err := s.deps.Catalog.Get(r.Context(), req.ImportID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}

	// A language this file has no column for is refused before it costs
	// anything: there would be nowhere to write the answer, and the operator
	// would pay for a pass whose output cannot be exported.
	if !carriesLang(imp, lang) {
		writeError(w, http.StatusBadRequest, codeBadRequest, fmt.Sprintf(
			"bu dosyada %s için sütun yok; sütun eşlemesinden bir sütun seçin", lang.Label()))
		return
	}

	// The card's own model, through the same allow-list every other per-run
	// selection goes through: both names become argv to a subprocess, and a
	// card is more dangerous than a search box because it is spent later,
	// without anybody re-reading it.
	//
	// `llmSelection` and not `leadgenSelection`: the saved choice is
	// deliberately *not* frozen into a card that named nothing. Lead-gen folds
	// it in at the door because a search is over in a minute; a catalog card
	// can sit in a backlog for a week, and an operator who changes the machine
	// default expects the cards they never touched to follow it.
	sel, ok := s.llmSelection(w, req.Provider, req.Model)
	if !ok {
		return
	}

	// The pass is a chain of typed-JSON calls, so a provider that cannot serve
	// a schema cannot serve any of it. Refused here rather than discovered in
	// the pipeline: the domain gate in catalog.Rewrite covers every caller,
	// this one exists so the operator gets a sentence in the screen they
	// pressed the button on instead of a red card six minutes later.
	//
	// Asked of the card's selection, not of the saved one. A card that names a
	// provider that can serve a schema must not be refused because the machine
	// default cannot — that refusal is exactly the wall an operator hits after
	// changing the model to get past it.
	if why := s.catalogModelRefusal(s.effectiveSelection(sel)); why != "" {
		writeError(w, http.StatusUnprocessableEntity, codeBadRequest, why)
		return
	}

	params, err := json.Marshal(map[string]any{
		"import_id":   req.ImportID,
		"product_ids": req.ProductIDs,
		"fields":      req.Fields,
		"research":    req.Research,
		"lang":        req.Lang,
		"provider":    sel.Provider,
		"model":       sel.Model,
	})
	if err != nil {
		writeDomainError(w, r, err)
		return
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = fmt.Sprintf("%s — %d ürün", imp.Filename, len(req.ProductIDs))
	}

	run, err := s.deps.Runner.Create(r.Context(), coderunner.CreateRequest{
		Title:  title,
		Prompt: title,
		Agent:  catalogjob.Agent,
		Params: string(params),
		// The column and the params say the same thing on purpose: the params
		// are what the executor reads, and the column is what every screen
		// already draws. A worker-lane card is not held to the coding-model
		// list, so this is the model the pass will spend — empty when the card
		// leaves it to the daemon.
		Model: sel.Model,
	})
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	if _, err := s.deps.Runner.Enqueue(r.Context(), run.ID); err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"run_id":   run.ID,
		"products": len(req.ProductIDs),
	})
}

// handleListCatalogOutputs lists generated content across every import.
//
// `lang` is absent-means-all here and absent-means-source everywhere else in
// this file, which is the one place that asymmetry exists. It is not an
// oversight: "" is the source language and a real answer, so there is no value
// left over to spell "unset" with. catalogLang's rule that an unknown language
// is a 400 rather than a silent downgrade still applies.
func (s *Server) handleListCatalogOutputs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, ok := intQuery(w, r, "limit", 0)
	if !ok {
		return
	}
	offset, ok := intQuery(w, r, "offset", 0)
	if !ok {
		return
	}
	var langs []catalog.Lang
	if q.Has("lang") {
		lang, ok := catalogLang(w, r)
		if !ok {
			return
		}
		langs = []catalog.Lang{lang}
	}
	if status := q.Get("status"); status != "" && !catalog.ValidStatus(status) {
		writeError(w, http.StatusBadRequest, codeBadRequest,
			"status must be one of: "+joinStrings(catalog.Statuses()))
		return
	}

	page, err := s.deps.Catalog.Outputs(r.Context(), catalog.OutputFilter{
		Langs:   langs,
		Dialect: q.Get("dialect"),
		Status:  q.Get("status"),
		Limit:   limit,
		Offset:  offset,
	})
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) handleExportCatalog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	res, err := s.deps.Catalog.Export(r.Context(), id)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// --- helpers ----------------------------------------------------------------

// carriesLang reports whether this import has somewhere to put a language.
func carriesLang(imp catalog.Import, lang catalog.Lang) bool {
	for _, l := range imp.File.Langs() {
		if l == lang {
			return true
		}
	}
	return false
}

// importView is catalogImportView plus the answers only the server holds.
//
// Today that is one: whether the operator's saved model can serve the schema a
// rewrite is built out of. It belongs on the view rather than behind a route of
// its own because the screen already fetches this and a disabled button owes
// the operator a reason at the moment they look at it — not after they press.
//
// It is the *saved* model's answer, because that is what a card gets when the
// operator picks nothing. A screen that offers a per-card provider asks the
// same question of that provider itself, from `GET /llm/providers`, which
// carries `structured_output` for exactly this; it must not read a blocked
// default as blocking a card that names a provider which can serve a schema.
func (s *Server) importView(imp catalog.Import) catalogImportResponse {
	v := catalogImportView(imp, s.cfg.CatalogSampleChars)
	v.RewriteBlocked = s.catalogModelRefusal(s.catalogSelection())
	return v
}

func catalogImportView(imp catalog.Import, sampleChars int) catalogImportResponse {
	fields := make([]string, 0, len(catalog.Fields()))
	for _, f := range catalog.Fields() {
		if f.Writable() {
			fields = append(fields, string(f))
		}
	}
	delim := string(imp.File.Delimiter)
	if imp.File.Delimiter == 0 {
		delim = ""
	}
	// The wire keys carry the language: a bare field name for the file's own
	// language, "field@lang" for a target. A mapping saved before languages
	// existed round-trips unchanged through this.
	mapping := map[string]string{}
	for f, col := range imp.File.Mapping {
		mapping[catalog.LangField{Field: f}.String()] = col
	}
	for lang, cols := range imp.File.Translations {
		for f, col := range cols {
			mapping[catalog.LangField{Field: f, Lang: lang}.String()] = col
		}
	}

	// Which languages THIS import can carry, not which ones this binary knows.
	// A store whose export has no Arabic column must not be offered an Arabic
	// pass: there would be nowhere to write the answer.
	langs := make([]catalogLangView, 0, len(imp.File.Langs()))
	for _, l := range imp.File.Langs() {
		langs = append(langs, catalogLangViewOf(imp, l))
	}
	// And which languages this binary can write at all. The mapping form reads
	// this one: a language with no column yet is exactly the language somebody
	// opens that form to give a column to.
	writable := make([]catalogLangView, 0, len(catalog.Langs()))
	for _, l := range catalog.Langs() {
		writable = append(writable, catalogLangViewOf(imp, l))
	}
	var suggested map[string]string
	if imp.File.Dialect.IsZero() {
		suggested = map[string]string{}
		for f, col := range catalog.Suggest(imp.File.Header) {
			suggested[string(f)] = col
		}
	}

	return catalogImportResponse{
		Import:    imp,
		Dialect:   imp.File.Dialect.Key,
		Header:    imp.File.Header,
		Readable:  imp.File.Readable(),
		Mapping:   mapping,
		Suggested: suggested,
		Sample:    imp.File.Sample(sampleChars),
		Framing: catalogFraming{
			Delimiter: delim,
			Encoding:  string(imp.File.Encoding),
			HasBOM:    imp.File.HasBOM,
			CRLF:      imp.File.CRLF,
		},
		Fields:            fields,
		Statuses:          catalog.Statuses(),
		Languages:         langs,
		WritableLanguages: writable,
		PendingTarget:     imp.File.PendingTarget(),
		TargetLang:        string(imp.File.TargetLang),
	}
}

// catalogLangViewOf is one language as this file sees it: the columns it
// resolves and the fields it could write there.
//
// It is one function because the two lists that use it — what this file
// carries, and what this binary can write — must describe a language the same
// way. Two spellings of "the Arabic surface" is how a form ends up offering a
// column the table then refuses to read.
func catalogLangViewOf(imp catalog.Import, l catalog.Lang) catalogLangView {
	cols := map[string]string{}
	for f, col := range imp.File.ColumnsFor(l) {
		cols[string(f)] = col
	}
	// Offered is the file's own answer to "what could be rewritten here";
	// Writes is the operator's to "may it be". Asking the file rather than a
	// constant is the whole point of the field configuration, and asking
	// `Writes` rather than reading `File.Write` directly is what keeps an
	// unconfigured import reading as "everything" instead of "nothing".
	offered := imp.File.Offered(l)
	fields := make([]catalogWriteField, 0, len(offered))
	for _, f := range offered {
		lf := catalog.LangField{Field: f, Lang: l}
		fields = append(fields, catalogWriteField{
			Key:    lf.String(),
			Field:  string(f),
			Lang:   string(l),
			Column: cols[string(f)],
			Write:  imp.File.Writes(lf),
		})
	}
	return catalogLangView{
		Lang: string(l), Label: l.Label(), Dir: l.Dir(), Columns: cols, Fields: fields,
	}
}

// catalogVersion resolves the draft key for an import.
//
// It is computed here rather than sent by the client because it is a cache
// key: a client that could name one could serve itself a draft written under a
// brand voice that no longer exists, and would have no way of knowing.
//
// The composition itself is the studio's, not this package's. It used to be
// here, and it disagreed with the one the board card writes under: this layer
// passed an empty skill version long after task-87 shipped the skill, so every
// draft a bulk rewrite paid for was stored under a key no read looked up and
// the products screen showed nothing at all. Two implementations of one key
// format is the bug; asking the one thing that owns it is the fix.
func (s *Server) catalogVersion(ctx context.Context, importID string, lang catalog.Lang) (string, error) {
	if importID == "" {
		return "", nil
	}
	return s.deps.Catalog.CurrentDraftVersion(ctx, importID, lang)
}

// catalogSelection is the operator's saved model choice, the same one lead-gen
// spends. It is read from settings rather than taken from the request for the
// reason task-70 moved the lead-gen picker off the search bar: which tier a run
// spends is a decision made once, in one place.
func (s *Server) catalogSelection() llm.Selection {
	provider, model := s.savedSelection()
	return llm.Selection{Provider: provider, Model: model}
}

// catalogLang reads the language a request is about. Absent means the file's
// own language, which is what every client written before languages existed
// asks for and what every stored draft is.
//
// An unknown value is refused rather than silently treated as the source
// language: a client asking for a language this binary does not carry would
// otherwise be handed the Turkish copy and have no way to tell.
func catalogLang(w http.ResponseWriter, r *http.Request) (catalog.Lang, bool) {
	raw := r.URL.Query().Get("lang")
	l := catalog.Lang(raw)
	if !catalog.KnownLang(l) {
		writeError(w, http.StatusBadRequest, codeBadRequest,
			fmt.Sprintf("unknown language %q", raw))
		return "", false
	}
	return l, true
}

func intQuery(w http.ResponseWriter, r *http.Request, name string, def int) (int, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		writeError(w, http.StatusBadRequest, codeBadRequest, name+" must be a non-negative integer")
		return 0, false
	}
	return n, true
}

func joinStrings(in []string) string {
	out := ""
	for i, s := range in {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

// effectiveSelection is which model a card will actually spend: its own if it
// names one, and the operator's saved choice if it does not. It is the route's
// copy of catalogjob.selectionFor, and the two must agree — a gate that asked
// about a different model than the pass spends is a gate that refuses the wrong
// cards and clears the wrong ones.
func (s *Server) effectiveSelection(sel llm.Selection) llm.Selection {
	if !sel.IsZero() {
		return sel
	}
	return s.catalogSelection()
}

// catalogModelRefusal is why a rewrite cannot start under this selection, or ""
// when it can.
//
// It reads the same capability the router resolves, so the answer here and the
// refusal inside the pass cannot disagree. A router that cannot answer at all
// clears the check: an unwired daemon serving these routes is a daemon that
// degrades, not one that refuses everything.
func (s *Server) catalogModelRefusal(sel llm.Selection) string {
	if s.deps.LLM == nil {
		return ""
	}
	reader, ok := s.deps.LLM.(interface {
		Capabilities(llm.Class, llm.Selection) (llm.Capabilities, error)
	})
	if !ok {
		return ""
	}
	caps, err := reader.Capabilities(llm.Reason, sel)
	if err != nil || caps.StructuredOutput {
		return ""
	}
	name := sel.Key()
	if name == "" {
		name = "seçili model"
	}
	return fmt.Sprintf(
		"%s yapılandırılmış çıktı veremiyor; katalog yazımı onu zorunlu kılıyor. "+
			"Ayarlar'dan yapılandırılmış çıktı veren bir model seçin.", name)
}
