import { useReducedMotion, type Transition, type Variants } from "framer-motion";

/**
 * The one place that decides how long anything takes.
 *
 * Before this file the application had two transitions in it — `transition-colors`
 * on thirteen lines and one hand-written `transform .12s` on a disclosure caret
 * — so there was nothing to be consistent with. The risk now is the opposite
 * one: twelve screens each picking their own 250ms. These constants exist so
 * that a screen importing motion cannot invent a duration, and so that the
 * numbers here and the `--dur-*` custom properties in `index.css` are the same
 * numbers rather than two drifting copies.
 *
 * Seconds, because that is framer-motion's unit; `index.css` states the same
 * values in milliseconds because that is CSS's.
 */
export const DUR = {
  /** A control acknowledging a press or a hover. Below this it reads as a glitch. */
  fast: 0.12,
  /** The default: a panel opening, a rail sliding, a screen swapping. */
  base: 0.2,
  /** Something crossing a large distance — the save bar rising from the floor. */
  slow: 0.32,
} as const;

/**
 * One curve, and it decelerates hard.
 *
 * A symmetric ease makes an interface feel like it is floating; this one leaves
 * immediately and arrives gently, which is what makes a control feel mechanical
 * — the right register for a console that drives a daemon. Kept in sync with
 * `--ease-decisive`.
 */
export const EASE = [0.2, 0.8, 0.2, 1] as const;

export const TR = {
  fast: { duration: DUR.fast, ease: EASE },
  base: { duration: DUR.base, ease: EASE },
  slow: { duration: DUR.slow, ease: EASE },
} satisfies Record<string, Transition>;

/**
 * The sliding indicators — the sidebar rail, a tab underline.
 *
 * A spring rather than a duration, because this one is a physical object being
 * moved to a new position and the distance varies: sliding one nav item and
 * sliding five should not take the same time. `bounce: 0` keeps it from
 * overshooting, which on a 2px rail would read as a rendering error rather
 * than as personality.
 */
export const RAIL: Transition = { type: "spring", bounce: 0, duration: 0.34 };

/**
 * A board card settling after a drop.
 *
 * Slightly softer than {@link RAIL} because the card is bigger and travels
 * further, and a trace of bounce is what makes the drop read as landing rather
 * than as teleporting. This is the one place in the application with any
 * bounce in it at all.
 */
export const SETTLE: Transition = { type: "spring", bounce: 0.18, duration: 0.42 };

// ---------------------------------------------------------------------------
// variants
// ---------------------------------------------------------------------------

/**
 * Switching screens.
 *
 * 8px and not 24px: the pane is 886px wide and a large slide would read as
 * navigation between documents, when what actually happened is that one tab
 * replaced another. Paired with `AnimatePresence mode="wait"` so the two
 * screens never overlap — they share a scroll container.
 */
export const screenSwap: Variants = {
  hidden: { opacity: 0, y: 8 },
  shown: { opacity: 1, y: 0, transition: TR.base },
  gone: { opacity: 0, y: -6, transition: TR.fast },
};

/** The scrim behind a modal. Opacity only — a blurred backdrop that also moves is nauseating. */
export const scrim: Variants = {
  hidden: { opacity: 0 },
  shown: { opacity: 1, transition: TR.fast },
  gone: { opacity: 0, transition: TR.fast },
};

/**
 * The modal panel itself.
 *
 * It grows from 0.98 rather than from 0.9. A large scale reads as a dialog
 * being thrown at the operator; a small one reads as it stepping forward out of
 * the page, which is what the elevation shadow is already saying.
 */
export const dialog: Variants = {
  hidden: { opacity: 0, scale: 0.98, y: 6 },
  shown: { opacity: 1, scale: 1, y: 0, transition: TR.base },
  gone: { opacity: 0, scale: 0.98, y: 4, transition: TR.fast },
};

/** A bar docking to an edge — the settings save bar, the lead-gen selection bar. */
export const dock: Variants = {
  hidden: { opacity: 0, y: 12 },
  shown: { opacity: 1, y: 0, transition: TR.slow },
  gone: { opacity: 0, y: 12, transition: TR.fast },
};

/**
 * A row appearing in or leaving a list that the operator is watching change —
 * a draft being decided, a terminal tab closing.
 *
 * Deliberately *not* applied to lists that merely render on arrival. A screen
 * whose every section fades up on load is the single most recognisable tell of
 * a generated interface, and it also lies: nothing happened, the data was
 * simply there.
 */
export const row: Variants = {
  hidden: { opacity: 0, height: 0 },
  shown: { opacity: 1, height: "auto", transition: TR.base },
  gone: { opacity: 0, height: 0, transition: TR.fast },
};

// ---------------------------------------------------------------------------
// reduced motion
// ---------------------------------------------------------------------------

/**
 * The same variants with every displacement removed.
 *
 * Opacity is kept: "reduce motion" is a request about vestibular movement, not
 * a request for hard cuts, and a cross-fade is the standard substitution. The
 * CSS side collapses durations to 1ms for everything that is not framer-motion
 * (`index.css`), and this is the half framer-motion owns.
 *
 * Pure, so it is testable and so a component can compute it once rather than
 * branching at every property.
 */
export function flatten(variants: Variants): Variants {
  const out: Variants = {};
  for (const [state, value] of Object.entries(variants)) {
    if (typeof value !== "object" || value === null) {
      out[state] = value;
      continue;
    }
    const { x: _x, y: _y, scale: _scale, height: _height, ...rest } = value as Record<string, unknown>;
    out[state] = rest as Variants[string];
  }
  return out;
}

/**
 * Picks the variant set this viewer should see.
 *
 * `useReducedMotion()` returns `null` before the media query has been read,
 * which is not the same as `false`; treating it as "no preference" is correct
 * — the query resolves synchronously on every platform this app ships to
 * (one, macOS) — but the `=== true` is written out so the null is not read as
 * an accident.
 */
export function useMotion(variants: Variants): Variants {
  const reduced = useReducedMotion();
  return reduced === true ? flatten(variants) : variants;
}
