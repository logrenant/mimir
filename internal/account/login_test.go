package account

import (
	"os"
	"strings"
	"testing"
)

// The CLI wraps the authorization URL in a terminal hyperlink and paints it
// blue, so the bytes on the pty carry the address three times over: once inside
// an OSC 8 sequence, once as visible text, and once more closing the link. The
// URL has to be readable out of that, or the browser is never opened and the
// operator is left looking at a shell.
func TestAuthURL_SurvivesTheEscapeSequencesTheCLIPaintsItWith(t *testing.T) {
	raw := "Opening browser to sign in…\r\n" +
		"If the browser didn't open, visit: " +
		"\x1b]8;;https://claude.com/cai/oauth/authorize?code=true&state=abc\x07" +
		"\x1b[94mhttps://claude.com/cai/oauth/authorize?code=true&state=abc\x1b[39m" +
		"\x1b]8;;\x07\r\n"

	clean := string(ansi.ReplaceAll([]byte(raw), nil))
	if strings.Contains(clean, "\x1b") {
		t.Errorf("an escape sequence survived: %q", clean)
	}

	got := authURL.FindString(clean)
	want := "https://claude.com/cai/oauth/authorize?code=true&state=abc"
	if got != want {
		t.Errorf("URL: got %q, want %q", got, want)
	}
}

// The prompt that means the CLI gave up on opening a browser itself. Detected
// on the readable text, not the raw bytes, for the same reason as above.
func TestLoginOutput_TheCodePromptIsReadableAfterStripping(t *testing.T) {
	raw := []byte("\x1b[2mPaste code here if prompted\x1b[22m > ")
	clean := strings.ToLower(string(ansi.ReplaceAll(raw, nil)))
	if !strings.Contains(clean, "paste code") {
		t.Errorf("the paste prompt was not readable: %q", clean)
	}
}

// The shim has to be found *before* /usr/bin, or the CLI opens the default
// browser with whatever Claude session it already has — the one thing this
// flow exists to avoid.
func TestLoginEnviron_PutsTheShimFirstOnPath(t *testing.T) {
	parent := []string{"PATH=/usr/bin:/bin", "HOME=/Users/x", "CLAUDECODE=1"}

	got := loginEnviron(parent, "/slots/mimir", "/slots/mimir/browser-shim")

	var path string
	for _, kv := range got {
		if after, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = after
		}
	}
	if !strings.HasPrefix(path, "/slots/mimir/browser-shim"+string(os.PathListSeparator)) {
		t.Errorf("PATH does not start with the shim: %q", path)
	}
	if !strings.HasSuffix(path, "/usr/bin:/bin") {
		t.Errorf("the machine's own PATH did not survive: %q", path)
	}
	if !contains(got, "CLAUDE_SECURESTORAGE_CONFIG_DIR=/slots/mimir") {
		t.Errorf("the login was not pointed at Mimir's slot: %v", got)
	}
	// Environ's job, re-checked here because this is the environment that
	// actually reaches the login: a nested session's variables would make the
	// CLI think it was resuming somebody else's session.
	if contains(got, "CLAUDECODE=1") {
		t.Error("the parent session's CLAUDECODE reached the login")
	}
}

// A shim that failed would push the CLI onto its paste-a-code path, which asks
// the operator to do by hand what the loopback callback does for them.
func TestWriteBrowserShim_IsAnExecutableThatSucceeds(t *testing.T) {
	dir := t.TempDir()

	shimDir, err := writeBrowserShim(dir)
	if err != nil {
		t.Fatalf("writeBrowserShim: %v", err)
	}
	info, err := os.Stat(shimDir + "/open")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("the shim is not executable: %v", info.Mode())
	}
	body, err := os.ReadFile(shimDir + "/open")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(body), "exit 0") {
		t.Errorf("the shim must succeed, not fail: %q", body)
	}
}

// A reset kills the login, and the goroutine draining its pty is still blocked
// on a read when the signal lands. What it learns afterwards — that the process
// died — is about an attempt nobody is waiting on any more, so it must not
// settle over the idle state the reset just wrote. Without the generation guard
// the panel showed "giriş tamamlanmadı" immediately after signing out.
func TestLoginState_AStaleAttemptDoesNotSettleOverAReset(t *testing.T) {
	r := NewRegistry(nil, t.TempDir(), "claude")

	r.login.mu.Lock()
	r.login.gen = 1
	r.login.state = LoginWaiting
	r.login.mu.Unlock()
	stale := uint64(1)

	r.cancelLogin()

	r.setLogin(stale, func(f *loginFlow) {
		f.state = LoginFailed
		f.message = "exit status 1"
	})
	if got := r.LoginState(); got.State != LoginIdle || got.Message != "" {
		t.Errorf("a killed attempt wrote over the reset: %+v", got)
	}

	// And its output is dropped too, rather than piling up under a state it no
	// longer describes.
	if r.appendLoginOutput(stale, []byte("late output")) != "" {
		t.Error("output from a killed attempt was kept")
	}
	if got := r.LoginState(); got.Output != "" {
		t.Errorf("Output: got %q, want empty", got.Output)
	}
}
