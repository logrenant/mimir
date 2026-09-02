import { useEffect, useRef, useState } from "react";

import { api, DaemonError, type BrainScanEvent, type BrainScanStatus } from "../lib/daemon";
import { pollInterval } from "../lib/brainGraph";

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
    <div style={{ height: "100%", display: "grid", gridTemplateRows: "auto 1fr", minHeight: 0 }}>
      <div
        style={{
          borderBottom: "1px solid #24272d",
          padding: "10px 14px",
          display: "flex",
          alignItems: "center",
          gap: 12,
          flexWrap: "wrap",
        }}
      >
        <span className="display" style={{ font: "400 13px/1 Aldrich,ui-sans-serif,system-ui", color: "#eef0f2" }}>
          agy · sürekli tarama
        </span>
        <span style={{ font: "400 10.5px/1 ui-monospace,Menlo,monospace", color: "#6b7079" }}>
          {status ? `${status.provider} · ${status.model} · ${phaseLabel(status)}` : "bağlanıyor…"}
        </span>
        {status && (
          <span style={{ font: "400 10.5px/1 ui-monospace,Menlo,monospace", color: "#4f545e" }}>
            {status.nodes_total} düğüm · bu turda {status.scanned_session} · kalan {status.remaining}
          </span>
        )}
        <span style={{ marginLeft: "auto", display: "flex", gap: 6 }}>
          {status?.paused ? (
            <ConsoleButton label="sürdür" disabled={busy} onClick={() => void act(api.resumeBrainScan)} />
          ) : (
            <ConsoleButton label="duraklat" disabled={busy} onClick={() => void act(api.pauseBrainScan)} />
          )}
          <ConsoleButton
            label="şimdi tara"
            disabled={busy || !!status?.paused}
            onClick={() => void act(api.scanBrainNow)}
          />
        </span>
      </div>

      <div
        onScroll={(e) => {
          const el = e.currentTarget;
          stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
        }}
        style={{ overflowY: "auto", padding: "10px 14px", minHeight: 0 }}
      >
        {error && (
          <p style={{ margin: "0 0 8px", font: "400 11px/1.6 ui-monospace,Menlo,monospace", color: "#e5484d" }}>
            {error}
          </p>
        )}
        {events.length === 0 && !error && (
          <p style={{ margin: 0, font: "400 11px/1.6 ui-sans-serif,system-ui", color: "#4f545e" }}>
            Tarama bir şey yapar yapmaz satırlar burada belirir.
          </p>
        )}
        {events.map((e) => (
          <div
            key={e.seq}
            style={{
              display: "flex",
              gap: 10,
              font: "400 11px/1.7 ui-monospace,Menlo,monospace",
              color: colorFor(e.kind),
            }}
          >
            <span style={{ color: "#3a3f47", flexShrink: 0 }}>
              {new Date(e.at).toLocaleTimeString()}
            </span>
            <span style={{ minWidth: 0, wordBreak: "break-all" }}>{e.text}</span>
          </div>
        ))}
        <div ref={bottom} />
      </div>
    </div>
  );
}

function ConsoleButton({
  label,
  disabled,
  onClick,
}: {
  label: string;
  disabled?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      style={{
        background: "none",
        border: "1px solid #24272d",
        borderRadius: 4,
        padding: "5px 9px",
        cursor: disabled ? "not-allowed" : "pointer",
        opacity: disabled ? 0.4 : 1,
        font: "500 9.5px/1 ui-monospace,Menlo,monospace",
        letterSpacing: ".1em",
        textTransform: "uppercase",
        color: "#8a9099",
      }}
    >
      {label}
    </button>
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

function colorFor(kind: BrainScanEvent["kind"]): string {
  switch (kind) {
    case "failed":
      return "#e5484d";
    case "unreadable":
      return "#8a9099";
    case "pass":
    case "sweep":
      return "#c6f04a";
    case "control":
    case "backoff":
      return "#2547e8";
    case "project":
      return "#eef0f2";
    default:
      return "#8a9099";
  }
}
