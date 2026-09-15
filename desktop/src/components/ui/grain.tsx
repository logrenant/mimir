import { cn } from "../../lib/cn";
import grainWide from "../../assets/brand/grain-wide.webp";

/**
 * The brand's own surface, as a backdrop.
 *
 * The guide's poster is a grained gradient and it is the single most
 * recognisable thing the brand owns — and until now it appeared nowhere in the
 * product, which is most of why a correct interface still did not look like
 * *this company's* interface.
 *
 * It is rationed on purpose. Grain everywhere is wallpaper; the working panes
 * keep the dot grid, which is the technical half of the vocabulary, and this
 * appears on the two surfaces that are not work: the gate the operator crosses
 * at every launch, and the band above the day's reading. Two placements, and
 * there is no third.
 *
 * Built from two layers rather than one image, because the halves want
 * opposite treatment. The gradient is soft and enormous, so it ships as a
 * 1200px WebP at 16 kB and stretches — blur does not care about scale. The
 * grain is high-frequency, which is exactly what a codec discards and what
 * stretching destroys: at full resolution it cost 114 kB and still went soft
 * on resize. So `.grain-noise` generates it in the compositor at the display's
 * real pixel density, sharper than the source and free.
 */
export function Grain({
  /** How much of the gradient shows through. */
  intensity = 0.3,
  /** How much film sits on top of it. */
  grain = 0.18,
  /**
   * Which part of the art to show, as `object-position`.
   *
   * It matters more than it sounds. The source is a diagonal of light across
   * two dark corners, and a band 180px tall cropped from the middle of it is
   * a rectangle of near-black — which is exactly what the first build shipped:
   * grain that was technically present and visually absent. Each placement
   * picks the part of the frame that actually has light in it.
   */
  position = "50% 50%",
  /**
   * `cover` crops the art to the box; `fill` compresses the whole composition
   * into it.
   *
   * `fill` is normally a mistake and here it is the right answer, for the same
   * reason the two layers are split in the first place: the image is nothing
   * but a blur, and a blur has no proportions to distort. Cropping it into a
   * band 190px tall on a 1440px screen throws away the diagonal that is the
   * whole composition and keeps one corner of dark; compressing it lets the
   * sweep run the full width, which is what the poster does.
   */
  fit = "cover",
  /**
   * The wash that keeps text legible over it. `left` fades from the ground
   * colour on the left — for a band with a heading on it; `full` is an even
   * veil, for a surface that only has to be dark.
   */
  wash = "left",
  className,
}: {
  intensity?: number;
  grain?: number;
  position?: string;
  fit?: "cover" | "fill";
  wash?: "left" | "full" | "none";
  className?: string;
}) {
  return (
    <div aria-hidden className={cn("pointer-events-none absolute inset-0 overflow-hidden", className)}>
      <img
        src={grainWide}
        alt=""
        className={cn("absolute inset-0 size-full", fit === "fill" ? "object-fill" : "object-cover")}
        style={{ opacity: intensity, objectPosition: position }}
      />
      <div className="grain-noise absolute inset-0" style={{ "--grain-opacity": grain } as React.CSSProperties} />
      {wash !== "none" && (
        <div
          className="absolute inset-0"
          style={{
            background:
              wash === "left"
                ? "linear-gradient(100deg, var(--color-ground) 0%, color-mix(in srgb, var(--color-ground) 88%, transparent) 22%, color-mix(in srgb, var(--color-ground) 55%, transparent) 48%, transparent 88%)"
                : "radial-gradient(120% 90% at 50% 42%, color-mix(in srgb, var(--color-ground) 18%, transparent) 0%, color-mix(in srgb, var(--color-ground) 72%, transparent) 68%, var(--color-ground) 100%)",
          }}
        />
      )}
      {/* The bottom edge dissolves into the ground rather than ending on a
          line. A hard edge under a photographic texture reads as a cropping
          mistake. */}
      <div className="absolute inset-x-0 bottom-0 h-20 bg-gradient-to-b from-transparent to-ground" />
    </div>
  );
}
