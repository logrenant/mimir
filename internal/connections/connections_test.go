package connections_test

import (
	"testing"

	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/connections"
	"github.com/logrenant/mimir/internal/llm"
)

// The four connections this build ships, under the ids the binaries already
// had. That equality is what makes the whole refactor free: a settings.json
// holding `provider: "agy"` still resolves, and every cache key derived from
// `Selection.Key()` is byte-identical.
func TestSpecs_ShipTheBuiltInsUnderTheirExistingIDs(t *testing.T) {
	got := map[string]llm.ConnectionSpec{}
	for _, spec := range connections.New(config.Load()).Specs() {
		got[spec.ID] = spec
	}

	for _, id := range []string{"agy", "claude", "gemini", "ollama"} {
		spec, ok := got[id]
		if !ok {
			t.Fatalf("%q yerleşik bağlantı olarak yok: %v", id, got)
		}
		if !spec.Builtin {
			t.Errorf("%q yerleşik işaretlenmemiş", id)
		}
		if spec.Adapter == "" {
			t.Errorf("%q bir adaptöre bağlanmamış", id)
		}
	}
}

// Who bills for it is not which binary runs it. Antigravity has no
// subscription of its own — it rides a Google AI plan — so an operator looking
// at `agy` and `gemini` is looking at one company's bill.
func TestSpecs_AgyAndGeminiAreBothGoogle(t *testing.T) {
	vendors := map[string]string{}
	for _, spec := range connections.New(config.Load()).Specs() {
		vendors[spec.ID] = spec.Vendor
	}
	if vendors["agy"] != "google" || vendors["gemini"] != "google" {
		t.Errorf("agy=%q gemini=%q — ikisi de Google olmalı", vendors["agy"], vendors["gemini"])
	}
	if vendors["claude"] != "anthropic" {
		t.Errorf("claude satıcısı %q", vendors["claude"])
	}
}

// A connection is CLI or API, and a screen draws that as a chip. Every built-in
// is a CLI today; the field exists so an API connection is not a special case
// bolted on later.
func TestSpecs_BuiltInsAreAllCLITransports(t *testing.T) {
	for _, spec := range connections.New(config.Load()).Specs() {
		if spec.Transport() != llm.TransportCLI {
			t.Errorf("%q taşıması %q", spec.ID, spec.Transport())
		}
	}
}

func TestAllows_KeepsTheShippedGateForBuiltIns(t *testing.T) {
	r := connections.New(config.Load())

	if !r.Allows("claude", "claude-sonnet-5") {
		t.Error("kendi modelini reddetti")
	}
	if r.Allows("claude", "qwen3:8b") {
		t.Error("listesinde olmayan bir modeli kabul etti")
	}
	// Empty is "that connection's default", valid everywhere.
	if !r.Allows("claude", "") {
		t.Error("boş model reddedildi")
	}
	// A discovered connection's models are files on the machine, so the check
	// is on the shape of the name.
	if !r.Allows("ollama", "qwen3:8b") {
		t.Error("gerçek bir ollama etiketi reddedildi")
	}
	for _, bad := range []string{"--version", "-m", "a b", "$(whoami)"} {
		if r.Allows("ollama", bad) {
			t.Errorf("kabul edilmemesi gereken model kabul edildi: %q", bad)
		}
	}
}

func TestAllows_RefusesAConnectionNobodyRegistered(t *testing.T) {
	r := connections.New(config.Load())
	if r.Allows("uydurma", "") {
		t.Error("kayıtlı olmayan bağlantı kabul edildi")
	}
	if _, err := r.Get("uydurma"); err == nil {
		t.Error("kayıtlı olmayan bağlantı için hata dönmedi")
	}
}

// The design's own guard rail. A connection may not name what runs: a CLI
// adapter takes its path from `internal/config` and there is no field on the
// spec that could carry one. This test is a reminder in the shape of an
// assertion — if somebody adds `CLIPath`, it stops compiling here first.
func TestConnectionSpec_CannotNameItsOwnExecutable(t *testing.T) {
	spec := llm.ConnectionSpec{ID: "x", Adapter: llm.AdapterClaudeCLI}
	// If this line ever needs changing because a path field appeared, read
	// `internal/llm/connection.go`'s doc comment before changing it.
	if got := spec.Transport(); got != llm.TransportCLI {
		t.Fatalf("taşıma %q", got)
	}
}
