package brain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/sync/errgroup"

	"github.com/logrenant/mimir/internal/store"
)

// HashStore is the "have I already read this?" lookup a scan needs.
type HashStore interface {
	BrainNodeHashes(ctx context.Context, projectPath, kind, promptVersion string) (map[string]string, error)
}

// ScanOptions bound one pass.
type ScanOptions struct {
	// Limit is how many files this pass will distil. A scan is deliberately
	// incremental rather than a single long job: the caller is an MCP tool with
	// a request timeout, and a pass that returns "twelve done, three hundred
	// left" is resumable by construction, where a four-hour call is not.
	Limit int

	// DryRun reports what a pass would cost without spending anything.
	DryRun bool
}

// ScanResult is what one pass did, and what is left.
type ScanResult struct {
	Scanned   int      `json:"scanned"`
	Skipped   int      `json:"skipped_unchanged"`
	Failed    int      `json:"failed"`
	Remaining int      `json:"remaining"`
	Eligible  int      `json:"eligible_total"`
	DryRun    bool     `json:"dry_run,omitempty"`
	Files     []string `json:"files,omitempty"`
}

// Scan reads a repository into Brain, one bounded batch at a time.
//
// This is the one deliberately expensive operation here: a distil per file,
// paid once per repository and amortised over every later session that would
// otherwise have opened the file to find out what it is. Three things keep that
// bill from being paid twice.
//
// A file whose content hash and prompt version already match is skipped without
// being read past its digest, so a second scan of an unchanged repository makes
// no model call at all and an interrupted scan resumes where it stopped.
// DryRun answers "what would this cost" for free. And the eligible set is
// narrow on purpose — see scannable.
//
// It reuses Core.Ingest rather than writing its own path, so the choke-point,
// the validator and the linker are the same ones everything else goes through.
// The only new logic here is which files, and whether to skip.
func (c *Core) Scan(ctx context.Context, projectPath string, hashes HashStore, opts ScanOptions) (ScanResult, error) {
	var res ScanResult
	res.DryRun = opts.DryRun

	if !c.Available() {
		return res, ErrNoStore
	}
	if projectPath == "" {
		return res, errNoProject
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = c.cfg.BrainScanBatch
	}

	files, err := listScannable(ctx, projectPath)
	if err != nil {
		return res, err
	}

	known := map[string]string{}
	if hashes != nil {
		if k, err := hashes.BrainNodeHashes(ctx, projectPath, KindFile, c.cfg.BrainPromptVersion); err == nil {
			known = k
		}
	}

	// Every eligible file is read and hashed, not just the batch. Hashing a
	// few megabytes takes milliseconds, and it is what makes "three hundred
	// left" an exact number rather than an estimate that quietly ignores the
	// files that changed since the last pass.
	type candidate struct {
		rel     string
		content string
		hash    string
	}
	var pending []candidate

	for _, rel := range files {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		full := filepath.Join(projectPath, rel)

		info, err := os.Stat(full)
		if err != nil || info.IsDir() || info.Size() == 0 {
			continue
		}
		if info.Size() > int64(c.cfg.BrainScanMaxFileBytes) {
			continue
		}

		raw, err := os.ReadFile(full)
		if err != nil || !isText(raw) {
			continue
		}
		res.Eligible++

		sum := sha256.Sum256(raw)
		hash := hex.EncodeToString(sum[:])
		if known[rel] == hash {
			res.Skipped++
			continue
		}
		pending = append(pending, candidate{rel: rel, content: string(raw), hash: hash})
	}

	batch := pending
	if len(batch) > limit {
		batch = batch[:limit]
	}
	res.Remaining = len(pending) - len(batch)

	if opts.DryRun {
		res.Remaining = len(pending)
		for _, cand := range pending {
			res.Files = append(res.Files, cand.rel)
		}
		return res, nil
	}

	var (
		mu sync.Mutex
		g  errgroup.Group
	)
	g.SetLimit(c.cfg.BrainScanConcurrency)

	for _, cand := range batch {
		g.Go(func() error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			_, err := c.Ingest(ctx, Input{
				Source:      cand.rel,
				Kind:        KindFile,
				Content:     "File: " + cand.rel + "\n\n" + cand.content,
				ProjectPath: projectPath,
				ContentHash: cand.hash,
			})

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				// One unreadable file is not a reason to abandon a scan that
				// has already paid for the others.
				res.Failed++
				return nil
			}
			res.Scanned++
			res.Files = append(res.Files, cand.rel)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return res, err
	}

	sort.Strings(res.Files)
	return res, nil
}

// listScannable returns the repository-relative paths worth reading.
//
// `git ls-files` is the allowlist, not a convenience: it already excludes
// build output, vendored trees and everything .gitignore names, which is the
// same judgement a person made about what belongs to the project. A directory
// that is not a repository falls back to a filtered walk.
func listScannable(ctx context.Context, projectPath string) ([]string, error) {
	var candidates []string

	out, err := exec.CommandContext(ctx, "git", "-C", projectPath, "ls-files", "-z").Output()
	if err == nil {
		for _, p := range strings.Split(string(out), "\x00") {
			if p != "" {
				candidates = append(candidates, p)
			}
		}
	} else {
		walkErr := filepath.WalkDir(projectPath, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if skipDir(d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			rel, relErr := filepath.Rel(projectPath, p)
			if relErr == nil {
				candidates = append(candidates, rel)
			}
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}

	kept := candidates[:0]
	for _, rel := range candidates {
		if scannable(rel) {
			kept = append(kept, rel)
		}
	}
	sort.Strings(kept)
	return kept, nil
}

func skipDir(name string) bool {
	switch name {
	case ".git", "node_modules", "target", "dist", "build", "vendor", ".venv":
		return true
	}
	return false
}

// scannable decides whether a path is worth a model call.
//
// The exclusions are not about size. A lock file, a golden fixture and a
// minified bundle are all perfectly readable and all worth nothing to a later
// session: they are generated, or they exist to be compared byte-for-byte, and
// a distilled paragraph about one is a paragraph nobody will ever search for.
// Every one of them costs the same as a file that does matter.
func scannable(rel string) bool {
	base := path.Base(rel)
	ext := strings.ToLower(path.Ext(rel))

	for _, seg := range strings.Split(rel, "/") {
		if skipDir(seg) || seg == "testdata" || seg == "__snapshots__" {
			return false
		}
	}

	switch base {
	case "go.sum", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "Cargo.lock",
		"bun.lockb", "composer.lock", ".DS_Store":
		return false
	}

	switch ext {
	case ".lock", ".golden", ".snap",
		".png", ".jpg", ".jpeg", ".gif", ".webp", ".ico", ".icns", ".svg",
		".woff", ".woff2", ".ttf", ".otf", ".eot",
		".pdf", ".zip", ".gz", ".tar", ".bin", ".wasm", ".db", ".sqlite":
		return false
	}
	if strings.HasSuffix(base, ".min.js") || strings.HasSuffix(base, ".min.css") {
		return false
	}
	return true
}

// isText rejects binaries the extension list did not name. A NUL byte or
// invalid UTF-8 is the cheap, reliable signal; sniffing content types is not
// worth the dependency for a decision this coarse.
func isText(raw []byte) bool {
	if bytes.IndexByte(raw, 0) >= 0 {
		return false
	}
	head := raw
	if len(head) > 8192 {
		head = head[:8192]
	}
	return utf8.Valid(head)
}

// errNoProject keeps the "no default project" rule audible here too: a scan
// with no directory named is not a scan of everything, it is a mistake.
var errNoProject = errNoProjectType{}

type errNoProjectType struct{}

func (errNoProjectType) Error() string {
	return "brain: scan needs a project path; there is no default project"
}

// compile-time assertion that the store satisfies the narrow interface, so a
// signature change is caught here rather than in cmd/.
var _ HashStore = (*store.Store)(nil)
