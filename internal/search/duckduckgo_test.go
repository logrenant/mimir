package search

import (
	"os"
	"testing"
)

func TestParseHTML(t *testing.T) {
	f, err := os.Open("testdata/ddg_html.html")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	res, err := parseHTML(f)
	if err != nil {
		t.Fatal(err)
	}

	if len(res) != 2 {
		t.Fatalf("expected 2 results, got %d", len(res))
	}
	if res[0].Title != "parser package - go/parser - Go Packages" {
		t.Errorf("unexpected title: %q", res[0].Title)
	}
	if res[0].URL != "https://pkg.go.dev/go/parser" {
		t.Errorf("unexpected URL: %q", res[0].URL)
	}
}

func TestParseLite(t *testing.T) {
	f, err := os.Open("testdata/ddg_lite.html")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	res, err := parseLite(f)
	if err != nil {
		t.Fatal(err)
	}

	if len(res) == 0 {
		t.Fatal("expected results, got 0")
	}
	
	found := false
	for _, r := range res {
		if r.URL == "https://pkg.go.dev/go/parser" && r.Title == "parser package - go/parser - Go Packages" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("did not find expected result in lite parsing")
	}
}
