// Package events defines the typed vocabulary for a live agent run and an
// in-process bus that fans those events out to watchers.
//
// It exists so the event shape is defined once, up front, rather than by
// whichever consumer happened to need it first — the same values are written
// to the on-disk transcript, replayed by the daemon's HTTP API, and (from M3)
// pushed over a WebSocket to the desktop app. The JSON tags are that wire
// format; changing one is a breaking change for every consumer.
package events

import (
	"encoding/json"
	"time"
)

// Kind identifies what happened. Values are wire strings — never renumber or
// rename one without updating every consumer.
type Kind string

const (
	KindRunStarted     Kind = "run.started"
	KindTextDelta      Kind = "text.delta"
	KindReasoningDelta Kind = "reasoning.delta"
	KindToolCall       Kind = "tool.call"
	KindToolResult     Kind = "tool.result"
	KindRateLimit      Kind = "rate_limit"
	KindRunCompleted   Kind = "run.completed"
	KindRunFailed      Kind = "run.failed"

	// KindStderr is a line the CLI wrote to stderr. It is not part of the
	// stream-json protocol and carries no structure — it exists because the
	// reasons a run cannot work ("run `claude login`", a crash trace) are
	// written there, and a watcher that never sees them cannot tell a broken
	// run from a thinking one.
	KindStderr Kind = "stderr"

	// KindRunStopped ends a run the operator cancelled. Distinct from
	// KindRunFailed because nothing is wrong: nobody should be asked to
	// diagnose a stop.
	KindRunStopped Kind = "run.stopped"
)

// Risk classifies what a tool call can do, so a watcher can weight what it
// shows. Unknown tools are treated as the most dangerous class, never the
// least.
const (
	RiskRead  = "read"
	RiskWrite = "write"
	RiskExec  = "exec"
)

// Event is one thing that happened during a run.
//
// It is a single flat struct rather than a per-kind interface because it is
// serialised to JSON on three paths and consumed by a TypeScript client; one
// shape with omitempty fields is far easier to keep honest across that boundary
// than a tagged union.
type Event struct {
	Kind  Kind      `json:"kind"`
	RunID string    `json:"run_id"`
	Seq   int64     `json:"seq"`
	At    time.Time `json:"at"`

	// Text carries the delta for KindTextDelta and KindReasoningDelta — the
	// new suffix only, never the accumulated message.
	Text string `json:"text,omitempty"`

	// Tool call / result.
	CallID   string          `json:"call_id,omitempty"`
	ToolName string          `json:"tool_name,omitempty"`
	Args     json.RawMessage `json:"args,omitempty"`
	Risk     string          `json:"risk,omitempty"`
	OK       *bool           `json:"ok,omitempty"` // pointer: absent ≠ false
	Output   string          `json:"output,omitempty"`

	// Run lifecycle.
	SessionID  string  `json:"session_id,omitempty"`
	Model      string  `json:"model,omitempty"`
	CostUSD    float64 `json:"cost_usd,omitempty"`
	DurationMs int64   `json:"duration_ms,omitempty"`
	NumTurns   int     `json:"num_turns,omitempty"`

	// Rate limit snapshot.
	Utilization float64 `json:"utilization,omitempty"`
	ResetsAt    int64   `json:"resets_at,omitempty"`

	// Error is set only on KindRunFailed.
	Error string `json:"error,omitempty"`
}

// Terminal reports whether this event ends its run. After a terminal event no
// further events are published for that RunID.
func (e Event) Terminal() bool {
	return e.Kind == KindRunCompleted || e.Kind == KindRunFailed ||
		e.Kind == KindRunStopped
}
