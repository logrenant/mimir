import { cn } from "../../lib/cn";

/**
 * A shape standing in for content that is on its way.
 *
 * Used where the shape of the answer is already known — a list of rows, a
 * status line — so the layout does not jump when the data lands. Where the
 * shape is *not* known, a skeleton is a lie about what is coming and the honest
 * control is {@link Spinner} on the button that started the request, or a
 * sentence.
 *
 * A tint step rather than a travelling shimmer. A shimmer is a timer pretending
 * to be progress, and this interface reserves motion for things that actually
 * report something.
 */
export function Skeleton({ className }: { className?: string }) {
  return <span aria-hidden className={cn("block animate-pulse rounded-sm bg-raised", className)} />;
}
