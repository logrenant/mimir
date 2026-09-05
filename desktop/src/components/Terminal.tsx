import { useEffect, useRef, useState } from "react";
import type { LineKind, Session } from "../lib/terminals";
import { isResumable, isRetryable, sessionText } from "../lib/terminals";

/**
 * One job's console.
 *
 * Not xterm.js and not a PTY. The daemon runs `claude` over pipes and parses
 * its stream-json, so the honest thing to render is that typed stream plus the
 * CLI's stderr — which is also the only form in which the useful failures
 * ("run `claude login`") exist. Drawing the lines ourselves keeps the app's
 * palette and its two fonts, and adds no dependency to parse ANSI that is never
 * sent.
 */

const LINE_COLOR: Record<LineKind, string> = {
  meta: "#6b7079",
  text: "#eef0f2",
  reasoning: "#8a9099",
  tool: "#2547e8",
  result: "#8a9099",
  stderr: "#e5a23d",
  ok: "#c6f04a",
  bad: "#e5484d",
};

const MONO = "400 11.5px/1.65 ui-monospace,SFMono-Regular,Menlo,monospace";

export function Terminal({
  session,
  busy,
  onStop,
  onClose,
  onRetry,
}: {
  session: Session;
  /**
   * Whether another run is in flight. It does not disable anything — a retry is
   * still accepted — it changes what the bar promises will happen, because the
   * queue is where the card will land rather than straight into execution.
   */
  busy: boolean;
  onStop: (runID: string) => void;
  onClose: (runID: string) => void;
  onRetry: (runID: string, fresh: boolean) => void;
}) {
  const scroller = useRef<HTMLDivElement>(null);
  // Following is the default and scrolling up is how you leave it: a terminal
  // that yanks you back to the bottom while you are reading is unusable.
  const [following, setFollowing] = useState(true);
  const [copied, setCopied] = useState(false);

  const live = session.status === "running" || session.status === "queued";
  // A run that ended without finishing — a dropped connection and a conflict
  // both arrive here — is the one the recovery buttons are for.
  const recoverable = isRetryable(session);
  const resumable = isResumable(session);
  const waits = busy ? " · şu an başka bir task çalışıyor, kuyruğa alınır" : "";

  useEffect(() => {
    if (!following) return;
    const el = scroller.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [session.lines, following]);

  const onScroll = () => {
    const el = scroller.current;
    if (!el) return;
    setFollowing(el.scrollHeight - el.scrollTop - el.clientHeight < 24);
  };

  const copy = () => {
    void navigator.clipboard
      .writeText(sessionText(session))
      .then(() => {
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1500);
      })
      .catch(() => setCopied(false));
  };

  return (
    <div style={{ height: "100%", display: "grid", gridTemplateRows: "auto 1fr auto", minHeight: 0 }}>
      {/* Bar and banner are one grid row: the banner comes and goes, and a
          conditional child of the grid itself would renumber the rows under
          the scroller every time it did. */}
      <div>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 10,
          padding: "9px 14px",
          borderBottom: "1px solid #24272d",
          background: "#16181c",
        }}
      >
        <StatusDot status={session.status} />
        <span style={{ font: "400 11px/1 ui-monospace,Menlo,monospace", color: "#8a9099" }}>
          {session.runID.slice(0, 8)}
        </span>
        <span
          style={{
            font: "450 12px/1 ui-sans-serif,system-ui",
            color: "#eef0f2",
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
            maxWidth: 320,
          }}
        >
          {session.title}
        </span>
        <StatusBadge status={session.status} />
        <div style={{ flex: 1 }} />

        {/* The recovery pair, first in the row because on a failed run it is
            the only thing anybody came to this bar to press. */}
        {recoverable && (
          <>
            {resumable ? (
              <BarButton
                tone="accent"
                onClick={() => onRetry(session.runID, false)}
                title={`Oturumu kaldığı yerden sürdürür (--resume)${waits}`}
              >
                ▶ devam et
              </BarButton>
            ) : (
              // No session id means nothing to resume — the run died before the
              // CLI said who it was. Saying so beats a button that would
              // silently start over under a label promising otherwise.
              <span
                style={{ font: "400 10.5px/1 ui-monospace,Menlo,monospace", color: "#6b7079" }}
                title="Bu çalışma kendini tanıtmadan bitti, sürdürülecek bir oturum yok."
              >
                sürdürülemez
              </span>
            )}
            <BarButton
              onClick={() => onRetry(session.runID, true)}
              title={`Oturumu atar, görevi baştan çalıştırır${waits}`}
            >
              baştan dene
            </BarButton>
            <BarDivider />
          </>
        )}

        {!following && (
          <BarButton
            onClick={() => {
              setFollowing(true);
              const el = scroller.current;
              if (el) el.scrollTop = el.scrollHeight;
            }}
          >
            en alta in
          </BarButton>
        )}
        <BarButton onClick={copy}>{copied ? "kopyalandı" : "kopyala"}</BarButton>
        {live && (
          <BarButton tone="bad" onClick={() => onStop(session.runID)}>
            stop
          </BarButton>
        )}
        <BarDivider />
        <BarButton onClick={() => onClose(session.runID)}>kapat</BarButton>
      </div>

      {/* Said once, under the bar, rather than on each button: it is a fact
          about the machine right now, not about either choice. */}
      {recoverable && busy && (
        <div
          style={{
            padding: "6px 14px",
            borderBottom: "1px solid #24272d",
            background: "#131519",
            font: "400 10.5px/1.5 ui-monospace,Menlo,monospace",
            color: "#e5a23d",
          }}
        >
          şu an başka bir task çalışıyor — buradan başlatılan iş kuyruğa alınır
        </div>
      )}
      </div>

      <div
        ref={scroller}
        onScroll={onScroll}
        style={{
          overflowY: "auto",
          padding: "12px 14px",
          background: "#0c0d10",
          minHeight: 0,
        }}
      >
        {session.lines.length === 0 ? (
          <p style={{ margin: 0, font: MONO, color: "#4f545e" }}>
            {session.status === "queued"
              ? "kuyrukta — bir slot boşalınca başlayacak"
              : session.status === "backlog"
                ? "bu kart henüz çalıştırılmadı"
                : "bağlanıldı, ilk satır bekleniyor…"}
          </p>
        ) : (
          session.lines.map((line, i) => (
            <div
              key={`${line.seq}-${i}`}
              style={{
                font: MONO,
                color: LINE_COLOR[line.kind],
                whiteSpace: "pre-wrap",
                wordBreak: "break-word",
              }}
            >
              {line.text}
            </div>
          ))
        )}
      </div>

      {session.closedReason && (
        <div
          style={{
            padding: "7px 14px",
            borderTop: "1px solid #24272d",
            background: "#16181c",
            font: "400 11px/1.5 ui-monospace,Menlo,monospace",
            color: "#e5a23d",
          }}
        >
          {session.closedReason}
        </div>
      )}
    </div>
  );
}

export function StatusDot({ status }: { status: string }) {
  const color =
    status === "running"
      ? "#2547e8"
      : status === "queued"
        ? "#e5a23d"
        : status === "completed"
          ? "#c6f04a"
          : status === "failed"
            ? "#e5484d"
            : status === "stopped"
              ? "#8a9099"
              : "#4f545e";
  return <span style={{ width: 6, height: 6, borderRadius: "50%", background: color, flexShrink: 0 }} />;
}

/** The run's state as a word, boxed in its own colour. */
function StatusBadge({ status }: { status: string }) {
  const color =
    status === "running"
      ? "#2547e8"
      : status === "queued"
        ? "#e5a23d"
        : status === "completed"
          ? "#c6f04a"
          : status === "failed"
            ? "#e5484d"
            : "#6b7079";
  return (
    <span
      style={{
        font: "400 9.5px/1 ui-monospace,Menlo,monospace",
        color,
        border: `1px solid ${color}40`,
        borderRadius: 3,
        padding: "3px 5px",
        letterSpacing: "0.04em",
        textTransform: "uppercase",
        flexShrink: 0,
      }}
    >
      {status}
    </span>
  );
}

/** Separates what acts on the run from what acts on the view. */
function BarDivider() {
  return <span style={{ width: 1, height: 14, background: "#24272d", flexShrink: 0 }} />;
}

function BarButton({
  children,
  onClick,
  tone,
  title,
}: {
  children: React.ReactNode;
  onClick: () => void;
  tone?: "bad" | "accent";
  title?: string;
}) {
  const [hovered, setHovered] = useState(false);
  const color = tone === "bad" ? "#e5484d" : tone === "accent" ? "#2547e8" : "#8a9099";
  return (
    <button
      type="button"
      onClick={onClick}
      title={title}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      style={{
        background: hovered ? "#1c1f24" : "none",
        border: `1px solid ${tone ? color : "#24272d"}`,
        borderRadius: 5,
        padding: "3px 8px",
        cursor: "pointer",
        font: "400 10.5px/1 ui-monospace,Menlo,monospace",
        color,
        whiteSpace: "nowrap",
        flexShrink: 0,
      }}
    >
      {children}
    </button>
  );
}
