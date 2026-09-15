import type {
  InputHTMLAttributes,
  ReactNode,
  Ref,
  SelectHTMLAttributes,
  TextareaHTMLAttributes,
} from "react";
import { cn } from "../../lib/cn";
import { Icon } from "./icon";

/**
 * The text controls.
 *
 * Six input recipes and five `<select>` recipes were in use, disagreeing about
 * corner, fill and — the one that actually matters — whether focus was marked
 * at all: `NewTaskOverlay`'s and `ModelSelect`'s did nothing whatsoever, so a
 * keyboard user tabbing through the new-task dialog could not tell where they
 * were.
 *
 * One recipe, and the fill is the part that changed. These used to sit on
 * `ground` — the same tone as the page behind them — so an input was a
 * rectangle of nothing with a hairline around it, indistinguishable from a
 * disabled readout. They are `sunken` now: cut *into* the surface rather than
 * drawn on it. A recessed box says "put something here" before any placeholder
 * has been read, which is why every composer worth copying is recessed.
 *
 * Focus does both halves: the border goes Electric *and* the ring appears. The
 * border alone is a 1px change that is easy to miss against a hairline UI; the
 * ring alone floats free of the control it belongs to. Electric and not Lime,
 * because Lime now means "this is the action" and a focused field is not one.
 *
 * **`className` on an `Input` cannot set its width.** The base below carries
 * `w-full`, and `cn()` is a join rather than a merge, so a caller's `w-20`
 * and this file's `w-full` both reach the class list and the cascade decides —
 * `w-full` wins. Lead-gen's search row rendered as three stacked full-width
 * boxes for exactly this reason, including a "count" field a thousand pixels
 * across. Put the width on a wrapper; `Select` below already takes its
 * `className` that way, and its doc comment says why.
 */
const BASE = cn(
  "w-full rounded-md border border-edge bg-sunken text-text",
  "placeholder:text-muted/60",
  "transition-[border-color] duration-[var(--dur-fast)] ease-decisive",
  "focus-ring focus:border-electric",
  "disabled:cursor-not-allowed disabled:opacity-50",
);

// `ref` is an ordinary prop on a function component in React 19, so these need
// no forwardRef — only the type saying so. `QuickTask` focuses its textarea on
// summon, which is the whole point of that window.
export function Input({
  className,
  ref,
  ...props
}: InputHTMLAttributes<HTMLInputElement> & { ref?: Ref<HTMLInputElement> }) {
  return <input {...props} ref={ref} className={cn(BASE, "h-9 px-3 text-base", className)} />;
}

export function Textarea({
  className,
  ref,
  ...props
}: TextareaHTMLAttributes<HTMLTextAreaElement> & { ref?: Ref<HTMLTextAreaElement> }) {
  return (
    <textarea
      {...props}
      ref={ref}
      className={cn(BASE, "resize-y px-3.5 py-3 font-mono text-sm leading-[1.7]", className)}
    />
  );
}

/**
 * A native `<select>`, kept native.
 *
 * A custom listbox would need a portal, a focus trap and its own keyboard
 * model, and would lose the platform's own type-ahead — for a control whose
 * whole job here is picking one of four models. Where an option needs a second
 * line or a state, `Popover` + `MenuList` is the shape instead; this one styles
 * a list of strings and never holds one.
 */
export function Select({ className, children, ...props }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    // The class lands on the wrapper, not on the `<select>`: sizing is the
    // caller's business and the chevron has to move with the right edge, so a
    // `max-w-*` applied to the inner element alone would leave it behind.
    <div className={cn("relative inline-flex w-full items-center", className)}>
      <select {...props} className={cn(BASE, "h-9 appearance-none pr-8 pl-3 text-base")}>
        {children}
      </select>
      <Icon
        name="chevronDown"
        size={14}
        className="pointer-events-none absolute right-2.5 text-muted"
      />
    </div>
  );
}

/**
 * A radio, drawn for the same reason `Checkbox` is.
 *
 * `accent-color` gets the hue right and leaves the browser's border weight and
 * corner treatment, which next to this type and these corners reads as a
 * control from a different application. The real input stays — it is what
 * keyboard users and screen readers operate — under a span we draw.
 */
export function Radio({
  checked,
  label,
  className,
  ...props
}: Omit<InputHTMLAttributes<HTMLInputElement>, "type"> & { label: ReactNode }) {
  return (
    <label
      className={cn(
        "flex cursor-pointer items-center gap-3 rounded-md px-2.5 py-2",
        "transition-colors duration-[var(--dur-fast)] hover:bg-raised",
        props.disabled && "cursor-not-allowed opacity-50",
        className,
      )}
    >
      <span className="relative inline-flex size-4 shrink-0 items-center justify-center">
        <input
          {...props}
          type="radio"
          checked={checked}
          className="peer absolute inset-0 size-full cursor-pointer opacity-0 disabled:cursor-not-allowed"
        />
        <span
          aria-hidden
          className={cn(
            "flex size-4 items-center justify-center rounded-full border",
            "transition-colors duration-[var(--dur-fast)]",
            "peer-focus-visible:outline-2 peer-focus-visible:outline-offset-2 peer-focus-visible:outline-electric",
            checked ? "border-lime" : "border-edge-strong peer-hover:border-muted",
          )}
        >
          {checked && <span className="size-1.5 rounded-full bg-lime" />}
        </span>
      </span>
      <span className="min-w-0 text-base">{label}</span>
    </label>
  );
}

/**
 * The small caps caption above a control.
 *
 * `.label` already exists as a class; this pairs it with the control and gives
 * the gap one value instead of the four that were in use.
 */
export function FieldLabel({
  children,
  htmlFor,
  className,
}: {
  children: ReactNode;
  htmlFor?: string;
  className?: string;
}) {
  return (
    <label htmlFor={htmlFor} className={cn("label block text-muted", className)}>
      {children}
    </label>
  );
}
