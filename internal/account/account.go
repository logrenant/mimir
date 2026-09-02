// Package account owns which Claude Code identity a coding run spends.
//
// An account here is a credential slot, not a login. The CLI keeps its
// credentials in the macOS keychain and derives the entry it uses from
// CLAUDE_SECURESTORAGE_CONFIG_DIR, so a directory path is the entire handle: a
// slot with no directory is the CLI's default, and any other directory is a
// second, separately authenticated identity. Mimir never sees, stores or moves
// a credential — it only decides which slot a subprocess is pointed at.
//
// The shape mirrors internal/project deliberately: a path is accepted once, at
// registration, and everything afterwards carries the opaque id this hands
// back. That is the same discipline for the same reason, and it is why this is
// a registry rather than a field on a request.
package account

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/logrenant/mimir/internal/store"
)

var (
	// ErrInvalidDir means the directory is relative, missing, or not a
	// directory.
	ErrInvalidDir = errors.New("account: config directory is not usable")

	// ErrAccountNotFound means no account has that id.
	ErrAccountNotFound = errors.New("account: no such account")

	// ErrAccountInUse means the account still has work attached to it, so
	// forgetting it now would strand a queue.
	ErrAccountInUse = errors.New("account: this account still has queued or running work")
)

// Account is a registered credential slot.
type Account struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// ConfigDir is empty for the CLI's default slot. It is reported so the
	// operator can tell two slots apart; it holds no secret, only a path whose
	// hash names a keychain entry.
	ConfigDir  string    `json:"config_dir"`
	IsDefault  bool      `json:"is_default"`
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
	DeleteAccount(ctx context.Context, id string) error
	CountRunsForAccount(ctx context.Context, id string, statuses ...string) (int, error)
}

// Registry is the one place an account comes from.
type Registry struct {
	store Store
}

func NewRegistry(s Store) *Registry { return &Registry{store: s} }

func newID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("account: generating id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func fromRow(r store.AccountRow) Account {
	return Account{
		ID:         r.ID,
		Label:      r.Label,
		ConfigDir:  r.ConfigDir,
		IsDefault:  r.ConfigDir == "",
		CreatedAt:  r.CreatedAt,
		LastUsedAt: r.LastUsedAt,
	}
}

// canonical validates a config directory and returns it in the one form the
// store keys on.
//
// It is created if it does not exist: the directory's contents are irrelevant —
// the CLI hashes the path to name a keychain entry — so asking the operator to
// mkdir first would be ceremony with no meaning. It must still be absolute and
// must still be a directory, because a typo that silently became a new identity
// is the failure worth preventing.
func canonical(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", nil // the CLI's default slot
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("%w: %q is not an absolute path", ErrInvalidDir, dir)
	}
	clean := filepath.Clean(dir)

	if err := os.MkdirAll(clean, 0o700); err != nil {
		return "", fmt.Errorf("%w: %q could not be created (%v)", ErrInvalidDir, clean, err)
	}
	info, err := os.Stat(clean)
	if err != nil {
		return "", fmt.Errorf("%w: %q could not be read (%v)", ErrInvalidDir, clean, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: %q is a file, not a directory", ErrInvalidDir, clean)
	}
	// EvalSymlinks after the directory is known to exist, so two registrations
	// of the same slot through different paths are one account.
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return clean, nil
	}
	return resolved, nil
}

// Register records a credential slot, or returns the one already registered for
// that directory.
//
// Idempotent by directory, not by label: the same path is the same keychain
// entry, and two rows for it would let the dispatcher believe one identity
// could run two tasks at once — which is the whole thing this package exists to
// prevent.
func (r *Registry) Register(ctx context.Context, label, configDir string) (Account, error) {
	dir, err := canonical(configDir)
	if err != nil {
		return Account{}, err
	}

	if existing, found, err := r.store.FindAccountByConfigDir(ctx, dir); err != nil {
		return Account{}, err
	} else if found {
		return fromRow(existing), nil
	}

	label = strings.TrimSpace(label)
	if label == "" {
		label = defaultLabel(dir)
	}

	id, err := newID()
	if err != nil {
		return Account{}, err
	}
	row := store.AccountRow{
		ID:        id,
		Label:     label,
		ConfigDir: dir,
		CreatedAt: time.Now().UTC(),
	}
	if err := r.store.InsertAccount(ctx, row); err != nil {
		return Account{}, err
	}
	return fromRow(row), nil
}

func defaultLabel(dir string) string {
	if dir == "" {
		return "Default"
	}
	return filepath.Base(dir)
}

// Get returns a registered account.
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

// List returns every registered slot, oldest first — which is the order the
// dispatcher tries them in.
func (r *Registry) List(ctx context.Context) ([]Account, error) {
	rows, err := r.store.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Account, len(rows))
	for i, row := range rows {
		out[i] = fromRow(row)
	}
	return out, nil
}

// Touch records that a run just used this slot.
func (r *Registry) Touch(ctx context.Context, id string, at time.Time) error {
	if id == "" {
		return nil
	}
	return r.store.TouchAccount(ctx, id, at)
}

// Delete forgets a slot, refusing while work still points at it.
//
// The credentials are not touched: they live in the keychain, and Mimir has no
// business logging anybody out. Deleting here means "stop offering this slot".
func (r *Registry) Delete(ctx context.Context, id string) error {
	if _, err := r.Get(ctx, id); err != nil {
		return err
	}
	n, err := r.store.CountRunsForAccount(ctx, id,
		store.RunStatusQueued, store.RunStatusRunning)
	if err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%w: %d run(s)", ErrAccountInUse, n)
	}
	return r.store.DeleteAccount(ctx, id)
}
