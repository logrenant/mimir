package extract_test

import (
	"testing"

	"github.com/logrenant/mimir/internal/extract"
)

func TestMeta_OGProperty(t *testing.T) {
	doc, err := extract.Document(readFixture(t, "og_meta_only.html"))
	if err != nil {
		t.Fatal(err)
	}
	if got := extract.Meta(doc, "og:title"); got != "Example User (@exampleuser) • Instagram photos and videos" {
		t.Errorf("unexpected og:title: %q", got)
	}
	if got := extract.Meta(doc, "og:missing"); got != "" {
		t.Errorf("expected empty string for missing meta, got %q", got)
	}
}

func TestText_Attr_TextList(t *testing.T) {
	doc, err := extract.Document(`<html><body>
		<h1>  Title  </h1>
		<a href="/a">One</a>
		<a href="/b">Two</a>
	</body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if got := extract.Text(doc, "h1"); got != "Title" {
		t.Errorf("unexpected text: %q", got)
	}
	if got := extract.Attr(doc, "a", "href"); got != "/a" {
		t.Errorf("unexpected attr: %q", got)
	}
	list := extract.TextList(doc, "a")
	if len(list) != 2 || list[0] != "One" || list[1] != "Two" {
		t.Errorf("unexpected list: %v", list)
	}
}

func TestText_Attr_Missing(t *testing.T) {
	doc, err := extract.Document(`<html><body></body></html>`)
	if err != nil {
		t.Fatal(err)
	}
	if got := extract.Text(doc, "h1"); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
	if got := extract.Attr(doc, "a", "href"); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}
