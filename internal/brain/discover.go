package brain

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DiscoverProjects returns every project under root, so that a machine-wide
// scan is one command rather than a list somebody keeps up to date.
//
// A project is a git checkout, or — for the directories that never became one —
// a directory that holds files of its own. The two rules are ordered, and the
// order is what stops a file being scanned twice under two different project
// paths:
//
//   - A directory with a `.git` in it is a project and owns everything beneath
//     it. Nothing under it is discovered separately, because `git ls-files`
//     will already list it.
//   - A directory without one is a project if it directly holds a file worth
//     reading. It claims its subtree the same way, except that a repository
//     nested inside it is still discovered — `~/development/personal/goat` is
//     600 loose files *and* a checkout called GoatNative, and both are wanted.
//     `listScannable`'s walk skips that nested checkout for the same reason.
//
// The depth bound is not an optimisation. It is what keeps a mis-typed root —
// `/`, or a home directory — from turning into an hours-long directory walk
// before the first model call is ever made.
func DiscoverProjects(root string, maxDepth int) ([]string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, &os.PathError{Op: "discover", Path: abs, Err: os.ErrInvalid}
	}

	var found []string
	var walk func(dir string, depth int, claimed bool)

	walk = func(dir string, depth int, claimed bool) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			// An unreadable directory is not a reason to abandon the roots that
			// can be read; it simply holds no projects as far as this pass knows.
			return
		}

		if isRepo(dir) {
			found = append(found, dir)
			return
		}
		if !claimed && holdsOwnFiles(entries) {
			found = append(found, dir)
			claimed = true
		}
		if depth >= maxDepth {
			return
		}

		for _, e := range entries {
			// e.IsDir() is false for a symlink, which is deliberate: following
			// one is how a walk ends up outside the root it was given, or in a
			// cycle.
			if !e.IsDir() || skipDir(e.Name()) || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			walk(filepath.Join(dir, e.Name()), depth+1, claimed)
		}
	}

	walk(abs, 0, false)
	sort.Strings(found)
	return found, nil
}

// holdsOwnFiles reports whether a directory has content of its own, using the
// same judgement a scan would: an empty file, a lock file or an icon is not a
// reason to call a directory a project.
func holdsOwnFiles(entries []os.DirEntry) bool {
	for _, e := range entries {
		if e.IsDir() || !scannable(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil || info.Size() == 0 {
			continue
		}
		return true
	}
	return false
}
