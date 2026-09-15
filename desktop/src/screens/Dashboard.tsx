import { AnimatePresence, motion } from "framer-motion";
import {
  Fragment,
  lazy,
  Suspense,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { cn } from "../lib/cn";
import { Badge } from "../components/ui/badge";
import { Icon, type IconName } from "../components/ui/icon";
import { MenuItem, MenuList } from "../components/ui/menu";
import { Orb } from "../components/ui/orb";
import {
  Popover,
  PopoverBody,
  PopoverFoot,
  PopoverHead,
} from "../components/ui/popover";
import { Card, CardBody, CardHeader } from "../components/ui/card";
import { Masthead } from "../components/ui/masthead";
import { Button, IconButton } from "../components/ui/button";
import { Overlay } from "../components/ui/overlay";
import { Pulse } from "../components/ui/pulse";
import { Stream } from "../components/ui/stream";
import { Rail, RailItem } from "../components/ui/rail";
import { SETTLE, screenSwap, useMotion } from "../lib/motion";
import {
  api,
  DaemonError,
  status as daemonStatus,
  type CodingModel,
  type Diagnostics,
  type DiagnosticsDependency,
  type LLMProviderList,
  type Run,
} from "../lib/daemon";
import {
  actionsFor,
  catalogParams,
  catalogSelection,
  catalogSummary,
  heldLabel,
  leadgenSummary,
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
  modelControlFor,
  retryOutcome,
  withCatalogSelection,
  type ColumnID,
} from "../lib/board";
import { Mark, Wordmark } from "../components/brand";
import {
  loadAttachments,
  TaskComposer,
  type Attached,
} from "../components/TaskComposer";
import {
  modelLabel,
  ModelSelect,
  ProviderModelPicker,
  useModels,
  useProviders,
} from "../components/ModelPicker";
import { modelForProvider } from "../lib/settings";
import { modelChangeWarning } from "../lib/catalog";
import { identityLabel, useAccounts } from "../components/AccountsProvider";
import { useTerminals } from "../components/TerminalsProvider";
import { useRuns, runTime, type BoardRun } from "../components/RunsProvider";
import { MODULES, type ModuleDef } from "../lib/modules";
import { NewTaskOverlay } from "../components/NewTaskOverlay";
import {
  DaemonFootnote,
  DependencyRow,
  HealthStrip,
  useDiagnostics,
} from "../components/DiagnosticsPanel";
import { Home } from "./Home";
import { Leadgen } from "./Leadgen";
import { Brain } from "./Brain";
import { ErrorBoundary } from "../components/ErrorBoundary";
import { Settings } from "./Settings";
import { Terminals } from "./Terminals";
import { Workspace } from "./Workspace";

/**
 * Katalog is loaded on demand, and it is the only screen that is.
 *
 * Its rich-text editor is the heaviest thing in this app — measured at +130 kB
 * gzipped, more than half the bundle again — and most launches never open it.
 * Everything else here is small enough that splitting it would buy a spinner
 * and nothing else.
 */
const Catalog = lazy(() => import("./catalog"));


/**
 * The app's one screen: a sidebar over the daemon's real modules (Coding
 * runner, Lead-gen), each rendering the existing, fully-wired `Workspace` /
 * `Leadgen` screen. Everything here — the connection pill, the dependency
 * list, the module descriptions — comes from `lib/daemon.ts` or is static
 * copy; there is no sample or placeholder data.
 */

// ---------------------------------------------------------------------------
// main component
// ---------------------------------------------------------------------------

// Brain sits with Genel/Board/Terminals rather than under MODÜLLER: it is not
// a module the daemon happens to expose, it is the store everything else
// writes into. Settings sits there for the mirror-image reason: it is not a
// thing the daemon does, it is what the operator has told it to do — and it
// governs modules rather than being one.
type Screen = "home" | "board" | "terminals" | "brain" | "settings" | "module";

/** The screen's own name, so a crash says which tab it was. The sidebar's
 * labels, not the type's members — the operator never saw "leadgen". */
function screenLabel(screen: Screen, mod: ModuleDef): string {
  switch (screen) {
    case "home":
      return "Genel";
    case "board":
      return "Board";
    case "terminals":
      return "Terminals";
    case "brain":
      return "Brain";
    case "settings":
      return "Ayarlar";
    case "module":
      return mod.name;
  }
}

export function Dashboard() {
  const [screen, setScreen] = useState<Screen>("home");
  const [activeModule, setActiveModule] = useState<ModuleDef>(MODULES[0]);
  // Which import the Katalog screen should open on, when a finished card sent
  // the operator there. Cleared by opening the module any other way, so the
  // screen does not keep reopening the last card's result.
  const [catalogImportID, setCatalogImportID] = useState<string>("");
  const [overlayOpen, setOverlayOpen] = useState(false);
  const [baseUrl, setBaseUrl] = useState<string | null>(null);
  const [connected, setConnected] = useState(false);
  const {
    diagnostics,
    error: diagnosticsError,
    refresh: refreshDiagnostics,
  } = useDiagnostics();
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

  const goHome = () => setScreen("home");
  const goBoard = () => setScreen("board");
  const goTerminals = () => setScreen("terminals");
  const goBrain = () => setScreen("brain");
  const goSettings = () => setScreen("settings");
  const goModule = (mod: ModuleDef) => {
    setActiveModule(mod);
    setCatalogImportID("");
    setScreen("module");
  };
  const goCatalog = (importID: string) => {
    const mod = MODULES.find((m) => m.key === "catalog");
    if (!mod) return;
    setActiveModule(mod);
    setCatalogImportID(importID);
    setScreen("module");
  };
  const openDiagnostics = () => {
    void refreshDiagnostics();
    setOverlayOpen(true);
  };

  // The key is what makes a screen a distinct thing to AnimatePresence: a
  // module is keyed by which module, so switching between Coding runner and
  // Lead-gen crosses rather than mutating one pane in place.
  const key = screen === "module" ? `module:${activeModule.key}` : screen;
  const swap = useMotion(screenSwap);

  return (
    <div className="relative grid h-full grid-rows-[auto_1fr] overflow-hidden bg-ground">
      <TitleBar
        baseUrl={baseUrl}
        connected={connected}
        diagnostics={diagnostics}
        diagnosticsError={diagnosticsError}
        onRefreshDiagnostics={() => void refreshDiagnostics()}
        onOpenDiagnostics={openDiagnostics}
      />

      <div className="grid min-h-0 grid-cols-[240px_1fr]">
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
        />

        {/* `.grid-ground` textures the pane the screens sit on. It is on the
            container and not on each screen so a screen that paints its own
            background simply covers it. */}
        <div className="grid-ground min-h-0 min-w-0 overflow-hidden">
          {/* One boundary around the screen rather than one per screen: a tab
              that throws takes itself down and leaves the sidebar standing, and
              the key means switching tabs is the way out of a broken one. */}
          <ErrorBoundary
            what={screenLabel(screen, activeModule)}
            resetKey={screen}
          >
            {/* `mode="wait"` because the two screens share one pane: overlapping
                them would put two scroll containers on top of each other for
                200ms, and the outgoing one is the taller of the two often
                enough that the pane would visibly jump. */}
            <AnimatePresence mode="wait" initial={false}>
              <motion.div
                key={key}
                variants={swap}
                initial="hidden"
                animate="shown"
                exit="gone"
                className="h-full min-h-0"
              >
                {screen === "home" && (
                  <Home
                    onGoBoard={goBoard}
                    onGoTerminals={goTerminals}
                    onGoModule={goModule}
                  />
                )}
                {screen === "board" && (
                  <BoardScreen
                    onGoTerminals={goTerminals}
                    onGoCatalog={goCatalog}
                  />
                )}
                {screen === "terminals" && <Terminals />}
                {screen === "brain" && <Brain />}
                {screen === "settings" && <Settings />}
                {screen === "module" && (
                  <ModuleScreen
                    mod={activeModule}
                    catalogImportID={catalogImportID}
                    onGoHome={goHome}
                    onGoTerminals={goTerminals}
                    onGoBoard={goBoard}
                  />
                )}
              </motion.div>
            </AnimatePresence>
          </ErrorBoundary>
        </div>
      </div>

      <DiagnosticsOverlay
        open={overlayOpen}
        baseUrl={baseUrl}
        diagnostics={diagnostics}
        error={diagnosticsError}
        onClose={() => setOverlayOpen(false)}
      />
    </div>
  );
}

// ---------------------------------------------------------------------------
// title bar + sidebar
// ---------------------------------------------------------------------------

/**
 * The title bar.
 *
 * 38px, no brand mark, and the word "orchestration" set in caps at the left —
 * which is to say the top of the application named a *category* rather than
 * the product, and the one place every screen shares carried nothing anybody
 * needed. 48px now, and it carries the symbol, the wordmark, and the daemon.
 *
 * The daemon reading is the part that actually changed. It used to open
 * `DiagnosticsOverlay`: a modal, over a scrim, in the middle of the screen,
 * dismissed before work could resume — for four lines of status. A status
 * check is not a decision and should not be staged like one. It is a
 * {@link Popover} now, hanging off the control that produced it, and the modal
 * is still there behind a button in its footer for the times the answer is
 * "something is broken, show me everything".
 */
function TitleBar({
  baseUrl,
  connected,
  diagnostics,
  diagnosticsError,
  onRefreshDiagnostics,
  onOpenDiagnostics,
}: {
  baseUrl: string | null;
  connected: boolean;
  diagnostics: Diagnostics | null;
  diagnosticsError: string | null;
  onRefreshDiagnostics: () => void;
  onOpenDiagnostics: () => void;
}) {
  const pill = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);

  return (
    <div className="relative flex h-12 items-center gap-3 border-b border-edge bg-panel px-4 shadow-elev-1 select-none">
      <Mark className="size-4 text-mist" />
      <Wordmark className="h-3 w-auto text-mist/90" />
      <span lang="en" className="label ml-1 text-muted/60">
        orchestration
      </span>

      <div className="flex-1" />

      {/* The one round thing at this end of the application. It is a status
          reading rather than a button-shaped button, and it is also the
          daemon's heartbeat: the dot ticks each time the shell hears back, so
          a dot that has gone still is the reading, not a dot that has gone
          red. */}
      <button
        ref={pill}
        type="button"
        aria-haspopup="dialog"
        aria-expanded={open}
        onClick={() => {
          if (!open) onRefreshDiagnostics();
          setOpen((was) => !was);
        }}
        className={cn(
          "focus-ring flex h-7 items-center gap-2 rounded-full pr-2.5 pl-2.5",
          "font-mono text-xs transition-colors duration-[var(--dur-fast)]",
          "outline outline-edge hover:outline-edge-strong",
          open ? "bg-raised text-text" : "text-muted hover:text-text",
        )}
      >
        <Pulse
          tone={connected ? "ok" : "bad"}
          beat={connected ? (baseUrl ?? "up") : undefined}
        />
        {baseUrl ?? "bağlanıyor…"}
      </button>

      <DaemonPopover
        open={open}
        anchor={pill}
        baseUrl={baseUrl}
        connected={connected}
        diagnostics={diagnostics}
        error={diagnosticsError}
        onClose={() => setOpen(false)}
        onOpenFull={() => {
          setOpen(false);
          onOpenDiagnostics();
        }}
      />
    </div>
  );
}

/**
 * The daemon, read without leaving the screen.
 *
 * Everything here comes from `GET /diagnostics` and from the shell's own
 * handshake. There is no summary line computed from a mood: the transport
 * either answered or it did not, and each dependency says for itself.
 */
function DaemonPopover({
  open,
  anchor,
  baseUrl,
  connected,
  diagnostics,
  error,
  onClose,
  onOpenFull,
}: {
  open: boolean;
  anchor: React.RefObject<HTMLButtonElement | null>;
  baseUrl: string | null;
  connected: boolean;
  diagnostics: Diagnostics | null;
  error: string | null;
  onClose: () => void;
  onOpenFull: () => void;
}) {
  return (
    <Popover
      open={open}
      onClose={onClose}
      anchor={anchor}
      align="end"
      width={320}
      label="Daemon durumu"
    >
      <PopoverHead
        title="Daemon"
        aside={
          <Badge tone={connected ? "ok" : "bad"} shape="status" dot>
            {connected ? "bağlı" : "yok"}
          </Badge>
        }
      />
      <PopoverBody flush className="flex flex-col gap-3.5">
        <p className="font-mono text-xs break-all text-muted">
          {baseUrl ?? "bağlanıyor…"}
        </p>
        <div className="h-px bg-edge" />
        <HealthStrip diagnostics={diagnostics} error={error} />
        {diagnostics && <DaemonFootnote diagnostics={diagnostics} />}
      </PopoverBody>
      <PopoverFoot>
        <Button variant="quiet" size="sm" iconAfter="arrowRight" onClick={onOpenFull}>
          Tam teşhis
        </Button>
      </PopoverFoot>
    </Popover>
  );
}

interface NavItem {
  key: string;
  label: string;
  icon: IconName;
  onSelect: () => void;
  /** A count in the trailing position — open terminals, and nothing else so far. */
  count?: number;
  /** Modules are marked: they are what the daemon does, as against the parts
   * of the shell itself. One dot each, and it does not move. */
  module?: boolean;
}

/**
 * The nav row, and the rail that slides between them.
 *
 * The selected row was `bg-raised` and a 2px Electric rail. Both halves were
 * invisible: `raised` sat six luminance points above the ground it was drawn
 * on, and Electric at #2547e8 against Carbon is a dark grey line. In practice
 * the sidebar did not show which screen you were on.
 *
 * It now says so three times over — a filled surface, a 3px Lime rail, and the
 * label going from `muted` to full Mist at medium weight — because "where am
 * I" is the one question a sidebar exists to answer and it should not need to
 * be squinted at. The rail is still one element shared across the rows by
 * `layoutId`, so it *travels* to the row you picked rather than blinking out
 * of one and into another.
 */
function NavRow({ item, on }: { item: NavItem; on: boolean }) {
  return (
    <RailItem
      label={item.label}
      icon={item.icon}
      on={on}
      onSelect={item.onSelect}
      layoutId="nav-rail"
      mark={
        item.count !== undefined && item.count > 0 ? (
          <span className="font-mono text-xs text-muted/60">{item.count}</span>
        ) : undefined
      }
    />
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
}) {
  const { account, status } = useAccounts();

  const mimir: NavItem[] = [
    { key: "home", label: "Genel", icon: "grid", onSelect: onGoHome },
    { key: "board", label: "Board", icon: "board", onSelect: onGoBoard },
    {
      key: "terminals",
      label: "Terminals",
      icon: "terminal",
      onSelect: onGoTerminals,
      count: terminalCount,
    },
    { key: "brain", label: "Brain", icon: "brain", onSelect: onGoBrain },
    { key: "settings", label: "Ayarlar", icon: "settings", onSelect: onGoSettings },
  ];

  const active = screen === "module" ? `module:${activeModuleKey}` : screen;
  const identity = identityLabel(status) || account?.label || "hesap bağlı değil";

  return (
    <div className="grid min-h-0 grid-rows-[1fr_auto] border-r border-edge bg-ground">
      <div className="flex flex-col gap-6 overflow-y-auto px-3 py-4">
        <Rail label="Mimir" className="gap-1">
          <SidebarLabel>Mimir</SidebarLabel>
          {mimir.map((item) => (
            <NavRow key={item.key} item={item} on={active === item.key} />
          ))}
        </Rail>

        <Rail label="Modüller" className="gap-1">
          <SidebarLabel>Modüller</SidebarLabel>
          {MODULES.map((mod) => (
            <NavRow
              key={mod.key}
              item={{
                key: `module:${mod.key}`,
                label: mod.name,
                icon: mod.icon,
                module: true,
                onSelect: () => onGoModule(mod),
              }}
              on={active === `module:${mod.key}`}
            />
          ))}
        </Rail>
      </div>

      {/* The foot used to hold the diagnostics button; that reading lives in
          the title bar now, beside the connection it describes. What belongs
          here is the identity every run is charged to — the one piece of state
          the operator has to be able to see without opening anything. */}
      <div className="flex items-center gap-2.5 border-t border-edge px-3 py-3">
        <Orb size={26} live={Boolean(account)} />
        <span className="min-w-0 flex-1 truncate text-sm text-muted" title={identity}>
          {identity}
        </span>
      </div>
    </div>
  );
}

/**
 * The group heading above a run of nav rows.
 *
 * `Terminals.tsx` had a byte-identical copy of this called `SectionLabel`,
 * differing only in its padding; it imports this one now.
 */
export function SidebarLabel({ children }: { children: ReactNode }) {
  return <span className="label px-3 pb-1 text-muted/60">{children}</span>;
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

/**
 * The five columns, marked.
 *
 * `queued` was `#e5a23d` — an amber that exists in no token, in eleven places,
 * and nobody had noticed it was a fifth colour. Queued is not a warning: it is
 * work that is going to happen, which is what Electric reports here, so it
 * takes Electric at half strength. Backlog is quieter still, because a card
 * nobody has committed to is the one state that should not catch the eye.
 */
const COLUMN_DOT: Record<ColumnID, string> = {
  backlog: "bg-muted/40",
  queued: "bg-electric/60",
  running: "bg-electric",
  done: "bg-lime",
  failed: "bg-bad",
};

function BoardScreen({
  onGoTerminals,
  onGoCatalog,
}: {
  onGoTerminals: () => void;
  onGoCatalog: (importID: string) => void;
}) {
  const terminals = useTerminals();
  const { models } = useModels();
  const providers = useProviders();
  const { runs, limits, error, loading, refresh } = useRuns();
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
    window.setTimeout(
      () => setNotice((current) => (current === message ? null : current)),
      4000,
    );
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
    <div className="grid h-full min-h-0 grid-rows-[auto_1fr]">
      <div className="px-6 pt-5">
        <Masthead
          title="Board"
          count={runs ? `${runs.length} kart` : "yükleniyor…"}
          aside={
            <>
              {/* The notice sits with the controls that cause it rather than
                  floating over the columns, and it is Electric because almost
                  everything it says is "queued" — a refusal to move a card, or
                  a card that will start when the current one ends. */}
              {notice && (
                <span className="max-w-[420px] text-right font-mono text-xs leading-[1.45] text-electric">
                  {notice}
                </span>
              )}
              <Button
                variant="ghost"
                size="sm"
                icon="refresh"
                loading={loading}
                onClick={() => void refresh()}
              >
                Yenile
              </Button>
              <Button icon="plus" onClick={() => setComposing(true)}>
                Yeni task
              </Button>
            </>
          }
        />
      </div>

      <div className="min-h-0 overflow-auto px-8 pt-5 pb-8">
        {error && (
          <p className="mb-4 font-mono text-xs leading-[1.5] text-bad">{error}</p>
        )}

        {runs === null ? (
          <p className="text-sm leading-relaxed text-muted/70">Kartlar yükleniyor…</p>
        ) : (
          <div className="grid min-w-[1040px] grid-cols-[repeat(5,minmax(0,1fr))] items-start gap-4">
            {BOARD_COLUMNS.map((column) => (
              <div
                key={column.id}
                onDragOver={(e) => {
                  if (column.droppable) e.preventDefault();
                }}
                onDrop={() => onDrop(column.id)}
                className={cn(
                  "flex min-h-[160px] flex-col gap-3 rounded-lg bg-sunken p-3",
                  "outline transition-[outline-color] duration-[var(--dur-fast)] ease-decisive",
                  // Only a column that will take the card lights up, and it
                  // lights up in the colour of the operator's own actions. The
                  // three runner-owned columns stay dark, which is the refusal
                  // said before the drop rather than after it.
                  dragged && column.droppable ? "outline-lime" : "outline-edge/70",
                )}
              >
                <div className="flex items-center gap-2 px-1">
                  <span
                    aria-hidden
                    className={`size-1.5 rounded-full ${COLUMN_DOT[column.id]}`}
                  />
                  <span className="label text-muted">{column.label}</span>
                  <span className="font-mono text-xs text-muted/60">
                    {grouped[column.id].length}
                  </span>
                </div>

                {grouped[column.id].length === 0 && (
                  <span className="px-1 text-sm leading-[1.5] text-muted/50">
                    {column.id === "backlog" ? "“Yeni task” ile başlayın" : "boş"}
                  </span>
                )}

                {grouped[column.id].map((run) => (
                  <RunCard
                    key={run.id}
                    run={run as BoardRun}
                    now={now}
                    held={heldLabel(run, limits)}
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
                    onDelete={() =>
                      void act(() => api.deleteCodingTask(run.id))
                    }
                    onTerminal={() => watch(run as BoardRun)}
                    onContinue={() => void retryCard(run as BoardRun, false)}
                    onRetry={() => void retryCard(run as BoardRun, true)}
                    onKick={() => void kickQueue()}
                    stalled={isStalled(run, runs, now)}
                    beingDragged={dragged?.id === run.id}
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
          providers={providers}
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
          onGoResult={() => {
            const p = catalogParams(selected);
            if (!p?.import_id) return;
            onGoCatalog(p.import_id);
            setSelected(null);
          }}
        />
      )}
    </div>
  );
}

/**
 * The primary action for a card in this state.
 *
 * A card had up to eight buttons on it in a wrapping flex, and on a 210px
 * column they wrapped to three rows — so a board of nine cards was a board of
 * about forty controls with nowhere for the eye to land. They were also all the
 * same weight, which said that "sil" and "run" were equally likely to be what
 * you came here to do.
 *
 * Each status has exactly one verb you almost always want. That one is a
 * button; the rest are a menu. The ranking lives here rather than inline so the
 * card cannot disagree with itself between renders, and `actionsFor` remains
 * the authority on which verbs are *legal* — this only orders the legal ones.
 */
function primaryAction(actions: string[], resumable: boolean, stalled: boolean): string | null {
  if (actions.includes("run")) return "run";
  if (actions.includes("stop")) return "stop";
  // A queued card nothing is moving: the useful verb is the one that says why,
  // not the one that takes it out of the queue.
  if (stalled && actions.includes("kick")) return "kick";
  if (actions.includes("continue") && resumable) return "continue";
  if (actions.includes("retry")) return "retry";
  if (actions.includes("terminal")) return "terminal";
  if (actions.includes("kick")) return "kick";
  return null;
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
  beingDragged,
  held,
}: {
  run: BoardRun;
  now: number;
  /**
   * When this queued card's work carries on, or empty.
   *
   * A parked card and a card that has simply not started yet are both `queued`,
   * and an operator who cannot tell them apart reads an overnight pause as a
   * hang. The answer is the daemon's own live holds, never a guess at the row's
   * error text.
   */
  held: string;
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
  /** This card is the one under the pointer. The only thing here that casts. */
  beingDragged: boolean;
}) {
  const overflow = useRef<HTMLButtonElement>(null);
  const [menuOpen, setMenuOpen] = useState(false);

  const actions = actionsFor(run.status, run.agent);
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

  const VERB: Record<string, { label: string; icon: IconName; act: () => void; hint?: string }> = {
    run: { label: "Çalıştır", icon: "play", act: onRun },
    kick: {
      label: "Kuyruğu yokla",
      icon: "refresh",
      act: onKick,
      hint: "Kuyruğu yeniden yoklar. Başlamıyorsa nedenini söyler — çoğunlukla bağlı hesap yoktur.",
    },
    stop: { label: "Durdur", icon: "stop", act: onStop },
    dequeue: { label: "Kuyruktan çıkar", icon: "close", act: onStop },
    continue: {
      label: "Devam et",
      icon: "play",
      act: onContinue,
      hint: `Oturumu kaldığı yerden sürdürür (--resume)${waits}`,
    },
    retry: {
      label: "Baştan dene",
      icon: "refresh",
      act: onRetry,
      hint: `Oturumu atar, görevi baştan çalıştırır${waits}`,
    },
    edit: {
      label: "Düzenle",
      icon: "settings",
      act: onEdit,
      hint: "Başlığı, isteği, modeli ve görselleri değiştirir",
    },
    terminal: { label: "Terminal", icon: "terminal", act: onTerminal },
    delete: { label: "Sil", icon: "trash", act: onDelete },
  };

  const primary = primaryAction(actions, resumable, stalled);
  // Everything else that is legal, in the order `actionsFor` gave it — the
  // daemon's ordering, not a second opinion about it.
  const rest = actions.filter(
    (a) => a !== primary && VERB[a] && !(a === "continue" && !resumable),
  );

  return (
    // `layout` is what makes a drop land. Without it a card that changes column
    // is unmounted from one list and mounted in another, which reads as a jump
    // and leaves the operator unsure whether the drop took. The spring carries
    // it, so the answer is visible in the motion itself.
    <motion.div
      layout
      layoutId={run.id}
      transition={SETTLE}
      draggable={draggable}
      onDragStart={onDragStart}
      onDragEnd={onDragEnd}
      onClick={onOpen}
      className={cn(
        "flex cursor-pointer flex-col gap-2.5 rounded-lg bg-panel p-3.5 shadow-elev-1",
        "outline outline-transparent",
        "transition-[outline-color,box-shadow] duration-[var(--dur-fast)] ease-decisive",
        "hover:outline-edge-strong",
        // Only while it is genuinely off the page does it cast a shadow.
        beingDragged && "shadow-elev-2 outline-lime",
      )}
    >
      <div className="flex items-center gap-2">
        <span className="font-mono text-xs text-muted/60">{run.id.slice(0, 8)}</span>
        {run.status === "stopped" && (
          <Badge tone="muted" className="px-1.5 py-0.5">
            durduruldu
          </Badge>
        )}
        <div className="flex-1" />
        {run.attachments && run.attachments.length > 0 && (
          <span className="flex items-center gap-1 font-mono text-xs text-muted/60">
            <Icon name="image" size={12} />
            {run.attachments.length}
          </span>
        )}
        {run.status === "running" && (
          <span className="figure text-xs text-lime">{elapsedLabel(run.started_at, now)}</span>
        )}
      </div>

      <span className="text-base leading-[1.45] text-text">{cardTitle(run)}</span>

      {/* What a lead-gen card will actually search. The prompt is the
          operator's sentence; this is the region and category the executor was
          handed, and the router has often read one into the other. */}
      {leadgenSummary(run) && (
        <span className="font-mono text-xs leading-[1.45] text-muted">
          ⌖ {leadgenSummary(run)}
        </span>
      )}

      {/* What a catalog card will rewrite, read from the card's own params.
          Never a second request: task-80 removed the board's 1+N fan-out, and
          a card body that fetched its own import would put it back one card at
          a time. */}
      {catalogSummary(run) && (
        <span className="font-mono text-xs leading-[1.45] text-muted">
          ⌖ {catalogSummary(run)}
        </span>
      )}

      {/* The pause, said out loud. "queued" alone cannot distinguish work that
          has not started from work the model budget stopped, and only one of
          them ends by itself. */}
      {held && (
        <Badge tone="warn" className="self-start px-1.5 py-0.5">
          {held}
        </Badge>
      )}

      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 font-mono text-xs text-muted/60">
        {/* Which sub-agent will do this, and under which instructions. Shown
            on every card rather than only the unusual ones: "coding" is a
            choice now, and a chip that appears only when the answer is
            surprising teaches the operator to read its absence as nothing. */}
        <AgentChip agent={run.agent} skills={run.skills} />
        {/* A card with no project names none — a sub-agent that opens no
            folder has nothing to put here, and an empty span would read as a
            project whose name failed to load. */}
        {run.projectName && <span>· {run.projectName}</span>}
        {modelName && <span>· {modelName}</span>}
        {when && <span>· {formatRelativeTime(when)}</span>}
      </div>

      {/* Said on the card rather than in a toast: this is the state the card is
          in, and it is still in it after the toast has gone. */}
      {stalled && (
        <p className="font-mono text-xs leading-[1.45] text-warn/80">
          kuyrukta bekliyor, çalışan yok — “kuyruğu yokla” nedenini söyler
        </p>
      )}

      {/* The run's own stream, on the card that is producing it. */}
      {run.status === "running" && <Stream />}

      <div className="flex items-center gap-1.5" onClick={(e) => e.stopPropagation()}>
        {primary && (
          <Button
            size="sm"
            variant={primary === "stop" ? "danger" : primary === "run" ? "primary" : "ghost"}
            icon={VERB[primary].icon}
            title={VERB[primary].hint}
            onClick={VERB[primary].act}
          >
            {VERB[primary].label}
          </Button>
        )}
        <div className="flex-1" />
        {rest.length > 0 && (
          <>
            <IconButton
              ref={overflow}
              name="more"
              label="Diğer işlemler"
              size="sm"
              aria-haspopup="menu"
              active={menuOpen}
              activeAria="expanded"
              onClick={() => setMenuOpen((was) => !was)}
            />
            <Popover
              open={menuOpen}
              onClose={() => setMenuOpen(false)}
              anchor={overflow}
              align="end"
              width={220}
              label="Kart işlemleri"
            >
              <MenuList label="Kart işlemleri">
                {rest.map((key) => (
                  <MenuItem
                    key={key}
                    icon={VERB[key].icon}
                    title={VERB[key].label}
                    tone={key === "delete" ? "danger" : "normal"}
                    onSelect={() => {
                      setMenuOpen(false);
                      VERB[key].act();
                    }}
                  />
                ))}
              </MenuList>
            </Popover>
          </>
        )}
      </div>
    </motion.div>
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
  providers,
  editing,
  onEditingChange,
  onClose,
  onTerminal,
  onSaved,
  onGoResult,
}: {
  run: BoardRun;
  models: CodingModel[] | null;
  /** The daemon's own providers, for a card whose model is one of those rather
   *  than a coding model. Null when the daemon did not answer, and then the
   *  card simply offers no model control — the pass still routes. */
  providers: LLMProviderList | null;
  editing: boolean;
  onEditingChange: (next: boolean) => void;
  onClose: () => void;
  onTerminal: () => void;
  onSaved: (updated: Run) => void;
  /** Opens the screen that owns this card's result. */
  onGoResult: () => void;
}) {
  const [attachments, setAttachments] = useState<Attached[]>([]);
  const [title, setTitle] = useState(run.title ?? "");
  const [prompt, setPrompt] = useState(run.prompt);
  const [model, setModel] = useState(run.model ?? "");
  const [saving, setSaving] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);

  const editable = canEdit(run.status);
  const control = modelControlFor(run);
  // A catalog card's model lives in its params, not in the column: the column
  // is rewritten with whatever the *last* attempt spent, so a card re-run after
  // a model change would show the old model until the new pass finished.
  const saved = catalogSelection(run);
  const [provider, setProvider] = useState(saved.provider);
  const [llmModel, setLLMModel] = useState(saved.model);

  // The form follows the card. It is re-seeded when the card itself changes —
  // a save returns a new row, and a poll can bring one in underneath — rather
  // than on every render, so typing is never overwritten mid-word.
  useEffect(() => {
    setTitle(run.title ?? "");
    setPrompt(run.prompt);
    setModel(run.model ?? "");
    const next = catalogSelection(run);
    setProvider(next.provider);
    setLLMModel(next.model);
    setProblem(null);
  }, [run.id, run.title, run.prompt, run.model, run.params]);

  useEffect(() => {
    if (!run.attachments || run.attachments.length === 0) {
      setAttachments([]);
      return;
    }
    void loadAttachments(run.attachments).then(setAttachments);
  }, [run.attachments]);

  const selectionChanged =
    control === "catalog" &&
    (provider !== saved.provider || llmModel !== saved.model);

  const dirty =
    title.trim() !== (run.title ?? "").trim() ||
    prompt.trim() !== run.prompt.trim() ||
    (control === "coding" && model !== (run.model ?? "")) ||
    selectionChanged ||
    attachments.map((a) => a.id).join(",") !==
      (run.attachments ?? []).join(",");

  const save = async () => {
    if (!prompt.trim()) return;
    setSaving(true);
    setProblem(null);
    try {
      const updated = await api.editCodingTask(run.id, {
        title: title.trim(),
        prompt: prompt.trim(),
        // The column and the params say the same thing when the model is
        // what changed: the executor reads the params, and every screen
        // already draws the column. Sent only then — a catalog card's column
        // holds whatever the last attempt actually spent, and overwriting that
        // while renaming the card would erase a true fact about it.
        model: control === "coding" ? model : selectionChanged ? llmModel : undefined,
        attachment_ids: attachments.map((a) => a.id),
        params:
          selectionChanged
            ? (withCatalogSelection(run, provider, llmModel) ?? undefined)
            : undefined,
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
    setProvider(saved.provider);
    setLLMModel(saved.model);
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
    // What the *next* attempt will spend, for a card that carries its own
    // choice; what the last one did for everyone else. A card re-run under a
    // new model used to read as unchanged here until the pass finished.
    [
      "model",
      control === "catalog"
        ? saved.model || "ayarlardaki model"
        : modelLabel(models, run.model) || run.model || "—",
    ],
    ["oturum", run.session_id ?? "—"],
    ["maliyet", run.cost_usd ? `$${run.cost_usd.toFixed(4)}` : "—"],
    ["tur", run.num_turns ? String(run.num_turns) : "—"],
    [
      "oluşturuldu",
      isSetTime(run.created_at)
        ? formatRelativeTime(run.created_at as string)
        : "—",
    ],
    [
      "başladı",
      isSetTime(run.started_at)
        ? formatRelativeTime(run.started_at as string)
        : "—",
    ],
    [
      "bitti",
      isSetTime(run.ended_at)
        ? formatRelativeTime(run.ended_at as string)
        : "—",
    ],
  ];

  return (
    <Overlay
      open
      onClose={onClose}
      width={600}
      title={cardTitle(run)}
      subtitle={<span className="font-mono">{run.id}</span>}
      aside={
        <>
          {editable && !editing && (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => onEditingChange(true)}
            >
              düzenle
            </Button>
          )}
          {/* A finished catalog card has a result somewhere else, and the
              board is not where it can be read. The door is offered only once
              there is something behind it — on a card still queued it would
              open an import with no drafts in it. */}
          {catalogParams(run) && run.status === "completed" && (
            <Button variant="ghost" size="sm" onClick={onGoResult}>
              ürünleri gör
            </Button>
          )}
          <Button variant="ghost" size="sm" onClick={onTerminal}>
            terminali aç
          </Button>
        </>
      }
      footer={
        editable && editing ? (
          <>
            <span className="mr-auto font-mono text-xs leading-[1.45] text-muted/60">
              {run.status === "queued"
                ? "kuyrukta — başlarsa kaydetme reddedilir"
                : "⌘↵ ile kaydet"}
            </span>
            <Button
              variant="ghost"
              size="sm"
              disabled={saving}
              onClick={cancel}
            >
              Vazgeç
            </Button>
            <Button
              size="sm"
              loading={saving}
              disabled={!dirty || !prompt.trim()}
              onClick={() => void save()}
            >
              Kaydet
            </Button>
          </>
        ) : undefined
      }
    >
      <div className="flex flex-col gap-3">
        {editable && editing ? (
          <>
            {control === "coding" && (
              <label className="flex min-w-0 flex-col gap-1.5">
                <span className="label text-muted">MODEL</span>
                <ModelSelect
                  models={models}
                  value={model}
                  onChange={setModel}
                  disabled={saving}
                />
              </label>
            )}

            {/* A catalog card spends the daemon's own provider, never a coding
                model, so this is the picker it gets — and changing it here is
                the whole point: a failed card is edited and then re-run, and
                the next attempt reads exactly this. */}
            {control === "catalog" && (
              <div className="flex flex-col gap-2">
                <ProviderModelPicker
                  providers={providers}
                  provider={provider}
                  model={llmModel}
                  routedLabel="Ayarlardaki model"
                  onProvider={(next) => {
                    setProvider(next);
                    setLLMModel(modelForProvider(providers, next));
                  }}
                  onModel={setLLMModel}
                />
                {modelChangeWarning(saved.model, llmModel) && (
                  <p className="max-w-[76ch] text-xs leading-relaxed text-warn">
                    {modelChangeWarning(saved.model, llmModel)}
                  </p>
                )}
              </div>
            )}

            {/* The honest answer for a card with no per-card choice: the
                control it used to be offered edited a field its executor never
                read, which is worse than no control at all. */}
            {control === "daemon" && (
              <p className="max-w-[76ch] text-xs leading-relaxed text-muted">
                Bu kart daemon'ın kendi modelini harcar; hangisi olduğu
                Ayarlar → Yönlendirme'de seçilir.
              </p>
            )}

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
              <p className="font-mono text-xs leading-[1.5] text-bad">
                {problem}
              </p>
            )}
          </>
        ) : (
          <>
            <pre className="font-mono text-sm leading-[1.6] break-words whitespace-pre-wrap text-muted">
              {run.prompt}
            </pre>

            {attachments.length > 0 && (
              <div className="flex flex-wrap gap-2">
                {attachments.map((att) => (
                  <img
                    key={att.id}
                    src={att.previewURI}
                    alt={att.filename}
                    className="size-[92px] rounded-sm border border-edge object-cover"
                  />
                ))}
              </div>
            )}

            {run.error && (
              <p className="font-mono text-sm leading-[1.6] whitespace-pre-wrap text-bad">
                {run.error}
              </p>
            )}
          </>
        )}

        <dl className="grid grid-cols-[auto_1fr] gap-x-3.5 gap-y-1.5 border-t border-edge pt-3">
          {rows.map(([key, value]) => (
            <Fragment key={key}>
              <dt className="label text-muted/60">{key}</dt>
              <dd className="font-mono text-sm leading-[1.45] break-words text-muted">
                {value}
              </dd>
            </Fragment>
          ))}
        </dl>
      </div>
    </Overlay>
  );
}

// ---------------------------------------------------------------------------
// module screen — chrome around the real Workspace / Leadgen screens
// ---------------------------------------------------------------------------

function ModuleScreen({
  mod,
  catalogImportID,
  onGoHome,
  onGoTerminals,
  onGoBoard,
}: {
  mod: ModuleDef;
  /** The import a finished card sent the operator here to look at. */
  catalogImportID?: string;
  onGoHome: () => void;
  onGoTerminals: () => void;
  /** Katalog links to the card it queued; nothing else here needs it. */
  onGoBoard: () => void;
}) {
  return (
    <div className="grid h-full min-h-0 grid-rows-[auto_1fr]">
      <div className="flex flex-col gap-1.5 px-6 pt-4">
        <Button
          variant="quiet"
          size="sm"
          className="self-start"
          onClick={onGoHome}
        >
          ← genel
        </Button>
        {/* The module's own route, which is what says it is wired to something
            rather than a placeholder. The tool list is joined only when there
            is one — "· —" was a separator with nothing on the other side. */}
        <Masthead
          title={mod.name}
          count={mod.tools === "—" ? mod.route : `${mod.route} · ${mod.tools}`}
        />
      </div>
      <div className="min-h-0 overflow-hidden">
        {mod.key === "coding" && <Workspace onGoTerminals={onGoTerminals} />}
        {mod.key === "leadgen" && <Leadgen />}
        {mod.key === "catalog" && (
          <Suspense
            fallback={
              <div className="flex h-full items-center justify-center">
                <p className="text-xs text-muted/60">katalog yükleniyor…</p>
              </div>
            }
          >
            <Catalog importID={catalogImportID} onGoBoard={onGoBoard} />
          </Suspense>
        )}
      </div>
    </div>
  );
}

/**
 * The sub-agent a card belongs to, and the skills it is held to.
 *
 * The skills are shown because the mandate is only worth having if it is
 * visible: a card that was run under instructions nobody can see is a card
 * whose output nobody can check. They are read-only here — the agent's
 * contract, not a second decision.
 */
function AgentChip({ agent, skills }: { agent?: string; skills?: string }) {
  const key = agent ?? "coding";
  const names = (skills ?? "").split(",").filter(Boolean);
  return (
    <span className="inline-flex items-center gap-1">
      <Badge
        tone={key === "coding" ? "muted" : "accent"}
        className="px-1.5 py-0.5"
      >
        {key}
      </Badge>
      {names.length > 0 && (
        <span title={`Bu kart şu skill'lere bağlı: ${names.join(", ")}`}>
          {names.join(" · ")}
        </span>
      )}
    </span>
  );
}

// ---------------------------------------------------------------------------
// diagnostics overlay — the daemon's own /diagnostics answer, verbatim
// ---------------------------------------------------------------------------

// agy first: it is the distil tier and, since task-51, it has no fallback, so
// it is the row an operator has to look at before any of the others. pdftotext
// is optional in the same sense the maps sidecar is — absent, PDFs are skipped
// and everything else still works.
const DEP_NAMES = [
  "agy",
  "crawl4ai",
  "claude",
  "pdftotext",
  "duckduckgo",
  "maps_scraper",
] as const;

function DiagnosticsOverlay({
  open,
  baseUrl,
  diagnostics,
  error,
  onClose,
}: {
  open: boolean;
  baseUrl: string | null;
  diagnostics: Diagnostics | null;
  error: string | null;
  onClose: () => void;
}) {
  return (
    <Overlay
      open={open}
      onClose={onClose}
      width={520}
      title={<Wordmark className="h-3.5 w-auto text-mist" />}
      subtitle="mimir-daemon launchd altında sürekli çalışır; uygulama ona bağlanır."
    >
      <div className="flex flex-col gap-3.5">
        <Card>
          <CardHeader
            title="Daemon"
            subtitle={baseUrl ?? "…"}
            aside={
              <Badge tone={baseUrl ? "ok" : "muted"} shape="status">
                {baseUrl ? "ready" : "…"}
              </Badge>
            }
          />
          <CardBody>
            <p className="text-xs leading-relaxed text-muted">
              Bağlandı. Token kabukta ve bu istemcide kalır — URL'e hiç girmez.
            </p>
          </CardBody>
        </Card>

        <Card>
          <CardHeader
            title="Bağımlılıklar"
            subtitle="daemon'ın kendi diagnostics çıktısı"
          />
          <CardBody className="flex flex-col gap-2.5">
            {error && (
              <p className="text-xs leading-relaxed text-bad">{error}</p>
            )}
            {!error && !diagnostics && (
              <p className="text-xs text-muted/70">yükleniyor…</p>
            )}
            {diagnostics &&
              DEP_NAMES.map((name) => {
                const dep = diagnostics.dependencies?.[name] as
                  DiagnosticsDependency | undefined;
                return dep ? (
                  <DependencyRow key={name} name={name} dep={dep} />
                ) : null;
              })}
            {diagnostics && <DaemonFootnote diagnostics={diagnostics} />}
          </CardBody>
        </Card>
      </div>
    </Overlay>
  );
}
