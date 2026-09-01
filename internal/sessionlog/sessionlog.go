// Package sessionlog turns the JSONL transcripts that Claude Code and this
// repo's own coding-task runner leave behind into Episodes: one prompt and the
// work that answered it.
//
// It is deliberately inert. No network, no model, no database, no filesystem
// writes — a reader in, values out. That is what makes the expensive half of
// the memory (internal/memory) testable against fixtures instead of against a
// live transcript directory, and it is why a transcript-format change can only
// ever cost episodes, never correctness elsewhere.
//
// The transcript format is Claude Code's private business and carries no
// compatibility promise. Every parse decision here therefore fails soft: an
// unreadable line, an unknown record type, a field that changed shape — all are
// skipped. A format drift must degrade this package to producing fewer
// episodes, never to producing wrong ones or to failing an ingest.
package sessionlog

import (
	"sort"
	"strings"
	"time"
)

// SourceKind says which producer wrote a transcript. These are wire strings:
// they are persisted on every episode row and surface through the memory tools,
// so renaming one is a breaking change for stored data.
type SourceKind string

const (
	// SourceClaudeCode is an interactive session under ~/.claude/projects.
	SourceClaudeCode SourceKind = "claude_code"
	// SourceGoatRun is one of this daemon's own coding runs.
	SourceGoatRun SourceKind = "goat_run"
)

// Field ceilings. An episode is an index entry, not a copy of the transcript:
// everything here is a pointer back to work that is still on disk, and the
// transcript itself remains the record. These bounds are what keep the store
// small and the recap prompt cheap no matter how long a session ran.
const (
	MaxPromptChars    = 600
	MaxAssistantChars = 1200
	MaxToolCalls      = 60
	MaxFiles          = 40
	MaxCommands       = 20
	MaxTargetChars    = 160
)

// ToolCall is one action an assistant took, reduced to what is worth
// remembering: which tool, what it aimed at, and whether it worked.
//
// Target is a file path, a command's human description, or a search pattern —
// never an argument body. A command line can carry a secret; "Run the test
// suite" cannot, and for recall it is the more useful of the two anyway.
type ToolCall struct {
	Name   string
	Target string
	OK     bool
}

// Episode is one iteration: a prompt, and the assistant chain that answered it.
type Episode struct {
	Key         string
	SourceKind  SourceKind
	SourcePath  string
	SessionID   string
	ProjectPath string
	GitBranch   string

	StartedAt time.Time
	EndedAt   time.Time

	UserPrompt    string
	AssistantText string

	ToolCalls    []ToolCall
	FilesTouched []string
	Commands     []string

	// InputTokens is every token the model processed for this episode, cache
	// reads included. It is a size signal, not a bill: cached and fresh tokens
	// cost differently, and this number does not try to model that.
	InputTokens  int
	OutputTokens int
	CostUSD      float64

	// Open marks the last episode of a transcript, which has no following
	// prompt to close it and may still be growing. Callers use it to hold off
	// on paying for a recap of work that is not finished.
	Open bool
}

// Significance scores how much an episode is worth remembering.
//
// This is the cost filter, and it runs before any model call: a session is
// mostly short exchanges that answered a question and changed nothing, and
// recapping those would spend most of the ingest budget on the least useful
// half of the transcript. A score of zero means the episode is stored and
// searchable but never recapped.
func (e Episode) Significance() int {
	score := 0
	wrote := false
	ran := false
	for _, tc := range e.ToolCalls {
		switch tc.Name {
		case "Edit", "Write", "NotebookEdit", "MultiEdit":
			wrote = true
		case "Bash":
			ran = true
		}
	}
	if wrote {
		score += 5
	}
	if ran {
		score++
	}
	if len(e.ToolCalls) >= 3 {
		score += 2
	}
	if len(e.AssistantText) >= 400 {
		score += 2
	}
	if len(e.FilesTouched) >= 3 {
		score++
	}
	return score
}

// Significant reports whether the episode is worth a recap at all.
func (e Episode) Significant() bool { return e.Significance() > 0 }

// FilesText and CommandsText are the flat projections the full-text index
// stores. They exist because FTS5 can only index real columns.
func (e Episode) FilesText() string    { return strings.Join(e.FilesTouched, " ") }
func (e Episode) CommandsText() string { return strings.Join(e.Commands, " ") }

// FactsInput is the deterministic block's ingredients, named so it can be built
// from a parsed Episode or from a stored row without two copies of the format.
type FactsInput struct {
	Branch      string
	Prompt      string
	Outcome     string
	Files       []string
	Commands    []string
	FailedSteps []string
}

// Facts renders the episode as the deterministic block the recap prompt reads.
func (e Episode) Facts() string {
	return BuildFacts(FactsInput{
		Branch:      e.GitBranch,
		Prompt:      e.UserPrompt,
		Outcome:     e.AssistantText,
		Files:       e.FilesTouched,
		Commands:    e.Commands,
		FailedSteps: e.FailedSteps(),
	})
}

// FailedSteps lists the tools that reported an error, deduplicated and ordered.
// A step that failed is often the most reusable thing in an episode — it is the
// trap a later session would otherwise walk into again — so it is carried
// separately rather than left to be inferred from a count.
func (e Episode) FailedSteps() []string { return failedTools(e.ToolCalls) }

// BuildFacts renders the block. Byte-identical for identical input, because the
// recap is cached against the episode and a prompt that reordered a map on
// every run would make every cached recap look stale.
func BuildFacts(in FactsInput) string {
	var b strings.Builder

	b.WriteString("Request: ")
	b.WriteString(oneLine(in.Prompt))
	b.WriteString("\n")

	if in.Branch != "" {
		b.WriteString("Branch: ")
		b.WriteString(in.Branch)
		b.WriteString("\n")
	}

	if len(in.Files) > 0 {
		b.WriteString("Files: ")
		b.WriteString(strings.Join(in.Files, ", "))
		b.WriteString("\n")
	}

	if len(in.Commands) > 0 {
		b.WriteString("Commands: ")
		b.WriteString(strings.Join(in.Commands, "; "))
		b.WriteString("\n")
	}

	if len(in.FailedSteps) > 0 {
		b.WriteString("Failed steps: ")
		b.WriteString(strings.Join(in.FailedSteps, ", "))
		b.WriteString("\n")
	}

	if in.Outcome != "" {
		b.WriteString("Outcome: ")
		b.WriteString(oneLine(in.Outcome))
		b.WriteString("\n")
	}

	return b.String()
}

func failedTools(calls []ToolCall) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, tc := range calls {
		if tc.OK || tc.Name == "" {
			continue
		}
		label := tc.Name
		if tc.Target != "" {
			label += " (" + tc.Target + ")"
		}
		if _, dup := seen[label]; dup {
			continue
		}
		seen[label] = struct{}{}
		out = append(out, label)
		if len(out) == 5 {
			break
		}
	}
	return out
}

// oneLine flattens text so a multi-line prompt cannot forge structure inside
// the recap prompt's fact block.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.Join(strings.Fields(s), " ")
}

// truncate cuts s to at most max runes, marking that it did.
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max])) + " …"
}

// addUnique appends v to list if it is new and there is room.
func addUnique(list []string, seen map[string]struct{}, v string, max int) []string {
	if v == "" || len(list) >= max {
		return list
	}
	if _, dup := seen[v]; dup {
		return list
	}
	seen[v] = struct{}{}
	return append(list, v)
}

// sortedCopy returns list sorted, so two parses of the same episode produce
// byte-identical facts and index text.
func sortedCopy(list []string) []string {
	out := append([]string(nil), list...)
	sort.Strings(out)
	return out
}
