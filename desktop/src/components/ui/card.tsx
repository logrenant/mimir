import type { ReactNode } from "react";
import { cn } from "../../lib/cn";

/** How far off the page this surface is. */
export type Elevation = "flat" | "raised" | "floating";
/** The inner padding, on the spacing scale. `none` is for a card that holds a
 * console or a list that paints its own gutters. */
export type Pad = "none" | "sm" | "md" | "lg";

/**
 * A surface.
 *
 * The guide's description is "one lit surface in a dark hall", and this used to
 * read that as: a tint of Carbon, a hairline edge, and depth from the tint step
 * alone. The tint step was four luminance points, so the border was doing all
 * of the work — and because every element in the application had the same
 * border, nothing had a hierarchy. A card, a row, a badge and a tab strip were
 * one rectangle repeated at four sizes.
 *
 * The surface ramp is wider now (`ground` → `panel` → `raised` → `overlay`),
 * which means a panel separates by *tone* and does not need an edge to be a
 * panel. So a card is borderless by default. `bordered` is still there for the
 * one case tone cannot handle: a card sitting on a surface of its own tone.
 *
 * `raised` adds the inset highlight a lit surface catches on its top edge —
 * still no drop shadow, still no glow, the guide's ban intact. `floating` is
 * the only level that casts, and it is for things genuinely above the page: a
 * card under the pointer being dragged.
 *
 * `accent` is the other half of the vocabulary: a 3px Lime rail down the left
 * edge marking the one thing that is active. It was 2px and Electric, which on
 * Carbon was a dark grey line nobody saw.
 */
export function Card({
  className,
  elevation = "flat",
  pad = "none",
  bordered = false,
  accent = false,
  children,
}: {
  className?: string;
  elevation?: Elevation;
  pad?: Pad;
  /** For a card on a surface of its own tone, where tint cannot separate them. */
  bordered?: boolean;
  /** Draws the active rail. One card in a list, never several. */
  accent?: boolean;
  children: ReactNode;
}) {
  return (
    <div
      className={cn(
        "relative rounded-lg bg-panel",
        PAD[pad],
        bordered && "border border-edge",
        elevation === "raised" && "shadow-elev-1",
        elevation === "floating" && "shadow-elev-2",
        className,
      )}
    >
      {accent && (
        <span aria-hidden className="absolute inset-y-0 left-0 w-[3px] rounded-l-lg bg-lime" />
      )}
      {children}
    </div>
  );
}

const PAD: Record<Pad, string> = {
  none: "",
  sm: "p-3",
  md: "p-4",
  lg: "p-5",
};

export function CardHeader({
  title,
  subtitle,
  aside,
}: {
  title: ReactNode;
  subtitle?: ReactNode;
  aside?: ReactNode;
}) {
  return (
    // `shrink-0` for the card that is a column with a scrolling body: a header
    // that shrinks loses its own text before the body gives up a pixel.
    <div className="flex shrink-0 items-start justify-between gap-4 px-5 pt-4 pb-3">
      <div className="min-w-0">
        {/* 15px and Open Sans. This was 13px Aldrich caps — the same size as
            the body text under it, so the heading did not lead anything. */}
        <h2 className="truncate text-lg leading-tight font-semibold">{title}</h2>
        {subtitle && <p className="mt-1 text-sm text-muted">{subtitle}</p>}
      </div>
      {/* `shrink-0` and no wrapping is what a header aside wants right up to
          the moment its content is wider than the room left over — and then it
          is the worst of the options, because the overflow leaves the card and
          gets clipped by the screen, taking whatever was last in the row with
          it. Nothing changes while the row fits; when it does not, it wraps
          instead of walking off the edge. */}
      {aside && (
        <div className="flex min-w-0 flex-wrap items-center justify-end gap-2">{aside}</div>
      )}
    </div>
  );
}

export function CardBody({ className, children }: { className?: string; children: ReactNode }) {
  return <div className={cn("px-5 pb-4", className)}>{children}</div>;
}
