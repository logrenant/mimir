import type { ReactNode } from "react";
import { cn } from "../../lib/cn";

export function Card({ className, children }: { className?: string; children: ReactNode }) {
  return (
    <div className={cn("rounded-lg border border-edge bg-panel", className)}>{children}</div>
  );
}

export function CardHeader({ title, subtitle, aside }: { title: ReactNode; subtitle?: ReactNode; aside?: ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-4 border-b border-edge px-4 py-3">
      <div>
        <h2 className="text-sm font-semibold tracking-tight">{title}</h2>
        {subtitle && <p className="mt-0.5 text-xs text-muted">{subtitle}</p>}
      </div>
      {aside}
    </div>
  );
}

export function CardBody({ className, children }: { className?: string; children: ReactNode }) {
  return <div className={cn("px-4 py-3", className)}>{children}</div>;
}
