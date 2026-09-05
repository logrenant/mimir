import { describe, expect, test } from "vitest";
import type { Run, RunStatus } from "./daemon";
import {
  actionsFor,
  allowedMove,
  canContinue,
  cardTitle,
  columnOf,
  elapsedLabel,
  canEdit,
  groupRuns,
  isStalled,
  pollInterval,
  retryOutcome,
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

  test("a failure dropped back in the queue is a retry", () => {
    expect(allowedMove("failed", "queued")).toEqual({ allowed: true, action: "retry" });
  });

  test("but there is no un-failing a card into the backlog", () => {
    const move = allowedMove("failed", "backlog");
    expect(move.allowed).toBe(false);
    if (!move.allowed) expect(move.reason).toContain("Queued");
  });

  test("and a finished card has nothing left to ask for", () => {
    const move = allowedMove("done", "queued");
    expect(move.allowed).toBe(false);
    if (!move.allowed) expect(move.reason.length).toBeGreaterThan(0);
  });

  test("a drop back onto the same column is a no-op, not a complaint", () => {
    const move = allowedMove("queued", "queued");
    expect(move.allowed).toBe(false);
    if (!move.allowed) expect(move.reason).toBe("");
  });
});

describe("actionsFor", () => {
  test("a backlog card can be run or thrown away, but not stopped", () => {
    expect(actionsFor("backlog")).toEqual(["run", "edit", "delete"]);
  });

  test("a queued card can be dequeued, watched, and can ask the queue why it is waiting", () => {
    expect(actionsFor("queued")).toEqual(["kick", "edit", "dequeue", "terminal"]);
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

describe("picking a failed run back up", () => {
  test("a failed or stopped card offers both ways of going again", () => {
    for (const status of ["failed", "stopped"] as const) {
      expect(actionsFor(status)).toContain("continue");
      expect(actionsFor(status)).toContain("retry");
    }
  });

  test("continue leads, because it is the one that keeps the work already done", () => {
    const actions = actionsFor("failed");
    expect(actions.indexOf("continue")).toBeLessThan(actions.indexOf("retry"));
  });

  test("a completed run has nothing left to pick up", () => {
    expect(actionsFor("completed")).not.toContain("continue");
    expect(actionsFor("completed")).not.toContain("retry");
  });

  test("a backlog card is Enqueue's business, not a retry's", () => {
    expect(actionsFor("backlog")).toEqual(["run", "edit", "delete"]);
  });

  test("continue needs a session to resume — without one it would silently start over", () => {
    expect(canContinue(run({ id: "a", status: "failed", session_id: "sess-1" }))).toBe(true);
    expect(canContinue(run({ id: "b", status: "failed" }))).toBe(false);
    expect(canContinue(run({ id: "c", status: "failed", session_id: "   " }))).toBe(false);
  });

  test("only a run that stopped short can be continued", () => {
    expect(canContinue(run({ id: "d", status: "completed", session_id: "sess-1" }))).toBe(false);
    expect(canContinue(run({ id: "e", status: "running", session_id: "sess-1" }))).toBe(false);
    expect(canContinue(run({ id: "f", status: "stopped", session_id: "sess-1" }))).toBe(true);
  });

  test("a retry waits when something is already in flight — capacity is one run", () => {
    expect(retryOutcome([run({ id: "a", status: "running" })])).toBe("queued");
    expect(retryOutcome([run({ id: "a", status: "queued" })])).toBe("starts");
    expect(retryOutcome([run({ id: "a", status: "completed" })])).toBe("starts");
    expect(retryOutcome([])).toBe("starts");
  });

  test("a board that has not loaded yet does not claim the queue is busy", () => {
    expect(retryOutcome(null)).toBe("starts");
  });
});

describe("isStalled", () => {
  const at = Date.parse("2026-09-01T10:00:00Z");
  const queued = run({ id: "a", status: "queued", queued_at: "2026-09-01T10:00:00Z" });

  test("a card that has just been queued is waiting its turn, not stuck", () => {
    expect(isStalled(queued, [queued], at + 5_000)).toBe(false);
  });

  test("nor is one waiting behind a run that is actually going", () => {
    const runs = [queued, run({ id: "b", status: "running" })];
    expect(isStalled(queued, runs, at + 600_000)).toBe(false);
  });

  test("but a card queued long ago with nothing running is a queue that stopped", () => {
    expect(isStalled(queued, [queued], at + 600_000)).toBe(true);
  });

  test("and only queued cards can stall", () => {
    expect(isStalled(run({ id: "c", status: "failed" }), null, at)).toBe(false);
  });
});

describe("canEdit", () => {
  test("intent is editable — including the failed card a fix is worth making to", () => {
    for (const status of ["backlog", "queued", "failed", "stopped"] as RunStatus[]) {
      expect(canEdit(status)).toBe(true);
      expect(actionsFor(status)).toContain("edit");
    }
  });

  test("but what was spent is a record, not a field", () => {
    for (const status of ["running", "completed"] as RunStatus[]) {
      expect(canEdit(status)).toBe(false);
      expect(actionsFor(status)).not.toContain("edit");
    }
  });
});
