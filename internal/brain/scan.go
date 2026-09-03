package brain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
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
	Scanned int `json:"scanned"`
	Skipped int `json:"skipped_unchanged"`
	Failed  int `json:"failed"`

	// Changed counts the files this pass re-read because their content moved,
	// as opposed to files it had never seen. Both are Scanned, and a console
	// that cannot tell them apart can never say the detection is working —
	// which is the only visible sign that editing a file re-teaches Brain.
	Changed int `json:"changed,omitempty"`

	// ChangedFiles names what Changed counted, like FailedFiles does.
	ChangedFiles []string `json:"changed_files,omitempty"`

	// Unreadable is a file the extractor could not turn into text — a PDF that
	// is page images, or a machine with no poppler. It is deliberately not
	// Failed: Failed is what tells the supervisor the provider is down, and a
	// scanned manual must not look like agy being signed out.
	Unreadable int  `json:"unreadable,omitempty"`
	Remaining  int  `json:"remaining"`
	Eligible   int  `json:"eligible_total"`
	DryRun     bool `json:"dry_run,omitempty"`

	Files []string `json:"files,omitempty"`

	// FailedFiles names what Failed counted. A console that can only say "three
	// files failed" sends its reader to a log file; one that names them is the
	// difference between a number and a thing to go and look at.
	FailedFiles []string `json:"failed_files,omitempty"`
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
		size    int64
		modTime time.Time
		// changed is true when this file had a stored hash and it moved. A
		// file seen for the first time is not a change.
		changed bool
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

		hash, ok := digest(full, info, c.cfg.BrainScanMaxFileBytes, c.cfg.BrainScanMaxPDFBytes)
		if !ok {
			continue
		}
		res.Eligible++

		// The digest is of the file's bytes, never of the text extracted from
		// them, and it is taken before the extractor runs. That is what makes a
		// second sweep over ~/Documents spawn neither pdftotext nor agy.
		previous, seen := known[rel]
		if seen && previous == hash {
			res.Skipped++
			continue
		}

		content, err := c.contentOf(ctx, full, rel)
		if err != nil {
			res.Unreadable++
			continue
		}
		pending = append(pending, candidate{
			rel:     rel,
			content: content,
			hash:    hash,
			size:    info.Size(),
			modTime: info.ModTime(),
			changed: seen,
		})
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
			out, err := c.Ingest(ctx, Input{
				Source:      cand.rel,
				Kind:        KindFile,
				Content:     "File: " + cand.rel + "\n\n" + cand.content,
				ProjectPath: projectPath,
				ContentHash: cand.hash,
				SizeBytes:   cand.size,
				ModifiedAt:  cand.modTime,
			})

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				// One unreadable file is not a reason to abandon a scan that
				// has already paid for the others.
				res.Failed++
				res.FailedFiles = append(res.FailedFiles, cand.rel)
				return nil
			}
			// A stored node with no assessment is not a scanned file. Ingest
			// keeps it — a titled node is still findable — but it drops the
			// content hash, so the next pass offers the file again, and
			// counting it here is what stops a run reporting a clean sweep
			// while the provider was down for half of it.
			if !out.Distilled {
				res.Failed++
				res.FailedFiles = append(res.FailedFiles, cand.rel)
				return nil
			}
			res.Scanned++
			res.Files = append(res.Files, cand.rel)
			if cand.changed {
				res.Changed++
				res.ChangedFiles = append(res.ChangedFiles, cand.rel)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return res, err
	}

	sort.Strings(res.Files)
	sort.Strings(res.FailedFiles)
	sort.Strings(res.ChangedFiles)
	return res, nil
}

// listScannable returns the repository-relative paths worth reading.
//
// `git ls-files` is the allowlist, not a convenience: with --exclude-standard
// it already excludes build output, vendored trees and everything .gitignore
// names, which is the same judgement a person made about what belongs to the
// project. `--others` is there because a file being uncommitted says nothing
// about whether it belongs to the work — a scan that saw only the index would
// miss a whole afternoon's files, and on this machine it missed 69 of
// CozyFarm's 87. A directory that is not a repository falls back to a filtered
// walk.
func listScannable(ctx context.Context, projectPath string) ([]string, error) {
	var candidates []string

	out, err := exec.CommandContext(ctx, "git", "-C", projectPath,
		"ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
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
				// A repository nested under a non-repository directory is its
				// own project, with its own .gitignore and its own scan. Walking
				// into it here would read those files a second time, under a
				// project path they do not belong to.
				if p != projectPath && isRepo(p) {
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
	case ".git", "node_modules", "target", "dist", "build", ".build", "vendor", ".venv":
		return true
	}
	return false
}

// isRepo reports whether a directory is the root of a git checkout. `.git` is a
// directory in a normal clone and a file in a worktree or submodule, so the
// test is existence, not type.
func isRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
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

	// .pdf is not on this list, and that is the one deliberate inclusion: a
	// technical manual is the opposite of a generated file — it is the thing a
	// later session most needs told about, and on this machine ~/Documents is
	// six of them and nothing else. It is read through poppler (pdf.go), and
	// skipped without complaint when poppler is not installed.
	switch ext {
	case ".lock", ".golden", ".snap",
		".png", ".jpg", ".jpeg", ".gif", ".webp", ".ico", ".icns", ".svg",
		".woff", ".woff2", ".ttf", ".otf", ".eot",
		".zip", ".gz", ".tar", ".bin", ".wasm", ".db", ".sqlite":
		return false
	}
	if strings.HasSuffix(base, ".min.js") || strings.HasSuffix(base, ".min.css") {
		return false
	}
	return true
}

// digest hashes a file and decides, by class, whether it is worth reading at
// all. The two size ceilings are separate numbers on purpose: a 7.6 MB manual
// extracts to 73 KB of text, so one ceiling would either exclude every real PDF
// or let a 96 KB rule wave through a file poppler then has to parse.
//
// A non-PDF is read into memory because it has to be anyway; a PDF is streamed
// through the hash and handed to the extractor by path.
func digest(full string, info os.FileInfo, maxText, maxPDF int) (string, bool) {
	if isPDF(full) {
		if info.Size() > int64(maxPDF) {
			return "", false
		}
		f, err := os.Open(full)
		if err != nil {
			return "", false
		}
		defer func() { _ = f.Close() }()

		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return "", false
		}
		return hex.EncodeToString(h.Sum(nil)), true
	}

	if info.Size() > int64(maxText) {
		return "", false
	}
	raw, err := os.ReadFile(full)
	if err != nil || !isText(raw) {
		return "", false
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), true
}

// contentOf returns the text a file contributes to Brain. A PDF is text behind
// an extractor; everything else is already text by the time digest accepted it.
func (c *Core) contentOf(ctx context.Context, full, rel string) (string, error) {
	if isPDF(rel) {
		return c.pdfText(ctx, full)
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	return string(raw), nil
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
