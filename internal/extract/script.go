package extract

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// ScriptJSON returns the JSON parsed from the text content of the first
// element matching selector — e.g. "#__UNIVERSAL_DATA_FOR_REHYDRATION__" for
// TikTok, "#__NEXT_DATA__" for Next.js apps. ok=false (not an error) when
// the element is missing or its content isn't valid JSON — this kind of
// embedded state blob is undocumented and version-fragile by nature.
func ScriptJSON(doc *goquery.Document, selector string) (json.RawMessage, bool) {
	sel := doc.Find(selector).First()
	if sel.Length() == 0 {
		return nil, false
	}
	raw := strings.TrimSpace(sel.Text())
	if raw == "" || !json.Valid([]byte(raw)) {
		return nil, false
	}
	return json.RawMessage(raw), true
}

// Dig walks a nested structure produced by json.Unmarshal(data, &v) (i.e.
// made of map[string]any / []any / scalars) one path segment at a time. A
// segment that parses as a non-negative integer indexes into a []any;
// otherwise it looks up a map key. Returns ok=false on any miss instead of
// panicking — every provider package's JSON-blob path-walk is expected to
// occasionally miss as the upstream site's internal shape drifts.
func Dig(v any, path ...string) (any, bool) {
	cur := v
	for _, seg := range path {
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[seg]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil, false
			}
			cur = node[idx]
		default:
			return nil, false
		}
	}
	return cur, true
}

// VarJSON best-effort extracts a JSON object assigned to a legacy inline
// `window.<varName> = {...};` (or bare `<varName> = {...};`) script pattern,
// balancing nested braces (including braces inside string literals)
// properly rather than a naive non-greedy regex. ok=false is a normal,
// common outcome — most modern pages don't use this pattern at all — never
// treat it as an error to log.
func VarJSON(html, varName string) (json.RawMessage, bool) {
	re := regexp.MustCompile(`(?:window\.)?` + regexp.QuoteMeta(varName) + `\s*=\s*`)
	loc := re.FindStringIndex(html)
	if loc == nil {
		return nil, false
	}
	rest := html[loc[1]:]
	if rest == "" || rest[0] != '{' {
		return nil, false
	}

	depth := 0
	inString := false
	escaped := false
	for i, r := range rest {
		if inString {
			switch {
			case escaped:
				escaped = false
			case r == '\\':
				escaped = true
			case r == '"':
				inString = false
			}
			continue
		}
		switch r {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				candidate := rest[:i+1]
				if json.Valid([]byte(candidate)) {
					return json.RawMessage(candidate), true
				}
				return nil, false
			}
		}
	}
	return nil, false
}
