import type { ReactNode } from "react";
import { cn } from "../../lib/cn";

/**
 * Nothing here, said usefully.
 *
 * There were about eighteen of these, every one a bare string dropped into a
 * `<div>` — "boş", "henüz düğüm yok", "bir düğüme tıkla", "ilk satır
 * bekleniyor…". They were inconsistent in size, colour and alignment, but the
 * real problem is that most of them only said that a list was empty, when an
 * empty screen is the best moment the interface gets to say what to do next.
 *
 * So `title` states the condition and `hint` is optional room for the way out.
 * `action` is for the cases where the way out is a control rather than a
 * sentence — an empty board column can offer the button that fills it.
 *
 * Deliberately without an illustration or an icon: this is a console, and a
 * shrugging cartoon in the middle of a dependency list would be the wrong
 * register entirely.
 */
export function Empty({
  title,
  hint,
  action,
  compact = false,
  className,
}: {
  title: ReactNode;
  hint?: ReactNode;
  action?: ReactNode;
  /** For an empty state inside a small box — a board column, a sidebar list. */
  compact?: boolean;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex flex-col items-center justify-center gap-2 text-center",
        compact ? "px-4 py-6" : "px-6 py-14",
        className,
      )}
    >
      <p className={cn("text-muted", compact ? "text-sm" : "text-lg")}>{title}</p>
      {hint && (
        <p className={cn("max-w-[48ch] text-pretty text-muted/60", compact ? "text-xs" : "text-sm")}>
          {hint}
        </p>
      )}
      {action && <div className="mt-2">{action}</div>}
    </div>
  );
}
