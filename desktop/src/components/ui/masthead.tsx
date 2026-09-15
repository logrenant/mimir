import type { ReactNode } from "react";
import { cn } from "../../lib/cn";

/**
 * A screen's own name.
 *
 * Aldrich was re-declared inline six times at five different sizes — 18px in
 * `Dashboard` twice, 16px in `Terminals`, 15px in `NewTaskOverlay`, 13px in
 * `BrainConsole`. More to the point, headings were 13–18px and body copy was
 * 14px, so the whole application was set at one size and nothing led anything.
 * That flatness is most of why a working console read as a placeholder.
 *
 * 30px display and no rule under it. The border was there because the heading
 * was not big enough to separate itself; at this size the whitespace does the
 * job, and a hairline under every screen's title was one more of the borders
 * that made the application read as a stack of boxes.
 *
 * The live count sits beside it rather than in a tile of its own: the number is
 * what the screen is *about* — a graph's size, a board's depth — and a big
 * number in a card with a small grey caption underneath is the treatment every
 * dashboard already uses.
 */
export function Masthead({
  title,
  count,
  aside,
  /** Draws the rule back, for a screen whose content starts immediately under
   * it with no gap of its own — a full-bleed table, a board. */
  ruled = false,
  className,
}: {
  title: ReactNode;
  /** The one live figure this screen is about. Already formatted. */
  count?: ReactNode;
  /** Controls belonging to the screen as a whole. */
  aside?: ReactNode;
  ruled?: boolean;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex items-center justify-between gap-6",
        ruled ? "border-b border-edge pb-4" : "pb-1",
        className,
      )}
    >
      <div className="flex min-w-0 items-baseline gap-3">
        <h1 className="display truncate text-3xl">{title}</h1>
        {count !== undefined && (
          <span className="shrink-0 font-mono text-sm text-muted">{count}</span>
        )}
      </div>
      {aside && <div className="flex shrink-0 items-center gap-2">{aside}</div>}
    </div>
  );
}
