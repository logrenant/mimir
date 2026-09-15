import { describe, expect, it } from "vitest";
import { place, type Box } from "./popover";

const VIEW = { width: 1000, height: 800 };
/** A control in the middle of the title bar. */
const PILL: Box = { left: 400, top: 12, width: 140, height: 28 };
const PANEL = { width: 320, height: 260 };

describe("place", () => {
  it("hangs below the trigger by the gap", () => {
    const at = place(PILL, PANEL, VIEW);
    expect(at.side).toBe("bottom");
    expect(at.top).toBe(PILL.top + PILL.height + 8);
  });

  it("aligns to the trigger's edges", () => {
    expect(place(PILL, PANEL, VIEW, { align: "start" }).left).toBe(400);
    // The title bar's status pill is at the right end of the window, so `end`
    // is the alignment it actually uses: the panel's right edge meets the
    // pill's, and it opens inward.
    expect(place(PILL, PANEL, VIEW, { align: "end" }).left).toBe(400 + 140 - 320);
    expect(place(PILL, PANEL, VIEW, { align: "center" }).left).toBe(400 + 70 - 160);
  });

  it("flips above when the panel does not fit below", () => {
    const low: Box = { left: 400, top: 700, width: 140, height: 28 };
    const at = place(low, PANEL, VIEW);
    expect(at.side).toBe("top");
    expect(at.top).toBe(700 - 8 - 260);
  });

  // A panel that fits nowhere scrolls, on whichever side has the most room to
  // scroll in. Asking for "top" from a control in the title bar used to be
  // honoured literally: four pixels of room, a panel drawn as a sliver, and a
  // menu that appeared to do nothing when clicked.
  it("takes the roomier side when the panel fits on neither", () => {
    const tall = { width: 320, height: 900 };
    expect(place(PILL, tall, VIEW).side).toBe("bottom");
    expect(place(PILL, tall, VIEW, { side: "top" }).side).toBe("bottom");
  });

  // The reason `maxHeight` is returned at all: a panel taller than its room
  // has to scroll inside, or its footer — the "Tam teşhis" button — ends up
  // below the bottom of the window, which is where "the button is missing"
  // reports come from.
  it("reports the room available so the panel can scroll rather than overflow", () => {
    const at = place(PILL, { width: 320, height: 900 }, VIEW);
    expect(at.maxHeight).toBe(800 - 40 - 8 - 8);
    expect(at.maxHeight).toBeLessThan(900);
  });

  it("keeps the panel inside the viewport's padding", () => {
    const rightEdge: Box = { left: 960, top: 12, width: 32, height: 28 };
    expect(place(rightEdge, PANEL, VIEW).left).toBe(1000 - 320 - 8);

    const leftEdge: Box = { left: 4, top: 12, width: 32, height: 28 };
    expect(place(leftEdge, PANEL, VIEW, { align: "end" }).left).toBe(8);
  });

  // A panel wider than the window pins to the left edge, where its content
  // starts, rather than to the right one.
  it("pins an oversized panel to the left edge", () => {
    expect(place(PILL, { width: 1200, height: 200 }, VIEW).left).toBe(8);
  });

  // A trigger low in a short window, under a panel taller than either side can
  // hold. It has to go where the room is: staying below left it a `maxHeight`
  // of a few pixels, which draws as a sliver and reads as a control that does
  // nothing when you click it.
  it("puts a panel that fits nowhere on the side with more room", () => {
    const low: Box = { left: 100, top: 560, width: 200, height: 36 };
    const got = place(low, { width: 280, height: 900 }, { width: 1200, height: 620 });
    expect(got.side).toBe("top");
    expect(got.maxHeight).toBeGreaterThan(500);
  });
});
