package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/logrenant/goat-mcp/internal/coderunner"
	"github.com/logrenant/goat-mcp/internal/events"
	"github.com/logrenant/goat-mcp/internal/project"
	"github.com/logrenant/goat-mcp/internal/store"
)

// startRun registers the project and starts a coding task through the HTTP
// API, returning the run id — the same path the desktop app takes.
func startRun(t *testing.T, l *live) string {
	t.Helper()

	_, body := l.request(t, http.MethodPost, "/projects", `{"path":`+quote(l.workdir)+`}`)
	var proj project.Project
	if err := json.Unmarshal(body, &proj); err != nil {
		t.Fatalf("decoding project: %v", err)
	}

	_, body = l.request(t, http.MethodPost, "/coding-tasks",
		`{"project_id":`+quote(proj.ID)+`,"prompt":"add a test"}`)
	var run struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &run); err != nil {
		t.Fatalf("decoding run: %v", err)
	}
	if run.ID == "" {
		t.Fatal("no run id returned")
	}
	return run.ID
}

// dialWS opens the run socket. useSubprotocol picks the browser-style auth
// (the token in Sec-WebSocket-Protocol) over the header form.
func dialWS(t *testing.T, ctx context.Context, l *live, runID string, useSubprotocol bool) (*websocket.Conn, *http.Response, error) {
	t.Helper()

	opts := &websocket.DialOptions{HTTPClient: l.client}
	if useSubprotocol {
		opts.Subprotocols = []string{bearerSubprotocol + testToken}
	} else {
		opts.HTTPHeader = http.Header{"Authorization": []string{"Bearer " + testToken}}
	}
	url := "ws" + strings.TrimPrefix(l.url, "http") + "/ws/runs/" + runID
	return websocket.Dial(ctx, url, opts) //nolint:bodyclose // closed by the caller via the conn
}

// closeResp releases a dial's HTTP response. After a successful upgrade the
// connection is hijacked and Body is nil, so both halves need checking.
func closeResp(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
}

// drain reads frames until the server closes the socket, and returns what it
// received. A close that is not a normal closure fails the test: the socket
// ending badly is exactly the failure this endpoint must not have.
func drain(t *testing.T, ctx context.Context, conn *websocket.Conn) []events.Event {
	t.Helper()

	var got []events.Event
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				return got
			}
			t.Fatalf("reading frame %d: %v", len(got)+1, err)
		}
		var ev events.Event
		if err := json.Unmarshal(data, &ev); err != nil {
			t.Fatalf("frame %d is not an Event: %v (%s)", len(got)+1, err, data)
		}
		got = append(got, ev)
	}
}

// assertOrdered checks the one invariant a watcher depends on: every event
// exactly once, in order, starting at the beginning of the run.
func assertOrdered(t *testing.T, got []events.Event) {
	t.Helper()

	if len(got) == 0 {
		t.Fatal("no events received")
	}
	if got[0].Seq != 1 {
		t.Errorf("first event has Seq %d — a watcher must see the run from its beginning", got[0].Seq)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Seq <= got[i-1].Seq {
			t.Fatalf("Seq went %d → %d at index %d: events are duplicated or out of order",
				got[i-1].Seq, got[i].Seq, i)
		}
	}
	if last := got[len(got)-1]; !last.Terminal() {
		t.Errorf("stream ended on %s, not a terminal event", last.Kind)
	}
}

// --- auth ------------------------------------------------------------------

func TestWS_RejectsAnUnauthenticatedUpgrade(t *testing.T) {
	l := newLive(t, writeFakeClaude(t, ""))
	runID := startRun(t, l)
	l.runner.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	url := "ws" + strings.TrimPrefix(l.url, "http") + "/ws/runs/" + runID
	conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: l.client})
	if err == nil {
		_ = conn.CloseNow()
		t.Fatal("an unauthenticated client got a socket")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status: got %v, want 401", resp)
	}
	closeResp(resp)
}

// A browser cannot set an Authorization header, so the token rides the
// subprotocol list. The handshake has to echo it back or the browser's
// WebSocket rejects the connection.
func TestWS_AuthenticatesViaSubprotocolAndEchoesIt(t *testing.T) {
	l := newLive(t, writeFakeClaude(t, ""))
	runID := startRun(t, l)
	l.runner.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, resp, err := dialWS(t, ctx, l, runID, true)
	if err != nil {
		t.Fatalf("dial with the subprotocol token: %v", err)
	}
	closeResp(resp)
	defer func() { _ = conn.CloseNow() }()

	if got, want := conn.Subprotocol(), bearerSubprotocol+testToken; got != want {
		t.Errorf("negotiated subprotocol: got %q, want %q", got, want)
	}
	drain(t, ctx, conn)
}

func TestWS_UnknownRunIs404BeforeTheUpgrade(t *testing.T) {
	l := newLive(t, writeFakeClaude(t, ""))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, resp, err := dialWS(t, ctx, l, "no-such-run", false)
	if err == nil {
		_ = conn.CloseNow()
		t.Fatal("got a socket for a run that does not exist")
	}
	if resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: got %v, want 404 — the id must fail while there is still a status to send", resp)
	}
	closeResp(resp)
}

// --- streaming -------------------------------------------------------------

func TestWS_StreamsALiveRunThroughToCompletion(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "release")
	l := newLive(t, writeFakeClaude(t, gate))
	runID := startRun(t, l)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Connected while the fake CLI is still blocked, so nothing below can pass
	// by racing a run that already finished.
	conn, resp, err := dialWS(t, ctx, l, runID, false)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	closeResp(resp)
	defer func() { _ = conn.CloseNow() }()

	if err := os.WriteFile(gate, []byte("go"), 0o600); err != nil {
		t.Fatalf("releasing the fake CLI: %v", err)
	}

	got := drain(t, ctx, conn)
	assertOrdered(t, got)

	kinds := make([]events.Kind, 0, len(got))
	for _, ev := range got {
		kinds = append(kinds, ev.Kind)
	}
	want := []events.Kind{events.KindRunStarted, events.KindTextDelta, events.KindRunCompleted}
	if len(kinds) != len(want) {
		t.Fatalf("kinds: got %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("kinds: got %v, want %v", kinds, want)
		}
	}
}

// Joining halfway through must still show the run from its beginning — that is
// what the transcript replay is for.
func TestWS_JoinMidRunStillSeesTheBeginning(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "release")
	l := newLive(t, writeFakeClaude(t, gate))
	runID := startRun(t, l)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Let the run produce its opening events, then release only after a
	// watcher has attached — so the stream it receives spans both halves.
	waitForTranscript(t, l, runID, 1)

	conn, resp, err := dialWS(t, ctx, l, runID, false)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	closeResp(resp)
	defer func() { _ = conn.CloseNow() }()

	if err := os.WriteFile(gate, []byte("go"), 0o600); err != nil {
		t.Fatalf("releasing the fake CLI: %v", err)
	}

	got := drain(t, ctx, conn)
	assertOrdered(t, got)
	if len(got) < 3 {
		t.Errorf("got %d events, want the whole run (3)", len(got))
	}
}

func TestWS_ReplaysAFinishedRunThenCloses(t *testing.T) {
	l := newLive(t, writeFakeClaude(t, ""))
	runID := startRun(t, l)
	l.runner.Wait() // the run is over, and its bus subscriptions are closed

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, resp, err := dialWS(t, ctx, l, runID, false)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	closeResp(resp)
	defer func() { _ = conn.CloseNow() }()

	got := drain(t, ctx, conn)
	assertOrdered(t, got)
	if len(got) != 3 {
		t.Errorf("got %d events, want the 3 in the transcript", len(got))
	}
}

// A watcher hanging up must be irrelevant to the run: the socket is a window,
// not a dependency.
func TestWS_ClientDisconnectDoesNotAffectTheRun(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "release")
	l := newLive(t, writeFakeClaude(t, gate))
	runID := startRun(t, l)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	conn, resp, err := dialWS(t, ctx, l, runID, false)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	closeResp(resp)
	_ = conn.CloseNow() // hang up mid-run

	if err := os.WriteFile(gate, []byte("go"), 0o600); err != nil {
		t.Fatalf("releasing the fake CLI: %v", err)
	}
	l.runner.Wait()

	run, err := l.runner.Get(context.Background(), runID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if run.Status != "completed" {
		t.Errorf("run status: got %q, want completed — a watcher leaving must not disturb it", run.Status)
	}
}

// A transcript that cannot be read is not fatal to the socket: the run's own
// events still arrive over the bus.
func TestWS_SurvivesAMissingTranscript(t *testing.T) {
	l := newLive(t, writeFakeClaude(t, ""))
	runID := startRun(t, l)
	l.runner.Wait()

	if err := os.Remove(filepath.Join(l.cfg.TranscriptDir, runID+".jsonl")); err != nil {
		t.Fatalf("removing the transcript: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, resp, err := dialWS(t, ctx, l, runID, false)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	closeResp(resp)
	defer func() { _ = conn.CloseNow() }()

	// No events to replay and none coming, so the server closes cleanly rather
	// than leaving the client hanging.
	if got := drain(t, ctx, conn); len(got) != 0 {
		t.Errorf("got %d events with no transcript, want 0", len(got))
	}
}

// waitForTranscript blocks until the run's transcript holds at least n events.
func waitForTranscript(t *testing.T, l *live, runID string, n int) {
	t.Helper()

	path := filepath.Join(l.cfg.TranscriptDir, runID+".jsonl")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			if len(strings.Split(strings.TrimSpace(string(data)), "\n")) >= n &&
				strings.TrimSpace(string(data)) != "" {
				return
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("reading transcript: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("transcript did not reach %d events in time", n)
}

// --- the dropped-event path ------------------------------------------------

// mutableTranscript stands in for the JSONL file as it grows during a run.
type mutableTranscript struct {
	mu   sync.Mutex
	data string
}

func (m *mutableTranscript) set(s string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = s
}

func (m *mutableTranscript) OpenTranscript(context.Context, string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return io.NopCloser(strings.NewReader(m.data)), nil
}

type channelEvents struct{ ch chan events.Event }

func (c channelEvents) Subscribe(string) (<-chan events.Event, func()) { return c.ch, func() {} }

func line(t *testing.T, ev events.Event) string {
	t.Helper()
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	return string(b) + "\n"
}

// The bus is allowed to drop events for a slow subscriber — that is what keeps
// a stalled watcher from stalling the run. The watcher must not see the hole:
// a Seq jump means "go read the transcript", not "skip an event".
func TestWS_FillsAGapTheBusDropped(t *testing.T) {
	first := events.Event{Kind: events.KindRunStarted, RunID: "r1", Seq: 1}
	second := events.Event{Kind: events.KindTextDelta, RunID: "r1", Seq: 2, Text: "the dropped one"}
	third := events.Event{Kind: events.KindRunCompleted, RunID: "r1", Seq: 3}

	transcript := &mutableTranscript{}
	transcript.set(line(t, first)) // at connect time, only the first event exists

	source := channelEvents{ch: make(chan events.Event, 4)}
	runner := &fakeRunner{run: coderunner.Run{ID: "r1", Status: store.RunStatusRunning}}

	srv := httptest.NewServer(New(testConfig(), Deps{
		Runner:      runner,
		Events:      source,
		Transcripts: transcript,
	}).Handler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws/runs/r1",
		&websocket.DialOptions{
			HTTPClient: srv.Client(),
			HTTPHeader: http.Header{"Authorization": []string{"Bearer " + testToken}},
		})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	closeResp(resp)
	defer func() { _ = conn.CloseNow() }()

	// The run wrote all three; the bus delivered only the last. This is exactly
	// the drop the events package documents as permitted.
	transcript.set(line(t, first) + line(t, second) + line(t, third))
	source.ch <- third

	got := drain(t, ctx, conn)
	assertOrdered(t, got)

	if len(got) != 3 {
		t.Fatalf("got %d events, want 3 — the dropped one must be recovered from the transcript", len(got))
	}
	if got[1].Text != "the dropped one" {
		t.Errorf("second event: got %+v, want the recovered one", got[1])
	}
}
