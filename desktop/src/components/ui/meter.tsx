import { cn } from "../../lib/cn";

/**
 * A proportion, drawn.
 *
 * Two of these existed and neither was reusable: `Leadgen`'s category rail drew
 * a 2px bar inline, and `Brain` reported its layout progress as the string
 * "%73". A number is not a proportion — the point of a share is that you can
 * compare two of them without reading either.
 *
 * Lime at the head rather than a solid fill: the guide gives Lime 2% of a
 * surface, and a full bar of it on every row of a forty-row rail would spend
 * that many times over. The fill is a Mist tint; Lime marks where it has
 * reached.
 */
export function Meter({
  value,
  label,
  tone = "lime",
  className,
}: {
  /** 0–1. Values outside the range are clamped, because callers divide by
   * totals that can be zero. */
  value: number;
  /** The accessible name. A bar with no name is decoration. */
  label: string;
  tone?: "lime" | "electric";
  className?: string;
}) {
  const share = Number.isFinite(value) ? Math.min(1, Math.max(0, value)) : 0;
  return (
    <div
      role="progressbar"
      aria-label={label}
      aria-valuenow={Math.round(share * 100)}
      aria-valuemin={0}
      aria-valuemax={100}
      className={cn("relative h-1 w-full overflow-hidden rounded-full bg-sunken", className)}
    >
      <div
        className="h-full rounded-full bg-muted/40 transition-[width] duration-[var(--dur-base)] ease-decisive"
        style={{ width: `${share * 100}%` }}
      >
        {/* The head. Two pixels of accent per bar, which is what 2% of a
            surface actually looks like. */}
        <span
          aria-hidden
          className={cn(
            "absolute top-0 h-full w-1.5 rounded-full",
            tone === "lime" ? "bg-lime" : "bg-electric",
            share === 0 && "hidden",
          )}
          style={{ left: `calc(${share * 100}% - 4px)` }}
        />
      </div>
    </div>
  );
}
