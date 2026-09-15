// Package secrets holds the one thing this repository did not hold before: a
// credential the operator typed in.
//
// ---------------------------------------------------------------------------
// Why this is new, and why it is small.
// ---------------------------------------------------------------------------
// Until now Mimir configured no model credential at all. `docs/SECURITY.md` said
// so plainly, and it was true: the CLIs ride the operator's own logins, and
// `internal/account` never sees a secret — it points `claude auth login` at a
// directory and lets the keychain keep what comes back.
//
// An API-key connection breaks that, and it breaks it deliberately: a key the
// operator minted in a vendor's console is the only way to reach a provider
// that has no CLI. So this package exists, and it is written to be the *only*
// place a model credential is ever read or written. Nothing else in the tree
// takes a secret as a parameter, returns one, or logs one.
//
// ---------------------------------------------------------------------------
// The design, and the honest part of it.
// ---------------------------------------------------------------------------
// The industry answer for a cross-platform desktop app is Electron's
// `safeStorage`, which VS Code, Slack and Discord ship: the OS store where there
// is one — macOS Keychain, Windows DPAPI, Linux Secret Service — and a
// documented fallback where there is not. The part worth copying is not the
// encryption; it is that `safeStorage` **reports which backend it got**
// (`getSelectedStorageBackend() === "basic_text"`) so an application can tell
// the operator their secret is not really protected.
//
// So `Backend()` is part of the interface rather than an implementation detail.
// A machine with no usable OS store still works — the file vault is 0600 in a
// 0700 directory — and the operator is *told*, at the moment they add a key,
// that this is where it went. Hiding that would be the security weakness; the
// fallback itself is not.
package secrets

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/zalando/go-keyring"
)

// ErrNotFound means no secret is stored under that reference. It is a state,
// not a failure: a connection that has not been given a key yet is a normal
// connection.
var ErrNotFound = errors.New("secrets: no secret stored under that reference")

// Backend names where secrets actually live.
const (
	// BackendOS is the operating system's own credential store.
	BackendOS = "os-keychain"
	// BackendFile is a 0600 file beside the store. Honest, portable, and
	// weaker — which is why it is reported rather than assumed.
	BackendFile = "file"
)

// service is the account namespace in the OS store. One name for the whole
// application, with the connection id as the account, so an operator looking at
// Keychain Access sees one Mimir group rather than a scatter.
const service = "studio.mimir.connection"

// Vault stores and returns credentials.
//
// The interface is four methods and no more. In particular there is no List:
// nothing in this system has a reason to enumerate secrets, and an enumerator
// is the shape a leak takes.
type Vault interface {
	Get(ref string) (string, error)
	Put(ref, secret string) error
	Delete(ref string) error
	// Backend says where secrets are actually kept, so the operator can be
	// told. See the package doc.
	Backend() string
}

// Open returns the best vault this machine can give, and says which it is.
//
// The OS store is probed rather than assumed, because "is there a keychain" is
// not answerable from the platform alone: a headless Linux session has no D-Bus
// Secret Service, a locked keychain refuses, and a container has neither. The
// probe writes a value, reads it back and deletes it — the only question that
// matters is whether a round trip works, and asking it costs one write.
//
// It never fails. A machine with no OS store gets the file vault, which is the
// point of having one.
func Open(dir string) Vault {
	if probeOS() == nil {
		return &osVault{}
	}
	return &fileVault{path: filepath.Join(dir, "credentials.json")}
}

const probeRef = "__mimir_probe__"

func probeOS() error {
	const want = "ok"
	if err := keyring.Set(service, probeRef, want); err != nil {
		return err
	}
	got, err := keyring.Get(service, probeRef)
	// Deleted whatever happened: a probe that leaves a row behind is a probe
	// that has to be explained to whoever opens Keychain Access.
	_ = keyring.Delete(service, probeRef)
	if err != nil {
		return err
	}
	if got != want {
		return errors.New("secrets: the OS store did not return what was written")
	}
	return nil
}

// --- the OS store ------------------------------------------------------------

type osVault struct{}

func (*osVault) Backend() string { return BackendOS }

func (*osVault) Get(ref string) (string, error) {
	v, err := keyring.Get(service, ref)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", fmt.Errorf("%w: %s", ErrNotFound, ref)
	}
	if err != nil {
		// Deliberately not wrapped with the value or the reference's contents:
		// an error from this package is read by a log.
		return "", errors.New("secrets: the OS store could not be read")
	}
	return v, nil
}

func (*osVault) Put(ref, secret string) error {
	if err := keyring.Set(service, ref, secret); err != nil {
		return errors.New("secrets: the OS store could not be written")
	}
	return nil
}

func (*osVault) Delete(ref string) error {
	err := keyring.Delete(service, ref)
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		// Deleting what is not there is a success: the caller wanted it gone.
		return nil
	}
	return errors.New("secrets: the OS store could not be written")
}

// --- the file fallback -------------------------------------------------------

// fileVault is a JSON object of ref → secret, 0600, in a 0700 directory.
//
// It does not reuse `settings.writeFile`, and that is not an oversight:
// that writer chmods 0644, which is correct for a settings file and wrong for
// this by exactly the margin that matters.
type fileVault struct {
	mu   sync.Mutex
	path string
}

func (*fileVault) Backend() string { return BackendFile }

func (v *fileVault) Get(ref string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	all, err := v.read()
	if err != nil {
		return "", err
	}
	secret, ok := all[ref]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrNotFound, ref)
	}
	return secret, nil
}

func (v *fileVault) Put(ref, secret string) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	all, err := v.read()
	if err != nil {
		return err
	}
	all[ref] = secret
	return v.write(all)
}

func (v *fileVault) Delete(ref string) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	all, err := v.read()
	if err != nil {
		return err
	}
	delete(all, ref)
	return v.write(all)
}

func (v *fileVault) read() (map[string]string, error) {
	data, err := os.ReadFile(v.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, errors.New("secrets: the credentials file could not be read")
	}
	out := map[string]string{}
	if err := json.Unmarshal(data, &out); err != nil {
		// Not repaired and not discarded: a file that will not parse may be a
		// file somebody edited, and silently replacing it loses their keys.
		return nil, errors.New("secrets: the credentials file is not valid JSON")
	}
	return out, nil
}

func (v *fileVault) write(all map[string]string) error {
	data, err := json.Marshal(all)
	if err != nil {
		return errors.New("secrets: the credentials could not be encoded")
	}
	dir := filepath.Dir(v.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errors.New("secrets: the credentials directory could not be created")
	}

	// Written to a temp file in the same directory and renamed, so a crash
	// mid-write cannot leave a half-file where the keys used to be. Created
	// 0600 rather than chmod'ed afterwards: between the two there is a window
	// where the file exists and is readable.
	tmp, err := os.CreateTemp(dir, ".credentials-*")
	if err != nil {
		return errors.New("secrets: the credentials file could not be created")
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return errors.New("secrets: the credentials file could not be secured")
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return errors.New("secrets: the credentials file could not be written")
	}
	if err := tmp.Close(); err != nil {
		return errors.New("secrets: the credentials file could not be closed")
	}
	if err := os.Rename(tmp.Name(), v.path); err != nil {
		return errors.New("secrets: the credentials file could not be replaced")
	}
	return nil
}
