import { describe, expect, test } from "vitest";
import type { RunEvent } from "./daemon";
import { emptyRun, reduceRun } from "./runStream";

function event(partial: Partial<RunEvent> & { kind: RunEvent["kind"]; seq: number }): RunEvent {
  return { run_id: "run-1", at: "2026-08-31T00:00:00Z", ...partial } as RunEvent;
}

describe("reduceRun", () => {
  test("appends deltas, because the daemon sends suffixes not snapshots", () => {
    let view = emptyRun();
    view = reduceRun(view, event({ kind: "text.delta", seq: 1, text: "Hel" }));
    view = reduceRun(view, event({ kind: "text.delta", seq: 2, text: "lo" }));

    expect(view.text).toBe("Hello");
  });

  test("drops replayed and out-of-order events on seq", () => {
    let view = emptyRun();
    view = reduceRun(view, event({ kind: "text.delta", seq: 1, text: "a" }));
    view = reduceRun(view, event({ kind: "text.delta", seq: 2, text: "b" }));
    // The server stitches its transcript replay to the live bus on seq; a
    // client that trusted arrival order would render "ab" twice, and a
    // duplicated delta is a corrupted message, not a cosmetic glitch.
    view = reduceRun(view, event({ kind: "text.delta", seq: 1, text: "a" }));
    view = reduceRun(view, event({ kind: "text.delta", seq: 2, text: "b" }));

    expect(view.text).toBe("ab");
    expect(view.lastSeq).toBe(2);
  });

  test("matches a tool result to its call by id, not by arrival order", () => {
    let view = emptyRun();
    view = reduceRun(view, event({ kind: "tool.call", seq: 1, call_id: "c1", tool_name: "Read", risk: "read" }));
    view = reduceRun(view, event({ kind: "tool.call", seq: 2, call_id: "c2", tool_name: "Edit", risk: "write" }));
    view = reduceRun(view, event({ kind: "tool.result", seq: 3, call_id: "c2", ok: true, output: "edited" }));

    expect(view.tools).toHaveLength(2);
    expect(view.tools[0]!.callID).toBe("c1");
    // The first call is still pending: its result has not arrived, and it must
    // not inherit its neighbour's.
    expect(view.tools[0]!.ok).toBeUndefined();
    expect(view.tools[1]).toMatchObject({ callID: "c2", ok: true, output: "edited" });
  });

  test("a result with no matching call still shows up", () => {
    let view = emptyRun();
    view = reduceRun(view, event({ kind: "tool.result", seq: 1, call_id: "orphan", tool_name: "Bash", ok: false }));

    expect(view.tools).toHaveLength(1);
    expect(view.tools[0]).toMatchObject({ callID: "orphan", ok: false });
  });

  test("keeps reasoning separate from the answer", () => {
    let view = emptyRun();
    view = reduceRun(view, event({ kind: "reasoning.delta", seq: 1, text: "thinking" }));
    view = reduceRun(view, event({ kind: "text.delta", seq: 2, text: "answer" }));

    expect(view.reasoning).toBe("thinking");
    expect(view.text).toBe("answer");
  });

  test("completion carries cost and turns; failure carries the error", () => {
    let done = reduceRun(emptyRun(), event({ kind: "run.completed", seq: 1, cost_usd: 0.42, num_turns: 3 }));
    expect(done).toMatchObject({ finished: true, failed: false, costUSD: 0.42, numTurns: 3 });

    let failed = reduceRun(emptyRun(), event({ kind: "run.failed", seq: 1, error: "claude CLI unavailable" }));
    expect(failed).toMatchObject({ finished: true, failed: true, error: "claude CLI unavailable" });
  });

  test("an unknown event kind is ignored, not fatal", () => {
    const view = reduceRun(emptyRun(), event({ kind: "rate_limit", seq: 1, utilization: 0.8 }));
    expect(view.rateLimited).toBe(0.8);
    expect(view.finished).toBe(false);
  });
});
