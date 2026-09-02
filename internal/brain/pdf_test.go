package brain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/logrenant/mimir/internal/config"
)

// fakePDFToText writes a stand-in for poppler, so `make check` does not require
// it to be installed. body is shell, run with the real argv in "$@".
func fakePDFToText(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-pdftotext.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// writePDF drops a file with a .pdf name. Nothing here parses it — the
// extractor is always a fake in tests — but the bytes have to be stable so the
// content hash is.
func writePDF(t *testing.T, dir, name string) string {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, []byte("%PDF-1.4\nnot really a pdf, but stable\n%%EOF\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return full
}

func pdfCore(t *testing.T, bin string) *Core {
	t.Helper()
	cfg := config.Load()
	cfg.StorePath = filepath.Join(t.TempDir(), "mimir.db")
	cfg.BrainScanPDFPath = bin
	return New(cfg, newFakeStore(), nil)
}

func TestPDFText_ExtractsTheDocument(t *testing.T) {
	dir := t.TempDir()
	full := writePDF(t, dir, "manual.pdf")
	c := pdfCore(t, fakePDFToText(t, `echo "Bolted connection torque values for the frame"`))

	got, err := c.pdfText(context.Background(), full)
	if err != nil {
		t.Fatalf("pdfText: %v", err)
	}
	if !strings.Contains(got, "torque") {
		t.Errorf("text = %q", got)
	}
}

// The two absences are different problems with different fixes, and a scan that
// reported them as one thing would send an operator to install what they have.
func TestPDFText_MissingBinaryIsDistinctFromNoTextLayer(t *testing.T) {
	dir := t.TempDir()
	full := writePDF(t, dir, "scan.pdf")

	// A scanned manual: poppler succeeds and produces nothing. This is the real
	// measured shape — 8 MB of page images, 47 bytes of form feeds, which
	// -nopgbrk removes entirely.
	c := pdfCore(t, fakePDFToText(t, `exit 0`))
	if _, err := c.pdfText(context.Background(), full); !errors.Is(err, ErrNoTextLayer) {
		t.Fatalf("err = %v, want ErrNoTextLayer", err)
	}

	missing := pdfCore(t, filepath.Join(t.TempDir(), "no-such-pdftotext"))
	if _, err := missing.pdfText(context.Background(), full); !errors.Is(err, ErrNoPDFExtractor) {
		t.Fatalf("err = %v, want ErrNoPDFExtractor", err)
	}
}

func TestPDFText_CapsTheOutput(t *testing.T) {
	dir := t.TempDir()
	full := writePDF(t, dir, "huge.pdf")

	c := pdfCore(t, fakePDFToText(t, `while :; do printf 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'; done`))
	c.cfg.BrainScanPDFMaxChars = 4 << 10

	got, err := c.pdfText(context.Background(), full)
	if err != nil {
		t.Fatalf("pdfText: %v", err)
	}
	if len(got) > c.cfg.BrainScanPDFMaxChars {
		t.Errorf("output ran past the ceiling: %d bytes", len(got))
	}
}

func TestPDFText_ReportsAFailingExtractor(t *testing.T) {
	dir := t.TempDir()
	full := writePDF(t, dir, "damaged.pdf")

	c := pdfCore(t, fakePDFToText(t, `echo "Syntax Error: Couldn't find trailer dictionary" >&2
exit 1`))
	_, err := c.pdfText(context.Background(), full)
	if err == nil {
		t.Fatal("a damaged PDF must be an error, not empty text")
	}
	if errors.Is(err, ErrNoTextLayer) || errors.Is(err, ErrNoPDFExtractor) {
		t.Errorf("a parse failure was reported as one of the two absences: %v", err)
	}
}

// --- the scan's side of it ---------------------------------------------------

// A PDF that cannot be read is not a failed distil. Failed is what tells the
// supervisor its provider is down; a manual made of page images must not look
// like agy being signed out.
func TestScan_UnreadablePDFIsNotAFailure(t *testing.T) {
	dir := scanRepo(t)
	writePDF(t, dir, "manual.pdf")

	st := newFakeStore()
	cfg := config.Load()
	cfg.StorePath = filepath.Join(t.TempDir(), "mimir.db")
	cfg.BrainScanPDFPath = fakePDFToText(t, `exit 0`) // no text layer
	c := New(cfg, st, &fakeLLM{distil: goodDistil, relate: `{"related":[]}`})

	got, err := c.Scan(context.Background(), dir, &fakeHashes{}, ScanOptions{Limit: 50})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if got.Failed != 0 {
		t.Errorf("failed = %d, want 0: an unreadable PDF is not a provider failure", got.Failed)
	}
	if got.Unreadable != 1 {
		t.Errorf("unreadable = %d, want 1: %+v", got.Unreadable, got)
	}
	if got.Scanned != 3 {
		t.Errorf("scanned = %d, want the 3 text files: %v", got.Scanned, got.Files)
	}
}

func TestScan_ExtractsAPDFIntoANode(t *testing.T) {
	dir := scanRepo(t)
	writePDF(t, dir, "manual.pdf")

	st := newFakeStore()
	cfg := config.Load()
	cfg.StorePath = filepath.Join(t.TempDir(), "mimir.db")
	cfg.BrainScanPDFPath = fakePDFToText(t, `echo "Erection sequence for a rigid frame, bay by bay, with torque values"`)
	c := New(cfg, st, &fakeLLM{distil: goodDistil, relate: `{"related":[]}`})

	got, err := c.Scan(context.Background(), dir, &fakeHashes{}, ScanOptions{Limit: 50})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if got.Scanned != 4 {
		t.Fatalf("scanned = %d, want 4 (three text files and the manual): %v", got.Scanned, got.Files)
	}

	var body string
	for _, n := range st.nodes {
		if strings.HasSuffix(n.SourceKey, "manual.pdf") {
			body = n.Body
		}
	}
	if !strings.Contains(body, "Erection sequence") {
		t.Errorf("the PDF's node does not carry its text: %q", body)
	}
}

// The digest is of the file, so an unchanged PDF costs neither poppler nor a
// model on the next sweep.
func TestScan_UnchangedPDFDoesNotRunTheExtractorAgain(t *testing.T) {
	dir := scanRepo(t)
	writePDF(t, dir, "manual.pdf")

	calls := filepath.Join(t.TempDir(), "calls")
	st := newFakeStore()
	cfg := config.Load()
	cfg.StorePath = filepath.Join(t.TempDir(), "mimir.db")
	cfg.BrainScanPDFPath = fakePDFToText(t, `echo x >> `+calls+`
echo "Erection sequence for a rigid frame, bay by bay, with torque values"`)
	c := New(cfg, st, &fakeLLM{distil: goodDistil, relate: `{"related":[]}`})

	if _, err := c.Scan(context.Background(), dir, &fakeHashes{}, ScanOptions{Limit: 50}); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	known := map[string]string{}
	for _, n := range st.nodes {
		known[n.SourceKey] = n.ContentHash
	}

	before, err := os.ReadFile(calls)
	if err != nil {
		t.Fatalf("the extractor never ran: %v", err)
	}

	second := New(cfg, st, nil) // a model call here would panic
	got, err := second.Scan(context.Background(), dir, &fakeHashes{m: known}, ScanOptions{Limit: 50})
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if got.Scanned != 0 || got.Skipped != 4 {
		t.Fatalf("second scan = %+v, want nothing to do", got)
	}

	after, _ := os.ReadFile(calls)
	if len(after) != len(before) {
		t.Errorf("pdftotext ran again for an unchanged file: %q -> %q", before, after)
	}
}

func TestScan_OversizePDFIsSkipped(t *testing.T) {
	dir := scanRepo(t)
	full := writePDF(t, dir, "huge.pdf")
	if err := os.WriteFile(full, []byte(strings.Repeat("x", 4<<10)), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Load()
	cfg.StorePath = filepath.Join(t.TempDir(), "mimir.db")
	cfg.BrainScanMaxPDFBytes = 1 << 10
	cfg.BrainScanPDFPath = fakePDFToText(t, `echo "should never run"`)
	c := New(cfg, newFakeStore(), nil)

	got, err := c.Scan(context.Background(), dir, &fakeHashes{}, ScanOptions{DryRun: true})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if strings.Contains(strings.Join(got.Files, ","), "huge.pdf") {
		t.Errorf("an oversized PDF was eligible: %v", got.Files)
	}
}
