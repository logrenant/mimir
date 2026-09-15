/**
 * Telling the shell this page is still running.
 *
 * The Rust half (`src-tauri/src/liveness.rs`) reloads a window whose page has
 * stopped answering, because macOS suspends the WebContent process behind a
 * hidden window and does not always bring it back — a window that opens blank
 * and stays blank, with every timer in it stopped.
 *
 * Two ways in, deliberately. The interval is the slow, unconditional one: it
 * proves the page is running even when nothing has happened. `__mimirPing` is
 * the direct answer to being poked, and it is the one that matters, because a
 * window that has just been shown must be judged on what it does *now* rather
 * than on a tick from before it was hidden.
 *
 * Everything here is best-effort. A failed `invoke` means the shell is not
 * listening, which is a shell without this handshake, not a page in trouble.
 */

import { invoke } from "@tauri-apps/api/core";

/** The unconditional tick. Slower than the shell's grace window on purpose:
 * the poke is what a shown window is judged on, so this only has to be often
 * enough that a page running quietly in the background stays accounted for. */
const INTERVAL_MS = 5000;

declare global {
  interface Window {
    __mimirPing?: () => void;
  }
}

function beat(): void {
  void invoke("webview_heartbeat").catch(() => {});
}

/**
 * Starts the heartbeat and exposes the poke. Returns the stop function, which
 * nothing calls today — the page lives as long as the window does.
 */
export function startHeartbeat(): () => void {
  window.__mimirPing = beat;

  // At once, so a page that has just reloaded is accounted for before the
  // first interval rather than five seconds into its life.
  beat();

  const timer = setInterval(beat, INTERVAL_MS);

  // A view that has just become visible again is exactly the case this exists
  // for, and the browser tells us about it for free.
  const onVisible = () => {
    if (document.visibilityState === "visible") beat();
  };
  document.addEventListener("visibilitychange", onVisible);
  window.addEventListener("focus", beat);

  return () => {
    clearInterval(timer);
    document.removeEventListener("visibilitychange", onVisible);
    window.removeEventListener("focus", beat);
    delete window.__mimirPing;
  };
}
