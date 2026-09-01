package extract_test

import (
	"encoding/json"
	"testing"

	"github.com/logrenant/goat-mcp/internal/extract"
)

func TestScriptJSON_TikTokUniversalData(t *testing.T) {
	doc, err := extract.Document(readFixture(t, "tiktok_universal_data.html"))
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := extract.ScriptJSON(doc, "#__UNIVERSAL_DATA_FOR_REHYDRATION__")
	if !ok {
		t.Fatal("expected to find the script blob")
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}

	nickname, ok := extract.Dig(v, "__DEFAULT_SCOPE__", "webapp.user-detail", "userInfo", "user", "nickname")
	if !ok {
		t.Fatal("expected to dig to nickname")
	}
	if nickname != "Example User" {
		t.Errorf("unexpected nickname: %v", nickname)
	}

	followers, ok := extract.Dig(v, "__DEFAULT_SCOPE__", "webapp.user-detail", "userInfo", "stats", "followerCount")
	if !ok {
		t.Fatal("expected to dig to followerCount")
	}
	if followers != float64(125000) {
		t.Errorf("unexpected followerCount: %v", followers)
	}
}

func TestScriptJSON_Missing(t *testing.T) {
	doc, err := extract.Document(readFixture(t, "empty.html"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := extract.ScriptJSON(doc, "#nope"); ok {
		t.Fatal("expected ok=false for a missing selector")
	}
}

func TestDig_MissesGracefully(t *testing.T) {
	v := map[string]any{"a": map[string]any{"b": []any{1, 2, 3}}}
	if _, ok := extract.Dig(v, "a", "b", "9"); ok {
		t.Fatal("expected out-of-range index to miss")
	}
	if _, ok := extract.Dig(v, "a", "missing"); ok {
		t.Fatal("expected missing key to miss")
	}
	if _, ok := extract.Dig(v, "a", "b", "notanumber"); ok {
		t.Fatal("expected non-numeric array index to miss")
	}
	got, ok := extract.Dig(v, "a", "b", "1")
	if !ok || got != 2 {
		t.Fatalf("expected 2, got %v ok=%v", got, ok)
	}
}

func TestVarJSON_NestedBraces(t *testing.T) {
	html := `<script>window._sharedData = {"a": {"b": 1, "c": [1,2,{"d":3}]}, "e": "text with { brace }"};</script>`
	raw, ok := extract.VarJSON(html, "_sharedData")
	if !ok {
		t.Fatal("expected VarJSON to find the blob")
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}
	a, ok := v["a"].(map[string]any)
	if !ok || a["b"] != float64(1) {
		t.Errorf("unexpected parsed value: %v", v)
	}
}

func TestVarJSON_NotPresent(t *testing.T) {
	if _, ok := extract.VarJSON(`<html>no such var here</html>`, "_sharedData"); ok {
		t.Fatal("expected ok=false when the pattern isn't present")
	}
}
