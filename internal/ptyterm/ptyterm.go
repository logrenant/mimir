// Package ptyterm runs an interactive shell on a pseudo-terminal, so the app's
// terminal is the operator's own terminal rather than a transcript of one.
//
// The distinction matters more than it sounds. The coding runner spawns
// `claude -p` headless and pipes its stdout: that is a report of a session, and
// nothing in it can be typed into. Everything the operator configured for their
// shell — oh-my-zsh, its plugins, the prompt, aliases, and the `claude-acct`
// function that selects a credential slot — exists only inside an interactive
// login shell, and a pipe never starts one.
//
// So this package starts the same process Terminal.app starts: `$SHELL -l -i`
// on a pty. Anything the operator's zsh does in their own window, it does here,
// because it is the same shell reading the same rc files. A profile is then
// typed into it exactly as a person would type it — see Profile.
package ptyterm

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"github.com/creack/pty"
)

// Profile is a named way to open a session, as a line typed at the prompt.
//
// The command is typed rather than exec'd because the two are not equivalent:
// `claude-acct` is a shell function defined in the operator's .zshrc, so it has
// no binary to exec — only an interactive shell that has sourced that file can
// run it. Typing it also means what the operator sees in the scrollback is the
// command they would have written themselves, which is the point of the
// feature: no hidden wrapper, nothing to keep in sync with their shell.
type Profile struct {
	// Name is what the picker shows and what a client sends back.
	Name string `json:"name"`
	// Command is the line typed at the prompt to begin the session. Empty
	// opens a plain shell and types nothing.
	Command string `json:"command"`
}

// Profiles are the identities a session can be opened as.
//
// They are declared here rather than derived from the account registry on
// purpose. The registry answers "which credential slots exist on disk", which
// is a different question: both discovered slots resolve to the same login, and
// the operator's shell already owns the mapping from a name to the command that
// selects it. Deriving these would mean re-deciding, in Go, something .zshrc
// has already decided — and getting it wrong silently.
var Profiles = []Profile{
	// The default keychain slot, which is what a bare `claude` spends.
	{Name: "salihdevran", Command: "claude"},
	{Name: "eziode", Command: "claude-acct eziode"},
}

// ProfileByName looks up a profile. The bool is false for an unknown name,
// which a caller should treat as a bad request rather than falling back: the
// operator picked an identity, and quietly opening a different one would spend
// the wrong account.
func ProfileByName(name string) (Profile, bool) {
	for _, p := range Profiles {
		if p.Name == name {
			return p, true
		}
	}
	return Profile{}, false
}

// Size is a terminal's dimensions in character cells.
type Size struct {
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
}

// Session is one running shell and the pty it is attached to.
//
// The zero value is not usable; call Start. A Session is safe for concurrent
// use: one goroutine typically reads output while another writes input and a
// third resizes.
type Session struct {
	cmd *exec.Cmd
	tty *os.File

	mu     sync.Mutex
	closed bool
}

// shellPath is the shell to run, preferring the operator's own.
//
// SHELL is what their terminal emulator reads to decide the same thing, so
// reading it here is what makes the two agree. Under launchd SHELL is set from
// the user record, so this holds for the daemon too; the fallback is only for
// an environment that has stripped it entirely.
func shellPath() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/zsh"
}

// Start opens a pty, runs an interactive login shell on it, and types the
// profile's command.
//
// The shell is started `-l -i` — login and interactive — because those two
// flags are what decide which rc files are read, and therefore whether
// oh-my-zsh and `claude-acct` exist at all. A non-interactive shell skips
// .zshrc entirely, which is why running the command through `zsh -c` would
// report "command not found" for a function the operator uses every day.
func Start(profile Profile, size Size) (*Session, error) {
	shell := shellPath()

	// argv[0] with a leading dash is the convention a login shell is
	// recognised by, and it is what Terminal.app passes. Some prompts and
	// profile scripts branch on it.
	cmd := exec.Command(shell, "-l", "-i")
	cmd.Args[0] = "-" + baseName(shell)

	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		cmd.Dir = home
	}
	cmd.Env = environ()

	// Setsid: the shell must be the session leader of its own session for the
	// pty to become its controlling terminal. Without one, job control is off
	// and ^C reaches nothing — the shell would report "no job control in this
	// shell" and interactive programs could not be interrupted.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}

	tty, err := pty.StartWithSize(cmd, &pty.Winsize{
		Rows: clampDim(size.Rows, 24),
		Cols: clampDim(size.Cols, 80),
	})
	if err != nil {
		return nil, fmt.Errorf("ptyterm: starting %s: %w", shell, err)
	}

	s := &Session{cmd: cmd, tty: tty}

	if line := strings.TrimSpace(profile.Command); line != "" {
		if _, err := io.WriteString(tty, line+"\n"); err != nil {
			_ = s.Close()
			return nil, fmt.Errorf("ptyterm: typing profile %q: %w", profile.Name, err)
		}
	}
	return s, nil
}

// baseName is filepath.Base without the import, for the argv[0] convention.
func baseName(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

// clampDim keeps a dimension positive. A zero from a client that has not
// measured its viewport yet would make the kernel pick 0x0, and curses programs
// draw nothing at all on such a terminal.
func clampDim(v, fallback uint16) uint16 {
	if v == 0 {
		return fallback
	}
	return v
}

// environ is the shell's environment.
//
// Deliberately the daemon's own, plus the two variables a pty needs. The
// session variables that account.Environ strips are not stripped here, and the
// difference is intentional: those exist to stop a *headless* nested `claude`
// from believing it is resuming the session that launched it. This shell is not
// nested in anything — it is a login shell, it re-reads the operator's rc files
// from scratch, and stripping what their terminal would have had is what would
// make it differ from their terminal.
func environ() []string {
	out := os.Environ()

	// TERM is what curses and every prompt read to decide what they may draw.
	// A pty with no TERM leaves them assuming "dumb": no colour, no cursor
	// addressing, and oh-my-zsh's prompt rendered as escape codes in the text.
	if os.Getenv("TERM") == "" {
		out = append(out, "TERM=xterm-256color")
	}
	// Without a UTF-8 locale the shell mangles the non-ASCII characters that
	// this operator's company names are full of, and zsh's line editor
	// miscounts their width when redrawing the line.
	if os.Getenv("LANG") == "" {
		out = append(out, "LANG=en_US.UTF-8")
	}
	return out
}

// Read returns output from the shell. It is io.Reader on the pty, so a read
// returns as soon as any byte is available rather than waiting for a line.
func (s *Session) Read(p []byte) (int, error) {
	n, err := s.tty.Read(p)
	// A closed pty surfaces as EIO on this platform once the child is gone.
	// That is the normal end of a session, not a fault, so it is reported as
	// EOF — the shape every caller already handles.
	if err != nil && isClosedPTY(err) {
		return n, io.EOF
	}
	return n, err
}

// Write sends input to the shell, exactly as a keystroke would arrive.
func (s *Session) Write(p []byte) (int, error) {
	return s.tty.Write(p)
}

// Resize tells the kernel the terminal's new size, which makes it deliver
// SIGWINCH to the foreground process group. Programs that draw a full screen
// redraw on that signal; without it they keep using the old size and their
// output wraps against a width that is no longer there.
func (s *Session) Resize(size Size) error {
	return pty.Setsize(s.tty, &pty.Winsize{
		Rows: clampDim(size.Rows, 24),
		Cols: clampDim(size.Cols, 80),
	})
}

// Wait blocks until the shell exits.
func (s *Session) Wait() error { return s.cmd.Wait() }

// Close ends the session and releases the pty.
//
// The signal goes to the process group rather than the shell alone: whatever
// the operator was running is a child of that shell, and signalling only the
// parent leaves the child holding the pty open. Idempotent, so a caller may
// close on both the read error and the deferred path.
func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	if s.cmd.Process != nil {
		// Negative pid means "the group with this id", which is the shell's
		// own, because it was started with Setsid.
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGHUP)
	}
	return s.tty.Close()
}

// isClosedPTY reports whether an error is the ordinary end of a pty rather
// than a fault worth showing.
func isClosedPTY(err error) bool {
	return errors.Is(err, syscall.EIO) || errors.Is(err, os.ErrClosed)
}
