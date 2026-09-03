import { useEffect, useRef, useState } from "react";
import { Terminal as Xterm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";
import { endpoint, ptyWSURL, type TerminalProfile } from "../lib/daemon";

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
        background: "#0d0f12",
        foreground: "#eef0f2",
        cursor: "#c6f04a",
        selectionBackground: "#2547e8",
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
      observer.disconnect();
      // Closing the socket only detaches now: the daemon owns the shell, so
      // this leaves it running for the next viewer (internal/ptyterm).
      socket?.close();
      term.dispose();
    };
  }, [profile.name, attempt]);

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%", minHeight: 0 }}>
      {error ? (
        <div style={{ color: "#e5484d", fontSize: 12, padding: "6px 10px" }}>{error}</div>
      ) : null}
      {closed && !error ? (
        <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "6px 10px" }}>
          <span style={{ color: "#8a9099", fontSize: 12 }}>oturum kapandı</span>
          <button
            type="button"
            onClick={() => setAttempt((n) => n + 1)}
            style={{
              background: "none",
              border: "1px solid #24272d",
              borderRadius: 2,
              color: "#eef0f2",
              cursor: "pointer",
              fontSize: 11,
              padding: "2px 8px",
            }}
          >
            yeniden başlat
          </button>
        </div>
      ) : null}
      <div ref={hostRef} style={{ flex: 1, minHeight: 0, padding: "4px 6px" }} />
    </div>
  );
}
