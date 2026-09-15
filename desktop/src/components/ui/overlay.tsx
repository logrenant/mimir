import { AnimatePresence, motion } from "framer-motion";
import { useEffect, useId, useRef, type ReactNode } from "react";
import { cn } from "../../lib/cn";
import { dialog, scrim, useMotion } from "../../lib/motion";
import { IconButton } from "./button";

/**
 * The modal.
 *
 * Three existed — `NewTaskOverlay`, `Dashboard#RunDetailOverlay`,
 * `Dashboard#DiagnosticsOverlay` — hand-built, near-identical, and none of them
 * a dialog in any sense a browser understands: no `role`, no `aria-modal`, no
 * focus trap, and only one of the three handled Escape. Tab from inside any of
 * them and you were walking the board underneath, invisibly.
 *
 * That accessibility work is untouched below and should stay untouched: focus
 * moves in on open and returns to whatever opened it on close, Tab and
 * Shift-Tab wrap inside, Escape closes, the page behind is inert.
 *
 * What this is *for* narrowed, though. It used to be the only floating surface
 * in the application, so it was also carrying jobs that are not decisions —
 * "what is the daemon doing" dimmed the whole screen and had to be dismissed
 * before work resumed. Those are `Popover`'s now. This is for something the
 * operator must finish or abandon, and staging it like an event is correct
 * only when it is one.
 */
export function Overlay({
  open,
  onClose,
  title,
  subtitle,
  aside,
  footer,
  width = 560,
  className,
  children,
}: {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  subtitle?: ReactNode;
  /** Extra controls in the header, beside the close button. */
  aside?: ReactNode;
  footer?: ReactNode;
  width?: number;
  className?: string;
  children: ReactNode;
}) {
  const panel = useRef<HTMLDivElement>(null);
  const opener = useRef<HTMLElement | null>(null);
  const labelId = useId();
  const scrimV = useMotion(scrim);
  const dialogV = useMotion(dialog);

  // Focus in on open, and back out to the control that opened it on close. The
  // return trip is the half that is usually missing: without it, dismissing a
  // dialog drops focus onto <body> and the next Tab starts from the top of the
  // application.
  useEffect(() => {
    if (!open) return;
    opener.current = document.activeElement as HTMLElement | null;
    const first = panel.current?.querySelector<HTMLElement>(FOCUSABLE);
    (first ?? panel.current)?.focus();
    return () => opener.current?.focus?.();
  }, [open]);

  useEffect(() => {
    if (!open) return;

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        onClose();
        return;
      }
      if (e.key !== "Tab" || !panel.current) return;

      const stops = [...panel.current.querySelectorAll<HTMLElement>(FOCUSABLE)].filter(
        (el) => el.offsetParent !== null,
      );
      if (stops.length === 0) return;

      const first = stops[0];
      const last = stops[stops.length - 1];
      // Wrapping is done by hand because there is no inert-everything-else in
      // this WebView's baseline: without it Tab leaves the panel silently.
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    };

    window.addEventListener("keydown", onKeyDown, true);
    return () => window.removeEventListener("keydown", onKeyDown, true);
  }, [open, onClose]);

  return (
    <AnimatePresence>
      {open && (
        <motion.div
          variants={scrimV}
          initial="hidden"
          animate="shown"
          exit="gone"
          onMouseDown={onClose}
          // Translucent and blurred rather than an opaque cover: the board
          // stays legible underneath, because it is still the thing being
          // worked on. An opaque scrim reads as having navigated somewhere.
          className="absolute inset-0 z-50 grid place-items-center bg-carbon/78 p-8 backdrop-blur-md"
        >
          <motion.div
            ref={panel}
            variants={dialogV}
            initial="hidden"
            animate="shown"
            exit="gone"
            role="dialog"
            aria-modal="true"
            aria-labelledby={labelId}
            tabIndex={-1}
            // The scrim closes on mousedown; the panel must not, or a text
            // selection that ends outside the panel would dismiss it.
            onMouseDown={(e) => e.stopPropagation()}
            style={{ width, maxWidth: "100%" }}
            className={cn(
              "flex max-h-[84vh] flex-col overflow-hidden rounded-xl bg-panel shadow-elev-3 outline-none",
              "outline outline-edge-strong/60",
              className,
            )}
          >
            <div className="flex items-start justify-between gap-4 px-6 pt-5 pb-4">
              <div className="min-w-0">
                <h2 id={labelId} className="display truncate text-xl">
                  {title}
                </h2>
                {subtitle && <p className="mt-1.5 text-sm text-muted">{subtitle}</p>}
              </div>
              <div className="flex shrink-0 items-center gap-1.5">
                {aside}
                <IconButton name="close" label="Kapat" size="sm" onClick={onClose} />
              </div>
            </div>

            <div className="min-h-0 flex-1 overflow-y-auto px-6 pb-5">{children}</div>

            {footer && (
              <div className="flex items-center justify-end gap-2.5 border-t border-edge px-6 py-4">
                {footer}
              </div>
            )}
          </motion.div>
        </motion.div>
      )}
    </AnimatePresence>
  );
}

const FOCUSABLE =
  'a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])';
