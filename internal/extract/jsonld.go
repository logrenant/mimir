package extract

import (
	"encoding/json"

	"github.com/PuerkitoBio/goquery"
)

// JSONLD returns every valid JSON-LD block found in
// <script type="application/ld+json"> tags on the page. A block that fails
// to parse (malformed JSON — common in the wild) is skipped, not an error:
// one bad block must never sink extraction of the others.
func JSONLD(doc *goquery.Document) []map[string]any {
	var blocks []map[string]any
	doc.Find(`script[type="application/ld+json"]`).Each(func(_ int, s *goquery.Selection) {
		var v any
		if err := json.Unmarshal([]byte(s.Text()), &v); err != nil {
			return
		}
		switch t := v.(type) {
		case map[string]any:
			blocks = append(blocks, t)
		case []any:
			for _, item := range t {
				if m, ok := item.(map[string]any); ok {
					blocks = append(blocks, m)
				}
			}
		}
	})
	return blocks
}

// FindJSONLDByType returns the first block (searching top-level blocks and,
// for each, its "@graph" array of nested nodes) whose "@type" matches one of
// types — schema.org's "@type" may be a single string or an array of
// strings, both are checked.
func FindJSONLDByType(blocks []map[string]any, types ...string) (map[string]any, bool) {
	want := make(map[string]bool, len(types))
	for _, t := range types {
		want[t] = true
	}
	for _, b := range blocks {
		if found, ok := findTypeIn(b, want); ok {
			return found, true
		}
	}
	return nil, false
}

func findTypeIn(m map[string]any, want map[string]bool) (map[string]any, bool) {
	if typeMatches(m["@type"], want) {
		return m, true
	}
	if graph, ok := m["@graph"].([]any); ok {
		for _, item := range graph {
			if gm, ok := item.(map[string]any); ok {
				if found, ok := findTypeIn(gm, want); ok {
					return found, true
				}
			}
		}
	}
	return nil, false
}

func typeMatches(v any, want map[string]bool) bool {
	switch t := v.(type) {
	case string:
		return want[t]
	case []any:
		for _, item := range t {
			if s, ok := item.(string); ok && want[s] {
				return true
			}
		}
	}
	return false
}
