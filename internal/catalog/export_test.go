package catalog

import (
	"bytes"
	"testing"
)

// IKAS repeats a product's description on every variant row. Writing the
// rewrite to only the first one leaves the file disagreeing with itself about
// what the product says, and the admin panel importing it back has to pick.
func TestExport_WritesToEveryVariantRowThatCarriedTheField(t *testing.T) {
	f, _ := parseFixture(t, "ikas-variants.csv")
	products := Products("imp", f)
	if len(products) != 1 {
		t.Fatalf("varyant satırları tek ürüne inmedi: %d", len(products))
	}
	if len(products[0].Rows) != 2 {
		t.Fatalf("ürün iki satır taşımıyor: %v", products[0].Rows)
	}

	next := products[0].Original
	next.DescriptionHTML = "<div class=\"rte\"><p>Yeni metin.</p></div>"
	out, err := Export(f, products, approvedSource(map[string]Content{products[0].ID: next}))
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if n := bytes.Count(out, []byte("Yeni metin.")); n != 2 {
		t.Errorf("yeni açıklama %d satıra yazıldı, 2 bekleniyordu", n)
	}
	if bytes.Contains(out, []byte("yoğun</b> nemlendirici")) {
		t.Error("eski açıklama bir varyant satırında kaldı")
	}
}

// Shopify fills Title and Body (HTML) on the handle's first row only, so
// "every row that carried it" is one row and the variant rows below stay
// untouched — the behaviour rowsFor replaced, unchanged.
func TestExport_LeavesShopifyVariantRowsAlone(t *testing.T) {
	f, raw := parseFixture(t, "shopify.csv")
	products := Products("imp", f)
	var target Product
	for _, p := range products {
		if len(p.Rows) > 1 {
			target = p
			break
		}
	}
	if target.ID == "" {
		t.Skip("fikstürde varyant satırlı ürün yok")
	}
	next := target.Original
	next.Title = "Yepyeni Başlık"
	out, err := Export(f, products, approvedSource(map[string]Content{target.ID: next}))
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if n := bytes.Count(out, []byte("Yepyeni Başlık")); n != 1 {
		t.Errorf("başlık %d satıra yazıldı, 1 bekleniyordu", n)
	}
	if len(bytes.Split(out, []byte("\n"))) != len(bytes.Split(raw, []byte("\n"))) {
		t.Error("satır sayısı değişti")
	}
}

// approvedSource is the common case in these tests: the source language's copy
// was approved and no other language was touched.
func approvedSource(m map[string]Content) map[string]map[Lang]Content {
	out := make(map[string]map[Lang]Content, len(m))
	for id, c := range m {
		out[id] = map[Lang]Content{LangSource: c}
	}
	return out
}
