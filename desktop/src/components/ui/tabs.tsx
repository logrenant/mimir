import { motion } from "framer-motion";
import type { ReactNode } from "react";
import { cn } from "../../lib/cn";
import { RAIL } from "../../lib/motion";
import { Icon, type IconName } from "./icon";
import { Segmented, type Segment } from "./segmented";

export interface TabDef<K extends string> {
  key: K;
  label: ReactNode;
  icon?: IconName;
  /** Rendered after the label in mono — a row count, an open-session count. */
  count?: number;
  /** Draws the unsaved marker. `Settings` uses it per channel. */
  dirty?: boolean;
  disabled?: boolean;
}

/**
 * The tab strip, with the indicator that slides.
 *
 * Four of these existed — `Brain`'s underline, `Leadgen`'s view tabs and its
 * channel tabs (identical class strings, in the same file), and `Settings`'s
 * channel tabs with their unsaved dot. Three of the four were byte-identical.
 *
 * The indicator is a single element shared across the tabs by `layoutId`, so
 * changing tab moves one object rather than fading one out and another in.
 * That is the difference between an interface that switches and one that
 * *travels*, and it is the clearest thing in this design that says the surface
 * is a mechanism. It is also honest motion under the rule this app follows: it
 * happens because the operator clicked, and it shows what changed.
 *
 * The underline is Lime. It was Electric, which on Carbon is a dark grey line
 * two pixels tall — the selected tab and the unselected ones were, in practice,
 * the same tab.
 *
 * `variant="segmented"` is kept as a spelling of {@link Segmented} rather than
 * a second implementation here: six call sites use it, and a segmented control
 * is a recessed track with a travelling thumb, which is a different object from
 * a row of tabs under a rule.
 */
export function Tabs<K extends string>({
  tabs,
  active,
  onSelect,
  variant = "underline",
  /** Distinguishes the sliding indicator from every other strip on the screen.
   * Two strips sharing an id would hand the indicator between them. */
  id,
  className,
}: {
  tabs: TabDef<K>[];
  active: K;
  onSelect: (key: K) => void;
  variant?: "underline" | "segmented";
  id: string;
  className?: string;
}) {
  if (variant === "segmented") {
    return (
      <Segmented
        segments={tabs as Segment<K>[]}
        active={active}
        onSelect={onSelect}
        id={id}
        className={className}
      />
    );
  }

  return (
    <div role="tablist" className={cn("flex items-center gap-1 border-b border-edge", className)}>
      {tabs.map((tab) => {
        const on = tab.key === active;
        return (
          <button
            key={tab.key}
            type="button"
            role="tab"
            aria-selected={on}
            disabled={tab.disabled}
            onClick={() => onSelect(tab.key)}
            className={cn(
              "focus-ring-inset relative -mb-px inline-flex h-10 items-center gap-2 px-3.5",
              "text-base font-medium",
              "transition-colors duration-[var(--dur-fast)] ease-decisive",
              "disabled:cursor-not-allowed disabled:opacity-40",
              on ? "text-text" : "text-muted hover:text-text",
            )}
          >
            {tab.icon && <Icon name={tab.icon} size={15} className="relative" />}
            <span className="relative">{tab.label}</span>
            {tab.count !== undefined && (
              <span className="relative font-mono text-xs text-muted/70">{tab.count}</span>
            )}
            {tab.dirty && <span aria-hidden className="relative size-1.5 rounded-full bg-lime" />}
            {on && (
              <motion.span
                layoutId={`${id}-tab`}
                transition={RAIL}
                aria-hidden
                className="absolute inset-x-0 -bottom-px h-0.5 bg-lime"
              />
            )}
          </button>
        );
      })}
    </div>
  );
}
