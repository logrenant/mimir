// Package graphify reads a project's structure from Graphify, when the
// operator has installed it.
//
// Brain's own reading of a repository is semantic and file-shaped: a model says
// what each file is about, and one more model call decides which other files it
// belongs with (internal/brain/relate.go). That answers "what is this about"
// and cannot answer "who calls this function" — there are no symbols in the
// graph, and the edges are a judgement rather than a fact.
//
// Graphify's AST pass is the missing half: tree-sitter over a dozen languages,
// producing functions, classes and the calls and imports between them. It is
// deterministic, it costs no model call, and it is Apache-2.0 and free.
//
// # Why this shells out instead of vendoring
//
// Graphify is a Python package (`pip install graphifyy`) and Mimir ships as two
// self-contained Go binaries. Making Python a runtime prerequisite would trade
// the install story for a feature, so this package treats Graphify the way
// internal/brain/github.go treats GitHub: an outside source that is used when
// it answers and forgotten when it does not. A machine without it loses the
// symbol layer and nothing else — no error, no empty screen, no setup step.
//
// # Why `python -m graphify.extract` and not the CLI
//
// The `graphify` command is not a graph builder. It installs a skill into an
// agent's directory (`graphify install`) and manages git hooks; the graph is
// built by *the agent*, following `skill.md`, which dispatches subagents over
// documents and images. That path needs a model and produces an output layer —
// an HTML viewer, a Markdown report, an Obsidian vault — that Mimir already has
// its own versions of.
//
// `python -m graphify.extract <dir>` is the deterministic half of it, is a
// documented entry point of the package, prints its result as JSON on stdout,
// and needs none of Graphify's optional extras. It is the only part of
// Graphify this package touches.
package graphify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// ErrUnavailable means Graphify is not installed, or the interpreter that was
// asked cannot import it. It is a typed sentinel so a caller can tell "this
// machine does not have the feature" from "the feature is broken" (SD-6).
var ErrUnavailable = errors.New("graphify: not installed")

// Info is what was found.
type Info struct {
	// Python is the interpreter that could import it — recorded because a
	// machine can easily have three, and only one of them has the package.
	Python string `json:"python"`
	// Version is graphifyy's own, as its metadata reports it. Empty when the
	// package is importable but unregistered, which is a source checkout.
	Version string `json:"version,omitempty"`
}

// Detect finds an interpreter that can import graphify.
//
// The check is an import rather than a `--version`, because importing is what
// Extract will do and the two must not be able to disagree.
//
// With no interpreter named it tries the well-known places in order (see
// Candidates), because "which Python" is a question an operator should not have
// to answer. On a Mac with Homebrew there are three of them, only one has the
// package, and the one that has it is usually a virtual environment rather than
// anything on PATH.
func Detect(ctx context.Context, python string) (Info, error) {
	var last error
	for _, candidate := range Candidates(python) {
		resolved, err := exec.LookPath(candidate)
		if err != nil {
			last = fmt.Errorf("%w: no %s", ErrUnavailable, candidate)
			continue
		}

		out, err := exec.CommandContext(ctx, resolved, "-c", probe).Output()
		if err != nil {
			last = fmt.Errorf("%w: %s cannot import graphify", ErrUnavailable, resolved)
			continue
		}
		version, upstream := parseProbe(string(out))
		noteExtensionDrift(version, upstream)
		return Info{Python: resolved, Version: version}, nil
	}
	if last == nil {
		last = ErrUnavailable
	}
	return Info{}, last
}

// parseProbe splits the probe's two lines. A probe from an older graphify has
// only the first, and that is not an error.
func parseProbe(out string) (version string, extensions []string) {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) > 0 {
		version = strings.TrimSpace(lines[0])
	}
	if len(lines) > 1 {
		extensions = strings.Fields(strings.TrimSpace(lines[1]))
	}
	return version, extensions
}

// noteExtensionDrift says out loud when the installed package parses languages
// this binary does not ask it about.
//
// The allow-list is deliberately ours (SD-1): what Mimir scans must not change
// because somebody ran pip. But a list that silently falls behind is how the
// structural layer quietly stops covering half a repository, which is exactly
// what happened between task-72 and this one — eighteen extensions against a
// hundred. So the divergence is reported rather than absorbed, and widening it
// stays a decision somebody makes in code.
func noteExtensionDrift(version string, upstream []string) {
	if len(upstream) == 0 {
		return
	}
	ours := map[string]struct{}{}
	for _, ext := range ParsedExtensions() {
		ours[ext] = struct{}{}
	}

	var missing []string
	for _, ext := range upstream {
		if _, known := ours[strings.ToLower(ext)]; !known {
			missing = append(missing, ext)
		}
	}
	if len(missing) == 0 {
		return
	}
	sort.Strings(missing)
	slog.Info("graphify parses languages this build does not ask it about",
		"version", version, "ours", len(ours), "theirs", len(upstream),
		"unread", strings.Join(missing, " "))
}

// DefaultPython is the interpreter on PATH, and the last thing tried.
const DefaultPython = "python3"

// VenvPath is the virtual environment Mimir suggests and then finds on its own.
//
// A dedicated environment rather than `pip install` into the system Python,
// because on a current Mac that either refuses (PEP 668) or writes a package
// into a Homebrew install that Homebrew will later overwrite. It is under the
// operator's home rather than beside the store, so it survives a reinstall and
// so `rm -rf` on it is an obvious, reversible thing to do.
const VenvPath = "~/.mimir/graphify"

// Candidates is the list Detect walks, in order.
//
// An explicit answer is the only one used when there is one: an operator who
// named an interpreter is telling us where it is, and quietly falling back to a
// different one would make the setting a suggestion.
func Candidates(explicit string) []string {
	if e := strings.TrimSpace(explicit); e != "" {
		return []string{expand(e)}
	}
	return []string{
		// What the screen tells them to make.
		expand(VenvPath + "/bin/python3"),
		// And what `pipx install graphifyy` makes, which is the other way
		// somebody who already knows pipx will have done it.
		expand("~/.local/pipx/venvs/graphifyy/bin/python"),
		DefaultPython,
	}
}

// expand resolves a leading ~ — the paths above are written the way they are
// typed, and exec.LookPath does not know what a home directory is.
func expand(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~"))
}

// probe prints the installed version, or an empty line for a checkout that was
// never pip-installed. Importing the top-level package is deliberately cheap:
// graphify's `__init__` defers every heavy import, so this does not pay for
// tree-sitter to answer "is it there".
// probe answers two questions in one interpreter start: which version is
// installed, and which extensions its AST pass claims to read. The second line
// is the drift check — see noteExtensionDrift.
//
// It fails soft in both halves. An older graphify with no CODE_EXTENSIONS is
// still a working graphify, and losing the drift report is not a reason to
// lose the structural layer.
const probe = `import importlib.metadata as m, graphify
try:
    print(m.version("graphifyy"))
except Exception:
    print("")
try:
    from graphify.detect import CODE_EXTENSIONS
    print(" ".join(sorted(CODE_EXTENSIONS)))
except Exception:
    print("")
`

// Node is one thing Graphify found: a file, a class, a function, a method.
//
// The id is Graphify's own slug and is meaningful only inside one extraction —
// it is used to resolve edges and then thrown away. What identifies a node
// across runs is SourceFile plus Label, which is what the caller keys on.
type Node struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	FileType string `json:"file_type"`
	// SourceFile is relative to the directory Graphify was pointed at.
	SourceFile string `json:"source_file"`
	// SourceLocation is "L12" for code. Kept as written: it is a label, and
	// parsing it into a number would invent precision for the document forms
	// ("§3.1") that the same field carries.
	SourceLocation string `json:"source_location"`
}

// Edge is one relation. Relation is Graphify's vocabulary — `calls`,
// `contains`, `method`, `imports_from`, `inherits`, `uses` — and is carried
// through unchanged, because a neighbour that says "calls" is worth more to a
// reader than one that says "semantic".
type Edge struct {
	Source   string `json:"source"`
	Target   string `json:"target"`
	Relation string `json:"relation"`
	// Confidence is EXTRACTED (the parser saw it), INFERRED (resolved across
	// files) or AMBIGUOUS.
	Confidence string  `json:"confidence"`
	SourceFile string  `json:"source_file"`
	Weight     float64 `json:"weight"`
}

// Extraction is what `graphify.extract` returns.
type Extraction struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// IsFile reports whether this node is the file itself rather than something
// inside it. Graphify labels the file node with its base name; everything else
// is a symbol.
func (n Node) IsFile() bool {
	if n.SourceFile == "" || n.Label == "" {
		return false
	}
	return n.Label == baseName(n.SourceFile)
}

func baseName(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

// Prune drops what Graphify's own build step drops, and for its reasons:
// edges whose endpoints are not both present — those name the standard library
// or a third-party package, which is expected rather than an error — and code
// nodes left with no edges at all, which are bundled or synthetic symbols that
// would arrive in the picture as a field of unconnected dots.
func (e Extraction) Prune() Extraction {
	known := make(map[string]struct{}, len(e.Nodes))
	for _, n := range e.Nodes {
		known[n.ID] = struct{}{}
	}

	edges := make([]Edge, 0, len(e.Edges))
	degree := make(map[string]int, len(e.Nodes))
	for _, edge := range e.Edges {
		if _, ok := known[edge.Source]; !ok {
			continue
		}
		if _, ok := known[edge.Target]; !ok {
			continue
		}
		// A self-edge is a dot with a loop nobody can see, and it would count
		// twice towards the degree that keeps the node.
		if edge.Source == edge.Target {
			continue
		}
		edges = append(edges, edge)
		degree[edge.Source]++
		degree[edge.Target]++
	}

	nodes := make([]Node, 0, len(e.Nodes))
	for _, n := range e.Nodes {
		if n.FileType == "code" && degree[n.ID] == 0 {
			continue
		}
		nodes = append(nodes, n)
	}

	return Extraction{Nodes: nodes, Edges: edges}
}

// Parse decodes what the module printed.
//
// Unknown fields are ignored rather than rejected: graphifyy is a 0.x package
// that releases most days, and a field added upstream must not turn the symbol
// layer off on every machine at once.
func Parse(raw []byte) (Extraction, error) {
	var out Extraction
	if err := json.Unmarshal(raw, &out); err != nil {
		return Extraction{}, fmt.Errorf("graphify: unreadable extraction: %w", err)
	}
	return out, nil
}
