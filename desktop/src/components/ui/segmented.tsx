import { motion } from "framer-motion";
import type { ReactNode } from "react";
import { cn } from "../../lib/cn";
import { RAIL } from "../../lib/motion";
import { Icon, type IconName } from "./icon";

export interface Segment<K extends string> {
  key: K;
  label: ReactNode;
  icon?: IconName;
  count?: number;
  /** The unsaved marker. `Settings` uses it per channel. */
  dirty?: boolean;
  disabled?: boolean;
}

/**
 * A small closed set, one of which is on.
 *
 * This was `Tabs variant="segmented"`, and it was a row of buttons on the same
 * tone as the surface around it with the selected one a shade lighter. Both
 * halves of that are backwards. A segmented control is a *track* with a
 * travelling thumb, and the track has to be recessed — cut into the surface —
 * or there is nothing for the thumb to sit in and the whole thing reads as
 * three buttons that happen to be adjacent.
 *
 * So: the track is `sunken`, below the panel it sits on; the thumb is `raised`,
 * above it; and it is one element moved by `layoutId` rather than two faded, so
 * changing segment moves an object instead of swapping a colour. That travel is
 * the clearest statement this interface makes that the surface is a mechanism.
 */
export function Segmented<K extends string>({
  segments,
  active,
  onSelect,
  /** Distinguishes this control's thumb from every other one on the screen.
   * Two controls sharing an id would hand the thumb between them. */
  id,
  size = "md",
  className,
}: {
  segments: Segment<K>[];
  active: K;
  onSelect: (key: K) => void;
  id: string;
  size?: "sm" | "md";
  className?: string;
}) {
  return (
    <div
      role="tablist"
      className={cn(
        "inline-flex items-center gap-1 rounded-full bg-sunken p-1",
        "outline outline-edge/70",
        className,
      )}
    >
      {segments.map((segment) => {
        const on = segment.key === active;
        return (
          <button
            key={segment.key}
            type="button"
            role="tab"
            aria-selected={on}
            disabled={segment.disabled}
            onClick={() => onSelect(segment.key)}
            className={cn(
              "focus-ring-inset relative inline-flex items-center gap-1.5 rounded-full font-medium",
              "transition-colors duration-[var(--dur-fast)] ease-decisive",
              "disabled:pointer-events-none disabled:opacity-40",
              size === "sm" ? "h-7 px-3 text-xs" : "h-8 px-3.5 text-base",
              on ? "text-text" : "text-muted hover:text-text",
            )}
          >
            {on && (
              <motion.span
                layoutId={`${id}-thumb`}
                transition={RAIL}
                aria-hidden
                className="absolute inset-0 rounded-full bg-raised shadow-elev-1"
              />
            )}
            {segment.icon && <Icon name={segment.icon} size={14} className="relative" />}
            <span className="relative">{segment.label}</span>
            {segment.count !== undefined && (
              <span className="relative font-mono text-xs text-muted/70">{segment.count}</span>
            )}
            {segment.dirty && (
              <span aria-hidden className="relative size-1.5 rounded-full bg-lime" />
            )}
          </button>
        );
      })}
    </div>
  );
}
