/**
 * What a paste into the shell terminal should type.
 *
 * ⌘V with an image on the pasteboard used to do nothing at all: an image is not
 * text, so xterm has nothing to send down the pty. The image does not have to
 * cross the WebView boundary, though — the pty is on this same Mac, and the CLI
 * on the other end of it (`claude`) reads the system pasteboard itself when it
 * sees ^V, which on macOS is its documented paste-an-image keystroke. So the
 * fix is a keystroke, not an upload: the terminal forwards ^V and the program
 * already listening for it picks the bytes up from the pasteboard.
 *
 * A pure function because the rule — image without text, and nothing else — is
 * the part worth testing, and it needs no DOM to state (desktop/AGENTS.md).
 */

/** ^V. Not `\x16` at the call site, which says nothing about why. */
export const PASTE_IMAGE_KEY = "\x16";

/** A paste reduced to the two things the decision turns on. */
export type PastedClipboard = {
  /** The `text/plain` flavour, empty when there is none. */
  text: string;
  /** MIME types of everything else on the pasteboard. */
  types: string[];
};

/**
 * The bytes to send to the pty for this paste, or null to leave it to xterm.
 *
 * Text wins whenever there is any: xterm's own paste already types it, and a
 * shell that is not reading an image takes ^V as quoted-insert — so a paste
 * that could be typed is never turned into a keystroke.
 */
export function shellPasteInput(clip: PastedClipboard): string | null {
  if (clip.text.length > 0) return null;
  if (!clip.types.some((type) => type.startsWith("image/"))) return null;
  return PASTE_IMAGE_KEY;
}

/** The shape above, read off a real paste event. */
export function clipboardOf(transfer: DataTransfer | null): PastedClipboard {
  if (!transfer) return { text: "", types: [] };
  const fromItems = Array.from(transfer.items ?? []).map((item) => item.type);
  const fromFiles = Array.from(transfer.files ?? []).map((file) => file.type);
  return { text: transfer.getData("text/plain"), types: [...fromItems, ...fromFiles] };
}
