import type { ReactNode } from "react";
import { cn } from "../../lib/cn";
import { Pulse, type PulseTone } from "./pulse";

type Tone = "ok" | "warn" | "bad" | "muted" | "accent";

/**
 * A state marker, not decoration.
 *
 * Six shapes were doing this job — this one, `Terminal#StatusBadge`,
 * `Leadgen#DraftState`, the `DURDURULDU` chip and two `borderRadius: 999`
 * pills in `Dashboard`, and `Brain`'s tag chips — and they disagreed about
 * size, corner and colour.
 *
 * Two shapes remain, separated by what they mean rather than by where they
 * appear. A **status** is round, because a state is a reading and readings in
 * this interface are round (the connection pill, the dot, the orb). A **tag**
 * is square, because it is a piece of data — a label the operator or the
 * daemon attached to something.
 *
 * `solid` exists for the one badge on a screen that has to be seen before the
 * screen is read: a failure, a run that is live. Everything else is an outline
 * and a word, so the filled Lime button stays the only fill in the room.
 */
const OUTLINE: Record<Tone, string> = {
  ok: "text-ok outline-ok/40",
  warn: "text-warn outline-warn/50",
  bad: "text-bad outline-bad/45",
  muted: "text-muted outline-edge",
  accent: "text-lime outline-lime/45",
};

const SOLID: Record<Tone, string> = {
  ok: "bg-ok text-lime-ink outline-transparent",
  warn: "bg-warn text-mist outline-transparent",
  bad: "bg-bad text-mist outline-transparent",
  muted: "bg-raised text-text outline-transparent",
  accent: "bg-lime text-lime-ink outline-transparent",
};

export function Badge({
  tone = "muted",
  title,
  dot = false,
  beat,
  shape = "tag",
  solid = false,
  className,
  children,
}: {
  tone?: Tone;
  /** Native tooltip. A badge is two words; the sentence behind them goes here. */
  title?: string;
  /** Prefixes the label with a {@link Pulse} in the same tone. */
  dot?: boolean;
  /** Passed through to the dot: makes it tick on each fresh signal. */
  beat?: number | string;
  /** Round for a state, square for a piece of data. */
  shape?: "status" | "tag";
  /** Filled. For the one badge on a screen that must be seen before the screen is read. */
  solid?: boolean;
  className?: string;
  children: ReactNode;
}) {
  return (
    <span
      title={title}
      className={cn(
        "inline-flex items-center gap-1.5 px-2 py-1 text-xs leading-none font-medium",
        "outline outline-offset-0",
        shape === "status" ? "rounded-full" : "rounded-sm",
        solid ? SOLID[tone] : OUTLINE[tone],
        className,
      )}
    >
      {dot && (
        <Pulse
          tone={tone as PulseTone}
          beat={beat}
          size={5}
          className={solid ? "brightness-0 opacity-70" : undefined}
        />
      )}
      {children}
    </span>
  );
}
