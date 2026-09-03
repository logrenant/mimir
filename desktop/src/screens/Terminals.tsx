import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Terminal, StatusDot } from "../components/Terminal";
import { BrainConsole, statusOf } from "../components/BrainConsole";
import { ShellTerminal } from "../components/ShellTerminal";
import { useTerminals } from "../components/TerminalsProvider";
import { api, DaemonError, type BrainScanStatus, type Run, type TerminalProfile } from "../lib/daemon";
import { recentRuns } from "../lib/terminals";
import { cardTitle, isSetTime } from "../lib/board";

/**
 * Every job the operator has watched, one tab each — plus the ones they have
 * not.
 *
 * A run started anywhere (the board, the coding runner, the menu-bar quick
 * task) opens its terminal here, so "where is that thing running?" has one
 * answer. The sessions themselves live in TerminalsProvider, above this screen,
 * so leaving it does not close a socket.
 *
 * Recents is not a second store: a finished run still has its transcript and
 * the socket replays it, so opening one is the same operation as opening a live
 * one. It is collapsed by default because the open sessions are the point, and
 * a long history would push them off the screen.
 */
export function Terminals() {
  const { sessions, activeID, setActive, open, close, stop } = useTerminals();

  // The resident scan is the one job here that is not a run: no row, no
  // transcript, no socket. It gets its own row rather than a fake Session,
  // because pretending it is a coding run would mean either lying to
  // TerminalsProvider or teaching it a second kind of thing.
  const [brainOpen, setBrainOpen] = useState(false);
  const [scan, setScan] = useState<BrainScanStatus | null>(null);

  // The interactive shell is a third kind of pane, beside the brain console and
  // a run's transcript. It is identified by profile name rather than by a
  // Session, because it has no run row: nothing dispatched it, the operator
  // opened a terminal.
  const [profiles, setProfiles] = useState<TerminalProfile[]>([]);
  const [shell, setShell] = useState<TerminalProfile | null>(null);
  const active = brainOpen ? null : (sessions.find((s) => s.runID === activeID) ?? sessions[0] ?? null);

  // One slow poll, only for the sidebar's dot and label. The console does its
  // own faster one while it is open.
  useEffect(() => {
    let live = true;
    let timer: ReturnType<typeof setTimeout>;
    const tick = async () => {
      try {
        const res = await api.brainScan();
        if (live) setScan(res.scan);
      } catch {
        if (live) setScan(null);
      }
      if (live) timer = setTimeout(tick, brainOpen ? 15000 : 30000);
    };
    void tick();
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [brainOpen]);

  const [history, setHistory] = useState<Run[] | null>(null);
  const [historyError, setHistoryError] = useState<string | null>(null);
  const [recentsOpen, setRecentsOpen] = useState(false);

  // Fetched when the section is expanded, and again whenever it is re-opened.
  // A collapsed section is not worth an N+1 fan-out on a timer — the daemon has
  // no cross-project run route, so this is one request per registered project.
  useEffect(() => {
    if (!recentsOpen) return;
    let cancelled = false;

    void (async () => {
      try {
        const { projects } = await api.listProjects();
        const perProject = await Promise.all(
          projects.map(async (project) => {
            const { runs } = await api.listCodingTasks(project.id);
            return runs;
          }),
        );
        if (cancelled) return;
        const merged = perProject
          .flat()
          .sort((a, b) => (b.started_at ?? "").localeCompare(a.started_at ?? ""));
        setHistory(merged);
        setHistoryError(null);
      } catch (err) {
        if (!cancelled) setHistoryError(err instanceof DaemonError ? err.message : String(err));
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [recentsOpen]);

  // The daemon owns the profile list so the picker and the shell that runs the
  // command cannot disagree about what a name means.
  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const { profiles } = await api.terminalProfiles();
        if (!cancelled) setProfiles(profiles);
      } catch {
        // A daemon too old to know the route still runs coding sessions; the
        // shell section simply does not appear.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const openMap = useMemo(
    () => Object.fromEntries(sessions.map((s) => [s.runID, s])),
    [sessions],
  );
  const recents = useMemo(
    () => (history ? recentRuns(history, openMap) : []),
    [history, openMap],
  );

  return (
    <div style={{ height: "100%", display: "grid", gridTemplateColumns: "232px 1fr", minHeight: 0 }}>
      <div
        style={{
          borderRight: "1px solid #24272d",
          overflowY: "auto",
          padding: "10px 8px",
          display: "flex",
          flexDirection: "column",
          gap: 3,
        }}
      >
        <SectionLabel>BRAIN</SectionLabel>
        <SidebarRow
          on={brainOpen}
          status={statusOf(scan)}
          label="agy · sürekli tarama"
          detail={scan ? `${scan.nodes_total} düğüm` : undefined}
          onClick={() => {
            setShell(null);
            setBrainOpen(true);
          }}
        />

        {profiles.length > 0 && (
          <>
            <div style={{ height: 12 }} />
            <SectionLabel>KABUK</SectionLabel>
            {profiles.map((p) => (
              <SidebarRow
                key={p.name}
                on={shell?.name === p.name}
                status={shell?.name === p.name ? "running" : "idle"}
                label={p.name}
                detail={p.command}
                onClick={() => {
                  setBrainOpen(false);
                  setShell(p);
                }}
              />
            ))}
          </>
        )}

        <div style={{ height: 12 }} />
        <SectionLabel>OTURUMLAR</SectionLabel>

        {sessions.length === 0 && (
          <p style={{ margin: "0 9px 6px", font: "400 11px/1.6 ui-sans-serif,system-ui", color: "#4f545e" }}>
            Açık terminal yok. Board'dan bir kartı çalıştırın, ya da aşağıdan geçmiş bir oturumu açın.
          </p>
        )}

        {sessions.map((session) => (
          <SidebarRow
            key={session.runID}
            on={active?.runID === session.runID}
            status={session.status}
            label={session.title}
            onClick={() => {
              setBrainOpen(false);
              setShell(null);
              setActive(session.runID);
            }}
          />
        ))}

        <button
          type="button"
          onClick={() => setRecentsOpen((v) => !v)}
          style={{
            display: "flex",
            alignItems: "center",
            gap: 6,
            marginTop: 12,
            background: "none",
            border: "none",
            padding: "2px 9px 6px",
            cursor: "pointer",
            font: "500 9.5px/1 ui-monospace,Menlo,monospace",
            letterSpacing: ".12em",
            color: "#4f545e",
            textAlign: "left",
          }}
        >
          <span
            style={{
              display: "inline-block",
              width: 8,
              transform: recentsOpen ? "rotate(90deg)" : "none",
              transition: "transform .12s",
            }}
          >
            ›
          </span>
          RECENTS
          {history && <span style={{ letterSpacing: 0 }}>{recents.length}</span>}
        </button>

        {recentsOpen && (
          <>
            {historyError && (
              <p style={{ margin: "0 9px", font: "400 11px/1.5 ui-monospace,Menlo,monospace", color: "#e5484d" }}>
                {historyError}
              </p>
            )}
            {history === null && !historyError && (
              <p style={{ margin: "0 9px", font: "400 11px/1.5 ui-sans-serif,system-ui", color: "#4f545e" }}>
                yükleniyor…
              </p>
            )}
            {history !== null && recents.length === 0 && (
              <p style={{ margin: "0 9px", font: "400 11px/1.5 ui-sans-serif,system-ui", color: "#4f545e" }}>
                geçmiş oturum yok
              </p>
            )}
            {recents.map((run) => (
              <SidebarRow
                key={run.id}
                on={false}
                status={run.status}
                label={cardTitle(run)}
                detail={
                  isSetTime(run.started_at)
                    ? new Date(run.started_at as string).toLocaleString()
                    : undefined
                }
                onClick={() => {
                  setBrainOpen(false);
                  setShell(null);
                  open(run);
                }}
              />
            ))}
          </>
        )}
      </div>

      <div style={{ minWidth: 0, minHeight: 0 }}>
        {brainOpen ? (
          <BrainConsole />
        ) : shell ? (
          <ShellTerminal key={shell.name} profile={shell} />
        ) : active ? (
          <Terminal session={active} onStop={(id) => void stop(id)} onClose={close} />
        ) : (
          <div style={{ height: "100%", display: "grid", placeItems: "center", padding: 30 }}>
            <div style={{ maxWidth: 380, textAlign: "center", display: "flex", flexDirection: "column", gap: 8 }}>
              <h2
                className="display"
                style={{ margin: 0, font: "400 16px/1.3 Aldrich,ui-sans-serif,system-ui", color: "#eef0f2" }}
              >
                Açık terminal yok
              </h2>
              <p style={{ margin: 0, font: "400 12px/1.7 ui-sans-serif,system-ui", color: "#6b7079" }}>
                Çalışan her job burada kendi sekmesini alır. Bitmiş bir oturumu Recents'ten açarsanız
                transcript baştan oynatılır.
              </p>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

function SectionLabel({ children }: { children: ReactNode }) {
  return (
    <span
      style={{
        font: "500 9.5px/1 ui-monospace,Menlo,monospace",
        letterSpacing: ".12em",
        color: "#4f545e",
        padding: "2px 9px 8px",
      }}
    >
      {children}
    </span>
  );
}

function SidebarRow({
  on,
  status,
  label,
  detail,
  onClick,
}: {
  on: boolean;
  status: string;
  label: string;
  detail?: string;
  onClick: () => void;
}) {
  const [hovered, setHovered] = useState(false);
  return (
    <button
      type="button"
      onClick={onClick}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      style={{
        display: "flex",
        alignItems: "center",
        gap: 8,
        width: "100%",
        textAlign: "left",
        background: on ? "#1c1f24" : hovered ? "#16181c" : "transparent",
        border: "none",
        borderRadius: 6,
        padding: "7px 9px",
        cursor: "pointer",
        color: on ? "#eef0f2" : "#8a9099",
        minWidth: 0,
      }}
    >
      <StatusDot status={status} />
      <span style={{ minWidth: 0, display: "flex", flexDirection: "column", gap: 2 }}>
        <span
          style={{
            font: `${on ? "500" : "450"} 11.5px/1.35 ui-sans-serif,system-ui`,
            overflow: "hidden",
            textOverflow: "ellipsis",
            whiteSpace: "nowrap",
          }}
        >
          {label}
        </span>
        {detail && (
          <span style={{ font: "400 9.5px/1 ui-monospace,Menlo,monospace", color: "#4f545e" }}>
            {detail}
          </span>
        )}
      </span>
    </button>
  );
}
