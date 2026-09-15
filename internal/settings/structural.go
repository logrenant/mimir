package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The structural layer's one setting: whether to use Graphify at all.
//
// It lives here rather than in internal/config for the reason this package
// exists (SD-1): whether an outside tool on *this* machine may be run is the
// operator's answer, not the build's. It differs per person — most machines
// will not have Graphify installed — and, like the scan policy next to it, it
// is a decision about what Mimir is allowed to do rather than a timeout.
//
// The default is on. That is not a decision to run anything: with Graphify
// absent, "on" means the daemon looks for it once per sweep, does not find it,
// and does nothing. The alternative — default off — would mean an operator who
// installed Graphify because Mimir asked them to would then have to find a
// switch to make it matter.

// Structural is what the operator has said about the parser.
type Structural struct {
	// Enabled turns the whole pass off, including the look for the
	// interpreter. Off is a real answer: it is how somebody with Graphify
	// installed for another project keeps it out of Mimir's sweeps.
	Enabled bool `json:"enabled"`
	// Python is the interpreter to ask. Empty means the shipped default, which
	// is `python3` on PATH — named because a machine can easily have three and
	// only one of them has the package.
	Python string `json:"python,omitempty"`
}

// DefaultStructural is what applies before anything is saved.
var DefaultStructural = Structural{Enabled: true}

func (s *Store) structuralPath() string { return filepath.Join(s.dir, "structural.json") }

// StructuralSettings returns the operator's answer and whether they have given
// one. Same signature and same reasons as ScanPolicy: "off" and "never asked"
// are different states, and only one of them may be overwritten by a default.
func (s *Store) StructuralSettings() (Structural, bool, error) {
	if s == nil {
		return DefaultStructural, false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	b, err := os.ReadFile(s.structuralPath())
	if errors.Is(err, os.ErrNotExist) {
		return DefaultStructural, false, nil
	}
	if err != nil {
		return DefaultStructural, false, fmt.Errorf("settings: reading %s: %w", s.structuralPath(), err)
	}

	var out Structural
	if err := json.Unmarshal(b, &out); err != nil {
		return DefaultStructural, false, fmt.Errorf("settings: parsing %s: %w", s.structuralPath(), err)
	}
	out.Python = strings.TrimSpace(out.Python)
	return out, true, nil
}

// EffectiveStructural is the answer in force, with the default filled in.
func (s *Store) EffectiveStructural() Structural {
	out, configured, err := s.StructuralSettings()
	if err != nil || !configured {
		return DefaultStructural
	}
	return out
}

// PutStructural saves the answer and returns it as stored.
func (s *Store) PutStructural(in Structural) (Structural, error) {
	if s == nil {
		return Structural{}, errors.New("settings: no store")
	}
	in.Python = strings.TrimSpace(in.Python)
	// An interpreter is a program to run, so it is named rather than pathed by
	// the operator in the normal case; a path with a directory in it is
	// accepted too, and both are resolved by exec.LookPath at the far end.
	if strings.ContainsAny(in.Python, " \t\n") {
		return Structural{}, fmt.Errorf("%w: an interpreter is one word, not a command line", ErrBadScanPath)
	}

	b, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return Structural{}, fmt.Errorf("settings: encoding the structural settings: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := writeFile(s.structuralPath(), append(b, '\n')); err != nil {
		return Structural{}, err
	}
	return in, nil
}
