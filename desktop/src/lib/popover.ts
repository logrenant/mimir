/**
 * Where a floating panel goes.
 *
 * Kept out of the component and free of the DOM for one reason: placement is
 * the part of a popover that is actually hard — it flips, it clamps, it has to
 * cope with a trigger near an edge and with a panel taller than the window —
 * and it is the part that is impossible to check by looking at the screen,
 * because the failure is a panel half off an edge you are not currently
 * looking at. As arithmetic over four rectangles it can simply be asserted.
 *
 * Coordinates are viewport coordinates, which is what `getBoundingClientRect`
 * returns and what `position: fixed` consumes, so the component does no
 * conversion.
 */

export type Side = "bottom" | "top";
export type Align = "start" | "center" | "end";

export interface Box {
  left: number;
  top: number;
  width: number;
  height: number;
}

export interface Viewport {
  width: number;
  height: number;
}

export interface PlaceOptions {
  /** Preferred side. Flipped only if the panel does not fit there and does fit opposite. */
  side?: Side;
  /** Which edge of the panel lines up with which edge of the trigger. */
  align?: Align;
  /** Space between trigger and panel. */
  gap?: number;
  /** Space the panel keeps from the viewport's own edges. */
  padding?: number;
}

export interface Placement {
  left: number;
  top: number;
  /** The side actually used, which is not always the side asked for. */
  side: Side;
  /**
   * The height the panel may occupy at this placement. A panel taller than
   * this must scroll internally — growing past it would put its footer off
   * the bottom of the window, which is where "the button is missing" bug
   * reports come from.
   */
  maxHeight: number;
}

/**
 * Places `panel` against `trigger` inside `viewport`.
 *
 * The order is deliberate: choose the side first, then the offset along it,
 * then clamp. Clamping before choosing produces a panel that is inside the
 * window but no longer pointing at anything.
 */
export function place(
  trigger: Box,
  panel: { width: number; height: number },
  viewport: Viewport,
  options: PlaceOptions = {},
): Placement {
  const { side: preferred = "bottom", align = "start", gap = 8, padding = 8 } = options;

  const roomBelow = viewport.height - (trigger.top + trigger.height) - gap - padding;
  const roomAbove = trigger.top - gap - padding;

  // Flip when the preferred side cannot hold the panel and the other side is
  // better. "Better" and not "sufficient": a panel that fits nowhere used to
  // stay where it was asked for, on the reasoning that moving it gained
  // nothing — which is false when the room it was asked for is four pixels and
  // the room opposite is four hundred. A control near the bottom of the window
  // opened a menu with a `maxHeight` of almost zero, which is a control that
  // does nothing when you click it.
  const opposite: Side = preferred === "bottom" ? "top" : "bottom";
  const roomFor = (s: Side) => (s === "bottom" ? roomBelow : roomAbove);
  const side: Side =
    roomFor(preferred) >= panel.height || roomFor(preferred) >= roomFor(opposite)
      ? preferred
      : opposite;

  const room = side === "bottom" ? roomBelow : roomAbove;
  const height = Math.min(panel.height, Math.max(room, 0));

  const top =
    side === "bottom"
      ? trigger.top + trigger.height + gap
      : trigger.top - gap - height;

  let left: number;
  switch (align) {
    case "center":
      left = trigger.left + trigger.width / 2 - panel.width / 2;
      break;
    case "end":
      left = trigger.left + trigger.width - panel.width;
      break;
    default:
      left = trigger.left;
  }

  // Clamp horizontally last. `Math.max` after `Math.min` so a panel wider than
  // the viewport pins to the left edge rather than to the right one, which is
  // where its own content starts.
  left = Math.max(padding, Math.min(left, viewport.width - panel.width - padding));

  return { left, top, side, maxHeight: Math.max(room, 0) };
}
