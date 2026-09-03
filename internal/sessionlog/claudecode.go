package sessionlog

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"time"
)

// maxLineBytes matches the coding-task runner's own scanner ceiling. A single
// transcript line carries a whole tool result and can be very large; a line
// past this is skipped rather than allowed to abort the scan.
const maxLineBytes = 8 << 20

// ccRecord is the subset of a Claude Code transcript line this package reads.
// Every field is optional on purpose: the format is not ours, and a record that
// dropped a field must parse into a less useful episode, not a failed ingest.
type ccRecord struct {
	Type        string `json:"type"`
	UUID        string `json:"uuid"`
	SessionID   string `json:"sessionId"`
	Timestamp   string `json:"timestamp"`
	CWD         string `json:"cwd"`
	GitBranch   string `json:"gitBranch"`
	IsSidechain bool   `json:"isSidechain"`
	IsMeta      bool   `json:"isMeta"`
	PromptSrc   string `json:"promptSource"`
	Origin      struct {
		Kind string `json:"kind"`
	} `json:"origin"`
	Message ccMessage `json:"message"`
}

type ccMessage struct {
	ID      string          `json:"id"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	Usage   struct {
		InputTokens         int `json:"input_tokens"`
		OutputTokens        int `json:"output_tokens"`
		CacheCreationTokens int `json:"cache_creation_input_tokens"`
		CacheReadTokens     int `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

// A tool_use block carries its own id in "id"; the tool_result that reports on
// it refers back with "tool_use_id". Both are read here so a call can be paired
// with its outcome.
type ccBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Name      string          `json:"name"`
	ID        string          `json:"id"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
}

// Source identifies one transcript file.
type Source struct {
	Kind        SourceKind
	Path        string
	ProjectPath string
}

// ParseClaudeCode reads a Claude Code transcript and returns the episodes it
// contains, plus the offset an incremental re-read should resume from.
//
// startOffset is where r is already positioned, so returned offsets are
// absolute within the file. The resume offset points at the *last* prompt in
// the stream, not at the end: that episode has no following prompt to close it
// and may still be growing, so the next pass re-reads and refreshes it. Episode
// keys are content-derived, which is what makes re-reading an update instead of
// a duplicate.
func ParseClaudeCode(r io.Reader, src Source, startOffset int64) ([]Episode, int64, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineBytes)

	var (
		episodes  []Episode
		cur       *ccBuilder
		offset    = startOffset
		lastStart = startOffset
	)

	flush := func() {
		if cur == nil {
			return
		}
		episodes = append(episodes, cur.build(src))
		cur = nil
	}

	for scanner.Scan() {
		line := scanner.Bytes()
		lineStart := offset
		offset += int64(len(line)) + 1

		var rec ccRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue // Not our business to repair; skip and keep going.
		}
		if rec.IsSidechain {
			// Subagent traffic. It belongs to the parent episode's tool calls,
			// which the parent's own Task tool_use already records, so counting
			// it again would double the noise for none of the signal.
			continue
		}

		switch rec.Type {
		case "user":
			if prompt, ok := humanPrompt(rec); ok {
				flush()
				lastStart = lineStart
				cur = newCCBuilder(rec, prompt, src.ProjectPath)
				continue
			}
			if cur != nil {
				cur.addToolResults(rec)
			}
		case "assistant":
			if cur != nil {
				cur.addAssistant(rec)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		// Partial progress is still progress: return what parsed and let the
		// caller record the offset it reached.
		flush()
		return episodes, lastStart, err
	}

	if cur != nil {
		cur.open = true
		flush()
	} else {
		// Nothing open, so there is nothing to re-read next time.
		lastStart = offset
	}

	return episodes, lastStart, nil
}

// humanPrompt reports whether a user record is a person asking for something,
// and returns the text if so.
//
// The distinction matters more than it looks: a transcript's user records are
// mostly tool results, and the rest include slash-command echoes, hook output
// and background task notifications. Treating any of those as a prompt would
// cut episodes at meaningless boundaries and fill the memory with entries
// nobody asked for.
func humanPrompt(rec ccRecord) (string, bool) {
	if rec.IsMeta || rec.PromptSrc == "system" || rec.Origin.Kind == "task-notification" {
		return "", false
	}

	text, ok := plainText(rec.Message.Content)
	if !ok {
		return "", false
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}

	if IsWrapperEcho(text) {
		return "", false
	}
	return text, true
}

// IsWrapperEcho reports whether the text is Claude Code plumbing echoed into
// the transcript as ordinary user text.
//
// Exported because it is also the test for a row stored before this filter
// existed: a memory that learned to recognise noise should be able to drop the
// noise it already kept.
//
// Slash commands, hook output and local-command results all arrive this way,
// wrapped in an XML-ish tag. The tag names are not a fixed list — real
// transcripts carry <command-name>, <command-message>, <local-command-stdout>,
// <local-command-caveat> and more — so matching a hardcoded set means the next
// wrapper Claude Code adds silently becomes an episode titled with its own
// plumbing. Matching the shape instead degrades correctly.
func IsWrapperEcho(text string) bool {
	if !strings.HasPrefix(text, "<") {
		return false
	}
	end := strings.IndexAny(text, "> \n")
	if end < 0 || text[end] != '>' {
		return false
	}
	tag := strings.ToLower(text[1:end])
	for _, hint := range []string{"command", "hook", "caveat", "notification", "reminder", "stdout"} {
		if strings.Contains(tag, hint) {
			return true
		}
	}
	return false
}

// plainText extracts text from a content field that is either a bare string or
// an array of blocks. It reports false when the content carries anything other
// than text, which is how a tool_result carrier is told from a prompt.
func plainText(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, true
	}

	var blocks []ccBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", false
	}
	var parts []string
	for _, b := range blocks {
		if b.Type != "text" {
			return "", false
		}
		parts = append(parts, b.Text)
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, "\n"), true
}

// ccBuilder accumulates one episode as records stream past.
type ccBuilder struct {
	rec         ccRecord
	prompt      string
	projectPath string
	open        bool

	startedAt time.Time
	endedAt   time.Time

	assistantText string
	calls         []ToolCall
	callIndex     map[string]int

	files      []string
	filesSeen  map[string]struct{}
	cmds       []string
	cmdsSeen   map[string]struct{}
	countedMsg map[string]struct{}

	inputTokens  int
	outputTokens int
}

func newCCBuilder(rec ccRecord, prompt, projectPath string) *ccBuilder {
	at := parseTime(rec.Timestamp)
	if projectPath == "" {
		projectPath = rec.CWD
	}
	return &ccBuilder{
		rec:         rec,
		prompt:      prompt,
		projectPath: projectPath,
		startedAt:   at,
		endedAt:     at,
		callIndex:   map[string]int{},
		filesSeen:   map[string]struct{}{},
		cmdsSeen:    map[string]struct{}{},
		countedMsg:  map[string]struct{}{},
	}
}

func (b *ccBuilder) addAssistant(rec ccRecord) {
	if at := parseTime(rec.Timestamp); !at.IsZero() {
		b.endedAt = at
	}

	// One assistant message is written out once per content block, repeating
	// its usage each time. Counting per line would multiply an episode's token
	// total by its block count.
	//
	// Cache reads are deliberately excluded. They re-report the whole live
	// context on every turn, so summing them measures how long the conversation
	// was rather than how much this episode added — on real transcripts that
	// inflates a single episode past nine million tokens, which is a number no
	// consumer of this memory could use for anything.
	if id := rec.Message.ID; id != "" {
		if _, done := b.countedMsg[id]; !done {
			b.countedMsg[id] = struct{}{}
			u := rec.Message.Usage
			b.inputTokens += u.InputTokens + u.CacheCreationTokens
			b.outputTokens += u.OutputTokens
		}
	}

	var blocks []ccBlock
	if err := json.Unmarshal(rec.Message.Content, &blocks); err != nil {
		return
	}
	for _, blk := range blocks {
		switch blk.Type {
		case "text":
			if t := strings.TrimSpace(blk.Text); t != "" {
				b.assistantText = t // Keep the latest; it is the conclusion.
			}
		case "tool_use":
			b.addToolUse(blk)
		}
	}
}

func (b *ccBuilder) addToolUse(blk ccBlock) {
	if len(b.calls) >= MaxToolCalls {
		return
	}
	target := toolTarget(blk.Name, blk.Input)

	switch blk.Name {
	case "Read", "Edit", "Write", "NotebookEdit", "MultiEdit":
		if f, ok := normalizeFile(target, b.projectPath); ok {
			b.files = addUnique(b.files, b.filesSeen, f, MaxFiles)
		}
	case "Bash":
		b.cmds = addUnique(b.cmds, b.cmdsSeen, target, MaxCommands)
	}

	// Optimistic until a tool_result says otherwise: most calls succeed, and
	// the ones that do not are corrected below when their result arrives.
	b.calls = append(b.calls, ToolCall{Name: blk.Name, Target: target, OK: true})
	if blk.ID != "" {
		b.callIndex[blk.ID] = len(b.calls) - 1
	}
}

func (b *ccBuilder) addToolResults(rec ccRecord) {
	if at := parseTime(rec.Timestamp); !at.IsZero() {
		b.endedAt = at
	}
	var blocks []ccBlock
	if err := json.Unmarshal(rec.Message.Content, &blocks); err != nil {
		return
	}
	for _, blk := range blocks {
		if blk.Type != "tool_result" || !blk.IsError {
			continue
		}
		if i, ok := b.callIndex[blk.ToolUseID]; ok {
			b.calls[i].OK = false
		}
	}
}

func (b *ccBuilder) build(src Source) Episode {
	e := Episode{
		Key:           episodeKey(src.Path, b.rec.UUID, b.prompt),
		SourceKind:    src.Kind,
		SourcePath:    src.Path,
		SessionID:     b.rec.SessionID,
		ProjectPath:   b.projectPath,
		GitBranch:     b.rec.GitBranch,
		StartedAt:     b.startedAt,
		EndedAt:       b.endedAt,
		UserPrompt:    b.prompt,
		AssistantText: b.assistantText,
		ToolCalls:     b.calls,
		FilesTouched:  sortedCopy(b.files),
		Commands:      sortedCopy(b.cmds),
		InputTokens:   b.inputTokens,
		OutputTokens:  b.outputTokens,
		Open:          b.open,
	}
	if e.EndedAt.Before(e.StartedAt) {
		e.EndedAt = e.StartedAt
	}
	return e
}

// episodeKey identifies an episode by where it came from rather than by what it
// contains, so re-reading a still-growing episode updates the same row instead
// of creating a second one. The prompt is folded in only as a guard for the
// case where a transcript is rewritten and uuids are reused.
func episodeKey(sourcePath, uuid, prompt string) string {
	h := sha256.New()
	h.Write([]byte(sourcePath))
	h.Write([]byte{0})
	h.Write([]byte(uuid))
	h.Write([]byte{0})
	h.Write([]byte(prompt))
	return hex.EncodeToString(h.Sum(nil))[:32]
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// toolTarget reduces a tool's arguments to the one string worth remembering.
//
// It never returns an argument body. A Bash command line can contain a token or
// a password; its description cannot, and "Run the race detector" is what a
// later search would look for anyway.
func toolTarget(name string, input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var args struct {
		FilePath     string `json:"file_path"`
		Path         string `json:"path"`
		Description  string `json:"description"`
		Pattern      string `json:"pattern"`
		URL          string `json:"url"`
		Query        string `json:"query"`
		Subagent     string `json:"subagent_type"`
		NotebookdPth string `json:"notebook_path"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return ""
	}

	var v string
	switch name {
	case "Bash":
		v = args.Description
	case "Read", "Edit", "Write", "MultiEdit":
		v = firstNonEmpty(args.FilePath, args.Path)
	case "NotebookEdit":
		v = firstNonEmpty(args.NotebookdPth, args.FilePath)
	case "Grep", "Glob":
		v = firstNonEmpty(args.Pattern, args.Path)
	case "WebFetch", "WebSearch":
		v = firstNonEmpty(args.URL, args.Query)
	case "Task", "Agent":
		v = firstNonEmpty(args.Description, args.Subagent)
	default:
		v = firstNonEmpty(args.Description, args.FilePath, args.Path, args.Query)
	}
	return truncate(oneLine(v), MaxTargetChars)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// normalizeFile decides whether a touched path is worth remembering and, if so,
// what to call it.
//
// Order matters here. A path inside the project is project work by definition
// and is kept whatever it looks like — a repository has every right to contain
// a .claude directory or a file under a temp-looking prefix, and a filter that
// ran first would silently erase real work. Only paths outside the project face
// the junk filters, which exist for one thing: the agent's own plumbing —
// spilled tool results, scratchpads, temp dirs — which records how a session
// ran rather than what it changed, and which on real transcripts was the single
// largest source of noise in the file list.
//
// What survives from outside the project is reduced to a basename, because an
// absolute path from an unrelated tree tells a project index nothing it can use.
func normalizeFile(path, projectPath string) (string, bool) {
	if path == "" {
		return "", false
	}

	if projectPath != "" {
		if rel, ok := strings.CutPrefix(path, strings.TrimSuffix(projectPath, "/")+"/"); ok {
			return rel, rel != ""
		}
	}

	for _, junk := range []string{"/.claude/", "/tool-results/", "/scratchpad/"} {
		if strings.Contains(path, junk) {
			return "", false
		}
	}
	for _, prefix := range []string{"/tmp/", "/private/tmp/", "/var/folders/", "/private/var/folders/"} {
		if strings.HasPrefix(path, prefix) {
			return "", false
		}
	}

	if i := strings.LastIndex(path, "/"); i >= 0 && i+1 < len(path) {
		return path[i+1:], true
	}
	return path, true
}
