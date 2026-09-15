package llm

import (
	"context"
	"testing"
	"time"
)

// The bug this pins.
//
// `Providers()` used to walk `byClass` and `fallback` only. With the shipped
// configuration that is `{agy, claude}` — so `Discover` never asked `gemini` or
// `ollama` anything, `GET /llm/providers` carried no availability row for them,
// and the desktop picker's "no data means fine" branch reported two providers
// as installed and working without ever having looked.
//
// A registry that answers about half of itself is worse than one that answers
// about none: the half it omits is silently affirmed.
func TestProviders_WalksEveryRegisteredConnection(t *testing.T) {
	cfg := testConfig(t)
	cfg.ClaudeCLIPath = writeFakeCLI(t, "claude", `echo '{"type":"result","result":"ok"}'`)
	cfg.AgyCLIPath = writeFakeCLI(t, "agy", `echo '{"status":"SUCCESS","response":"ok"}'`)
	cfg.GeminiCLIPath = writeFakeCLI(t, "gemini", `echo 'ok'`)
	cfg.OllamaCLIPath = writeFakeCLI(t, "ollama", `echo 'ok'`)

	got := map[string]bool{}
	for _, p := range NewRouter(cfg).Providers() {
		got[p.Name()] = true
	}

	for _, want := range []string{"agy", "claude", "gemini", "ollama"} {
		if !got[want] {
			t.Errorf("%q kayıtlı ama Providers() onu vermiyor: %v", want, got)
		}
	}
}

// And the consequence, at the surface an operator actually reads.
func TestDiscover_ReportsEveryRegisteredConnection(t *testing.T) {
	cfg := testConfig(t)
	cfg.ClaudeCLIPath = writeFakeCLI(t, "claude", `echo '{"type":"result","result":"ok"}'`)
	cfg.AgyCLIPath = writeFakeCLI(t, "agy", `echo '{"status":"SUCCESS","response":"ok"}'`)
	cfg.GeminiCLIPath = writeFakeCLI(t, "gemini", `echo 'ok'`)
	cfg.OllamaCLIPath = writeFakeCLI(t, "ollama", `echo 'ok'`)

	seen := map[string]bool{}
	for _, a := range Discover(context.Background(), NewRouter(cfg), false, 5*time.Second) {
		seen[a.Provider] = true
	}

	for _, want := range []string{"agy", "claude", "gemini", "ollama"} {
		if !seen[want] {
			t.Errorf("%q hakkında hiçbir şey sorulmamış: %v", want, seen)
		}
	}
}
