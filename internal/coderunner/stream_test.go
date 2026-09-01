package coderunner

import (
	"testing"
	"time"

	"github.com/logrenant/goat-mcp/internal/events"
)

var at = time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

// parseAll feeds lines through one state, as the runner does.
func parseAll(s *state, lines ...string) []events.Event {
	var out []events.Event
	for _, l := range lines {
		out = append(out, parseLine([]byte(l), s, at)...)
	}
	return out
}

func kinds(evs []events.Event) []events.Kind {
	out := make([]events.Kind, 0, len(evs))
	for _, e := range evs {
		out = append(out, e.Kind)
	}
	return out
}

func textOf(evs []events.Event, kind events.Kind) string {
	var s string
	for _, e := range evs {
		if e.Kind == kind {
			s += e.Text
		}
	}
	return s
}

func TestParseLine_SystemInitStartsTheRun(t *testing.T) {
	s := newState("run1")
	evs := parseAll(s, `{"type":"system","subtype":"init","session_id":"sess-1","model":"claude-sonnet-5","cwd":"/tmp"}`)

	if len(evs) != 1 || evs[0].Kind != events.KindRunStarted {
		t.Fatalf("want one run.started, got %v", kinds(evs))
	}
	if evs[0].SessionID != "sess-1" || evs[0].Model != "claude-sonnet-5" {
		t.Errorf("session/model not captured: %+v", evs[0])
	}
}

// thinking_tokens is a token estimate, not content — it must not reach a watcher.
func TestParseLine_IgnoresNonInitSystemLines(t *testing.T) {
	s := newState("run1")
	evs := parseAll(s, `{"type":"system","subtype":"thinking_tokens","estimated_tokens":33}`)

	if len(evs) != 0 {
		t.Fatalf("want no events, got %v", kinds(evs))
	}
}

func TestParseLine_UnknownTypeIsIgnored(t *testing.T) {
	s := newState("run1")
	evs := parseAll(s, `{"type":"some_future_event","payload":{"a":1}}`)

	if len(evs) != 0 {
		t.Fatalf("an unknown line type must be skipped, got %v", kinds(evs))
	}
}

// A malformed line must not kill a run that is otherwise producing work.
func TestParseLine_MalformedLineIsSkippedAndStreamContinues(t *testing.T) {
	s := newState("run1")
	evs := parseAll(s,
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"before"}]}}`,
		`{ this is not json`,
		``,
		`   `,
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"beforeafter"}]}}`,
	)

	if got := textOf(evs, events.KindTextDelta); got != "beforeafter" {
		t.Fatalf("text after a malformed line: got %q, want %q", got, "beforeafter")
	}
}

func TestParseLine_TextIsEmittedAsSuffixDeltas(t *testing.T) {
	s := newState("run1")
	evs := parseAll(s,
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"Hello"}]}}`,
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"Hello, world"}]}}`,
	)

	deltas := []string{}
	for _, e := range evs {
		if e.Kind == events.KindTextDelta {
			deltas = append(deltas, e.Text)
		}
	}

	if len(deltas) != 2 || deltas[0] != "Hello" || deltas[1] != ", world" {
		t.Fatalf("deltas must be suffixes only, got %q", deltas)
	}
}

// Re-sending identical content produces nothing: a delta is new text or it is
// not an event.
func TestParseLine_NoDeltaWhenNothingChanged(t *testing.T) {
	s := newState("run1")
	line := `{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"same"}]}}`
	evs := parseAll(s, line, line, line)

	if got := textOf(evs, events.KindTextDelta); got != "same" {
		t.Fatalf("repeated identical content: got %q, want %q", got, "same")
	}
}

// THE goat v1 BUG. Per-run length tracking swallows the start of every message
// after the first; per-message tracking is what makes this pass.
func TestParseLine_SecondMessageDeltasAreNotSwallowed(t *testing.T) {
	s := newState("run1")
	evs := parseAll(s,
		// First message grows to 20 characters.
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"a long first message"}]}}`,
		// Second message starts from zero. With a per-run counter at 20, its
		// first 20 characters would be silently dropped.
		`{"type":"assistant","message":{"id":"m2","content":[{"type":"text","text":"second"}]}}`,
		`{"type":"assistant","message":{"id":"m2","content":[{"type":"text","text":"second message"}]}}`,
	)

	want := "a long first message" + "second" + " message"
	if got := textOf(evs, events.KindTextDelta); got != want {
		t.Fatalf("per-message delta tracking is broken:\n got %q\nwant %q", got, want)
	}
}

func TestParseLine_ThinkingIsTrackedSeparatelyFromText(t *testing.T) {
	s := newState("run1")
	evs := parseAll(s,
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"thinking","thinking":"let me see"}]}}`,
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"answer"}]}}`,
	)

	if got := textOf(evs, events.KindReasoningDelta); got != "let me see" {
		t.Errorf("reasoning: got %q", got)
	}
	if got := textOf(evs, events.KindTextDelta); got != "answer" {
		t.Errorf("text: got %q", got)
	}
}

// The same tool_use block reappears in later lines for the same message; the
// call is announced once.
func TestParseLine_ToolCallEmittedExactlyOncePerBlock(t *testing.T) {
	s := newState("run1")
	line := `{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":"call-1","name":"Read","input":{"file_path":"/x"}}]}}`
	evs := parseAll(s, line, line, line)

	var calls int
	for _, e := range evs {
		if e.Kind == events.KindToolCall {
			calls++
			if e.CallID != "call-1" || e.ToolName != "Read" {
				t.Errorf("unexpected call: %+v", e)
			}
			if e.Risk != events.RiskRead {
				t.Errorf("Read should be risk %q, got %q", events.RiskRead, e.Risk)
			}
			if string(e.Args) != `{"file_path":"/x"}` {
				t.Errorf("args not passed through: %s", e.Args)
			}
		}
	}
	if calls != 1 {
		t.Fatalf("want exactly 1 tool.call, got %d", calls)
	}
}

func TestParseLine_ToolResultResolvesItsToolName(t *testing.T) {
	s := newState("run1")
	evs := parseAll(s,
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":"call-1","name":"Bash","input":{}}]}}`,
		`{"type":"user","message":{"id":"u1","content":[{"type":"tool_result","tool_use_id":"call-1","is_error":false,"content":"ok output"}]}}`,
	)

	var found bool
	for _, e := range evs {
		if e.Kind != events.KindToolResult {
			continue
		}
		found = true
		if e.ToolName != "Bash" {
			t.Errorf("tool name not resolved from the earlier call: %q", e.ToolName)
		}
		if e.OK == nil || !*e.OK {
			t.Errorf("OK: got %v, want true", e.OK)
		}
		if e.Output != "ok output" {
			t.Errorf("Output: got %q", e.Output)
		}
		if e.Risk != events.RiskExec {
			t.Errorf("Bash should be risk %q, got %q", events.RiskExec, e.Risk)
		}
	}
	if !found {
		t.Fatal("no tool.result event")
	}
}

func TestParseLine_ToolResultErrorIsFlagged(t *testing.T) {
	s := newState("run1")
	evs := parseAll(s,
		`{"type":"user","message":{"id":"u1","content":[{"type":"tool_result","tool_use_id":"unknown","is_error":true,"content":"boom"}]}}`,
	)

	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %v", kinds(evs))
	}
	e := evs[0]
	if e.OK == nil || *e.OK {
		t.Errorf("OK: got %v, want false", e.OK)
	}
	// An unknown tool falls back to a placeholder name and the dangerous risk class.
	if e.ToolName != "tool" || e.Risk != events.RiskExec {
		t.Errorf("unknown tool fallback: name=%q risk=%q", e.ToolName, e.Risk)
	}
}

// tool_result content arrives either as a string or as a block list.
func TestParseLine_ToolResultContentBlockList(t *testing.T) {
	s := newState("run1")
	evs := parseAll(s,
		`{"type":"user","message":{"id":"u1","content":[{"type":"tool_result","tool_use_id":"c","content":[{"type":"text","text":"line one"},{"type":"text","text":"line two"}]}]}}`,
	)

	if len(evs) != 1 || evs[0].Output != "line one\nline two" {
		t.Fatalf("block-list content not flattened: %+v", evs)
	}
}

func TestParseLine_ResultCompletesTheRun(t *testing.T) {
	s := newState("run1")
	evs := parseAll(s,
		`{"type":"system","subtype":"init","session_id":"sess-1","model":"claude-sonnet-5"}`,
		`{"is_error":false,"subtype":"success","total_cost_usd":0.0082,"num_turns":3,"duration_ms":6761,"result":"done","session_id":"sess-1","type":"result"}`,
	)

	last := evs[len(evs)-1]
	if last.Kind != events.KindRunCompleted {
		t.Fatalf("want run.completed, got %v", last.Kind)
	}
	if last.CostUSD != 0.0082 || last.NumTurns != 3 || last.DurationMs != 6761 {
		t.Errorf("result metadata not captured: %+v", last)
	}
	if last.SessionID != "sess-1" || last.Model != "claude-sonnet-5" {
		t.Errorf("session/model not carried to the terminal event: %+v", last)
	}
	if !last.Terminal() {
		t.Error("run.completed must be terminal")
	}
}

func TestParseLine_ErrorResultFailsTheRun(t *testing.T) {
	s := newState("run1")
	evs := parseAll(s,
		`{"is_error":true,"subtype":"error_during_execution","type":"result","session_id":"s"}`,
	)

	if len(evs) != 1 || evs[0].Kind != events.KindRunFailed {
		t.Fatalf("want run.failed, got %v", kinds(evs))
	}
	if evs[0].Error == "" {
		t.Error("a failed run must carry an error string")
	}
	if !evs[0].Terminal() {
		t.Error("run.failed must be terminal")
	}
}

func TestParseLine_RateLimitEvent(t *testing.T) {
	s := newState("run1")
	evs := parseAll(s,
		`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","unifiedWindows":{"five_hour":{"utilization":0.44,"resetsAt":1788104400}}}}`,
	)

	if len(evs) != 1 || evs[0].Kind != events.KindRateLimit {
		t.Fatalf("want rate_limit, got %v", kinds(evs))
	}
	if evs[0].Utilization != 0.44 || evs[0].ResetsAt != 1788104400 {
		t.Errorf("rate limit fields: %+v", evs[0])
	}
}

// Seq must be monotonic across the whole run — it is how a watcher that
// dropped events knows to go read the transcript.
func TestParseLine_SeqIsMonotonicAcrossTheRun(t *testing.T) {
	s := newState("run1")
	evs := parseAll(s,
		`{"type":"system","subtype":"init","session_id":"s","model":"m"}`,
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"one"}]}}`,
		`{"type":"assistant","message":{"id":"m1","content":[{"type":"tool_use","id":"c1","name":"Read","input":{}}]}}`,
		`{"type":"user","message":{"id":"u1","content":[{"type":"tool_result","tool_use_id":"c1","content":"x"}]}}`,
		`{"is_error":false,"type":"result","subtype":"success"}`,
	)

	if len(evs) < 5 {
		t.Fatalf("want at least 5 events, got %d: %v", len(evs), kinds(evs))
	}
	for i, e := range evs {
		if e.Seq != int64(i+1) {
			t.Fatalf("event %d has seq %d, want %d", i, e.Seq, i+1)
		}
		if e.RunID != "run1" {
			t.Fatalf("event %d has run id %q", i, e.RunID)
		}
	}
}
