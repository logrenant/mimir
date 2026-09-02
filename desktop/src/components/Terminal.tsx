import { useEffect, useRef, useState } from "react";
import type { LineKind, Session } from "../lib/terminals";
import { sessionText } from "../lib/terminals";

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
  onStop,
  onClose,
}: {
  session: Session;
  onStop: (runID: string) => void;
  onClose: (runID: string) => void;
}) {
  const scroller = useRef<HTMLDivElement>(null);
  // Following is the default and scrolling up is how you leave it: a terminal
  // that yanks you back to the bottom while you are reading is unusable.
  const [following, setFollowing] = useState(true);
  const [copied, setCopied] = useState(false);

  const live = session.status === "running" || session.status === "queued";

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
        <span style={{ font: "400 11px/1 ui-monospace,Menlo,monospace", color: "#6b7079" }}>
          {session.status}
        </span>
        <div style={{ flex: 1 }} />
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
        <BarButton onClick={() => onClose(session.runID)}>kapat</BarButton>
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

function BarButton({
  children,
  onClick,
  tone,
}: {
  children: React.ReactNode;
  onClick: () => void;
  tone?: "bad";
}) {
  const [hovered, setHovered] = useState(false);
  return (
    <button
      type="button"
      onClick={onClick}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      style={{
        background: hovered ? "#1c1f24" : "none",
        border: `1px solid ${tone === "bad" ? "#e5484d" : "#24272d"}`,
        borderRadius: 5,
        padding: "3px 8px",
        cursor: "pointer",
        font: "400 10.5px/1 ui-monospace,Menlo,monospace",
        color: tone === "bad" ? "#e5484d" : "#8a9099",
      }}
    >
      {children}
    </button>
  );
}
