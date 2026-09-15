package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

var (
	// ErrIsolationViolation is returned when a response is not properly refined,
	// lacks the required provenance metadata, or contains raw content signatures.
	ErrIsolationViolation = errors.New("mcp: response not refined / contains raw content")

	// ErrResponseTooLarge is returned when a response exceeds its tool's size budget.
	ErrResponseTooLarge = errors.New("mcp: response exceeds tool size budget")

	// ErrSkillUnavailable is returned when a tool declares a skill it cannot
	// work without and that skill's body could not be loaded. It fails the
	// call rather than degrading it, which is what makes the skill a contract
	// instead of a suggestion: an answer produced without the instructions it
	// was supposed to follow is worse than no answer, because nothing
	// downstream can tell the difference.
	ErrSkillUnavailable = errors.New("mcp: required skill could not be loaded")
)

// SkilledTool is implemented by tools that cannot do their job without a
// skill. Optional: a tool that does not implement it is unaffected.
type SkilledTool interface {
	Skills() []string
}

// SkillBudgeted is implemented by tools that carry a skill, to declare how
// much room its body may take. The allowance is *added* to the tool's own
// budget rather than carved out of it — a skill must not silently shrink the
// answer it was attached to — but it is still bounded, so a skill somebody
// grew by a page fails here rather than at the model's context window.
type SkillBudgeted interface {
	SkillBudgetTokens() int
}

// SkillSource is the narrow half of skills.Store this package needs.
type SkillSource interface {
	Body(id string) (body, version string)
}

// skillAttachment is what rides along on a response.
type skillAttachment struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	// Body is present the first time this process sends a skill, and again
	// whenever the operator has edited it since. After that only the version
	// travels: the consumer read the instructions once, and repeating them on
	// every tool call would spend the budget the instructions are about.
	Body string `json:"body,omitempty"`
}

// skillGate remembers what this process has already told the consumer.
//
// Per process rather than per session because that is the honest unit here:
// mimir-mcp is spawned per Claude Code session and lives as long as it, and
// the daemon's /mcp has no session identity to key on. Getting it wrong in
// this direction costs a repeat; the other direction would silently drop the
// instructions on a fresh consumer.
type skillGate struct {
	src  SkillSource
	mu   sync.Mutex
	sent map[string]string
}

func newSkillGate(src SkillSource) *skillGate {
	if src == nil {
		return nil
	}
	return &skillGate{src: src, sent: map[string]string{}}
}

// resolve loads every skill a tool declared. A skill with an empty body is a
// refusal, not an omission.
func (g *skillGate) resolve(ids []string) ([]skillAttachment, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if g == nil {
		// A tool declared a skill and this process was wired with no source
		// for one. Failing closed is the point: the alternative is a daemon
		// that quietly stops enforcing the mandate because of a wiring bug.
		return nil, fmt.Errorf("%w: no skill source configured", ErrSkillUnavailable)
	}

	out := make([]skillAttachment, 0, len(ids))
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, id := range ids {
		body, version := g.src.Body(id)
		if strings.TrimSpace(body) == "" {
			return nil, fmt.Errorf("%w: %s", ErrSkillUnavailable, id)
		}
		att := skillAttachment{ID: id, Version: version}
		if g.sent[id] != version {
			att.Body = body
			g.sent[id] = version
		}
		out = append(out, att)
	}
	return out, nil
}

// RefinedResponse is implemented by tools that return refined page content.
type RefinedResponse interface {
	IsRefined() bool
	SizeBudgetTokens() int
}

// MetadataResponse is implemented by tools that return metadata only.
type MetadataResponse interface {
	MetadataOnly() bool
	SizeBudgetTokens() int
}

// finalizeResponse is the single fail-closed choke-point (SD-2 / SD-7) for a
// process with no skill source. Kept as the plain two-argument form because it
// is what the isolation tests exercise, and those assertions are about
// refinement and size, not about skills.
func finalizeResponse(tool Tool, v any) (any, error) {
	return finalizeWithSkills(tool, v, nil)
}

// finalizeWithSkills is the choke-point proper. Every successful tool result
// passes through here on its way to the MCP transport, and three things have
// to be true at once: the payload is refined or metadata-only, every skill the
// tool declared actually loaded, and the whole thing — attachment included —
// fits the budget.
func finalizeWithSkills(tool Tool, v any, gate *skillGate) (any, error) {
	name := tool.Name()

	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	budget := 0
	isMetadata := false
	isRefined := false

	if mr, ok := v.(MetadataResponse); ok {
		isMetadata = mr.MetadataOnly()
		budget = mr.SizeBudgetTokens()
	} else if rr, ok := v.(RefinedResponse); ok {
		isRefined = rr.IsRefined()
		budget = rr.SizeBudgetTokens()
	}

	if !isMetadata && !isRefined {
		return nil, fmt.Errorf("%w (tool %q)", ErrIsolationViolation, name)
	}

	// The skill gate runs before the size check, not after, so the attachment
	// is measured rather than smuggled past the ceiling.
	var attachments []skillAttachment
	if st, ok := tool.(SkilledTool); ok {
		var err error
		if attachments, err = gate.resolve(st.Skills()); err != nil {
			return nil, fmt.Errorf("%w (tool %q)", err, name)
		}
	}
	if len(attachments) > 0 {
		var err error
		if v, b, err = attach(v, b, attachments); err != nil {
			return nil, err
		}
		// The allowance is added to the tool's budget, never taken out of it.
		if sb, ok := tool.(SkillBudgeted); ok && budget > 0 {
			budget += sb.SkillBudgetTokens()
		}
	}

	// Estimate token size as chars / 4
	estimatedTokens := len(b) / 4
	if budget > 0 && estimatedTokens > budget {
		return nil, fmt.Errorf("%w: tool %q produced ~%d tokens, budget %d", ErrResponseTooLarge, name, estimatedTokens, budget)
	}

	// Scan for raw HTML / Script signatures
	strData := string(b)
	if strings.Contains(strData, "<html") || strings.Contains(strData, "\\u003chtml") ||
		strings.Contains(strData, "<script") || strings.Contains(strData, "\\u003cscript") ||
		containsLongBase64(strData) {
		return nil, fmt.Errorf("%w (tool %q)", ErrIsolationViolation, name)
	}

	return v, nil
}

// attach puts the skill beside the response rather than inside it. A tool
// response is an object in every case this codebase has, so the attachment
// becomes a sibling field; the "result" wrapper is the honest fallback for a
// shape that is not an object, and no shipped tool takes it.
func attach(v any, marshalled []byte, atts []skillAttachment) (any, []byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(marshalled, &obj); err != nil || obj == nil {
		wrapped := map[string]any{"result": v, "skill": atts}
		b, mErr := json.Marshal(wrapped)
		if mErr != nil {
			return nil, nil, mErr
		}
		return wrapped, b, nil
	}

	raw, err := json.Marshal(atts)
	if err != nil {
		return nil, nil, err
	}
	obj["skill"] = raw
	b, err := json.Marshal(obj)
	if err != nil {
		return nil, nil, err
	}
	return obj, b, nil
}

func containsLongBase64(s string) bool {
	// A simple heuristic for unrefined image payloads: looking for base64 blocks
	// commonly found in raw data URIs. E.g. "data:image/png;base64,iVBORw0K..."
	// 500 characters of unbroken base64-like characters without spaces.
	// Since json marshaling keeps strings intact, we can just look for data:image
	if strings.Contains(s, "data:image/") && strings.Contains(s, ";base64,") {
		return true
	}
	return false
}
