package sessionlog

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/logrenant/mimir/internal/events"
)

// RunMeta is what the store knows about a coding run that its event transcript
// does not: the prompt that started it, and which project it ran in.
type RunMeta struct {
	RunID       string
	ProjectPath string
	Prompt      string
	SessionID   string
	Model       string
	CostUSD     float64
}

// ParseMimirRun reads one coding run's event transcript into a single episode.
//
// A run is one prompt by construction, so unlike a Claude Code session there is
// nothing to segment: the whole file is the episode. The events are decoded
// with events.Event itself rather than a private struct, because that type's
// JSON tags are the on-disk format — a second definition here is exactly how
// the two would drift apart.
func ParseMimirRun(r io.Reader, src Source, meta RunMeta) (Episode, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineBytes)

	projectPath := src.ProjectPath
	if projectPath == "" {
		projectPath = meta.ProjectPath
	}

	var (
		text      strings.Builder
		calls     []ToolCall
		callIndex = map[string]int{}
		files     []string
		filesSeen = map[string]struct{}{}
		cmds      []string
		cmdsSeen  = map[string]struct{}{}
		startedAt time.Time
		endedAt   time.Time
		costUSD   = meta.CostUSD
		sessionID = meta.SessionID
	)

	for scanner.Scan() {
		var ev events.Event
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			continue
		}
		if startedAt.IsZero() && !ev.At.IsZero() {
			startedAt = ev.At.UTC()
		}
		if !ev.At.IsZero() {
			endedAt = ev.At.UTC()
		}

		switch ev.Kind {
		case events.KindRunStarted:
			if ev.SessionID != "" {
				sessionID = ev.SessionID
			}
		case events.KindTextDelta:
			// Deltas are suffixes, so concatenation rebuilds the message.
			text.WriteString(ev.Text)
		case events.KindToolCall:
			if len(calls) >= MaxToolCalls {
				continue
			}
			target := toolTarget(ev.ToolName, ev.Args)
			switch ev.ToolName {
			case "Read", "Edit", "Write", "NotebookEdit", "MultiEdit":
				if f, ok := normalizeFile(target, projectPath); ok {
					files = addUnique(files, filesSeen, f, MaxFiles)
				}
			case "Bash":
				cmds = addUnique(cmds, cmdsSeen, target, MaxCommands)
			}
			calls = append(calls, ToolCall{Name: ev.ToolName, Target: target, OK: true})
			if ev.CallID != "" {
				callIndex[ev.CallID] = len(calls) - 1
			}
		case events.KindToolResult:
			// OK is a pointer because absent and false mean different things:
			// only an explicit false is a failure.
			if ev.OK != nil && !*ev.OK {
				if i, ok := callIndex[ev.CallID]; ok {
					calls[i].OK = false
				}
			}
		case events.KindRunCompleted, events.KindRunFailed:
			if ev.CostUSD > 0 {
				costUSD = ev.CostUSD
			}
			if ev.Kind == events.KindRunFailed && ev.Error != "" {
				text.WriteString("\n[run failed] " + ev.Error)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Episode{}, err
	}

	if endedAt.Before(startedAt) {
		endedAt = startedAt
	}

	return Episode{
		// A run id is already unique and stable, so it keys the episode
		// directly; there is no growing-file case here to guard against.
		Key:           "run:" + meta.RunID,
		SourceKind:    SourceMimirRun,
		SourcePath:    src.Path,
		SessionID:     sessionID,
		ProjectPath:   projectPath,
		StartedAt:     startedAt,
		EndedAt:       endedAt,
		UserPrompt:    truncate(meta.Prompt, MaxPromptChars),
		AssistantText: truncate(strings.TrimSpace(text.String()), MaxAssistantChars),
		ToolCalls:     calls,
		FilesTouched:  sortedCopy(files),
		Commands:      sortedCopy(cmds),
		CostUSD:       costUSD,
	}, nil
}
