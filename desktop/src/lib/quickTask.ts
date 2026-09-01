/**
 * The quick-task window's decisions, kept out of the component so they can be
 * tested without a DOM.
 *
 * Each one is a rule the window would otherwise get wrong silently: starting a
 * run on a project that no longer exists, sending on a keystroke meant to add
 * a line, or notifying twice for one run.
 */

import type { Project } from "./daemon";
import type { RunView } from "./runStream";

/**
 * Which project a summon should land on.
 *
 * The head of the daemon's list, which is ordered by `last_used_at` — the
 * folder this operator was working in most recently. The window is mounted for
 * the app's whole lifetime, so without this a project chosen days ago would
 * still be selected after a dozen folders had come and gone.
 *
 * `pinned` is the one exception: a selection the operator made *by hand* is
 * kept, because they said so. It is dropped anyway if the daemon no longer
 * knows that project — a run against a stale id would just be rejected.
 */
export function defaultProject(
  projects: Project[],
  current: string | null,
  pinned = false,
): string | null {
  if (pinned && current && projects.some((project) => project.id === current)) return current;
  return projects[0]?.id ?? null;
}

/** Enter starts the run; Shift+Enter is a newline. */
export function isSubmitKey(key: string, shiftKey: boolean): boolean {
  return key === "Enter" && !shiftKey;
}

/** Esc dismisses the window. The run keeps going — dismissing is not cancelling. */
export function isDismissKey(key: string): boolean {
  return key === "Escape";
}

/** A run needs a folder to run in and something to do; a second click while the first is in flight is not a second run. */
export function canStart(selected: string | null, prompt: string, busy: boolean): boolean {
  return Boolean(selected) && prompt.trim().length > 0 && !busy;
}

export type Notification = { title: string; body: string };

/**
 * What to tell the operator when a run ends, or `null` while it is still
 * going. A task started from the menu bar is a task nobody is watching, so the
 * notification is the result — but only on the terminal event, and only once.
 */
export function finishedNotification(view: RunView): Notification | null {
  if (!view.finished) return null;
  if (view.failed) {
    return {
      title: "GOAT — run failed",
      body: view.error ?? "The coding run did not finish.",
    };
  }
  return {
    title: "GOAT — run completed",
    body: view.numTurns ? `Finished in ${view.numTurns} turns.` : "The coding run finished.",
  };
}
