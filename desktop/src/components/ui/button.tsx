import type { ButtonHTMLAttributes } from "react";
import { cn } from "../../lib/cn";

type Props = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "primary" | "ghost";
};

export function Button({ variant = "primary", className, ...props }: Props) {
  return (
    <button
      {...props}
      className={cn(
        "inline-flex items-center justify-center gap-2 rounded-md px-3 py-1.5 text-sm font-medium transition-colors",
        "disabled:cursor-not-allowed disabled:opacity-40",
        variant === "primary"
          ? "bg-accent text-ink hover:bg-accent/85"
          : "border border-edge text-text hover:bg-edge/60",
        className,
      )}
    />
  );
}
