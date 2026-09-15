package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/logrenant/mimir/internal/catalog"
	"github.com/logrenant/mimir/internal/config"
)

// The catalog's MCP surface: two reads, and deliberately nothing that writes.
//
// Importing a CSV is not here. internal/api/AGENTS.md admits a filesystem path
// at exactly one route, and an import is a file somebody drops on a screen —
// putting it behind a tool would mean either a path argument or a base64 blob
// in a session's context, and both are worse than the button that exists.
//
// Queuing a bulk rewrite is not here either, and that is a change of mind worth
// recording rather than hiding. It would be the one tool in this registry whose
// entire effect is to spend money — a catalog's worth of searches, crawls and
// reason calls — from a sentence somebody typed. The route that queues it
// (POST /catalog/rewrite) takes an explicit list of products for exactly that
// reason, and it sits on a screen where the operator has just looked at them.
// A tool would either repeat that guard badly or drop it, and internal/tools
// would have to grow a dependency on the queue to offer it at all.

// CatalogReader is what these tools read through.
type CatalogReader interface {
	Get(ctx context.Context, id string) (catalog.Import, error)
	List(ctx context.Context, limit int) ([]catalog.Import, error)
	Products(ctx context.Context, f catalog.ProductFilter, version string) ([]catalog.ProductView, error)
	Product(ctx context.Context, productID string, lang catalog.Lang, version string) (catalog.ProductView, error)
	// CurrentDraftVersion is the key a stored draft is written under, composed
	// by the studio from the operator's model choice and the catalog agent's
	// skill version. These tools ask rather than compose: they used to pass a
	// zero selection and an empty skill version, so they looked up a key
	// nothing had ever been written to and reported every product as having no
	// draft at all.
	CurrentDraftVersion(ctx context.Context, importID string, lang catalog.Lang) (string, error)
}

// --- catalog_products --------------------------------------------------------

// CatalogProductsTool lists what is in an import.
type CatalogProductsTool struct {
	cfg     config.Config
	catalog CatalogReader
}

func NewCatalogProducts(cfg config.Config, c CatalogReader) *CatalogProductsTool {
	return &CatalogProductsTool{cfg: cfg, catalog: c}
}

func (t *CatalogProductsTool) Name() string { return "catalog_products" }

func (t *CatalogProductsTool) Description() string {
	return "Lists the products of an imported e-commerce catalog with their rewrite status " +
		"(pending/researched/drafted/approved/rejected/failed). Metadata only — titles, SKUs, " +
		"categories and statuses, never description text (≤ ~1200 tokens). " +
		"Omit import_id to list the imports themselves."
}

func (t *CatalogProductsTool) InputSchema() json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{
		"type": "object",
		"properties": {
			"import_id": { "type": "string", "description": "The import to list. Omit to list imports." },
			"status": { "type": "string", "enum": ["pending","researched","drafted","approved","rejected","failed"] },
			"category": { "type": "string" },
			"limit": { "type": "integer", "minimum": 1, "maximum": %d }
		},
		"additionalProperties": false
	}`, t.cfg.CatalogProductsPageMax))
}

type catalogProductsArgs struct {
	ImportID string `json:"import_id"`
	Status   string `json:"status"`
	Category string `json:"category"`
	Limit    int    `json:"limit"`
}

type catalogImportLine struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Products int    `json:"products"`
}

type catalogProductLine struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	SKU      string `json:"sku,omitempty"`
	Category string `json:"category,omitempty"`
	Status   string `json:"status"`
	HasDraft bool   `json:"has_draft"`
}

type catalogProductsResponse struct {
	Imports  []catalogImportLine  `json:"imports,omitempty"`
	Products []catalogProductLine `json:"products,omitempty"`
	Total    int                  `json:"total"`

	// MetadataOnly, like web_search's. Nothing here is page-derived: a title
	// and an SKU are the operator's own spreadsheet, and the description text
	// this tool deliberately does not carry is what would have needed refining.
	Meta   bool `json:"metadata_only"`
	budget int  `json:"-"`
}

func (r catalogProductsResponse) MetadataOnly() bool    { return r.Meta }
func (r catalogProductsResponse) SizeBudgetTokens() int { return r.budget }

func (t *CatalogProductsTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var in catalogProductsArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	if strings.TrimSpace(in.ImportID) == "" {
		imports, err := t.catalog.List(ctx, 25)
		if err != nil {
			return nil, catalogError(err)
		}
		out := catalogProductsResponse{Meta: true, budget: 1200}
		for _, imp := range imports {
			out.Imports = append(out.Imports, catalogImportLine{
				ID: imp.ID, Filename: imp.Filename, Products: imp.ProductCount,
			})
		}
		out.Total = len(out.Imports)
		return out, nil
	}

	limit := in.Limit
	if limit <= 0 || limit > t.cfg.CatalogProductsPageMax {
		limit = 50
	}
	version, err := t.version(ctx, in.ImportID)
	if err != nil {
		return nil, catalogError(err)
	}
	products, err := t.catalog.Products(ctx, catalog.ProductFilter{
		ImportID: in.ImportID, Status: in.Status, Category: in.Category, Limit: limit,
	}, version)
	if err != nil {
		return nil, catalogError(err)
	}

	out := catalogProductsResponse{Meta: true, budget: 1200, Total: len(products)}
	for _, p := range products {
		out.Products = append(out.Products, catalogProductLine{
			ID: p.ID, Title: p.Original.Title, SKU: p.SKU,
			Category: p.Category, Status: p.Status, HasDraft: p.Draft != nil,
		})
	}
	return out, nil
}

func (t *CatalogProductsTool) version(ctx context.Context, importID string) (string, error) {
	return t.catalog.CurrentDraftVersion(ctx, importID, catalog.LangSource)
}

// --- catalog_product ---------------------------------------------------------

// CatalogProductTool reads one product's copy, current and drafted.
type CatalogProductTool struct {
	cfg     config.Config
	catalog CatalogReader
}

func NewCatalogProduct(cfg config.Config, c CatalogReader) *CatalogProductTool {
	return &CatalogProductTool{cfg: cfg, catalog: c}
}

func (t *CatalogProductTool) Name() string { return "catalog_product" }

func (t *CatalogProductTool) Description() string {
	return "Reads one catalog product: its current listing and the rewritten draft beside it, " +
		"as plain text rather than the store's HTML (≤ ~1500 tokens). " +
		"Use catalog_products to find an id."
}

func (t *CatalogProductTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"product_id": { "type": "string", "minLength": 1 }
		},
		"required": ["product_id"],
		"additionalProperties": false
	}`)
}

type catalogProductArgs struct {
	ProductID string `json:"product_id"`
}

type catalogListing struct {
	Title          string   `json:"title,omitempty"`
	Description    []string `json:"description,omitempty"`
	SEOTitle       string   `json:"seo_title,omitempty"`
	SEODescription string   `json:"seo_description,omitempty"`
	Tags           string   `json:"tags,omitempty"`
}

type catalogProductResponse struct {
	ID      string          `json:"id"`
	Status  string          `json:"status"`
	Current catalogListing  `json:"current"`
	Draft   *catalogListing `json:"draft,omitempty"`
	Notes   []string        `json:"notes,omitempty"`

	// The description is page-derived text — it is the merchant's own HTML,
	// flattened to the blocks internal/catalog already parsed it into. It never
	// carries markup and it is bounded, which is what refined means here.
	Refined bool `json:"refined"`
	budget  int  `json:"-"`
}

func (r catalogProductResponse) IsRefined() bool       { return r.Refined }
func (r catalogProductResponse) SizeBudgetTokens() int { return r.budget }

func (t *CatalogProductTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var in catalogProductArgs
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(in.ProductID) == "" {
		return nil, errors.New("invalid arguments: product_id is required")
	}

	view, err := t.catalog.Product(ctx, in.ProductID, catalog.LangSource, "")
	if err != nil {
		return nil, catalogError(err)
	}
	// The version is resolved from the product's own import, so a caller cannot
	// name a cache key and be served copy written under a brand voice that no
	// longer exists.
	if version, err := t.catalog.CurrentDraftVersion(ctx, view.ImportID, catalog.LangSource); err == nil {
		if fresh, err := t.catalog.Product(ctx, in.ProductID, catalog.LangSource, version); err == nil {
			view = fresh
		}
	}

	out := catalogProductResponse{
		ID: view.ID, Status: view.Status, Refined: true, budget: 1500,
		Current: listingOf(view.Original),
	}
	if view.Draft != nil {
		d := listingOf(view.Draft.Content)
		out.Draft = &d
		out.Notes = view.Draft.Notes
	}
	return out, nil
}

// listingOf flattens a listing to text. The description arrives as the blocks
// internal/catalog parsed it into, so what leaves here is prose and never
// markup — the tags are not the consumer's business and would only spend its
// context.
func listingOf(c catalog.Content) catalogListing {
	out := catalogListing{
		Title: c.Title, SEOTitle: c.SEOTitle,
		SEODescription: c.SEODescription, Tags: c.Tags,
	}
	if doc, err := catalog.ParseHTML(c.DescriptionHTML); err == nil {
		for _, b := range doc.Blocks {
			if s := strings.TrimSpace(b.Text); s != "" {
				out.Description = append(out.Description, s)
			}
		}
	}
	return out
}

func catalogError(err error) error {
	switch {
	case errors.Is(err, catalog.ErrUnknownImport):
		return errors.New("no such catalog import — call catalog_products with no arguments to list them")
	case errors.Is(err, catalog.ErrUnknownProduct):
		return errors.New("no such product — call catalog_products with an import_id to list them")
	}
	return err
}
