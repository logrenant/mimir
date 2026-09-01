package coderunner

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/logrenant/mimir/internal/events"
)

// toolRisk classifies what a tool can do. A tool absent from this map is
// treated as RiskExec: risk classification fails dangerous, not safe.
var toolRisk = map[string]string{
	"Read":         events.RiskRead,
	"Glob":         events.RiskRead,
	"Grep":         events.RiskRead,
	"WebFetch":     events.RiskRead,
	"WebSearch":    events.RiskRead,
	"TodoWrite":    events.RiskRead,
	"Write":        events.RiskWrite,
	"Edit":         events.RiskWrite,
	"NotebookEdit": events.RiskWrite,
	"Bash":         events.RiskExec,
	"Task":         events.RiskExec,
}

func riskOf(tool string) string {
	if r, ok := toolRisk[tool]; ok {
		return r
	}
	return events.RiskExec
}

// cliLine is the subset of one `claude --output-format stream-json` line that
// we act on. Captured from the real CLI (v2.1.251); unknown fields are
// ignored, and unknown line types are skipped entirely rather than treated as
// errors, so a CLI that grows a new event type does not break a run.
type cliLine struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`

	SessionID string `json:"session_id"`
	Model     string `json:"model"`

	Message *struct {
		ID      string `json:"id"`
		Content []struct {
			Type string `json:"type"`

			Text     string `json:"text"`
			Thinking string `json:"thinking"`

			// tool_use
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`

			// tool_result
			ToolUseID string          `json:"tool_use_id"`
			IsError   bool            `json:"is_error"`
			Content   json.RawMessage `json:"content"`
		} `json:"content"`
	} `json:"message"`

	// result
	IsError      bool    `json:"is_error"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	DurationMs   int64   `json:"duration_ms"`
	NumTurns     int     `json:"num_turns"`
	Result       string  `json:"result"`

	// rate_limit_event
	RateLimitInfo *struct {
		UnifiedWindows map[string]struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    int64   `json:"resetsAt"`
		} `json:"unifiedWindows"`
	} `json:"rate_limit_info"`
}

// msgProgress is how much of one assistant message has already been emitted.
//
// This is per message id, and that is the whole point. An `assistant` line
// carries the content blocks of ONE message, and the same message id recurs as
// that message grows — so a delta is the suffix beyond what this message has
// already emitted. goat v1 tracked these lengths per *run*
// (packages/core/providers/claude-code.js), which silently corrupts every
// message after the first: the second message's text starts at length 0 again
// while the counter is still at the first message's length, so its opening
// text is swallowed. A coding session with tool calls always has several
// messages, so that bug would fire on essentially every real run.
type msgProgress struct {
	textLen     int
	thinkingLen int
}

// state carries what the parser must remember between lines.
type state struct {
	runID     string
	seq       int64
	msgs      map[string]*msgProgress
	toolNames map[string]string   // tool_use id -> tool name, for tool_result
	emitted   map[string]struct{} // tool_use ids already announced
	sessionID string
	model     string
}

func newState(runID string) *state {
	return &state{
		runID:     runID,
		msgs:      map[string]*msgProgress{},
		toolNames: map[string]string{},
		emitted:   map[string]struct{}{},
	}
}

func (s *state) progress(msgID string) *msgProgress {
	p, ok := s.msgs[msgID]
	if !ok {
		p = &msgProgress{}
		s.msgs[msgID] = p
	}
	return p
}

// next stamps an event with the run id and the next sequence number.
func (s *state) next(kind events.Kind, at time.Time) events.Event {
	s.seq++
	return events.Event{Kind: kind, RunID: s.runID, Seq: s.seq, At: at}
}

// parseLine translates one stream-json line into zero or more events.
//
// Pure and deterministic: same line plus same state produces the same events,
// with no I/O — which is what lets the whole translation be golden-file tested
// without spending a token (SD-8).
//
// An unparseable line yields no events and no error: a malformed line must not
// kill a run that is otherwise producing useful work.
func parseLine(line []byte, s *state, at time.Time) []events.Event {
	trimmed := strings.TrimSpace(string(line))
	if trimmed == "" {
		return nil
	}

	var l cliLine
	if err := json.Unmarshal([]byte(trimmed), &l); err != nil {
		return nil
	}

	switch l.Type {
	case "system":
		if l.Subtype != "init" {
			// thinking_tokens and friends are progress estimates, not content.
			return nil
		}
		s.sessionID = l.SessionID
		s.model = l.Model
		ev := s.next(events.KindRunStarted, at)
		ev.SessionID = l.SessionID
		ev.Model = l.Model
		return []events.Event{ev}

	case "rate_limit_event":
		if l.RateLimitInfo == nil {
			return nil
		}
		w, ok := l.RateLimitInfo.UnifiedWindows["five_hour"]
		if !ok {
			return nil
		}
		ev := s.next(events.KindRateLimit, at)
		ev.Utilization = w.Utilization
		ev.ResetsAt = w.ResetsAt
		return []events.Event{ev}

	case "assistant":
		return parseAssistant(l, s, at)

	case "user":
		return parseUser(l, s, at)

	case "result":
		return parseResult(l, s, at)
	}

	return nil
}

func parseAssistant(l cliLine, s *state, at time.Time) []events.Event {
	if l.Message == nil {
		return nil
	}
	prog := s.progress(l.Message.ID)

	// Blocks of the same kind within one line concatenate before diffing —
	// the message's text is the whole of its text blocks, not the last one.
	var text, thinking strings.Builder
	var out []events.Event

	for _, block := range l.Message.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "thinking":
			thinking.WriteString(block.Thinking)
		case "tool_use":
			if block.ID == "" {
				continue
			}
			s.toolNames[block.ID] = block.Name
			// The same tool_use block reappears in later lines for this
			// message; announce it exactly once.
			if _, seen := s.emitted[block.ID]; seen {
				continue
			}
			s.emitted[block.ID] = struct{}{}

			ev := s.next(events.KindToolCall, at)
			ev.CallID = block.ID
			ev.ToolName = block.Name
			ev.Args = block.Input
			ev.Risk = riskOf(block.Name)
			out = append(out, ev)
		}
	}

	if d := suffixAfter(text.String(), prog.textLen); d != "" {
		ev := s.next(events.KindTextDelta, at)
		ev.Text = d
		out = append(out, ev)
		prog.textLen = len(text.String())
	}
	if d := suffixAfter(thinking.String(), prog.thinkingLen); d != "" {
		ev := s.next(events.KindReasoningDelta, at)
		ev.Text = d
		out = append(out, ev)
		prog.thinkingLen = len(thinking.String())
	}
	return out
}

// suffixAfter returns the part of full beyond the first n bytes, or "" if
// there is nothing new. Guards against a shorter-than-before value, which
// would otherwise slice out of range.
func suffixAfter(full string, n int) string {
	if n >= len(full) {
		return ""
	}
	return full[n:]
}

func parseUser(l cliLine, s *state, at time.Time) []events.Event {
	if l.Message == nil {
		return nil
	}
	var out []events.Event

	for _, block := range l.Message.Content {
		if block.Type != "tool_result" {
			continue
		}
		name := s.toolNames[block.ToolUseID]
		if name == "" {
			name = "tool"
		}
		ok := !block.IsError

		ev := s.next(events.KindToolResult, at)
		ev.CallID = block.ToolUseID
		ev.ToolName = name
		ev.OK = &ok
		ev.Risk = riskOf(name)
		ev.Output = toolResultText(block.Content)
		out = append(out, ev)
	}
	return out
}

func parseResult(l cliLine, s *state, at time.Time) []events.Event {
	kind := events.KindRunCompleted
	if l.IsError {
		kind = events.KindRunFailed
	}

	ev := s.next(kind, at)
	ev.SessionID = firstNonEmpty(l.SessionID, s.sessionID)
	ev.Model = s.model
	ev.CostUSD = l.TotalCostUSD
	ev.DurationMs = l.DurationMs
	ev.NumTurns = l.NumTurns
	ev.Text = l.Result
	if l.IsError {
		ev.Error = firstNonEmpty(l.Subtype, "the claude CLI reported an error")
	}
	return []events.Event{ev}
}

// toolResultText flattens a tool_result's content, which the CLI sends either
// as a plain string or as a list of blocks.
func toolResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}

	var blocks []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		parts := make([]string, 0, len(blocks))
		for _, b := range blocks {
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
