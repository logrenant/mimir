package api

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/logrenant/mimir/internal/coderunner"
	"github.com/logrenant/mimir/internal/config"
	"github.com/logrenant/mimir/internal/project"
)

// Error codes. Wire strings — the desktop app switches on them.
const (
	codeBadRequest   = "bad_request"
	codeUnauthorized = "unauthorized"
	codeForbidden    = "forbidden"
	codeNotFound     = "not_found"
	codeInternal     = "internal"
)

type errorEnvelope struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: errorDetail{Code: code, Message: message}})
}

// writeDomainError maps an error from the runtime packages onto a status.
//
// Only errors whose text is already written for a human — the path guards in
// internal/project say what is wrong and what to do — are echoed. Anything
// unrecognised is logged and answered with a generic 500: an internal failure
// message is for the operator's log, not for a response body.
func writeDomainError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, project.ErrInvalidPath), errors.Is(err, project.ErrPathNotAllowed):
		writeError(w, http.StatusBadRequest, codeBadRequest, err.Error())
	case errors.Is(err, project.ErrProjectNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, err.Error())
	case errors.Is(err, coderunner.ErrRunNotFound):
		writeError(w, http.StatusNotFound, codeNotFound, err.Error())
	case errors.Is(err, coderunner.ErrClaudeUnavailable):
		// Actionable and the operator's to fix (SD-6), so it is worth echoing.
		writeError(w, http.StatusServiceUnavailable, codeInternal, err.Error())
	default:
		slog.Error("request failed", "path", r.URL.Path, "error", err)
		writeError(w, http.StatusInternalServerError, codeInternal, "internal error")
	}
}

// guardLoopback rejects any peer that is not on this machine.
//
// Defence in depth: the listener already binds cfg.DaemonHost, which is the
// constant 127.0.0.1. This is the second lock, so that a future change to how
// the address is chosen cannot silently turn the coding-task runner into a
// network service.
func (s *Server) guardLoopback(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			slog.Warn("rejected non-loopback request", "remote", r.RemoteAddr, "path", r.URL.Path)
			writeError(w, http.StatusForbidden, codeForbidden, "this daemon serves loopback clients only")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bearerSubprotocol is how a browser presents the token on a WebSocket
// upgrade: `new WebSocket(url, ["mimir.bearer." + token])`.
//
// The native WebSocket API cannot set an Authorization header, and the usual
// workaround — a query parameter — is wrong for a credential that starts
// coding sessions: URLs end up in access logs, history, and referrers. The
// subprotocol list is a request header, so it does not.
const bearerSubprotocol = "mimir.bearer."

// presentedToken extracts the token a request is offering, from either the
// Authorization header (every normal client) or the WebSocket subprotocol list
// (a browser). It does not look at the query string, deliberately.
func presentedToken(r *http.Request) string {
	const prefix = "Bearer "
	if header := r.Header.Get("Authorization"); len(header) > len(prefix) && header[:len(prefix)] == prefix {
		return header[len(prefix):]
	}
	for _, proto := range requestedSubprotocols(r) {
		if strings.HasPrefix(proto, bearerSubprotocol) {
			return strings.TrimPrefix(proto, bearerSubprotocol)
		}
	}
	return ""
}

func requestedSubprotocols(r *http.Request) []string {
	raw := r.Header.Values("Sec-WebSocket-Protocol")
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		for _, proto := range strings.Split(value, ",") {
			if proto = strings.TrimSpace(proto); proto != "" {
				out = append(out, proto)
			}
		}
	}
	return out
}

// negotiableSubprotocols is what the upgrade may echo back. A browser's
// WebSocket rejects a handshake whose response does not name one of the
// protocols it asked for, so the token-bearing one has to be accepted rather
// than ignored.
func negotiableSubprotocols(r *http.Request) []string {
	for _, proto := range requestedSubprotocols(r) {
		if strings.HasPrefix(proto, bearerSubprotocol) {
			return []string{proto}
		}
	}
	return nil
}

// requireToken enforces the per-launch bearer token the parent process minted.
func (s *Server) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented := presentedToken(r)

		// An empty configured token can never match — ValidateDaemon already
		// refuses to start in that state, and this keeps it true even if a
		// Server were constructed directly.
		if s.cfg.DaemonAuthToken == "" ||
			subtle.ConstantTimeCompare([]byte(presented), []byte(s.cfg.DaemonAuthToken)) != 1 {
			slog.Warn("rejected unauthenticated request", "path", r.URL.Path)
			writeError(w, http.StatusUnauthorized, codeUnauthorized, "a valid bearer token is required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// limitBody caps request bodies. A prompt is text; nothing here has a reason
// to be large, and the daemon should not be a memory sink for a bug upstream.
func limitBody(cfg config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, cfg.DaemonMaxRequestBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// statusRecorder captures the status for the access log without buffering the
// body — /mcp streams, and must not be held up by logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer, so
// streaming handlers (the MCP SSE transport) can still flush.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		slog.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds())
	})
}

// recoverPanics keeps one bad handler from taking the daemon down. The same
// posture SD-4 requires at the MCP tool edge, applied at the HTTP edge.
func recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic in handler", "panic", rec, "path", r.URL.Path,
					"stack", string(debug.Stack()))
				writeError(w, http.StatusInternalServerError, codeInternal, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
