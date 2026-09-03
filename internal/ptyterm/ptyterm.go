// Package ptyterm runs interactive shells on pseudo-terminals, so the app's
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
//
// # A shell outlives the window looking at it
//
// The unit of lifetime here is the *session*, not the connection. A shell is
// owned by the Registry and keeps running while nothing is attached: closing a
// viewer detaches, it does not hang up.
//
// This is not a refinement, it is the difference between the feature working
// and not. When the pty was owned by the websocket, opening the second profile
// unmounted the first viewer, which closed its socket, which SIGHUP'd that
// shell — so two accounts could never be open at once, and every `claude`
// launched in a killed session lost the workspace-trust answer the operator had
// just given, which is why it asked again every single time.
package ptyterm

import (
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
	// Name is what the picker shows, what a client sends back, and the key the
	// Registry holds the session under.
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

const (
	// readChunk is how much pty output one read may carry. Sized for a screen
	// redraw rather than a keystroke: a program clearing and repainting a
	// 120x30 terminal emits several kilobytes at once.
	readChunk = 32 * 1024

	// historyBytes is how much output a session remembers for a viewer that
	// attaches later. It has to hold enough escape sequences to reconstruct
	// what is on screen — a reattaching viewer replays it to catch up — and
	// bounding it is what stops a session that has been running all day from
	// holding a day of output in memory.
	historyBytes = 256 * 1024

	// subscriberQueue is how many chunks may be in flight to one viewer. A
	// local websocket drains far faster than a shell fills this; a viewer that
	// manages to fill it is not slow but gone, and is dropped rather than
	// allowed to stall the shell for everyone.
	subscriberQueue = 256
)

// Session is one running shell and the pty it is attached to.
//
// The zero value is not usable; sessions come from a Registry. A Session is
// safe for concurrent use: its own goroutine drains the pty while callers
// write input, resize, and attach or detach viewers.
type Session struct {
	profile Profile

	cmd *exec.Cmd
	tty *os.File

	// exited is closed when the shell is gone, so a caller can wait for the
	// end without polling and the Registry can forget the session.
	exited chan struct{}

	mu      sync.Mutex
	closed  bool
	history []byte
	sub     chan []byte
}

// Profile returns the identity this session was opened as.
func (s *Session) Profile() Profile { return s.profile }

// Done is closed when the shell exits.
func (s *Session) Done() <-chan struct{} { return s.exited }

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

// start opens a pty, runs an interactive login shell on it, and types the
// profile's command.
//
// The shell is started `-l -i` — login and interactive — because those two
// flags are what decide which rc files are read, and therefore whether
// oh-my-zsh and `claude-acct` exist at all. A non-interactive shell skips
// .zshrc entirely, which is why running the command through `zsh -c` would
// report "command not found" for a function the operator uses every day.
//
// Unexported: a session that no Registry owns is a shell nobody can find again
// and nobody will ever close.
func start(profile Profile, size Size) (*Session, error) {
	shell := shellPath()

	// argv[0] with a leading dash is the convention a login shell is
	// recognised by, and it is what Terminal.app passes. Some prompts and
	// profile scripts branch on it.
	cmd := exec.Command(shell, "-l", "-i")
	cmd.Args[0] = "-" + baseName(shell)

	if home, err := os.UserHomeDir(); err == nil && home != "" {
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

	s := &Session{profile: profile, cmd: cmd, tty: tty, exited: make(chan struct{})}
	go s.pump()

	if line := strings.TrimSpace(profile.Command); line != "" {
		if _, err := io.WriteString(tty, line+"\n"); err != nil {
			_ = s.Close()
			return nil, fmt.Errorf("ptyterm: typing profile %q: %w", profile.Name, err)
		}
	}
	return s, nil
}

// pump is the session's only reader of the pty.
//
// One reader, owned by the session, is what lets a viewer come and go without
// the shell noticing: output is always being drained into history, so a shell
// that writes while nobody is watching neither blocks on a full pty buffer nor
// loses what it said.
func (s *Session) pump() {
	buf := make([]byte, readChunk)
	for {
		n, err := s.tty.Read(buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			s.broadcast(chunk)
		}
		if err != nil {
			break
		}
	}

	s.mu.Lock()
	if s.sub != nil {
		close(s.sub)
		s.sub = nil
	}
	s.mu.Unlock()

	close(s.exited)
	_ = s.tty.Close()
	// Reaped here because this goroutine is the one that knows the shell is
	// finished; leaving it unwaited would keep a zombie for every session.
	_ = s.cmd.Wait()
}

// broadcast records a chunk in history and hands it to the attached viewer.
func (s *Session) broadcast(chunk []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.history = append(s.history, chunk...)
	if len(s.history) > historyBytes {
		// Trim from the front, and copy rather than reslice: a reslice keeps
		// the whole original array alive behind the smaller slice, so a
		// long-running session would never actually release the memory.
		keep := make([]byte, historyBytes)
		copy(keep, s.history[len(s.history)-historyBytes:])
		s.history = keep
	}

	if s.sub == nil {
		return
	}
	select {
	case s.sub <- chunk:
	default:
		// The viewer is not draining. Drop it rather than stall the shell;
		// history still holds these bytes, so reattaching catches up.
		close(s.sub)
		s.sub = nil
	}
}

// Attach subscribes a viewer to this session's output.
//
// It returns what the session has already said, a channel of what it says next,
// and a detach function. The history and the channel are handed over under one
// lock so nothing can be written between the two and go unseen.
//
// A second attach replaces the first: the UI holds one socket per profile, and
// two viewers of one shell would only be two copies of the same screen.
func (s *Session) Attach() (history []byte, out <-chan []byte, detach func()) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sub != nil {
		close(s.sub)
	}
	ch := make(chan []byte, subscriberQueue)
	s.sub = ch

	past := make([]byte, len(s.history))
	copy(past, s.history)

	return past, ch, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		// Only detach the subscription still in place: a viewer that was
		// already dropped, or replaced by a newer one, must not close the
		// newcomer's channel on its way out.
		if s.sub == ch {
			close(s.sub)
			s.sub = nil
		}
	}
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

// Close ends the session and releases the pty.
//
// The signal goes to the process group rather than the shell alone: whatever
// the operator was running is a child of that shell, and signalling only the
// parent leaves the child holding the pty open. Idempotent.
//
// Only a Registry (or a failed start) calls this. A viewer going away calls
// detach — the whole point of the session being the unit of lifetime.
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
	// Closing the pty makes pump's blocked Read return, which is what runs the
	// teardown and closes exited.
	return s.tty.Close()
}

// Registry owns the live sessions, one per profile.
//
// It exists so a shell can be found again. A viewer asks for a profile and gets
// whatever is already running under that name, which is what makes both
// terminals stay open while the operator looks at one of them.
type Registry struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

func NewRegistry() *Registry {
	return &Registry{sessions: map[string]*Session{}}
}

// Attach returns the live session for a profile, starting one if there is none.
//
// The size only applies to a session being started. An existing session keeps
// the size its viewer set: a second viewer with a different window must not
// reflow a shell somebody else is watching, and the viewer sends its own resize
// as soon as it has measured itself anyway.
func (r *Registry) Attach(profile Profile, size Size) (*Session, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if s, ok := r.sessions[profile.Name]; ok {
		select {
		case <-s.exited:
			// Gone since it was registered. Fall through and start a fresh one
			// rather than hand back a shell that cannot be typed into.
			delete(r.sessions, profile.Name)
		default:
			return s, nil
		}
	}

	s, err := start(profile, size)
	if err != nil {
		return nil, err
	}
	r.sessions[profile.Name] = s

	// Forget it when the shell exits, so the next attach starts a new one.
	go func() {
		<-s.exited
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.sessions[profile.Name] == s {
			delete(r.sessions, profile.Name)
		}
	}()

	return s, nil
}

// Kill ends one session. Reports whether there was one to end.
//
// The map entry goes here rather than being left to the watcher goroutine that
// reaps exited sessions. That goroutine only wakes once the shell has actually
// gone, so a caller that killed a session and immediately asked what was
// running would be told it still was — the registry has to be consistent the
// moment Kill returns, not shortly afterwards.
func (r *Registry) Kill(name string) bool {
	r.mu.Lock()
	s, ok := r.sessions[name]
	if ok {
		delete(r.sessions, name)
	}
	r.mu.Unlock()

	if !ok {
		return false
	}
	_ = s.Close()
	return true
}

// Live reports which profiles have a running shell, for a client that wants to
// show which terminals are up before opening one.
func (r *Registry) Live() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]string, 0, len(r.sessions))
	for name := range r.sessions {
		out = append(out, name)
	}
	return out
}

// CloseAll ends every session, for daemon shutdown.
func (r *Registry) CloseAll() {
	r.mu.Lock()
	all := make([]*Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		all = append(all, s)
	}
	r.mu.Unlock()

	for _, s := range all {
		_ = s.Close()
	}
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
