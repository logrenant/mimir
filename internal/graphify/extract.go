package graphify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// driver is the whole of what is asked of Graphify.
//
// The file list arrives on stdin rather than as arguments for two reasons. A
// repository has more files than an argument list can hold, and — the one that
// matters — *Mimir* decides which files are read. task-69 made that the
// operator's setting rather than the machine's, and pointing an outside tool at
// a directory would hand it back to the tool: it would walk past the exclusion
// list and read the file the operator took out of it. So the caller filters,
// and Graphify parses exactly what it is given.
//
// The redirect is not decoration. Above a few dozen files `graphify.extract`
// prints its progress ("AST extraction: 120/316 uncached files") to *stdout*,
// which lands in the middle of the JSON and makes the whole extraction
// unreadable — the failure is a parse error on the letter A. Sending whatever
// the library prints to stderr, where the rest of its chatter already goes,
// leaves stdout carrying one thing.
const driver = `import contextlib, json, sys
from pathlib import Path
from graphify.extract import extract
paths = [Path(line) for line in sys.stdin.read().splitlines() if line.strip()]
with contextlib.redirect_stdout(sys.stderr):
    result = extract(paths)
json.dump(result, sys.stdout)
`

// MaxOutputBytes bounds what one project may print. A repository big enough to
// exceed this has a symbol graph nobody can read anyway, and an unbounded pipe
// into a resident daemon is a memory ceiling nobody set.
const MaxOutputBytes = 48 << 20

// ErrTooLarge means the extraction did not fit in MaxOutputBytes.
var ErrTooLarge = errors.New("graphify: extraction is too large")

// Extract parses `files` — paths relative to `dir` — and returns what Graphify
// found, pruned the way Graphify's own build step prunes it.
//
// Every failure is the caller's cue to carry on without a symbol layer. There
// is no state to roll back: nothing has been written, and the semantic graph
// this would have joined is already complete on its own.
func Extract(ctx context.Context, info Info, dir string, files []string) (Extraction, error) {
	if info.Python == "" {
		return Extraction{}, ErrUnavailable
	}
	if len(files) == 0 {
		return Extraction{}, nil
	}

	cmd := exec.CommandContext(ctx, info.Python, "-c", driver)
	cmd.Dir = dir
	// One path per line, and a trailing newline: a line-oriented protocol whose
	// last line has no terminator loses it to any reader less forgiving than
	// Python's splitlines.
	cmd.Stdin = strings.NewReader(strings.Join(files, "\n") + "\n")

	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return Extraction{}, ctx.Err()
		}
		// Graphify's own warnings go to stderr and are not failures; this is
		// the exit status, so the first line of stderr is the explanation.
		return Extraction{}, fmt.Errorf("graphify: extraction failed: %w: %s", err, firstLine(errOut.String()))
	}
	if out.Len() > MaxOutputBytes {
		return Extraction{}, ErrTooLarge
	}

	parsed, err := Parse(out.Bytes())
	if err != nil {
		return Extraction{}, err
	}
	return parsed.Prune(), nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	if s == "" {
		return "no output"
	}
	return s
}

// Parses reports whether Graphify's AST pass understands this file, by
// extension. It is the same list `graphify.extract.collect_files` walks for,
// restated here because the caller chooses the files and must not send a PDF to
// a tree-sitter parser to be told no.
// parsedExtensions is what the AST pass reads, mirroring
// graphify.detect.CODE_EXTENSIONS.
//
// A constant list rather than one read out of the installed package at run
// time (SD-1, SD-5): deriving it would let a pip upgrade change what Mimir
// scans without a line of this repository moving, and "the graph grew last
// Tuesday" is not a thing anyone should have to explain. Detect() compares the
// two and says so when they diverge, which is the reporting half of the same
// decision.
//
// Upstream lists 102 entries; 97 survive case-folding, because Fortran appears
// as both .F90 and .f90 and this matcher lowercases first.
var parsedExtensions = map[string]struct{}{}

func init() {
	for _, ext := range []string{
		".asd", ".astro", ".bash", ".c", ".cc", ".cjs", ".cl", ".cls",
		".cpp", ".cs", ".cshtml", ".csproj", ".cts", ".cu", ".cuh", ".cxx",
		".dart", ".dfm", ".dm", ".dme", ".dmf", ".dmi", ".dmm", ".dpk",
		".dpr", ".ejs", ".ets", ".ex", ".exs", ".f", ".f03", ".f08",
		".f90", ".f95", ".fsproj", ".go", ".gradle", ".groovy", ".h", ".hcl",
		".hpp", ".inc", ".java", ".jl", ".js", ".json", ".jsx", ".kt",
		".kts", ".lfm", ".lisp", ".lpk", ".lpr", ".lsp", ".lua", ".luau",
		".m", ".metal", ".mjs", ".ml", ".mli", ".mm", ".mts", ".pas",
		".php", ".pp", ".ps1", ".psd1", ".psm1", ".py", ".r", ".rake",
		".razor", ".rb", ".resource", ".robot", ".rs", ".scala", ".sh", ".sln",
		".slnx", ".sql", ".sv", ".svelte", ".svh", ".swift", ".tf", ".tfvars",
		".toc", ".trigger", ".ts", ".tsx", ".v", ".vbproj", ".vue", ".xaml",
		".zig",
	} {
		parsedExtensions[ext] = struct{}{}
	}
}

// ParsedExtensions is the allow-list, sorted — for the drift check and for
// anything that wants to report what this binary reads.
func ParsedExtensions() []string {
	out := make([]string, 0, len(parsedExtensions))
	for ext := range parsedExtensions {
		out = append(out, ext)
	}
	sort.Strings(out)
	return out
}

// Parses reports whether the AST pass will read this file.
func Parses(rel string) bool {
	i := strings.LastIndexByte(rel, '.')
	if i < 0 {
		return false
	}
	_, ok := parsedExtensions[strings.ToLower(rel[i:])]
	return ok
}
