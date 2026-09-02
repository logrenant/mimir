package brain

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

// ErrNoPDFExtractor means poppler's pdftotext is not installed. It is not a
// failure of anything: a machine without poppler simply has no PDFs in Brain,
// and every other file still gets read.
var ErrNoPDFExtractor = errors.New("brain: pdftotext is not installed")

// ErrNoTextLayer means the PDF is a scan — pages of images with no characters
// on them. Deliberately a different error from the one above: one is fixed by
// `brew install poppler` and the other cannot be fixed at all, and reporting
// them as one thing sends an operator to install what they already have.
var ErrNoTextLayer = errors.New("brain: pdf has no text layer")

func isPDF(rel string) bool { return strings.EqualFold(path.Ext(rel), ".pdf") }

// pdfText extracts a PDF's text through poppler.
//
// A PDF is the reason ~/Documents is worth scanning: on this machine it is six
// technical manuals and nothing else, and a manual nobody can search is a
// manual nobody reads. The extractor is optional by construction — absent, the
// file is skipped and the scan carries on — which puts it in the same category
// as the Maps sidecar rather than the store.
//
// The subprocess posture matters more here than anywhere else in this package:
// the input is an arbitrary file from the operator's disk and the reader is a
// C++ parser. It gets a timeout, an output ceiling, an empty environment and a
// working directory that is not a repository, and a crash is a skipped file
// rather than a failed scan.
func (c *Core) pdfText(ctx context.Context, full string) (string, error) {
	bin, err := c.pdftotext()
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, c.cfg.BrainScanPDFTimeout)
	defer cancel()

	// -q          keeps poppler's own chatter off stderr
	// -enc UTF-8  is deterministic; the manuals here are Turkish
	// -nopgbrk    drops form feeds, which is what turns "no text layer" into an
	//             empty string rather than a page of \f
	// -l          bounds the work by pages, whatever the file size
	// --          the path comes from a directory walk; a file called -f is not
	//             a flag
	// -           stdout, so nothing is written to disk
	args := []string{
		"-q", "-enc", "UTF-8", "-nopgbrk",
		"-l", fmt.Sprint(c.cfg.BrainScanPDFPages),
		"--", full, "-",
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = []string{}
	cmd.Dir = c.scratchDir()
	cmd.Stdin = nil

	var stderr strings.Builder
	cmd.Stderr = &limitedWriter{w: &stderr, left: 2 << 10}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("brain: starting pdftotext: %w", err)
	}

	// One byte past the ceiling is how "it was cut" is detected. The pipe is
	// always drained: leaving it full deadlocks the child, which then has to be
	// killed by the timeout instead of finishing.
	raw, readErr := io.ReadAll(io.LimitReader(stdout, int64(c.cfg.BrainScanPDFMaxChars)+1))
	truncated := len(raw) > c.cfg.BrainScanPDFMaxChars
	if truncated {
		raw = raw[:c.cfg.BrainScanPDFMaxChars]
		// A file this large is not going to be read to the end anyway — the
		// distil sees BrainBodyMaxChars of it — so the subprocess is stopped
		// rather than waited out.
		cancel()
	}
	waitErr := cmd.Wait()

	if !truncated {
		if readErr != nil {
			return "", fmt.Errorf("brain: reading pdftotext output: %w", readErr)
		}
		if waitErr != nil {
			return "", fmt.Errorf("brain: pdftotext failed on %s: %w (%s)",
				filepath.Base(full), waitErr, strings.TrimSpace(stderr.String()))
		}
	}

	text := strings.TrimSpace(string(raw))
	if len(text) < c.cfg.BrainScanPDFMinChars {
		return "", ErrNoTextLayer
	}
	return text, nil
}

// pdftotext resolves the binary once per process. A machine without poppler
// pays one failed lookup for a whole sweep rather than one per PDF, and says so
// once rather than once per file.
func (c *Core) pdftotext() (string, error) {
	c.pdfOnce.Do(func() {
		bin, err := exec.LookPath(c.cfg.BrainScanPDFPath)
		if err != nil {
			if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
				c.pdfErr = fmt.Errorf("%w: PDFs are skipped (`brew install poppler` to index them)", ErrNoPDFExtractor)
			} else {
				c.pdfErr = fmt.Errorf("%w: %v", ErrNoPDFExtractor, err)
			}
			slog.Info("pdftotext is not available", "path", c.cfg.BrainScanPDFPath, "detail", c.pdfErr.Error())
			return
		}
		c.pdfPath = bin
	})
	return c.pdfPath, c.pdfErr
}

// scratchDir is the working directory a subprocess of this package gets: an
// empty directory beside the store, never a repository. internal/llm makes the
// same choice for the same reason — a tool started inside a checkout reads that
// checkout's own instructions.
func (c *Core) scratchDir() string {
	if c.cfg.StorePath == "" {
		return os.TempDir()
	}
	dir := filepath.Join(filepath.Dir(c.cfg.StorePath), "scratch")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return os.TempDir()
	}
	return dir
}

// limitedWriter keeps a subprocess's stderr from becoming a memory problem. It
// is diagnostic text that ends up in one error message, not a stream.
type limitedWriter struct {
	w    io.Writer
	left int
	mu   sync.Mutex
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(p)
	if l.left <= 0 {
		return n, nil
	}
	if len(p) > l.left {
		p = p[:l.left]
	}
	l.left -= len(p)
	if _, err := l.w.Write(p); err != nil {
		return n, err
	}
	return n, nil
}
