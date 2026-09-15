import { useEffect, useRef, useState } from "react";
import { cn } from "../../lib/cn";

export type PulseTone = "ok" | "bad" | "warn" | "muted" | "accent";

const TONE: Record<PulseTone, string> = {
  ok: "bg-ok",
  bad: "bg-bad",
  warn: "bg-warn",
  muted: "bg-muted/40",
  accent: "bg-lime",
};

/**
 * The status dot, and the one place in this interface where motion is bound to
 * a live signal rather than to a click.
 *
 * There were four dots — `Terminal#StatusDot`, `Dashboard#navDotStyle`, the one
 * in `DiagnosticsPanel`, the one in `AccountPanel` — each with its own hex and
 * its own idea of how many states exist.
 *
 * The important part is `beat`. A dot that blinks on a `setInterval` says
 * nothing: it looks identical whether the daemon answered a moment ago or died
 * an hour ago, which is precisely the question the dot exists to answer. So
 * this one does not run on a timer. It is given the sequence number of the last
 * successful poll, and it ticks once each time that number changes. A daemon
 * that has stopped answering produces a dot that has stopped moving, and the
 * stillness is the reading.
 *
 * That inversion is why nothing else in this app pulses. If everything breathed
 * decoratively, a dot going still would mean nothing at all.
 */
export function Pulse({
  tone = "muted",
  beat,
  size = 6,
  className,
  title,
}: {
  tone?: PulseTone;
  /**
   * Any value that changes when a fresh signal arrives — a poll counter, a
   * `seq`, a timestamp. Leave it undefined for a dot that only reports a
   * state and has no heartbeat behind it.
   */
  beat?: number | string;
  size?: number;
  className?: string;
  title?: string;
}) {
  const [ticks, setTicks] = useState(0);
  const previous = useRef(beat);

  useEffect(() => {
    if (beat === undefined || beat === previous.current) return;
    previous.current = beat;
    // The counter, not the beat itself, is what keys the animation: two
    // successive polls could carry the same value, and re-running a CSS
    // animation needs a genuinely new key.
    setTicks((n) => n + 1);
  }, [beat]);

  const live = beat !== undefined && (tone === "ok" || tone === "accent");

  return (
    <span
      title={title}
      className={cn("relative inline-flex shrink-0", className)}
      style={{ width: size, height: size }}
    >
      {live && (
        <span
          key={ticks}
          aria-hidden
          className={cn("absolute inset-0 rounded-full", TONE[tone])}
          style={{ animation: "mimirRipple var(--dur-slow) var(--ease-decisive) 1" }}
        />
      )}
      <span
        key={`dot-${ticks}`}
        className={cn("relative size-full rounded-full", TONE[tone])}
        style={live ? { animation: "mimirBeat var(--dur-base) var(--ease-decisive) 1" } : undefined}
      />
    </span>
  );
}
