package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Product is one sellable thing, assembled from one or more rows of the export.
//
// Rows holds the indexes into File.Rows that belong to it, in file order. The
// export writes through those indexes, which is how a Shopify product with
// eleven variant rows gets its description rewritten once and its other ten
// rows copied through untouched.
type Product struct {
	// ID is derived from the import and the product's own key, so re-importing
	// the same file addresses the same products and a stored draft is still
	// found. It is not a random id for exactly that reason.
	ID       string `json:"id"`
	ImportID string `json:"import_id"`

	Key      string `json:"key"` // handle, or SKU, or the row number
	Handle   string `json:"handle"`
	SKU      string `json:"sku"`
	Category string `json:"category"`

	Rows []int `json:"rows"`

	Original Content `json:"original"`
	// Translations is what the file already said in each target language.
	//
	// It is read for two reasons. Export compares against it to decide whether
	// a cell changed, so a language whose copy an operator already wrote is not
	// rewritten with the same bytes. And a link the merchant already shipped in
	// the Arabic cell is a link a person put there, which is a different URL
	// policy from one a model produced.
	Translations map[Lang]Content `json:"translations,omitempty"`
}

// Content is the writable half of a product: what a rewrite reads and what it
// may replace. Everything else about a row is copied, never modelled.
type Content struct {
	Title           string `json:"title"`
	DescriptionHTML string `json:"description_html"`
	SEOTitle        string `json:"seo_title"`
	SEODescription  string `json:"seo_description"`
	Tags            string `json:"tags"`
}

// Get reads one writable field.
func (c Content) Get(f Field) string {
	switch f {
	case FieldTitle:
		return c.Title
	case FieldDescriptionHTML:
		return c.DescriptionHTML
	case FieldSEOTitle:
		return c.SEOTitle
	case FieldSEODescription:
		return c.SEODescription
	case FieldTags:
		return c.Tags
	}
	return ""
}

// Set writes one writable field. A non-writable field is ignored rather than
// rejected: the caller is iterating a list, and identity fields being
// unwritable is a property of this type, not an error at the call site.
func (c *Content) Set(f Field, v string) {
	switch f {
	case FieldTitle:
		c.Title = v
	case FieldDescriptionHTML:
		c.DescriptionHTML = v
	case FieldSEOTitle:
		c.SEOTitle = v
	case FieldSEODescription:
		c.SEODescription = v
	case FieldTags:
		c.Tags = v
	}
}

// IsZero reports whether nothing at all was read.
func (c Content) IsZero() bool {
	return c.Title == "" && c.DescriptionHTML == "" && c.SEOTitle == "" &&
		c.SEODescription == "" && c.Tags == ""
}

// Products groups a file's rows into products.
//
// When the dialect names a GroupBy field, consecutive rows sharing that value
// become one product and each writable field is taken from the first row that
// has it filled. That last part is the whole reason grouping exists: Shopify
// writes Title and Body (HTML) on a handle's first row only, so reading row by
// row would produce one real product followed by ten empty ones.
func Products(importID string, f File) []Product {
	group := f.Dialect.GroupBy
	if f.Dialect.IsZero() {
		// An operator's own mapping may still name a column that joins variant
		// rows; if it does, grouping is as meaningful as it is for a known
		// dialect. A group id is the stronger claim of the two, so it wins.
		for _, candidate := range []Field{FieldGroupID, FieldHandle} {
			if _, ok := f.Mapping[candidate]; ok {
				group = candidate
				break
			}
		}
	}

	langs := f.Langs()

	var out []Product
	byKey := map[string]int{} // key → index in out

	for i, row := range f.Rows {
		key := ""
		if group != "" {
			key = strings.TrimSpace(f.cell(row, LangField{Field: group}))
		}
		if key == "" {
			// No grouping key: this row is its own product. Keyed by row
			// number so two blank-handle rows do not collapse into one.
			key = fmt.Sprintf("#%d", i)
		}

		at, seen := byKey[key]
		if !seen {
			p := Product{
				ID:       productID(importID, key),
				ImportID: importID,
				Key:      key,
				Handle:   f.cell(row, LangField{Field: FieldHandle}),
				SKU:      f.cell(row, LangField{Field: FieldSKU}),
				Category: f.cell(row, LangField{Field: FieldCategory}),
			}
			out = append(out, p)
			at = len(out) - 1
			byKey[key] = at
		}

		p := &out[at]
		p.Rows = append(p.Rows, i)
		// First non-empty wins, per field and per language. A target language
		// the file does not carry contributes nothing and leaves Translations
		// nil, so a single-language import is byte-identical to what it was.
		for _, lang := range langs {
			for _, fl := range []Field{FieldTitle, FieldDescriptionHTML, FieldSEOTitle, FieldSEODescription, FieldTags} {
				lf := LangField{Field: fl, Lang: lang}
				if p.Content(lang).Get(fl) != "" {
					continue
				}
				v := f.cell(row, lf)
				if v == "" {
					continue
				}
				if lang == LangSource {
					p.Original.Set(fl, v)
					continue
				}
				if p.Translations == nil {
					p.Translations = map[Lang]Content{}
				}
				c := p.Translations[lang]
				c.Set(fl, v)
				p.Translations[lang] = c
			}
		}
		if p.SKU == "" {
			p.SKU = f.cell(row, LangField{Field: FieldSKU})
		}
		if p.Category == "" {
			p.Category = f.cell(row, LangField{Field: FieldCategory})
		}
	}
	return out
}

// productID is stable across re-imports of the same file: same import, same
// key, same id. A random id would orphan every stored draft the moment an
// operator re-uploaded a corrected export.
func productID(importID, key string) string {
	sum := sha256.Sum256([]byte(importID + "\x00" + key))
	return "prd_" + hex.EncodeToString(sum[:8])
}
