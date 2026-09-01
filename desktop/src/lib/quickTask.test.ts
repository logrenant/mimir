import { describe, expect, test } from "vitest";
import { canStart, defaultProject, finishedNotification, isDismissKey, isSubmitKey } from "./quickTask";
import { emptyRun, reduceRun } from "./runStream";
import type { Project } from "./daemon";
import type { RunEvent } from "./daemon";

function project(id: string): Project {
  return {
    id,
    path: `/tmp/${id}`,
    display_name: id,
    created_at: "2026-09-01T00:00:00Z",
    last_used_at: "2026-09-01T00:00:00Z",
  };
}

describe("defaultProject", () => {
  test("keeps a hand-picked project that still exists", () => {
    expect(defaultProject([project("a"), project("b")], "b", true)).toBe("b");
  });

  // The daemon orders by last_used_at, so the head is the folder the operator
  // was last working in. A window that lives for the app's whole lifetime must
  // follow that, or it goes on offering a folder from days ago.
  test("otherwise follows the most recently used project", () => {
    expect(defaultProject([project("a"), project("b")], null)).toBe("a");
    expect(defaultProject([project("a"), project("b")], "b")).toBe("a");
  });

  test("drops even a hand-picked selection the daemon no longer knows about", () => {
    expect(defaultProject([project("a")], "deleted", true)).toBe("a");
  });

  test("is null when nothing is registered — a run cannot be scoped", () => {
    expect(defaultProject([], "a", true)).toBeNull();
  });
});

describe("keys", () => {
  test("Enter sends, Shift+Enter does not", () => {
    expect(isSubmitKey("Enter", false)).toBe(true);
    expect(isSubmitKey("Enter", true)).toBe(false);
    expect(isSubmitKey("g", false)).toBe(false);
  });

  test("Esc dismisses", () => {
    expect(isDismissKey("Escape")).toBe(true);
    expect(isDismissKey("Enter")).toBe(false);
  });
});

describe("canStart", () => {
  test("needs a project, a prompt, and no run already in flight", () => {
    expect(canStart("a", "do the thing", false)).toBe(true);
    expect(canStart(null, "do the thing", false)).toBe(false);
    expect(canStart("a", "   ", false)).toBe(false);
    expect(canStart("a", "do the thing", true)).toBe(false);
  });
});

describe("finishedNotification", () => {
  const event = (partial: Partial<RunEvent>): RunEvent =>
    ({ run_id: "run-1", seq: 1, at: "2026-09-01T00:00:00Z", ...partial }) as RunEvent;

  test("says nothing while the run is going", () => {
    const view = reduceRun(emptyRun(), event({ kind: "text.delta", text: "working" }));
    expect(finishedNotification(view)).toBeNull();
  });

  test("reports completion with the turn count", () => {
    const view = reduceRun(emptyRun(), event({ kind: "run.completed", num_turns: 4 }));
    expect(finishedNotification(view)).toEqual({
      title: "Mimir — run completed",
      body: "Finished in 4 turns.",
    });
  });

  // The daemon's error text is already written for a human, so it is shown as
  // written rather than replaced with "something went wrong".
  test("carries the daemon's own failure message", () => {
    const view = reduceRun(emptyRun(), event({ kind: "run.failed", error: "claude exited with 1" }));
    expect(finishedNotification(view)).toEqual({
      title: "Mimir — run failed",
      body: "claude exited with 1",
    });
  });
});
