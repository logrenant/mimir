import { useEffect, useRef, type ReactNode } from "react";
import { cn } from "../../lib/cn";
import { Icon, type IconName } from "./icon";

/**
 * A list of choices inside a {@link Popover}.
 *
 * The application picked things with a native `<select>` in four places, which
 * is right for "one of four models" and wrong the moment an option needs a
 * second line, an icon or a state — a `<select>` can hold a string and nothing
 * else. This is the shape for the rest: a menu of actions, or a picker whose
 * options carry a description.
 *
 * The keyboard model is the platform's, not an invention: Up and Down move,
 * Home and End jump, Enter and Space choose, and focus starts on the selected
 * item rather than the first one, so opening a picker and pressing Enter
 * changes nothing. `Popover` owns Escape.
 */
export function MenuList({
  label,
  className,
  children,
}: {
  label: string;
  className?: string;
  children: ReactNode;
}) {
  const list = useRef<HTMLDivElement>(null);

  // Focus lands on whichever item says it is current; failing that, the first.
  // Deferred by one frame because `Popover` measures before it paints and the
  // items do not exist during that pass.
  useEffect(() => {
    const frame = requestAnimationFrame(() => {
      const node = list.current;
      if (!node) return;
      const items = [...node.querySelectorAll<HTMLElement>('[role="menuitem"]:not([disabled])')];
      (items.find((el) => el.getAttribute("aria-current") === "true") ?? items[0])?.focus();
    });
    return () => cancelAnimationFrame(frame);
  }, []);

  const onKeyDown = (e: React.KeyboardEvent) => {
    const node = list.current;
    if (!node) return;
    const items = [...node.querySelectorAll<HTMLElement>('[role="menuitem"]:not([disabled])')];
    if (items.length === 0) return;
    const at = items.indexOf(document.activeElement as HTMLElement);

    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        items[(at + 1) % items.length].focus();
        break;
      case "ArrowUp":
        e.preventDefault();
        items[(at - 1 + items.length) % items.length].focus();
        break;
      case "Home":
        e.preventDefault();
        items[0].focus();
        break;
      case "End":
        e.preventDefault();
        items[items.length - 1].focus();
        break;
    }
  };

  return (
    <div
      ref={list}
      role="menu"
      aria-label={label}
      onKeyDown={onKeyDown}
      className={cn("flex flex-col gap-0.5 p-1.5", className)}
    >
      {children}
    </div>
  );
}

export function MenuItem({
  icon,
  title,
  detail,
  current = false,
  disabled = false,
  tone = "normal",
  onSelect,
}: {
  icon?: IconName;
  title: ReactNode;
  /** A second line. The reason a menu exists here rather than a `<select>`. */
  detail?: ReactNode;
  /** Marks the option already in effect. Focus opens on it. */
  current?: boolean;
  disabled?: boolean;
  tone?: "normal" | "danger";
  onSelect: () => void;
}) {
  return (
    <button
      type="button"
      role="menuitem"
      aria-current={current}
      disabled={disabled}
      onClick={onSelect}
      className={cn(
        "focus-ring-inset flex w-full items-center gap-2.5 rounded-md px-2.5 py-2 text-left",
        "transition-colors duration-[var(--dur-fast)] ease-decisive",
        "disabled:pointer-events-none disabled:opacity-40",
        tone === "danger"
          ? "text-bad hover:bg-bad/10"
          : current
            ? "bg-raised text-text"
            : "text-muted hover:bg-raised hover:text-text",
      )}
    >
      {icon && <Icon name={icon} size={15} className="mt-px" />}
      <span className="min-w-0 flex-1">
        <span className="block truncate text-base leading-tight">{title}</span>
        {detail && (
          <span className="mt-0.5 block truncate font-mono text-xs text-muted/70">{detail}</span>
        )}
      </span>
      {/* The tick, and only on the option in effect. A column of empty
          checkmark slots is how a menu of four options becomes a table. */}
      {current && <Icon name="check" size={14} className="text-lime" />}
    </button>
  );
}

/** A rule between groups of items. Not a heading — those are `MenuLabel`. */
export function MenuSeparator() {
  return <div role="separator" className="my-1 h-px bg-edge" />;
}

export function MenuLabel({ children }: { children: ReactNode }) {
  return <div className="label px-2.5 pt-2.5 pb-1.5 text-muted/70">{children}</div>;
}
