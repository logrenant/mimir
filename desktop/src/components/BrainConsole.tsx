import { useEffect, useRef, useState } from "react";

import { api, DaemonError, type BrainScanEvent, type BrainScanStatus } from "../lib/daemon";
import { pollInterval } from "../lib/brainGraph";
import { scanLineClass } from "../lib/lineColors";
import { Button } from "./ui/button";
import { Stream } from "./ui/stream";

/**
 * The resident scan, as a terminal.
 *
 * It is not a coding run and deliberately not modelled as one: there is no run
 * row, no transcript and no socket, so it is not a `Session` and does not go
 * through TerminalsProvider. What it shares with a run is the only thing that
 * matters to the operator — it is a long-lived job spending their quota, and
 * "where is that thing running?" should have one answer.
 *
 * The lines come from the daemon's own ring buffer by sequence, so a tab that
 * has been open for an hour asks for what it is missing rather than the whole
 * history, and one that was closed for an hour gets the tail.
 */
export function BrainConsole() {
  const [events, setEvents] = useState<BrainScanEvent[]>([]);
  const [status, setStatus] = useState<BrainScanStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const seq = useRef(0);
  const bottom = useRef<HTMLDivElement | null>(null);
  const stick = useRef(true);

  useEffect(() => {
    let live = true;
    let timer: ReturnType<typeof setTimeout>;

    const tick = async () => {
      try {
        const [s, log] = await Promise.all([api.brainScan(), api.brainScanLog(seq.current)]);
        if (!live) return;
        setStatus(s.scan);
        setError(null);
        if (log.events.length > 0) {
          seq.current = log.seq;
          // Bounded on this side too: a console that grows forever is a leak
          // with a scrollbar.
          setEvents((prev) => [...prev, ...log.events].slice(-800));
        }
      } catch (e: unknown) {
        if (live) setError(e instanceof DaemonError ? e.message : String(e));
      }
      if (!live) return;
      timer = setTimeout(tick, pollInterval(status));
    };
    void tick();

    return () => {
      live = false;
      clearTimeout(timer);
    };
    // status is read for the interval only; depending on it would restart the
    // timer on every tick.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Follow the tail, but only while the reader is already at the bottom —
  // scrolling up to read something and being yanked back is the worst thing a
  // live console does.
  useEffect(() => {
    if (stick.current) bottom.current?.scrollIntoView({ block: "end" });
  }, [events]);

  const act = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    try {
      await fn();
      const s = await api.brainScan();
      setStatus(s.scan);
    } catch (e: unknown) {
      setError(e instanceof DaemonError ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="grid h-full min-h-0 grid-rows-[auto_1fr]">
      <div className="flex flex-wrap items-center gap-3 border-b border-edge bg-panel px-3.5 py-2.5 shadow-elev-1">
        <span className="display text-base leading-none">agy · sürekli tarama</span>
        <span className="font-mono text-xs text-muted/70">
          {status ? `${status.provider} · ${status.model} · ${phaseLabel(status)}` : "bağlanıyor…"}
        </span>
        {status && (
          <span className="font-mono text-xs text-muted/60">
            {status.nodes_total} düğüm · bu turda {status.scanned_session} · kalan {status.remaining}
          </span>
        )}
        <span className="ml-auto flex gap-1.5">
          {status?.paused ? (
            <Button size="sm" variant="ghost" disabled={busy} onClick={() => void act(api.resumeBrainScan)}>
              sürdür
            </Button>
          ) : (
            <Button size="sm" variant="ghost" disabled={busy} onClick={() => void act(api.pauseBrainScan)}>
              duraklat
            </Button>
          )}
          <Button
            size="sm"
            variant="ghost"
            disabled={busy || !!status?.paused}
            onClick={() => void act(api.scanBrainNow)}
          >
            şimdi tara
          </Button>
        </span>
      </div>

      {/* A scan that is actually moving says so on the seam, the same way a
          run does. `phase` is the daemon's word for it, so this is a reading
          and not an animation that happens to be on screen. */}
      {status && !status.paused && status.phase === "scanning" && <Stream />}

      <div
        onScroll={(e) => {
          const el = e.currentTarget;
          stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
        }}
        className="min-h-0 overflow-y-auto bg-sunken px-3.5 py-2.5"
      >
        {error && (
          <p className="mb-2 font-mono text-xs leading-[1.6] text-bad">{error}</p>
        )}
        {events.length === 0 && !error && (
          <p className="text-xs leading-[1.6] text-muted/60">
            Tarama bir şey yapar yapmaz satırlar burada belirir.
          </p>
        )}
        {events.map((e) => (
          <div
            key={e.seq}
            className={`flex gap-2.5 font-mono text-xs leading-[1.7] ${scanLineClass(e.kind)}`}
          >
            <span className="shrink-0 text-muted/40">{new Date(e.at).toLocaleTimeString()}</span>
            <span className="min-w-0 break-all">{e.text}</span>
          </div>
        ))}
        <div ref={bottom} />
      </div>
    </div>
  );
}

/** The scan's phases in the operator's own language, not the wire's. */
export function phaseLabel(status: BrainScanStatus): string {
  if (status.paused) return "duraklatıldı";
  if (status.provider_down) return "sağlayıcı yanıt vermiyor";
  switch (status.phase) {
    case "scanning":
      return status.project_label ? `tarıyor · ${status.project_label}` : "tarıyor";
    case "discovering":
      return "projeleri buluyor";
    case "backoff":
      return "geri çekildi";
    default:
      return "bekliyor";
  }
}

/** A dot's worth of colour, inside the four. Failure is the one hue outside the
 * palette, which is what the guide reserves it for. */
export function statusOf(status: BrainScanStatus | null): string {
  if (!status) return "queued";
  if (status.provider_down) return "failed";
  if (status.paused) return "stopped";
  if (status.phase === "scanning" || status.phase === "discovering") return "running";
  return "completed";
}

