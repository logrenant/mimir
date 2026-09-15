// Package skills holds the working instructions each sub-agent cannot work
// without: how a marketing brief is framed, what a code review is obliged to
// look at, how a question is asked of the knowledge graph, what an outreach
// letter may and may not say.
//
// The shape is deliberately internal/settings' rule-file shape, and the reason
// is the same one that package gives for existing: a skill body is not a
// property of the machine, it is the operator's own writing, so it cannot be an
// internal/config constant without the app lying about what it offers. What is
// new here is that the *set* is closed and the *use* is mandatory — a rule file
// that will not load costs a lead-gen letter its guidance, but a skill that
// will not load stops the job. That is the whole difference between guidance
// and a contract, and it is why Body() never fails while the callers that gate
// on it do.
//
// Version is the half of a cache key a skill contributes. A skill silently
// changing would silently change everything produced under it, which is exactly
// the failure settings.RuleBody was written to prevent.
package skills

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed skills/*.md
var defaultSkills embed.FS

// ErrUnknownSkill is returned for an id outside the closed set below. It is a
// closed set for the same reason settings.Channel is: adding a skill means
// shipping a default for it, not adding a string somewhere.
var ErrUnknownSkill = errors.New("skills: unknown skill")

// The five skills this binary ships. The ids are wire strings — the agent
// registry names them, the desktop settings screen keys on them, and a run row
// records them — so they are never renamed, only added to.
const (
	Marketing    = "marketing"
	CodeReview   = "code-review"
	GraphQuery   = "graph-query"
	LeadOutreach = "lead-outreach"
	// ProductContent is the method a catalog rewrite follows. The brand's own
	// voice and markup vocabulary are read from the operator's CSV, never from
	// here: this file says how a listing is written, not how this store sounds.
	ProductContent = "product-content"
	// ProductContentAR / ProductContentEN are the target languages' own files.
	// They are separate from ProductContent because the *method* does not
	// change with the language and the typography does: one file would make an
	// operator edit Arabic punctuation rules to correct an English one, and it
	// would not fit the body ceiling either.
	ProductContentAR = "product-content-ar"
	ProductContentEN = "product-content-en"
)

// titles are what a skill is called on screen. Kept here rather than parsed out
// of the Markdown so an operator who rewrites a body cannot accidentally rename
// the thing the registry points at.
var titles = map[string]string{
	Marketing:        "Pazarlama",
	CodeReview:       "Kod incelemesi",
	GraphQuery:       "Grafik sorgusu",
	LeadOutreach:     "Lead outreach",
	ProductContent:   "Ürün içeriği",
	ProductContentEN: "Ürün içeriği — İngilizce",
	ProductContentAR: "Ürün içeriği — Arapça",
}

// IDs is every skill, in the order a UI should offer them.
func IDs() []string {
	return []string{
		Marketing, CodeReview, GraphQuery, LeadOutreach,
		ProductContent, ProductContentEN, ProductContentAR,
	}
}

// Known reports whether id names a shipped skill.
func Known(id string) bool { _, ok := titles[id]; return ok }

// Skill is one skill file as the settings screen sees it.
type Skill struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Path is the file on disk. Shown because an operator may well prefer
	// their own editor, and a file whose location is a secret is a file
	// nobody trusts.
	Path string `json:"path"`
	Body string `json:"body"`
	// IsDefault is true while the body is byte-identical to the shipped one.
	IsDefault bool      `json:"is_default"`
	Version   string    `json:"version"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// Store reads and writes the operator's skills under one directory.
//
// The mutex covers this process's own writes, not the files: an operator
// editing a skill in their own editor is expected, not a race to defend
// against. It only keeps two concurrent HTTP requests from interleaving.
type Store struct {
	dir string
	mu  sync.RWMutex
}

// New returns a store rooted at dir. The directory is created on the first
// write, not here.
func New(dir string) *Store { return &Store{dir: filepath.Clean(dir)} }

// Dir is where this store keeps its files.
func (s *Store) Dir() string {
	if s == nil {
		return ""
	}
	return s.dir
}

// Path is the file one skill lives in.
func (s *Store) Path(id string) string {
	if s == nil {
		return ""
	}
	return filepath.Join(s.dir, id+".md")
}

// Default is the body this binary ships for a skill. Exported so the settings
// screen can offer "restore the default" without a second source of truth for
// what the default is.
func Default(id string) (string, error) {
	if !Known(id) {
		return "", fmt.Errorf("%w: %s", ErrUnknownSkill, id)
	}
	b, err := defaultSkills.ReadFile("skills/" + id + ".md")
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrUnknownSkill, id)
	}
	return string(b), nil
}

// Skill returns one skill, seeding the file from the shipped default the first
// time it is asked for. Seeding on read rather than at startup is what makes
// the file discoverable: it exists as soon as the operator opens the screen
// that mentions it, and a daemon nobody configured writes nothing.
func (s *Store) Skill(id string) (Skill, error) {
	def, err := Default(id)
	if err != nil {
		return Skill{}, err
	}
	if s == nil {
		return skill(id, "", def, time.Time{}), nil
	}

	s.mu.RLock()
	path := s.Path(id)
	b, readErr := os.ReadFile(path)
	s.mu.RUnlock()

	if errors.Is(readErr, os.ErrNotExist) {
		// Best effort: an unwritable directory costs the operator a file
		// they can open in their editor, not the ability to run the job.
		s.mu.Lock()
		_ = writeFile(path, []byte(def))
		s.mu.Unlock()
		return skill(id, path, def, time.Time{}), nil
	}
	if readErr != nil {
		return Skill{}, fmt.Errorf("skills: reading %s: %w", path, readErr)
	}

	var modified time.Time
	if info, err := os.Stat(path); err == nil {
		modified = info.ModTime()
	}
	return skill(id, path, string(b), modified), nil
}

// Skills returns every skill, in IDs() order.
func (s *Store) Skills() ([]Skill, error) {
	out := make([]Skill, 0, len(IDs()))
	for _, id := range IDs() {
		sk, err := s.Skill(id)
		if err != nil {
			return nil, err
		}
		out = append(out, sk)
	}
	return out, nil
}

// PutSkill replaces one skill's body. An empty body is a reset rather than an
// empty instruction: a skill with nothing in it would silently drop the
// guidance every job under it was run with, and "I cleared the box" is much
// more likely to mean "start over" than "run with no instructions at all".
func (s *Store) PutSkill(id, body string) (Skill, error) {
	def, err := Default(id)
	if err != nil {
		return Skill{}, err
	}
	if s == nil {
		return Skill{}, errors.New("skills: no store")
	}
	if strings.TrimSpace(body) == "" {
		body = def
	}
	body = strings.ReplaceAll(body, "\r\n", "\n")

	s.mu.Lock()
	path := s.Path(id)
	err = writeFile(path, []byte(body))
	s.mu.Unlock()
	if err != nil {
		return Skill{}, err
	}
	return skill(id, path, body, time.Now()), nil
}

// ResetSkill puts the shipped default back.
func (s *Store) ResetSkill(id string) (Skill, error) {
	def, err := Default(id)
	if err != nil {
		return Skill{}, err
	}
	return s.PutSkill(id, def)
}

// Body is the hot path: the text and the version it hashes to, with no file
// metadata and no error worth stopping for.
//
// It falls back to the shipped default rather than failing, and the callers
// that enforce the mandate gate on the returned body being non-empty. That
// split is deliberate: "the operator's file is unreadable" should still run the
// job under the instructions this binary shipped, while "there is no such
// skill" should not run it at all. Only the second returns empty.
func (s *Store) Body(id string) (body, version string) {
	sk, err := s.Skill(id)
	if err == nil && strings.TrimSpace(sk.Body) != "" {
		return sk.Body, sk.Version
	}
	def, defErr := Default(id)
	if defErr != nil || strings.TrimSpace(def) == "" {
		return "", ""
	}
	return def, version8(def)
}

// BodySource is the one method a caller needs to resolve a skill set: the
// text and the version it hashes to. *Store satisfies it, and so does a test
// double that withholds a skill.
type BodySource interface {
	Body(id string) (body, version string)
}

// Require resolves everything a caller cannot work without: the composed body
// and the version the result is written under, in the order the ids are given.
//
// It exists because that version string is a **cache key half** that two
// packages have to agree on — the runner writes a catalog draft under it and
// the HTTP layer reads the draft back by it — and they used to build it
// separately. They drifted: the runner composed "product-content:<v>" while the
// API returned "", so every draft a bulk rewrite paid for was stored under a key
// no read ever looked up. One function, called from both sides, is the fix; two
// correct implementations of the same format is the bug.
//
// ok is false when any body is empty, which is the mandate: a missing skill
// refuses the job rather than running it unguided.
func Require(src BodySource, ids []string) (body, version string, ok bool) {
	if src == nil || len(ids) == 0 {
		return "", "", false
	}
	parts := make([]string, 0, len(ids))
	versions := make([]string, 0, len(ids))
	for _, id := range ids {
		b, v := src.Body(id)
		if strings.TrimSpace(b) == "" {
			return "", "", false
		}
		parts = append(parts, b)
		versions = append(versions, id+":"+v)
	}
	return strings.Join(parts, "\n\n---\n\n"), strings.Join(versions, "|"), true
}

// Compose joins several skills into the one block a run is given, in IDs()
// order rather than call order so two cards naming the same set hash the same.
// The second return is a version for the composition as a whole.
//
// A missing skill is not skipped: it returns ok=false and the caller refuses
// the job. That is the mandate.
func (s *Store) Compose(ids []string) (body, version string, ok bool) {
	want := map[string]struct{}{}
	for _, id := range ids {
		if !Known(id) {
			return "", "", false
		}
		want[id] = struct{}{}
	}
	if len(want) == 0 {
		return "", "", false
	}

	var parts []string
	var versions []string
	for _, id := range IDs() {
		if _, ok := want[id]; !ok {
			continue
		}
		b, v := s.Body(id)
		if strings.TrimSpace(b) == "" {
			return "", "", false
		}
		parts = append(parts, b)
		versions = append(versions, id+":"+v)
	}
	sort.Strings(versions)
	return strings.Join(parts, "\n\n---\n\n"), version8(strings.Join(versions, "|")), true
}

func skill(id, path, body string, modified time.Time) Skill {
	def, _ := Default(id)
	return Skill{
		ID:        id,
		Title:     titles[id],
		Path:      path,
		Body:      body,
		IsDefault: body == def,
		Version:   version8(body),
		UpdatedAt: modified,
	}
}

// version8 is the cache-key half a skill contributes. Eight hex characters is
// 32 bits — enough that two skills an operator actually writes will not
// collide, short enough to stay readable in a run row somebody is debugging.
func version8(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])[:8]
}

// writeFile creates the parent directory and writes atomically, so a crash
// mid-write leaves the previous body rather than half of the new one.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("skills: creating %s: %w", filepath.Dir(path), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("skills: writing %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("skills: writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("skills: writing %s: %w", path, err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("skills: writing %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("skills: writing %s: %w", path, err)
	}
	return nil
}
