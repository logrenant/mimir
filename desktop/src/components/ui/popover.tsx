import { AnimatePresence, motion } from "framer-motion";
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
  type RefObject,
} from "react";
import { createPortal } from "react-dom";
import { cn } from "../../lib/cn";
import { TR, useMotion } from "../../lib/motion";
import { place, type Align, type Side } from "../../lib/popover";

/**
 * A panel anchored to the control that opened it.
 *
 * This is the piece the application was missing, and its absence is why the
 * title bar's connection pill opened a *modal*: with only `Overlay` available,
 * "show me what the daemon is doing" dimmed the entire application, took over
 * the middle of the screen, and had to be dismissed before work could resume —
 * for a reading that fits in a paragraph. A status check is not a decision and
 * should not be staged like one.
 *
 * So the two are now separated by what they interrupt. `Overlay` is for
 * something the operator must finish or abandon; `Popover` is for something
 * they are looking *at*, beside the thing that produced it, dismissed by
 * looking away.
 *
 * It renders through a portal because the shell's panes are `overflow-hidden`
 * grid cells — a panel that stayed in the tree would be clipped by the title
 * bar it hangs from — and it is `position: fixed` because that is the
 * coordinate space `place()` computes in.
 */
export function Popover({
  open,
  onClose,
  anchor,
  side = "bottom",
  align = "start",
  width = 300,
  label,
  className,
  children,
}: {
  open: boolean;
  onClose: () => void;
  /** The control this hangs from. Its rect decides the position. */
  anchor: RefObject<HTMLElement | null>;
  side?: Side;
  align?: Align;
  width?: number;
  /** The accessible name. A dialog without one is announced as "dialog". */
  label: string;
  className?: string;
  children: ReactNode;
}) {
  const panel = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState<{ left: number; top: number; maxHeight: number } | null>(null);
  const variants = useMotion(POP);

  const measure = useCallback(() => {
    const trigger = anchor.current?.getBoundingClientRect();
    const box = panel.current?.getBoundingClientRect();
    if (!trigger || !box) return;
    const next = place(
      { left: trigger.left, top: trigger.top, width: trigger.width, height: trigger.height },
      { width, height: box.height },
      { width: window.innerWidth, height: window.innerHeight },
      { side, align },
    );
    setPos({ left: next.left, top: next.top, maxHeight: next.maxHeight });
  }, [anchor, align, side, width]);

  // Before paint, so the panel is never seen at 0,0 on its first frame.
  useLayoutEffect(() => {
    if (!open) {
      setPos(null);
      return;
    }
    measure();
  }, [open, measure]);

  // A popover's own content can change height while it is open — the daemon
  // answers and a "checking…" line becomes four dependency rows — and the
  // window can be resized under it. Both move where it belongs.
  useEffect(() => {
    if (!open || !panel.current) return;
    const observer = new ResizeObserver(measure);
    observer.observe(panel.current);
    window.addEventListener("resize", measure);
    window.addEventListener("scroll", measure, true);
    return () => {
      observer.disconnect();
      window.removeEventListener("resize", measure);
      window.removeEventListener("scroll", measure, true);
    };
  }, [open, measure]);

  useEffect(() => {
    if (!open) return;

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      e.stopPropagation();
      onClose();
      // Escape returns the keyboard to the control that opened this. Without
      // it the next Tab starts from the top of the application.
      anchor.current?.focus?.();
    };

    // Pointer-down and not click: a click that begins inside the panel and
    // ends outside it — the end of a text selection — must not dismiss.
    const onPointerDown = (e: PointerEvent) => {
      const target = e.target as Node;
      if (panel.current?.contains(target) || anchor.current?.contains(target)) return;
      onClose();
    };

    window.addEventListener("keydown", onKeyDown, true);
    window.addEventListener("pointerdown", onPointerDown, true);
    return () => {
      window.removeEventListener("keydown", onKeyDown, true);
      window.removeEventListener("pointerdown", onPointerDown, true);
    };
  }, [open, onClose, anchor]);

  return createPortal(
    <AnimatePresence>
      {open && (
        <motion.div
          ref={panel}
          variants={variants}
          initial="hidden"
          animate="shown"
          exit="gone"
          role="dialog"
          aria-label={label}
          style={{
            position: "fixed",
            width,
            left: pos?.left ?? 0,
            top: pos?.top ?? 0,
            maxHeight: pos?.maxHeight,
            // Hidden rather than unmounted until the first measurement: the
            // panel has to be in the document to have a height at all.
            visibility: pos ? "visible" : "hidden",
          }}
          className={cn(
            "z-60 overflow-y-auto rounded-lg bg-overlay shadow-elev-2",
            "outline outline-edge-strong/70",
            className,
          )}
        >
          {children}
        </motion.div>
      )}
    </AnimatePresence>,
    document.body,
  );
}

/**
 * It steps out of its trigger rather than growing from nothing: 2px of travel
 * and a scale of 0.98, which is enough to say where it came from and not
 * enough to read as an animation.
 */
const POP = {
  hidden: { opacity: 0, scale: 0.98, y: -4 },
  shown: { opacity: 1, scale: 1, y: 0, transition: TR.fast },
  gone: { opacity: 0, scale: 0.98, y: -2, transition: TR.fast },
};

/**
 * The parts a popover is usually built from — a titled head, a body with the
 * padding every one of them wants, and a footer that escalates.
 *
 * Separate exports rather than props on `Popover` because a menu wants none of
 * them and a status panel wants all three.
 */
export function PopoverHead({ title, aside }: { title: ReactNode; aside?: ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-3 px-4 pt-3.5 pb-2.5">
      <span className="label text-muted">{title}</span>
      {aside}
    </div>
  );
}

export function PopoverBody({
  className,
  /** Drops the bottom padding, for a body a {@link PopoverFoot} sits under. */
  flush = false,
  children,
}: {
  className?: string;
  flush?: boolean;
  children: ReactNode;
}) {
  return <div className={cn("px-4", flush ? "pb-2.5" : "pb-3.5", className)}>{children}</div>;
}

export function PopoverFoot({ children }: { children: ReactNode }) {
  return (
    <div className="flex items-center justify-end gap-2 border-t border-edge px-4 py-2.5">
      {children}
    </div>
  );
}
