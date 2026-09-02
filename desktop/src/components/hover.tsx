import { useState, type CSSProperties } from "react";

/**
 * The inline-style plumbing the shell was written with.
 *
 * Lifted out of `Dashboard.tsx` unchanged so the dashboard and the board can
 * share it rather than grow a second copy. It is not the destination — the
 * design tokens in `index.css` are — but moving every surface at once is its
 * own task, and a duplicated hover helper would make that task harder.
 */

/**
 * Parses a `"prop:value;prop:value"` CSS string into a React style object.
 *
 * `border` is expanded into its width/style/color longhand so it never mixes
 * with a hover delta's `border-color` — React warns when a shorthand and its
 * longhand are both set across a rerender, and the color can go stale.
 */
export function css(source: string): CSSProperties {
  const out: Record<string, string> = {};
  for (const decl of source.split(";")) {
    const i = decl.indexOf(":");
    if (i === -1) continue;
    const prop = decl.slice(0, i).trim();
    const value = decl.slice(i + 1).trim();
    if (!prop || !value) continue;
    if (prop === "border") {
      const parts = value.split(/\s+/);
      if (parts.length === 3) {
        out.borderWidth = parts[0];
        out.borderStyle = parts[1];
        out.borderColor = parts[2];
        continue;
      }
    }
    const camel = prop.replace(/-([a-z])/g, (_, c: string) => c.toUpperCase());
    out[camel] = value;
  }
  return out as CSSProperties;
}

type HoverDivProps = React.ComponentPropsWithoutRef<"div"> & {
  base: string;
  hover?: string;
};

export function HoverDiv({ base, hover, style, onMouseEnter, onMouseLeave, ...rest }: HoverDivProps) {
  const [hovered, setHovered] = useState(false);
  return (
    <div
      {...rest}
      style={{ ...css(base), ...(hovered && hover ? css(hover) : {}), ...style }}
      onMouseEnter={(e) => {
        setHovered(true);
        onMouseEnter?.(e);
      }}
      onMouseLeave={(e) => {
        setHovered(false);
        onMouseLeave?.(e);
      }}
    />
  );
}

type HoverButtonProps = React.ComponentPropsWithoutRef<"button"> & {
  base: string;
  hover?: string;
};

export function HoverButton({ base, hover, style, onMouseEnter, onMouseLeave, type, ...rest }: HoverButtonProps) {
  const [hovered, setHovered] = useState(false);
  return (
    <button
      {...rest}
      type={type ?? "button"}
      style={{ ...css(base), ...(hovered && hover ? css(hover) : {}), ...style }}
      onMouseEnter={(e) => {
        setHovered(true);
        onMouseEnter?.(e);
      }}
      onMouseLeave={(e) => {
        setHovered(false);
        onMouseLeave?.(e);
      }}
    />
  );
}

