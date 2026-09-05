package account

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()

	cfg := config.Load()
	cfg.StorePath = filepath.Join(t.TempDir(), "mimir.db")
	s, err := store.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// Connecting records exactly one row, and it is Mimir's own slot — never the
// CLI's default one, which is the operator's terminal login.
func TestRecord_WritesMimirsOwnSlot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "claude-session")
	r := NewRegistry(openTestStore(t), dir, "claude")

	acct, err := r.record(context.Background())
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if acct.ConfigDir != dir {
		t.Errorf("ConfigDir: got %q, want %q", acct.ConfigDir, dir)
	}
	if acct.ConfigDir == "" {
		t.Error("an empty ConfigDir is the CLI's own slot, which Mimir must not claim")
	}
	// The contents are irrelevant — the CLI hashes the path — so creating it
	// is kinder than making anybody mkdir first.
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Errorf("the slot directory was not created: %v", err)
	}
}

// There is one account, and a second login must not produce a second row: the
// dispatcher reads the list as capacity, and two rows would be two lanes on
// one rate limit.
func TestRecord_StaysOneRow(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "claude-session")
	r := NewRegistry(openTestStore(t), dir, "claude")
	ctx := context.Background()

	first, err := r.record(ctx)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	second, err := r.record(ctx)
	if err != nil {
		t.Fatalf("record again: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("two rows for one slot: %s and %s", first.ID, second.ID)
	}
	accounts, err := r.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(accounts) != 1 {
		t.Errorf("List: got %d accounts, want 1", len(accounts))
	}
}

// Nothing connected is the state every launch starts in, and the honest answer
// for it is an empty list rather than a default slot standing in.
func TestList_IsEmptyBeforeAnythingIsConnected(t *testing.T) {
	r := NewRegistry(openTestStore(t), filepath.Join(t.TempDir(), "slot"), "claude")

	accounts, err := r.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(accounts) != 0 {
		t.Errorf("got %+v, want nothing connected", accounts)
	}
	if _, ok, err := r.Current(context.Background()); err != nil || ok {
		t.Errorf("Current: got ok=%v err=%v, want not connected", ok, err)
	}
}

// Reset is the whole "closing Mimir resets the accounts" contract: the login is
// signed out through the CLI, the directory that names its keychain entry goes,
// and the row goes with it. A row left behind would advertise capacity the
// keychain no longer backs.
func TestReset_SignsOutRemovesTheSlotAndForgetsTheRow(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "claude-session")
	marker := filepath.Join(tmp, "logout-ran")

	// A stand-in CLI: the real one talks to the keychain, and what this has to
	// prove is that the logout is attempted with the slot pointed at Mimir's
	// own directory.
	fake := filepath.Join(tmp, "fake-claude.sh")
	script := "#!/bin/sh\n" +
		`if [ "$1" = auth ] && [ "$2" = logout ]; then printf %s "$CLAUDE_SECURESTORAGE_CONFIG_DIR" > ` +
		marker + "; fi\nexit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	r := NewRegistry(openTestStore(t), dir, fake)
	ctx := context.Background()
	if _, err := r.record(ctx); err != nil {
		t.Fatalf("record: %v", err)
	}

	if err := r.Reset(ctx); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("the logout never ran: %v", err)
	}
	if string(got) != dir {
		t.Errorf("logged out of %q, want Mimir's own slot %q", got, dir)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the slot directory survived the reset: %v", err)
	}
	accounts, err := r.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(accounts) != 0 {
		t.Errorf("the row survived the reset: %+v", accounts)
	}
}

// A relative path would be resolved against whatever directory the daemon
// happens to be in, which is a different keychain entry every time.
func TestEnsureDir_RefusesARelativePath(t *testing.T) {
	r := NewRegistry(openTestStore(t), "claude-session", "claude")
	if err := r.ensureDir(); !errors.Is(err, ErrNoSessionDir) {
		t.Errorf("want ErrNoSessionDir, got %v", err)
	}
}

// The child environment is where the account is actually chosen, and where a
// nested session's variables have to stop.
func TestEnviron(t *testing.T) {
	parent := []string{
		"PATH=/usr/bin",
		"HOME=/Users/x",
		"CLAUDECODE=1",
		"CLAUDE_CODE_SESSION_ID=abc",
		"CLAUDE_CODE_MESSAGING_TOKEN=secret",
		"CLAUDE_SECURESTORAGE_CONFIG_DIR=/inherited/slot",
		"AI_AGENT=claude-code",
	}

	t.Run("a named slot is pointed at", func(t *testing.T) {
		got := Environ(parent, "/slots/b")
		if !contains(got, "CLAUDE_SECURESTORAGE_CONFIG_DIR=/slots/b") {
			t.Errorf("the slot was not selected: %v", got)
		}
		if contains(got, "CLAUDE_SECURESTORAGE_CONFIG_DIR=/inherited/slot") {
			t.Error("the inherited slot survived and would win or duplicate")
		}
	})

	t.Run("the default slot is the variable's absence", func(t *testing.T) {
		// The empty string reaches the same slot, but absent is the narrower
		// claim: it cannot be read as naming anything. What matters either way
		// is that an inherited value must not leak through as the default.
		for _, kv := range Environ(parent, "") {
			if strings.HasPrefix(kv, "CLAUDE_SECURESTORAGE_CONFIG_DIR=") {
				t.Errorf("the default slot must not set the variable: %q", kv)
			}
		}
	})

	t.Run("a nested session's variables are stripped", func(t *testing.T) {
		got := Environ(parent, "")
		for _, unwanted := range []string{"CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=abc",
			"CLAUDE_CODE_MESSAGING_TOKEN=secret", "AI_AGENT=claude-code"} {
			if contains(got, unwanted) {
				t.Errorf("%q reached the child; it would think it was resuming a session", unwanted)
			}
		}
		// The machine's own environment is not the session's, and must survive.
		if !contains(got, "PATH=/usr/bin") || !contains(got, "HOME=/Users/x") {
			t.Errorf("PATH and HOME must survive: %v", got)
		}
	})
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// A slot that cannot be read is an answer about that slot, not a failure of the
// daemon: the operator needs it on the account row, not as a 500.
func TestProbe_ReportsAnUnreadableSlotAsAStatus(t *testing.T) {
	notClaude := filepath.Join(t.TempDir(), "not-claude.sh")
	if err := os.WriteFile(notClaude, []byte("#!/bin/sh\necho 'not json'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	status, err := Probe(context.Background(), notClaude, "")
	if err != nil {
		t.Fatalf("Probe should not error on unreadable output: %v", err)
	}
	if status.LoggedIn || status.Error == "" {
		t.Errorf("want a not-logged-in status carrying a reason, got %+v", status)
	}
}

func TestProbe_ReportsANonZeroExitAsAStatus(t *testing.T) {
	failing := filepath.Join(t.TempDir(), "failing.sh")
	if err := os.WriteFile(failing, []byte("#!/bin/sh\necho 'keychain locked' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	status, err := Probe(context.Background(), failing, "")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !strings.Contains(status.Error, "keychain locked") {
		t.Errorf("the CLI's own reason should survive: %+v", status)
	}
}
