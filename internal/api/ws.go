package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/logrenant/goat-mcp/internal/coderunner"
	"github.com/logrenant/goat-mcp/internal/events"
	"github.com/logrenant/goat-mcp/internal/store"
)

// Socket tuning. Package constants, not config: these are properties of the
// delivery mechanism, not operational values anyone should tune (SD-1) — the
// same call `internal/events` makes for its subscriber buffer.
const (
	// wsPingInterval is how long the socket may sit idle before we check the
	// peer is still there. A run can think for a minute without emitting.
	wsPingInterval = 20 * time.Second

	// wsWriteTimeout bounds one frame. A client that cannot absorb a single
	// event in this long is gone, not slow.
	wsWriteTimeout = 10 * time.Second

	// wsMaxTranscriptLine matches the runner's own scanner ceiling, so a line
	// it could write is a line we can read back.
	wsMaxTranscriptLine = 8 * 1024 * 1024
)

// handleRunStream streams one run's events, live, for as long as it runs.
//
// The ordering here is the whole design, and each step is load-bearing:
//
//  1. Resolve the run *before* upgrading. After the handshake there is no HTTP
//     status left to send, so an unknown id has to fail while it can still be
//     a plain 404.
//  2. Subscribe *before* replaying the transcript. Subscribing second would
//     lose anything published while the replay was in flight.
//  3. Replay the transcript, then follow the bus, skipping anything whose Seq
//     was already sent. The bus is best-effort by contract and the transcript
//     is the complete record; using both, keyed by Seq, is what lets a watcher
//     join a run halfway through and still see it from the beginning.
func (s *Server) handleRunStream(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	if runID == "" {
		writeError(w, http.StatusBadRequest, codeBadRequest, "run id is required")
		return
	}

	run, err := s.deps.Runner.Get(r.Context(), runID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}

	eventCh, unsubscribe := s.deps.Events.Subscribe(runID)
	defer unsubscribe()

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols: negotiableSubprotocols(r),
		// The library's origin check defends a server whose auth is ambient —
		// a cookie the browser attaches for you. Ours is not: every upgrade
		// already carried a bearer token a foreign page has no way to obtain,
		// checked before this handler ran. Meanwhile the legitimate client is
		// a Tauri WebView whose origin is not this host, so the check would
		// reject exactly the one caller we are building for.
		InsecureSkipVerify: true,
	})
	if err != nil {
		// Accept has already written a response.
		slog.Warn("websocket upgrade failed", "run_id", runID, "error", err)
		return
	}
	defer func() { _ = conn.CloseNow() }()

	// The client sends nothing; reading exists only to notice it went away.
	// CloseRead gives a context that is cancelled when it does.
	ctx := conn.CloseRead(r.Context())

	last, terminal, err := s.replayFrom(ctx, conn, runID, 0)
	if err != nil {
		// A run can legitimately have no transcript yet (started this
		// millisecond) or never (failed before the file was opened). Neither is
		// fatal to the socket: fall through and let the bus decide.
		if !errors.Is(err, coderunner.ErrTranscriptUnavailable) {
			slog.Warn("replaying transcript", "run_id", runID, "error", err)
		}
	}
	if terminal {
		closeNormally(conn, "run finished")
		return
	}
	// A run that was already over when we looked, and whose transcript did not
	// contain a terminal event, will never publish one either — nothing is
	// listening on the other end of that bus. Close rather than hang.
	if run.Status != store.RunStatusRunning {
		closeNormally(conn, "run finished")
		return
	}

	ping := time.NewTicker(wsPingInterval)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ping.C:
			pingCtx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}

		case ev, ok := <-eventCh:
			if !ok {
				// The bus closed the run's subscriptions: it is over.
				closeNormally(conn, "run finished")
				return
			}
			if ev.Seq <= last {
				continue // already sent, from the replay or a gap fill
			}
			// A jump means the bus dropped events for this subscriber, which it
			// is explicitly allowed to do. The transcript has them, so go and
			// fetch what was missed rather than showing the watcher a hole.
			if ev.Seq > last+1 {
				filled, filledTerminal, err := s.replayFrom(ctx, conn, runID, last)
				last = max(last, filled)
				if err != nil && !errors.Is(err, coderunner.ErrTranscriptUnavailable) {
					slog.Warn("filling a dropped-event gap", "run_id", runID, "error", err)
				}
				// The fill can carry the end of the run with it — the dropped
				// range may include the terminal event, or reach past this one.
				if filledTerminal {
					closeNormally(conn, "run finished")
					return
				}
			}
			if ev.Seq <= last {
				continue
			}
			if err := writeEvent(ctx, conn, ev); err != nil {
				return
			}
			last = ev.Seq
			if ev.Terminal() {
				closeNormally(conn, "run finished")
				return
			}
		}
	}
}

// replayFrom writes every transcript event with Seq greater than after, in
// order, and reports the highest Seq written and whether one of them ended the
// run.
//
// It serves double duty: the initial catch-up, and filling a gap the bus
// dropped mid-stream. Both are the same operation — "send what this client has
// not seen" — so they are the same code.
func (s *Server) replayFrom(ctx context.Context, conn *websocket.Conn,
	runID string, after int64) (last int64, terminal bool, err error) {

	last = after

	if s.deps.Transcripts == nil {
		return last, false, nil
	}
	rc, err := s.deps.Transcripts.OpenTranscript(ctx, runID)
	if err != nil {
		return last, false, err
	}
	defer func() { _ = rc.Close() }()

	scanner := bufio.NewScanner(rc)
	scanner.Buffer(make([]byte, 0, 64*1024), wsMaxTranscriptLine)

	for scanner.Scan() {
		var ev events.Event
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			// A half-written final line is normal while a run is in flight.
			continue
		}
		if ev.Seq <= last {
			continue
		}
		if err := writeEvent(ctx, conn, ev); err != nil {
			return last, terminal, err
		}
		last = ev.Seq
		if ev.Terminal() {
			terminal = true
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return last, terminal, err
	}
	return last, terminal, nil
}

func writeEvent(ctx context.Context, conn *websocket.Conn, ev events.Event) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageText, data)
}

func closeNormally(conn *websocket.Conn, reason string) {
	_ = conn.Close(websocket.StatusNormalClosure, reason)
}
