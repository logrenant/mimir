// Package settings holds the few values the operator owns rather than the
// binary: which model a lead-gen run spends by default, and the two rule files
// that shape what an outreach message says.
//
// It is deliberately *not* internal/config, and the split is the whole point of
// the package existing. config is SD-1 — constants, no setters, no file reader —
// because a timeout or a token ceiling is a property of the machine and a knob
// for it is a knob nobody remembers turning. What lives here fails that test in
// both directions: an outreach rule file is not a property of the machine, it is
// the operator's own writing, and the model a run spends is a decision they make
// per campaign rather than per build. Neither can be a constant without the app
// lying about what it offers.
//
// Everything is on disk, beside the store, and every read tolerates a directory
// that does not exist: a settings file that cannot be read means the defaults,
// never an error on a screen the operator was trying to open (SD-6).
package settings

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Channel is the medium an outreach message is written for. It is a closed set
// and it lives here, in the package that owns the rule files, because a channel
// *is* a rule file — adding a third one means shipping a third default, not
// adding a string somewhere.
type Channel string

const (
	ChannelEmail    Channel = "email"
	ChannelWhatsApp Channel = "whatsapp"
)

// Channels is every channel, in the order a UI should offer them: email first,
// because it is the one that existed before this package and the one every
// cached draft was written for.
func Channels() []Channel { return []Channel{ChannelEmail, ChannelWhatsApp} }

// ParseChannel resolves a wire string. The empty string is email — a client
// written before WhatsApp existed sends no channel and means the one it knew.
func ParseChannel(s string) (Channel, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", string(ChannelEmail):
		return ChannelEmail, true
	case string(ChannelWhatsApp):
		return ChannelWhatsApp, true
	default:
		return "", false
	}
}

// Label is how a channel reads on screen.
func (c Channel) Label() string {
	switch c {
	case ChannelWhatsApp:
		return "WhatsApp"
	default:
		return "E-posta"
	}
}

//go:embed rules/*.md
var defaultRules embed.FS

// ErrUnknownChannel is returned for a channel outside the closed set above.
var ErrUnknownChannel = errors.New("settings: unknown outreach channel")

// Values is what the operator has chosen, as one object.
//
// Provider and Model are the *default* model selection for lead-gen's model
// stages — exactly what a per-run picker used to send, hoisted to one place so
// the choice survives the screen it was made on. Both empty is the daemon's own
// class routing, which is what every run did before this existed.
type Values struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`

	// Distill and Reason are the operator's standing preference for each class
	// of the daemon's *own* work — Brain's distil and relation passes, refine's
	// five profiles, the catalog rewrite.
	//
	// Which class a piece of work belongs to stays in code (SD-1): "is this
	// compression or synthesis" is a property of the work and not a taste.
	// Which provider serves a class on *this machine* is not — it depends on
	// what is installed and signed in here, and that is the operator's to say.
	// This is `internal/settings`' whole argument, applied to the one decision
	// that previously had nowhere to live: a machine-wide Brain scan is
	// thousands of calls, and before this the only way to point it somewhere
	// else was to edit a constant and rebuild.
	Distill Choice `json:"distill,omitzero"`
	Reason  Choice `json:"reason,omitzero"`
}

// Choice is one provider/model pair. Both halves are validated against the
// daemon's published table before they are stored — they end up as argv.
type Choice struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

// IsZero reports whether no choice has been made.
func (c Choice) IsZero() bool { return c.Provider == "" && c.Model == "" }

// IsZero reports whether no model choice has been made.
//
// It deliberately answers about the search bar's own choice only: the two class
// defaults are read by the router and a screen that asked "has anything been
// configured" about them would get the wrong answer for the field it is
// showing.
func (v Values) IsZero() bool { return v.Provider == "" && v.Model == "" }

// Rule is one channel's rule file as the settings screen sees it.
type Rule struct {
	Channel Channel `json:"channel"`
	// Path is the file on disk. Shown because the operator may well prefer to
	// edit it in their own editor, and a rule file whose location is a secret
	// is a rule file nobody trusts.
	Path string `json:"path"`
	Body string `json:"body"`
	// IsDefault is true while the body is byte-identical to the shipped one.
	IsDefault bool `json:"is_default"`
	// Version is the first 8 hex characters of sha256(body). It is a cache-key
	// half, not a display string: the rule file is part of the drafting prompt,
	// so editing it has to invalidate the drafts written under the old text or
	// the operator would change the rules and be served the old letters.
	Version   string    `json:"version"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// Store reads and writes the operator's settings under one directory.
//
// The mutex covers this process's own writes. It is not a lock over the files —
// an operator editing a rule file in their own editor is expected, not a race to
// defend against — it only keeps two concurrent HTTP requests from interleaving
// a read-modify-write of the same JSON.
type Store struct {
	dir string
	mu  sync.RWMutex
}

// New returns a store rooted at dir. The directory is created on the first
// write, not here: a daemon that never saves a setting should not leave an
// empty folder behind.
func New(dir string) *Store { return &Store{dir: filepath.Clean(dir)} }

// Dir is where this store keeps its files.
func (s *Store) Dir() string {
	if s == nil {
		return ""
	}
	return s.dir
}

func (s *Store) valuesPath() string { return filepath.Join(s.dir, "settings.json") }

// RulePath is the file one channel's rules live in.
func (s *Store) RulePath(ch Channel) string {
	return filepath.Join(s.dir, "rules", string(ch)+".md")
}

// DefaultRule is the rule file this binary ships for a channel. Exported so the
// settings screen can offer "restore the default" without a second source of
// truth for what the default is.
func DefaultRule(ch Channel) (string, error) {
	b, err := defaultRules.ReadFile("rules/" + string(ch) + ".md")
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrUnknownChannel, ch)
	}
	return string(b), nil
}

// Get returns the operator's choices, or the zero value when nothing has been
// saved. A file that cannot be read or parsed is the zero value too, with the
// error returned alongside: the caller decides whether a broken settings file is
// worth a message, and lead-gen decides it is not (SD-6).
func (s *Store) Get() (Values, error) {
	if s == nil {
		return Values{}, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	b, err := os.ReadFile(s.valuesPath())
	if errors.Is(err, os.ErrNotExist) {
		return Values{}, nil
	}
	if err != nil {
		return Values{}, fmt.Errorf("settings: reading %s: %w", s.valuesPath(), err)
	}

	var v Values
	if err := json.Unmarshal(b, &v); err != nil {
		return Values{}, fmt.Errorf("settings: parsing %s: %w", s.valuesPath(), err)
	}
	v.Provider = strings.TrimSpace(v.Provider)
	v.Model = strings.TrimSpace(v.Model)
	return v, nil
}

// Put saves the operator's choices.
//
// It does not validate the provider/model pair. That check belongs to whoever
// is about to spend it — internal/api holds the allow-list both strings become
// argv against — and duplicating it here would give the daemon two answers to
// the same question.
func (s *Store) Put(v Values) error {
	if s == nil {
		return errors.New("settings: no store")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	v.Provider = strings.TrimSpace(v.Provider)
	v.Model = strings.TrimSpace(v.Model)

	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("settings: encoding values: %w", err)
	}
	return writeFile(s.valuesPath(), append(b, '\n'))
}

// Rule returns one channel's rules, seeding the file from the shipped default
// the first time it is asked for. Seeding on read rather than at startup is what
// makes the file discoverable: it exists as soon as the operator opens the
// screen that mentions it, and a daemon nobody configured writes nothing.
func (s *Store) Rule(ch Channel) (Rule, error) {
	if s == nil {
		return Rule{}, errors.New("settings: no store")
	}
	def, err := DefaultRule(ch)
	if err != nil {
		return Rule{}, err
	}

	s.mu.RLock()
	path := s.RulePath(ch)
	b, readErr := os.ReadFile(path)
	s.mu.RUnlock()

	if errors.Is(readErr, os.ErrNotExist) {
		// Best effort: an unwritable directory costs the operator a file they
		// can open in their editor, not the ability to draft a message.
		s.mu.Lock()
		_ = writeFile(path, []byte(def))
		s.mu.Unlock()
		return rule(ch, path, def, time.Time{}), nil
	}
	if readErr != nil {
		return Rule{}, fmt.Errorf("settings: reading %s: %w", path, readErr)
	}

	var modified time.Time
	if info, err := os.Stat(path); err == nil {
		modified = info.ModTime()
	}
	return rule(ch, path, string(b), modified), nil
}

// Rules returns every channel's rules, in Channels() order.
func (s *Store) Rules() ([]Rule, error) {
	out := make([]Rule, 0, len(Channels()))
	for _, ch := range Channels() {
		r, err := s.Rule(ch)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// PutRule replaces one channel's rules. An empty body is a reset rather than an
// empty prompt: a rule file with nothing in it would silently drop the guidance
// the drafts were written under, and "I cleared the box" is much more likely to
// mean "start over" than "write with no rules at all".
func (s *Store) PutRule(ch Channel, body string) (Rule, error) {
	if s == nil {
		return Rule{}, errors.New("settings: no store")
	}
	def, err := DefaultRule(ch)
	if err != nil {
		return Rule{}, err
	}
	if strings.TrimSpace(body) == "" {
		body = def
	}
	body = strings.ReplaceAll(body, "\r\n", "\n")

	s.mu.Lock()
	path := s.RulePath(ch)
	err = writeFile(path, []byte(body))
	s.mu.Unlock()
	if err != nil {
		return Rule{}, err
	}
	return rule(ch, path, body, time.Now()), nil
}

// ResetRule puts the shipped default back.
func (s *Store) ResetRule(ch Channel) (Rule, error) {
	def, err := DefaultRule(ch)
	if err != nil {
		return Rule{}, err
	}
	return s.PutRule(ch, def)
}

// RuleBody is the drafting path's read: the text and the version it hashes to,
// with no file metadata and no error worth stopping for. A rule file that cannot
// be read falls back to the shipped default, because the alternative — drafting
// with no rules at all — silently changes what every message says.
func (s *Store) RuleBody(ch Channel) (body string, version string) {
	r, err := s.Rule(ch)
	if err == nil {
		return r.Body, r.Version
	}
	def, defErr := DefaultRule(ch)
	if defErr != nil {
		return "", ""
	}
	return def, version8(def)
}

func rule(ch Channel, path, body string, modified time.Time) Rule {
	def, _ := DefaultRule(ch)
	return Rule{
		Channel:   ch,
		Path:      path,
		Body:      body,
		IsDefault: body == def,
		Version:   version8(body),
		UpdatedAt: modified,
	}
}

// version8 is the cache-key half a rule file contributes. Eight hex characters
// is 32 bits — enough that two rule files an operator actually writes will not
// collide, and short enough that the composed prompt_version stays readable in
// a database somebody is debugging.
func version8(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])[:8]
}

// writeFile creates the parent directory and writes atomically, so a crash
// mid-write leaves the previous rules rather than half of the new ones.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("settings: creating %s: %w", filepath.Dir(path), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("settings: writing %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("settings: writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("settings: writing %s: %w", path, err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("settings: writing %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("settings: writing %s: %w", path, err)
	}
	return nil
}
