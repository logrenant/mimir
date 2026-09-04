package account

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Slot is a credential slot as the filesystem describes it: a label to read
// and the directory the CLI hashes into a keychain entry name.
//
// It is not an Account. An Account exists once the registry has given the
// directory an id; a Slot is only the claim that one should.
type Slot struct {
	Label     string
	ConfigDir string
}

// defaultSlotNames are subdirectory names that mean the CLI's own slot rather
// than a second identity.
//
// The operator's shell maps exactly these to "do not set the variable"
// (`_claude_acct_dir` in ~/.zshrc), and the two must agree: a directory named
// `default` treated here as a directory would hash into a third, nameless
// identity that the shell can never reach and that no `claude login` has ever
// signed into. `a` and `salihdevran` are that file's back-compat aliases for
// the same slot, kept for the same reason.
var defaultSlotNames = map[string]bool{
	"default":     true,
	"a":           true,
	"salihdevran": true,
}

// Discover reads the credential slots on this machine.
//
// The CLI's own slot always comes first and always exists — it is the one a
// machine that never heard of any of this has been using — and every
// subdirectory of accountsDir follows, sorted by name so the dispatcher's
// "oldest first" order is stable across a rescan.
//
// A missing or unreadable accountsDir is not an error. It means one account,
// which is a true answer about the machine.
func Discover(accountsDir string) []Slot {
	slots := []Slot{{Label: "Default", ConfigDir: ""}}

	accountsDir = strings.TrimSpace(accountsDir)
	if accountsDir == "" {
		return slots
	}

	entries, err := os.ReadDir(accountsDir)
	if err != nil {
		return slots
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		// A slot is a directory. Dotfiles are the OS's (.DS_Store and
		// friends), never an identity somebody created.
		if strings.HasPrefix(name, ".") {
			continue
		}
		if !e.IsDir() {
			// A symlink to a directory is still a slot: the CLI resolves it,
			// and canonical() in Register resolves it the same way.
			info, statErr := os.Stat(filepath.Join(accountsDir, name))
			if statErr != nil || !info.IsDir() {
				continue
			}
		}
		if defaultSlotNames[strings.ToLower(name)] {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		slots = append(slots, Slot{Label: name, ConfigDir: filepath.Join(accountsDir, name)})
	}
	return slots
}

// Sync registers every discovered slot and marks it as the filesystem's.
//
// It adds and never removes. A row whose directory has since been deleted
// stays: a run may be pinned to it, and the honest report for a slot that
// cannot be read is a failing probe on its row — not a slot that vanishes from
// under a queue. Registration is idempotent by directory, so a rescan is free.
//
// The returned list is every registered slot afterwards, in the registry's own
// order, which is what a caller wants to hand straight back to a client.
func (r *Registry) Sync(ctx context.Context, slots []Slot) ([]Account, error) {
	for _, slot := range slots {
		acct, err := r.Register(ctx, slot.Label, slot.ConfigDir)
		if err != nil {
			return nil, fmt.Errorf("account: syncing %q: %w", slot.Label, err)
		}
		if acct.Discovered {
			continue
		}
		if err := r.store.MarkAccountDiscovered(ctx, acct.ID); err != nil {
			return nil, err
		}
	}
	return r.List(ctx)
}
