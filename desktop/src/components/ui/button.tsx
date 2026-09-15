import type { ButtonHTMLAttributes, ReactNode, Ref } from "react";
import { cn } from "../../lib/cn";
import { Icon, type IconName } from "./icon";

export type ButtonVariant = "primary" | "secondary" | "ghost" | "quiet" | "danger";
export type ButtonSize = "sm" | "md" | "lg";
export type ActiveAria = "pressed" | "expanded";

// `ref` is an ordinary prop on a function component in React 19, but it is not
// part of `ButtonHTMLAttributes` — it has to be declared. Every control that a
// `Popover` hangs from needs one, which since this pass is a great many of
// them: the daemon pill, every `Picker`, the account panel's help button.
type Base = Omit<ButtonHTMLAttributes<HTMLButtonElement>, "children"> & {
  ref?: Ref<HTMLButtonElement>;
};

type Props = Base & {
  variant?: ButtonVariant;
  size?: ButtonSize;
  /** Whether this control is currently on. See `ACTIVE` below. */
  active?: boolean;
  /** Which kind of "on" this is — the ARIA differs even though the look does
   * not. `"pressed"` for a setting that stays on, `"expanded"` for a control
   * that opens something. */
  activeAria?: ActiveAria;
  /** Swaps the leading glyph for a spinner and disables the button. The label
   * keeps its width, so a row of controls does not reflow mid-click. */
  loading?: boolean;
  /** Leading glyph. */
  icon?: IconName;
  /** Trailing glyph — a chevron on a control that opens something, an arrow on
   * one that leads somewhere. Never the same glyph as `icon`. */
  iconAfter?: IconName;
  children?: ReactNode;
};

/**
 * The button.
 *
 * ---------------------------------------------------------------------------
 * What changed.
 * ---------------------------------------------------------------------------
 * Every control in this application was a 10px all-caps Aldrich label inside a
 * 4px rectangle, on the reasoning that the brand reads as infrastructure and
 * infrastructure has square corners and a display face. Two things were wrong
 * with that.
 *
 * The first is legibility. Aldrich is a single geometric weight; at 10px, caps,
 * tracked out, in a product whose copy is half Turkish, "GÖRSEL EKLE" is
 * texture rather than words. A label is the one thing in an interface that has
 * to be read at a glance and it was the least readable text on the screen.
 *
 * The second is register. Small hard rectangles do not read as engineered, they
 * read as unfinished — the difference between an instrument and a form. The
 * instruments this product is measured against are softer than that and more
 * confident for it.
 *
 * So a label is Open Sans at 13px in sentence case, the corner is 10px, and
 * Aldrich keeps the jobs a display face is actually for: the masthead, the one
 * large figure, the wordmark.
 *
 * ---------------------------------------------------------------------------
 * The variants, and what each is for.
 * ---------------------------------------------------------------------------
 * `primary` is filled Lime with Carbon text, and it is the only filled control
 * in the system. Lime is bright enough on Carbon to be seen from across a
 * desk, which Electric at #2547e8 never was — that is why the previous palette
 * had two accents and produced screens with none.
 *
 * Exactly one of these belongs to a *context*: it is the answer to "what am I
 * here to do", and two answers is no answer. A screen is one context and a
 * dialog is another — but so is a card in a list of independent things, which
 * is why the board carries one filled "Çalıştır" per runnable card rather than
 * one for the whole board. The test is whether the operator is choosing
 * *between* them: they choose one card, then its verb.
 *
 * `secondary` is a filled surface with no accent, for the second action in a
 * pair — "cancel" beside "save". It is new; without it every non-primary
 * control was an outline, so a dialog's two buttons were an accent and a
 * hairline with nothing in between.
 *
 * `ghost` is an outline: a real control on a plain surface. `quiet` has no
 * chrome at all, for a control inside a surface that already has an edge — a
 * card header, a toolbar. `danger` is outlined and not filled, because filling
 * it red would put a second accent on a screen that already has one.
 *
 * `iconOnly` — a button with a glyph and no label — is available and requires
 * an `aria-label`, enforced by the type. Wrap it in `Tooltip` so the name is
 * there for the eye as well as the screen reader.
 */
export function Button({
  variant = "primary",
  size = "md",
  active,
  activeAria = "pressed",
  loading = false,
  icon,
  iconAfter,
  className,
  children,
  disabled,
  ref,
  ...props
}: Props) {
  return (
    <button
      {...props}
      ref={ref}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      {...ariaFor(active, activeAria)}
      className={cn(buttonClass(variant, size, active), className)}
    >
      {/* The spinner joins the label rather than replacing it. Swapping the
          label out leaves an empty lozenge that says only "wait", when the
          label already said what you are waiting for; and the button changes
          width mid-click, which is how you hit the one beside it. */}
      {loading ? <Spinner /> : icon && <Icon name={icon} size={GLYPH[size]} />}
      {children}
      {iconAfter && !loading && <Icon name={iconAfter} size={GLYPH[size]} />}
    </button>
  );
}

/**
 * A button that is only a glyph.
 *
 * A separate component rather than a prop, so the type can make `label`
 * mandatory. The previous file's doc comment said "an icon-only control in
 * this UI would have no accessible name" and solved that by having no icons;
 * this solves it by making the name impossible to omit.
 */
export function IconButton({
  name,
  label,
  variant = "quiet",
  size = "md",
  active,
  activeAria = "pressed",
  loading = false,
  className,
  disabled,
  ref,
  ...props
}: Base & {
  name: IconName;
  /** Both the accessible name and the tooltip text. Never optional. */
  label: string;
  variant?: ButtonVariant;
  size?: ButtonSize;
  active?: boolean;
  activeAria?: ActiveAria;
  loading?: boolean;
}) {
  return (
    <button
      {...props}
      ref={ref}
      aria-label={label}
      title={label}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      {...ariaFor(active, activeAria)}
      className={cn(buttonClass(variant, size, active), SQUARE[size], className)}
    >
      {loading ? <Spinner /> : <Icon name={name} size={GLYPH[size]} />}
    </button>
  );
}

/**
 * The class string on its own, for the places that need this shape on an
 * element that is not a `<button>`: the "open in WhatsApp" and "open in mail"
 * anchors in `Leadgen`, which must stay anchors because they carry an href the
 * operator may want to copy or open elsewhere.
 *
 * Exported rather than re-typed, because re-typing is exactly how those two
 * anchors drifted from this file in the first place.
 */
export function buttonClass(
  variant: ButtonVariant = "primary",
  size: ButtonSize = "md",
  active?: boolean,
): string {
  return cn(
    "inline-flex items-center justify-center gap-2 rounded-md font-medium whitespace-nowrap",
    "focus-ring select-none",
    "transition-[background-color,border-color,color,transform,box-shadow,opacity] duration-[var(--dur-fast)] ease-decisive",
    "active:translate-y-px",
    "disabled:pointer-events-none disabled:cursor-not-allowed disabled:opacity-40",
    SIZE[size],
    VARIANT[variant],
    active && ACTIVE,
  );
}

/**
 * The ARIA half of "on".
 *
 * The look is one thing and the announcement is two, and conflating them is
 * how a disclosure ends up telling a screen reader it is a toggle. `undefined`
 * rather than `false` when the caller passes no `active` at all, so a plain
 * button is not announced as an unpressed toggle.
 */
function ariaFor(active: boolean | undefined, kind: ActiveAria) {
  if (active === undefined) return {};
  return kind === "expanded" ? { "aria-expanded": active } : { "aria-pressed": active };
}

const SIZE: Record<ButtonSize, string> = {
  sm: "h-8 px-3 text-base",
  md: "h-9 px-4 text-base",
  lg: "h-11 px-5 text-lg",
};

/** The same heights with the horizontal padding removed. */
const SQUARE: Record<ButtonSize, string> = {
  sm: "w-8 px-0",
  md: "w-9 px-0",
  lg: "w-11 px-0",
};

const GLYPH: Record<ButtonSize, number> = { sm: 14, md: 15, lg: 17 };

/**
 * On.
 *
 * `desktop/AGENTS.md` already carried the rule — "a selected thing says so more
 * than once" — and the application did not, because there was nowhere to put
 * it. Two screens hand-wrote the same three-part recipe (`secondary` + a check
 * glyph + `aria-pressed`) and everything else said nothing at all: Katalog's
 * `sütunlar`, `alanlar` and `marka kimliği` looked identical whether their
 * panel was open or shut, which is what an operator reported.
 *
 * So it says it three times, like the nav row does. The surface fills, the
 * label goes to full Mist, and a 2px Lime bar sits along the bottom inside the
 * corner — the same mark `ui/tabs` draws under the tab that is on, because
 * these are the same claim about the same kind of control. The bar rather than
 * a left rail: a rail is for a row in a vertical list, and a row of buttons is
 * horizontal.
 *
 * It overrides the variant's own surface on purpose. `quiet` is the variant a
 * toolbar button wears, and `quiet` has no surface at all — so an active
 * `quiet` that kept its own background would be saying "on" in the label alone.
 */
const ACTIVE =
  "relative bg-raised text-text shadow-elev-1 hover:bg-raised " +
  "after:pointer-events-none after:absolute after:inset-x-2 after:bottom-0 " +
  "after:h-0.5 after:rounded-full after:bg-lime after:content-['']";

const VARIANT: Record<ButtonVariant, string> = {
  // The one filled control, and the only thing that lifts: a screen has
  // exactly one of these, so the gesture stays an event rather than a tic.
  //
  // Disabled, it stops being Lime rather than becoming translucent Lime. The
  // shared `disabled:opacity-40` is right for an outline and wrong for a fill:
  // Lime at forty per cent over Carbon is olive, which does not read as "off",
  // it reads as a colour that has gone wrong. Brain's "Kaydet" sat there in it
  // whenever there was nothing to save.
  primary:
    "bg-lime text-lime-ink shadow-elev-1 hover:-translate-y-px hover:bg-lime/90 active:translate-y-0 disabled:bg-raised disabled:text-text disabled:shadow-none",
  // A filled surface with no accent. The second half of a pair.
  secondary: "bg-raised text-text hover:bg-overlay",
  ghost: "border border-edge text-text hover:border-edge-strong hover:bg-raised",
  // No chrome at all, for a control inside a surface that already has an edge.
  quiet: "text-muted hover:bg-raised hover:text-text",
  // Outlined, not filled. A destructive action should be legible, not loud.
  danger: "border border-bad/50 text-bad hover:border-bad hover:bg-bad/10",
};

/**
 * The application's only spinner.
 *
 * Every loading state in this app used to be a text string — "…",
 * "yükleniyor…", "Aranıyor…", "Kaydediliyor…" — which reads as the label having
 * changed rather than as the control being busy. This is a real timer, and it
 * is the single permitted exception to "motion reports a real value": the
 * daemon does not report progress on these calls, so there is no value to
 * report. It exists only while a request is actually in flight.
 */
export function Spinner({ className }: { className?: string }) {
  return (
    <svg
      aria-hidden
      viewBox="0 0 16 16"
      className={cn("size-3.5 shrink-0 animate-spin", className)}
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
    >
      <circle cx="8" cy="8" r="6" className="opacity-25" />
      <path d="M14 8a6 6 0 0 0-6-6" strokeLinecap="round" />
    </svg>
  );
}
