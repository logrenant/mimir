package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// rawExactDenied are directories that may not themselves be a project root,
// but whose children legitimately can be. Every real project lives somewhere
// under one of these, so they must be matched exactly and never as a prefix —
// prefix-denying /Users would reject every project in a home directory.
var rawExactDenied = []string{
	"/",
	"/Users",
	"/home",
	"/private",
	"/var",
	"/tmp",
	"/opt",
	"/mnt",
	"/media",
	"/Volumes",
}

// rawTreeDenied are system trees that may not be a project root at any depth.
// Nothing a person edits as "their project" lives inside these.
var rawTreeDenied = []string{
	"/etc",
	"/usr",
	"/bin",
	"/sbin",
	"/dev",
	"/proc",
	"/boot",
	"/System",
	"/Library",
	"/Applications",
	"/cores",
}

var (
	denyOnce   sync.Once
	exactDeny  map[string]struct{}
	treeDeny   []string
	homeDenied []string
)

// initDeny expands each denylist entry to every form it can appear in after
// canonicalisation.
//
// This matters more than it looks on macOS. /etc, /var, and /tmp are symlinks
// into /private, so EvalSymlinks turns a candidate of "/etc" into
// "/private/etc" — which the literal string "/etc" would not match. Resolving
// the denylist through the same function as the candidate is what keeps the
// two comparable.
func initDeny() {
	exactDeny = map[string]struct{}{}
	for _, p := range rawExactDenied {
		exactDeny[normalize(p)] = struct{}{}
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			exactDeny[normalize(resolved)] = struct{}{}
		}
	}

	for _, p := range rawTreeDenied {
		treeDeny = append(treeDeny, normalize(p))
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			if n := normalize(resolved); n != normalize(p) {
				treeDeny = append(treeDeny, n)
			}
		}
	}

	// The home directory itself is not a project; a directory inside it is the
	// normal case.
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		homeDenied = append(homeDenied, normalize(home))
		if resolved, err := filepath.EvalSymlinks(home); err == nil {
			homeDenied = append(homeDenied, normalize(resolved))
		}
	}
}

// normalize lower-cases a cleaned path for comparison.
//
// macOS's default filesystem is case-insensitive, so "/ETC" and "/etc" are the
// same directory and a case-sensitive denylist would be trivially bypassed.
// Comparing case-insensitively is slightly over-strict on a case-sensitive
// filesystem — it would also reject a genuinely distinct "/Etc" — which is the
// right way for a denylist to err.
func normalize(p string) string {
	return strings.ToLower(filepath.Clean(p))
}

// Canonicalize resolves and vets a path without registering it.
//
// It exists for readers — the project memory is the first — that need the same
// path discipline as a run but must not mint the permission a run needs. A
// registration is a durable row the coding-task runner can later be pointed at,
// so creating one as a side effect of reading would quietly hand out the thing
// this package's "no default project, ever" rule is built to withhold.
//
// A caller that needs to act inside a directory still goes through Register and
// Resolve. This one only answers "is this a real, allowed directory, and what is
// its true path".
func Canonicalize(path string) (string, error) {
	return canonicalize(path)
}

// canonicalize resolves path and enforces that it is a directory permissive
// enough to work in but not so broad that it hands over the machine.
//
// Order matters: symlinks are resolved BEFORE the denylist is consulted, so a
// link like ~/shortcut -> / cannot smuggle the root past it.
func canonicalize(path string) (string, error) {
	denyOnce.Do(initDeny)

	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("%w: path is empty", ErrInvalidPath)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: %q is relative, an absolute path is required", ErrInvalidPath, path)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("%w: %q: %v", ErrInvalidPath, path, err)
	}

	// Resolves symlinks and fails for a path that does not exist.
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("%w: %q: %v", ErrInvalidPath, path, err)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("%w: %q: %v", ErrInvalidPath, path, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: %q is not a directory", ErrInvalidPath, path)
	}

	if err := checkAllowed(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

// checkAllowed rejects resolved if it is a root too broad to scope an agent to.
func checkAllowed(resolved string) error {
	n := normalize(resolved)

	if _, denied := exactDeny[n]; denied {
		return fmt.Errorf("%w: %q is a system or container directory, pick a specific project folder inside it", ErrPathNotAllowed, resolved)
	}
	for _, home := range homeDenied {
		if n == home {
			return fmt.Errorf("%w: %q is your home directory — pick a specific project folder inside it", ErrPathNotAllowed, resolved)
		}
	}
	for _, tree := range treeDeny {
		if n == tree || strings.HasPrefix(n, tree+string(filepath.Separator)) {
			return fmt.Errorf("%w: %q is inside the system directory %q", ErrPathNotAllowed, resolved, tree)
		}
	}
	return nil
}
