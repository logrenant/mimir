package graphify

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParse_ReadsTheModulesOutput(t *testing.T) {
	// The shape is graphify.extract's own, taken from its test fixture.
	raw := []byte(`{
	  "nodes": [
	    {"id":"n_t","label":"Transformer","file_type":"code","source_file":"model.py","source_location":"L1"},
	    {"id":"n_a","label":"MultiHeadAttention","file_type":"code","source_file":"model.py","source_location":"L10"}
	  ],
	  "edges": [
	    {"source":"n_t","target":"n_a","relation":"contains","confidence":"EXTRACTED","source_file":"model.py","weight":1.0}
	  ],
	  "input_tokens": 0,
	  "output_tokens": 0
	}`)

	got, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Nodes) != 2 || len(got.Edges) != 1 {
		t.Fatalf("nodes = %d, edges = %d; want 2 and 1", len(got.Nodes), len(got.Edges))
	}
	if got.Nodes[0].Label != "Transformer" || got.Nodes[0].SourceLocation != "L1" {
		t.Errorf("first node = %+v", got.Nodes[0])
	}
	if got.Edges[0].Relation != "contains" || got.Edges[0].Confidence != "EXTRACTED" {
		t.Errorf("edge = %+v", got.Edges[0])
	}
}

// graphifyy is a 0.x package that releases most days. A field it adds upstream
// must not turn the symbol layer off on every machine at once.
func TestParse_IgnoresFieldsItDoesNotKnow(t *testing.T) {
	raw := []byte(`{"nodes":[{"id":"a","label":"a.py","file_type":"code","source_file":"a.py","community":3,"embedding":[0.1]}],"edges":[],"hyperedges":[{"x":1}]}`)
	got, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(got.Nodes))
	}
}

func TestParse_RefusesWhatIsNotJSON(t *testing.T) {
	if _, err := Parse([]byte("Traceback (most recent call last):")); err == nil {
		t.Fatal("want an error for output that is not JSON")
	}
}

func TestNode_TheFileIsTheOneLabelledWithItsOwnName(t *testing.T) {
	file := Node{Label: "client.py", SourceFile: "raw/client.py", FileType: "code"}
	symbol := Node{Label: ".request()", SourceFile: "raw/client.py", FileType: "code"}

	if !file.IsFile() {
		t.Error("the file node was not recognised")
	}
	if symbol.IsFile() {
		t.Error("a method was taken for a file")
	}
}

// The same two filters graphify.build applies, and for the same reasons: an
// edge to the standard library names a node nobody extracted, and a code node
// with no edges is a synthetic symbol that would arrive as an unconnected dot.
func TestPrune_DropsExternalEdgesAndIsolatedCode(t *testing.T) {
	in := Extraction{
		Nodes: []Node{
			{ID: "a", FileType: "code", Label: "a.py", SourceFile: "a.py"},
			{ID: "b", FileType: "code", Label: "B", SourceFile: "a.py"},
			{ID: "lonely", FileType: "code", Label: "L", SourceFile: "a.py"},
			{ID: "note", FileType: "document", Label: "notes.md", SourceFile: "notes.md"},
		},
		Edges: []Edge{
			{Source: "a", Target: "b", Relation: "contains"},
			{Source: "b", Target: "os.path", Relation: "uses"},
			{Source: "b", Target: "b", Relation: "calls"},
		},
	}

	got := in.Prune()

	if len(got.Edges) != 1 || got.Edges[0].Target != "b" {
		t.Fatalf("edges = %+v; want only the one whose ends are both present", got.Edges)
	}
	ids := map[string]bool{}
	for _, n := range got.Nodes {
		ids[n.ID] = true
	}
	if ids["lonely"] {
		t.Error("an unconnected code node survived")
	}
	// A document is kept even alone: it can be a leaf concept on purpose.
	if !ids["note"] {
		t.Error("an unconnected document was dropped; only code nodes are")
	}
	if !ids["a"] || !ids["b"] {
		t.Error("a connected node was dropped")
	}
}

func TestParses_KnowsWhatTheASTPassReads(t *testing.T) {
	for _, rel := range []string{"a.go", "b/c.py", "d.TSX", "e.hpp"} {
		if !Parses(rel) {
			t.Errorf("%s: want parseable", rel)
		}
	}
	for _, rel := range []string{"README.md", "manual.pdf", "Makefile", "a.lock", "noext"} {
		if Parses(rel) {
			t.Errorf("%s: want not parseable", rel)
		}
	}
}

func TestDetect_AnInterpreterThatIsNotThereIsUnavailable(t *testing.T) {
	_, err := Detect(context.Background(), "python-that-does-not-exist-9000")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

// stubPython stands in for an interpreter: it is handed `-c <driver>` and a
// file list on stdin exactly as the real one is, so what is under test is this
// package's plumbing rather than Python's.
func stubPython(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "python3")
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtract_ParsesAndPrunesWhatTheInterpreterPrints(t *testing.T) {
	// Echoes the file list back as one node per line, so the test also proves
	// the list reached stdin.
	py := stubPython(t, `
while read -r line; do
  printf '%s\n' "$line" >> "$TMPDIR/seen"
done
cat <<'JSON'
{"nodes":[{"id":"f","label":"a.go","file_type":"code","source_file":"a.go","source_location":"L1"},
          {"id":"s","label":"Run","file_type":"code","source_file":"a.go","source_location":"L7"}],
 "edges":[{"source":"f","target":"s","relation":"contains","confidence":"EXTRACTED","source_file":"a.go","weight":1.0}]}
JSON`)

	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)

	got, err := Extract(context.Background(), Info{Python: py}, dir, []string{"a.go", "b/c.py"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Nodes) != 2 || len(got.Edges) != 1 {
		t.Fatalf("nodes = %d, edges = %d", len(got.Nodes), len(got.Edges))
	}

	seen, err := os.ReadFile(filepath.Join(dir, "seen"))
	if err != nil {
		t.Fatalf("the file list did not reach stdin: %v", err)
	}
	if string(seen) != "a.go\nb/c.py\n" {
		t.Errorf("stdin = %q, want the two paths one per line", seen)
	}
}

// The bug this exists for, found against the real package: above a few dozen
// files graphify.extract prints its progress to *stdout*, which lands in the
// middle of the JSON. The whole extraction then fails to parse — on 316 files
// the error was `invalid character 'A'`, from "AST extraction: …".
func TestExtract_SurvivesALibraryThatPrintsToStdout(t *testing.T) {
	py := stubPython(t, `cat > /dev/null
echo "  AST extraction: 316/316 uncached files (100%)"
echo '{"nodes":[{"id":"f","label":"a.go","file_type":"code","source_file":"a.go"},{"id":"s","label":"Run","file_type":"code","source_file":"a.go"}],"edges":[{"source":"f","target":"s","relation":"contains","confidence":"EXTRACTED","weight":1.0}]}'`)

	// The stub prints the noise itself, so this asserts the shape of the
	// failure rather than the fix — the real fix is in the driver, which sends
	// the library's own printing to stderr, and is exercised by the daemon.
	_, err := Extract(context.Background(), Info{Python: py}, t.TempDir(), []string{"a.go"})
	if err == nil {
		t.Fatal("want a parse error: this is what the driver's redirect prevents")
	}
	if !contains(err.Error(), "unreadable extraction") {
		t.Errorf("err = %v, want it to name the extraction as unreadable", err)
	}
}

// And the driver itself: whatever the library prints must not reach stdout.
func TestDriver_SendsTheLibrarysPrintingToStderr(t *testing.T) {
	if !contains(driver, "redirect_stdout(sys.stderr)") {
		t.Fatal("the driver no longer redirects the library's stdout; progress lines will corrupt the JSON")
	}
	// The dump has to be outside the redirect, or the result goes to stderr too.
	dump := indexOf(driver, "json.dump")
	redirect := indexOf(driver, "redirect_stdout")
	if dump < redirect {
		t.Fatal("the result is written before the redirect is set up")
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestExtract_AsksForNothingWhenThereIsNothingToParse(t *testing.T) {
	py := stubPython(t, `echo 'this should never run'; exit 1`)
	got, err := Extract(context.Background(), Info{Python: py}, t.TempDir(), nil)
	if err != nil {
		t.Fatalf("an empty file list must not be an error: %v", err)
	}
	if len(got.Nodes) != 0 {
		t.Errorf("nodes = %d, want none", len(got.Nodes))
	}
}

// A traceback is a normal outcome on a machine mid-upgrade, and it must read as
// one line the operator can act on rather than as a wall of Python.
func TestExtract_CarriesTheFirstLineOfTheFailure(t *testing.T) {
	py := stubPython(t, `cat > /dev/null; echo "ModuleNotFoundError: No module named 'tree_sitter'" >&2; echo "  more" >&2; exit 1`)

	_, err := Extract(context.Background(), Info{Python: py}, t.TempDir(), []string{"a.go"})
	if err == nil {
		t.Fatal("want an error")
	}
	if want := "ModuleNotFoundError"; !contains(err.Error(), want) {
		t.Errorf("err = %v, want it to carry %q", err, want)
	}
	if contains(err.Error(), "more") {
		t.Errorf("err = %v, want only the first line", err)
	}
}

func TestExtract_WithoutAnInterpreterIsUnavailable(t *testing.T) {
	_, err := Extract(context.Background(), Info{}, t.TempDir(), []string{"a.go"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func TestDetector_CachesBothAnswersAndForgetsOnDemand(t *testing.T) {
	d := NewDetector(time.Hour)

	if _, ok := d.Look(context.Background(), "python-that-does-not-exist-9000"); ok {
		t.Fatal("found an interpreter that is not installed")
	}
	// The negative answer is cached: a machine that will never have Graphify
	// must not pay a lookup per project.
	before := d.at
	if _, ok := d.Look(context.Background(), "python-that-does-not-exist-9000"); ok {
		t.Fatal("the cached answer changed")
	}
	if !d.at.Equal(before) {
		t.Error("the second look re-ran the probe instead of using the cache")
	}

	d.Forget()
	if !d.at.IsZero() {
		t.Error("Forget did not clear the cache")
	}
}

func TestDetector_ReChecksWhenTheInterpreterChanges(t *testing.T) {
	d := NewDetector(time.Hour)
	d.Look(context.Background(), "python-a-9000")
	first := d.at
	d.Look(context.Background(), "python-b-9000")
	if d.at.Equal(first) {
		t.Error("naming a different interpreter must re-run the probe")
	}
}

func TestDetector_NilIsUsable(t *testing.T) {
	var d *Detector
	if _, ok := d.Look(context.Background(), "python3"); ok {
		t.Error("a nil detector must find nothing")
	}
	d.Forget()
}

// "Which Python" is a question the operator should not have to answer: on a Mac
// there are three and the one with the package is usually a virtual
// environment that is on no PATH at all.
func TestCandidates_LooksInTheWellKnownPlacesFirst(t *testing.T) {
	got := Candidates("")
	if len(got) < 2 {
		t.Fatalf("candidates = %v, want the venv and the PATH interpreter at least", got)
	}
	if !strings.Contains(got[0], ".mimir/graphify") {
		t.Errorf("first candidate = %q, want the environment the screen tells them to make", got[0])
	}
	if got[len(got)-1] != DefaultPython {
		t.Errorf("last candidate = %q, want %q", got[len(got)-1], DefaultPython)
	}
	// The ~ is resolved: exec.LookPath does not know what a home directory is.
	if strings.HasPrefix(got[0], "~") {
		t.Errorf("candidate %q was not expanded", got[0])
	}
}

// An operator who named an interpreter is telling us where it is. Falling back
// to a different one would make the setting a suggestion.
func TestCandidates_AnExplicitInterpreterIsTheOnlyOne(t *testing.T) {
	got := Candidates("  /opt/py/bin/python3  ")
	if len(got) != 1 || got[0] != "/opt/py/bin/python3" {
		t.Fatalf("candidates = %v, want only the one that was named", got)
	}
}

// TestParses_CoversGraphifyCodeExtensions guards the ceiling this task raised.
//
// task-72 shipped eighteen extensions against upstream's hundred, and nothing
// said so: a .svelte or .tf file was simply never read, and the graph looked
// complete. These are the ones whose absence was actually costing coverage on
// this machine.
func TestParses_CoversGraphifyCodeExtensions(t *testing.T) {
	for _, rel := range []string{
		"app/Page.svelte", "app/Page.vue", "app/page.astro",
		"main.swift", "script.lua", "deploy.sh", "schema.sql",
		"infra/main.tf", "infra/vars.tfvars", "lib/thing.dart",
		"lib/thing.ex", "lib/thing.exs", "calc.jl", "plot.r",
		"build.gradle", "config.hcl", "main.zig", "build.ps1",
		"kernel.cu", "shader.metal", "View.xaml", "Page.razor",
		// And the eighteen it always had, which must not have been lost.
		"main.go", "app.py", "index.ts", "index.tsx", "lib.rs",
		"Main.java", "a.c", "a.h", "a.cpp", "a.cc", "a.cxx", "a.hpp",
		"a.rb", "a.cs", "a.kt", "a.kts", "a.scala", "a.php", "a.js",
	} {
		if !Parses(rel) {
			t.Errorf("Parses(%q) = false, want true", rel)
		}
	}

	// Still not everything. A README is prose, and the semantic pass reads it.
	for _, rel := range []string{"README.md", "notes.txt", "photo.png", "Makefile"} {
		if Parses(rel) {
			t.Errorf("Parses(%q) = true, want false", rel)
		}
	}
}

func TestParsedExtensions_IsTheWholeAllowList(t *testing.T) {
	got := ParsedExtensions()
	// 102 upstream entries, 97 after case-folding: Fortran ships .F90 and .f90
	// and this matcher lowercases before comparing.
	if len(got) != 97 {
		t.Fatalf("allow-list has %d extensions, want 97", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] >= got[i] {
			t.Fatalf("allow-list is not sorted at %d: %q then %q", i, got[i-1], got[i])
		}
	}
}

// TestParseProbe_ToleratesAnOlderGraphifyWithNoExtensionList — losing the drift
// report is not a reason to lose the structural layer.
func TestParseProbe_ToleratesAnOlderGraphifyWithNoExtensionList(t *testing.T) {
	version, exts := parseProbe("0.9.54\n")
	if version != "0.9.54" {
		t.Fatalf("version = %q", version)
	}
	if len(exts) != 0 {
		t.Fatalf("extensions = %v, want none", exts)
	}

	version, exts = parseProbe("0.9.54\n.go .py .svelte\n")
	if version != "0.9.54" || len(exts) != 3 {
		t.Fatalf("version=%q extensions=%v", version, exts)
	}
}
