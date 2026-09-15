import { cn } from "../../lib/cn";

/**
 * A switch: one setting, on or off.
 *
 * It is a second control rather than a widened `Checkbox` because the two say
 * different things. A checkbox is a selection inside a set — that one carries a
 * shift-range gesture and a third indeterminate state for exactly that reason,
 * and it sits in a table header over rows. A switch is a property of one thing
 * that stays that way after the operator looks away, and here it answers "may a
 * rewrite change this field". Overloading the box would give a settings row a
 * range gesture and a dash state that mean nothing to it.
 *
 * The usual rule about switches is that they apply immediately, and this one
 * does not: the field configuration is saved as a set, because the fields a
 * catalog offers depend on the file and a half-applied set is a rewrite writing
 * into a column nobody meant. The cost of breaking that rule is that a person
 * can walk away believing a flip took effect, so the panel that owns these has
 * to show its unsaved state — see the reviewer note in AGENTS.md.
 *
 * Same construction as `Checkbox` and for the same reason: the real input stays
 * because it is what keyboards and screen readers operate, and a span we draw
 * covers it so the shape belongs to this application rather than to the browser.
 */
export function Switch({
  checked,
  onChange,
  disabled,
  label,
  className,
}: {
  checked: boolean;
  onChange: (next: boolean) => void;
  disabled?: boolean;
  /** The accessible name. Rendered by the caller when there is a visible one. */
  label: string;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "relative inline-flex h-4 w-7 shrink-0 items-center",
        className,
      )}
    >
      <input
        type="checkbox"
        role="switch"
        aria-label={label}
        aria-checked={checked}
        checked={checked}
        disabled={disabled}
        onChange={(e) => onChange(e.target.checked)}
        className="peer absolute inset-0 size-full cursor-pointer opacity-0 disabled:cursor-not-allowed"
      />
      <span
        aria-hidden
        className={cn(
          // Fully round is the shape that makes a switch a switch, so it is the
          // one place here that does not take a step off the corner ramp.
          "pointer-events-none flex h-4 w-7 items-center rounded-full border px-[2px]",
          "transition-colors duration-[var(--dur-fast)] ease-decisive",
          "peer-focus-visible:outline-2 peer-focus-visible:outline-offset-2 peer-focus-visible:outline-electric",
          "peer-disabled:opacity-35",
          checked
            ? "justify-end border-lime bg-lime"
            : "justify-start border-edge-strong bg-sunken peer-hover:border-muted",
        )}
      >
        <span
          className={cn(
            "size-3 rounded-full transition-colors duration-[var(--dur-fast)] ease-decisive",
            checked ? "bg-lime-ink" : "bg-edge-strong",
          )}
        />
      </span>
    </span>
  );
}
