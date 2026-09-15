import { useEffect, useRef, useState } from "react";
import { Terminal as Xterm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { Button } from "./ui/button";

/**
 * One `@theme` colour, as the string xterm needs.
 *
 * xterm paints to a canvas and cannot read a custom property, so the value has
 * to be resolved once here. Resolving it beats restating it: this file used to
 * hold four literals that had drifted from the tokens they were copies of.
 */
function token(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}
import { endpoint, ptyWSURL, type TerminalProfile } from "../lib/daemon";
import { clipboardOf, shellPasteInput } from "../lib/shellPaste";

/**
 * A real terminal, attached to a real shell.
 *
 * The sibling of Terminal.tsx, not a replacement for it: that one renders a
 * finished run's parsed stream-json, which is the right shape for a transcript
 * and cannot be typed into. This one is the operator's own login shell on a
 * pty, so oh-my-zsh, its plugins, their prompt and the `claude-acct` function
 * all behave exactly as they do in Terminal.app — because it is the same shell
 * reading the same rc files (internal/ptyterm).
 *
 * xterm.js earns its dependency here for the reason Terminal.tsx says it does
 * not need one: a login shell emits real ANSI — cursor addressing, colour, the
 * alternate screen a TUI draws on — and rendering that by hand is not a smaller
 * job than the parser already written.
 */
export function ShellTerminal({ profile }: { profile: TerminalProfile }) {
  const hostRef = useRef<HTMLDivElement | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [closed, setClosed] = useState(false);
  // Bumped to ask for a fresh shell. Needed because sessions now outlive their
  // viewer: a shell the operator exited stays exited, and without this the
  // pane would show a dead terminal with no way back to a live one.
  const [attempt, setAttempt] = useState(0);

  // Keyed on the profile name: choosing a different identity is a different
  // session, and re-running the effect is how it gets one. Anything else would
  // keep the first shell and only relabel it.
  useEffect(() => {
    setClosed(false);
    setError(null);
    const host = hostRef.current;
    if (!host) return;

    let disposed = false;
    let socket: WebSocket | null = null;

    const term = new Xterm({
      convertEol: false,
      cursorBlink: true,
      fontFamily:
        '"SF Mono", ui-monospace, SFMono-Regular, Menlo, Monaco, "Courier New", monospace',
      fontSize: 12,
      // Matches the app's console palette so the terminal does not read as a
      // pasted-in widget.
      theme: {
        // Read off the document rather than restated here. This block was the
        // application's fifth colour source, and its "terminal black"
        // (#0d0f12) did not match either of the two other consoles.
        background: token("--color-sunken"),
        foreground: token("--color-text"),
        cursor: token("--color-lime"),
        selectionBackground: token("--color-electric"),
      },
      // Enough history that a long build's output is still reachable, but
      // bounded: xterm keeps every line in memory.
      scrollback: 10000,
    });

    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(host);
    fit.fit();

    (async () => {
      try {
        const ep = await endpoint();
        if (disposed) return;

        const { url, protocol } = ptyWSURL(
          profile.name,
          { rows: term.rows, cols: term.cols },
          ep,
        );
        socket = new WebSocket(url, protocol);
        // Output arrives as binary frames of raw pty bytes; without this the
        // browser hands us Blobs and every write would need an async read.
        socket.binaryType = "arraybuffer";

        socket.onmessage = (ev) => {
          if (typeof ev.data === "string") {
            term.write(ev.data);
            return;
          }
          // Bytes, not a decoded string: a read can end mid-escape-sequence or
          // mid-rune, and xterm reassembles both. Decoding here would not.
          term.write(new Uint8Array(ev.data));
        };
        socket.onerror = () => {
          if (!disposed) setError("terminal bağlantısı kurulamadı");
        };
        socket.onclose = () => {
          if (!disposed) setClosed(true);
        };

        // Keystrokes, including the control characters a TUI needs. onData is
        // the post-keymap stream, so ^C arrives as \x03 and reaches the pty as
        // a signal rather than as text.
        term.onData((data) => {
          if (socket?.readyState === WebSocket.OPEN) {
            socket.send(JSON.stringify({ type: "input", data }));
          }
        });
      } catch (err) {
        if (!disposed) setError(err instanceof Error ? err.message : String(err));
      }
    })();

    // ⌘V with an image on the pasteboard: xterm has nothing to type, so the
    // paste is turned into the keystroke the CLI answers by reading the
    // pasteboard itself (lib/shellPaste.ts). Everything else is left to xterm.
    const onPaste = (event: ClipboardEvent) => {
      const input = shellPasteInput(clipboardOf(event.clipboardData));
      if (input === null) return;
      event.preventDefault();
      if (socket?.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify({ type: "input", data: input }));
      }
    };
    // Capture, so it runs before xterm's own handler on the hidden textarea.
    host.addEventListener("paste", onPaste, true);

    // The shell only learns the viewport changed if we tell it, and a program
    // drawing a full screen redraws on the SIGWINCH that follows.
    const onResize = () => {
      // A hidden pane measures 0x0. Fitting to that would tell the shell it
      // has no screen, and the program drawing on it would reflow to nothing
      // — so the inactive terminal keeps the size it had until it is shown.
      if (host.clientWidth === 0 || host.clientHeight === 0) return;
      fit.fit();
      if (socket?.readyState === WebSocket.OPEN) {
        socket.send(JSON.stringify({ type: "resize", rows: term.rows, cols: term.cols }));
      }
    };
    const observer = new ResizeObserver(onResize);
    observer.observe(host);

    return () => {
      disposed = true;
      host.removeEventListener("paste", onPaste, true);
      observer.disconnect();
      // Closing the socket only detaches now: the daemon owns the shell, so
      // this leaves it running for the next viewer (internal/ptyterm).
      socket?.close();
      term.dispose();
    };
  }, [profile.name, attempt]);

  return (
    <div className="flex h-full min-h-0 flex-col bg-sunken">
      {error && (
        <div className="border-b border-edge bg-panel px-4 py-2.5 font-mono text-xs text-bad">
          {error}
        </div>
      )}
      {closed && !error && (
        <div className="flex h-12 items-center gap-3 border-b border-edge bg-panel px-4">
          <span className="text-base text-muted">Oturum kapandı</span>
          <div className="flex-1" />
          <Button size="sm" variant="ghost" icon="refresh" onClick={() => setAttempt((n) => n + 1)}>
            Yeniden başlat
          </Button>
        </div>
      )}
      <div ref={hostRef} className="min-h-0 flex-1 px-3 py-2.5" />
    </div>
  );
}
