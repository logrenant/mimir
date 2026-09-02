package coderunner

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pngBytes builds a real one-pixel PNG, because the type check reads magic
// numbers rather than believing a filename.
func pngBytes(t *testing.T) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encoding png: %v", err)
	}
	return buf.Bytes()
}

func TestSaveAttachment_RoundTripsAnImage(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineResult))
	data := pngBytes(t)

	att, err := h.runner.SaveAttachment("screenshot.png", data)
	if err != nil {
		t.Fatalf("SaveAttachment: %v", err)
	}
	if att.MIME != "image/png" || att.Bytes != len(data) {
		t.Errorf("unexpected attachment: %+v", att)
	}

	got, raw, err := h.runner.LoadAttachment(att.ID)
	if err != nil {
		t.Fatalf("LoadAttachment: %v", err)
	}
	if !bytes.Equal(raw, data) {
		t.Error("the bytes came back different")
	}
	if got.Filename != "screenshot.png" {
		t.Errorf("Filename: got %q", got.Filename)
	}
}

// The extension is the daemon's decision, not the client's: it ends up in a
// path, and a filename is the client's to choose.
func TestSaveAttachment_NamesTheFileFromTheSniffedType(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineResult))

	att, err := h.runner.SaveAttachment("../../evil.sh", pngBytes(t))
	if err != nil {
		t.Fatalf("SaveAttachment: %v", err)
	}
	if att.Ext != "png" {
		t.Errorf("Ext: got %q, want png", att.Ext)
	}
	if strings.ContainsAny(att.Filename, "/\\") {
		t.Errorf("Filename still looks like a path: %q", att.Filename)
	}

	stored := filepath.Join(h.cfg.AttachmentDir, att.ID+".png")
	if _, err := os.Stat(stored); err != nil {
		t.Errorf("the file was not written where its id says: %v", err)
	}
}

func TestSaveAttachment_RefusesSomethingThatIsNotAnImage(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineResult))

	_, err := h.runner.SaveAttachment("notes.txt", []byte("#!/bin/sh\nrm -rf /\n"))
	if !errors.Is(err, ErrAttachmentType) {
		t.Fatalf("want ErrAttachmentType, got %v", err)
	}
}

func TestLoadAttachment_RefusesAnIDThatIsAPath(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineResult))

	if _, _, err := h.runner.LoadAttachment("../../etc/passwd"); !errors.Is(err, ErrAttachmentNotFound) {
		t.Fatalf("want ErrAttachmentNotFound, got %v", err)
	}
}

// An upload happens before the task that will own it exists, so the sweep must
// never delete into that window.
func TestSweepAttachments_KeepsReferencedAndRecentFiles(t *testing.T) {
	h := newHarness(t, writeFakeClaude(t, 0, lineResult))

	owned, err := h.runner.SaveAttachment("owned.png", pngBytes(t))
	if err != nil {
		t.Fatalf("SaveAttachment: %v", err)
	}
	fresh, err := h.runner.SaveAttachment("fresh.png", pngBytes(t))
	if err != nil {
		t.Fatalf("SaveAttachment: %v", err)
	}
	orphan, err := h.runner.SaveAttachment("orphan.png", pngBytes(t))
	if err != nil {
		t.Fatalf("SaveAttachment: %v", err)
	}

	referenced, err := encodeAttachmentIDs([]string{owned.ID})
	if err != nil {
		t.Fatalf("encodeAttachmentIDs: %v", err)
	}

	// Nothing is swept while it is still young, referenced or not: that window
	// is exactly where a composer holding an upload lives.
	if removed, err := h.runner.SweepAttachments([]string{referenced}, time.Now()); err != nil {
		t.Fatalf("SweepAttachments: %v", err)
	} else if removed != 0 {
		t.Fatalf("swept %d fresh uploads", removed)
	}
	for _, id := range []string{owned.ID, fresh.ID, orphan.ID} {
		if _, _, err := h.runner.LoadAttachment(id); err != nil {
			t.Errorf("a fresh upload was swept: %v", err)
		}
	}

	// A day on, what no run points at is reclaimed and what one does is not.
	if _, err := h.runner.SweepAttachments([]string{referenced}, time.Now().Add(2*attachmentSweepAge)); err != nil {
		t.Fatalf("SweepAttachments: %v", err)
	}
	if _, _, err := h.runner.LoadAttachment(owned.ID); err != nil {
		t.Errorf("a referenced attachment was swept: %v", err)
	}
	for _, id := range []string{fresh.ID, orphan.ID} {
		if _, _, err := h.runner.LoadAttachment(id); !errors.Is(err, ErrAttachmentNotFound) {
			t.Errorf("an unreferenced attachment survived: %v", err)
		}
	}
}

// The whole point of an attachment is that the run can read it, which means the
// paths reach the prompt and the directory reaches --add-dir.
func TestRun_PassesAttachmentPathsToTheCLI(t *testing.T) {
	seen := filepath.Join(t.TempDir(), "stdin.txt")
	args := filepath.Join(t.TempDir(), "args.txt")
	h := newHarness(t, writeScriptedClaude(t,
		"cat > "+seen+"\necho \"$@\" > "+args+"\necho '"+lineResult+"'\n"))

	att, err := h.runner.SaveAttachment("shot.png", pngBytes(t))
	if err != nil {
		t.Fatalf("SaveAttachment: %v", err)
	}

	if _, err := h.runner.Start(context.Background(), CreateRequest{
		ProjectID:     h.projectID,
		Prompt:        "what is wrong here?",
		AttachmentIDs: []string{att.ID},
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	h.runner.Wait()

	prompt, err := os.ReadFile(seen)
	if err != nil {
		t.Fatalf("reading the prompt the CLI got: %v", err)
	}
	wantPath := filepath.Join(h.cfg.AttachmentDir, att.ID+".png")
	if !strings.Contains(string(prompt), wantPath) {
		t.Errorf("the prompt did not carry the attachment path:\n%s", prompt)
	}

	argv, err := os.ReadFile(args)
	if err != nil {
		t.Fatalf("reading argv: %v", err)
	}
	if !strings.Contains(string(argv), h.cfg.AttachmentDir) {
		t.Errorf("--add-dir did not include the attachment directory:\n%s", argv)
	}
}
