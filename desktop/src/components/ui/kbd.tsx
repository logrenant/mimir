import type { ReactNode } from "react";
import { cn } from "../../lib/cn";

/**
 * A keystroke, drawn.
 *
 * The application has real shortcuts — ⌘↵ submits a task, ⌘⇧G summons the quick
 * window, Escape closes an overlay — and told the operator about none of them,
 * or told them in a sentence ("göndermek için Cmd+Enter"). A key that looks
 * like a key is read without being read, which is the entire point: it is the
 * cheapest way an interface says "there is a faster path than the one you are
 * taking".
 */
export function Kbd({ className, children }: { className?: string; children: ReactNode }) {
  return (
    <kbd
      className={cn(
        "inline-flex h-5 min-w-5 items-center justify-center rounded-sm px-1.5",
        "bg-raised font-mono text-xs leading-none font-medium text-muted",
        "outline outline-edge",
        className,
      )}
    >
      {children}
    </kbd>
  );
}
