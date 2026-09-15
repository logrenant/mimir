package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/logrenant/mimir/internal/graphify"
	"github.com/logrenant/mimir/internal/settings"
)

// fakeProbe is the machine's answer, without a Python on it.
type fakeProbe struct {
	info    graphify.Info
	found   bool
	asked   []string
	forgets int
}

func (f *fakeProbe) Look(_ context.Context, python string) (graphify.Info, bool) {
	f.asked = append(f.asked, python)
	return f.info, f.found
}

func (f *fakeProbe) Forget() { f.forgets++ }

func structuralServer(t *testing.T, probe StructuralProbe) http.Handler {
	t.Helper()
	return New(testConfig(), Deps{
		Settings:   settings.New(t.TempDir()),
		Structural: probe,
	}).Handler()
}

func decodeStructural(t *testing.T, body string) structuralResponse {
	t.Helper()
	var got structuralResponse
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decoding %q: %v", body, err)
	}
	return got
}

// "Not installed" is a normal state and must not read as an error. It is also
// the state that has to carry the pip command, because that is the one thing
// the operator can do about it.
func TestStructural_NotInstalledIsAnAnswerNotAFault(t *testing.T) {
	h := structuralServer(t, &fakeProbe{})

	w := do(h, "GET", "/brain/structural", testToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	got := decodeStructural(t, w.Body.String())

	if !got.Enabled {
		t.Error("enabled = false; the shipped default is on")
	}
	if got.Installed {
		t.Error("installed = true with nothing found")
	}
	// Not `pip install graphifyy`: `pip` is usually not on PATH on a Mac, and
	// where it is, the system Python either refuses (PEP 668) or accepts into a
	// directory brew will replace. The command has to be one that works.
	if !strings.Contains(got.Install, "venv") || !strings.Contains(got.Install, "graphifyy") {
		t.Errorf("install = %q, want a command that makes an environment", got.Install)
	}
	// And when nothing was found, where it looked — the answer to "but I
	// installed it".
	if len(got.Looked) == 0 {
		t.Error("looked = none; a missing package should say where it was sought")
	}
}

func TestStructural_ReportsWhichInterpreterAnswered(t *testing.T) {
	probe := &fakeProbe{found: true, info: graphify.Info{Python: "/opt/py/bin/python3", Version: "0.9.54"}}
	h := structuralServer(t, probe)

	got := decodeStructural(t, do(h, "GET", "/brain/structural", testToken, "").Body.String())
	if !got.Installed || got.Version != "0.9.54" || got.Interpreter != "/opt/py/bin/python3" {
		t.Fatalf("got = %+v", got)
	}
}

// Looking for an interpreter the operator has told us not to use would spend a
// subprocess to fill in a field the screen greys out.
func TestStructural_DoesNotProbeWhileSwitchedOff(t *testing.T) {
	probe := &fakeProbe{found: true, info: graphify.Info{Version: "0.9.54"}}
	h := structuralServer(t, probe)

	if w := do(h, "PUT", "/brain/structural", testToken, `{"enabled":false}`); w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	before := len(probe.asked)

	got := decodeStructural(t, do(h, "GET", "/brain/structural", testToken, "").Body.String())
	if got.Enabled {
		t.Error("enabled = true after being switched off")
	}
	if got.Installed {
		t.Error("installed = true while switched off; nothing was asked")
	}
	if len(probe.asked) != before {
		t.Errorf("the probe was run %d more times while switched off", len(probe.asked)-before)
	}
}

// An operator naming a different Python is asking to be told about that one,
// so the cached answer about the previous one is dropped rather than served.
func TestStructural_NamingAnInterpreterForgetsTheCachedAnswer(t *testing.T) {
	probe := &fakeProbe{}
	h := structuralServer(t, probe)

	w := do(h, "PUT", "/brain/structural", testToken, `{"enabled":true,"python":"/opt/py/bin/python3.12"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if probe.forgets != 1 {
		t.Errorf("forgets = %d, want 1", probe.forgets)
	}

	got := decodeStructural(t, w.Body.String())
	if got.Python != "/opt/py/bin/python3.12" {
		t.Errorf("python = %q", got.Python)
	}
	if len(probe.asked) == 0 || probe.asked[len(probe.asked)-1] != "/opt/py/bin/python3.12" {
		t.Errorf("asked = %v, want the interpreter that was just named", probe.asked)
	}
}

func TestStructural_RefusesABodyThatIsNotAnObject(t *testing.T) {
	h := structuralServer(t, &fakeProbe{})
	if w := do(h, "PUT", "/brain/structural", testToken, `[]`); w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// Gated on Settings, like the scan policy: a daemon with no probe at all still
// answers, because "which folders may be read" and "may the parser run" are
// both worth answering on a machine somebody is debugging.
func TestStructural_ServedWithoutAProbe(t *testing.T) {
	h := structuralServer(t, nil)

	w := do(h, "GET", "/brain/structural", testToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if decodeStructural(t, w.Body.String()).Installed {
		t.Error("installed = true with no probe to ask")
	}
}
