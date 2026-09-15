import type { ReactElement } from "react";
import { cn } from "../../lib/cn";

/**
 * The icon set.
 *
 * The application had two pieces of vector art in it — a chevron inside
 * `field/Select` and the spinner inside `button` — and drew everything else in
 * words. That is why a row of controls read as a list of links: there was no
 * second channel, so every affordance had to spend horizontal space on a verb.
 *
 * These are drawn rather than installed. A library would have been faster and
 * would have handed this product the same glyphs as several hundred other
 * dashboards; the brand's face is a geometric single weight with flat
 * terminals, and an icon set can either agree with that or fight it. So:
 *
 *  - a 16 unit grid with 2 units of padding, so every glyph occupies the same
 *    optical square and a row of them sits on one line without nudging;
 *  - 1.5 units of stroke, butt caps and mitred joins — the terminals Aldrich
 *    has, not the rounded ones every icon library ships;
 *  - `currentColor` throughout, so an icon is coloured by the control it sits
 *    in and can never introduce a fifth colour of its own.
 *
 * One file and one map rather than one component per glyph: the whole set is
 * about 4 kB, splitting it would buy nothing, and a single map is what makes
 * "which icons do we have" answerable by reading one screen.
 */

const GLYPHS = {
  // --- navigation -------------------------------------------------------
  /** Genel. The overview: everything at once, in four quarters. */
  grid: (
    <>
      <rect x="2.5" y="2.5" width="4.75" height="4.75" />
      <rect x="8.75" y="2.5" width="4.75" height="4.75" />
      <rect x="2.5" y="8.75" width="4.75" height="4.75" />
      <rect x="8.75" y="8.75" width="4.75" height="4.75" />
    </>
  ),
  /** Board. Columns of unequal depth, which is what a board actually is. */
  board: (
    <>
      <rect x="2.5" y="2.5" width="3" height="11" />
      <rect x="6.5" y="2.5" width="3" height="7.5" />
      <rect x="10.5" y="2.5" width="3" height="4.5" />
    </>
  ),
  terminal: (
    <>
      <path d="M3.5 4.5 7 8l-3.5 3.5" />
      <path d="M8.5 11.5h5" />
    </>
  ),
  /** Brain. The graph, not an organ: three nodes and what joins them. */
  brain: (
    <>
      <circle cx="4" cy="4.5" r="1.75" />
      <circle cx="12" cy="6" r="1.75" />
      <circle cx="7.5" cy="12" r="1.75" />
      <path d="M5.6 5.2 10.4 5.7M11.3 7.6 8.4 10.5M6.6 6.1 6.9 10.3" />
    </>
  ),
  settings: (
    <>
      <path d="M2.5 5.5h11M2.5 10.5h11" />
      <circle cx="6" cy="5.5" r="1.75" />
      <circle cx="10.5" cy="10.5" r="1.75" />
    </>
  ),
  /** A module. An assembled thing, seen from a corner. */
  module: (
    <>
      <path d="M8 2.5 13.5 5.5v5L8 13.5 2.5 10.5v-5z" />
      <path d="M2.5 5.5 8 8.5l5.5-3M8 8.5v5" />
    </>
  ),

  // --- actions ----------------------------------------------------------
  plus: <path d="M8 3.5v9M3.5 8h9" />,
  close: <path d="M4 4l8 8M12 4l-8 8" />,
  check: <path d="M3.5 8.5 6.5 11.5 12.5 4.5" />,
  play: <path d="M5 3.5 12.5 8 5 12.5z" />,
  stop: <rect x="4.5" y="4.5" width="7" height="7" />,
  refresh: (
    <>
      <path d="M13 8a5 5 0 1 1-1.46-3.54" />
      <path d="M13.5 2.5v3h-3" />
    </>
  ),
  trash: (
    <>
      <path d="M3 4.5h10M6 4.5V2.5h4v2" />
      <path d="M4.75 4.5 5.4 13.5h5.2l.65-9" />
    </>
  ),
  search: (
    <>
      <circle cx="7" cy="7" r="4.25" />
      <path d="M10.2 10.2 13.5 13.5" />
    </>
  ),
  copy: (
    <>
      <rect x="5.5" y="5.5" width="8" height="8" />
      <path d="M10.5 5.5v-3h-8v8h3" />
    </>
  ),
  external: (
    <>
      <path d="M9 2.5h4.5V7M13.5 2.5 7.5 8.5" />
      <path d="M11.5 9.5v4h-9v-9h4" />
    </>
  ),
  more: (
    <>
      <circle cx="3.5" cy="8" r=".9" fill="currentColor" stroke="none" />
      <circle cx="8" cy="8" r=".9" fill="currentColor" stroke="none" />
      <circle cx="12.5" cy="8" r=".9" fill="currentColor" stroke="none" />
    </>
  ),

  // --- direction --------------------------------------------------------
  chevronDown: <path d="M4 6.25 8 10.25 12 6.25" />,
  chevronRight: <path d="M6.25 4 10.25 8 6.25 12" />,
  arrowRight: <path d="M2.5 8h10M9 4.5 12.5 8 9 11.5" />,

  // --- state ------------------------------------------------------------
  alert: (
    <>
      <path d="M8 2.5 14 13H2z" />
      <path d="M8 6.5v3.25M8 11.5v.6" />
    </>
  ),
  info: (
    <>
      <circle cx="8" cy="8" r="5.5" />
      <path d="M8 7.25v4M8 4.9v.6" />
    </>
  ),
  /** A signal that is alive. The trace, not a dot. */
  pulse: <path d="M2 8h2.75l2-4.5L9.25 12l1.75-4h3" />,

  // --- objects ----------------------------------------------------------
  folder: <path d="M2.5 13.5v-11h4l1.75 2.25h5.25v8.75z" />,
  image: (
    <>
      <rect x="2.5" y="3.5" width="11" height="9" />
      <circle cx="6" cy="6.75" r="1.1" />
      <path d="M2.5 11 6 8l3 2.5 2.5-2 2 1.75" />
    </>
  ),
  user: (
    <>
      <circle cx="8" cy="5.75" r="2.75" />
      <path d="M2.75 13.5c0-2.9 2.35-4.5 5.25-4.5s5.25 1.6 5.25 4.5" />
    </>
  ),
} as const;

export type IconName = keyof typeof GLYPHS;

/** Every glyph this application has. Exported so a picker can enumerate them. */
export const ICON_NAMES = Object.keys(GLYPHS) as IconName[];

export function Icon({
  name,
  size = 16,
  className,
}: {
  name: IconName;
  /** Optical size in pixels. The stroke scales with it. */
  size?: number;
  className?: string;
}): ReactElement {
  return (
    <svg
      aria-hidden
      focusable="false"
      viewBox="0 0 16 16"
      width={size}
      height={size}
      className={cn("shrink-0", className)}
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      // Butt caps and mitred joins. Every icon library ships round ones, and
      // round terminals beside Aldrich read as a control borrowed from a
      // friendlier product than this one.
      strokeLinecap="butt"
      strokeLinejoin="miter"
    >
      {GLYPHS[name]}
    </svg>
  );
}
