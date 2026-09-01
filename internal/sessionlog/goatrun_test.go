package sessionlog

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/logrenant/goat-mcp/internal/events"
)

func runTranscript(t *testing.T, evs ...events.Event) string {
	t.Helper()
	var b strings.Builder
	for _, ev := range evs {
		line, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		b.Write(line)
		b.WriteString("\n")
	}
	return b.String()
}

func at(sec int) time.Time { return time.Unix(1700000000+int64(sec), 0).UTC() }

func boolPtr(b bool) *bool { return &b }

func TestParseGoatRun(t *testing.T) {
	transcript := runTranscript(t,
		events.Event{Kind: events.KindRunStarted, RunID: "r1", Seq: 1, At: at(0), SessionID: "cli-sess", Model: "claude-sonnet-5"},
		events.Event{Kind: events.KindTextDelta, RunID: "r1", Seq: 2, At: at(1), Text: "Adding "},
		events.Event{Kind: events.KindToolCall, RunID: "r1", Seq: 3, At: at(2), CallID: "c1", ToolName: "Edit",
			Args: json.RawMessage(`{"file_path":"/work/demo/internal/crawl/client.go"}`)},
		events.Event{Kind: events.KindToolResult, RunID: "r1", Seq: 4, At: at(3), CallID: "c1", OK: boolPtr(true)},
		events.Event{Kind: events.KindToolCall, RunID: "r1", Seq: 5, At: at(4), CallID: "c2", ToolName: "Bash",
			Args: json.RawMessage(`{"command":"go test ./...","description":"Run the tests"}`)},
		events.Event{Kind: events.KindToolResult, RunID: "r1", Seq: 6, At: at(5), CallID: "c2", OK: boolPtr(false)},
		events.Event{Kind: events.KindTextDelta, RunID: "r1", Seq: 7, At: at(6), Text: "the retry."},
		events.Event{Kind: events.KindRunCompleted, RunID: "r1", Seq: 8, At: at(7), CostUSD: 0.12, NumTurns: 4},
	)

	ep, err := ParseGoatRun(strings.NewReader(transcript),
		Source{Kind: SourceGoatRun, Path: "/t/r1.jsonl", ProjectPath: "/work/demo"},
		RunMeta{RunID: "r1", Prompt: "add a retry to the crawl client"})
	if err != nil {
		t.Fatalf("ParseGoatRun: %v", err)
	}

	if ep.Key != "run:r1" {
		t.Errorf("Key = %q", ep.Key)
	}
	if ep.SourceKind != SourceGoatRun || ep.SessionID != "cli-sess" {
		t.Errorf("metadata: %+v", ep)
	}
	// Deltas are suffixes, so concatenation must rebuild the message.
	if ep.AssistantText != "Adding the retry." {
		t.Errorf("AssistantText = %q", ep.AssistantText)
	}
	if got := ep.FilesTouched; len(got) != 1 || got[0] != "internal/crawl/client.go" {
		t.Errorf("FilesTouched = %q", got)
	}
	if got := ep.Commands; len(got) != 1 || got[0] != "Run the tests" {
		t.Errorf("Commands = %q", got)
	}
	if ep.CostUSD != 0.12 {
		t.Errorf("CostUSD = %v", ep.CostUSD)
	}
	if !ep.StartedAt.Equal(at(0)) || !ep.EndedAt.Equal(at(7)) {
		t.Errorf("time range: %v .. %v", ep.StartedAt, ep.EndedAt)
	}
	if !ep.Significant() {
		t.Errorf("a run that edited a file must be significant")
	}

	var failed []string
	for _, tc := range ep.ToolCalls {
		if !tc.OK {
			failed = append(failed, tc.Name)
		}
	}
	if len(failed) != 1 || failed[0] != "Bash" {
		t.Errorf("want the failing bash call recorded, got %q", failed)
	}
}

// events.Event.OK is a pointer because absent and false mean different things
// for a tool result. Treating absent as failure would mark most calls failed.
func TestParseGoatRun_AbsentOKIsNotAFailure(t *testing.T) {
	transcript := runTranscript(t,
		events.Event{Kind: events.KindToolCall, RunID: "r1", Seq: 1, At: at(0), CallID: "c1", ToolName: "Read",
			Args: json.RawMessage(`{"file_path":"/work/demo/a.go"}`)},
		events.Event{Kind: events.KindToolResult, RunID: "r1", Seq: 2, At: at(1), CallID: "c1"},
	)

	ep, err := ParseGoatRun(strings.NewReader(transcript),
		Source{Kind: SourceGoatRun, ProjectPath: "/work/demo"}, RunMeta{RunID: "r1", Prompt: "p"})
	if err != nil {
		t.Fatalf("ParseGoatRun: %v", err)
	}
	if len(ep.ToolCalls) != 1 || !ep.ToolCalls[0].OK {
		t.Fatalf("an absent OK must not be read as a failure: %+v", ep.ToolCalls)
	}
}

func TestParseGoatRun_FailedRunRecordsItsError(t *testing.T) {
	transcript := runTranscript(t,
		events.Event{Kind: events.KindTextDelta, RunID: "r1", Seq: 1, At: at(0), Text: "starting"},
		events.Event{Kind: events.KindRunFailed, RunID: "r1", Seq: 2, At: at(1), Error: "the claude CLI exited without reporting a result"},
	)

	ep, err := ParseGoatRun(strings.NewReader(transcript),
		Source{Kind: SourceGoatRun, ProjectPath: "/work/demo"}, RunMeta{RunID: "r1", Prompt: "p"})
	if err != nil {
		t.Fatalf("ParseGoatRun: %v", err)
	}
	if !strings.Contains(ep.AssistantText, "[run failed]") {
		t.Errorf("a failed run must say so: %q", ep.AssistantText)
	}
}

func TestParseGoatRun_JunkIsSkipped(t *testing.T) {
	input := "not json\n{\"kind\":\"unknown.kind\"}\n" + runTranscript(t,
		events.Event{Kind: events.KindTextDelta, RunID: "r1", Seq: 1, At: at(0), Text: "ok"})

	ep, err := ParseGoatRun(strings.NewReader(input),
		Source{Kind: SourceGoatRun, ProjectPath: "/work/demo"}, RunMeta{RunID: "r1", Prompt: "p"})
	if err != nil {
		t.Fatalf("junk must not fail the parse: %v", err)
	}
	if ep.AssistantText != "ok" {
		t.Errorf("AssistantText = %q", ep.AssistantText)
	}
}
