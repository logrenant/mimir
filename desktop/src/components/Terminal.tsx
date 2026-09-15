import { useEffect, useRef, useState } from "react";
import type { Session } from "../lib/terminals";
import { CONSOLE_TEXT, LINE_CLASS } from "../lib/lineColors";
import { Badge } from "./ui/badge";
import { Button, IconButton } from "./ui/button";
import { Pulse } from "./ui/pulse";
import { Stream } from "./ui/stream";
import { Tooltip } from "./ui/tooltip";
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
    <div className="grid h-full min-h-0 grid-rows-[auto_1fr_auto]">
      {/* Bar and banner are one grid row: the banner comes and goes, and a
          conditional child of the grid itself would renumber the rows under
          the scroller every time it did. */}
      <div>
        <div className="flex h-12 items-center gap-3 border-b border-edge bg-panel px-4 shadow-elev-1">
          <StatusDot status={session.status} />
          {/* `min-w-0 flex-1` and not a fixed max: the pane is as wide as the
              window, and a title clipped at 360px was truncating with 600px of
              empty bar beside it. */}
          <span className="min-w-0 flex-1 truncate text-lg leading-tight font-medium text-text">
            {session.title}
          </span>
          <span className="shrink-0 font-mono text-xs text-muted/60">
            {session.runID.slice(0, 8)}
          </span>
          <StatusBadge status={session.status} />

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
                  Devam et
                </BarButton>
              ) : (
                // No session id means nothing to resume — the run died before the
                // CLI said who it was. Saying so beats a button that would
                // silently start over under a label promising otherwise.
                <span
                  className="font-mono text-xs whitespace-nowrap text-muted/70"
                  title="Bu çalışma kendini tanıtmadan bitti, sürdürülecek bir oturum yok."
                >
                  sürdürülemez
                </span>
              )}
              <BarButton
                onClick={() => onRetry(session.runID, true)}
                title={`Oturumu atar, görevi baştan çalıştırır${waits}`}
              >
                Baştan dene
              </BarButton>
              <BarDivider />
            </>
          )}

          {/* The view's own controls are glyphs. They are pressed often and
              they say nothing about the run — spelling them out put four words
              between the operator and the two buttons that actually act on the
              job. */}
          {!following && (
            <Tooltip label="En alta in">
              <IconButton
                name="arrowRight"
                label="En alta in"
                size="sm"
                className="rotate-90"
                onClick={() => {
                  setFollowing(true);
                  const el = scroller.current;
                  if (el) el.scrollTop = el.scrollHeight;
                }}
              />
            </Tooltip>
          )}
          <Tooltip label={copied ? "Kopyalandı" : "Transcript'i kopyala"}>
            <IconButton
              name={copied ? "check" : "copy"}
              label={copied ? "Kopyalandı" : "Transcript'i kopyala"}
              size="sm"
              className={copied ? "text-lime" : undefined}
              onClick={copy}
            />
          </Tooltip>
          {live && (
            <BarButton tone="bad" onClick={() => onStop(session.runID)}>
              Durdur
            </BarButton>
          )}
          <BarDivider />
          <Tooltip label="Sekmeyi kapat">
            <IconButton name="close" label="Sekmeyi kapat" size="sm" onClick={() => onClose(session.runID)} />
          </Tooltip>
        </div>

        {/* Said once, under the bar, rather than on each button: it is a fact
            about the machine right now, not about either choice. */}
        {recoverable && busy && (
          <div className="border-b border-edge bg-raised px-4 py-2 font-mono text-xs leading-[1.5] text-electric">
            şu an başka bir task çalışıyor — buradan başlatılan iş kuyruğa alınır
          </div>
        )}

        {/* The live marker, on the seam between the bar and the transcript. */}
        {live && <Stream />}
      </div>

      <div
        ref={scroller}
        onScroll={onScroll}
        className={`min-h-0 overflow-y-auto bg-sunken px-4 py-3.5 ${CONSOLE_TEXT}`}
      >
        {session.lines.length === 0 ? (
          <p className="text-muted/60">
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
              className={`break-words whitespace-pre-wrap ${LINE_CLASS[line.kind]}`}
            >
              {line.text}
            </div>
          ))
        )}
      </div>

      {session.closedReason && (
        <div className="border-t border-edge bg-panel px-4 py-2.5 font-mono text-xs leading-[1.5] text-bad/80">
          {session.closedReason}
        </div>
      )}
    </div>
  );
}

/**
 * A run's state as a tone.
 *
 * One switch instead of the three that were here — a dot, a badge and a bar
 * button each deciding the same thing from the same string. `queued` was
 * amber, which was a fifth colour; it is Electric now, because a queued run is
 * work that is going to happen and that is what Electric reports.
 */
function toneOf(status: string): "ok" | "bad" | "accent" | "muted" {
  switch (status) {
    case "running":
    case "queued":
      return "accent";
    case "completed":
      return "ok";
    case "failed":
      return "bad";
    default:
      return "muted";
  }
}

/** The application's status dot, wherever a run's state has to be glanced at. */
export function StatusDot({ status }: { status: string }) {
  return <Pulse tone={toneOf(status)} />;
}

/** The run's state as a word, boxed in its own tone. */
function StatusBadge({ status }: { status: string }) {
  return (
    <Badge tone={toneOf(status)} className="shrink-0 px-1.5 py-1">
      {status}
    </Badge>
  );
}

/** Separates what acts on the run from what acts on the view. */
function BarDivider() {
  return <span aria-hidden className="h-4 w-px shrink-0 bg-edge" />;
}

/**
 * A control that acts on the *run*, as against the view.
 *
 * These keep their words. "Devam et" and "Baştan dene" are the two most
 * consequential buttons in the application — one resumes a session and one
 * throws it away — and a glyph for either would be a guess the operator makes
 * with real tokens.
 */
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
  return (
    <Button
      size="sm"
      variant={tone === "bad" ? "danger" : tone === "accent" ? "primary" : "ghost"}
      icon={tone === "bad" ? "stop" : tone === "accent" ? "play" : undefined}
      className="shrink-0 whitespace-nowrap"
      onClick={onClick}
      title={title}
    >
      {children}
    </Button>
  );
}
