package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The scan policy: which folders Brain is allowed to read, and what inside them
// it must not.
//
// This belongs here rather than in internal/config for the reason the package
// doc gives, and more sharply than the model choice does. `BrainScanRoots` was a
// constant naming two folders in a home directory, which made "what may this
// daemon read?" a property of the *build* — a question the operator could only
// answer by editing Go and recompiling. It is the opposite of a machine
// property: it is consent, it differs per person, and it is the one setting
// where being wrong means a file was read that should not have been.
//
// config keeps the same two folders as the seed. A daemon nobody has configured
// behaves exactly as it did before; the first save is what takes the decision
// away from the binary.

// ErrBadScanPath is returned for a path that cannot be a root or an exclusion.
var ErrBadScanPath = errors.New("settings: a scan path must be absolute")

// ScanPolicy is the operator's answer to "what may Brain read?".
//
// Roots are where a sweep starts. Excludes are subtracted from them, and they
// are subtracted by *prefix*: an entry naming a directory removes everything
// beneath it, an entry naming a file removes that file. Both are absolute
// paths, because a relative one has no meaning in a daemon whose working
// directory is not the operator's.
type ScanPolicy struct {
	Roots    []string `json:"roots"`
	Excludes []string `json:"excludes"`
}

// IsZero reports whether the policy names nothing at all.
func (p ScanPolicy) IsZero() bool { return len(p.Roots) == 0 && len(p.Excludes) == 0 }

func (s *Store) scanPath() string { return filepath.Join(s.dir, "scan.json") }

// ScanPolicy returns the operator's policy and whether they have ever saved one.
//
// The bool is the whole point of the signature: "no roots" and "not configured"
// are different answers and the caller has to tell them apart. A policy that has
// never been written means the shipped default applies; a policy written with an
// empty root list means the operator turned the scan off, and seeding *that*
// with the default would turn it back on behind their back.
//
// A file that cannot be read or parsed reads as not-configured, with the error
// alongside (SD-6). The alternative — refusing to scan because a JSON file has a
// stray comma — fails in the more surprising direction.
func (s *Store) ScanPolicy() (ScanPolicy, bool, error) {
	if s == nil {
		return ScanPolicy{}, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	b, err := os.ReadFile(s.scanPath())
	if errors.Is(err, os.ErrNotExist) {
		return ScanPolicy{}, false, nil
	}
	if err != nil {
		return ScanPolicy{}, false, fmt.Errorf("settings: reading %s: %w", s.scanPath(), err)
	}

	var p ScanPolicy
	if err := json.Unmarshal(b, &p); err != nil {
		return ScanPolicy{}, false, fmt.Errorf("settings: parsing %s: %w", s.scanPath(), err)
	}
	// Normalised on the way out as well as in. The file is meant to be editable
	// by hand — it names folders, and an operator auditing what Mimir may read
	// should be able to open it — so what it holds is not necessarily what the
	// last Put wrote.
	p.Roots = normalizeScanPaths(p.Roots)
	p.Excludes = normalizeScanPaths(p.Excludes)
	return p, true, nil
}

// EffectiveScanPolicy is the policy actually in force, with the shipped roots
// filled in when the operator has never saved one.
//
// One function so the two callers that need this answer — the supervisor loop
// and the settings screen — cannot drift. A screen that showed the default
// while the loop scanned something else would be the worst version of this
// feature: the operator would be reading a list of folders that is not the list
// being read.
func (s *Store) EffectiveScanPolicy(defaultRoots []string) ScanPolicy {
	p, configured, err := s.ScanPolicy()
	if err != nil || !configured {
		return ScanPolicy{Roots: normalizeScanPaths(defaultRoots)}
	}
	return p
}

// PutScanPolicy saves the policy and returns it as stored.
//
// Validation is deliberately asymmetric. A root is a permission being granted,
// so it must be an absolute path and the caller is expected to have checked it
// harder than this (internal/api runs it through the same directory guard a
// coding task's folder goes through). An exclusion only ever takes permission
// away, so it needs no guard beyond being absolute — and unlike a root it may
// name a file, and may name something that does not exist yet.
func (s *Store) PutScanPolicy(p ScanPolicy) (ScanPolicy, error) {
	if s == nil {
		return ScanPolicy{}, errors.New("settings: no store")
	}
	for _, r := range p.Roots {
		if err := checkScanPath(r); err != nil {
			return ScanPolicy{}, err
		}
	}
	for _, e := range p.Excludes {
		if err := checkScanPath(e); err != nil {
			return ScanPolicy{}, err
		}
	}

	stored := ScanPolicy{
		Roots:    normalizeScanPaths(p.Roots),
		Excludes: normalizeScanPaths(p.Excludes),
	}

	b, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return ScanPolicy{}, fmt.Errorf("settings: encoding the scan policy: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := writeFile(s.scanPath(), append(b, '\n')); err != nil {
		return ScanPolicy{}, err
	}
	return stored, nil
}

// checkScanPath is the one rule both lists share.
func checkScanPath(p string) error {
	p = strings.TrimSpace(p)
	if p == "" {
		return fmt.Errorf("%w: the path is empty", ErrBadScanPath)
	}
	if !filepath.IsAbs(p) {
		return fmt.Errorf("%w: %q is relative", ErrBadScanPath, p)
	}
	return nil
}

// normalizeScanPaths cleans, de-duplicates and orders a list.
//
// Sorted because the list is rendered as-is and an operator who adds a folder
// should not find the previous five in a new order; de-duplicated because
// adding the same folder twice is a double-click, not an instruction. Entries
// that are not absolute are dropped rather than rejected: this runs on read as
// well as write, and a hand-edited file with one bad line should lose that line,
// not the whole policy.
func normalizeScanPaths(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, raw := range in {
		p := strings.TrimSpace(raw)
		if p == "" || !filepath.IsAbs(p) {
			continue
		}
		p = filepath.Clean(p)
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	sort.Strings(out)
	if len(out) == 0 {
		// nil rather than an empty slice, so a policy that names nothing
		// marshals as `null` and reads back the same way it was written.
		return nil
	}
	return out
}
