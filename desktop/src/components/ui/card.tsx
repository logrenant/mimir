import type { ReactNode } from "react";
import { cn } from "../../lib/cn";

/**
 * One lit surface in a dark hall — the guide's own description of how the
 * system should feel. A card is a tint of Carbon with a single hairline edge,
 * no shadow and no glow: the depth comes from the tint step, not from light.
 */
export function Card({ className, children }: { className?: string; children: ReactNode }) {
  return (
    <div className={cn("rounded-sm border border-edge bg-panel", className)}>{children}</div>
  );
}

export function CardHeader({ title, subtitle, aside }: { title: ReactNode; subtitle?: ReactNode; aside?: ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-4 border-b border-edge px-4 py-3">
      <div>
        <h2 className="display text-[13px] leading-tight">{title}</h2>
        {subtitle && <p className="mt-1 text-xs text-muted">{subtitle}</p>}
      </div>
      {aside}
    </div>
  );
}

export function CardBody({ className, children }: { className?: string; children: ReactNode }) {
  return <div className={cn("px-4 py-3", className)}>{children}</div>;
}
