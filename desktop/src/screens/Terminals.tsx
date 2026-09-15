import { useEffect, useMemo, useState } from "react";
import { Terminal, StatusDot } from "../components/Terminal";
import { BrainConsole, statusOf } from "../components/BrainConsole";
import { ShellTerminal } from "../components/ShellTerminal";
import { cn } from "../lib/cn";
import { Empty } from "../components/ui/empty";
import { Icon } from "../components/ui/icon";
import { RailItem } from "../components/ui/rail";
import { SidebarLabel as SectionLabel } from "./Dashboard";
import { useTerminals } from "../components/TerminalsProvider";
import { api, DaemonError, type BrainScanStatus, type Run, type TerminalProfile } from "../lib/daemon";
import { isBusy, recentRuns } from "../lib/terminals";
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
  const { sessions, activeID, setActive, open, close, stop, retry } = useTerminals();

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
  // Which shells are mounted. Append-only for the life of the screen: a
  // profile the operator has opened keeps its terminal, because the whole
  // point is that both accounts stay up while they look at one of them.
  const [opened, setOpened] = useState<string[]>([]);
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
    <div className="grid h-full min-h-0 grid-cols-[252px_1fr]">
      <div className="flex flex-col gap-1 overflow-y-auto border-r border-edge px-2.5 py-3">
        <SectionLabel>Brain</SectionLabel>
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
            <div className="h-4" />
            <SectionLabel>Kabuk</SectionLabel>
            {profiles.map((p) => (
              <SidebarRow
                key={p.name}
                on={shell?.name === p.name}
                // Running means the shell is up, not that you are looking at
                // it — that distinction is the feature, so the dot has to show
                // it. The daemon's own view (`running`) wins once it has been
                // read, so a shell left over from a previous window shows too.
                status={p.running || opened.includes(p.name) ? "running" : "idle"}
                label={p.name}
                detail={p.command}
                onClick={() => {
                  setBrainOpen(false);
                  setOpened((names) => (names.includes(p.name) ? names : [...names, p.name]));
                  setShell(p);
                }}
              />
            ))}
          </>
        )}

        <div className="h-4" />
        <SectionLabel>Oturumlar</SectionLabel>

        {sessions.length === 0 && (
          <p className="mx-3 mb-1.5 text-sm leading-[1.6] text-muted/60">
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
          aria-expanded={recentsOpen}
          className="focus-ring label mt-4 flex items-center gap-1.5 rounded-md px-3 py-1.5 text-left text-muted/60 transition-colors hover:text-muted"
        >
          <Icon
            name="chevronRight"
            size={12}
            className={cn(
              "transition-transform duration-[var(--dur-fast)] ease-decisive",
              recentsOpen && "rotate-90",
            )}
          />
          Recents
          {history && <span className="font-mono tracking-normal">{recents.length}</span>}
        </button>

        {recentsOpen && (
          <>
            {historyError && (
              <p className="mx-3 font-mono text-xs leading-[1.5] text-bad">{historyError}</p>
            )}
            {history === null && !historyError && (
              <p className="mx-3 text-sm leading-[1.5] text-muted/60">yükleniyor…</p>
            )}
            {history !== null && recents.length === 0 && (
              <p className="mx-3 text-sm leading-[1.5] text-muted/60">geçmiş oturum yok</p>
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

      <div className="min-h-0 min-w-0">
        {/*
         * Every shell opened this session stays mounted, hidden rather than
         * removed. Unmounting is what broke it before: the cleanup closed the
         * socket, and the socket owned the pty, so looking at the second
         * account hung up the first — taking with it the workspace-trust
         * answer, which is why `claude` asked again every time.
         *
         * The daemon now keeps the shell either way (internal/ptyterm), so
         * this is belt and braces: it also preserves the scrollback and the
         * cursor position, which a remount would replay but not restore.
         */}
        {opened.map((name) => {
          const p = profiles.find((x) => x.name === name);
          if (!p) return null;
          return (
            <div key={name} hidden={shell?.name !== name} className="h-full min-h-0">
              <ShellTerminal profile={p} />
            </div>
          );
        })}

        {shell ? null : brainOpen ? (
          <BrainConsole />
        ) : active ? (
          <Terminal
            session={active}
            busy={isBusy(sessions)}
            onStop={(id) => void stop(id)}
            onClose={close}
            onRetry={(id, fresh) => void retry(id, fresh)}
          />
        ) : (
          <Empty
            className="h-full"
            title={<span className="display text-xl text-text">Açık terminal yok</span>}
            hint="Çalışan her job burada kendi sekmesini alır. Bitmiş bir oturumu Recents'ten açarsanız transcript baştan oynatılır."
          />
        )}
      </div>
    </div>
  );
}


/**
 * One openable thing in the rail: a scan, a shell, a run, a past run.
 *
 * The hover was a `useState` boolean, so moving the pointer down this list
 * re-rendered every row it crossed. CSS knows how to do this.
 *
 * The active row carries the same three marks the application's nav does — a
 * filled surface, a Lime rail and the label going to full Mist — because "this
 * is the one you are looking at" should be said the same way everywhere it is
 * said, and because said once it was not loud enough to be said at all.
 */
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
  return (
    <RailItem
      on={on}
      onSelect={onClick}
      lead={<StatusDot status={status} />}
      label={
        <span className="flex min-w-0 flex-col gap-0.5">
          <span className={cn("truncate text-base leading-tight", on && "font-medium")}>
            {label}
          </span>
          {detail && (
            <span className="truncate font-mono text-xs leading-none text-muted/60">
              {detail}
            </span>
          )}
        </span>
      }
    />
  );
}
