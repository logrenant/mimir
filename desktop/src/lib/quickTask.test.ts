import { describe, expect, test } from "vitest";
import { finishedNotification, isDismissKey, isSubmitKey } from "./quickTask";
import { emptyRun, reduceRun } from "./runStream";
import type { RunEvent } from "./daemon";

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
