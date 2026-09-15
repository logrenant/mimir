// Package account owns the one Claude Code identity Mimir spends.
//
// There is exactly one, and it is Mimir's own. The CLI keeps its credentials
// in the macOS keychain and derives the entry it uses from
// CLAUDE_SECURESTORAGE_CONFIG_DIR, so a directory path is the entire handle —
// and the path this package hands the CLI is one Mimir derived for itself
// (config.ClaudeSessionDir), never the CLI's default slot and never a slot the
// operator signed into from their own shell.
//
// That separation is what makes the lifecycle safe. The slot survives a quit —
// connecting is a one-time thing, and the next launch reconciles what is left
// with the keychain (Restore) rather than signing it out. Signing out is the
// operator's own act (Reset), and it is also how they switch to another
// account. A reset that reached the default slot would log the operator out of
// the terminal they were using, which is why the slot is Mimir's own.
//
// Mimir still never sees, stores or moves a credential. It runs `claude auth
// login` (see login.go), points it at its own directory, and lets the keychain
// keep what comes back.
package account

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/logrenant/mimir/internal/store"
)

var (
	// ErrNoSessionDir means the registry was built without a directory to
	// sign into, which is a wiring mistake rather than an operator's.
	ErrNoSessionDir = errors.New("account: no session directory configured")

	// ErrAccountNotFound means no account has that id — including the case
	// that matters most, that nothing is connected at all.
	ErrAccountNotFound = errors.New("account: no such account")

	// ErrNotConnected means no Claude account is connected, so there is no
	// identity for a run to spend.
	ErrNotConnected = errors.New("account: no Claude account is connected — connect one first")
)

// Account is the connected Claude identity.
//
// ConfigDir is reported because it is what the operator would type to reach
// the same slot from a terminal. It holds no secret: the CLI hashes it to name
// a keychain entry, and the credential never leaves the keychain.
type Account struct {
	ID         string    `json:"id"`
	Label      string    `json:"label"`
	ConfigDir  string    `json:"config_dir"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
}

// Store is the persistence the registry needs. *store.Store satisfies it.
type Store interface {
	InsertAccount(ctx context.Context, a store.AccountRow) error
	GetAccount(ctx context.Context, id string) (store.AccountRow, bool, error)
	FindAccountByConfigDir(ctx context.Context, dir string) (store.AccountRow, bool, error)
	ListAccounts(ctx context.Context) ([]store.AccountRow, error)
	TouchAccount(ctx context.Context, id string, at time.Time) error
	DeleteAllAccounts(ctx context.Context) error
}

// Registry is the one place the account comes from.
//
// It holds the CLI path as well as the store because the two things it does
// beyond bookkeeping — signing in and signing out — are both subprocesses, and
// a caller that had to pass the path each time would eventually pass a
// different one to login than to logout.
type Registry struct {
	store   Store
	dir     string
	cliPath string

	login loginFlow
}

// NewRegistry builds the registry around Mimir's own credential slot.
func NewRegistry(s Store, sessionDir, cliPath string) *Registry {
	return &Registry{store: s, dir: sessionDir, cliPath: cliPath}
}

// Dir is the credential slot Mimir signs into. Callers that spawn a `claude`
// of their own pass it to Environ.
func (r *Registry) Dir() string { return r.dir }

func newID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("account: generating id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func fromRow(row store.AccountRow) Account {
	return Account{
		ID:         row.ID,
		Label:      row.Label,
		ConfigDir:  row.ConfigDir,
		CreatedAt:  row.CreatedAt,
		LastUsedAt: row.LastUsedAt,
	}
}

// ensureDir creates the slot directory if it is not there.
//
// The contents are irrelevant — the CLI hashes the path to name a keychain
// entry — so this is about the path existing, not about what is in it.
func (r *Registry) ensureDir() error {
	if r.dir == "" {
		return ErrNoSessionDir
	}
	if !filepath.IsAbs(r.dir) {
		return fmt.Errorf("%w: %q is not an absolute path", ErrNoSessionDir, r.dir)
	}
	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		return fmt.Errorf("account: creating %q: %w", r.dir, err)
	}
	return nil
}

// record writes the row for the connected account, or returns the one already
// there.
//
// Idempotent by directory, and there is only ever one directory, so this is
// also what keeps the table to a single row.
func (r *Registry) record(ctx context.Context) (Account, error) {
	if err := r.ensureDir(); err != nil {
		return Account{}, err
	}
	if existing, found, err := r.store.FindAccountByConfigDir(ctx, r.dir); err != nil {
		return Account{}, err
	} else if found {
		return fromRow(existing), nil
	}

	id, err := newID()
	if err != nil {
		return Account{}, err
	}
	row := store.AccountRow{
		ID:        id,
		Label:     "Claude",
		ConfigDir: r.dir,
		CreatedAt: time.Now().UTC(),
	}
	if err := r.store.InsertAccount(ctx, row); err != nil {
		return Account{}, err
	}
	return fromRow(row), nil
}

// Current returns the connected account, if there is one.
func (r *Registry) Current(ctx context.Context) (Account, bool, error) {
	rows, err := r.store.ListAccounts(ctx)
	if err != nil {
		return Account{}, false, err
	}
	if len(rows) == 0 {
		return Account{}, false, nil
	}
	return fromRow(rows[0]), true, nil
}

// Get returns the connected account by id.
func (r *Registry) Get(ctx context.Context, id string) (Account, error) {
	row, found, err := r.store.GetAccount(ctx, id)
	if err != nil {
		return Account{}, err
	}
	if !found {
		return Account{}, fmt.Errorf("%w: %s", ErrAccountNotFound, id)
	}
	return fromRow(row), nil
}

// List returns the connected account, or nothing.
//
// It stays a list because the dispatcher reads it as capacity — one connected
// account is one lane, none is none — and because the HTTP surface has always
// answered with an array.
func (r *Registry) List(ctx context.Context) ([]Account, error) {
	acct, ok, err := r.Current(ctx)
	if err != nil || !ok {
		return nil, err
	}
	return []Account{acct}, nil
}

// Touch records that a run just used the account.
func (r *Registry) Touch(ctx context.Context, id string, at time.Time) error {
	if id == "" {
		return nil
	}
	return r.store.TouchAccount(ctx, id, at)
}

// Reset signs the slot out, removes it, and forgets the row.
//
// This is what "çıkış yap" means, and all three parts are needed for it to be
// true: the row alone is bookkeeping, the keychain entry is the login, and the
// directory is the handle the entry is named after. Leaving any one behind
// would leave the next login connected as the account the operator just asked
// to leave — which matters, because this is also how they switch accounts.
//
// It runs only when asked. A launch runs Restore instead.
//
// Errors from the CLI are not returned. A slot that was never signed in makes
// `claude auth logout` exit non-zero, and that is the expected case at
// startup, not a failure of the reset.
func (r *Registry) Reset(ctx context.Context) error {
	r.cancelLogin()

	if r.dir != "" {
		logout(ctx, r.cliPath, r.dir)
		// Removed after the logout, not before: the directory is what names
		// the keychain entry, so deleting it first would leave the entry
		// behind with nothing able to address it again.
		if err := os.RemoveAll(r.dir); err != nil {
			return fmt.Errorf("account: removing %q: %w", r.dir, err)
		}
	}
	return r.store.DeleteAllAccounts(ctx)
}

// Restore reconciles the surviving slot with the keychain at startup.
//
// The slot outlives the app: quitting Mimir no longer signs it out, so the
// login the operator made once is still there at the next launch and the
// directory that names its keychain entry is still on disk. What a launch
// cannot assume is that the entry behind it is still good — a login lapses, or
// is revoked from somewhere else entirely — and a row with no login behind it
// would advertise capacity the keychain does not back. So the probe decides,
// and it costs nothing to ask.
//
// Three answers, and the difference between the last two is the whole reason
// this is not just a Probe call at the call site:
//
//   - signed in: keep the slot and make sure the row is there. record is
//     idempotent by directory, so this also repairs a lost database.
//   - signed out, cleanly: a full Reset. The CLI answered, the slot is empty,
//     and leaving the directory would leave a handle to nothing.
//   - the probe could not answer at all: forget the row, keep the directory.
//     A missing CLI or a locked keychain is not evidence the login is gone,
//     and the directory is the only thing that can address the entry again —
//     removing it here would orphan a perfectly good login for good.
func (r *Registry) Restore(ctx context.Context) (Account, bool, error) {
	if r.dir == "" {
		return Account{}, false, ErrNoSessionDir
	}

	// Nothing has ever signed in here, or a Reset already cleaned up. No point
	// spawning a probe to be told so.
	if _, err := os.Stat(r.dir); os.IsNotExist(err) {
		return Account{}, false, r.store.DeleteAllAccounts(ctx)
	}

	status, err := Probe(ctx, r.cliPath, r.dir)
	if err != nil {
		return Account{}, false, fmt.Errorf("account: probing %q: %w", r.dir, err)
	}

	switch {
	case status.LoggedIn:
		acct, err := r.record(ctx)
		if err != nil {
			return Account{}, false, err
		}
		return acct, true, nil

	case status.Error != "":
		// The row goes, because capacity has to stay honest, but the directory
		// stays: this is "we could not ask", not "there is nobody there".
		if err := r.store.DeleteAllAccounts(ctx); err != nil {
			return Account{}, false, err
		}
		return Account{}, false, fmt.Errorf("account: the slot at %q could not be read: %s", r.dir, status.Error)

	default:
		return Account{}, false, r.Reset(ctx)
	}
}
