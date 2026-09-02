import { describe, expect, test } from "vitest";
import type { Run, RunStatus } from "./daemon";
import {
  actionsFor,
  allowedMove,
  cardTitle,
  columnOf,
  elapsedLabel,
  groupRuns,
  pollInterval,
} from "./board";

function run(partial: Partial<Run> & { id: string; status: RunStatus }): Run {
  return { project_id: "p", prompt: "do something", ...partial } as Run;
}

describe("columns", () => {
  test("every run status lands in exactly one column", () => {
    const statuses: RunStatus[] = ["backlog", "queued", "running", "completed", "failed", "stopped"];
    for (const status of statuses) {
      expect(columnOf(status)).not.toBeNull();
    }
  });

  test("a stopped run sits with the failures, where 'did not finish' is looked for", () => {
    expect(columnOf("stopped")).toBe("failed");
  });

  test("queued keeps its queue order, because the dispatcher takes the oldest first", () => {
    const grouped = groupRuns([
      run({ id: "b", status: "queued", queued_at: "2026-09-01T10:05:00Z" }),
      run({ id: "a", status: "queued", queued_at: "2026-09-01T10:00:00Z" }),
    ]);
    expect(grouped.queued.map((r) => r.id)).toEqual(["a", "b"]);
  });
});

describe("allowedMove", () => {
  test("the two operator columns exchange cards", () => {
    expect(allowedMove("backlog", "queued")).toEqual({ allowed: true, action: "enqueue" });
    expect(allowedMove("queued", "backlog")).toEqual({ allowed: true, action: "dequeue" });
  });

  test("running, done and failed are the runner's to write, and say so", () => {
    for (const target of ["running", "done", "failed"] as const) {
      const move = allowedMove("backlog", target);
      expect(move.allowed).toBe(false);
      // A drop that silently springs back reads as a broken board, not a rule.
      if (!move.allowed) expect(move.reason.length).toBeGreaterThan(0);
    }
  });

  test("a drop back onto the same column is a no-op, not a complaint", () => {
    const move = allowedMove("queued", "queued");
    expect(move.allowed).toBe(false);
    if (!move.allowed) expect(move.reason).toBe("");
  });
});

describe("actionsFor", () => {
  test("a backlog card can be run or thrown away, but not stopped", () => {
    expect(actionsFor("backlog")).toEqual(["run", "delete"]);
  });

  test("a running card can be stopped and watched, never deleted underneath itself", () => {
    expect(actionsFor("running")).toContain("stop");
    expect(actionsFor("running")).not.toContain("delete");
  });
});

describe("pollInterval", () => {
  test("backs right off when nothing is moving", () => {
    expect(pollInterval([run({ id: "a", status: "completed" })])).toBe(15000);
  });

  test("and speeds up while anything is queued or in flight", () => {
    expect(pollInterval([run({ id: "a", status: "queued" })])).toBe(2000);
    expect(pollInterval([run({ id: "a", status: "running" })])).toBe(2000);
  });
});

describe("cardTitle", () => {
  test("prefers the title the operator wrote", () => {
    expect(cardTitle(run({ id: "a", status: "backlog", title: "fix the board" }))).toBe("fix the board");
  });

  test("falls back to the first non-empty line of the prompt", () => {
    expect(cardTitle(run({ id: "a", status: "backlog", prompt: "\n\nadd a test\nthen run it" }))).toBe(
      "add a test",
    );
  });
});

describe("elapsedLabel", () => {
  test("counts from the start, in mm:ss", () => {
    const started = "2026-09-01T10:00:00Z";
    expect(elapsedLabel(started, Date.parse(started) + 91_000)).toBe("01:31");
  });

  test("Go's zero time is not a start time", () => {
    // encoding/json marshals a zero time.Time as year 1 rather than omitting
    // it, so a backlog card still carries a started_at string.
    expect(elapsedLabel("0001-01-01T00:00:00Z", Date.now())).toBe("");
  });
});
