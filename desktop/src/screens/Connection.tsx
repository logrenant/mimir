import { useEffect, useRef, useState } from "react";
import { Wordmark } from "../components/brand";
import { Button } from "../components/ui/button";
import { Grain } from "../components/ui/grain";
import { Icon } from "../components/ui/icon";
import { Orb } from "../components/ui/orb";
import { HANDSHAKE_QUIET_MS, shellPhase } from "../lib/shell";
import { restart, status, type DaemonStatus } from "../lib/daemon";

/**
 * The handshake — and, on the happy path, nothing at all.
 *
 * ---------------------------------------------------------------------------
 * Why the poster went.
 * ---------------------------------------------------------------------------
 * This screen used to be the brand's own surface: grain, a lit mark, the
 * wordmark, a floating panel listing four dependencies, and a Continue button.
 * The reasoning was sound — a surface with nothing to do on it is where a brand
 * is allowed to be a brand — and the conclusion was still wrong, because it
 * mistook *how long* the operator waits. The daemon answers on the loopback in
 * about two hundred milliseconds. What that produced was a full-screen splash
 * flashing past at every launch, and a button standing between the operator and
 * their own application for no reason but to be pressed.
 *
 * The gate itself does not move, and its original argument is why: a window
 * that renders a project picker over a transport that does not work yet is a
 * window that lies. Nothing that talks to the daemon renders until the
 * handshake succeeds. What changed is that a *successful* handshake is now
 * silent, and the surface is spent where it is actually earned — on the
 * failure, where there genuinely is nothing to do but read what went wrong.
 */
export function Connection({ onReady }: { onReady: () => void }) {
  const [state, setState] = useState<DaemonStatus>({ state: "starting" });
  // How long this attempt has been going. Not a clock the render reads —
  // a single timer that fires once, at the point where silence stops being
  // honest and starts looking like a window that failed to open.
  const [elapsed, setElapsed] = useState(0);
  const startedAt = useRef(Date.now());

  useEffect(() => {
    let cancelled = false;

    const poll = async () => {
      const next = await status();
      if (!cancelled) setState(next);
    };

    void poll();
    const timer = window.setInterval(() => {
      if (state.state === "starting") void poll();
    }, 500);

    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [state.state]);

  // One timer, not a tick: the only thing the render needs to know is which
  // side of the threshold it is on.
  useEffect(() => {
    const at = window.setTimeout(
      () => setElapsed(Date.now() - startedAt.current),
      HANDSHAKE_QUIET_MS,
    );
    return () => window.clearTimeout(at);
  }, []);

  // Straight through. There is no Continue button any more, because there was
  // never a decision behind it — the daemon had already answered.
  useEffect(() => {
    if (state.state === "ready") onReady();
  }, [state.state, onReady]);

  const phase = shellPhase(state, elapsed);

  // Ready is handed to the Dashboard by the effect above; quiet is the whole
  // point of this pass. Both render nothing rather than a frame of something.
  if (phase === "ready" || phase === "quiet") return null;

  if (phase === "waiting") {
    return (
      <div className="grid h-full place-items-center bg-ground p-8">
        <div className="flex flex-col items-center gap-5">
          <Orb size={40} live />
          <Wordmark className="h-6 w-auto text-mist" />
          <p className="text-sm text-muted">daemon'a bağlanılıyor…</p>
        </div>
      </div>
    );
  }

  return <Failed message={failureMessage(state)} onRetry={() => {
    startedAt.current = Date.now();
    setElapsed(0);
    void restart().then(() => setState({ state: "starting" }));
  }} />;
}

/**
 * The one state that earns the brand's surface.
 *
 * There is nothing to operate here and nothing to hurry past: the daemon did
 * not start, and the only useful thing on the screen is its own stderr. That is
 * where the grain and the display face belong — not in front of a handshake
 * that worked.
 */
function Failed({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <div className="relative isolate grid h-full place-items-center overflow-hidden bg-ground p-8">
      <Grain intensity={0.72} grain={0.24} wash="full" position="62% 40%" />

      <div className="relative flex w-full max-w-[520px] flex-col items-center gap-8">
        <div className="flex flex-col items-center gap-5">
          <Orb size={48} live={false} />
          <Wordmark className="h-7 w-auto text-mist" />
        </div>

        <div className="w-full overflow-hidden rounded-xl bg-overlay shadow-elev-3 outline outline-edge-strong/70">
          <div className="flex items-center gap-2 px-6 pt-5 pb-3 text-base text-bad">
            <Icon name="alert" size={15} />
            Daemon başlamadı.
          </div>
          <div className="flex flex-col gap-4 px-6 pb-5">
            {/* The daemon's own stderr, verbatim: it names the cause
                ("MIMIR_DAEMON_TOKEN is empty", "could not listen on …") far
                better than anything this screen could paraphrase. */}
            <pre className="max-h-48 overflow-auto rounded-md bg-sunken p-3.5 font-mono text-xs leading-[1.6] whitespace-pre-wrap text-muted">
              {message}
            </pre>
            <Button icon="refresh" onClick={onRetry}>
              Tekrar dene
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}

function failureMessage(state: DaemonStatus): string {
  return state.state === "failed" && state.message
    ? state.message
    : "Sebep bildirilmedi.";
}
