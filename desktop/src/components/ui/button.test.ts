import { describe, expect, it } from "vitest";
import { buttonClass, type ButtonVariant } from "./button";

const VARIANTS: ButtonVariant[] = ["primary", "secondary", "ghost", "quiet", "danger"];

describe("buttonClass", () => {
  // Nine implementations existed because this one had no focus state, no
  // destructive variant and one size. The first of those was also an
  // accessibility hole: no button in the application marked focus.
  it("gives every variant a focus ring", () => {
    for (const v of VARIANTS) expect(buttonClass(v)).toContain("focus-ring");
  });

  // Aldrich is display-only now. A label is Open Sans at the body size in
  // sentence case, which is the size and face an operator can actually read —
  // 10px tracked-out caps in a half-Turkish product was texture, not words.
  it("sets every variant in the body size, not the display face", () => {
    for (const v of VARIANTS) {
      expect(buttonClass(v)).toContain("text-base");
      expect(buttonClass(v)).not.toContain("label");
      expect(buttonClass(v)).not.toContain("uppercase");
    }
  });

  it("gives every variant the same corner", () => {
    for (const v of VARIANTS) expect(buttonClass(v)).toContain("rounded-md");
  });

  it("acknowledges a press on every variant", () => {
    for (const v of VARIANTS) expect(buttonClass(v)).toContain("active:translate-y-px");
  });

  // One filled accent per screen. Lime and not Electric because Electric at
  // #2547e8 on Carbon reads as dark grey: a palette with two accents that
  // produced screens with none.
  it("fills with Lime on the primary variant alone", () => {
    expect(buttonClass("primary")).toContain("bg-lime");
    for (const v of VARIANTS.filter((v) => v !== "primary")) {
      expect(buttonClass(v)).not.toContain("bg-lime");
    }
  });

  // A fill this bright takes Carbon. Mist on Lime is unreadable and is the
  // mistake three of the nine hand-written primaries had made.
  it("puts Carbon on the Lime fill, never Mist", () => {
    expect(buttonClass("primary")).toContain("text-lime-ink");
    expect(buttonClass("primary")).not.toContain("text-mist");
  });

  // Lime at forty per cent over Carbon is olive, which reads as a colour that
  // has gone wrong rather than as a control that is off. A fill stops being
  // the accent when it is disabled; an outline can just fade.
  it("drops the accent on a disabled fill instead of fading it", () => {
    expect(buttonClass("primary")).toContain("disabled:bg-raised");
    for (const v of VARIANTS.filter((v) => v !== "primary")) {
      expect(buttonClass(v)).not.toContain("disabled:bg-raised");
    }
  });

  it("outlines the destructive variant rather than filling it", () => {
    expect(buttonClass("danger")).toContain("border-bad/50");
    expect(buttonClass("danger")).not.toContain("bg-bad ");
  });

  it("lifts only the one control that is accented", () => {
    expect(buttonClass("primary")).toContain("hover:-translate-y-px");
    for (const v of VARIANTS.filter((v) => v !== "primary")) {
      expect(buttonClass(v)).not.toContain("hover:-translate-y-px");
    }
  });

  // Size changes the box and nothing else. A "small" button that is also a
  // different colour is two controls wearing one name.
  it("changes only the metrics between sizes", () => {
    const strip = (v: string) =>
      v.split(" ").filter((c) => !/^(h-|w-|px-|text-)/.test(c));
    expect(strip(buttonClass("ghost", "sm"))).toEqual(strip(buttonClass("ghost", "md")));
    expect(strip(buttonClass("ghost", "lg"))).toEqual(strip(buttonClass("ghost", "md")));
  });

  it("carries no raw colour", () => {
    for (const v of VARIANTS) expect(buttonClass(v)).not.toMatch(/#[0-9a-f]{3,8}/i);
  });
});

// `desktop/AGENTS.md` carried the rule — "a selected thing says so more than
// once" — and the application did not, because there was nowhere to put it.
// Two screens hand-wrote the same recipe and everything else said nothing:
// Katalog's three toolbar buttons looked identical whether their panel was
// open or shut, which is what an operator reported.
describe("buttonClass(active)", () => {
  it("says on three times: a surface, a Lime mark, and the full text colour", () => {
    for (const v of VARIANTS) {
      const on = buttonClass(v, "md", true);
      expect(on.split(" ")).toContain("bg-raised");
      expect(on).toContain("after:bg-lime");
      expect(on).toContain("text-text");
    }
  });

  it("changes nothing when it is off", () => {
    for (const v of VARIANTS) {
      expect(buttonClass(v, "md", false)).toBe(buttonClass(v, "md"));
      expect(buttonClass(v, "md")).not.toContain("after:bg-lime");
    }
  });

  // `quiet` is what a toolbar button wears and `quiet` has no surface of its
  // own, so an active `quiet` that kept its variant's background would be
  // saying "on" in the label alone.
  it("overrides the variant's own surface, so even quiet fills", () => {
    // Unprefixed: `quiet` already carries `hover:bg-raised`, and a hover is
    // not a state.
    const surface = (v: string) => v.split(" ").includes("bg-raised");
    expect(surface(buttonClass("quiet"))).toBe(false);
    expect(surface(buttonClass("quiet", "md", true))).toBe(true);
  });

  // The mark is a bar along the bottom, not a left rail: a rail is for a row
  // in a vertical list, and a row of buttons is horizontal. It matches the
  // underline `ui/tabs` draws under the tab that is on.
  it("marks with a bottom bar rather than a rail", () => {
    const on = buttonClass("ghost", "md", true);
    expect(on).toContain("after:bottom-0");
    expect(on).toContain("after:h-0.5");
    expect(on).not.toContain("border-l");
  });

  it("carries no raw colour when on either", () => {
    for (const v of VARIANTS) {
      expect(buttonClass(v, "md", true)).not.toMatch(/#[0-9a-f]{3,8}/i);
    }
  });
});
