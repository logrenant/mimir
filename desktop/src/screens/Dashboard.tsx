import { useCallback, useEffect, useState, type CSSProperties, type ReactNode } from "react";
import { api, DaemonError, endpoint, type Diagnostics, type DiagnosticsDependency, type Run } from "../lib/daemon";
import { Leadgen } from "./Leadgen";
import { Workspace } from "./Workspace";

/**
 * The app's one screen: a sidebar over the daemon's real modules (Coding
 * runner, Lead-gen), each rendering the existing, fully-wired `Workspace` /
 * `Leadgen` screen. Everything here — the connection pill, the dependency
 * list, the module descriptions — comes from `lib/daemon.ts` or is static
 * copy; there is no sample or placeholder data.
 */

// ---------------------------------------------------------------------------
// style plumbing
// ---------------------------------------------------------------------------

/**
 * Parses a `"prop:value;prop:value"` CSS string into a React style object.
 *
 * `border` is expanded into its width/style/color longhand so it never mixes
 * with a hover delta's `border-color` — React warns when a shorthand and its
 * longhand are both set across a rerender, and the color can go stale.
 */
function css(source: string): CSSProperties {
  const out: Record<string, string> = {};
  for (const decl of source.split(";")) {
    const i = decl.indexOf(":");
    if (i === -1) continue;
    const prop = decl.slice(0, i).trim();
    const value = decl.slice(i + 1).trim();
    if (!prop || !value) continue;
    if (prop === "border") {
      const parts = value.split(/\s+/);
      if (parts.length === 3) {
        out.borderWidth = parts[0];
        out.borderStyle = parts[1];
        out.borderColor = parts[2];
        continue;
      }
    }
    const camel = prop.replace(/-([a-z])/g, (_, c: string) => c.toUpperCase());
    out[camel] = value;
  }
  return out as CSSProperties;
}

type HoverDivProps = React.ComponentPropsWithoutRef<"div"> & {
  base: string;
  hover?: string;
};

function HoverDiv({ base, hover, style, onMouseEnter, onMouseLeave, ...rest }: HoverDivProps) {
  const [hovered, setHovered] = useState(false);
  return (
    <div
      {...rest}
      style={{ ...css(base), ...(hovered && hover ? css(hover) : {}), ...style }}
      onMouseEnter={(e) => {
        setHovered(true);
        onMouseEnter?.(e);
      }}
      onMouseLeave={(e) => {
        setHovered(false);
        onMouseLeave?.(e);
      }}
    />
  );
}

type HoverButtonProps = React.ComponentPropsWithoutRef<"button"> & {
  base: string;
  hover?: string;
};

function HoverButton({ base, hover, style, onMouseEnter, onMouseLeave, type, ...rest }: HoverButtonProps) {
  const [hovered, setHovered] = useState(false);
  return (
    <button
      {...rest}
      type={type ?? "button"}
      style={{ ...css(base), ...(hovered && hover ? css(hover) : {}), ...style }}
      onMouseEnter={(e) => {
        setHovered(true);
        onMouseEnter?.(e);
      }}
      onMouseLeave={(e) => {
        setHovered(false);
        onMouseLeave?.(e);
      }}
    />
  );
}

function navStyle(on: boolean): string {
  return [
    "display:flex;align-items:center;gap:9px;width:100%;text-align:left",
    `background:${on ? "#1b1e24" : "transparent"}`,
    "border:none;border-radius:6px;padding:7px 9px;cursor:pointer",
    `font:${on ? "500" : "450"} 12.5px/1 ui-sans-serif,system-ui`,
    `color:${on ? "#e6e8ec" : "#8b919c"}`,
  ].join(";");
}

function navDotStyle(on: boolean): CSSProperties {
  return {
    width: 5,
    height: 5,
    borderRadius: "50%",
    flexShrink: 0,
    background: on ? "#d8b44a" : "#3a3d45",
  };
}

// ---------------------------------------------------------------------------
// real module registry — every entry here is a screen this app actually runs
// ---------------------------------------------------------------------------

interface ModuleDef {
  key: "coding" | "leadgen";
  name: string;
  desc: string;
  route: string;
  tools: string;
}

const MODULES: ModuleDef[] = [
  {
    key: "coding",
    name: "Coding runner",
    desc: "Klasör kapsamlı claude oturumu; canlı akış, araç çağrıları, maliyet.",
    route: "POST /coding-tasks · GET /ws/runs/{id}",
    tools: "Read · Edit · Bash · Grep",
  },
  {
    key: "leadgen",
    name: "Lead-gen · Maps",
    desc: "Bölge araması → kategorize → kategori başına gap analizi → taslak e-postalar.",
    route: "POST /maps/leadgen · POST /maps/emails/status",
    tools: "maps_search · gmaps_business_lookup",
  },
];

// ---------------------------------------------------------------------------
// diagnostics — the one piece of live daemon state the shell shows
// ---------------------------------------------------------------------------

function useDiagnostics() {
  const [diagnostics, setDiagnostics] = useState<Diagnostics | null>(null);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      setDiagnostics(await api.diagnostics());
      setError(null);
    } catch (err) {
      setError(err instanceof DaemonError ? err.message : String(err));
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  return { diagnostics, error, refresh };
}

// ---------------------------------------------------------------------------
// main component
// ---------------------------------------------------------------------------

type Screen = "home" | "board" | "module";

export function Dashboard() {
  const [screen, setScreen] = useState<Screen>("home");
  const [activeModule, setActiveModule] = useState<ModuleDef>(MODULES[0]);
  const [overlayOpen, setOverlayOpen] = useState(false);
  const [baseUrl, setBaseUrl] = useState<string | null>(null);
  const { diagnostics, error: diagnosticsError, refresh: refreshDiagnostics } = useDiagnostics();

  useEffect(() => {
    void endpoint().then((ep) => setBaseUrl(ep.base_url));
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
  const goModule = (mod: ModuleDef) => {
    setActiveModule(mod);
    setScreen("module");
  };
  const openDiagnostics = () => {
    void refreshDiagnostics();
    setOverlayOpen(true);
  };

  return (
    <div style={{ height: "100%", display: "grid", gridTemplateRows: "auto 1fr", background: "#0b0c0e", overflow: "hidden", position: "relative" }}>
      <TitleBar baseUrl={baseUrl} onOpenDiagnostics={openDiagnostics} />

      <div style={{ display: "grid", gridTemplateColumns: "214px 1fr", minHeight: 0 }}>
        <Sidebar
          screen={screen}
          activeModuleKey={screen === "module" ? activeModule.key : undefined}
          onGoHome={goHome}
          onGoBoard={goBoard}
          onGoModule={goModule}
          onOpenDiagnostics={openDiagnostics}
        />

        <div style={{ minHeight: 0, minWidth: 0, overflow: "hidden" }}>
          {screen === "home" && <HomeScreen onGoModule={goModule} />}
          {screen === "board" && <BoardScreen onGoModule={goModule} />}
          {screen === "module" && <ModuleScreen mod={activeModule} onGoHome={goHome} />}
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

function TitleBar({ baseUrl, onOpenDiagnostics }: { baseUrl: string | null; onOpenDiagnostics: () => void }) {
  return (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: 14,
        height: 38,
        padding: "0 14px",
        background: "#131519",
        borderBottom: "1px solid #23262d",
        WebkitUserSelect: "none",
      }}
    >
      <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
        <span style={{ width: 11, height: 11, borderRadius: "50%", background: "#3a3d45" }} />
        <span style={{ width: 11, height: 11, borderRadius: "50%", background: "#3a3d45" }} />
        <span style={{ width: 11, height: 11, borderRadius: "50%", background: "#3a3d45" }} />
      </div>
      <span style={{ font: "600 12px/1 ui-sans-serif,system-ui", letterSpacing: ".14em", color: "#e6e8ec", marginLeft: 6 }}>GOAT</span>
      <span style={{ font: "400 11px/1 ui-monospace,SFMono-Regular,Menlo,monospace", color: "#8b919c" }}>orchestration</span>
      <div style={{ flex: 1 }} />
      <button
        type="button"
        onClick={onOpenDiagnostics}
        style={{
          display: "flex",
          alignItems: "center",
          gap: 7,
          background: "none",
          border: "1px solid #23262d",
          borderRadius: 999,
          padding: "3px 9px 3px 8px",
          cursor: "pointer",
          font: "400 11px/1 ui-monospace,SFMono-Regular,Menlo,monospace",
          color: "#8b919c",
        }}
      >
        <span style={{ width: 6, height: 6, borderRadius: "50%", background: "#4ea87a" }} />
        {baseUrl ?? "bağlanıyor…"}
      </button>
    </div>
  );
}

function Sidebar({
  screen,
  activeModuleKey,
  onGoHome,
  onGoBoard,
  onGoModule,
  onOpenDiagnostics,
}: {
  screen: Screen;
  activeModuleKey?: string;
  onGoHome: () => void;
  onGoBoard: () => void;
  onGoModule: (mod: ModuleDef) => void;
  onOpenDiagnostics: () => void;
}) {
  return (
    <div style={{ borderRight: "1px solid #23262d", background: "#0f1114", display: "grid", gridTemplateRows: "1fr auto", minHeight: 0 }}>
      <div style={{ overflowY: "auto", padding: "14px 10px", display: "flex", flexDirection: "column", gap: 18 }}>
        <div style={{ display: "flex", flexDirection: "column", gap: 3 }}>
          <SidebarLabel>GOAT</SidebarLabel>
          <button type="button" onClick={onGoHome} style={css(navStyle(screen === "home"))}>
            <span style={navDotStyle(screen === "home")} />
            Genel
          </button>
          <button type="button" onClick={onGoBoard} style={css(navStyle(screen === "board"))}>
            <span style={navDotStyle(screen === "board")} />
            Board
          </button>
        </div>

        <div style={{ display: "flex", flexDirection: "column", gap: 3 }}>
          <SidebarLabel>MODÜLLER</SidebarLabel>
          {MODULES.map((mod) => (
            <button key={mod.key} type="button" onClick={() => onGoModule(mod)} style={css(navStyle(screen === "module" && activeModuleKey === mod.key))}>
              <span style={{ width: 5, height: 5, borderRadius: "50%", background: "#4ea87a", flexShrink: 0 }} />
              {mod.name}
            </button>
          ))}
        </div>
      </div>

      <div style={{ borderTop: "1px solid #23262d", padding: 10 }}>
        <HoverButton
          base="display:flex;align-items:center;gap:9px;background:none;border:none;border-radius:6px;padding:7px 9px;cursor:pointer;font:450 12px/1 ui-sans-serif,system-ui;color:#8b919c;text-align:left;width:100%"
          hover="background:#16181d;color:#e6e8ec"
          onClick={onOpenDiagnostics}
        >
          <span style={{ width: 5, height: 5, borderRadius: "50%", background: "#4ea87a", flexShrink: 0 }} />
          Daemon &amp; bağımlılıklar
        </HoverButton>
      </div>
    </div>
  );
}

function SidebarLabel({ children }: { children: ReactNode }) {
  return (
    <span style={{ font: "500 9.5px/1 ui-monospace,Menlo,monospace", letterSpacing: ".12em", color: "#4d525b", padding: "0 8px 6px" }}>
      {children}
    </span>
  );
}

// ---------------------------------------------------------------------------
// home screen
// ---------------------------------------------------------------------------

function HomeScreen({ onGoModule }: { onGoModule: (mod: ModuleDef) => void }) {
  return (
    <div style={{ height: "100%", overflowY: "auto", padding: "26px 30px 30px", boxSizing: "border-box" }}>
      <div style={{ maxWidth: 1080, display: "flex", flexDirection: "column", gap: 26 }}>
        <div style={{ display: "flex", flexDirection: "column", gap: 7, minWidth: 0 }}>
          <h1 style={{ margin: 0, font: "600 20px/1.25 ui-sans-serif,system-ui", letterSpacing: "-.01em", color: "#e6e8ec" }}>
            Tek model, altında modüller
          </h1>
          <p style={{ margin: 0, maxWidth: 560, font: "400 12.5px/1.7 ui-sans-serif,system-ui", color: "#8b919c", textWrap: "pretty" }}>
            GOAT yerel motoru yönetir: klasör kapsamlı kod görevleri ve Maps lead-gen hattı aynı goat-daemon
            üzerinden çalışır.
          </p>
        </div>

        <div style={{ display: "flex", flexDirection: "column", gap: 11 }}>
          <span style={{ font: "500 10px/1 ui-monospace,Menlo,monospace", letterSpacing: ".1em", color: "#8b919c" }}>MODÜLLER</span>
          <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fill,minmax(248px,1fr))", gap: 11 }}>
            {MODULES.map((mod) => (
              <HoverDiv
                key={mod.key}
                base="background:#16181d;border:1px solid #23262d;border-radius:8px;padding:13px 14px;cursor:pointer;display:flex;flex-direction:column;gap:9px"
                hover="border-color:#33373f"
                onClick={() => onGoModule(mod)}
              >
                <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
                  <span style={{ width: 6, height: 6, borderRadius: "50%", background: "#4ea87a" }} />
                  <span style={{ font: "600 12.5px/1 ui-sans-serif,system-ui", color: "#e6e8ec" }}>{mod.name}</span>
                  <span style={{ flex: 1 }} />
                  <span style={{ font: "400 9.5px/1 ui-monospace,Menlo,monospace", color: "#4ea87a" }}>bağlı</span>
                </div>
                <p style={{ margin: 0, font: "400 11.5px/1.6 ui-sans-serif,system-ui", color: "#8b919c", textWrap: "pretty" }}>{mod.desc}</p>
                <span style={{ font: "400 10px/1.5 ui-monospace,Menlo,monospace", color: "#4d525b" }}>{mod.route.split(" · ")[0]}</span>
              </HoverDiv>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// board — real coding-task runs across every registered project, grouped by
// their real status (internal/store.RunStatus*). Nothing here is sample
// data: it is GET /coding-tasks?project_id=… called once per project and
// merged client-side, because the daemon has no cross-project list route.
// ---------------------------------------------------------------------------

type BoardRun = Run & { projectName: string };

function isSetTime(iso: string | undefined): iso is string {
  // time.Time's zero value still marshals to "0001-01-01T00:00:00Z" — Go's
  // encoding/json does not treat a zero struct as "empty" — so a run that
  // has not ended yet still carries an ended_at string.
  return !!iso && !iso.startsWith("0001-01-01");
}

function firstLine(text: string): string {
  const line = text.split("\n")[0]?.trim() ?? "";
  return line.length > 140 ? line.slice(0, 140) + "…" : line;
}

function formatRelativeTime(iso: string): string {
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return iso;
  const minutes = Math.round((Date.now() - then) / 60000);
  if (minutes < 1) return "az önce";
  if (minutes < 60) return `${minutes} dk önce`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours} sa önce`;
  return `${Math.round(hours / 24)} gün önce`;
}

function useBoardRuns() {
  const [runs, setRuns] = useState<BoardRun[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      const { projects } = await api.listProjects();
      const perProject = await Promise.all(
        projects.map(async (project) => {
          const { runs: projectRuns } = await api.listCodingTasks(project.id);
          return projectRuns.map((run) => ({ ...run, projectName: project.display_name }));
        }),
      );
      setRuns(perProject.flat().sort((a, b) => b.started_at.localeCompare(a.started_at)));
      setError(null);
    } catch (err) {
      setError(err instanceof DaemonError ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
    // Runs transition running → completed/failed while the board sits open;
    // this is the only place in the app that polls, and only while mounted.
    const id = window.setInterval(() => void refresh(), 4000);
    return () => window.clearInterval(id);
  }, [refresh]);

  return { runs, error, loading, refresh };
}

const STATUS_COLUMNS: { status: string; title: string; color: string }[] = [
  { status: "running", title: "Çalışıyor", color: "#d59b3d" },
  { status: "completed", title: "Tamamlandı", color: "#4ea87a" },
  { status: "failed", title: "Başarısız", color: "#d1615d" },
];

function BoardScreen({ onGoModule }: { onGoModule: (mod: ModuleDef) => void }) {
  const { runs, error, loading, refresh } = useBoardRuns();
  const [selected, setSelected] = useState<BoardRun | null>(null);

  return (
    <div style={{ height: "100%", overflowY: "auto", padding: "26px 30px 30px", boxSizing: "border-box" }}>
      <div style={{ display: "flex", flexDirection: "column", gap: 18 }}>
        <div style={{ display: "flex", alignItems: "baseline", gap: 12, flexWrap: "wrap" }}>
          <h1 style={{ margin: 0, font: "600 20px/1.25 ui-sans-serif,system-ui", letterSpacing: "-.01em", color: "#e6e8ec" }}>Board</h1>
          <span style={{ font: "400 12px/1.6 ui-sans-serif,system-ui", color: "#8b919c" }}>
            Kayıtlı her projenin coding-task run'ları
          </span>
          <span style={{ flex: 1 }} />
          <HoverButton
            base="background:none;border:1px solid #23262d;color:#8b919c;border-radius:6px;padding:6px 11px;font:500 11px/1 ui-monospace,Menlo,monospace;cursor:pointer"
            hover="border-color:#33373f;color:#e6e8ec"
            onClick={() => void refresh()}
          >
            {loading ? "yenileniyor…" : "yenile"}
          </HoverButton>
        </div>

        {error && <span style={{ font: "400 12px/1.6 ui-sans-serif,system-ui", color: "#d1615d" }}>{error}</span>}

        {runs && runs.length === 0 && !error && (
          <div style={{ border: "1px dashed #2b2f37", borderRadius: 9, padding: 22, display: "flex", flexDirection: "column", gap: 10, alignItems: "flex-start" }}>
            <span style={{ font: "600 13px/1.3 ui-sans-serif,system-ui", color: "#e6e8ec" }}>Henüz run yok</span>
            <p style={{ margin: 0, font: "400 12px/1.7 ui-sans-serif,system-ui", color: "#8b919c", textWrap: "pretty" }}>
              Board, kayıtlı projelerdeki coding-task run'larını gösterir. Bir görev başlat, burada belirsin.
            </p>
            <HoverButton
              base="background:#d8b44a;border:none;color:#0b0c0e;border-radius:6px;padding:8px 13px;font:600 11.5px/1 ui-sans-serif,system-ui;cursor:pointer"
              onClick={() => onGoModule(MODULES[0])}
            >
              Coding runner'a git
            </HoverButton>
          </div>
        )}

        {runs && runs.length > 0 && (
          <div style={{ display: "flex", gap: 14, alignItems: "flex-start", overflowX: "auto" }}>
            {STATUS_COLUMNS.map((col) => {
              const items = runs.filter((r) => r.status === col.status);
              return (
                <div key={col.status} style={{ width: 300, flexShrink: 0, display: "flex", flexDirection: "column", gap: 8 }}>
                  <div style={{ display: "flex", alignItems: "center", gap: 8, padding: "0 2px" }}>
                    <span style={{ font: "600 10.5px/1 ui-sans-serif,system-ui", letterSpacing: ".1em", textTransform: "uppercase", color: col.color }}>
                      {col.title}
                    </span>
                    <span style={{ font: "400 10.5px/1 ui-monospace,Menlo,monospace", color: "#575c66" }}>{items.length}</span>
                  </div>
                  {items.map((run) => (
                    <RunCard key={run.id} run={run} onOpen={() => setSelected(run)} />
                  ))}
                </div>
              );
            })}
          </div>
        )}
      </div>

      {selected && <RunDetailOverlay run={selected} onClose={() => setSelected(null)} />}
    </div>
  );
}

function RunCard({ run, onOpen }: { run: BoardRun; onOpen: () => void }) {
  const dotColor = run.status === "running" ? "#d59b3d" : run.status === "failed" ? "#d1615d" : "#4ea87a";
  return (
    <HoverDiv
      base="background:#16181d;border:1px solid #23262d;border-radius:7px;padding:10px 11px;cursor:pointer;display:flex;flex-direction:column;gap:8px"
      hover="border-color:#33373f;background:#181b21"
      onClick={onOpen}
    >
      <div style={{ display: "flex", gap: 8, alignItems: "flex-start" }}>
        <span style={{ width: 7, height: 7, borderRadius: "50%", background: dotColor, flexShrink: 0, marginTop: 4 }} />
        <span style={{ font: "450 12.5px/1.45 ui-sans-serif,system-ui", color: "#e6e8ec", textWrap: "pretty" }}>{firstLine(run.prompt)}</span>
      </div>
      <div style={{ display: "flex", gap: 8, alignItems: "center", font: "400 10px/1 ui-monospace,Menlo,monospace", color: "#575c66", flexWrap: "wrap" }}>
        <span>{run.projectName}</span>
        {run.cost_usd ? (
          <>
            <span>·</span>
            <span>${run.cost_usd.toFixed(4)}</span>
          </>
        ) : null}
        {run.num_turns ? (
          <>
            <span>·</span>
            <span>{run.num_turns} turn</span>
          </>
        ) : null}
        <span>·</span>
        <span>{formatRelativeTime(run.started_at)}</span>
      </div>
    </HoverDiv>
  );
}

function RunDetailOverlay({ run, onClose }: { run: BoardRun; onClose: () => void }) {
  const statusColor = run.status === "running" ? "#d59b3d" : run.status === "failed" ? "#d1615d" : "#4ea87a";
  return (
    <div
      onClick={onClose}
      style={{
        position: "fixed",
        inset: 0,
        background: "rgba(6,7,8,.66)",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        padding: 26,
        boxSizing: "border-box",
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          width: 640,
          maxWidth: "100%",
          background: "#131519",
          border: "1px solid #2b2f37",
          borderRadius: 12,
          boxShadow: "0 22px 60px rgba(0,0,0,.6)",
          overflow: "hidden",
          display: "flex",
          flexDirection: "column",
        }}
      >
        <div style={{ display: "flex", alignItems: "flex-start", gap: 14, padding: "14px 16px", borderBottom: "1px solid #23262d" }}>
          <span style={{ width: 8, height: 8, borderRadius: "50%", background: statusColor, flexShrink: 0, marginTop: 6 }} />
          <div style={{ minWidth: 0, display: "flex", flexDirection: "column", gap: 5, flex: 1 }}>
            <span style={{ font: "600 13px/1.4 ui-sans-serif,system-ui", color: "#e6e8ec" }}>{run.projectName}</span>
            <span style={{ font: "400 10.5px/1 ui-monospace,Menlo,monospace", color: "#575c66" }}>
              {run.id} · {run.status}
              {run.model ? ` · ${run.model}` : ""}
            </span>
          </div>
          <HoverButton
            base="background:none;border:1px solid #2b2f37;color:#8b919c;border-radius:6px;width:26px;height:26px;cursor:pointer;font:400 13px/1 ui-sans-serif,system-ui"
            onClick={onClose}
          >
            ✕
          </HoverButton>
        </div>
        <div style={{ padding: 16, display: "flex", flexDirection: "column", gap: 14, overflowY: "auto", maxHeight: "60vh" }}>
          <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
            <span style={{ font: "500 10.5px/1 ui-monospace,Menlo,monospace", color: "#8b919c", letterSpacing: ".06em" }}>PROMPT</span>
            <pre style={{ margin: 0, font: "400 12px/1.7 ui-monospace,Menlo,monospace", color: "#c3c8d1", whiteSpace: "pre-wrap" }}>{run.prompt}</pre>
          </div>
          {run.error && (
            <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
              <span style={{ font: "500 10.5px/1 ui-monospace,Menlo,monospace", color: "#d1615d", letterSpacing: ".06em" }}>HATA</span>
              <pre style={{ margin: 0, font: "400 12px/1.7 ui-monospace,Menlo,monospace", color: "#d1615d", whiteSpace: "pre-wrap" }}>{run.error}</pre>
            </div>
          )}
          <div
            style={{
              display: "flex",
              flexDirection: "column",
              gap: 7,
              font: "400 11px/1.7 ui-monospace,Menlo,monospace",
              color: "#575c66",
              borderTop: "1px solid #23262d",
              paddingTop: 14,
            }}
          >
            {run.session_id && <span>session {run.session_id}</span>}
            <span>başladı {new Date(run.started_at).toLocaleString("tr-TR")}</span>
            {isSetTime(run.ended_at) && <span>bitti {new Date(run.ended_at).toLocaleString("tr-TR")}</span>}
            {run.cost_usd ? (
              <span>
                maliyet ${run.cost_usd.toFixed(4)}
                {run.num_turns ? ` · ${run.num_turns} turn` : ""}
              </span>
            ) : null}
          </div>
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// module screen — chrome around the real Workspace / Leadgen screens
// ---------------------------------------------------------------------------

function ModuleScreen({ mod, onGoHome }: { mod: ModuleDef; onGoHome: () => void }) {
  return (
    <div style={{ height: "100%", display: "grid", gridTemplateRows: "auto 1fr", minHeight: 0 }}>
      <div style={{ padding: "14px 20px 12px", display: "flex", flexDirection: "column", gap: 6, borderBottom: "1px solid #23262d" }}>
        <HoverButton
          base="align-self:flex-start;background:none;border:none;padding:0;cursor:pointer;font:400 10.5px/1 ui-monospace,Menlo,monospace;color:#575c66"
          hover="color:#d8b44a"
          onClick={onGoHome}
        >
          ← genel
        </HoverButton>
        <div style={{ display: "flex", alignItems: "baseline", gap: 11, flexWrap: "wrap" }}>
          <h1 style={{ margin: 0, font: "600 17px/1.2 ui-sans-serif,system-ui", letterSpacing: "-.01em", color: "#e6e8ec" }}>{mod.name}</h1>
          <span style={{ font: "400 10.5px/1 ui-monospace,Menlo,monospace", color: "#575c66" }}>
            {mod.route} · {mod.tools}
          </span>
        </div>
      </div>
      <div style={{ minHeight: 0, overflow: "hidden" }}>
        {mod.key === "coding" && <Workspace />}
        {mod.key === "leadgen" && <Leadgen />}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// diagnostics overlay — the daemon's own /diagnostics answer, verbatim
// ---------------------------------------------------------------------------

const DEP_NAMES = ["crawl4ai", "claude", "duckduckgo", "maps_scraper"] as const;

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
    <div onClick={onClose} style={{ position: "absolute", inset: 0, background: "#0b0c0e", display: "grid", placeItems: "center", padding: 28 }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: 520, maxWidth: "100%", display: "flex", flexDirection: "column", gap: 14 }}>
        <div style={{ display: "flex", flexDirection: "column", gap: 5 }}>
          <span style={{ font: "600 13px/1 ui-sans-serif,system-ui", letterSpacing: ".14em" }}>GOAT</span>
          <span style={{ font: "400 11px/1.5 ui-monospace,Menlo,monospace", color: "#8b919c" }}>
            Kabuk portu seçer, token üretir, goat-daemon'ı sidecar olarak başlatır.
          </span>
        </div>

        <div style={{ border: "1px solid #23262d", borderRadius: 9, background: "#131519", overflow: "hidden" }}>
          <div style={{ display: "flex", alignItems: "flex-start", justifyContent: "space-between", gap: 14, padding: "12px 14px", borderBottom: "1px solid #23262d" }}>
            <div style={{ display: "flex", flexDirection: "column", gap: 3 }}>
              <span style={{ font: "600 12.5px/1 ui-sans-serif,system-ui" }}>Daemon</span>
              <span style={{ font: "400 10.5px/1 ui-monospace,Menlo,monospace", color: "#8b919c" }}>{baseUrl ?? "…"}</span>
            </div>
            <span style={{ border: "1px solid rgba(78,168,122,.4)", borderRadius: 999, padding: "3px 9px", font: "500 10.5px/1 ui-monospace,Menlo,monospace", color: "#4ea87a" }}>
              ready
            </span>
          </div>
          <div style={{ padding: "12px 14px", display: "flex", flexDirection: "column", gap: 11 }}>
            <span style={{ font: "400 12px/1.6 ui-sans-serif,system-ui", color: "#8b919c", textWrap: "pretty" }}>
              Bağlandı. Token kabukta ve bu istemcide kalır — URL'e hiç girmez.
            </span>
            <HoverButton
              base="align-self:flex-start;background:#d8b44a;border:none;color:#0b0c0e;border-radius:6px;padding:8px 13px;font:600 11.5px/1 ui-sans-serif,system-ui;cursor:pointer"
              onClick={onClose}
            >
              Kapat
            </HoverButton>
          </div>
        </div>

        <div style={{ border: "1px solid #23262d", borderRadius: 9, background: "#131519", overflow: "hidden" }}>
          <div style={{ padding: "11px 14px", borderBottom: "1px solid #23262d", display: "flex", flexDirection: "column", gap: 3 }}>
            <span style={{ font: "600 12.5px/1 ui-sans-serif,system-ui" }}>Bağımlılıklar</span>
            <span style={{ font: "400 10.5px/1 ui-monospace,Menlo,monospace", color: "#8b919c" }}>daemon'ın kendi diagnostics çıktısı</span>
          </div>
          <div style={{ padding: "11px 14px", display: "flex", flexDirection: "column", gap: 9 }}>
            {error && <span style={{ font: "400 12px/1.6 ui-sans-serif,system-ui", color: "#d1615d" }}>{error}</span>}
            {!error && !diagnostics && (
              <span style={{ font: "400 12px/1.6 ui-sans-serif,system-ui", color: "#575c66" }}>yükleniyor…</span>
            )}
            {diagnostics &&
              DEP_NAMES.map((name) => {
                const dep = diagnostics.dependencies?.[name] as DiagnosticsDependency | undefined;
                if (!dep) return null;
                return (
                  <div key={name} style={{ display: "flex", alignItems: "flex-start", justifyContent: "space-between", gap: 14 }}>
                    <div style={{ minWidth: 0 }}>
                      <div style={{ font: "450 12px/1.4 ui-sans-serif,system-ui" }}>{name}</div>
                      {dep.detail && <div style={{ font: "400 10.5px/1 ui-monospace,Menlo,monospace", color: "#575c66" }}>{dep.detail}</div>}
                    </div>
                    <span
                      style={{
                        border: `1px solid ${dep.ok ? "rgba(78,168,122,.4)" : "#2b2f37"}`,
                        borderRadius: 999,
                        padding: "3px 8px",
                        font: "500 10px/1 ui-monospace,Menlo,monospace",
                        color: dep.ok ? "#4ea87a" : "#8b919c",
                      }}
                    >
                      {dep.ok ? "ok" : dep.optional ? "optional, down" : "down"}
                    </span>
                  </div>
                );
              })}
            {diagnostics && (
              <span style={{ font: "400 10.5px/1.5 ui-monospace,Menlo,monospace", color: "#4d525b", paddingTop: 2 }}>
                store {diagnostics.daemon.store} · {diagnostics.daemon.projects} proje · v{diagnostics.daemon.version}
              </span>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
