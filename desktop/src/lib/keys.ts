/**
 * Whether a key event landed on something the operator is typing into.
 *
 * A pane that binds bare keys — `j`, `k`, Space to tick a row — is binding
 * letters somebody may be in the middle of typing, and the handler that
 * `preventDefault()`s them cannot tell the difference on its own. Every one of
 * those bindings in this app is currently safe only by accident: the panes they
 * sit on happen to contain no text field today. The day one is added, the key is
 * simply untypable, and the symptom — "the space bar does not work" — is exactly
 * what the operator reported about the brand panel for an unrelated reason.
 *
 * So the guard is a function rather than a comment about where the search box
 * happens to be.
 */
export function isTypingTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  // `isContentEditable` is the right question and jsdom does not answer it —
  // it is `false` for every element there — so the attribute is read as well.
  // `closest` rather than the element itself because a key event inside an
  // editable region lands on whatever child the caret is in.
  if (target.isContentEditable) return true;
  const editable = target.closest("[contenteditable]");
  if (editable && editable.getAttribute("contenteditable") !== "false") return true;
  const tag = target.tagName;
  if (tag === "TEXTAREA" || tag === "SELECT") return true;
  if (tag === "INPUT") {
    // A checkbox or a radio is a control Space is *supposed* to reach, and a
    // row that ticks itself on Space is the reason these bindings exist. Only
    // the inputs that hold text are off limits.
    const type = (target as HTMLInputElement).type;
    return type !== "checkbox" && type !== "radio" && type !== "button";
  }
  return false;
}
