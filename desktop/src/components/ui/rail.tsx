import { motion } from "framer-motion";
import type { ReactNode } from "react";

import { cn } from "../../lib/cn";
import { RAIL } from "../../lib/motion";
import { Icon, type IconName } from "./icon";

/**
 * A vertical list where exactly one row is the place you are.
 *
 * `desktop/AGENTS.md` states the rule this draws — "a selected thing says so
 * more than once" — and until now the application held eight hand-written
 * answers to it in four different DOM shapes: `border-lime` in Katalog and
 * Lead-gen, `border-l-lime` in Settings, a positioned 3px `<span>` in the
 * sidebar and Terminals, `ui/card`'s unused `accent` prop, and — in Katalog's
 * product table — `border-l-electric`, which is the exact failure the palette
 * note says was fixed, since Electric is the machine's colour and a selected
 * row is the operator's.
 *
 * So selection is said three times, once: the surface fills, a 3px Lime rail
 * sits at the leading edge, and the label goes from `muted` to Mist at medium
 * weight. The rail is one element shared by `layoutId`, so it *travels* to the
 * row you picked rather than blinking out of one and into another; rails in
 * different lists need different `layoutId`s or they will fly across the screen
 * to each other.
 */
export function Rail({
  label,
  children,
  className,
}: {
  /** The list's accessible name — "Ayarlar bölümleri", "Katalog adımları". */
  label: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <nav aria-label={label} className={cn("flex flex-col gap-0.5", className)}>
      {children}
    </nav>
  );
}

export function RailItem({
  label,
  icon,
  lead,
  mark,
  on,
  onSelect,
  layoutId,
  className,
}: {
  /** Usually a string. A node when the row has two lines to say. */
  label: ReactNode;
  icon?: IconName;
  /** Something before the label that is not one of the drawn glyphs — a status
   * dot, a favicon. `icon` covers the ordinary case. */
  lead?: ReactNode;
  /**
   * Whatever this row has to say about itself — a count, a tick, an unsaved
   * dot. A slot rather than a union, because the rail does not know what its
   * rows are counting and should not learn.
   */
  mark?: ReactNode;
  on: boolean;
  onSelect: () => void;
  /**
   * The travelling rail's shared identity. Omit it and the rail is drawn
   * statically, which is what a list that is rebuilt on every render wants.
   */
  layoutId?: string;
  className?: string;
}) {
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-current={on ? "page" : undefined}
      className={cn(
        "focus-ring relative flex min-h-9 w-full min-w-0 items-center gap-2.5 rounded-md py-1.5 pr-2.5 pl-3 text-left text-base",
        "transition-colors duration-[var(--dur-fast)] ease-decisive",
        on
          ? "bg-raised font-medium text-text shadow-elev-1"
          : "text-muted hover:bg-panel hover:text-text",
        className,
      )}
    >
      {on &&
        (layoutId ? (
          <motion.span
            layoutId={layoutId}
            transition={RAIL}
            aria-hidden
            className="absolute inset-y-1.5 left-0 w-[3px] rounded-full bg-lime"
          />
        ) : (
          <span
            aria-hidden
            className="absolute inset-y-1.5 left-0 w-[3px] rounded-full bg-lime"
          />
        ))}
      {icon && <Icon name={icon} size={15} className={on ? "text-lime" : undefined} />}
      {lead}
      <span className="min-w-0 flex-1 truncate">{label}</span>
      {mark}
    </button>
  );
}
