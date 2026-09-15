import { useRef, useState, type ReactNode } from "react";
import { cn } from "../../lib/cn";
import { Icon, type IconName } from "./icon";
import { MenuItem, MenuLabel, MenuList } from "./menu";
import { Popover } from "./popover";

export interface Choice<V extends string> {
  value: V;
  label: ReactNode;
  /** The second line — what this option actually means. */
  detail?: ReactNode;
  icon?: IconName;
  disabled?: boolean;
}

/**
 * One of a known set, chosen from a menu.
 *
 * `ui/field`'s `Select` stays for what a native `<select>` is genuinely good
 * at, and this exists for what it is not. A `<select>` option holds a string:
 * no second line, no state, no glyph. So every picker in this application that
 * had something to say about its options said it *underneath* the control
 * instead — `AgentSelect` renders the chosen agent's description and its skill
 * badges below itself, `ProviderModelPicker` puts "· varsayılan" inside an
 * option label, `ModelSelect` appends "— varsayılan" to a string. All three are
 * the same missing feature worked around three ways: the description belongs
 * beside the option it describes, at the moment you are choosing between them.
 *
 * The trigger reads as a field rather than as a button — same height, same
 * recessed fill, same focus treatment as `Input` — because that is what it is
 * standing in for and a form of mismatched controls is the thing this whole
 * pass exists to stop.
 */
export function Picker<V extends string>({
  choices,
  value,
  onChange,
  /** The accessible name for the menu, and the empty state's text. */
  label,
  /** Shown when nothing is chosen and there is no matching choice. */
  placeholder = "seçin",
  /** A heading above the list. */
  heading,
  disabled = false,
  width,
  className,
}: {
  choices: Choice<V>[];
  value: V | "";
  onChange: (value: V) => void;
  label: string;
  placeholder?: ReactNode;
  heading?: ReactNode;
  disabled?: boolean;
  /** Menu width. Defaults to the trigger's own width, which is usually right. */
  width?: number;
  className?: string;
}) {
  const trigger = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  const chosen = choices.find((c) => c.value === value);

  return (
    <>
      <button
        ref={trigger}
        type="button"
        disabled={disabled}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((was) => !was)}
        className={cn(
          "focus-ring flex h-9 w-full items-center gap-2 rounded-md border border-edge bg-sunken px-3",
          "text-left text-base transition-[border-color] duration-[var(--dur-fast)] ease-decisive",
          "hover:border-edge-strong",
          "disabled:cursor-not-allowed disabled:opacity-50",
          open && "border-electric",
          className,
        )}
      >
        {chosen?.icon && <Icon name={chosen.icon} size={15} className="text-muted" />}
        <span className={cn("min-w-0 flex-1 truncate", chosen ? "text-text" : "text-muted/60")}>
          {chosen?.label ?? placeholder}
        </span>
        <Icon name="chevronDown" size={14} className="text-muted" />
      </button>

      <Popover
        open={open}
        onClose={() => setOpen(false)}
        anchor={trigger}
        label={label}
        width={width ?? trigger.current?.offsetWidth ?? 280}
      >
        {heading && <MenuLabel>{heading}</MenuLabel>}
        <MenuList label={label}>
          {choices.map((choice) => (
            <MenuItem
              key={choice.value}
              icon={choice.icon}
              title={choice.label}
              detail={choice.detail}
              current={choice.value === value}
              disabled={choice.disabled}
              onSelect={() => {
                onChange(choice.value);
                setOpen(false);
                trigger.current?.focus();
              }}
            />
          ))}
        </MenuList>
      </Popover>
    </>
  );
}
