import { useEffect, useRef } from "react";
import { cn } from "../../lib/cn";

/**
 * A tick box, built rather than borrowed.
 *
 * `accent-color` on a native box gets the hue right and nothing else: the
 * corner radius, the border weight and the unchecked state are the browser's,
 * and next to Aldrich caps and hairline edges that reads as a control from a
 * different application. So the real input stays — it is what keyboard users
 * and screen readers actually operate — and is covered by a span we draw.
 *
 * Three states, because a header box over a filtered table has three honest
 * things to say: none of these rows, some of them, all of them. A header that
 * could only be on or off would show "off" while forty rows below it were
 * ticked.
 */
export function Checkbox({
  checked,
  indeterminate = false,
  onChange,
  disabled,
  label,
  className,
}: {
  checked: boolean;
  /** Draws the dash. Ignored when `checked` — a box cannot be both. */
  indeterminate?: boolean;
  /**
   * `shiftKey` is the gesture that started the change, not a second event.
   *
   * A range selection has to know whether shift was down, and by the time
   * `change` fires there is nowhere left to read it: a checkbox's change event
   * carries no modifiers. Cancelling the native toggle to handle the click
   * ourselves is worse — for a checkbox the activation has already fired
   * `change` by then, so it toggles once for us and once for the browser.
   * Recording the modifier on the way in and letting the natural change do the
   * work is the version with exactly one toggle in it.
   */
  onChange: (next: boolean, shiftKey: boolean) => void;
  disabled?: boolean;
  /** The accessible name. Rendered off-screen when the box stands alone. */
  label: string;
  className?: string;
}) {
  const ref = useRef<HTMLInputElement>(null);
  const shift = useRef(false);

  // `indeterminate` is a property, not an attribute: React cannot set it from
  // JSX and it is reset by every re-render that touches `checked`.
  useEffect(() => {
    if (ref.current) ref.current.indeterminate = !checked && indeterminate;
  }, [checked, indeterminate]);

  return (
    <span className={cn("relative inline-flex size-4 shrink-0 items-center justify-center", className)}>
      <input
        ref={ref}
        type="checkbox"
        aria-label={label}
        checked={checked}
        disabled={disabled}
        onPointerDown={(e) => (shift.current = e.shiftKey)}
        onKeyDown={(e) => (shift.current = e.shiftKey)}
        onChange={(e) => onChange(e.target.checked, shift.current)}
        className="peer absolute inset-0 size-full cursor-pointer opacity-0 disabled:cursor-not-allowed"
      />
      <span
        aria-hidden
        className={cn(
          "pointer-events-none flex size-4 items-center justify-center rounded-[3px] border transition-colors",
          // The focus ring is on the drawn box, driven by the real input's
          // focus — losing the outline is the usual cost of hiding a native
          // control, and it is the thing keyboard users navigate by.
          "peer-focus-visible:ring-2 peer-focus-visible:ring-electric/70 peer-focus-visible:ring-offset-1 peer-focus-visible:ring-offset-ground",
          "peer-disabled:opacity-35",
          checked || indeterminate
            ? "border-electric bg-electric text-mist"
            : "border-edge bg-ground peer-hover:border-muted/70",
        )}
      >
        {checked ? (
          // A path rather than a glyph: "✓" is a font's opinion, and at 16px
          // the two differ enough to look misaligned in one of them.
          <svg viewBox="0 0 12 12" className="size-3" fill="none" stroke="currentColor" strokeWidth="2">
            <path d="M2.5 6.2 4.8 8.5 9.5 3.8" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        ) : indeterminate ? (
          <span className="h-0.5 w-2 rounded-full bg-mist" />
        ) : null}
      </span>
    </span>
  );
}
