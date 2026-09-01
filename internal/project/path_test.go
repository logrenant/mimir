package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonicalize_AcceptsANormalProjectDirectory(t *testing.T) {
	dir := t.TempDir()

	got, err := canonicalize(dir)
	if err != nil {
		t.Fatalf("canonicalize(%q): %v", dir, err)
	}

	// t.TempDir sits under /var/folders on macOS, which resolves into
	// /private/var — so the result is allowed to differ from the input, but it
	// must be absolute and must exist.
	if !filepath.IsAbs(got) {
		t.Errorf("result %q is not absolute", got)
	}
	if info, err := os.Stat(got); err != nil || !info.IsDir() {
		t.Errorf("result %q is not an existing directory", got)
	}
}

func TestCanonicalize_RejectsMalformedPaths(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tests := []struct {
		name string
		path string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"relative", "some/relative/dir"},
		{"dot", "."},
		{"nonexistent", filepath.Join(t.TempDir(), "does-not-exist")},
		{"a file, not a directory", file},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := canonicalize(tc.path); !errors.Is(err, ErrInvalidPath) {
				t.Fatalf("want ErrInvalidPath, got %v", err)
			}
		})
	}
}

func TestCanonicalize_RejectsBroadRoots(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}

	paths := []string{
		"/",
		home,
		home + "/", // trailing slash must not sneak past Clean
		"/etc",
		"/usr",
		"/bin",
		"/sbin",
		"/System",
		"/Library",
		"/Applications",
		"/Users",
		"/private",
		"/var",
		"/tmp",
	}

	for _, p := range paths {
		t.Run(p, func(t *testing.T) {
			if _, err := os.Stat(p); err != nil {
				t.Skipf("%s does not exist on this machine", p)
			}
			_, err := canonicalize(p)
			if !errors.Is(err, ErrPathNotAllowed) {
				t.Fatalf("canonicalize(%q) must be refused, got err=%v", p, err)
			}
		})
	}
}

// A path inside the home directory is the normal case and must work — it is
// home *itself* that is refused.
func TestCanonicalize_AcceptsDirectoryInsideHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	dir := filepath.Join(home, "development")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Skip("no ~/development on this machine")
	}

	if _, err := canonicalize(dir); err != nil {
		t.Fatalf("a directory inside home must be allowed, got %v", err)
	}
}

// The guard that actually matters: a symlink must not be able to smuggle a
// denied root past the denylist. Symlinks are resolved before the check.
func TestCanonicalize_RejectsSymlinkToDeniedRoot(t *testing.T) {
	targets := []string{"/", "/etc", "/usr"}

	for _, target := range targets {
		t.Run("link to "+target, func(t *testing.T) {
			if _, err := os.Stat(target); err != nil {
				t.Skipf("%s does not exist", target)
			}

			link := filepath.Join(t.TempDir(), "shortcut")
			if err := os.Symlink(target, link); err != nil {
				t.Fatalf("Symlink: %v", err)
			}

			_, err := canonicalize(link)
			if !errors.Is(err, ErrPathNotAllowed) {
				t.Fatalf("a symlink to %s must be refused, got err=%v", target, err)
			}
		})
	}
}

// `..` must not walk out of an allowed directory into a denied one.
func TestCanonicalize_RejectsDotDotEscapeToRoot(t *testing.T) {
	dir := t.TempDir()
	escape := dir + strings.Repeat("/..", 12) // far past the filesystem root

	_, err := canonicalize(escape)
	if !errors.Is(err, ErrPathNotAllowed) {
		t.Fatalf("a `..` walk to / must be refused, got err=%v", err)
	}
}

// macOS's default filesystem is case-insensitive, so /ETC is /etc. A
// case-sensitive denylist would be bypassed by simply shouting.
func TestCanonicalize_RejectsDeniedRootInDifferentCase(t *testing.T) {
	variants := []string{"/ETC", "/Etc", "/USR"}

	for _, v := range variants {
		t.Run(v, func(t *testing.T) {
			if _, err := os.Stat(v); err != nil {
				t.Skipf("%s does not resolve on this filesystem (case-sensitive)", v)
			}
			_, err := canonicalize(v)
			if !errors.Is(err, ErrPathNotAllowed) {
				t.Fatalf("canonicalize(%q) must be refused, got err=%v", v, err)
			}
		})
	}
}

// A directory inside a denied system tree is denied too — unlike the broad
// container roots, nothing under /etc is a legitimate project.
func TestCanonicalize_RejectsDirectoryInsideSystemTree(t *testing.T) {
	candidates := []string{"/etc/ssl", "/usr/share", "/usr/local", "/Library/Fonts"}

	for _, p := range candidates {
		t.Run(p, func(t *testing.T) {
			if info, err := os.Stat(p); err != nil || !info.IsDir() {
				t.Skipf("%s does not exist", p)
			}
			_, err := canonicalize(p)
			if !errors.Is(err, ErrPathNotAllowed) {
				t.Fatalf("canonicalize(%q) must be refused, got err=%v", p, err)
			}
		})
	}
}

// Two spellings of the same directory canonicalize to one path, which is what
// makes registration idempotent.
func TestCanonicalize_IsStableAcrossSpellings(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "project")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	link := filepath.Join(dir, "link-to-project")
	if err := os.Symlink(sub, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	direct, err := canonicalize(sub)
	if err != nil {
		t.Fatalf("canonicalize(sub): %v", err)
	}
	viaLink, err := canonicalize(link)
	if err != nil {
		t.Fatalf("canonicalize(link): %v", err)
	}
	viaDotDot, err := canonicalize(filepath.Join(sub, "..", "project"))
	if err != nil {
		t.Fatalf("canonicalize(dotdot): %v", err)
	}

	if direct != viaLink || direct != viaDotDot {
		t.Fatalf("same directory canonicalized three ways:\n direct=%q\n link  =%q\n dotdot=%q",
			direct, viaLink, viaDotDot)
	}
}
