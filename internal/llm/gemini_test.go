package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// The failure this whole capability field exists to prevent.
//
// Brain's distil and relation passes send a schema and parse what comes back.
// The `gemini` CLI has no `--json-schema` flag — measured off the installed
// binary, not remembered — so a schema-carrying call would come back as prose,
// the JSON parse would fail, and the pass would store a node with a title and
// no assessment. Nothing errors and nothing is logged as wrong.
func TestRouter_RefusesASchemaToAProviderThatCannotServeOne(t *testing.T) {
	cfg := testConfig(t)
	cfg.GeminiCLIPath = writeFakeCLI(t, "gemini", `echo 'prose, not JSON'`)
	r := NewRouter(cfg)

	_, err := r.CompleteWith(context.Background(), Distill,
		Selection{Provider: "gemini"},
		Request{User: "x", Schema: json.RawMessage(`{"type":"object"}`)})

	if !errors.Is(err, ErrNoStructuredOutput) {
		t.Fatalf("beklenen ErrNoStructuredOutput, alınan %v", err)
	}
	// Not an availability error: the provider is fine, it simply cannot do
	// this. Wrapping it would send the call down the fallback path, which
	// would end with the work running somewhere the operator did not choose.
	if errors.Is(err, ErrProviderUnavailable) {
		t.Error("yetenek uyuşmazlığı bir erişilebilirlik hatası olarak sarmalandı")
	}
}

// The same refusal from a direct caller that went around the router.
func TestGemini_RefusesASchemaItself(t *testing.T) {
	cfg := testConfig(t)
	cfg.GeminiCLIPath = writeFakeCLI(t, "gemini", `echo 'ok'`)

	_, err := NewGemini(cfg).Complete(context.Background(),
		Request{User: "x", Schema: json.RawMessage(`{}`)})
	if !errors.Is(err, ErrNoStructuredOutput) {
		t.Fatalf("beklenen ErrNoStructuredOutput, alınan %v", err)
	}
}

// A schemaless call is the one this provider is for, and `-o text` means the
// answer is stdout with no envelope to guess at.
func TestGemini_ReadsPlainTextOutput(t *testing.T) {
	cfg := testConfig(t)
	cfg.GeminiCLIPath = writeFakeCLI(t, "gemini", `echo '  bir cevap  '`)

	got, err := NewGemini(cfg).Complete(context.Background(), Request{User: "soru"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got.Text != "bir cevap" {
		t.Errorf("metin %q", got.Text)
	}
	if got.Provider != "gemini" {
		t.Errorf("sağlayıcı %q", got.Provider)
	}
}

// The one envelope this file parses, because it is the one that was actually
// observed — a signed-out CLI on this machine printed exactly this shape, on
// stdout, and the message is the only useful thing on the screen.
func TestGemini_ReportsTheCLIsOwnAuthError(t *testing.T) {
	cfg := testConfig(t)
	cfg.GeminiCLIPath = writeFakeCLI(t, "gemini",
		`echo '{"session_id":"x","error":{"type":"Error","message":"Please set an Auth method","code":41}}'`)

	_, err := NewGemini(cfg).Complete(context.Background(), Request{User: "soru"})
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("beklenen ErrProviderUnavailable, alınan %v", err)
	}
	if !strings.Contains(err.Error(), "Please set an Auth method") {
		t.Errorf("CLI'ın kendi cümlesi kayboldu: %v", err)
	}
	if !strings.Contains(err.Error(), "41") {
		t.Errorf("hata kodu kayboldu: %v", err)
	}
}

// An answer that merely starts with a brace is an answer, not an envelope.
// Reading every `{` as an error would eat a legitimate JSON reply.
func TestGemini_DoesNotMistakeAJSONAnswerForAnError(t *testing.T) {
	cfg := testConfig(t)
	cfg.GeminiCLIPath = writeFakeCLI(t, "gemini", `echo '{"tags":["a"]}'`)

	got, err := NewGemini(cfg).Complete(context.Background(), Request{User: "soru"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got.Text != `{"tags":["a"]}` {
		t.Errorf("metin %q", got.Text)
	}
}

// Content goes over stdin, never argv: a page body runs past ARG_MAX. The
// prompt flag carries an empty string — its own help says the prompt is
// appended to stdin — so the flag only puts the CLI in headless mode.
func TestGemini_SendsContentOverStdin(t *testing.T) {
	cfg := testConfig(t)
	// This fake echoes what it read on stdin rather than discarding it.
	cfg.GeminiCLIPath = writeFakeEchoCLI(t, "gemini")

	got, err := NewGemini(cfg).Complete(context.Background(),
		Request{System: "sistem", User: "kullanıcı"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !strings.Contains(got.Text, "sistem") || !strings.Contains(got.Text, "kullanıcı") {
		t.Errorf("stdin'e yazılmadı: %q", got.Text)
	}
}

// Discovery separates the two questions because they have different prices.
// "Installed" is `--version` and free; "signed in" is a real call.
func TestDiscover_SeparatesInstalledFromSignedIn(t *testing.T) {
	cfg := testConfig(t)
	cfg.GeminiCLIPath = writeFakeCLI(t, "gemini",
		`echo '{"session_id":"x","error":{"type":"Error","message":"Please set an Auth method","code":41}}'`)
	// Every provider gets a fake, not just the one under test. Discover asks
	// all of them, and `config.Load()` points claude and agy at the real
	// binaries — so leaving them alone makes this test spend the operator's
	// quota and take half a minute. AGENTS.md records the last time that
	// happened.
	cfg.ClaudeCLIPath = writeFakeCLI(t, "claude", `echo '{"type":"result","result":"ok"}'`)
	cfg.AgyCLIPath = writeFakeCLI(t, "agy", `echo '{"status":"SUCCESS","response":"ok"}'`)
	// Ollama too, now that Providers() actually walks every registered
	// connection: without a fake this test loads a real local model and takes
	// seven seconds. Fixing the registry is what made the omission visible.
	cfg.OllamaCLIPath = writeFakeCLI(t, "ollama", `echo 'ok'`)
	r := NewRouter(cfg)

	// Unprobed: the binary answers, so it is installed, and nothing has been
	// spent to learn more than that.
	for _, a := range Discover(context.Background(), r, false, 5*time.Second) {
		if a.Provider != "gemini" {
			continue
		}
		if !a.Installed {
			t.Error("kurulu CLI kurulu değil bildirildi")
		}
		if a.Probed || a.SignedIn {
			t.Error("sorulmadan oturum durumu iddia edildi")
		}
		if a.StructuredOutput {
			t.Error("gemini yapısal çıktı verebilir bildirildi")
		}
		if !a.Agentic {
			t.Error("gemini ajan değil bildirildi")
		}
	}

	// Probed: the login is the question, and the CLI's own sentence is the
	// answer.
	for _, a := range Discover(context.Background(), r, true, 5*time.Second) {
		if a.Provider != "gemini" {
			continue
		}
		if !a.Probed {
			t.Error("sonda atılmadı")
		}
		if a.SignedIn {
			t.Error("oturumu kapalı CLI açık bildirildi")
		}
		if !strings.Contains(a.Detail, "Please set an Auth method") {
			t.Errorf("CLI'ın cümlesi taşınmadı: %q", a.Detail)
		}
		// The package's own bookkeeping prefix is not the operator's
		// information.
		if strings.HasPrefix(a.Detail, "llm: provider unavailable") {
			t.Errorf("iç önek operatöre gösterildi: %q", a.Detail)
		}
	}
}

// The operator's standing preference answers the classes the daemon routes on
// its own — which is what "the model is selectable in every task, Brain
// included" actually requires: Brain's passes never carried a selection.
func TestUseDefaults_AnswersTheDaemonsOwnClasses(t *testing.T) {
	cfg := testConfig(t)
	cfg.AgyCLIPath = writeFakeCLI(t, "agy", `echo '{"status":"SUCCESS","response":"agy answered"}'`)
	cfg.GeminiCLIPath = writeFakeCLI(t, "gemini", `echo 'gemini answered'`)
	cfg.ClaudeCLIPath = writeFakeCLI(t, "claude", `echo '{"type":"result","result":"claude answered"}'`)

	r := NewRouter(cfg)
	// Distil routes to agy by class. The operator prefers gemini on this
	// machine, and says so once rather than per call.
	r.UseDefaults(func(c Class) Selection {
		if c == Distill {
			return Selection{Provider: "gemini"}
		}
		return Selection{}
	})

	got, err := r.Complete(context.Background(), Distill, Request{User: "soru"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got.Provider != "gemini" {
		t.Errorf("operatörün varsayılanı uygulanmadı: %q", got.Provider)
	}

	// Reason expressed no preference, so the class routing still decides.
	got, err = r.Complete(context.Background(), Reason, Request{User: "soru"})
	if err != nil {
		t.Fatalf("Complete(Reason): %v", err)
	}
	if got.Provider != "claude" {
		t.Errorf("tercih edilmeyen sınıf yönlendirmesini kaybetti: %q", got.Provider)
	}
}

// A per-run selection is an instruction and outranks the standing preference.
func TestUseDefaults_ARunsOwnSelectionWins(t *testing.T) {
	cfg := testConfig(t)
	cfg.AgyCLIPath = writeFakeCLI(t, "agy", `echo '{"status":"SUCCESS","response":"agy answered"}'`)
	cfg.GeminiCLIPath = writeFakeCLI(t, "gemini", `echo 'gemini answered'`)
	cfg.ClaudeCLIPath = writeFakeCLI(t, "claude", `echo '{"type":"result","result":"claude answered"}'`)

	r := NewRouter(cfg)
	r.UseDefaults(func(Class) Selection { return Selection{Provider: "gemini"} })

	got, err := r.CompleteWith(context.Background(), Distill,
		Selection{Provider: "agy"}, Request{User: "soru"})
	if err != nil {
		t.Fatalf("CompleteWith: %v", err)
	}
	if got.Provider != "agy" {
		t.Errorf("koşunun kendi seçimi ezildi: %q", got.Provider)
	}
}
