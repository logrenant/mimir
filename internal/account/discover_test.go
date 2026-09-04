package account

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func labels(slots []Slot) []string {
	out := make([]string, len(slots))
	for i, s := range slots {
		out[i] = s.Label
	}
	return out
}

func equal(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// A machine with no accounts directory still has one account: the CLI's own.
// That is a true answer about the machine, not a failure to read it.
func TestDiscover_MissingDirectoryIsOneDefaultSlot(t *testing.T) {
	slots := Discover(filepath.Join(t.TempDir(), "never-created"))

	if len(slots) != 1 || slots[0].ConfigDir != "" || slots[0].Label != "Default" {
		t.Fatalf("got %+v, want a single default slot", slots)
	}
	if got := Discover(""); len(got) != 1 {
		t.Errorf("an empty root: got %+v, want the default slot alone", got)
	}
}

func TestDiscover_DefaultFirstThenDirectoriesByName(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"zeta", "eziode"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o700); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}

	slots := Discover(root)

	if want := []string{"Default", "eziode", "zeta"}; !equal(labels(slots), want) {
		t.Fatalf("got %v, want %v", labels(slots), want)
	}
	if slots[1].ConfigDir != filepath.Join(root, "eziode") {
		t.Errorf("ConfigDir: got %q", slots[1].ConfigDir)
	}
}

// The shell maps `default`, `a` and `salihdevran` to "do not set the
// variable". Treated as directories here they would hash into a third,
// nameless identity no `claude login` has ever signed into.
func TestDiscover_DefaultAliasDirectoriesCollapse(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"default", "a", "salihdevran", "eziode"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o700); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}

	if want := []string{"Default", "eziode"}; !equal(labels(Discover(root)), want) {
		t.Fatalf("got %v, want %v", labels(Discover(root)), want)
	}
}

func TestDiscover_SkipsFilesAndDotEntries(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".Trash"), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "eziode"), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	if want := []string{"Default", "eziode"}; !equal(labels(Discover(root)), want) {
		t.Fatalf("got %v, want %v", labels(Discover(root)), want)
	}
}

// A rescan is meant to be free: same directories, same rows, and nothing
// registered by hand is swept away by it.
func TestSync_IsIdempotentAndAddsOnly(t *testing.T) {
	ctx := context.Background()
	r := NewRegistry(openTestStore(t))

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "eziode"), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	manual, err := r.Register(ctx, "elle", filepath.Join(t.TempDir(), "elsewhere"))
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	first, err := r.Sync(ctx, Discover(root))
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	second, err := r.Sync(ctx, Discover(root))
	if err != nil {
		t.Fatalf("Sync again: %v", err)
	}
	if len(first) != 3 || len(second) != 3 {
		t.Fatalf("slot count: first %d, second %d, want 3 each", len(first), len(second))
	}

	byID := map[string]Account{}
	for _, a := range second {
		byID[a.ID] = a
	}
	if a := byID[manual.ID]; a.Discovered {
		t.Errorf("a hand-registered slot outside the tree was marked discovered: %+v", a)
	}
	for _, a := range second {
		if a.ID == manual.ID {
			continue
		}
		if !a.Discovered {
			t.Errorf("%q came from the scan and is not marked discovered", a.Label)
		}
	}
}

// The filesystem is the authority for a discovered slot: forgetting it in the
// app would promise a removal the next scan takes back.
func TestDelete_RefusesADiscoveredSlot(t *testing.T) {
	ctx := context.Background()
	r := NewRegistry(openTestStore(t))

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "eziode"), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	accounts, err := r.Sync(ctx, Discover(root))
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}

	for _, a := range accounts {
		if err := r.Delete(ctx, a.ID); !errors.Is(err, ErrAccountDiscovered) {
			t.Errorf("Delete(%q): got %v, want ErrAccountDiscovered", a.Label, err)
		}
	}
}

// A slot is for coding runs, and only for coding runs.
//
// The registry used to carry a "background" mark that pointed the daemon's own
// refine, distil and recap calls at one slot. It was a switch with no moment
// attached: a coding run has a human who dispatched it and can say which
// account pays, and a resident sweep has nobody, so the mark quietly redirected
// every summary the daemon made afterwards. Those calls now run on the CLI's
// own login, named in cmd/mimir-daemon. Asserting the absence keeps the
// concept from growing back one method at a time.
func TestRegistry_HasNoBackgroundSlotConcept(t *testing.T) {
	ctx := context.Background()
	r := NewRegistry(openTestStore(t))

	accounts, err := r.Sync(ctx, []Slot{{Label: "Default"}, {Label: "eziode", ConfigDir: filepath.Join(t.TempDir(), "eziode")}})
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(accounts) != 2 {
		t.Fatalf("Sync registered %d slots, want 2", len(accounts))
	}

	// Every slot is routable by a coding run and none of them is special: what
	// a slot means is now the same for all of them.
	for _, a := range accounts {
		got, err := r.Get(ctx, a.ID)
		if err != nil {
			t.Fatalf("Get(%q): %v", a.Label, err)
		}
		if got.ConfigDir != a.ConfigDir {
			t.Errorf("%s: ConfigDir = %q, want %q", a.Label, got.ConfigDir, a.ConfigDir)
		}
	}
}
