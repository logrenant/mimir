package account

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// Signing Mimir's slot in, and signing it out again.
//
// `claude auth login` is an interactive program: it prints a URL, opens a
// browser, and then either waits on a loopback callback or asks for a code to
// be pasted back. So it is run on a pty rather than over pipes — over pipes it
// takes the non-interactive path and there is nothing to complete.
//
// Two things about the browser are deliberate:
//
//   - The CLI's own `open` is neutralised with a PATH shim, and Mimir opens the
//     window itself. Left alone the CLI opens the *default* browser with
//     whatever Claude session it already has, which is the one thing this flow
//     must not do — the point of connecting an account here is choosing which
//     one, and a browser already signed in never asks.
//   - The shim exits 0 rather than failing. The CLI picks its redirect target
//     from whether it believes a browser opened: a successful open means a
//     `http://localhost:<port>/callback` the CLI is listening on, so finishing
//     in the browser finishes the login with nothing to paste. A failed one
//     drops to the paste-a-code flow, which still works here (SubmitCode) but
//     asks the operator to do by hand what the callback does for them.

// loginTimeout bounds one login attempt. It is generous because the operator
// is signing into a website in the middle of it; what it prevents is a shell
// left running for the life of the daemon after they gave up.
const loginTimeout = 10 * time.Minute

// loginOutputMax is how much of the CLI's own output is kept to show back. The
// tail is the part that says what it is waiting for.
const loginOutputMax = 4 * 1024

// Login states, as the desktop app reads them.
const (
	// LoginIdle means nothing has been started, or the last attempt was
	// cleared by a reset.
	LoginIdle = "idle"
	// LoginOpening means the CLI is running but has not printed a URL yet.
	LoginOpening = "opening"
	// LoginWaiting means the browser window is open and the CLI is waiting on
	// its callback. Nothing more is asked of the operator here.
	LoginWaiting = "waiting"
	// LoginCode means the CLI fell back to asking for a pasted code.
	LoginCode = "code"
	// LoginDone means the slot is signed in.
	LoginDone = "done"
	// LoginFailed means the attempt ended without a login.
	LoginFailed = "failed"
)

// LoginState is one login attempt as a client reads it.
type LoginState struct {
	State string `json:"state"`
	// URL is the authorization page. Reported even though Mimir opened it: a
	// window that landed behind another app, or a Chrome that is not
	// installed, leaves the operator with a link to use.
	URL string `json:"url,omitempty"`
	// Message says what happened in Mimir's own words — which browser was
	// opened, or why the attempt failed.
	Message string `json:"message,omitempty"`
	// Output is the tail of the CLI's own output, ANSI stripped. Shown
	// verbatim: it is already written for a human.
	Output string `json:"output,omitempty"`
	// Email is filled in once the attempt succeeded and the slot was probed.
	Email string `json:"email,omitempty"`
}

// loginFlow is the at-most-one attempt in flight. Zero value is idle.
type loginFlow struct {
	mu sync.Mutex

	// gen names the current attempt. The goroutine draining a login's pty
	// outlives a reset that killed it — it is still blocked on a read when the
	// signal lands — and it would otherwise settle the state it finds *after*
	// the reset cleared it, leaving "giriş tamamlanmadı" on a panel that has
	// just signed out. It carries the generation it started under and stops
	// writing the moment that is no longer the current one.
	gen uint64

	state   string
	url     string
	message string
	email   string
	output  []byte

	cmd    *exec.Cmd
	tty    *os.File
	cancel context.CancelFunc
}

// ansi matches the escape sequences the CLI paints its output with: CSI for
// colour, and OSC for the terminal hyperlink it wraps the URL in. Both have to
// go before the URL can be read out of the text.
var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

// authURL matches the authorization page the CLI prints.
var authURL = regexp.MustCompile(`https://[a-zA-Z0-9.-]*claude\.(?:com|ai)/[^\s"'<>]+`)

// LoginState reports the attempt in flight, or the last one's outcome.
func (r *Registry) LoginState() LoginState {
	r.login.mu.Lock()
	defer r.login.mu.Unlock()
	return r.login.snapshot()
}

// snapshot must be called with the lock held.
func (f *loginFlow) snapshot() LoginState {
	state := f.state
	if state == "" {
		state = LoginIdle
	}
	return LoginState{
		State:   state,
		URL:     f.url,
		Message: f.message,
		Output:  strings.TrimSpace(string(f.output)),
		Email:   f.email,
	}
}

// StartLogin signs Mimir's slot in.
//
// It returns as soon as the CLI has printed its URL and the browser has been
// opened — not when the login finishes. The operator is in a browser at that
// point, so the client polls LoginState rather than holding a request open for
// minutes.
//
// An attempt already in flight is returned as it stands rather than restarted:
// two `claude auth login` processes on one slot race for the same keychain
// entry, and the second would open a window whose code the first cannot use.
func (r *Registry) StartLogin(parent context.Context) (LoginState, error) {
	r.login.mu.Lock()
	switch r.login.state {
	case LoginOpening, LoginWaiting, LoginCode:
		state := r.login.snapshot()
		r.login.mu.Unlock()
		return state, nil
	}
	r.login.mu.Unlock()

	if err := r.ensureDir(); err != nil {
		return LoginState{}, err
	}
	shim, err := writeBrowserShim(r.dir)
	if err != nil {
		return LoginState{}, err
	}

	// The attempt outlives the request that started it, so it gets its own
	// context. Cancelling the request must not cancel a browser window the
	// operator is already typing into.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), loginTimeout)

	cmd := exec.CommandContext(ctx, r.cliPath, "auth", "login")
	cmd.Env = loginEnviron(os.Environ(), r.dir, shim)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}

	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 120})
	if err != nil {
		cancel()
		return LoginState{}, fmt.Errorf("account: starting `%s auth login`: %w", r.cliPath, err)
	}

	r.login.mu.Lock()
	r.login.gen++
	gen := r.login.gen
	r.login.state = LoginOpening
	r.login.url = ""
	r.login.message = ""
	r.login.email = ""
	r.login.output = nil
	r.login.cmd = cmd
	r.login.tty = tty
	r.login.cancel = cancel
	r.login.mu.Unlock()

	opened := make(chan struct{})
	go r.readLogin(ctx, gen, cmd, tty, cancel, opened)

	// Wait for the URL rather than returning immediately: the whole point of
	// the call is the window, and a client told "opening" with no URL has
	// nothing to show and no way to retry.
	select {
	case <-opened:
	case <-time.After(30 * time.Second):
	case <-ctx.Done():
	}
	return r.LoginState(), nil
}

// readLogin drains the pty, reacts to what the CLI says, and settles the state
// when the process is gone.
func (r *Registry) readLogin(ctx context.Context, gen uint64, cmd *exec.Cmd, tty *os.File,
	cancel context.CancelFunc, opened chan struct{}) {
	defer cancel()

	buf := make([]byte, 4096)
	announced := false
	for {
		n, err := tty.Read(buf)
		if n > 0 {
			text := r.appendLoginOutput(gen, buf[:n])
			if !announced {
				if url := authURL.FindString(text); url != "" {
					announced = true
					message := openIncognito(url)
					r.setLogin(gen, func(f *loginFlow) {
						f.url = url
						f.message = message
						if f.state == LoginOpening {
							f.state = LoginWaiting
						}
					})
					close(opened)
				}
			}
			if strings.Contains(strings.ToLower(text), "paste code") {
				r.setLogin(gen, func(f *loginFlow) {
					if f.state == LoginOpening || f.state == LoginWaiting {
						f.state = LoginCode
					}
				})
			}
		}
		if err != nil {
			break
		}
	}
	_ = tty.Close()
	_ = cmd.Wait()
	if !announced {
		close(opened)
	}

	// The CLI's exit code is not the answer — it exits non-zero when it is
	// killed by the timeout, and the operator may well have finished the login
	// before that. The keychain is the authority, so the slot is probed.
	status, err := Probe(context.WithoutCancel(ctx), r.cliPath, r.dir)
	if err == nil && status.LoggedIn {
		if _, err := r.record(context.WithoutCancel(ctx)); err != nil {
			r.setLogin(gen, func(f *loginFlow) {
				f.state = LoginFailed
				f.message = fmt.Sprintf("giriş yapıldı ama hesap kaydedilemedi: %v", err)
			})
			return
		}
		r.setLogin(gen, func(f *loginFlow) {
			f.state = LoginDone
			f.email = status.Email
			f.message = ""
		})
		return
	}

	r.setLogin(gen, func(f *loginFlow) {
		if f.state == LoginDone {
			return
		}
		f.state = LoginFailed
		if status.Error != "" {
			f.message = status.Error
		} else if f.message == "" {
			f.message = "giriş tamamlanmadı"
		}
	})
}

// appendLoginOutput records a chunk and returns the readable tail, so the
// caller can look for the URL and the paste prompt in the same pass.
func (r *Registry) appendLoginOutput(gen uint64, chunk []byte) string {
	r.login.mu.Lock()
	defer r.login.mu.Unlock()

	if r.login.gen != gen {
		return ""
	}
	clean := ansi.ReplaceAll(chunk, nil)
	r.login.output = append(r.login.output, clean...)
	if len(r.login.output) > loginOutputMax {
		r.login.output = r.login.output[len(r.login.output)-loginOutputMax:]
	}
	return string(r.login.output)
}

// setLogin applies a change only if the attempt that asked for it is still the
// current one.
func (r *Registry) setLogin(gen uint64, mutate func(*loginFlow)) {
	r.login.mu.Lock()
	defer r.login.mu.Unlock()
	if r.login.gen != gen {
		return
	}
	mutate(&r.login)
}

// SubmitCode answers the CLI's paste prompt.
//
// Only reachable in the LoginCode state, which is the fallback the CLI takes
// when it could not open a browser itself. Typed into the same pty the flow is
// running on, because that is the process waiting for it.
func (r *Registry) SubmitCode(code string) error {
	code = strings.TrimSpace(code)
	if code == "" {
		return fmt.Errorf("account: the code is empty")
	}

	r.login.mu.Lock()
	tty := r.login.tty
	state := r.login.state
	r.login.mu.Unlock()

	if tty == nil || (state != LoginCode && state != LoginWaiting) {
		return fmt.Errorf("account: no login is waiting for a code")
	}
	if _, err := tty.WriteString(code + "\n"); err != nil {
		return fmt.Errorf("account: sending the code: %w", err)
	}
	return nil
}

// cancelLogin ends an attempt in flight. Part of Reset: a browser window whose
// process is gone is a window that can no longer finish anything.
func (r *Registry) cancelLogin() {
	r.login.mu.Lock()
	cmd, cancel := r.login.cmd, r.login.cancel
	// A new generation, so the goroutine still draining that pty settles
	// nothing: it is about to see the process die and would otherwise report
	// the kill as a failed login on a panel that has just signed out.
	r.login.gen++
	r.login.cmd, r.login.tty, r.login.cancel = nil, nil, nil
	r.login.state = LoginIdle
	r.login.url, r.login.message, r.login.email, r.login.output = "", "", "", nil
	r.login.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		// Negative pid is the process group's, which is the login's own
		// because it was started with Setsid.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	if cancel != nil {
		cancel()
	}
}

// logout signs the slot out. Best effort by contract — see Reset.
func logout(ctx context.Context, cliPath, dir string) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, cliPath, "auth", "logout")
	cmd.Env = Environ(os.Environ(), dir)
	_ = cmd.Run()
}

// loginEnviron is the login subprocess's environment: the slot, plus a PATH
// whose first entry is the shim that neutralises the CLI's own browser launch.
func loginEnviron(parent []string, dir, shimDir string) []string {
	out := make([]string, 0, len(parent)+2)
	for _, kv := range Environ(parent, dir) {
		if strings.HasPrefix(kv, "PATH=") {
			out = append(out, "PATH="+shimDir+string(os.PathListSeparator)+strings.TrimPrefix(kv, "PATH="))
			continue
		}
		out = append(out, kv)
	}
	// A pty with no TERM leaves the CLI assuming "dumb", which is a different
	// code path with different prompts.
	out = append(out, "TERM=xterm-256color")
	return out
}

// writeBrowserShim creates the directory whose `open` the CLI finds first.
func writeBrowserShim(dir string) (string, error) {
	shimDir := filepath.Join(dir, "browser-shim")
	if err := os.MkdirAll(shimDir, 0o700); err != nil {
		return "", fmt.Errorf("account: creating %q: %w", shimDir, err)
	}
	shim := filepath.Join(shimDir, "open")
	// Exits 0 without doing anything: the CLI must believe the browser opened
	// (so it listens on its loopback callback) while the window Mimir opens is
	// the only one that appears.
	if err := os.WriteFile(shim, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		return "", fmt.Errorf("account: writing %q: %w", shim, err)
	}
	return shimDir, nil
}

// chromeApp is the browser Mimir opens the authorization page in. Named
// exactly as `open -a` expects it.
const chromeApp = "Google Chrome"

// openIncognito opens the authorization page in a private window, and says
// what it managed to do.
//
// Private matters more than which browser: a normal window carries whichever
// Claude session the operator is already signed into, and the page then never
// asks who is connecting. Chrome is the one browser whose private mode can be
// asked for from the command line, so a machine without it gets an ordinary
// window and is told so rather than being left to wonder why the page skipped
// the question.
func openIncognito(url string) string {
	if _, err := os.Stat("/Applications/" + chromeApp + ".app"); err == nil {
		cmd := exec.Command("/usr/bin/open", "-na", chromeApp, "--args", "--incognito", url)
		if err := cmd.Run(); err == nil {
			return "Giriş sayfası Chrome'da gizli pencerede açıldı."
		}
	}
	if err := exec.Command("/usr/bin/open", url).Run(); err != nil {
		return "Tarayıcı açılamadı — giriş bağlantısını elle açın."
	}
	return "Chrome bulunamadı: sayfa varsayılan tarayıcıda, normal pencerede açıldı — " +
		"orada zaten açık bir Claude oturumu varsa hangi hesapla bağlandığınızı sormayabilir."
}
