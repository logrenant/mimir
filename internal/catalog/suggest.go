package catalog

import "strings"

// Suggest is a best guess at the column map of a header no profile matched.
//
// It exists because the alternative is a form with eight empty dropdowns over a
// thirty-seven column file, which is what a store sees the first time its
// platform is one this package has never met. A guess the operator can correct
// in one click is worth more than a blank form, and it is a *guess*: nothing
// here decides anything, SetMapping still takes whatever the operator sends.
//
// It costs no model call and is deterministic — the same header always yields
// the same map. Two fields never claim the same column: the first field to
// want a column takes it, in Fields() order, so a file with one "Açıklama" and
// one "Metadata Açıklama" does not map both to the description.
func Suggest(header []string) map[Field]string {
	have := make(map[string]string, len(header))
	for _, h := range header {
		n := normalizeHeader(h)
		if n == "" {
			continue
		}
		// First spelling wins, so a duplicated column name resolves to the
		// leftmost one — the one an exporter filled.
		if _, seen := have[n]; !seen {
			have[n] = h
		}
	}

	out := map[Field]string{}
	taken := map[string]bool{}

	for _, f := range Fields() {
		for _, name := range synonyms(f) {
			actual, ok := have[normalizeHeader(name)]
			if !ok || taken[actual] {
				continue
			}
			out[f] = actual
			taken[actual] = true
			break
		}
	}
	return out
}

// synonyms is the ordered list of header spellings that mean a field: every
// known profile's own column name first, then the spellings other exporters
// and hand-edited sheets use. Order is preference order — an exact platform
// column beats a generic word.
//
// It is a constant table (SD-1), not a setting. Widening it is a code change
// with a fixture, which is the only way a claim like "this column is the
// description" gets reviewed.
func synonyms(f Field) []string {
	out := make([]string, 0, 8)
	for _, d := range dialects {
		if col, ok := d.Columns[f]; ok && col != "" {
			out = append(out, col)
		}
	}
	return append(out, generic[f]...)
}

var generic = map[Field][]string{
	FieldGroupID: {"Product Group ID", "Group ID", "Grup ID", "Ürün Grubu"},
	FieldHandle:  {"Handle", "Slug", "URL", "Permalink", "Link"},
	FieldSKU:     {"SKU", "Stok Kodu", "Stock Code", "Barkod", "Barcode", "Ürün Kodu"},
	FieldTitle: {
		"İsim", "Isim", "Ürün Adı", "Urun Adi", "Ad", "Başlık",
		"Name", "Product Name", "Title", "Product Title",
	},
	FieldDescriptionHTML: {
		"Açıklama", "Aciklama", "Ürün Açıklaması", "Detay", "İçerik",
		"Description", "Product Description", "Body (HTML)", "Body", "Content",
	},
	FieldSEOTitle: {
		"Metadata Başlık", "SEO Başlık", "SEO Baslik", "Meta Başlık", "Meta Title",
		"SEO Title", "Page Title",
	},
	FieldSEODescription: {
		"Metadata Açıklama", "SEO Açıklama", "Meta Açıklama", "Meta Description",
		"SEO Description",
	},
	FieldTags:     {"Etiketler", "Etiket", "Tags", "Keywords", "Anahtar Kelimeler"},
	FieldCategory: {"Kategoriler", "Kategori", "Categories", "Category", "Type", "Ürün Tipi"},
}

// enoughToRead reports whether a column map names something a product can be
// built out of. A row with neither a title nor a description is a price list,
// not a catalogue this tool has anything to say about.
func enoughToRead(m map[Field]string) bool {
	return m[FieldTitle] != "" || m[FieldDescriptionHTML] != ""
}

// Readable reports whether this file can be turned into products: a profile
// matched, or the operator's own mapping names enough.
func (f File) Readable() bool {
	if !f.Dialect.IsZero() {
		return true
	}
	return enoughToRead(f.Mapping)
}

// Sample is one example value per column, taken from the file's first data row
// and trimmed. A column in a thirty-seven column export is told apart by what
// is in it far more reliably than by what it is called, and the operator doing
// the telling apart is looking at a dropdown.
func (f File) Sample(max int) map[string]string {
	if len(f.Rows) == 0 {
		return map[string]string{}
	}
	row := f.Rows[0]
	out := make(map[string]string, len(f.Header))
	for i, h := range f.Header {
		if i >= len(row) {
			break
		}
		v := strings.TrimSpace(row[i])
		if v == "" {
			continue
		}
		out[h] = trimRunes(v, max)
	}
	return out
}

// trimRunes cuts on rune boundaries. A byte cut through "ş" is mojibake in the
// dropdown of the very screen that exists to make a Turkish export legible.
func trimRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max])) + "…"
}
