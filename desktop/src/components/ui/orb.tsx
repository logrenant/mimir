import { cn } from "../../lib/cn";
import { Mark } from "../brand";

/**
 * Mimir, as a thing on the screen.
 *
 * Every interface this product is measured against opens with an identity: a
 * circular mark at the head of the composer, beside the agent's name, in front
 * of the account. This application had a wordmark in the connection gate and
 * nothing anywhere else — so the agent that does the work was never actually
 * present in the interface that commands it.
 *
 * The symbol is the guide's six-armed asterisk, the lockup that "travels
 * alone". What surrounds it is a radial wash, and the wash is the one place in
 * this design where Lime is allowed to be soft: everywhere else it is a flat
 * fill marking something you can press, and here it is light coming off the
 * mark. That reads as a presence rather than as a control, which is exactly
 * the distinction the composer needs at its left edge.
 *
 * `live` is bound to real state by its caller — a run in flight, a daemon
 * answering — never to a timer. A resting orb is a resting agent.
 */
export function Orb({
  size = 32,
  live = false,
  className,
}: {
  size?: number;
  /** Lights the wash. The caller passes real state, not a decorative flag. */
  live?: boolean;
  className?: string;
}) {
  return (
    <span
      aria-hidden
      style={{ width: size, height: size }}
      className={cn(
        "relative inline-grid shrink-0 place-items-center overflow-hidden rounded-full",
        "bg-raised outline outline-edge",
        "transition-[background-color] duration-[var(--dur-base)] ease-decisive",
        className,
      )}
    >
      <span
        className="absolute inset-0 transition-opacity duration-[var(--dur-slow)] ease-decisive"
        style={{
          opacity: live ? 1 : 0.32,
          background: live
            ? "radial-gradient(circle at 32% 28%, color-mix(in srgb, var(--color-lime) 62%, transparent) 0%, color-mix(in srgb, var(--color-lime) 12%, transparent) 58%, transparent 78%)"
            : "radial-gradient(circle at 32% 28%, color-mix(in srgb, var(--color-mist) 22%, transparent) 0%, transparent 62%)",
        }}
      />
      {/* The film that keeps it a material rather than a gradient swatch. It
          is the same grain the gate and the day's band carry, at a fraction of
          the strength — a 32px circle cannot take more than a suggestion. */}
      <span className="grain-noise absolute inset-0" style={{ "--grain-opacity": 0.22 } as React.CSSProperties} />
      <Mark
        className={cn(
          "relative transition-colors duration-[var(--dur-base)]",
          // Mist and not Lime while lit: the wash behind it *is* Lime, and a
          // Lime mark on a Lime glow is a mark nobody can see.
          live ? "text-mist" : "text-muted",
        )}
        style={{ width: size * 0.4, height: size * 0.4 }}
      />
    </span>
  );
}
