package secrets

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The file vault is what a machine with no usable OS store gets, so its
// permissions are the whole guarantee. 0600 in a 0700 directory, created that
// way rather than chmod'ed afterwards — between create and chmod there is a
// window in which the file exists and is readable.
func TestFileVault_WritesUnreadableByAnybodyElse(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "settings")
	v := &fileVault{path: filepath.Join(dir, "credentials.json")}

	if err := v.Put("anthropic-1", "sk-test-value"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	info, err := os.Stat(v.path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("dosya kipi %o, 0600 bekleniyordu", mode)
	}
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if mode := dirInfo.Mode().Perm(); mode != 0o700 {
		t.Errorf("dizin kipi %o, 0700 bekleniyordu", mode)
	}
}

func TestFileVault_RoundTrips(t *testing.T) {
	v := &fileVault{path: filepath.Join(t.TempDir(), "s", "credentials.json")}

	if _, err := v.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("beklenen ErrNotFound, alınan %v", err)
	}
	if err := v.Put("a", "one"); err != nil {
		t.Fatal(err)
	}
	if err := v.Put("b", "two"); err != nil {
		t.Fatal(err)
	}
	got, err := v.Get("a")
	if err != nil || got != "one" {
		t.Fatalf("Get(a) = %q, %v", got, err)
	}

	// Deleting one leaves the other: a vault that dropped everything on a
	// delete would lose keys nobody asked it to touch.
	if err := v.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Get("a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("silinen anahtar hâlâ okunuyor: %v", err)
	}
	if got, err := v.Get("b"); err != nil || got != "two" {
		t.Errorf("diğer anahtar kayboldu: %q, %v", got, err)
	}

	// Deleting what is not there is what the caller wanted.
	if err := v.Delete("nope"); err != nil {
		t.Errorf("olmayan anahtarın silinmesi hata verdi: %v", err)
	}
}

// A file somebody hand-edited into invalid JSON is not repaired and not
// replaced: silently starting over would lose every key in it.
func TestFileVault_RefusesToSilentlyDiscardAnUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(path, []byte("{ bozuk"), 0o600); err != nil {
		t.Fatal(err)
	}
	v := &fileVault{path: path}

	if _, err := v.Get("a"); err == nil {
		t.Error("bozuk dosya sessizce boş sayıldı")
	}
	if err := v.Put("a", "x"); err == nil {
		t.Error("bozuk dosyanın üstüne yazıldı")
	}
	// And the operator's file is still there to be repaired.
	if _, err := os.Stat(path); err != nil {
		t.Errorf("operatörün dosyası silindi: %v", err)
	}
}

// The lesson taken from Electron's safeStorage: the fallback is fine, hiding it
// is not. An operator whose key went into a file has to be able to find that
// out, and the only way is for the vault to say so.
func TestVault_SaysWhichBackendItGot(t *testing.T) {
	file := &fileVault{path: filepath.Join(t.TempDir(), "credentials.json")}
	if file.Backend() != BackendFile {
		t.Errorf("dosya kasası kendini %q diye bildiriyor", file.Backend())
	}
	if (&osVault{}).Backend() != BackendOS {
		t.Error("OS kasası kendini yanlış bildiriyor")
	}

	// Open never fails: a machine with no OS store still gets a vault.
	v := Open(t.TempDir())
	switch v.Backend() {
	case BackendOS, BackendFile:
	default:
		t.Errorf("bilinmeyen arka uç %q", v.Backend())
	}
}

// Nothing here enumerates. There is no reason in this system to list secrets,
// and an enumerator is the shape a leak takes — so the file's own contents are
// the only place they exist together, and that file is 0600.
func TestFileVault_StoresNothingButTheSecrets(t *testing.T) {
	v := &fileVault{path: filepath.Join(t.TempDir(), "credentials.json")}
	if err := v.Put("anthropic-1", "sk-secret"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(v.path)
	if err != nil {
		t.Fatal(err)
	}
	var all map[string]string
	if err := json.Unmarshal(data, &all); err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all["anthropic-1"] != "sk-secret" {
		t.Errorf("beklenmeyen içerik: %v", all)
	}
}
