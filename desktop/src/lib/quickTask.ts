/**
 * What is left of the quick-task window's decisions: the keystrokes, and what
 * to say when a run ends.
 *
 * Choosing a folder and deciding whether a run may start moved to
 * `lib/wizard.ts` when the panel became a conversation (task-96). They were the
 * form's questions — "which of these is selected", "is the form complete" — and
 * a conversation asks different ones.
 *
 * `defaultProject` went with them and was not replaced. It landed every summon
 * on the most recently used folder, silently; the wizard reads the folder out
 * of the sentence and *says* which one it read, and when it cannot, the most
 * recent folder is simply the first chip offered. One tap instead of a silent
 * assumption.
 */

import type { RunView } from "./runStream";

/** Enter starts the run; Shift+Enter is a newline. */
export function isSubmitKey(key: string, shiftKey: boolean): boolean {
  return key === "Enter" && !shiftKey;
}

/** Esc dismisses the window. The run keeps going — dismissing is not cancelling. */
export function isDismissKey(key: string): boolean {
  return key === "Escape";
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
      title: "Mimir — run failed",
      body: view.error ?? "The coding run did not finish.",
    };
  }
  return {
    title: "Mimir — run completed",
    body: view.numTurns ? `Finished in ${view.numTurns} turns.` : "The coding run finished.",
  };
}
