import { cn } from "../../lib/cn";

/**
 * A run that is talking.
 *
 * The daemon cannot say how far through a coding task it is — there is no
 * denominator — so this reports *that* work is happening rather than how much
 * is left, and it exists only while a socket is open. That makes it the one
 * indeterminate thing in an interface whose rule is that motion reports a real
 * value; the value it reports is "the stream is alive", and when the stream
 * ends the element is unmounted rather than stopped.
 *
 * One pixel, at 60%. The first build drew it at two pixels in full Electric
 * across the whole width of the card, which made the busiest thing on a live
 * card its *decoration* rather than its title or its elapsed time. A marker
 * that shouts louder than what it marks is reporting itself.
 *
 * Electric because work in progress is what Electric reports here, and it is
 * the only accent on a card whose rail is already Lime.
 */
export function Stream({ className }: { className?: string }) {
  return (
    <span
      aria-hidden
      className={cn("block h-px w-full overflow-hidden opacity-45", className)}
      style={{
        // Short marks, wide gaps. The first version was 8px on / 16px off at
        // full strength across the whole width of a card, which on the
        // terminal pane — 950px of it — stopped reading as motion and started
        // reading as a dotted rule between the bar and the transcript.
        backgroundImage:
          "repeating-linear-gradient(90deg, var(--color-electric) 0 4px, transparent 4px 24px)",
        backgroundSize: "24px 100%",
        animation: "mimirCrawl 900ms linear infinite",
      }}
    />
  );
}
