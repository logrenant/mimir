import { describe, expect, it } from "vitest";
import { DUR, EASE, TR, flatten, dialog, dock, row, screenSwap, scrim } from "./motion";

describe("durations", () => {
  it("are ordered and distinguishable", () => {
    expect(DUR.fast).toBeLessThan(DUR.base);
    expect(DUR.base).toBeLessThan(DUR.slow);
  });

  // The numbers in index.css are the same numbers in milliseconds. A change on
  // one side without the other is exactly the drift this module exists to stop.
  it("match the --dur-* custom properties", () => {
    expect(DUR.fast * 1000).toBe(120);
    expect(DUR.base * 1000).toBe(200);
    expect(DUR.slow * 1000).toBe(320);
  });

  it("carries the one curve on every transition", () => {
    for (const tr of Object.values(TR)) expect(tr.ease).toBe(EASE);
  });
});

describe("flatten", () => {
  it("removes every displacement", () => {
    const flat = flatten(screenSwap);
    for (const state of Object.values(flat)) {
      expect(state).not.toHaveProperty("y");
      expect(state).not.toHaveProperty("x");
      expect(state).not.toHaveProperty("scale");
      expect(state).not.toHaveProperty("height");
    }
  });

  // "Reduce motion" asks for less movement, not for hard cuts — a cross-fade is
  // the standard substitution, so the opacity has to survive.
  it("keeps opacity, so the substitution is a cross-fade", () => {
    expect(flatten(dialog).shown).toMatchObject({ opacity: 1 });
    expect(flatten(dialog).hidden).toMatchObject({ opacity: 0 });
  });

  it("keeps the transition, so a flattened variant still ends where it should", () => {
    expect(flatten(dock).shown).toHaveProperty("transition");
  });

  it("does not mutate the source", () => {
    flatten(screenSwap);
    expect(screenSwap.hidden).toMatchObject({ y: 8 });
  });

  it("leaves a variant with nothing to strip alone", () => {
    expect(flatten(scrim)).toEqual(scrim);
  });
});

describe("variants", () => {
  // AnimatePresence needs all three; a missing `gone` exits by snapping.
  it("each declare hidden, shown and gone", () => {
    for (const v of [screenSwap, scrim, dialog, dock, row]) {
      expect(Object.keys(v).sort()).toEqual(["gone", "hidden", "shown"]);
    }
  });

  // Leaving is always quicker than arriving: the operator has already decided,
  // and waiting for a dismissal to finish is what makes an interface feel slow.
  it("leave faster than they arrive", () => {
    for (const v of [screenSwap, dialog, dock, row]) {
      const shown = v.shown as { transition: { duration: number } };
      const gone = v.gone as { transition: { duration: number } };
      expect(gone.transition.duration).toBeLessThanOrEqual(shown.transition.duration);
    }
  });
});
