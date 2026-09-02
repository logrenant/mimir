package sessionlog

import (
	"strings"
	"testing"
)

// agyLines builds a transcript out of records, in the shape the real files use:
// one JSON object per line, no wrapper.
func agyLines(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

const (
	agyUserOne = `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-09-02T09:00:00Z","content":"<USER_REQUEST>\nrewrite the store layer\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nThe current local time is: 2026-09-02T12:00:00+03:00.\n</ADDITIONAL_METADATA>"}`
	agyPlanOne = `{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-09-02T09:00:10Z","content":"Rewrote it onto SQLite."}`
	agyReadOne = `{"step_index":2,"source":"MODEL","type":"GENERIC","status":"DONE","created_at":"2026-09-02T09:00:20Z","content":"Created At: x\nFile Path: ` + "`file:///repo/internal/store/brain.go`" + `\nTotal Lines: 80"}`
	agyCmdFail = `{"step_index":3,"source":"MODEL","type":"GENERIC","status":"DONE","created_at":"2026-09-02T09:00:30Z","content":"Created At: x\n\nThe command exited with code 1.\nOutput:\nboom"}`
	agyUserTwo = `{"step_index":4,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","created_at":"2026-09-02T09:05:00Z","content":"<USER_REQUEST>\nnow add tests\n</USER_REQUEST>"}`
	agySystem  = `{"step_index":5,"source":"SYSTEM","type":"SYSTEM_MESSAGE","status":"DONE","created_at":"2026-09-02T09:05:01Z","content":"housekeeping nobody asked for"}`
	agyCheckpt = `{"step_index":6,"source":"SYSTEM","type":"CHECKPOINT","status":"DONE","created_at":"2026-09-02T09:05:02Z","content":"{{ CHECKPOINT 0 }} truncated context"}`
)

func testSource() Source {
	return Source{Kind: SourceAntigravity, Path: "/tmp/transcript.jsonl", ProjectPath: "/repo"}
}

func TestParseAntigravity_SplitsOnUserInput(t *testing.T) {
	in := agyLines(agyUserOne, agyPlanOne, agyReadOne, agyCmdFail, agyUserTwo, agySystem, agyCheckpt)

	eps, _, err := ParseAntigravity(strings.NewReader(in), testSource(), 0)
	if err != nil {
		t.Fatalf("ParseAntigravity: %v", err)
	}
	if len(eps) != 2 {
		t.Fatalf("got %d episodes, want 2", len(eps))
	}

	first := eps[0]
	if first.UserPrompt != "rewrite the store layer" {
		t.Errorf("prompt = %q — the metadata block leaked in", first.UserPrompt)
	}
	if first.AssistantText != "Rewrote it onto SQLite." {
		t.Errorf("assistant = %q", first.AssistantText)
	}
	if first.SourceKind != SourceAntigravity || first.ProjectPath != "/repo" {
		t.Errorf("attribution = %s / %s", first.SourceKind, first.ProjectPath)
	}
	if len(first.FilesTouched) != 1 || first.FilesTouched[0] != "/repo/internal/store/brain.go" {
		t.Errorf("files = %v, want the file:// URI resolved to a path", first.FilesTouched)
	}
	// The end time is the last step's, not the prompt's: an episode that ran
	// for twenty minutes must not look instantaneous.
	if !first.EndedAt.After(first.StartedAt) {
		t.Errorf("ended %v is not after started %v", first.EndedAt, first.StartedAt)
	}

	// A system message and a checkpoint are not work the user asked for.
	if eps[1].AssistantText != "" || len(eps[1].ToolCalls) != 0 {
		t.Errorf("system records were folded into an episode: %+v", eps[1])
	}
}

func TestParseAntigravity_ClassifiesSteps(t *testing.T) {
	in := agyLines(agyUserOne, agyReadOne, agyCmdFail)

	eps, _, err := ParseAntigravity(strings.NewReader(in), testSource(), 0)
	if err != nil || len(eps) != 1 {
		t.Fatalf("ParseAntigravity: %v (%d episodes)", err, len(eps))
	}
	calls := eps[0].ToolCalls
	if len(calls) != 2 {
		t.Fatalf("got %d tool calls, want 2", len(calls))
	}
	if calls[0].Name != "read" || !calls[0].OK {
		t.Errorf("first call = %+v, want a successful read", calls[0])
	}
	// A non-zero exit is the only failure signal these records carry.
	if calls[1].Name != "command" || calls[1].OK {
		t.Errorf("second call = %+v, want a failed command", calls[1])
	}
}

// The resume offset points at the last prompt rather than the end of the file,
// so a still-growing episode is refreshed in place instead of duplicated.
func TestParseAntigravity_ResumesAtTheLastPrompt(t *testing.T) {
	head := agyLines(agyUserOne, agyPlanOne)
	full := head + agyLines(agyUserTwo, agyPlanOne)

	_, resume, err := ParseAntigravity(strings.NewReader(full), testSource(), 0)
	if err != nil {
		t.Fatalf("ParseAntigravity: %v", err)
	}
	if resume != int64(len(head)) {
		t.Fatalf("resume = %d, want the offset of the last prompt (%d)", resume, len(head))
	}

	// Re-reading from there yields that episode again, under the same key.
	eps, _, err := ParseAntigravity(strings.NewReader(full[resume:]), testSource(), resume)
	if err != nil {
		t.Fatalf("resumed parse: %v", err)
	}
	if len(eps) != 1 || eps[0].UserPrompt != "now add tests" {
		t.Fatalf("resumed parse gave %d episodes: %+v", len(eps), eps)
	}
}

// The same prompt in the same transcript is the same episode, so a re-read
// updates one row rather than adding another.
func TestParseAntigravity_KeyIsStable(t *testing.T) {
	in := agyLines(agyUserOne, agyPlanOne)

	first, _, _ := ParseAntigravity(strings.NewReader(in), testSource(), 0)
	second, _, _ := ParseAntigravity(strings.NewReader(in), testSource(), 0)
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("parses gave %d and %d episodes", len(first), len(second))
	}
	if first[0].Key != second[0].Key {
		t.Errorf("keys differ across identical parses: %s vs %s", first[0].Key, second[0].Key)
	}
}

// A format change must cost episodes, never correctness.
func TestParseAntigravity_FailsSoft(t *testing.T) {
	in := agyLines(
		"not json at all",
		`{"type":"SOMETHING_NEW","content":"added next release"}`,
		agyUserOne,
		`{"step_index":9,"source":"USER_EXPLICIT","type":"USER_INPUT","status":"DONE","content":"<ADDITIONAL_METADATA>no request here</ADDITIONAL_METADATA>"}`,
		agyPlanOne,
	)

	eps, _, err := ParseAntigravity(strings.NewReader(in), testSource(), 0)
	if err != nil {
		t.Fatalf("unparseable lines must be skipped, not returned: %v", err)
	}
	if len(eps) != 1 {
		t.Fatalf("got %d episodes, want 1 — a USER_INPUT with no request is not an episode", len(eps))
	}
	if eps[0].UserPrompt != "rewrite the store layer" {
		t.Errorf("prompt = %q", eps[0].UserPrompt)
	}
}

func TestParseAntigravity_Empty(t *testing.T) {
	eps, resume, err := ParseAntigravity(strings.NewReader(""), testSource(), 0)
	if err != nil || len(eps) != 0 || resume != 0 {
		t.Errorf("empty transcript: %d episodes, resume %d, err %v", len(eps), resume, err)
	}
}
