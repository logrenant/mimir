import type { DaemonStatus } from "./daemon";

/**
 * What the shell shows while the daemon is being reached.
 *
 * The gate itself is not negotiable — `Connection`'s own argument holds: a
 * window that renders a project picker over a transport that does not work yet
 * is a window that lies. What was wrong was the *presentation*. A handshake
 * that succeeds in two hundred milliseconds has nothing to report, and it was
 * reporting it with a poster, four dependency rows and a button to press.
 *
 * So the wait is graded. Nothing at all while it is plausibly instant, one
 * line if it is taking long enough to wonder about, and the full honest
 * surface — stderr, dependencies, retry — only when it has actually failed.
 */

/**
 * How long a handshake may take before the operator is told anything.
 *
 * Long enough that a working daemon never paints a loading state (it answers
 * in ~200ms on the loopback), short enough that a hung one does not look like
 * a window that failed to open.
 */
export const HANDSHAKE_QUIET_MS = 600;

export type ShellPhase = "quiet" | "waiting" | "failed" | "ready";

/**
 * The phase, from the daemon's answer and how long it has been coming.
 *
 * Pure so it can be tested, which matters more than it looks: the bug this
 * replaces was a screen that showed itself whenever `state !== "ready"`, and
 * "when does the operator see something" is exactly the kind of rule that
 * drifts once it lives inside a component.
 */
export function shellPhase(state: DaemonStatus, elapsedMs: number): ShellPhase {
  if (state.state === "ready") return "ready";
  if (state.state === "failed") return "failed";
  return elapsedMs >= HANDSHAKE_QUIET_MS ? "waiting" : "quiet";
}
