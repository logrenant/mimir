import type { ButtonHTMLAttributes } from "react";
import { cn } from "../../lib/cn";

type Props = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "primary" | "ghost";
};

/**
 * Electric is the CTA colour and it is 8% of a surface, so exactly one primary
 * button belongs on a screen. Everything else is a ghost: an edge and a label.
 * Square-ish corners, Aldrich caps — the brand reads as infrastructure, and
 * infrastructure does not have pill buttons.
 */
export function Button({ variant = "primary", className, ...props }: Props) {
  return (
    <button
      {...props}
      className={cn(
        "label inline-flex items-center justify-center gap-2 rounded-sm px-3.5 py-2 leading-none transition-colors",
        "disabled:cursor-not-allowed disabled:opacity-40",
        variant === "primary"
          ? "bg-electric text-mist hover:bg-electric/85"
          : "border border-edge text-text hover:border-muted/60 hover:bg-raised",
        className,
      )}
    />
  );
}
