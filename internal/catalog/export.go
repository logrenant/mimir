package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Export writes the catalog back out.
//
// The contract is that a cell nobody approved a change for is *copied*, not
// re-derived: the raw string that was read is the raw string that is written.
// That is why an import with no approved drafts produces the file it was given,
// down to the columns this package has no name for.
//
// approved is keyed by product id and then by language. A product absent from
// it is untouched, and so is a language absent from its entry.
//
// Every language is written in one pass over one row set, because the file is
// one file: an export that wrote only the source language would leave the
// Arabic column saying what it said before the operator approved a change to
// it, and two exports would each undo the other's work.
func Export(f File, products []Product, approved map[string]map[Lang]Content) ([]byte, error) {
	rows := make([][]string, len(f.Rows))
	for i, r := range f.Rows {
		rows[i] = append([]string(nil), r...)
	}

	for _, p := range products {
		byLang, ok := approved[p.ID]
		if !ok {
			continue
		}
		for _, lang := range f.Langs() {
			draft, ok := byLang[lang]
			if !ok {
				continue
			}
			for _, field := range Fields() {
				if !field.Writable() {
					continue
				}
				lf := LangField{Field: field, Lang: lang}
				col := f.index(lf)
				if col < 0 {
					continue
				}
				v := draft.Get(field)
				// Compared against this language's own original, not the
				// source language's: an Arabic cell equal to the Turkish one
				// is a change, and an Arabic cell equal to the Arabic one is
				// not.
				if v == p.Content(lang).Get(field) {
					continue
				}
				// Write everywhere the value was read from. On a Shopify export
				// that is one row — the handle's first, the only one carrying
				// Title and Body (HTML) — so its variant rows stay untouched. On
				// an IKAS export the description repeats on every variant row, and
				// writing only the first would leave the file disagreeing with
				// itself about what the product says.
				for _, at := range p.rowsFor(f, lf) {
					if at < 0 || at >= len(rows) {
						continue
					}
					for col >= len(rows[at]) {
						rows[at] = append(rows[at], "")
					}
					rows[at][col] = v
				}
			}
		}
	}
	return WriteCSV(f, rows)
}

// rowsFor is every row this product's value for a field lives on: the rows
// that had it filled, or failing that the product's first row.
//
// The fallback is per language on purpose. A target-language column that was
// empty on every row — the usual case the first time a store writes Arabic —
// has no row that "carried the value", and writing nowhere would make the pass
// silently do nothing. Falling back to the row the product's source-language
// content sits on puts the translation beside the thing it translates.
func (p Product) rowsFor(f File, lf LangField) []int {
	var out []int
	for _, i := range p.Rows {
		if i < 0 || i >= len(f.Rows) {
			continue
		}
		if f.cell(f.Rows[i], lf) != "" {
			out = append(out, i)
		}
	}
	if len(out) > 0 {
		return out
	}
	if lf.Lang != LangSource {
		// Follow the source language's own rows: on an IKAS export that is
		// every variant row, on a Shopify one it is the handle's first.
		if src := p.rowsFor(f, LangField{Field: lf.Field}); len(src) > 0 {
			return src
		}
	}
	if len(p.Rows) == 0 {
		return nil
	}
	return p.Rows[:1]
}

var unsafeFileChars = regexp.MustCompile(`[^\p{L}\p{N}._-]+`)

// WriteExport puts the bytes in the exports directory, beside the lead-gen
// workbooks. One place for every file this app writes.
func WriteExport(dir, sourceName string, data []byte, now time.Time) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("catalog: export directory is empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("catalog: create export directory: %w", err)
	}
	path := filepath.Join(dir, exportFileName(sourceName, now))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("catalog: write export: %w", err)
	}
	return path, nil
}

func exportFileName(sourceName string, now time.Time) string {
	base := strings.TrimSuffix(filepath.Base(sourceName), filepath.Ext(sourceName))
	base = unsafeFileChars.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "katalog"
	}
	return fmt.Sprintf("%s-mimir-%s.csv", base, now.Format("20060102-150405"))
}
