import {
  Fragment,
  useEffect,
  useMemo,
  useState,
  type CSSProperties,
  type ReactNode,
} from "react";
import { css, HoverButton, HoverDiv } from "../components/hover";
import {
  api,
  DaemonError,
  status as daemonStatus,
  type CodingModel,
  type Diagnostics,
  type DiagnosticsDependency,
  type Run,
} from "../lib/daemon";
import {
  actionsFor,
  allowedMove,
  BOARD_COLUMNS,
  canContinue,
  canEdit,
  cardTitle,
  columnOf,
  elapsedLabel,
  formatRelativeTime,
  groupRuns,
  isSetTime,
  isStalled,
  retryOutcome,
  type ColumnID,
} from "../lib/board";
import { Wordmark } from "../components/brand";
import { loadAttachments, TaskComposer, type Attached } from "../components/TaskComposer";
import { modelLabel, ModelSelect, useModels } from "../components/ModelPicker";
import { useTerminals } from "../components/TerminalsProvider";
import { useRuns, runTime, type BoardRun } from "../components/RunsProvider";
import { MODULES, type ModuleDef } from "../lib/modules";
import { NewTaskOverlay } from "../components/NewTaskOverlay";
import { useDiagnostics } from "../components/DiagnosticsPanel";
import { Home } from "./Home";
import { Leadgen } from "./Leadgen";
import { Brain } from "./Brain";
import { Settings } from "./Settings";
import { Terminals } from "./Terminals";
import { Workspace } from "./Workspace";

/**
 * The app's one screen: a sidebar over the daemon's real modules (Coding
 * runner, Lead-gen), each rendering the existing, fully-wired `Workspace` /
 * `Leadgen` screen. Everything here — the connection pill, the dependency
 * list, the module descriptions — comes from `lib/daemon.ts` or is static
 * copy; there is no sample or placeholder data.
 */

function navStyle(on: boolean): string {
  return [
    "display:flex;align-items:center;gap:9px;width:100%;text-align:left",
    `background:${on ? "#1c1f24" : "transparent"}`,
    "border:none;border-radius:6px;padding:7px 9px;cursor:pointer",
    `font:${on ? "500" : "450"} 12.5px/1 ui-sans-serif,system-ui`,
    `color:${on ? "#eef0f2" : "#8a9099"}`,
  ].join(";");
}

function navDotStyle(on: boolean): CSSProperties {
  return {
    width: 5,
    height: 5,
    borderRadius: "50%",
    flexShrink: 0,
    background: on ? "#2547e8" : "#3b3f48",
  };
}

// ---------------------------------------------------------------------------
// main component
// ---------------------------------------------------------------------------

// Brain sits with Genel/Board/Terminals rather than under MODÜLLER: it is not
// a module the daemon happens to expose, it is the store everything else
// writes into. Settings sits there for the mirror-image reason: it is not a
// thing the daemon does, it is what the operator has told it to do — and it
// governs modules rather than being one.
type Screen = "home" | "board" | "terminals" | "brain" | "settings" | "module";

export function Dashboard() {
  const [screen, setScreen] = useState<Screen>("home");
  const [activeModule, setActiveModule] = useState<ModuleDef>(MODULES[0]);
  const [overlayOpen, setOverlayOpen] = useState(false);
  const [baseUrl, setBaseUrl] = useState<string | null>(null);
  const [connected, setConnected] = useState(false);
  const { diagnostics, error: diagnosticsError, refresh: refreshDiagnostics } = useDiagnostics();
  const terminals = useTerminals();

  // The pill used to be a hardcoded green dot over an unhandled promise: it
  // reported a connection nobody had checked, and said "bağlanıyor…" forever
  // if the daemon never came up. Both halves come from the shell now.
  useEffect(() => {
    void daemonStatus()
      .then((s) => {
        if (s.state === "ready") {
          setBaseUrl(s.base_url);
          setConnected(true);
          return;
        }
        setBaseUrl(s.state === "failed" ? "daemon başlamadı" : null);
        setConnected(false);
      })
      .catch(() => {
        setBaseUrl("daemon'a ulaşılamıyor");
        setConnected(false);
      });
  }, []);

  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOverlayOpen(false);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  const goHome = () => setScreen("home");
  const goBoard = () => setScreen("board");
  const goTerminals = () => setScreen("terminals");
  const goBrain = () => setScreen("brain");
  const goSettings = () => setScreen("settings");
  const goModule = (mod: ModuleDef) => {
    setActiveModule(mod);
    setScreen("module");
  };
  const openDiagnostics = () => {
    void refreshDiagnostics();
    setOverlayOpen(true);
  };

  return (
    <div style={{ height: "100%", display: "grid", gridTemplateRows: "auto 1fr", background: "#101114", overflow: "hidden", position: "relative" }}>
      <TitleBar baseUrl={baseUrl} connected={connected} onOpenDiagnostics={openDiagnostics} />

      <div style={{ display: "grid", gridTemplateColumns: "214px 1fr", minHeight: 0 }}>
        <Sidebar
          screen={screen}
          activeModuleKey={screen === "module" ? activeModule.key : undefined}
          terminalCount={terminals.sessions.length}
          onGoHome={goHome}
          onGoBoard={goBoard}
          onGoTerminals={goTerminals}
          onGoBrain={goBrain}
          onGoSettings={goSettings}
          onGoModule={goModule}
          onOpenDiagnostics={openDiagnostics}
        />

        <div style={{ minHeight: 0, minWidth: 0, overflow: "hidden" }}>
          {screen === "home" && <Home onGoBoard={goBoard} onGoTerminals={goTerminals} onGoModule={goModule} />}
          {screen === "board" && <BoardScreen onGoTerminals={goTerminals} />}
          {screen === "terminals" && <Terminals />}
          {screen === "brain" && <Brain />}
          {screen === "settings" && <Settings />}
          {screen === "module" && <ModuleScreen mod={activeModule} onGoHome={goHome} onGoTerminals={goTerminals} />}
        </div>
      </div>

      {overlayOpen && (
        <DiagnosticsOverlay
          baseUrl={baseUrl}
          diagnostics={diagnostics}
          error={diagnosticsError}
          onClose={() => setOverlayOpen(false)}
        />
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// title bar + sidebar
// ---------------------------------------------------------------------------

function TitleBar({
  baseUrl,
  connected,
  onOpenDiagnostics,
}: {
  baseUrl: string | null;
  connected: boolean;
  onOpenDiagnostics: () => void;
}) {
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: 14,
        height: 38,
        padding: "0 14px",
        background: "#16181c",
        borderBottom: "1px solid #24272d",
        WebkitUserSelect: "none",
      }}
    >
      <span className="label" style={{ color: "#8a9099" }}>orchestration</span>
      <div style={{ flex: 1 }} />
      <button
        type="button"
        onClick={onOpenDiagnostics}
        style={{
          display: "flex",
          alignItems: "center",
          gap: 7,
          background: "none",
          border: "1px solid #24272d",
          borderRadius: 999,
          padding: "3px 9px 3px 8px",
          cursor: "pointer",
          font: "400 11px/1 ui-monospace,SFMono-Regular,Menlo,monospace",
          color: "#8a9099",
        }}
      >
        <span style={{ width: 6, height: 6, borderRadius: "50%", background: connected ? "#c6f04a" : "#e5484d" }} />
        {baseUrl ?? "bağlanıyor…"}
      </button>
    </div>
  );
}

function Sidebar({
  screen,
  activeModuleKey,
  terminalCount,
  onGoHome,
  onGoBoard,
  onGoTerminals,
  onGoBrain,
  onGoSettings,
  onGoModule,
  onOpenDiagnostics,
}: {
  screen: Screen;
  activeModuleKey?: string;
  terminalCount: number;
  onGoHome: () => void;
  onGoBoard: () => void;
  onGoTerminals: () => void;
  onGoBrain: () => void;
  onGoSettings: () => void;
  onGoModule: (mod: ModuleDef) => void;
  onOpenDiagnostics: () => void;
}) {
  return (
    <div style={{ borderRight: "1px solid #24272d", background: "#101114", display: "grid", gridTemplateRows: "1fr auto", minHeight: 0 }}>
      <div style={{ overflowY: "auto", padding: "14px 10px", display: "flex", flexDirection: "column", gap: 18 }}>
        <div style={{ display: "flex", flexDirection: "column", gap: 3 }}>
          <SidebarLabel>Mimir</SidebarLabel>
          <button type="button" onClick={onGoHome} style={css(navStyle(screen === "home"))}>
            <span style={navDotStyle(screen === "home")} />
            Genel
          </button>
          <button type="button" onClick={onGoBoard} style={css(navStyle(screen === "board"))}>
            <span style={navDotStyle(screen === "board")} />
            Board
          </button>
          <button type="button" onClick={onGoTerminals} style={css(navStyle(screen === "terminals"))}>
            <span style={navDotStyle(screen === "terminals")} />
            Terminals
            {terminalCount > 0 && (
              <span style={{ marginLeft: "auto", font: "400 10px/1 ui-monospace,Menlo,monospace", color: "#4f545e" }}>
                {terminalCount}
              </span>
            )}
          </button>
          <button type="button" onClick={onGoBrain} style={css(navStyle(screen === "brain"))}>
            <span style={navDotStyle(screen === "brain")} />
            Brain
          </button>
          <button type="button" onClick={onGoSettings} style={css(navStyle(screen === "settings"))}>
            <span style={navDotStyle(screen === "settings")} />
            Ayarlar
          </button>
        </div>

        <div style={{ display: "flex", flexDirection: "column", gap: 3 }}>
          <SidebarLabel>MODÜLLER</SidebarLabel>
          {MODULES.map((mod) => (
            <button key={mod.key} type="button" onClick={() => onGoModule(mod)} style={css(navStyle(screen === "module" && activeModuleKey === mod.key))}>
              <span style={{ width: 5, height: 5, borderRadius: "50%", background: "#c6f04a", flexShrink: 0 }} />
              {mod.name}
            </button>
          ))}
        </div>
      </div>

      <div style={{ borderTop: "1px solid #24272d", padding: 10 }}>
        <HoverButton
          base="display:flex;align-items:center;gap:9px;background:none;border:none;border-radius:6px;padding:7px 9px;cursor:pointer;font:450 12px/1 ui-sans-serif,system-ui;color:#8a9099;text-align:left;width:100%"
          hover="background:#16181c;color:#eef0f2"
          onClick={onOpenDiagnostics}
        >
          <span style={{ width: 5, height: 5, borderRadius: "50%", background: "#c6f04a", flexShrink: 0 }} />
          Daemon &amp; bağımlılıklar
        </HoverButton>
      </div>
    </div>
  );
}

function SidebarLabel({ children }: { children: ReactNode }) {
  return (
    <span style={{ font: "500 9.5px/1 ui-monospace,Menlo,monospace", letterSpacing: ".12em", color: "#4f545e", padding: "0 8px 6px" }}>
      {children}
    </span>
  );
}

// ---------------------------------------------------------------------------
// home screen
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// board — every coding task across every registered project, in the five
// states internal/store.RunStatus* actually has. Two of the columns are the
// operator's (Backlog, Queued) and three are the runner's; that is why only
// the first two accept a drop. Data is GET /coding-tasks?project_id=… once per
// project, merged client-side, because the daemon has no cross-project route.
// ---------------------------------------------------------------------------

const COLUMN_COLOR: Record<ColumnID, string> = {
  backlog: "#4f545e",
  queued: "#e5a23d",
  running: "#2547e8",
  done: "#c6f04a",
  failed: "#e5484d",
};

function BoardScreen({ onGoTerminals }: { onGoTerminals: () => void }) {
  const terminals = useTerminals();
  const { models } = useModels();
  const { runs, error, loading, refresh } = useRuns();
  const [selected, setSelected] = useState<BoardRun | null>(null);
  // Opening a card to read it and opening it to change it are the same overlay
  // and two intents: the card body opens the first, "düzenle" the second.
  const [editingCard, setEditingCard] = useState(false);
  const [composing, setComposing] = useState(false);
  const [dragged, setDragged] = useState<BoardRun | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [now, setNow] = useState(() => Date.now());

  const grouped = useMemo(() => groupRuns(runs ?? []), [runs]);

  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, []);

  const say = (message: string) => {
    setNotice(message);
    window.setTimeout(() => setNotice((current) => (current === message ? null : current)), 4000);
  };

  const act = async (what: () => Promise<unknown>) => {
    try {
      await what();
      await refresh();
    } catch (err) {
      say(err instanceof DaemonError ? err.message : String(err));
    }
  };

  const watch = (run: BoardRun) => {
    terminals.open(run);
    onGoTerminals();
  };

  const runCard = (run: BoardRun) =>
    act(async () => {
      await api.enqueueCodingTask(run.id);
      terminals.open(run);
    });

  // Whether a retry will start now or wait. Read once per render rather than
  // per card, because it is a property of the board, not of any one card.
  const busy = retryOutcome(runs) === "queued";

  /**
   * Continue (fresh = false) and start over (fresh = true).
   *
   * The terminal is opened either way: the card leaves the Failed column the
   * moment this returns, and an operator who pressed a button and watched the
   * card vanish has nowhere to look for what happened next.
   */
  const retryCard = (run: BoardRun, fresh: boolean) =>
    act(async () => {
      await api.retryCodingTask(run.id, fresh);
      terminals.open(run);
      say(
        busy
          ? "Kuyruğa alındı — çalışan task bitince başlayacak."
          : fresh
            ? "Baştan başlatılıyor."
            : "Kaldığı yerden devam ediyor.",
      );
    });

  /**
   * "Kuyruğu yokla": ask the dispatcher to look at the queue again.
   *
   * The daemon pumps its queue when work is released and when a run frees its
   * slot, never when the account changes — so a card queued while nothing was
   * connected sits there after the login that could start it. When there is
   * still nothing to start with, the daemon says so and `act` shows it, which
   * is the answer the operator was actually looking for.
   */
  const kickQueue = () =>
    act(async () => {
      await api.kickQueue();
      say("Kuyruk yoklandı — sıradaki kart başlayabiliyorsa başlıyor.");
    });

  const onDrop = (column: ColumnID) => {
    const run = dragged;
    setDragged(null);
    if (!run) return;
    const from = columnOf(run.status);
    if (!from) return;
    const move = allowedMove(from, column);
    if (!move.allowed) {
      if (move.reason) say(move.reason);
      return;
    }
    if (move.action === "retry") {
      // Dragged back into the queue: continue where there is a session to
      // continue, start over where there is not — the choice the two buttons
      // on the card make explicit, made here from the same fact.
      void retryCard(run, !canContinue(run));
      return;
    }
    void act(async () => {
      if (move.action === "enqueue") {
        await api.enqueueCodingTask(run.id);
        terminals.open(run);
      } else {
        await api.stopCodingTask(run.id);
      }
    });
  };

  return (
    <div style={{ height: "100%", display: "grid", gridTemplateRows: "auto 1fr", minHeight: 0 }}>
      <div style={{ padding: "18px 22px 12px", display: "flex", alignItems: "center", gap: 12, borderBottom: "1px solid #24272d", flexWrap: "wrap" }}>
        <h1 className="display" style={{ margin: 0, font: "400 18px/1.2 Aldrich,ui-sans-serif,system-ui", color: "#eef0f2" }}>
          Board
        </h1>
        <span style={{ font: "400 11px/1 ui-monospace,Menlo,monospace", color: "#6b7079" }}>
          {runs ? `${runs.length} kart` : "yükleniyor…"}
        </span>
        <div style={{ flex: 1 }} />
        {notice && (
          <span style={{ font: "400 11px/1.4 ui-monospace,Menlo,monospace", color: "#e5a23d", maxWidth: 460, textAlign: "right" }}>
            {notice}
          </span>
        )}
        <HoverButton
          base="background:#2547e8;border:none;border-radius:5px;padding:6px 12px;cursor:pointer;font:500 11.5px/1 ui-sans-serif,system-ui;color:#eef0f2"
          hover="background:#1d3ac4"
          onClick={() => setComposing(true)}
        >
          + Yeni task
        </HoverButton>
        <HoverButton
          base="background:none;border:1px solid #24272d;border-radius:5px;padding:6px 10px;cursor:pointer;font:400 10.5px/1 ui-monospace,Menlo,monospace;color:#8a9099"
          hover="border-color:#343841;color:#eef0f2"
          onClick={() => void refresh()}
        >
          {loading ? "…" : "yenile"}
        </HoverButton>
      </div>

      <div style={{ minHeight: 0, overflow: "auto", padding: "16px 22px 22px" }}>
        {error && (
          <p style={{ margin: "0 0 14px", font: "400 11.5px/1.5 ui-monospace,Menlo,monospace", color: "#e5484d" }}>{error}</p>
        )}

        {runs === null ? (
          <p style={{ margin: 0, font: "400 12px/1.6 ui-sans-serif,system-ui", color: "#6b7079" }}>
            Kartlar yükleniyor…
          </p>
        ) : (
          <div style={{ display: "grid", gridTemplateColumns: "repeat(5, minmax(210px, 1fr))", gap: 12, alignItems: "start", minWidth: 1120 }}>
            {BOARD_COLUMNS.map((column) => (
              <div
                key={column.id}
                onDragOver={(e) => {
                  if (column.droppable) e.preventDefault();
                }}
                onDrop={() => onDrop(column.id)}
                style={{
                  display: "flex",
                  flexDirection: "column",
                  gap: 9,
                  background: "#131518",
                  border: `1px solid ${dragged && column.droppable ? "#2547e8" : "#1c1f24"}`,
                  borderRadius: 8,
                  padding: 10,
                  minHeight: 140,
                }}
              >
                <div style={{ display: "flex", alignItems: "center", gap: 7 }}>
                  <span style={{ width: 6, height: 6, borderRadius: "50%", background: COLUMN_COLOR[column.id] }} />
                  <span className="label" style={{ color: "#8a9099" }}>{column.label}</span>
                  <span style={{ font: "400 10.5px/1 ui-monospace,Menlo,monospace", color: "#4f545e" }}>
                    {grouped[column.id].length}
                  </span>
                </div>

                {grouped[column.id].length === 0 && (
                  <span style={{ font: "400 11px/1.5 ui-sans-serif,system-ui", color: "#3b3f48" }}>
                    {column.id === "backlog" ? "“+ Yeni task” ile başlayın" : "boş"}
                  </span>
                )}

                {grouped[column.id].map((run) => (
                  <RunCard
                    key={run.id}
                    run={run as BoardRun}
                    now={now}
                    modelName={modelLabel(models, run.model)}
                    onOpen={() => {
                      setSelected(run as BoardRun);
                      setEditingCard(false);
                    }}
                    onEdit={() => {
                      setSelected(run as BoardRun);
                      setEditingCard(true);
                    }}
                    onDragStart={() => setDragged(run as BoardRun)}
                    onDragEnd={() => setDragged(null)}
                    busy={busy}
                    onRun={() => void runCard(run as BoardRun)}
                    onStop={() => void act(() => api.stopCodingTask(run.id))}
                    onDelete={() => void act(() => api.deleteCodingTask(run.id))}
                    onTerminal={() => watch(run as BoardRun)}
                    onContinue={() => void retryCard(run as BoardRun, false)}
                    onRetry={() => void retryCard(run as BoardRun, true)}
                    onKick={() => void kickQueue()}
                    stalled={isStalled(run, runs, now)}
                  />
                ))}
              </div>
            ))}
          </div>
        )}
      </div>

      {composing && (
        <NewTaskOverlay
          onClose={() => setComposing(false)}
          onCreated={() => {
            setComposing(false);
            void refresh();
          }}
        />
      )}
      {selected && (
        <RunDetailOverlay
          run={selected}
          models={models}
          editing={editingCard}
          onEditingChange={setEditingCard}
          onClose={() => setSelected(null)}
          onSaved={(updated) => {
            // Kept open on the card that was just saved rather than closed:
            // the operator is usually still reading what they changed.
            setSelected({ ...updated, projectName: selected.projectName });
            void refresh();
          }}
          onTerminal={() => {
            watch(selected);
            setSelected(null);
          }}
        />
      )}
    </div>
  );
}

function RunCard({
  run,
  now,
  modelName,
  busy,
  onOpen,
  onDragStart,
  onDragEnd,
  onRun,
  onStop,
  onDelete,
  onTerminal,
  onContinue,
  onRetry,
  onKick,
  onEdit,
  stalled,
}: {
  run: BoardRun;
  now: number;
  /** Which model this card will spend, or did. Empty when nothing is pinned. */
  modelName: string;
  /** Whether something is already running, which is what makes a retry wait. */
  busy: boolean;
  onOpen: () => void;
  onDragStart: () => void;
  onDragEnd: () => void;
  onRun: () => void;
  onStop: () => void;
  onDelete: () => void;
  onTerminal: () => void;
  onContinue: () => void;
  onRetry: () => void;
  onKick: () => void;
  onEdit: () => void;
  /** Queued, with nothing running and nothing having moved it for a while. */
  stalled: boolean;
}) {
  const actions = actionsFor(run.status);
  // Everything a drag can act on: the two operator columns, and a failure that
  // can be picked back up by dropping it in Queued.
  const draggable =
    run.status === "backlog" ||
    run.status === "queued" ||
    run.status === "failed" ||
    run.status === "stopped";
  const when = runTime(run);
  const resumable = canContinue(run);
  // The hint is where "it will wait" is said. Saying it on the button label
  // would make the two buttons change width as another card starts and stops.
  const waits = busy ? " — şu an bir task çalışıyor, kuyruğa alınır" : "";
  const continueHint = `Oturumu kaldığı yerden sürdürür (--resume)${waits}`;
  const kickHint = "Kuyruğu yeniden yoklar. Başlamıyorsa nedenini söyler — çoğunlukla bağlı hesap yoktur.";
  const retryHint = `Oturumu atar, görevi baştan çalıştırır${waits}`;

  return (
    <HoverDiv
      base="background:#16181c;border:1px solid #24272d;border-radius:7px;padding:10px 11px;display:flex;flex-direction:column;gap:8;cursor:pointer"
      hover="border-color:#343841"
      draggable={draggable}
      onDragStart={onDragStart}
      onDragEnd={onDragEnd}
      onClick={onOpen}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 7 }}>
        <span style={{ font: "400 10px/1 ui-monospace,Menlo,monospace", color: "#4f545e" }}>
          {run.id.slice(0, 8)}
        </span>
        {run.status === "stopped" && (
          <span style={{ font: "400 9.5px/1 ui-monospace,Menlo,monospace", color: "#8a9099", border: "1px solid #3b3f48", borderRadius: 3, padding: "2px 4px" }}>
            DURDURULDU
          </span>
        )}
        <div style={{ flex: 1 }} />
        {run.status === "running" && (
          <span style={{ font: "400 10px/1 ui-monospace,Menlo,monospace", color: "#2547e8" }}>
            {elapsedLabel(run.started_at, now)}
          </span>
        )}
        {(run.attachments?.length ?? 0) > 0 && (
          <span style={{ font: "400 10px/1 ui-monospace,Menlo,monospace", color: "#8a9099" }}>
            🖼 {run.attachments?.length}
          </span>
        )}
      </div>

      <p style={{ margin: 0, font: "450 12px/1.5 ui-sans-serif,system-ui", color: "#eef0f2", textWrap: "pretty" }}>
        {cardTitle(run)}
      </p>

      <div style={{ display: "flex", alignItems: "center", gap: 7, font: "400 10px/1 ui-monospace,Menlo,monospace", color: "#4f545e" }}>
        <span>{run.projectName}</span>
        {modelName && <span>· {modelName}</span>}
        {when && <span>· {formatRelativeTime(when)}</span>}
      </div>

      {/* Said on the card rather than in a toast: this is the state the card is
          in, and it is still in it after the toast has gone. */}
      {stalled && (
        <p style={{ margin: 0, font: "400 10px/1.4 ui-monospace,Menlo,monospace", color: "#e5a23d" }}>
          kuyrukta bekliyor, çalışan yok — “kuyruğu yokla” nedenini söyler
        </p>
      )}

      <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }} onClick={(e) => e.stopPropagation()}>
        {actions.includes("run") && <CardButton tone="accent" onClick={onRun}>▶ run</CardButton>}
        {actions.includes("kick") && (
          <CardButton tone={stalled ? "accent" : undefined} onClick={onKick} title={kickHint}>
            kuyruğu yokla
          </CardButton>
        )}
        {actions.includes("stop") && <CardButton tone="bad" onClick={onStop}>stop</CardButton>}
        {actions.includes("dequeue") && <CardButton onClick={onStop}>kuyruktan çıkar</CardButton>}
        {/* Continue is offered only when there is a session to resume; without
            one the card would promise to carry on and quietly start over. */}
        {actions.includes("continue") && resumable && (
          <CardButton tone="accent" onClick={onContinue} title={continueHint}>
            ▶ devam et
          </CardButton>
        )}
        {actions.includes("retry") && (
          <CardButton onClick={onRetry} title={retryHint}>
            baştan dene
          </CardButton>
        )}
        {actions.includes("edit") && (
          <CardButton onClick={onEdit} title="Başlığı, isteği, modeli ve görselleri değiştirir">
            düzenle
          </CardButton>
        )}
        {actions.includes("terminal") && <CardButton onClick={onTerminal}>terminal</CardButton>}
        {actions.includes("delete") && <CardButton onClick={onDelete}>sil</CardButton>}
      </div>
    </HoverDiv>
  );
}

function CardButton({
  children,
  onClick,
  tone,
  title,
}: {
  children: ReactNode;
  onClick: () => void;
  tone?: "accent" | "bad";
  title?: string;
}) {
  const color = tone === "accent" ? "#2547e8" : tone === "bad" ? "#e5484d" : "#8a9099";
  return (
    <HoverButton
      base={`background:none;border:1px solid ${tone ? color : "#24272d"};border-radius:4px;padding:3px 7px;cursor:pointer;font:400 10px/1 ui-monospace,Menlo,monospace;color:${color}`}
      hover="background:#1c1f24"
      onClick={onClick}
      title={title}
    >
      {children}
    </HoverButton>
  );
}

/**
 * One card, opened.
 *
 * It reads and it edits, because those are the same card: a board where
 * changing a title means deleting the task and writing it again is a board that
 * makes the operator do the computer's job. What may be changed is the daemon's
 * rule, not this component's — `canEdit` mirrors store.EditableStatuses — and
 * the daemon still refuses with a 409 if the dispatcher claimed the card while
 * this form was open, which is why the failure is shown here rather than
 * assumed away.
 */
function RunDetailOverlay({
  run,
  models,
  editing,
  onEditingChange,
  onClose,
  onTerminal,
  onSaved,
}: {
  run: BoardRun;
  models: CodingModel[] | null;
  editing: boolean;
  onEditingChange: (next: boolean) => void;
  onClose: () => void;
  onTerminal: () => void;
  onSaved: (updated: Run) => void;
}) {
  const [attachments, setAttachments] = useState<Attached[]>([]);
  const [title, setTitle] = useState(run.title ?? "");
  const [prompt, setPrompt] = useState(run.prompt);
  const [model, setModel] = useState(run.model ?? "");
  const [saving, setSaving] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);

  const editable = canEdit(run.status);

  // The form follows the card. It is re-seeded when the card itself changes —
  // a save returns a new row, and a poll can bring one in underneath — rather
  // than on every render, so typing is never overwritten mid-word.
  useEffect(() => {
    setTitle(run.title ?? "");
    setPrompt(run.prompt);
    setModel(run.model ?? "");
    setProblem(null);
  }, [run.id, run.title, run.prompt, run.model]);

  useEffect(() => {
    if (!run.attachments || run.attachments.length === 0) {
      setAttachments([]);
      return;
    }
    void loadAttachments(run.attachments).then(setAttachments);
  }, [run.attachments]);

  const dirty =
    title.trim() !== (run.title ?? "").trim() ||
    prompt.trim() !== run.prompt.trim() ||
    model !== (run.model ?? "") ||
    attachments.map((a) => a.id).join(",") !== (run.attachments ?? []).join(",");

  const save = async () => {
    if (!prompt.trim()) return;
    setSaving(true);
    setProblem(null);
    try {
      const updated = await api.editCodingTask(run.id, {
        title: title.trim(),
        prompt: prompt.trim(),
        model,
        attachment_ids: attachments.map((a) => a.id),
      });
      onSaved(updated);
      onEditingChange(false);
    } catch (err) {
      setProblem(err instanceof DaemonError ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  const cancel = () => {
    setTitle(run.title ?? "");
    setPrompt(run.prompt);
    setModel(run.model ?? "");
    setProblem(null);
    if (run.attachments && run.attachments.length > 0) {
      void loadAttachments(run.attachments).then(setAttachments);
    } else {
      setAttachments([]);
    }
    onEditingChange(false);
  };

  const rows: [string, string][] = [
    ["proje", run.projectName],
    ["durum", run.status],
    ["model", modelLabel(models, run.model) || run.model || "—"],
    ["oturum", run.session_id ?? "—"],
    ["maliyet", run.cost_usd ? `$${run.cost_usd.toFixed(4)}` : "—"],
    ["tur", run.num_turns ? String(run.num_turns) : "—"],
    ["oluşturuldu", isSetTime(run.created_at) ? formatRelativeTime(run.created_at as string) : "—"],
    ["başladı", isSetTime(run.started_at) ? formatRelativeTime(run.started_at as string) : "—"],
    ["bitti", isSetTime(run.ended_at) ? formatRelativeTime(run.ended_at as string) : "—"],
  ];

  return (
    <div onClick={onClose} style={{ position: "absolute", inset: 0, background: "rgba(10,11,13,.86)", display: "grid", placeItems: "center", padding: 28 }}>
      <div
        onClick={(e) => e.stopPropagation()}
        style={{ width: 600, maxWidth: "100%", maxHeight: "80vh", overflowY: "auto", background: "#16181c", border: "1px solid #24272d", borderRadius: 10, padding: 18, display: "flex", flexDirection: "column", gap: 13 }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <span style={{ font: "400 11px/1 ui-monospace,Menlo,monospace", color: "#6b7079" }}>{run.id}</span>
          <div style={{ flex: 1 }} />
          {editable && !editing && (
            <HoverButton
              base="background:none;border:1px solid #24272d;border-radius:5px;padding:4px 9px;cursor:pointer;font:400 10.5px/1 ui-monospace,Menlo,monospace;color:#8a9099"
              hover="border-color:#343841;color:#eef0f2"
              onClick={() => onEditingChange(true)}
            >
              düzenle
            </HoverButton>
          )}
          <HoverButton
            base="background:none;border:1px solid #24272d;border-radius:5px;padding:4px 9px;cursor:pointer;font:400 10.5px/1 ui-monospace,Menlo,monospace;color:#8a9099"
            hover="border-color:#343841;color:#eef0f2"
            onClick={onTerminal}
          >
            terminali aç
          </HoverButton>
          <HoverButton base="background:none;border:none;cursor:pointer;font:400 13px/1 ui-monospace,Menlo,monospace;color:#6b7079" hover="color:#eef0f2" onClick={onClose}>
            ✕
          </HoverButton>
        </div>

        {editable && editing ? (
          <>
            <label style={{ display: "flex", flexDirection: "column", gap: 5, minWidth: 0 }}>
              <span className="label" style={{ color: "#6b7079" }}>MODEL</span>
              <ModelSelect models={models} value={model} onChange={setModel} disabled={saving} />
            </label>

            {/* The same composer the new-task form uses, so an image added
                after the fact arrives the way the first ones did: paste, drop
                or pick. */}
            <TaskComposer
              title={title}
              onTitleChange={setTitle}
              prompt={prompt}
              onPromptChange={setPrompt}
              attachments={attachments}
              onAttachmentsChange={setAttachments}
              disabled={saving}
              rows={8}
              onSubmit={() => void save()}
            />

            {problem && (
              <p style={{ margin: 0, font: "400 11px/1.5 ui-monospace,Menlo,monospace", color: "#e5484d" }}>{problem}</p>
            )}

            <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
              <HoverButton
                base="background:#2547e8;border:none;border-radius:5px;padding:7px 13px;cursor:pointer;font:500 11.5px/1 ui-sans-serif,system-ui;color:#eef0f2"
                hover="background:#1d3ac4"
                disabled={saving || !dirty || !prompt.trim()}
                onClick={() => void save()}
              >
                {saving ? "kaydediliyor…" : "Kaydet"}
              </HoverButton>
              <HoverButton
                base="background:none;border:1px solid #24272d;border-radius:5px;padding:7px 13px;cursor:pointer;font:450 11.5px/1 ui-sans-serif,system-ui;color:#8a9099"
                hover="border-color:#343841;color:#eef0f2"
                disabled={saving}
                onClick={cancel}
              >
                Vazgeç
              </HoverButton>
              <span style={{ font: "400 10.5px/1.4 ui-monospace,Menlo,monospace", color: "#4f545e" }}>
                {run.status === "queued"
                  ? "kuyrukta — başlarsa kaydetme reddedilir"
                  : "⌘↵ ile kaydet"}
              </span>
            </div>
          </>
        ) : (
          <>
            <h2 style={{ margin: 0, font: "500 14px/1.4 ui-sans-serif,system-ui", color: "#eef0f2" }}>{cardTitle(run)}</h2>

            <pre style={{ margin: 0, font: "400 11.5px/1.6 ui-monospace,Menlo,monospace", color: "#8a9099", whiteSpace: "pre-wrap", wordBreak: "break-word" }}>
              {run.prompt}
            </pre>

            {attachments.length > 0 && (
              <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
                {attachments.map((att) => (
                  <img
                    key={att.id}
                    src={att.previewURI}
                    alt={att.filename}
                    style={{ width: 92, height: 92, objectFit: "cover", borderRadius: 6, border: "1px solid #24272d" }}
                  />
                ))}
              </div>
            )}

            {run.error && (
              <p style={{ margin: 0, font: "400 11.5px/1.6 ui-monospace,Menlo,monospace", color: "#e5484d", whiteSpace: "pre-wrap" }}>{run.error}</p>
            )}
          </>
        )}

        <div style={{ display: "grid", gridTemplateColumns: "auto 1fr", gap: "5px 14px" }}>
          {rows.map(([key, value]) => (
            <Fragment key={key}>
              <span className="label" style={{ color: "#4f545e" }}>{key}</span>
              <span style={{ font: "400 11.5px/1.4 ui-monospace,Menlo,monospace", color: "#8a9099" }}>{value}</span>
            </Fragment>
          ))}
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// module screen — chrome around the real Workspace / Leadgen screens
// ---------------------------------------------------------------------------

function ModuleScreen({
  mod,
  onGoHome,
  onGoTerminals,
}: {
  mod: ModuleDef;
  onGoHome: () => void;
  onGoTerminals: () => void;
}) {
  return (
    <div style={{ height: "100%", display: "grid", gridTemplateRows: "auto 1fr", minHeight: 0 }}>
      <div style={{ padding: "14px 20px 12px", display: "flex", flexDirection: "column", gap: 6, borderBottom: "1px solid #24272d" }}>
        <HoverButton
          base="align-self:flex-start;background:none;border:none;padding:0;cursor:pointer;font:400 10.5px/1 ui-monospace,Menlo,monospace;color:#6b7079"
          hover="color:#2547e8"
          onClick={onGoHome}
        >
          ← genel
        </HoverButton>
        <div style={{ display: "flex", alignItems: "baseline", gap: 11, flexWrap: "wrap" }}>
          <h1 className="display" style={{ margin: 0, font: "400 18px/1.2 Aldrich,ui-sans-serif,system-ui", color: "#eef0f2" }}>{mod.name}</h1>
          <span style={{ font: "400 10.5px/1 ui-monospace,Menlo,monospace", color: "#6b7079" }}>
            {mod.route} · {mod.tools}
          </span>
        </div>
      </div>
      <div style={{ minHeight: 0, overflow: "hidden" }}>
        {mod.key === "coding" && <Workspace onGoTerminals={onGoTerminals} />}
        {mod.key === "leadgen" && <Leadgen />}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// diagnostics overlay — the daemon's own /diagnostics answer, verbatim
// ---------------------------------------------------------------------------

// agy first: it is the distil tier and, since task-51, it has no fallback, so
// it is the row an operator has to look at before any of the others. pdftotext
// is optional in the same sense the maps sidecar is — absent, PDFs are skipped
// and everything else still works.
const DEP_NAMES = ["agy", "crawl4ai", "claude", "pdftotext", "duckduckgo", "maps_scraper"] as const;

function DiagnosticsOverlay({
  baseUrl,
  diagnostics,
  error,
  onClose,
}: {
  baseUrl: string | null;
  diagnostics: Diagnostics | null;
  error: string | null;
  onClose: () => void;
}) {
  return (
    <div onClick={onClose} style={{ position: "absolute", inset: 0, background: "#101114", display: "grid", placeItems: "center", padding: 28 }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: 520, maxWidth: "100%", display: "flex", flexDirection: "column", gap: 14 }}>
        <div style={{ display: "flex", flexDirection: "column", gap: 5 }}>
          <Wordmark className="h-3.5 w-auto text-mist" />
          <span style={{ font: "400 11px/1.5 ui-monospace,Menlo,monospace", color: "#8a9099" }}>
            mimir-daemon launchd altında sürekli çalışır; uygulama ona bağlanır.
          </span>
        </div>

        <div style={{ border: "1px solid #24272d", borderRadius: 9, background: "#16181c", overflow: "hidden" }}>
          <div style={{ display: "flex", alignItems: "flex-start", justifyContent: "space-between", gap: 14, padding: "12px 14px", borderBottom: "1px solid #24272d" }}>
            <div style={{ display: "flex", flexDirection: "column", gap: 3 }}>
              <span style={{ font: "600 12.5px/1 ui-sans-serif,system-ui" }}>Daemon</span>
              <span style={{ font: "400 10.5px/1 ui-monospace,Menlo,monospace", color: "#8a9099" }}>{baseUrl ?? "…"}</span>
            </div>
            <span style={{ border: "1px solid rgba(78,168,122,.4)", borderRadius: 999, padding: "3px 9px", font: "500 10.5px/1 ui-monospace,Menlo,monospace", color: "#c6f04a" }}>
              ready
            </span>
          </div>
          <div style={{ padding: "12px 14px", display: "flex", flexDirection: "column", gap: 11 }}>
            <span style={{ font: "400 12px/1.6 ui-sans-serif,system-ui", color: "#8a9099", textWrap: "pretty" }}>
              Bağlandı. Token kabukta ve bu istemcide kalır — URL'e hiç girmez.
            </span>
            <HoverButton
              base="align-self:flex-start;background:#2547e8;border:none;color:#101114;border-radius:6px;padding:8px 13px;font:600 11.5px/1 ui-sans-serif,system-ui;cursor:pointer"
              onClick={onClose}
            >
              Kapat
            </HoverButton>
          </div>
        </div>

        <div style={{ border: "1px solid #24272d", borderRadius: 9, background: "#16181c", overflow: "hidden" }}>
          <div style={{ padding: "11px 14px", borderBottom: "1px solid #24272d", display: "flex", flexDirection: "column", gap: 3 }}>
            <span style={{ font: "600 12.5px/1 ui-sans-serif,system-ui" }}>Bağımlılıklar</span>
            <span style={{ font: "400 10.5px/1 ui-monospace,Menlo,monospace", color: "#8a9099" }}>daemon'ın kendi diagnostics çıktısı</span>
          </div>
          <div style={{ padding: "11px 14px", display: "flex", flexDirection: "column", gap: 9 }}>
            {error && <span style={{ font: "400 12px/1.6 ui-sans-serif,system-ui", color: "#e5484d" }}>{error}</span>}
            {!error && !diagnostics && (
              <span style={{ font: "400 12px/1.6 ui-sans-serif,system-ui", color: "#6b7079" }}>yükleniyor…</span>
            )}
            {diagnostics &&
              DEP_NAMES.map((name) => {
                const dep = diagnostics.dependencies?.[name] as DiagnosticsDependency | undefined;
                if (!dep) return null;
                return (
                  <div key={name} style={{ display: "flex", alignItems: "flex-start", justifyContent: "space-between", gap: 14 }}>
                    <div style={{ minWidth: 0 }}>
                      <div style={{ font: "450 12px/1.4 ui-sans-serif,system-ui" }}>{name}</div>
                      {dep.detail && <div style={{ font: "400 10.5px/1 ui-monospace,Menlo,monospace", color: "#6b7079" }}>{dep.detail}</div>}
                    </div>
                    <span
                      style={{
                        border: `1px solid ${dep.ok ? "rgba(78,168,122,.4)" : "#2c3037"}`,
                        borderRadius: 999,
                        padding: "3px 8px",
                        font: "500 10px/1 ui-monospace,Menlo,monospace",
                        color: dep.ok ? "#c6f04a" : "#8a9099",
                      }}
                    >
                      {dep.ok ? "ok" : dep.optional ? "optional, down" : "down"}
                    </span>
                  </div>
                );
              })}
            {diagnostics && (
              <span style={{ font: "400 10.5px/1.5 ui-monospace,Menlo,monospace", color: "#4f545e", paddingTop: 2 }}>
                store {diagnostics.daemon.store} · {diagnostics.daemon.projects} proje · v{diagnostics.daemon.version}
              </span>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
