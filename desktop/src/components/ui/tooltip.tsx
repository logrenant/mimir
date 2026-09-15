import { useId, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { cn } from "../../lib/cn";
import { place } from "../../lib/popover";

/**
 * The name of a control that shows only a glyph.
 *
 * This exists because icons do. Every control in this application used to
 * carry a word, which is why a card's action row was three links wide; now
 * that "stop" is a square and "open" is an arrow, the word has to live
 * somewhere, and `aria-label` alone serves the screen reader while leaving a
 * sighted operator to guess.
 *
 * Deliberately thin: no arrow, no delay group, no interactive content. A
 * tooltip that can be hovered into is a popover wearing the wrong clothes, and
 * this one is unreachable by the pointer on purpose.
 *
 * Keyboard focus opens it too — that is the half usually missing, and it is
 * the half that matters for someone tabbing through a toolbar of glyphs.
 */
export function Tooltip({
  label,
  side = "bottom",
  children,
}: {
  label: string;
  side?: "top" | "bottom";
  children: ReactNode;
}) {
  const wrap = useRef<HTMLSpanElement>(null);
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null);
  const id = useId();

  const show = () => {
    const trigger = wrap.current?.getBoundingClientRect();
    if (!trigger) return;
    // Measured off the label's own length rather than a real box: the tip is
    // one line of 11px text and rendering it twice to measure it would cost a
    // frame for a two-pixel improvement in centring.
    const width = Math.min(240, label.length * 6 + 20);
    const next = place(
      { left: trigger.left, top: trigger.top, width: trigger.width, height: trigger.height },
      { width, height: 24 },
      { width: window.innerWidth, height: window.innerHeight },
      { side, align: "center", gap: 6 },
    );
    setPos({ left: next.left, top: next.top });
  };

  const hide = () => setPos(null);

  return (
    <>
      <span
        ref={wrap}
        // `contents` so wrapping a button does not add a box that breaks the
        // flex row it was laid out in.
        className="contents"
        onPointerEnter={show}
        onPointerLeave={hide}
        onFocusCapture={show}
        onBlurCapture={hide}
        aria-describedby={pos ? id : undefined}
      >
        {children}
      </span>
      {pos &&
        createPortal(
          <span
            id={id}
            role="tooltip"
            style={{ position: "fixed", left: pos.left, top: pos.top }}
            className={cn(
              "pointer-events-none z-70 rounded-sm bg-overlay px-2 py-1",
              "text-xs whitespace-nowrap text-text shadow-elev-2 outline outline-edge-strong/70",
            )}
          >
            {label}
          </span>,
          document.body,
        )}
    </>
  );
}
