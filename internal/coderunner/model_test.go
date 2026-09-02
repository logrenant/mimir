package coderunner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// writeRecordingClaude is writeFakeClaude that also appends its own argv to
// logPath, one invocation per line. The model a run actually spawned with is
// not observable from the store — the result event overwrites the column with
// whatever the CLI reports — so the argv is the only honest witness.
func writeRecordingClaude(t *testing.T, logPath string, exitCode int, lines ...string) string {
	t.Helper()

	var script strings.Builder
	script.WriteString("#!/bin/sh\n")
	script.WriteString("echo \"$*\" >> '" + logPath + "'\n")
	script.WriteString("cat >/dev/null\n")
	for _, l := range lines {
		script.WriteString("echo '" + strings.ReplaceAll(l, "'", `'\''`) + "'\n")
	}
	script.WriteString("exit " + strconv.Itoa(exitCode) + "\n")

	path := filepath.Join(t.TempDir(), "recording-claude.sh")
	if err := os.WriteFile(path, []byte(script.String()), 0o755); err != nil {
		t.Fatalf("writing recording claude: %v", err)
	}
	return path
}

func readArgvLog(t *testing.T, logPath string) []string {
	t.Helper()
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading argv log: %v", err)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func TestStartUsesDefaultModelWhenNoneRequested(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "argv.log")
	h := newHarness(t, writeRecordingClaude(t, logPath, 0, lineInit, lineResult))

	if _, err := h.runner.Start(context.Background(), CreateRequest{
		ProjectID: h.projectID,
		Prompt:    "no model named",
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	h.runner.Wait()

	argv := readArgvLog(t, logPath)
	if len(argv) != 1 {
		t.Fatalf("expected one invocation, got %d: %v", len(argv), argv)
	}
	want := "--model " + h.cfg.CodingModel
	if !strings.Contains(argv[0], want) {
		t.Fatalf("argv %q does not carry %q", argv[0], want)
	}
}

func TestStartUsesRequestedModel(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "argv.log")
	h := newHarness(t, writeRecordingClaude(t, logPath, 0, lineInit, lineResult))

	// Any offered model that is not the default, so the assertion cannot pass
	// by accident.
	var picked string
	for _, m := range h.cfg.CodingModels {
		if m.ID != h.cfg.CodingModel {
			picked = m.ID
			break
		}
	}
	if picked == "" {
		t.Fatal("config offers no model other than the default")
	}

	run, err := h.runner.Start(context.Background(), CreateRequest{
		ProjectID: h.projectID,
		Prompt:    "pick a model",
		Model:     picked,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if run.Model != picked {
		t.Fatalf("run reports model %q, want %q", run.Model, picked)
	}
	h.runner.Wait()

	argv := readArgvLog(t, logPath)
	if len(argv) != 1 {
		t.Fatalf("expected one invocation, got %d: %v", len(argv), argv)
	}
	if !strings.Contains(argv[0], "--model "+picked) {
		t.Fatalf("argv %q does not carry --model %s", argv[0], picked)
	}
	if strings.Contains(argv[0], "--model "+h.cfg.CodingModel) {
		t.Fatalf("argv %q still carries the default model", argv[0])
	}
}

// A backlog card keeps the model it was written with: the point of pinning is
// that a card released next week runs as what the operator chose today.
func TestBacklogCardKeepsItsModelUntilEnqueued(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "argv.log")
	h := newHarness(t, writeRecordingClaude(t, logPath, 0, lineInit, lineResult))

	var picked string
	for _, m := range h.cfg.CodingModels {
		if m.ID != h.cfg.CodingModel {
			picked = m.ID
			break
		}
	}

	run, err := h.runner.Create(context.Background(), CreateRequest{
		ProjectID: h.projectID,
		Prompt:    "later",
		Model:     picked,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if run.Model != picked {
		t.Fatalf("created run reports model %q, want %q", run.Model, picked)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatal("Create spawned the CLI")
	}

	if _, err := h.runner.Enqueue(context.Background(), run.ID); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	h.runner.Wait()

	argv := readArgvLog(t, logPath)
	if len(argv) != 1 {
		t.Fatalf("expected one invocation, got %d: %v", len(argv), argv)
	}
	if !strings.Contains(argv[0], "--model "+picked) {
		t.Fatalf("argv %q does not carry --model %s", argv[0], picked)
	}
}

func TestUnknownModelIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "argv.log")
	h := newHarness(t, writeRecordingClaude(t, logPath, 0, lineInit, lineResult))

	_, err := h.runner.Create(context.Background(), CreateRequest{
		ProjectID: h.projectID,
		Prompt:    "bad model",
		Model:     "gpt-9-turbo",
	})
	if !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("Create error = %v, want ErrUnknownModel", err)
	}

	runs, err := h.runner.List(context.Background(), h.projectID, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("a refused task left %d rows behind", len(runs))
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatal("a refused task still spawned the CLI")
	}
}
