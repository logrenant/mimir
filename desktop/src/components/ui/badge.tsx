import type { ReactNode } from "react";
import { cn } from "../../lib/cn";

type Tone = "ok" | "warn" | "bad" | "muted" | "accent";

/**
 * A state marker, not decoration.
 *
 * The brand allows one energy accent at a time, so a badge is an outline and a
 * label — never a filled chip competing with the one control that is actually
 * accented on the screen.
 */
const tones: Record<Tone, string> = {
  ok: "border-ok/45 text-ok",
  warn: "border-warn/60 text-warn",
  bad: "border-bad/50 text-bad",
  muted: "border-edge text-muted",
  accent: "border-electric/60 text-electric",
};

export function Badge({
  tone = "muted",
  title,
  children,
}: {
  tone?: Tone;
  /** Native tooltip. A badge is two words; the sentence behind them goes here. */
  title?: string;
  children: ReactNode;
}) {
  return (
    <span
      title={title}
      className={cn(
        "label inline-flex items-center rounded-sm border px-2 py-1 leading-none",
        tones[tone],
      )}
    >
      {children}
    </span>
  );
}
