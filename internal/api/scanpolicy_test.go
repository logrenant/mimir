package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/logrenant/mimir/internal/settings"
)

// The real settings store against a temp directory rather than a fake. What is
// under test here is the round trip — a folder picked on a screen, validated,
// written, and read back by the scan — and a fake store would assert only that
// the handler called a method.
func policyServer(t *testing.T, defaults []string) (http.Handler, *fakeScanner) {
	t.Helper()
	cfg := testConfig()
	cfg.BrainScanRoots = defaults

	scanner := &fakeScanner{}
	return New(cfg, Deps{
		Settings:  settings.New(t.TempDir()),
		BrainScan: scanner,
	}).Handler(), scanner
}

func decodePolicy(t *testing.T, body string) scanPolicyResponse {
	t.Helper()
	var got scanPolicyResponse
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decoding %q: %v", body, err)
	}
	return got
}

// Before anything is saved the screen must be told these are the shipped
// folders, not the operator's — the two sentences read differently and only one
// of them invites a look.
func TestScanPolicy_UnconfiguredReportsTheDefaults(t *testing.T) {
	dir := t.TempDir()
	h, _ := policyServer(t, []string{dir})

	w := do(h, "GET", "/brain/scan/policy", testToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	got := decodePolicy(t, w.Body.String())

	if got.Configured {
		t.Error("configured = true before anything was saved")
	}
	if len(got.Roots) != 1 || got.Roots[0] != dir {
		t.Errorf("roots = %v, want the default %q", got.Roots, dir)
	}
	if len(got.DefaultRoots) != 1 {
		t.Errorf("default_roots = %v, want the shipped list", got.DefaultRoots)
	}
}

func TestScanPolicy_SaveRoundTripsAndWakesTheScan(t *testing.T) {
	root := t.TempDir()
	h, scanner := policyServer(t, []string{t.TempDir()})

	// What comes back is the *resolved* path, not the one that was sent.
	// On macOS a temp dir is under /var, which is a symlink to /private/var,
	// and resolving before judging is the whole reason a link to / cannot be
	// smuggled in as a root.
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}

	body := `{"roots":["` + root + `"],"excludes":["` + filepath.Join(root, "private") + `"]}`
	w := do(h, "PUT", "/brain/scan/policy", testToken, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	got := decodePolicy(t, w.Body.String())
	if !got.Configured {
		t.Error("configured = false after a save")
	}
	if len(got.Roots) != 1 || got.Roots[0] != resolved {
		t.Errorf("roots = %v, want [%s]", got.Roots, resolved)
	}
	if len(got.Excludes) != 1 {
		t.Errorf("excludes = %v, want the one entry", got.Excludes)
	}

	// A control, not a suggestion: the operator who just excluded a folder
	// should not wait out the idle interval for it to take effect.
	if scanner.woken == 0 {
		t.Error("saving a policy did not wake the scan")
	}

	// And it is readable back on a fresh request.
	again := decodePolicy(t, do(h, "GET", "/brain/scan/policy", testToken, "").Body.String())
	if len(again.Roots) != 1 || again.Roots[0] != resolved {
		t.Errorf("re-read roots = %v, want [%s]", again.Roots, resolved)
	}
}

// A root is a permission being granted, so it goes through the same guard a
// coding task's folder does. The home directory is the case that matters: it is
// the one an operator would plausibly pick, and it would turn a sweep into a
// walk of everything they own.
func TestScanPolicy_RefusesRootsTheDirectoryGuardRejects(t *testing.T) {
	h, _ := policyServer(t, nil)

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory on this machine")
	}

	for _, bad := range []string{"/", home, "/etc", "relative/path", "/no/such/directory/here"} {
		w := do(h, "PUT", "/brain/scan/policy", testToken, `{"roots":["`+bad+`"]}`)
		if w.Code != http.StatusBadRequest {
			t.Errorf("root %q: status = %d, want 400 (%s)", bad, w.Code, strings.TrimSpace(w.Body.String()))
		}
	}
}

// An exclusion only ever takes permission away, so it needs none of that guard
// — and it has to be allowed to name a file, and a path that does not exist
// yet. Excluding something you are about to create is a reasonable thing to
// want, and refusing it would push the operator to create the file first.
func TestScanPolicy_ExclusionsNeedNotExist(t *testing.T) {
	root := t.TempDir()
	h, _ := policyServer(t, nil)

	body := `{"roots":["` + root + `"],"excludes":["/not/created/yet.env","` +
		filepath.Join(root, "secrets.txt") + `"]}`
	w := do(h, "PUT", "/brain/scan/policy", testToken, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if got := decodePolicy(t, w.Body.String()); len(got.Excludes) != 2 {
		t.Errorf("excludes = %v, want both entries", got.Excludes)
	}
}

// Removing the last folder is a decision, and it has to survive a read: seeding
// the default back over it would turn the scan on again behind the operator.
func TestScanPolicy_EmptyRootsSurviveARead(t *testing.T) {
	h, _ := policyServer(t, []string{t.TempDir()})

	if w := do(h, "PUT", "/brain/scan/policy", testToken, `{"roots":[]}`); w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	got := decodePolicy(t, do(h, "GET", "/brain/scan/policy", testToken, "").Body.String())
	if len(got.Roots) != 0 {
		t.Errorf("roots = %v, want none — the operator emptied the list", got.Roots)
	}
	if !got.Configured {
		t.Error("configured = false, but an empty list was saved on purpose")
	}
}

// Neither list carries omitempty, so a nil slice would be served as `null`.
// The desktop's types say `string[]`, and `policy.roots` reaching a length
// check as null is one of the two ways the Brain tab used to empty the window.
func TestScanPolicy_EmptyListsAreServedAsListsNotNull(t *testing.T) {
	h, _ := policyServer(t, nil)

	if w := do(h, "PUT", "/brain/scan/policy", testToken, `{"roots":[],"excludes":[]}`); w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}

	body := do(h, "GET", "/brain/scan/policy", testToken, "").Body.String()
	for _, want := range []string{`"roots":[]`, `"excludes":[]`, `"default_roots":[]`} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %s: %s", want, body)
		}
	}
}

func TestScanPolicy_ResetRestoresTheShippedRoots(t *testing.T) {
	shipped := t.TempDir()
	other := t.TempDir()
	h, _ := policyServer(t, []string{shipped})

	if w := do(h, "PUT", "/brain/scan/policy", testToken,
		`{"roots":["`+other+`"]}`); w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}

	w := do(h, "POST", "/brain/scan/policy/reset", testToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("reset: %d %s", w.Code, w.Body.String())
	}
	got := decodePolicy(t, w.Body.String())
	if len(got.Roots) != 1 || got.Roots[0] != filepath.Clean(shipped) {
		t.Errorf("roots after reset = %v, want [%s]", got.Roots, shipped)
	}
}

// The routes are gated on Settings, not on the scan: "which folders may be
// read" is worth answering on a daemon whose supervisor never started, which is
// exactly the machine somebody is debugging.
func TestScanPolicy_ServedWithoutASupervisor(t *testing.T) {
	cfg := testConfig()
	cfg.BrainScanRoots = []string{t.TempDir()}
	h := New(cfg, Deps{Settings: settings.New(t.TempDir())}).Handler()

	if w := do(h, "GET", "/brain/scan/policy", testToken, ""); w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 with no BrainScan wired: %s", w.Code, w.Body.String())
	}
}

// Without a settings store there is nowhere to save one, and the route must be
// honestly absent rather than a 500 from a nil interface.
func TestScanPolicy_AbsentWithoutASettingsStore(t *testing.T) {
	h := New(testConfig(), Deps{BrainScan: &fakeScanner{}}).Handler()

	if w := do(h, "GET", "/brain/scan/policy", testToken, ""); w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 with no Settings wired", w.Code)
	}
}
