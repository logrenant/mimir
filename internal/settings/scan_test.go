package settings

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestScanPolicy_UnwrittenIsNotConfigured(t *testing.T) {
	s := New(t.TempDir())

	p, configured, err := s.ScanPolicy()
	if err != nil {
		t.Fatalf("ScanPolicy: %v", err)
	}
	if configured {
		t.Error("a store that was never written reports configured = true")
	}
	if !p.IsZero() {
		t.Errorf("policy = %+v, want zero", p)
	}
}

// The distinction the bool exists for. An operator who removes every folder has
// made a decision, and seeding the default over it would turn the scan back on
// behind their back.
func TestScanPolicy_EmptyRootsIsADecisionNotAnAbsence(t *testing.T) {
	s := New(t.TempDir())
	defaults := []string{"/Users/x/development"}

	if got := s.EffectiveScanPolicy(defaults); len(got.Roots) != 1 {
		t.Fatalf("before any save, roots = %v, want the defaults", got.Roots)
	}

	if _, err := s.PutScanPolicy(ScanPolicy{}); err != nil {
		t.Fatalf("PutScanPolicy: %v", err)
	}
	if got := s.EffectiveScanPolicy(defaults); len(got.Roots) != 0 {
		t.Errorf("after saving an empty policy, roots = %v, want none", got.Roots)
	}
}

func TestPutScanPolicy_RoundTripsNormalised(t *testing.T) {
	s := New(t.TempDir())

	stored, err := s.PutScanPolicy(ScanPolicy{
		Roots: []string{"/Users/x/dev/", "/Users/x/dev", "/Users/x/docs/./"},
		Excludes: []string{
			"/Users/x/dev/secret.txt",
			"/Users/x/dev/secret.txt",
		},
	})
	if err != nil {
		t.Fatalf("PutScanPolicy: %v", err)
	}

	wantRoots := []string{"/Users/x/dev", "/Users/x/docs"}
	if len(stored.Roots) != len(wantRoots) {
		t.Fatalf("roots = %v, want %v (deduplicated and cleaned)", stored.Roots, wantRoots)
	}
	for i, want := range wantRoots {
		if stored.Roots[i] != want {
			t.Errorf("roots[%d] = %q, want %q", i, stored.Roots[i], want)
		}
	}
	if len(stored.Excludes) != 1 {
		t.Errorf("excludes = %v, want one entry", stored.Excludes)
	}

	read, configured, err := s.ScanPolicy()
	if err != nil || !configured {
		t.Fatalf("ScanPolicy after Put: configured=%v err=%v", configured, err)
	}
	if len(read.Roots) != 2 || len(read.Excludes) != 1 {
		t.Errorf("read back %+v, want the stored shape", read)
	}
}

// A root is a permission being granted, so a relative path is refused rather
// than quietly dropped: the caller asked for something that cannot mean what
// they think it means.
func TestPutScanPolicy_RejectsRelativePaths(t *testing.T) {
	s := New(t.TempDir())

	if _, err := s.PutScanPolicy(ScanPolicy{Roots: []string{"relative/dev"}}); !errors.Is(err, ErrBadScanPath) {
		t.Errorf("relative root: err = %v, want ErrBadScanPath", err)
	}
	if _, err := s.PutScanPolicy(ScanPolicy{Excludes: []string{"relative/x"}}); !errors.Is(err, ErrBadScanPath) {
		t.Errorf("relative exclude: err = %v, want ErrBadScanPath", err)
	}
}

// The file is meant to be readable and editable by a person auditing what Mimir
// may read, so one bad line must cost that line and not the whole policy.
func TestScanPolicy_HandEditedFileLosesOnlyTheBadLine(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if err := os.WriteFile(filepath.Join(dir, "scan.json"),
		[]byte(`{"roots":["/Users/x/dev","not-absolute",""],"excludes":null}`), 0o600); err != nil {
		t.Fatal(err)
	}

	p, configured, err := s.ScanPolicy()
	if err != nil || !configured {
		t.Fatalf("configured=%v err=%v", configured, err)
	}
	if len(p.Roots) != 1 || p.Roots[0] != "/Users/x/dev" {
		t.Errorf("roots = %v, want just the absolute one", p.Roots)
	}
}

// A corrupt file reads as not-configured rather than as an error that stops the
// scan: refusing to work because of a stray comma fails in the more surprising
// direction (SD-6).
func TestScanPolicy_CorruptFileFallsBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if err := os.WriteFile(filepath.Join(dir, "scan.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.ScanPolicy(); err == nil {
		t.Error("a corrupt file should report its error to the caller")
	}
	got := s.EffectiveScanPolicy([]string{"/Users/x/development"})
	if len(got.Roots) != 1 || got.Roots[0] != "/Users/x/development" {
		t.Errorf("effective roots = %v, want the defaults", got.Roots)
	}
}

func TestScanPolicy_NilStoreIsSafe(t *testing.T) {
	var s *Store
	if _, configured, err := s.ScanPolicy(); configured || err != nil {
		t.Errorf("nil store: configured=%v err=%v", configured, err)
	}
	if got := s.EffectiveScanPolicy([]string{"/a"}); len(got.Roots) != 1 {
		t.Errorf("nil store effective = %v, want the defaults", got.Roots)
	}
}
