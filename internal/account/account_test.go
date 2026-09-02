package account

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestRegister_DefaultSlotHasNoDirectory(t *testing.T) {
	r := NewRegistry(openTestStore(t))

	acct, err := r.Register(context.Background(), "", "")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !acct.IsDefault || acct.ConfigDir != "" {
		t.Errorf("the default slot is the absence of a directory: %+v", acct)
	}
	if acct.Label != "Default" {
		t.Errorf("Label: got %q, want Default", acct.Label)
	}
}

// The same directory is the same keychain entry. Two rows for it would let the
// dispatcher believe one identity could run two tasks at once — which is the
// whole thing this package exists to prevent.
func TestRegister_IsIdempotentByDirectory(t *testing.T) {
	r := NewRegistry(openTestStore(t))
	dir := filepath.Join(t.TempDir(), "slot-b")

	first, err := r.Register(context.Background(), "b", dir)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	second, err := r.Register(context.Background(), "a different label", dir)
	if err != nil {
		t.Fatalf("Register again: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("the same directory produced two accounts: %s and %s", first.ID, second.ID)
	}
}

func TestRegister_CreatesTheDirectoryAndRefusesAFile(t *testing.T) {
	r := NewRegistry(openTestStore(t))
	tmp := t.TempDir()

	// The contents are irrelevant — the CLI hashes the path — so creating it
	// is kinder than making the operator mkdir first.
	dir := filepath.Join(tmp, "made", "up")
	if _, err := r.Register(context.Background(), "b", dir); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Errorf("the directory was not created: %v", err)
	}

	file := filepath.Join(tmp, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := r.Register(context.Background(), "bad", file); !errors.Is(err, ErrInvalidDir) {
		t.Errorf("want ErrInvalidDir for a file, got %v", err)
	}

	// A typo that silently became a new identity is the failure worth
	// preventing, so a relative path is refused rather than resolved.
	if _, err := r.Register(context.Background(), "bad", "slot-b"); !errors.Is(err, ErrInvalidDir) {
		t.Errorf("want ErrInvalidDir for a relative path, got %v", err)
	}
}

func TestList_IsOldestFirstSoAssignmentIsPredictable(t *testing.T) {
	r := NewRegistry(openTestStore(t))
	ctx := context.Background()

	if _, err := r.Register(ctx, "first", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	time.Sleep(1100 * time.Millisecond) // created_at has second resolution
	if _, err := r.Register(ctx, "second", filepath.Join(t.TempDir(), "b")); err != nil {
		t.Fatalf("Register: %v", err)
	}

	accounts, err := r.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(accounts) != 2 || accounts[0].Label != "first" {
		t.Fatalf("order: got %+v", accounts)
	}
}

// Forgetting a slot with work attached would strand a queue nothing can drain.
func TestDelete_RefusesWhileWorkIsAttached(t *testing.T) {
	st := openTestStore(t)
	r := NewRegistry(st)
	ctx := context.Background()

	acct, err := r.Register(ctx, "b", filepath.Join(t.TempDir(), "b"))
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := st.InsertRun(ctx, store.RunRow{
		ID: "queued-run", ProjectID: "p", Prompt: "x",
		Status: store.RunStatusQueued, RequestedAccountID: acct.ID,
		CreatedAt: time.Now().UTC(), QueuedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("InsertRun: %v", err)
	}

	if err := r.Delete(ctx, acct.ID); !errors.Is(err, ErrAccountInUse) {
		t.Fatalf("want ErrAccountInUse, got %v", err)
	}

	// Once the work is over, the slot can be forgotten. The credentials stay
	// in the keychain: Mimir has no business logging anybody out.
	if _, err := st.UpdateRunStatus(ctx, "queued-run",
		store.RunStatusQueued, store.RunStatusCompleted,
		time.Time{}, time.Now().UTC(), ""); err != nil {
		t.Fatalf("UpdateRunStatus: %v", err)
	}
	if err := r.Delete(ctx, acct.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := r.Get(ctx, acct.ID); !errors.Is(err, ErrAccountNotFound) {
		t.Errorf("want ErrAccountNotFound, got %v", err)
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
