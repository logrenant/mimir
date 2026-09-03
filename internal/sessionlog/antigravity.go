package sessionlog

import (
	"bufio"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"time"
)

// SourceAntigravity is a session from the `agy` CLI or Antigravity IDE.
//
// Their transcripts are plain JSONL, one record per step, and that is the only
// thing this package reads. The conversation database sitting next to them is
// protobuf blobs with no published schema; parsing it would be a reverse
// engineering exercise that breaks on their next release, and it holds nothing
// the transcript does not.
const SourceAntigravity SourceKind = "antigravity"

// agyRecord is one line of an Antigravity transcript.
type agyRecord struct {
	StepIndex int    `json:"step_index"`
	Source    string `json:"source"`
	Type      string `json:"type"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	Content   string `json:"content"`
}

var (
	// The prompt is fenced; everything after it in a USER_INPUT record is
	// machinery — the local time, open editors, settings changes — that would
	// otherwise become the episode's prompt.
	agyRequestRe = regexp.MustCompile(`(?s)<USER_REQUEST>\n?(.*?)\n?</USER_REQUEST>`)

	// Tool steps identify themselves by what they say they did rather than by a
	// name field, so these are the markers that survive being read literally.
	agyFileURIRe = regexp.MustCompile(`file://(/[^\s"'` + "`" + `)\]]+)`)
	agyExitRe    = regexp.MustCompile(`The command exited with code (\d+)`)
)

// ParseAntigravity reads an Antigravity transcript and returns its episodes,
// plus the offset an incremental re-read should resume from.
//
// It follows the same contract as ParseClaudeCode and for the same reasons: the
// resume offset points at the last prompt rather than the end of the file, so a
// still-growing episode is refreshed in place instead of duplicated, and every
// parse decision fails soft — an unreadable line or an unknown record type
// costs one step, never the pass.
func ParseAntigravity(r io.Reader, src Source, startOffset int64) ([]Episode, int64, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)

	var (
		episodes    []Episode
		cur         *agyBuilder
		offset      = startOffset
		lastPromptA = startOffset
		parseErr    error
	)

	flush := func() {
		if cur != nil {
			episodes = append(episodes, cur.build(src))
			cur = nil
		}
	}

	for sc.Scan() {
		lineStart := offset
		line := sc.Bytes()
		offset += int64(len(line)) + 1

		var rec agyRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}

		switch rec.Type {
		case "USER_INPUT":
			prompt := extractAgyPrompt(rec.Content)
			if prompt == "" {
				continue
			}
			flush()
			lastPromptA = lineStart
			cur = &agyBuilder{
				prompt:    prompt,
				startedAt: parseTime(rec.CreatedAt),
				stepIndex: rec.StepIndex,
			}
		case "PLANNER_RESPONSE":
			if cur != nil {
				cur.addAssistant(rec.Content)
				cur.touch(parseTime(rec.CreatedAt))
			}
		case "GENERIC":
			if cur != nil {
				cur.addStep(rec)
				cur.touch(parseTime(rec.CreatedAt))
			}
		default:
			// CHECKPOINT, SYSTEM_MESSAGE and anything added later: not work the
			// user asked for, and not worth an episode.
		}
	}
	if err := sc.Err(); err != nil {
		parseErr = err
	}

	flush()
	return episodes, lastPromptA, parseErr
}

// extractAgyPrompt pulls the request out of the fenced block, falling back to
// the whole content when the fence is missing — a transcript that changed shape
// should cost fidelity, not the episode.
func extractAgyPrompt(content string) string {
	if m := agyRequestRe.FindStringSubmatch(content); len(m) == 2 {
		return strings.TrimSpace(m[1])
	}
	trimmed := strings.TrimSpace(content)
	if trimmed == "" || strings.HasPrefix(trimmed, "<") {
		return ""
	}
	return trimmed
}

type agyBuilder struct {
	prompt    string
	assistant strings.Builder
	startedAt time.Time
	endedAt   time.Time
	stepIndex int

	tools    []ToolCall
	files    []string
	fileSeen map[string]struct{}
}

func (b *agyBuilder) touch(t time.Time) {
	if t.IsZero() {
		return
	}
	if b.startedAt.IsZero() {
		b.startedAt = t
	}
	if t.After(b.endedAt) {
		b.endedAt = t
	}
}

func (b *agyBuilder) addAssistant(content string) {
	text := strings.TrimSpace(content)
	if text == "" {
		return
	}
	if b.assistant.Len() > 0 {
		b.assistant.WriteString("\n")
	}
	b.assistant.WriteString(text)
}

// addStep reduces one tool step to what is worth remembering. The name is
// inferred from what the step says it did, because these records carry no tool
// name — which means a step whose wording changes becomes "step" rather than
// disappearing.
func (b *agyBuilder) addStep(rec agyRecord) {
	if len(b.tools) >= MaxToolCalls {
		return
	}

	content := rec.Content
	ok := !strings.EqualFold(rec.Status, "ERROR")

	name := "step"
	target := ""
	switch {
	case strings.Contains(content, "Created file "):
		name = "write"
	case strings.Contains(content, "File Path:"):
		name = "read"
	case agyExitRe.MatchString(content):
		name = "command"
		if m := agyExitRe.FindStringSubmatch(content); len(m) == 2 && m[1] != "0" {
			ok = false
		}
	case strings.Contains(content, "results"):
		name = "search"
	}

	// Paths are collected from every step, not just the file ones: a command
	// that touched a file is still a touch, and the URI is the only unambiguous
	// path in this format.
	for _, m := range agyFileURIRe.FindAllStringSubmatch(content, MaxFiles) {
		if len(m) != 2 {
			continue
		}
		p := m[1]
		if b.fileSeen == nil {
			b.fileSeen = map[string]struct{}{}
		}
		if _, dup := b.fileSeen[p]; dup {
			continue
		}
		if len(b.files) >= MaxFiles {
			break
		}
		b.fileSeen[p] = struct{}{}
		b.files = append(b.files, p)
		if target == "" {
			target = p
		}
	}

	b.tools = append(b.tools, ToolCall{Name: name, Target: clip(target, MaxTargetChars), OK: ok})
}

func (b *agyBuilder) build(src Source) Episode {
	if b.endedAt.IsZero() {
		b.endedAt = b.startedAt
	}
	return Episode{
		Key:           episodeKey(src.Path, "agy", b.prompt),
		SourceKind:    SourceAntigravity,
		SourcePath:    src.Path,
		ProjectPath:   src.ProjectPath,
		StartedAt:     b.startedAt,
		EndedAt:       b.endedAt,
		UserPrompt:    b.prompt,
		AssistantText: strings.TrimSpace(b.assistant.String()),
		ToolCalls:     b.tools,
		FilesTouched:  b.files,
	}
}

func clip(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max]))
}
