package catalog

import (
	"encoding/json"
	"time"
)

// The row shapes the store persists. They live here rather than in
// internal/store because they are this package's vocabulary — the store's job
// is to write them down, not to decide what a catalog product is.

// StoredImport is one import as a row.
//
// The whole File is carried, header and every raw cell, because that is what
// lossless export means: the bytes read are the bytes written back for every
// cell nobody approved a change for, and re-deriving them from a file the
// operator may have since deleted is not something this package will attempt.
type StoredImport struct {
	ID           string    `json:"id"`
	Filename     string    `json:"filename"`
	Dialect      string    `json:"dialect"`
	ProductCount int       `json:"product_count"`
	CreatedAt    time.Time `json:"created_at"`

	// FileJSON and BrandJSON are stored opaque. A schema change here is a
	// migration of meaning, not of columns, so it belongs to a version
	// constant rather than to a new table.
	FileJSON  json.RawMessage `json:"file"`
	BrandJSON json.RawMessage `json:"brand"`
	// SiteJSON is the storefront reading, and it is its own column for the
	// reason 0027 gives: BrandKit.hash is a draft cache key and a colour is not
	// evidence about copy. Empty for every import nobody has pointed at a shop.
	SiteJSON json.RawMessage `json:"site,omitempty"`
}

func (i Import) stored() StoredImport {
	fileJSON, _ := json.Marshal(i.File)
	brandJSON, _ := json.Marshal(i.Brand)
	var siteJSON json.RawMessage
	if i.Site.URL != "" {
		siteJSON, _ = json.Marshal(i.Site)
	}
	return StoredImport{
		ID:           i.ID,
		Filename:     i.Filename,
		Dialect:      i.File.Dialect.Key,
		ProductCount: i.ProductCount,
		CreatedAt:    i.CreatedAt,
		FileJSON:     fileJSON,
		BrandJSON:    brandJSON,
		SiteJSON:     siteJSON,
	}
}

// value rebuilds an Import from its row.
//
// `Dialect` is carried across even when the file body is not: a listing
// deliberately leaves `file_json` on disk (one screen would otherwise cost as
// much to open as every import at once), and the profile that matched is the
// one thing about the file the list still has to name.
func (r StoredImport) value() (Import, error) {
	imp := Import{
		ID: r.ID, Filename: r.Filename,
		ProductCount: r.ProductCount, CreatedAt: r.CreatedAt,
	}
	imp.File.Dialect.Key = r.Dialect
	if len(r.FileJSON) > 0 {
		if err := json.Unmarshal(r.FileJSON, &imp.File); err != nil {
			return Import{}, err
		}
	}
	if len(r.BrandJSON) > 0 {
		if err := json.Unmarshal(r.BrandJSON, &imp.Brand); err != nil {
			return Import{}, err
		}
	}
	if len(r.SiteJSON) > 0 {
		if err := json.Unmarshal(r.SiteJSON, &imp.Site); err != nil {
			return Import{}, err
		}
	}
	// A stored dialect of "" means the profile table did not match this header
	// *at the time it was imported*. Detection is a pure function of the header
	// and the table is code, so a profile added later applies to files imported
	// before it — an operator whose IKAS export came up unrecognised does not
	// have to re-upload a thousand products to benefit from the profile that
	// now reads it. Their own mapping still wins: a hand-made map exists
	// precisely because we failed them once, and it is not ours to discard.
	//
	// The test is the operator's mapping alone, not also "did nothing match".
	// It used to be both, which made the sentence above false in the case that
	// matters most: a file that already matched kept the columns it was bound
	// to at import time forever, so a column added to a profile later — the
	// Arabic body on the IKAS custom-fields export — reached no import anybody
	// already had.
	if len(imp.File.Mapping) == 0 {
		if d, ok := Detect(imp.File.Header); ok {
			imp.File.Dialect = d
		}
	}
	return imp, nil
}

// StoredProduct is one product as a row: what it was, and where it stands.
//
// Status is the decision in Lang, not a decision about the product as a whole.
// That is the change per-language approval makes and it is deliberately made in
// the meaning of an existing field rather than in a new one: a client that
// names no language asks about the source language, gets catalog_products' own
// column, and gets Lang and SourceStatus omitted — byte-identical to the
// response it has always had.
type StoredProduct struct {
	Product
	// Lang is which language Status is about. Omitted for the source language,
	// because the zero value is what every stored row already means.
	Lang   Lang   `json:"lang,omitempty"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	// SourceStatus is the file's own language's decision, carried beside a
	// target language's so a screen can show that approving the Arabic did not
	// move the Turkish — without a second request. Empty on a source-language
	// read, where it would only repeat Status.
	SourceStatus string    `json:"source_status,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitzero"`
	// Statuses is every language's decision about this product, so a table that
	// draws one column per language does not need one request per language.
	// Asking per language is what made the language a mode on that screen.
	//
	// The source language is always present; a target language appears only
	// once something has been decided in it, because absence is pending and
	// synthesising an entry for every language this binary can write would put
	// an Arabic reading on a file with no Arabic column. Read-side only: it is
	// composed per request and never stored, like SourceStatus.
	Statuses map[Lang]string `json:"statuses,omitempty"`
}

// DraftRow is one generated output as the cross-import listing sees it: which
// file it belongs to, which product, which language, and what it changed.
//
// Product and Content travel but do not serialise. Changed is computed from
// them here rather than in a client for four reasons, and the first is enough:
// a client that diffed for itself would need the original, which means shipping
// every product's description HTML beside every draft's — a listing that costs
// as much to open as opening every product at once.
type DraftRow struct {
	ImportID string `json:"import_id"`
	Filename string `json:"filename"`
	Dialect  string `json:"dialect"`

	ProductID string `json:"product_id"`
	Title     string `json:"title"`
	Handle    string `json:"handle"`

	Lang   Lang   `json:"lang"`
	Status string `json:"status"`

	// Fields is what the pass was asked to write; Changed is what actually
	// differs from the cell this draft would be exported into. They are not the
	// same list and a screen wants the second one.
	Fields  []string `json:"fields,omitempty"`
	Changed []string `json:"changed,omitempty"`

	Provider         string `json:"provider,omitempty"`
	Model            string `json:"model,omitempty"`
	EditedByOperator bool   `json:"edited_by_operator"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	Product Product `json:"-"`
	Content Content `json:"-"`
}

// StoredDraft is one rewrite, keyed by the product and the version that
// produced it.
//
// EditedByOperator is the guard that makes a re-run safe: a draft somebody
// corrected by hand is never overwritten by another pass, and the guard is
// enforced in SQL rather than trusted to a caller.
type StoredDraft struct {
	ProductID string    `json:"product_id"`
	Version   string    `json:"version"`
	Content   Content   `json:"content"`
	Fields    []string  `json:"fields"`
	Notes     []string  `json:"notes,omitempty"`
	Provider  string    `json:"provider,omitempty"`
	Model     string    `json:"model,omitempty"`
	CreatedAt time.Time `json:"created_at"`

	EditedByOperator bool `json:"edited_by_operator"`
}
