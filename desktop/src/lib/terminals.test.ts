import { describe, expect, test } from "vitest";
import type { Run, RunEvent } from "./daemon";
import {
  appendEvent,
  formatEventLine,
  MAX_LINES,
  newSession,
  openSession,
  orderedSessions,
  recentRuns,
  runsToAdopt,
  sessionText,
  staleDismissals,
  tailLines,
} from "./terminals";

function event(partial: Partial<RunEvent> & { kind: RunEvent["kind"]; seq: number }): RunEvent {
  return { run_id: "run-1", at: "2026-09-01T00:00:00Z", ...partial } as RunEvent;
}

function session() {
  return newSession({ id: "run-1", project_id: "p", prompt: "do it", status: "running" } as Run);
}

describe("formatEventLine", () => {
  test("a tool call names the tool and what it is acting on", () => {
    const line = formatEventLine(
      event({ kind: "tool.call", seq: 1, tool_name: "Bash", risk: "exec", args: { command: "go test ./..." } }),
    );
    expect(line?.text).toContain("Bash");
    expect(line?.text).toContain("go test ./...");
    expect(line?.kind).toBe("tool");
  });

  test("stderr is its own kind, because it is why a broken run is broken", () => {
    const line = formatEventLine(event({ kind: "stderr", seq: 1, text: "run `claude login`" }));
    expect(line?.kind).toBe("stderr");
    expect(line?.text).toContain("claude login");
  });

  test("a stop is not a failure and does not read like one", () => {
    const line = formatEventLine(event({ kind: "run.stopped", seq: 1 }));
    expect(line?.text).toContain("stopped by the operator");
  });
});

describe("appendEvent", () => {
  test("deltas extend the previous line rather than starting a new one", () => {
    let s = session();
    s = appendEvent(s, event({ kind: "text.delta", seq: 1, text: "Hel" }));
    s = appendEvent(s, event({ kind: "text.delta", seq: 2, text: "lo" }));

    expect(s.lines).toHaveLength(1);
    expect(s.lines[0].text).toBe("Hello");
  });

  test("but a tool call in between breaks the run of deltas", () => {
    let s = session();
    s = appendEvent(s, event({ kind: "text.delta", seq: 1, text: "a" }));
    s = appendEvent(s, event({ kind: "tool.call", seq: 2, tool_name: "Read" }));
    s = appendEvent(s, event({ kind: "text.delta", seq: 3, text: "b" }));

    expect(s.lines.map((l) => l.kind)).toEqual(["text", "tool", "text"]);
  });

  test("replayed events are dropped on seq, as the socket replays the transcript", () => {
    let s = session();
    s = appendEvent(s, event({ kind: "text.delta", seq: 1, text: "a" }));
    s = appendEvent(s, event({ kind: "text.delta", seq: 1, text: "a" }));

    expect(sessionText(s)).toBe("a");
  });

  test("a terminal event moves the session's badge without a poll", () => {
    let s = session();
    s = appendEvent(s, event({ kind: "run.failed", seq: 1, error: "nope" }));
    expect(s.status).toBe("failed");

    let stopped = session();
    stopped = appendEvent(stopped, event({ kind: "run.stopped", seq: 1 }));
    expect(stopped.status).toBe("stopped");
  });

  test("scrollback is bounded — a long run must not be an unbounded array", () => {
    let s = session();
    for (let i = 1; i <= MAX_LINES + 50; i++) {
      s = appendEvent(s, event({ kind: "tool.call", seq: i, tool_name: `t${i}` }));
    }
    expect(s.lines).toHaveLength(MAX_LINES);
    // The tail is the part anyone reads.
    expect(s.lines[s.lines.length - 1].text).toContain(`t${MAX_LINES + 50}`);
  });
});

describe("openSession", () => {
  test("re-opening keeps the scrollback it already has", () => {
    const run = { id: "run-1", project_id: "p", prompt: "do it", status: "running" } as Run;
    let map = openSession({}, run);
    map = { ...map, "run-1": appendEvent(map["run-1"], event({ kind: "text.delta", seq: 1, text: "hi" })) };

    map = openSession(map, { ...run, status: "completed" });

    expect(map["run-1"].lines).toHaveLength(1);
    expect(map["run-1"].status).toBe("completed");
  });

  test("live sessions come first, so what just started is nearest to hand", () => {
    let map = openSession({}, { id: "done", project_id: "p", prompt: "a", status: "completed" } as Run);
    map = openSession(map, { id: "live", project_id: "p", prompt: "b", status: "running" } as Run);

    expect(orderedSessions(map)[0].runID).toBe("live");
  });
});

describe("recentRuns", () => {
  const run = (id: string, status: Run["status"]) =>
    ({ id, project_id: "p", prompt: "do it", status }) as Run;

  test("offers finished runs, since the socket replays their transcript", () => {
    const got = recentRuns([run("a", "completed"), run("b", "failed"), run("c", "stopped")], {});
    expect(got.map((r) => r.id)).toEqual(["a", "b", "c"]);
  });

  test("skips runs that never started — there is nothing to replay", () => {
    // Opening one would show an empty console and look broken.
    const got = recentRuns([run("a", "backlog"), run("b", "queued"), run("c", "completed")], {});
    expect(got.map((r) => r.id)).toEqual(["c"]);
  });

  test("and skips what is already open, so a tab is never listed twice", () => {
    const open = openSession({}, run("a", "completed"));
    const got = recentRuns([run("a", "completed"), run("b", "completed")], open);
    expect(got.map((r) => r.id)).toEqual(["b"]);
  });
});

describe("auto-adopt", () => {
  const runs = [
    { id: "r1", project_id: "p", prompt: "one", status: "running" },
    { id: "r2", project_id: "p", prompt: "two", status: "queued" },
    { id: "r3", project_id: "p", prompt: "three", status: "completed" },
    { id: "r4", project_id: "p", prompt: "four", status: "backlog" },
  ] as Run[];

  test("only a running job gets a terminal it did not ask for", () => {
    expect(runsToAdopt(runs, {}, new Set()).map((r) => r.id)).toEqual(["r1"]);
  });

  test("an already open session is not adopted twice", () => {
    const open = openSession({}, runs[0]);
    expect(runsToAdopt(runs, open, new Set())).toEqual([]);
  });

  test("closing a tab on a running job keeps it closed", () => {
    expect(runsToAdopt(runs, {}, new Set(["r1"]))).toEqual([]);
  });

  test("a dismissal is forgotten once the run is over, not kept forever", () => {
    expect(staleDismissals(runs, new Set(["r1", "r2"]))).toEqual([]);
    expect(staleDismissals(runs, new Set(["r3", "gone"])).sort()).toEqual(["gone", "r3"]);
  });
});

describe("tailLines", () => {
  test("a short session is shown whole", () => {
    let s = session();
    s = appendEvent(s, event({ kind: "text.delta", seq: 1, text: "one" }));
    expect(tailLines(s, 10)).toHaveLength(s.lines.length);
  });

  test("a long one keeps the end, which is where the news is", () => {
    let s = session();
    for (let i = 1; i <= 20; i++) {
      s = appendEvent(s, event({ kind: "stderr", seq: i, text: `line ${i}` }));
    }
    const tail = tailLines(s, 3);
    expect(tail).toHaveLength(3);
    expect(tail[2]).toBe(s.lines[s.lines.length - 1]);
  });
});
