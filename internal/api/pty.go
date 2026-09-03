package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/coder/websocket"
	"github.com/logrenant/mimir/internal/ptyterm"
)

// ptyReadChunk is how much pty output one read may carry.
//
// Sized for a screen redraw rather than a keystroke: a program clearing and
// repainting a 120x30 terminal emits several kilobytes at once, and reading it
// in one frame keeps the client from rendering a half-drawn screen.
const ptyReadChunk = 32 * 1024

// ptyClientMessage is what the browser sends up the socket.
//
// One envelope for both kinds of message, because they share an ordering that
// matters: a resize that overtook the input typed before it would reflow text
// the shell had not yet echoed.
type ptyClientMessage struct {
	Type string `json:"type"`
	// Data carries keystrokes for Type "input".
	Data string `json:"data"`
	// Rows and Cols carry the viewport for Type "resize".
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
}

// handleTerminalProfiles lists the identities a session can be opened as.
//
// A GET rather than a constant compiled into the app: the picker and the shell
// that actually runs the command must not be able to disagree about what
// "eziode" means, and one of them has to be the authority.
func (s *Server) handleTerminalProfiles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"profiles": ptyterm.Profiles})
}

// handleTerminalPTY runs an interactive shell and bridges it to a websocket.
//
// The shape mirrors handleRunStream: everything that can still be an HTTP
// status is resolved before the upgrade, because after the handshake a failure
// can only be a close frame the operator never sees. Here that means the
// profile — an unknown name is a 400, not a session opened as somebody else.
//
// Output is sent as binary frames of raw pty bytes rather than text. The pty
// emits whatever the program wrote, which is not required to be valid UTF-8 at
// a read boundary: a text frame would force a lossy conversion mid-escape
// sequence, and the terminal would render the tail of a colour code as
// characters. xterm.js reassembles partial sequences itself, so handing it the
// bytes untouched is both simpler and more correct.
func (s *Server) handleTerminalPTY(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("profile")
	if name == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "profile is required")
		return
	}
	profile, ok := ptyterm.ProfileByName(name)
	if !ok {
		writeError(w, http.StatusBadRequest, codeBadRequest,
			"unknown terminal profile "+strconv.Quote(name))
		return
	}

	size := ptyterm.Size{
		Rows: uint16(atoiDefault(r.URL.Query().Get("rows"), 30)),
		Cols: uint16(atoiDefault(r.URL.Query().Get("cols"), 120)),
	}

	// Started before the upgrade so a shell that cannot start — no pty
	// available, SHELL pointing at nothing — is still a 500 with a readable
	// body rather than a socket that opens and immediately closes.
	session, err := ptyterm.Start(profile, size)
	if err != nil {
		slog.Warn("starting pty session", "profile", profile.Name, "error", err)
		writeError(w, http.StatusInternalServerError, codeInternal, err.Error())
		return
	}
	defer func() { _ = session.Close() }()

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols: negotiableSubprotocols(r),
		// Same reasoning as handleRunStream: the bearer token was checked
		// before this handler ran, and the legitimate client is a Tauri WebView
		// whose origin is not this host.
		InsecureSkipVerify: true,
	})
	if err != nil {
		slog.Warn("pty websocket upgrade failed", "profile", profile.Name, "error", err)
		return
	}
	defer func() { _ = conn.CloseNow() }()

	// A screen repaint is far larger than the default read limit, and the
	// limit applies to frames we read — paste is the case that exceeds it.
	conn.SetReadLimit(ptyReadChunk)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Output pump. Its own goroutine because both directions block: the shell
	// may sit silent while the operator types, and vice versa.
	go func() {
		defer cancel()
		buf := make([]byte, ptyReadChunk)
		for {
			n, readErr := session.Read(buf)
			if n > 0 {
				writeCtx, done := context.WithTimeout(ctx, wsWriteTimeout)
				err := conn.Write(writeCtx, websocket.MessageBinary, buf[:n])
				done()
				if err != nil {
					return
				}
			}
			if readErr != nil {
				// EOF is the shell exiting, which is a normal end of session.
				if !errors.Is(readErr, io.EOF) {
					slog.Debug("pty read ended", "profile", profile.Name, "error", readErr)
				}
				return
			}
		}
	}()

	// Input pump, on this goroutine so the handler lives as long as the socket.
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}

		var msg ptyClientMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			// A frame we cannot parse is the client's bug, not a reason to
			// drop a session the operator may have work in.
			slog.Debug("unparsable pty frame", "profile", profile.Name, "error", err)
			continue
		}

		switch msg.Type {
		case "input":
			if _, err := session.Write([]byte(msg.Data)); err != nil {
				return
			}
		case "resize":
			if err := session.Resize(ptyterm.Size{Rows: msg.Rows, Cols: msg.Cols}); err != nil {
				slog.Debug("pty resize", "profile", profile.Name, "error", err)
			}
		}
	}
}

// atoiDefault parses a query dimension, falling back when it is absent or
// nonsense. A bad size is not worth refusing a session over — ptyterm clamps
// it, and the client sends a real one as soon as it has measured itself.
func atoiDefault(s string, fallback int) int {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}
