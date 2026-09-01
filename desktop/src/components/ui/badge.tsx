import type { ReactNode } from "react";
import { cn } from "../../lib/cn";

type Tone = "ok" | "warn" | "bad" | "muted";

const tones: Record<Tone, string> = {
  ok: "border-ok/40 text-ok",
  warn: "border-warn/40 text-warn",
  bad: "border-bad/40 text-bad",
  muted: "border-edge text-muted",
};

export function Badge({ tone = "muted", children }: { tone?: Tone; children: ReactNode }) {
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full border px-2 py-0.5 text-[11px] font-medium",
        tones[tone],
      )}
    >
      {children}
    </span>
  );
}
