package catalog

import "strings"

// Field is a column this package knows how to read and, for some of them,
// rewrite. It is a closed set and the values are wire strings — they land in
// an operator's saved column mapping and in a run row's params — so they are
// never renamed, only added to.
type Field string

const (
	// FieldHandle joins several rows into one product. It is read and never
	// written: a handle is the product's URL, and rewriting it turns every
	// link and every ranking that pointed at the old one into a 404. That is
	// not a rewrite, it is a migration, and this tool does not do migrations.
	FieldHandle Field = "handle"
	FieldSKU    Field = "sku"
	// FieldGroupID joins a platform's variant rows into one product. It is an
	// internal identity — IKAS writes a UUID here — so it is read, never
	// written, and never shown as if it meant something to a shopper.
	FieldGroupID Field = "group_id"

	FieldTitle           Field = "title"
	FieldDescriptionHTML Field = "description_html"
	FieldSEOTitle        Field = "seo_title"
	FieldSEODescription  Field = "seo_description"
	FieldTags            Field = "tags"
	FieldCategory        Field = "category"
)

// Fields is every field, in the order a UI should offer them.
func Fields() []Field {
	return []Field{
		FieldGroupID, FieldHandle, FieldSKU, FieldTitle, FieldDescriptionHTML,
		FieldSEOTitle, FieldSEODescription, FieldTags, FieldCategory,
	}
}

// Writable is whether a rewrite may touch this field. FieldGroupID,
// FieldHandle and FieldSKU are identity, not content.
func (f Field) Writable() bool {
	switch f {
	case FieldTitle, FieldDescriptionHTML, FieldSEOTitle, FieldSEODescription, FieldTags:
		return true
	}
	return false
}

// Dialect is one platform's export layout.
type Dialect struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	// Signature is the set of headers that identifies this platform. It is
	// matched as a *subset* of the file's header, so a store that added
	// metafield columns — which every real store has — still resolves.
	Signature []string `json:"-"`
	// GroupBy is the field whose value joins consecutive rows into one
	// product. Empty means one row is one product.
	GroupBy Field `json:"group_by"`
	// Columns maps a field to the header that carries it, in the file's own
	// language.
	Columns map[Field]string `json:"columns"`
	// Translations is the same map per target language. Absent means this
	// profile carries one language and one only, which is what every profile
	// shipped before this field was.
	//
	// It is a new key rather than a retyping of Columns because a Dialect is
	// serialised whole into catalog_imports.file_json: changing the shape of
	// Columns would fail json.Unmarshal on every row already stored, and
	// Studio.List swallows an unreadable row on purpose, so the symptom would
	// be every operator's catalog screen quietly going empty.
	Translations map[Lang]map[Field]string `json:"translations,omitempty"`
	// TargetColumns is a translation surface whose *language the file does not
	// record*. See Dialect.Targets in lang.go for why that is a category of its
	// own rather than an entry in Translations.
	TargetColumns map[Field]string `json:"target_columns,omitempty"`
}

// IsZero reports whether no platform matched.
func (d Dialect) IsZero() bool { return d.Key == "" }

// dialects is the closed set, in detection order. Adding a platform means
// adding a profile here and a fixture under testdata — not a code path.
var dialects = []Dialect{
	{
		Key:  "shopify",
		Name: "Shopify",
		// Shopify writes one row per variant and fills Title/Body (HTML) only
		// on the first row of each handle, which is what GroupBy is for.
		Signature: []string{"Handle", "Title", "Body (HTML)", "Variant SKU"},
		GroupBy:   FieldHandle,
		Columns: map[Field]string{
			FieldHandle:          "Handle",
			FieldSKU:             "Variant SKU",
			FieldTitle:           "Title",
			FieldDescriptionHTML: "Body (HTML)",
			FieldSEOTitle:        "SEO Title",
			FieldSEODescription:  "SEO Description",
			FieldTags:            "Tags",
			FieldCategory:        "Type",
		},
	},
	{
		Key:  "ikas",
		Name: "IKAS",
		// Taken verbatim from a real IKAS product export. The profile that
		// stood here before was guessed — "Ürün Adı", "Stok Kodu", "Kategori"
		// — and matched nothing a store actually downloads.
		Signature: []string{"Ürün Grup ID", "İsim", "Açıklama", "SKU"},
		GroupBy:   FieldGroupID,
		Columns: map[Field]string{
			FieldGroupID:         "Ürün Grup ID",
			FieldHandle:          "Slug",
			FieldSKU:             "SKU",
			FieldTitle:           "İsim",
			FieldDescriptionHTML: "Açıklama",
			FieldSEOTitle:        "Metadata Başlık",
			FieldSEODescription:  "Metadata Açıklama",
			FieldTags:            "Etiketler",
			FieldCategory:        "Kategoriler",
		},
	},
	{
		Key:  "ikas-ceviriler",
		Name: "IKAS (çeviriler)",
		// Taken verbatim from a real export of IKAS's Çeviriler page. It is the
		// translation surface proper: the store's default-language columns and,
		// beside each, the column its translation goes in.
		//
		// "Çevrilecek Meta Slug" is deliberately not mapped. It is the product's
		// URL, FieldHandle is read and never written, and the real export has it
		// empty on all 1013 rows — IKAS does not translate a slug either.
		Signature: []string{"Ürün Grup ID", "İsim", "Açıklama", "Çevrilecek İsim", "Çevrilecek Açıklama"},
		GroupBy:   FieldGroupID,
		Columns: map[Field]string{
			FieldGroupID:         "Ürün Grup ID",
			FieldHandle:          "Meta Slug",
			FieldTitle:           "İsim",
			FieldDescriptionHTML: "Açıklama",
			FieldSEOTitle:        "Meta Başlığı",
			FieldSEODescription:  "Meta Açıklaması",
		},
		// Which language these hold is not in the file. See File.ColumnsFor.
		TargetColumns: map[Field]string{
			FieldTitle:           "Çevrilecek İsim",
			FieldDescriptionHTML: "Çevrilecek Açıklama",
			FieldSEOTitle:        "Çevrilecek Meta Başlığı",
			FieldSEODescription:  "Çevrilecek Meta Açıklaması",
		},
	},
	{
		Key:  "ikas-fields-variant",
		Name: "IKAS (özel alanlar — varyantlı)",
		// The variant-level custom fields export. Ahead of "ikas-fields" in this
		// table because that profile's signature is a subset of this header:
		// detection takes the first match, and matching the product-level
		// profile here would silently drop the variant identity this file has.
		Signature: []string{"Ürün Grup ID", "Varyant ID", "İsim", "Html:Detay"},
		GroupBy:   FieldGroupID,
		Columns: map[Field]string{
			FieldGroupID:         "Ürün Grup ID",
			FieldSKU:             "SKU",
			FieldTitle:           "İsim",
			FieldDescriptionHTML: "Html:Detay",
		},
		Translations: map[Lang]map[Field]string{
			LangAR: {FieldDescriptionHTML: "Html:Detay-AR"},
		},
	},
	{
		Key:  "ikas-fields",
		Name: "IKAS (özel alanlar)",
		// The second export IKAS offers: custom fields, where a store keeps the
		// long rich-text detail it did not want in the product description.
		// One title and one HTML body, and that is the whole surface.
		Signature: []string{"Ürün Grup ID", "İsim", "Html:Detay"},
		GroupBy:   FieldGroupID,
		Columns: map[Field]string{
			FieldGroupID:         "Ürün Grup ID",
			FieldTitle:           "İsim",
			FieldDescriptionHTML: "Html:Detay",
		},
		// Html:Detay-AR is in the real export this profile was written from.
		// It is a custom field the store created, not something IKAS ships, so
		// it is named here as a column this profile *may* find and bind only
		// when the header actually has it — a store that never created it gets
		// a profile with no Arabic and is correctly not offered an Arabic pass.
		Translations: map[Lang]map[Field]string{
			LangAR: {FieldDescriptionHTML: "Html:Detay-AR"},
		},
	},
}

// Dialects is every profile, for a client that wants to name them.
func Dialects() []Dialect {
	out := make([]Dialect, len(dialects))
	copy(out, dialects)
	return out
}

// Detect resolves a header to a platform profile.
//
// Matching is case- and space-insensitive on the signature only. A profile
// whose signature is present wins even if some of its optional columns (an
// SEO field a store never filled in) are absent — those resolve to an empty
// column name and are simply not readable, which is the truth rather than a
// failure to detect.
func Detect(header []string) (Dialect, bool) {
	have := headerIndexByName(header)
	for _, d := range dialects {
		matched := true
		for _, sig := range d.Signature {
			if _, ok := have[normalizeHeader(sig)]; !ok {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		return bind(d, have), true
	}
	return Dialect{}, false
}

// bindDialect resolves a profile by key against a header, skipping the
// signature test. It is what an operator's explicit pick goes through: they
// have told us which platform this is, so the question is no longer "does the
// signature match" but "which of this profile's columns does this file have".
func bindDialect(key string, header []string) (Dialect, bool) {
	for _, d := range dialects {
		if d.Key != key {
			continue
		}
		return bind(d, headerIndexByName(header)), true
	}
	return Dialect{}, false
}

// bind ties a profile to the header's own spelling, so export writes back the
// column name the file actually had rather than the one the profile guessed.
//
// A column the profile names and the file does not have is left out rather
// than bound to an empty string: absent is readable as absent, and an empty
// column name would make index() return -1 anyway but say nothing.
func bind(d Dialect, have map[string]string) Dialect {
	bound := Dialect{
		Key: d.Key, Name: d.Name, GroupBy: d.GroupBy,
		Columns:       bindColumns(d.Columns, have),
		TargetColumns: bindColumns(d.TargetColumns, have),
	}
	if len(bound.TargetColumns) == 0 {
		bound.TargetColumns = nil
	}
	for lang, cols := range d.Translations {
		// A language whose columns are all absent does not appear at all, which
		// is how a profile can name an Arabic column that most stores using it
		// never created. Binding it to nothing would advertise a language this
		// file cannot carry.
		if got := bindColumns(cols, have); len(got) > 0 {
			if bound.Translations == nil {
				bound.Translations = map[Lang]map[Field]string{}
			}
			bound.Translations[lang] = got
		}
	}
	return bound
}

func bindColumns(cols map[Field]string, have map[string]string) map[Field]string {
	out := map[Field]string{}
	for f, col := range cols {
		if actual, ok := have[normalizeHeader(col)]; ok {
			out[f] = actual
		}
	}
	return out
}

func headerIndexByName(header []string) map[string]string {
	have := make(map[string]string, len(header))
	for _, h := range header {
		have[normalizeHeader(h)] = h
	}
	return have
}

func normalizeHeader(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// index returns the position of a field's column in one language, or -1.
func (f File) index(lf LangField) int {
	col, ok := f.ColumnsFor(lf.Lang)[lf.Field]
	if !ok || col == "" {
		return -1
	}
	return f.headerIndex(col)
}

// cell reads one field out of one raw row, tolerating a short row.
func (f File) cell(row []string, lf LangField) string {
	i := f.index(lf)
	if i < 0 || i >= len(row) {
		return ""
	}
	return row[i]
}
