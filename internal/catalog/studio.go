package catalog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/llm"
)

// Typed failures, so a handler can map them to a status and an operator can
// read a sentence that names the fix (SD-6).
var (
	// ErrNoDialect means the header matched no platform profile. It is not
	// fatal: the import exists and waits for the operator's column mapping.
	ErrNoDialect = errors.New("catalog: no known platform matched this header")
	// ErrTooManyProducts means the file is larger than one operator reviews.
	ErrTooManyProducts = errors.New("catalog: too many products in one file")
	// ErrUnknownImport / ErrUnknownProduct are misses, not failures.
	ErrUnknownImport  = errors.New("catalog: no such import")
	ErrUnknownProduct = errors.New("catalog: no such product")
	// ErrMappingIncomplete means an operator's column map named neither a
	// title nor a description. Everything downstream — the brand vocabulary,
	// the research query, the rewrite — reads one of those two, so a mapping
	// without either produces an import full of empty products and no
	// explanation. Refusing it here is the explanation.
	ErrMappingIncomplete = errors.New("catalog: a mapping needs at least a title or a description column")
	// ErrNotWritable means a caller named an identity field as rewritable.
	ErrNotWritable = errors.New("catalog: field is not rewritable")

	// ErrUnknownDialect means an operator picked a profile this binary does not
	// ship. The profile table is code, so this is version skew between a client
	// and a daemon rather than a bad request somebody typed.
	ErrUnknownDialect = errors.New("catalog: no such platform profile")

	// ErrNothingToWrite means a configuration selected only fields this file has
	// no column for. An empty configuration means "everything", so storing this
	// one would silently turn every switch back on.
	ErrNothingToWrite = errors.New("catalog: none of the selected fields have a column in this file")

	// ErrNoTargetColumns means a language was named for a file whose profile has
	// no language-agnostic translation columns to name.
	ErrNoTargetColumns = errors.New("catalog: this file has no translation columns")
	// ErrDraftLocked means a write was refused because the operator has edited
	// that draft by hand. It is not a failure of the run that met it: the
	// product simply keeps the words a person chose, and the caller narrates a
	// skip. It lives here rather than in internal/store because store imports
	// this package for its row shapes and the reverse would be a cycle.
	ErrDraftLocked = errors.New("catalog: this draft was edited by the operator")

	// ErrSiteRefused means the address an operator typed is not one this daemon
	// will fetch: not http(s), or resolving somewhere that is not the public
	// internet. It is a sentence they can act on — "this is not a shop" — and a
	// 500 would blame us and say nothing, which is how a typo becomes a bug
	// report. Wrapped, so the refusal carries which address and why.
	ErrSiteRefused = errors.New("catalog: this address will not be scanned")

	// ErrSiteUnreadable means the shop answered but nothing usable came back.
	// Stored as nothing rather than as a blank theme: a preview painted from a
	// failed scan would tell the operator their shop is a white page.
	ErrSiteUnreadable = errors.New("catalog: the storefront's colours and type could not be read")
)

// Store is the persistence this package needs. It is an interface so the engine
// is testable without a database and so a nil store degrades rather than
// panics, which is the store contract everywhere else in this repo.
type Store interface {
	PutCatalogImport(ctx context.Context, imp StoredImport) error
	GetCatalogImport(ctx context.Context, id string) (StoredImport, bool, error)
	ListCatalogImports(ctx context.Context, limit int) ([]StoredImport, error)
	DeleteCatalogImport(ctx context.Context, id string) error

	PutCatalogProducts(ctx context.Context, importID string, rows []StoredProduct) error
	ListCatalogProducts(ctx context.Context, f ProductFilter) ([]StoredProduct, error)
	// CatalogStatusCounts is keyed import, then language, then status. The
	// source language is the empty Lang, which is what every row written
	// before languages existed is a decision about.
	CatalogStatusCounts(ctx context.Context) (map[string]map[Lang]map[string]int, error)
	GetCatalogProduct(ctx context.Context, id string, lang Lang) (StoredProduct, bool, error)
	// CatalogProductLangStatuses is the decision table for a set of products,
	// keyed product then language. The source language is absent — it does not
	// live there — and so is any target language nobody has decided in.
	CatalogProductLangStatuses(ctx context.Context, productIDs []string) (map[string]map[Lang]string, error)
	SetCatalogProductStatus(ctx context.Context, id string, lang Lang, status, reason string, at time.Time) error

	GetCatalogResearch(ctx context.Context, productID, version string) (Findings, bool, error)
	PutCatalogResearch(ctx context.Context, productID, version string, f Findings) error

	GetCatalogDraft(ctx context.Context, productID, version string) (StoredDraft, bool, error)
	GetCatalogDrafts(ctx context.Context, productIDs []string, version string) (map[string]StoredDraft, error)
	PutCatalogDraft(ctx context.Context, d StoredDraft) error
	ListCatalogDrafts(ctx context.Context, f DraftFilter) ([]DraftRow, error)
}

// DraftKey is one (import, language) pair and the version a draft is current
// under for it.
//
// The caller composes the version, never the store. A version is a cache key —
// the prompt constant, the model, the brand hash, the skill version and the
// language suffix — and a layer that could compose one here would be the second
// implementation of a format three callers already have to agree on. That
// disagreement has happened once in this package and its symptom was a screen
// that showed nothing at all.
type DraftKey struct {
	ImportID string
	Version  string
	Lang     Lang
}

// DraftFilter bounds a cross-import draft listing.
//
// Keys is a list of triples rather than two independent sets because the
// constraint is on the pair: one version string can be current for one import
// and superseded for another — two imports share a brand hash until one of
// their voices is edited — and a filter written as `version IN (…)` would
// surface the stale one as current.
type DraftFilter struct {
	Keys   []DraftKey
	Status string
	Limit  int
	Offset int
}

// OutputFilter is what an operator asked the outputs screen for.
//
// Langs is the one filter in this package where the zero Lang cannot double as
// "unset": "" is the source language and a real answer. Empty means every
// language this binary carries.
type OutputFilter struct {
	Langs   []Lang
	Dialect string
	Status  string
	Limit   int
	Offset  int
}

// OutputPage is one page of the outputs listing.
//
// There is no total. It was going to be a second COUNT over the same join on
// the first page, and then the screen turned out not to need one: a complete
// page counts its own rows exactly, and a truncated one says "this many and
// more", which is the honest reading either way. A field nothing ever fills is
// a contract that lies to the next person who reads it.
type OutputPage struct {
	Outputs []DraftRow `json:"outputs"`
	Limit   int        `json:"limit"`
	Offset  int        `json:"offset"`
	HasMore bool       `json:"has_more"`
}

// Studio is the engine: one value, wired once, shared by the HTTP routes, the
// MCP tools and the board card alike.
type Studio struct {
	cfg   config.Config
	store Store
	llm   Completer
	// research is the market half, and it is optional: a daemon with no
	// Crawl4AI container still imports catalogs, still learns a brand
	// vocabulary and still exports — it just cannot tell a rewrite what the
	// competition says. Absent is a named degradation, not a broken tool.
	research Researcher
	now      func() time.Time

	// selection and skillVersion are the two halves of a draft key this package
	// cannot know: which model the operator standing-chose, and the version of
	// the standing instructions the catalog agent runs under. Both are injected
	// at wiring rather than passed per call, because three callers — the HTTP
	// routes, the MCP tools and the board card — have to compose the *same* key
	// or a draft one of them wrote is a draft the others cannot find. They did
	// compose it separately once, and they drifted; see CurrentDraftVersion.
	selection    func() llm.Selection
	skillVersion func() string
	// site reads the operator's storefront. Optional: a studio without one
	// refuses to scan rather than storing an empty reading as the shop's look.
	site SiteReader
	// lookup resolves a storefront's host for the outbound guard. Nil means the
	// system resolver; the tests supply their own so the guard is exercised
	// without depending on live DNS.
	lookup lookupIPs
}

// New wires the engine. A nil store is allowed and means every read is a miss
// and every write is a no-op — the daemon still serves the routes and says so,
// rather than the routes vanishing.
func New(cfg config.Config, s Store, c Completer) *Studio {
	return &Studio{cfg: cfg, store: s, llm: c, now: time.Now}
}

// UseResearcher wires the market half. Separate from New because the pipeline
// it needs is built from three clients the daemon already owns, and a
// constructor argument would make cmd/mimir-mcp — which has no catalog at all —
// build one to pass nothing to.
func (s *Studio) UseResearcher(r Researcher) { s.research = r }

// UseDraftKey wires the two halves of a draft key that live outside this
// package: the operator's standing model choice and the version of the catalog
// agent's skills. Function values rather than values, because both change while
// the daemon runs — an operator switches model in Settings, or edits a skill —
// and a key composed from a snapshot taken at boot would keep pointing at
// drafts that are no longer the answer.
//
// Injected the way llm.Router takes its defaults, and for the same layering
// reason: internal/settings and internal/skills sit above this package.
func (s *Studio) UseDraftKey(selection func() llm.Selection, skillVersion func() string) {
	s.selection = selection
	s.skillVersion = skillVersion
}

// UseClock is for tests only.
func (s *Studio) UseClock(fn func() time.Time) { s.now = fn }

// Import reads one uploaded export, derives the brand kit, and stores both.
//
// The brand kit is derived here rather than lazily on first rewrite because it
// is the first thing an operator wants to see: "did it understand my store" is
// the question the import screen answers, and answering it later would mean
// answering it during a bulk run nobody is watching.
func (s *Studio) Import(ctx context.Context, filename string, data []byte, sel llm.Selection) (Import, error) {
	f, err := ParseCSV(strings.NewReader(string(data)))
	if err != nil {
		return Import{}, err
	}

	id := newID("imp")
	products := Products(id, f)
	if len(products) > s.cfg.CatalogMaxProductsPerImport {
		return Import{}, fmt.Errorf("%w: %d products, the ceiling is %d",
			ErrTooManyProducts, len(products), s.cfg.CatalogMaxProductsPerImport)
	}

	kit := s.DeriveBrand(ctx, f, products, sel)

	imp := Import{
		ID:           id,
		Filename:     filename,
		File:         f,
		Brand:        kit,
		ProductCount: len(products),
		CreatedAt:    s.now().UTC(),
	}
	if err := s.save(ctx, imp, products); err != nil {
		return Import{}, err
	}
	if f.Dialect.IsZero() {
		// Reported through the value, not as an error: the import is real and
		// addressable, it just cannot be read until the operator says which
		// column is which.
		imp.Note = "Bu başlık bilinen bir platforma uymadı; sütunları siz eşleyin."
	}
	return imp, nil
}

func (s *Studio) save(ctx context.Context, imp Import, products []Product) error {
	if s.store == nil {
		return nil
	}
	if err := s.store.PutCatalogImport(ctx, imp.stored()); err != nil {
		return err
	}
	rows := make([]StoredProduct, 0, len(products))
	for _, p := range products {
		rows = append(rows, StoredProduct{
			Product: p,
			Status:  StatusPending,
		})
	}
	return s.store.PutCatalogProducts(ctx, imp.ID, rows)
}

// Import is one uploaded file as a caller sees it.
type Import struct {
	ID       string   `json:"id"`
	Filename string   `json:"filename"`
	File     File     `json:"-"`
	Brand    BrandKit `json:"brand"`
	// Site is what the operator's storefront looks like, when they have pointed
	// at it. Beside Brand rather than inside it on purpose: BrandKit.hash is a
	// draft cache key, and a colour correction must not discard a catalogue of
	// approved copy.
	Site         SiteScan  `json:"site,omitzero"`
	ProductCount int       `json:"product_count"`
	CreatedAt    time.Time `json:"created_at"`
	Note         string    `json:"note,omitempty"`
}

// Status is where one product stands. The vocabulary is closed and the strings
// are wire values — a client keys on them and a stored row records them.
const (
	StatusPending    = "pending"
	StatusResearched = "researched"
	StatusDrafted    = "drafted"
	StatusApproved   = "approved"
	StatusRejected   = "rejected"
	StatusFailed     = "failed"
)

// Statuses is every status, in the order a UI should group them.
func Statuses() []string {
	return []string{StatusPending, StatusResearched, StatusDrafted, StatusApproved, StatusRejected, StatusFailed}
}

// ValidStatus reports whether a wire string names one.
func ValidStatus(s string) bool {
	for _, v := range Statuses() {
		if v == s {
			return true
		}
	}
	return false
}

// ProductFilter bounds a product listing. There is no "everything" read: the
// ceiling is the caller's, and a client that asks for a whole catalog is asking
// the daemon to hold a table it cannot render anyway.
type ProductFilter struct {
	ImportID string
	// Lang is which language's decision Status filters on, and which language's
	// decision the returned rows carry. The zero value is the file's own
	// language, which is what every filter written before this field existed
	// meant and what every stored row in catalog_products records.
	Lang     Lang
	Status   string
	Category string
	Limit    int
	Offset   int
}

func newID(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Time is a poor id and a good fallback: it never collides within one
		// process and the alternative is failing an import over entropy.
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}
