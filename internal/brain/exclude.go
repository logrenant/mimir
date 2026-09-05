package brain

import (
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// What a scan refuses to read, in two layers that answer two different
// questions.
//
// `Excluder` is the operator's layer: paths they pointed at and said "not this
// one". It is theirs to edit and it can be emptied.
//
// `isSecret` is not. It is a shipped denylist of the file shapes that hold
// credentials, and it is not configurable because the failure it prevents is
// not a preference: a scan reads a file and hands its contents to an external
// model CLI, so a `.env` that slips through has already left the machine by the
// time anybody notices. The operator's list can only ever subtract from what is
// read; this one subtracts before they are asked.

// Excluder answers "may this path be read?" for one policy.
//
// The zero value excludes nothing, which is what a daemon with no saved policy
// wants — and what every existing caller gets by leaving the field alone.
type Excluder struct {
	// prefixes are cleaned absolute paths. A directory entry matches its whole
	// subtree; a file entry matches only itself.
	prefixes []string
}

// NewExcluder builds a matcher from the operator's list.
func NewExcluder(paths []string) Excluder {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" || !filepath.IsAbs(p) {
			continue
		}
		out = append(out, filepath.Clean(p))
	}
	sort.Strings(out)
	return Excluder{prefixes: out}
}

// Empty reports whether this excluder would let everything through.
func (e Excluder) Empty() bool { return len(e.prefixes) == 0 }

// List returns the entries, for a status snapshot.
func (e Excluder) List() []string {
	if len(e.prefixes) == 0 {
		return nil
	}
	out := make([]string, len(e.prefixes))
	copy(out, e.prefixes)
	return out
}

// Excludes reports whether an absolute path is covered.
//
// Matching is on path *components*, not on the string: `/a/b` must exclude
// `/a/b/c.go` and must not exclude `/a/bravo.go`. A plain `strings.HasPrefix`
// gets the second one wrong, which is the classic way a path denylist turns out
// to have been decorative.
func (e Excluder) Excludes(abs string) bool {
	if len(e.prefixes) == 0 || abs == "" {
		return false
	}
	abs = filepath.Clean(abs)
	for _, p := range e.prefixes {
		if abs == p {
			return true
		}
		if strings.HasPrefix(abs, p+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// secretNames are exact base names that hold credentials.
var secretNames = map[string]struct{}{
	".env":             {},
	".envrc":           {},
	".npmrc":           {},
	".pypirc":          {},
	".netrc":           {},
	".pgpass":          {},
	".htpasswd":        {},
	"credentials":      {},
	"kubeconfig":       {},
	"id_rsa":           {},
	"id_dsa":           {},
	"id_ecdsa":         {},
	"id_ed25519":       {},
	"secrets.yml":      {},
	"secrets.yaml":     {},
	"terraform.tfvars": {},
}

// secretExts are extensions that are a key or a certificate by definition.
var secretExts = map[string]struct{}{
	".pem":      {},
	".key":      {},
	".p12":      {},
	".pfx":      {},
	".jks":      {},
	".keystore": {},
	".asc":      {},
	".gpg":      {},
	".kdbx":     {},
	".tfstate":  {},
}

// secretFragments are substrings that mark a file as a credential wherever they
// appear in its name. Kept short and specific: a fragment list is the part of a
// denylist that goes wrong, and "secret" alone would drop `secrets.md`, a
// perfectly ordinary design note.
var secretFragments = []string{
	"service-account",
	"serviceaccount",
	"_rsa",
	"credentials.json",
}

// isSecret reports whether a repository-relative path looks like a credential.
//
// Name-shaped rather than content-shaped on purpose. Deciding by content would
// mean reading the file to find out whether it was safe to read, and the read is
// the thing being prevented.
func isSecret(rel string) bool {
	base := strings.ToLower(path.Base(rel))
	if base == "" {
		return false
	}
	if _, ok := secretNames[base]; ok {
		return true
	}
	// `.env.local`, `.env.production`, `.env.local.bak` — the dotfile plus any
	// suffix. Matched as a prefix because the suffix is unbounded in practice.
	if strings.HasPrefix(base, ".env.") || strings.HasSuffix(base, ".env") {
		return true
	}
	if _, ok := secretExts[strings.ToLower(path.Ext(base))]; ok {
		return true
	}
	// `terraform.tfstate.backup` and friends: the extension test above sees
	// `.backup`, so the state file needs its own substring check.
	if strings.Contains(base, ".tfstate") {
		return true
	}
	for _, frag := range secretFragments {
		if strings.Contains(base, frag) {
			return true
		}
	}
	// A directory that exists to hold keys. Checked on the path rather than the
	// base name so `~/project/.ssh/known_hosts` goes too.
	for _, seg := range strings.Split(rel, "/") {
		switch strings.ToLower(seg) {
		case ".ssh", ".gnupg", ".aws", ".kube", ".docker":
			return true
		}
	}
	return false
}
