package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// stdout is the answer, verbatim.
//
// The first probe of the real CLI merged the streams and showed the answer
// wrapped in spinner and cursor escapes — which would have justified writing an
// escape stripper. Discarding stderr instead showed stdout carrying exactly
// `OK\n\n`. This test pins the conclusion: decoration is stderr's, and the
// answer needs no unpicking.
func TestOllama_ReadsTheAnswerOffCleanStdout(t *testing.T) {
	cfg := testConfig(t)
	cfg.OllamaCLIPath = writeFakeCLI(t, "ollama",
		`printf 'OK\n\n' ; printf 'spinner noise' >&2`)

	got, err := NewOllama(cfg).Complete(context.Background(), Request{User: "soru"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got.Text != "OK" {
		t.Errorf("cevap %q", got.Text)
	}
	if got.Provider != "ollama" {
		t.Errorf("sağlayıcı %q", got.Provider)
	}
}

func TestOllama_RefusesASchema(t *testing.T) {
	cfg := testConfig(t)
	cfg.OllamaCLIPath = writeFakeCLI(t, "ollama", `echo ok`)

	_, err := NewOllama(cfg).Complete(context.Background(),
		Request{User: "x", Schema: json.RawMessage(`{}`)})
	if !errors.Is(err, ErrNoStructuredOutput) {
		t.Fatalf("beklenen ErrNoStructuredOutput, alınan %v", err)
	}
}

// The flags are the contract with a thinking model: `qwen3:8b` is installed on
// this machine and reports `thinking` among its capabilities, so without
// `--hidethinking` the reasoning is part of the answer every caller parses.
func TestOllama_HidesThinkingAndTheTerminalsLineBreaks(t *testing.T) {
	cfg := testConfig(t)
	cfg.OllamaCLIPath = writeFakeCLI(t, "ollama", `echo "$@"`)
	cfg.OllamaModel = "qwen3:8b"

	got, err := NewOllama(cfg).Complete(context.Background(), Request{User: "soru"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	for _, want := range []string{"run", "qwen3:8b", "--hidethinking", "--nowordwrap"} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("%q argümanı gönderilmedi: %q", want, got.Text)
		}
	}
}

// The models are files the operator pulled, so a fixed list in config would be
// a list of somebody else's machine.
func TestOllama_ListsTheModelsThisMachineHas(t *testing.T) {
	cfg := testConfig(t)
	// One `ollama` fake serving both subcommands, the way the real one does.
	cfg.OllamaCLIPath = writeFakeCLI(t, "ollama", `
case "$1" in
  list) printf 'NAME    ID  SIZE  MODIFIED\ngpt-oss:20b  a  13 GB  4 weeks ago\nnomic-embed-text:latest  b  274 MB  4 weeks ago\nqwen3:8b  c  5.2 GB  4 weeks ago\n' ;;
  show)
    case "$2" in
      nomic-embed-text:latest) printf '  Model\n    architecture nomic-bert\n\n  Capabilities\n    embedding\n\n  License\n    completion appears here as prose\n' ;;
      *) printf '  Model\n    architecture qwen3\n\n  Capabilities\n    completion\n    tools\n' ;;
    esac ;;
esac`)

	got, err := NewOllama(cfg).Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("beklenen 2 model, alınan %v", got)
	}
	for _, name := range got {
		if strings.Contains(name, "embed") {
			t.Errorf("gömme modeli listeye girdi: %v", got)
		}
	}
	// The header row is skipped by position, so a model could not be called
	// NAME and be smuggled in — nor could one be lost to a renamed header.
	for _, name := range got {
		if name == "NAME" {
			t.Error("başlık satırı model sayıldı")
		}
	}
}

// The filter reads the Capabilities block, not the whole output: a licence that
// happens to contain the word must not promote an embedding model.
func TestHasCompletionCapability_ReadsTheBlockAndNotTheProse(t *testing.T) {
	embedding := "  Model\n    architecture nomic-bert\n\n  Capabilities\n    embedding\n\n  License\n    completion\n"
	if hasCompletionCapability(embedding) {
		t.Error("lisans metnindeki kelime yeteneğe sayıldı")
	}
	completion := "  Capabilities\n    completion\n    tools\n"
	if !hasCompletionCapability(completion) {
		t.Error("gerçek yetenek okunmadı")
	}
}
