import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { open } from "@tauri-apps/plugin-dialog";

import { Badge } from "../components/ui/badge";
import { Button, IconButton } from "../components/ui/button";
import { Masthead } from "../components/ui/masthead";
import { MenuItem, MenuList } from "../components/ui/menu";
import { Picker } from "../components/ui/picker";
import { Popover } from "../components/ui/popover";
import { Meter } from "../components/ui/meter";
import { Tabs } from "../components/ui/tabs";
import { Card, CardBody, CardHeader } from "../components/ui/card";
import { ProviderModelPicker } from "../components/ModelPicker";
import { GraphConsole } from "../components/GraphConsole";
import { cn } from "../lib/cn";
import {
  api,
  DaemonError,
  type BrainGraph,
  type BrainNodeDetail,
  type BrainNodeVersion,
  type BrainProject,
  type BrainScanPolicy,
  type BrainScanStatus,
  type BrainStructural,
  type LLMProviderList,
} from "../lib/daemon";
import {
  addPath,
  describePolicy,
  isDirty,
  removePath,
  shortPath,
  type ScanPolicyDraft,
} from "../lib/scanPolicy";
import {
  alpha,
  byDegree,
  CARBON,
  ELECTRIC,
  fitView,
  hitTest,
  initialView,
  kindLegend,
  kindStyle,
  MIST,
  mix,
  pollInterval,
  screenToWorld,
  startLayout,
  versionLines,
  visibleLabels,
  worldSize,
  zoomAt,
  type LayoutNode,
  type View,
} from "../lib/brainGraph";

/**
 * The Brain tab: what the resident scan is doing, and the picture of what it
 * has learned.
 *
 * Two halves that refresh on different clocks, deliberately. The status is
 * polled — the daemon has no event stream for a scan and does not need one,
 * because a scan has one current state rather than a sequence of things that
 * must not be missed. The graph is fetched once and refetched when the status
 * says a sweep finished (`sweeps` changes), so the picture is never redrawn
 * under the operator's cursor for no reason.
 */
/**
 * The canvas's own colours.
 *
 * A 2D context resolves neither a custom property nor `color-mix`, so these
 * have to be strings — but they are built from the same four `brainGraph.ts`
 * builds the node fills from, rather than typed out again beside them.
 *
 * The three edge weights are the whole legibility budget of this picture. At
 * rest an edge is barely there, because eleven thousand of them at any real
 * opacity is a grey wash with no graph in it; touching the hovered node it
 * comes up enough to trace; touching the selected one it goes Electric.
 */
const LABEL = mix(MIST, CARBON, 0.55);
const EDGE_REST = alpha(LABEL, 0.16);
const EDGE_HOVER = alpha(MIST, 0.34);
const EDGE_SELECTED = alpha(ELECTRIC, 0.8);

export function Brain() {
  const { status, error, refresh } = useBrainScan();
  const scanPolicy = useScanPolicy();
  const structural = useStructural();
  // Moving or forgetting a project changes every id in it, so the selection
  // and the project filter are dropped rather than left pointing at rows that
  // no longer exist.
  const keep = useProjectKeeper(() => {
    void api.brainProjects().then((res) => setProjects(res.projects ?? []));
    setProject("");
    setSelectedID(null);
    setSelected(null);
  });
  const [projects, setProjects] = useState<BrainProject[]>([]);
  const [project, setProject] = useState<string>("");
  const [graph, setGraph] = useState<BrainGraph | null>(null);
  const [graphError, setGraphError] = useState<string | null>(null);
  const [selectedID, setSelectedID] = useState<string | null>(null);
  const [selected, setSelected] = useState<BrainNodeDetail | null>(null);
  const [versions, setVersions] = useState<BrainNodeVersion[]>([]);
  const [busy, setBusy] = useState(false);
  const [full, setFull] = useState(false);
  const [tab, setTab] = useState<Tab>("graph");
  // Off by default, and it has to be: the structural layer is thousands of
  // symbols and they are the most connected things in the graph, so a picture
  // ranked by degree turns into nothing but symbols the moment it is on.
  const [withSymbols, setWithSymbols] = useState(false);

  // The picker's vocabulary and the operator's pick for the next sweep. Empty
  // is the opening state and means the daemon's own distil routing, which is
  // what the button did before this control existed.
  const [providers, setProviders] = useState<LLMProviderList | null>(null);
  const [provider, setProvider] = useState("");
  const [model, setModel] = useState("");

  useEffect(() => {
    let live = true;
    api
      .llmProviders()
      .then((res) => live && setProviders(res))
      // A daemon that will not list its providers still runs the scan on its
      // configured routing, so the picker simply does not appear.
      .catch(() => live && setProviders(null));
    return () => {
      live = false;
    };
  }, []);

  // Escape leaves full screen, the way it closes the diagnostics overlay. A
  // view with no chrome needs a way out that does not depend on finding a
  // button.
  useEffect(() => {
    if (!full) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setFull(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [full]);

  const sweeps = status?.sweeps ?? 0;

  useEffect(() => {
    let live = true;
    api
      .brainProjects()
      .then((res) => live && setProjects(res.projects))
      .catch(() => live && setProjects([]));
    return () => {
      live = false;
    };
  }, [sweeps]);

  useEffect(() => {
    let live = true;
    setGraphError(null);
    api
      .brainGraph({
        project: project || undefined,
        kinds: withSymbols ? "all" : undefined,
      })
      .then((res) => live && setGraph(res))
      .catch((e: unknown) => live && setGraphError(messageOf(e)));
    return () => {
      live = false;
    };
  }, [project, sweeps, withSymbols]);

  const act = useCallback(
    async (fn: () => Promise<unknown>) => {
      setBusy(true);
      try {
        await fn();
        await refresh();
      } catch {
        await refresh();
      } finally {
        setBusy(false);
      }
    },
    [refresh],
  );

  // Selecting is a toggle and the background clears it. A selection nothing can
  // undo is a mode the operator did not ask to be in — and the id is held
  // separately from the detail so the highlight lands on the click rather than
  // on the response.
  const pickNode = useCallback(
    (id: string | null) => {
      if (id === null || id === selectedID) {
        setSelectedID(null);
        setSelected(null);
        return;
      }
      setSelectedID(id);
      setSelected(null);
      setVersions([]);
      void api
        .brainNode(id)
        .then((res) => {
          setSelected(res.node);
          // History rides node detail, so there is no second request for the
          // common case of a file with one reading.
          setVersions(res.versions ?? []);
        })
        .catch(() => {
          setSelected(null);
          setVersions([]);
        });
    },
    [selectedID],
  );

  return (
    // The screen scrolls; it does not fit.
    //
    // This was a fixed-height grid — four rows, the last one `1fr`, the whole
    // thing `overflow-hidden` — which is the right shape for a screen whose
    // contents are known to fit and the wrong one for this screen, which
    // carries a scan panel, a ten-cell figure strip, a tab bar, a graph, a
    // legend, a search console and a node reading. Everything below the fold
    // was not shortened, it was cut: the console's answer list ended
    // mid-row, and the rail ran off the right edge of the window with the
    // question box and the neighbour list half outside it.
    //
    // Two separate faults produced that, and both are here. The height one is
    // this row: content at its natural height inside one scroller, the way
    // `Home` already does it. The width one is the column definition below.
    <div className="h-full min-h-0 overflow-y-auto">
      <div className="flex min-w-0 flex-col gap-6 p-6">
        <Masthead
          title="Brain"
          count={
            graph
              ? `${graph.nodes.length.toLocaleString("tr")} düğüm · ${graph.edges.length.toLocaleString("tr")} bağ`
              : "okunuyor…"
          }
        />

        <ScanPanel
          status={status}
          error={error}
          busy={busy}
          providers={providers}
          provider={provider}
          model={model}
          onProvider={(id) => {
            setProvider(id);
            // The model list is a property of the provider, so a stale id from
            // the previous one would be a pair the daemon rejects. Opening on the
            // provider's own default is the choice the picker already labels.
            const next = providers?.providers.find((p) => p.id === id);
            setModel(next?.default_model ?? "");
          }}
          onModel={setModel}
          onPause={() => act(api.pauseBrainScan)}
          onResume={() => act(api.resumeBrainScan)}
          onNow={() => act(() => api.scanBrainNow(provider ? { provider, model } : undefined))}
        />

        <ScreenTabs tab={tab} onTab={setTab} />

        {/* The picture is the tab this screen opens on, and it gets everything
          left over. The permissions and the parser switch are things an
          operator sets once and then reads back rarely — before this they sat
          permanently above the graph and left it a strip. */}
        {tab === "graph" ? (
          // `minmax(0,1fr)` and not `1fr`. A `1fr` track takes the larger of the
          // share and its content's *min-content* width, and this card's header
          // alone — a project picker plus two buttons — is some seven hundred
          // pixels of that. The track refused to shrink, so the rail beside it
          // was pushed past the right edge of the window and clipped. `min-w-0`
          // on the rail is the same fix one level down: without it the neighbour
          // list's `truncate` never engaged and the column sized itself to the
          // longest node title it happened to be holding.
          //
          // `items-start` because the two columns are not two halves of one
          // thing. The graph is a fixed viewport onto a picture; the rail is a
          // stack of readings that are as long as they are.
          //
          // And one column below `xl`. Four hundred pixels of graph is not a
          // smaller graph, it is a card whose own toolbar does not fit inside
          // it — at a 1024 window the "Tam ekran" button was cut off by the
          // card holding it. The rail goes underneath instead, which is the
          // same content in the order it is read.
          <div className="grid grid-cols-1 items-start gap-6 xl:grid-cols-[minmax(0,1fr)_20rem]">
            <GraphCard
              graph={graph}
              graphError={graphError}
              projects={projects}
              project={project}
              onProject={(id) => {
                setProject(id);
                pickNode(null);
              }}
              selected={selectedID}
              onPick={pickNode}
              withSymbols={withSymbols}
              onSymbols={setWithSymbols}
              full={false}
              onToggleFull={() => setFull(true)}
            />

            <div className="flex min-w-0 flex-col gap-6">
              <Card>
                <CardHeader title="Türler" />
                <CardBody className="flex flex-wrap gap-2">
                  {kindLegend().map(({ kind, style }) => (
                    <span key={kind} className="flex items-center gap-1.5 text-xs text-muted">
                      <span
                        className="inline-block h-2 w-2 rounded-full"
                        style={{ background: style.fill }}
                      />
                      {style.label}
                    </span>
                  ))}
                </CardBody>
              </Card>
              <GraphConsole
                projectPath={project}
                projectLabel={projects.find((p) => p.id === project)?.label ?? "Tüm makine"}
                selectedID={selectedID}
                selectedTitle={selected?.title ?? ""}
                onPick={pickNode}
              />
              <NodePanel node={selected} versions={versions} onOpen={pickNode} />
            </div>
          </div>
        ) : (
          <div className="flex flex-col gap-6">
            <ScanPolicyCard
              policy={scanPolicy.policy}
              draft={scanPolicy.draft}
              busy={scanPolicy.busy}
              error={scanPolicy.error}
              onAddRoot={() => void scanPolicy.addRoot()}
              onAddExclude={(kind) => void scanPolicy.addExclude(kind)}
              onRemoveRoot={scanPolicy.removeRoot}
              onRemoveExclude={scanPolicy.removeExclude}
              onSave={() => void scanPolicy.save().then(refresh)}
              onReset={() => void scanPolicy.reset().then(refresh)}
              onRevert={scanPolicy.revert}
            />

            <StructuralCard
              state={structural.state}
              busy={structural.busy}
              error={structural.error}
              onToggle={() => void structural.toggle()}
            />

            <ProjectsCard
              projects={projects}
              busy={keep.busy}
              error={keep.error}
              onMove={(p) => void keep.move(p)}
              onForget={(p) => void keep.forget(p)}
            />
          </div>
        )}
      </div>

      {/* Full screen is an overlay rather than the browser's fullscreen API:
          this window is an app shell, and the picture should cover it without
          the OS animating a new space in and taking the title bar with it.
          This one *is* a fixed-height layout, correctly: it owns the viewport,
          so the picture fills it and the reading beside it scrolls inside. */}
      {full && (
        <div className="fixed inset-0 z-50 grid min-h-0 grid-cols-[minmax(0,1fr)_20rem] gap-4 bg-ground p-4">
          <GraphCard
            graph={graph}
            graphError={graphError}
            projects={projects}
            project={project}
            onProject={(id) => {
              setProject(id);
              pickNode(null);
            }}
            selected={selectedID}
            onPick={pickNode}
            withSymbols={withSymbols}
            onSymbols={setWithSymbols}
            full
            onToggleFull={() => setFull(false)}
          />
          <NodePanel
            node={selected}
            versions={versions}
            onOpen={pickNode}
            className="min-h-0 min-w-0 overflow-auto"
          />
        </div>
      )}
    </div>
  );
}

/**
 * The two halves of this screen.
 *
 * They were one column, and the picture lost: fifteen hundred nodes were drawn
 * in whatever was left under a permissions panel and a parser switch, which is
 * a strip rather than a graph. The graph opens first because it is the thing
 * somebody comes to this tab to look at; the rest is configuration, which is
 * read when it is being changed and not otherwise.
 *
 * The scan panel stays above both, because it is neither: it is what the daemon
 * is doing right now, and pausing it is something an operator wants to be able
 * to do while looking at the picture it is filling in.
 */
type Tab = "graph" | "config";

function ScreenTabs({ tab, onTab }: { tab: Tab; onTab: (t: Tab) => void }) {
  return (
    <Tabs
      id="brain"
      active={tab}
      onSelect={onTab}
      tabs={[
        { key: "graph", label: "Bilgi grafiği" },
        { key: "config", label: "Yapılandırma" },
      ]}
    />
  );
}

function GraphCard({
  graph,
  graphError,
  projects,
  project,
  onProject,
  selected,
  onPick,
  withSymbols,
  onSymbols,
  full,
  onToggleFull,
}: {
  graph: BrainGraph | null;
  graphError: string | null;
  projects: BrainProject[];
  project: string;
  onProject: (id: string) => void;
  selected: string | null;
  onPick: (id: string | null) => void;
  withSymbols: boolean;
  onSymbols: (on: boolean) => void;
  full: boolean;
  onToggleFull: () => void;
}) {
  return (
    // A definite height, because the page scrolls now and "whatever is left
    // over" is no longer a number. The picture is the object of this screen,
    // so it takes most of the fold — with a floor, so a short window gets a
    // small graph rather than a sliver, and a ceiling, so a tall one does not
    // hand it half a mile of empty canvas. Full screen keeps filling the
    // viewport it was given.
    <Card
      className={cn(
        "grid grid-rows-[auto_minmax(0,1fr)] overflow-hidden",
        full ? "h-full min-h-0" : "h-[clamp(26rem,62vh,44rem)]",
      )}
    >
      {/* A toolbar, not a card header. The header said "Bilgi grafiği" twenty
          pixels under a tab that says "Bilgi grafiği", and under that a count
          the masthead had already given at the top of the screen — so the only
          new word in it was "en bağlantılı olanlar", and that sentence was
          being squeezed into two lines by the controls beside it. What is left
          is the one reading that is not written anywhere else, and the room
          the controls actually need. */}
      <div className="flex flex-wrap items-center justify-between gap-3 px-5 pt-4 pb-3">
        <p className="min-w-0 truncate text-sm text-muted">
          {graph
            ? graph.truncated
              ? `en bağlantılı ${graph.nodes.length.toLocaleString("tr")} düğüm`
              : `${graph.nodes.length.toLocaleString("tr")} düğüm · ${graph.edges.length.toLocaleString("tr")} bağ`
            : "yükleniyor…"}
        </p>
        <div className="flex flex-wrap items-center gap-2">
          {/* The node count belongs beside each project, not glued into
                its name in brackets — "(1284)" inside a `<select>` option
                reads as part of the repo. */}
          <div className="w-52">
            <Picker
              label="Proje"
              choices={[
                {
                  value: ALL_PROJECTS,
                  label: "Tüm makine",
                  icon: "grid" as const,
                },
                ...projects.map((p) => ({
                  value: p.id,
                  label: p.label,
                  detail: `${p.nodes} düğüm`,
                  icon: "folder" as const,
                })),
              ]}
              value={project || ALL_PROJECTS}
              onChange={(id) => onProject(id === ALL_PROJECTS ? "" : id)}
            />
          </div>
          {/* Off by default and said out loud when on: the structural layer
                is an order of magnitude more nodes than everything else, and
                turning it on replaces the picture rather than adding to it. */}
          {/* A toggle has to look held down. `active` is the primitive's own
                answer to that now — this used to be a hand-written recipe here
                and one other place, and everything else in the app said
                nothing at all. */}
          <Button
            size="sm"
            variant="ghost"
            active={withSymbols}
            icon={withSymbols ? "check" : undefined}
            onClick={() => onSymbols(!withSymbols)}
            title="Graphify'ın çıkardığı fonksiyon ve tipler"
          >
            Semboller
          </Button>
          <Button
            size="sm"
            variant="ghost"
            active={full}
            activeAria="expanded"
            icon={full ? "close" : "external"}
            onClick={onToggleFull}
          >
            {full ? "Kapat" : "Tam ekran"}
          </Button>
        </div>
      </div>
      <CardBody className="min-h-0 p-0">
        {graphError ? (
          <p className="p-5 font-mono text-xs text-bad">{graphError}</p>
        ) : (
          <GraphCanvas graph={graph} selected={selected} onPick={onPick} />
        )}
      </CardBody>
    </Card>
  );
}

// --- the scan ----------------------------------------------------------------

function ScanPanel({
  status,
  error,
  busy,
  providers,
  provider,
  model,
  onProvider,
  onModel,
  onPause,
  onResume,
  onNow,
}: {
  status: BrainScanStatus | null;
  error: string | null;
  busy: boolean;
  providers: LLMProviderList | null;
  provider: string;
  model: string;
  onProvider: (v: string) => void;
  onModel: (v: string) => void;
  onPause: () => void;
  onResume: () => void;
  onNow: () => void;
}) {
  if (error) {
    return (
      <Card>
        <CardBody className="text-xs text-bad">{error}</CardBody>
      </Card>
    );
  }
  if (!status) {
    return (
      <Card>
        <CardBody className="text-sm text-muted">tarama durumu okunuyor…</CardBody>
      </Card>
    );
  }

  return (
    <Card elevation="raised">
      <CardHeader
        title="Sürekli tarama"
        subtitle={`${status.provider} · ${status.model} · ${
          status.roots?.length ? status.roots.join(" · ") : "klasör yok"
        }`}
        aside={<PhaseBadge status={status} />}
      />
      {/* The routing controls have a row of their own, and they need one. In
          the card header they shared `aside` with a badge and two buttons
          behind a `shrink-0`, so the row could not wrap and could not shrink:
          it simply ran off the right edge of the card, and the model control —
          last in, and only drawn once a provider was picked — was the part
          that went over the edge. A control you cannot reach is the same thing
          as a control that does not work, which is what it was reported as. */}
      <div className="flex flex-wrap items-end justify-between gap-4 border-t border-edge px-5 pt-4 pb-4">
        <ProviderModelPicker
          providers={providers}
          provider={provider}
          model={model}
          onProvider={onProvider}
          onModel={onModel}
        />
        <div className="flex items-center gap-2">
          {status.paused ? (
            <Button size="sm" icon="play" onClick={onResume} disabled={busy}>
              Sürdür
            </Button>
          ) : (
            <Button size="sm" variant="ghost" icon="stop" onClick={onPause} disabled={busy}>
              Duraklat
            </Button>
          )}
          <Button
            size="sm"
            variant="ghost"
            icon="refresh"
            onClick={onNow}
            disabled={busy || status.paused}
          >
            Şimdi tara
          </Button>
        </div>
      </div>
      {/* The same strip the dashboard uses for the day's figures, for the
          same reason: these are one reading of one scan taken at one moment,
          and a wrapping row of `flex-col` pairs put the labels and the values
          on lines that did not line up with each other. Cells with hairlines
          between them, values on a shared baseline.
          
          Five to a row rather than as many as fit. At `minmax(96px,1fr)` these
          ten squeezed onto one line by cutting their own labels —
          "değişme…", "okunama…", "tavana ta…" — a strip that fits by no longer
          saying anything. Auto-fit solves the words and leaves the arithmetic
          to the window, which lands ten cells as nine and an orphan; a counted
          grid gives two even rows and drops to three and two on a small one.
          
          The clip is what makes wrapping look right: every cell draws its own
          left hairline, and the row is shifted one pixel out of a hidden
          overflow so the leading one of each row falls outside. `first:` alone
          only reaches the first cell of the first row. */}
      <div className="overflow-hidden border-t border-edge">
        <div className="-ml-px grid w-[calc(100%+1px)] grid-cols-2 sm:grid-cols-3 lg:grid-cols-5">
          <Stat label="düğüm" value={status.nodes_total} />
          <Stat label="bu turda" value={status.scanned_session} />
          <Stat label="kalan" value={status.remaining} />
          <Stat label="değişmemiş" value={status.skipped_unchanged} />
          <Stat label="okunamayan" value={status.unreadable_session} />
          <Stat label="sembol" value={status.symbols_session ?? 0} />
          {(status.symbols_dropped ?? 0) > 0 && (
            <Stat label="tavana takılan" value={status.symbols_dropped ?? 0} />
          )}
          <Stat label="tur" value={status.sweeps} />
          <Stat
            label="proje"
            value={
              status.project_count ? `${status.project_index}/${status.project_count}` : "—"
            }
          />
          <Stat label="şu an" value={status.project_label || "—"} />
        </div>
      </div>
      {/* The daemon's own words, not a paraphrase: a message an operator can
          act on is the whole reason it is carried this far. */}
      {status.last_error && (
        <p className="border-t border-edge px-5 py-3 font-mono text-xs leading-[1.5] text-bad">
          {status.last_error}
        </p>
      )}
    </Card>
  );
}

function PhaseBadge({ status }: { status: BrainScanStatus }) {
  if (status.paused) return <Badge tone="muted">duraklatıldı</Badge>;
  if (status.provider_down) return <Badge tone="bad">sağlayıcı yanıt vermiyor</Badge>;
  if (status.phase === "scanning" || status.phase === "discovering") {
    return <Badge tone="accent">çalışıyor</Badge>;
  }
  return <Badge tone="ok">bekliyor</Badge>;
}

function Stat({ label, value }: { label: string; value: number | string }) {
  return (
    <div className="flex min-w-0 flex-col gap-2 border-l border-edge px-4 py-3.5">
      <span className="label truncate text-muted/70" title={label}>
        {label}
      </span>
      {/* `.figure`, not mono: these are quantities and Open Sans has real
          tabular figures, so a column of them lines up without the console
          face's width. A value the daemon has not sent yet is an em dash
          rather than an empty cell — a blank under a label reads as a
          rendering fault. */}
      <span className="figure truncate text-lg text-text">
        {value === undefined || value === null || value === "" ? "—" : value}
      </span>
    </div>
  );
}

// --- the picture -------------------------------------------------------------

/** Held at module scope so "nothing laid out yet" keeps the same identity
 * across frames, and the effects that depend on it do not re-run while the
 * simulation is still working. */
const NOTHING_PLACED: LayoutNode[] = [];

/** How long the layout may hold the main thread before yielding. Under a
 * frame, so a window doing nothing else still repaints. */
const FRAME_BUDGET_MS = 12;

/**
 * The settled picture, computed off the render path.
 *
 * This used to be a `useMemo`, which meant the whole simulation ran inside a
 * render: on a real knowledge base — fifteen hundred nodes and six thousand
 * edges — that measured about twelve seconds of blocked main thread every time
 * the tab was opened. Long enough that the daemon stopped hearing from the
 * app, and long enough for macOS to decide the WebView behind the window was
 * idle, suspend it, and sometimes not bring it back: a blank window, from a
 * picture that was only slow.
 *
 * So the simulation runs a frame's worth at a time and yields. The nodes are
 * published once, at the end, rather than at every step: an unsettled graph is
 * a cloud of dots that tells the reader nothing, and drawing six thousand
 * lines per frame to show it move would spend the budget this exists to save.
 */
function useSettledLayout(graph: BrainGraph | null): {
  placed: LayoutNode[];
  progress: number;
} {
  const [placed, setPlaced] = useState<LayoutNode[]>(NOTHING_PLACED);
  const [progress, setProgress] = useState(1);

  useEffect(() => {
    setPlaced(NOTHING_PLACED);

    if (!graph || graph.nodes.length === 0) {
      setProgress(1);
      return;
    }

    setProgress(0);
    const world = worldSize(graph.nodes.length);
    const run = startLayout(graph.nodes, graph.edges, { ...world, seed: 11 });

    let live = true;
    let frame = requestAnimationFrame(function pump() {
      if (!live) return;
      if (run.advance(FRAME_BUDGET_MS)) {
        setProgress(run.progress);
        frame = requestAnimationFrame(pump);
        return;
      }
      setProgress(1);
      setPlaced(run.nodes);
    });

    return () => {
      live = false;
      cancelAnimationFrame(frame);
    };
  }, [graph]);

  return { placed, progress };
}

function GraphCanvas({
  graph,
  selected,
  onPick,
}: {
  graph: BrainGraph | null;
  selected: string | null;
  onPick: (id: string | null) => void;
}) {
  const wrap = useRef<HTMLDivElement | null>(null);
  const canvas = useRef<HTMLCanvasElement | null>(null);
  const [size, setSize] = useState({ w: 0, h: 0 });
  const [hover, setHover] = useState<{
    node: LayoutNode;
    x: number;
    y: number;
  } | null>(null);
  const hoverID = hover?.node.id ?? null;
  const [view, setView] = useState<View>({ scale: 1, x: 0, y: 0 });

  // A drag that moves the picture must not also open the node it started on,
  // so the pointer's travel is remembered and a click past a few pixels is a
  // pan rather than a selection.
  const drag = useRef<{ x: number; y: number; travel: number } | null>(null);
  // Panning is state as well as a ref, because the cursor is rendered.
  const [panning, setPanning] = useState(false);

  useEffect(() => {
    const el = wrap.current;
    if (!el) return;
    const ro = new ResizeObserver(([entry]) => {
      setSize({
        w: Math.floor(entry.contentRect.width),
        h: Math.floor(entry.contentRect.height),
      });
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // The layout lives in a world much larger than this box — see worldSize. It
  // is computed from the graph alone, so resizing the window or opening full
  // screen re-frames the same picture instead of computing a different one.
  const { placed, progress } = useSettledLayout(graph);

  // The order the labels are chosen in changes when the picture does, not when
  // the operator pans — see byDegree.
  const labelOrder = useMemo(() => byDegree(placed), [placed]);

  const fit = useCallback(() => {
    if (placed.length === 0 || size.w === 0) return;
    setView(fitView(placed, size.w, size.h));
  }, [placed, size.w, size.h]);

  // The opening view is readable rather than complete — see READABLE_SCALE.
  // It is re-taken when the picture or the box changes, which is what makes
  // entering full screen re-frame the same graph at a sensible zoom.
  useEffect(() => {
    if (placed.length === 0 || size.w === 0) return;
    setView(initialView(placed, size.w, size.h));
  }, [placed, size.w, size.h]);

  // One draw per frame, whatever happens in between. Panning updates the view
  // on every pointer event, and a redraw per event meant six thousand lines
  // and fifteen hundred circles several times inside a single frame — work the
  // compositor then threw away.
  useEffect(() => {
    const el = canvas.current;
    if (!el || !graph) return;

    const draw = () => {
      const dpr = window.devicePixelRatio || 1;
      el.width = Math.max(1, Math.floor(size.w * dpr));
      el.height = Math.max(1, Math.floor(size.h * dpr));
      const ctx = el.getContext("2d");
      if (!ctx) return;

      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      ctx.clearRect(0, 0, size.w, size.h);

      const at = new Map(placed.map((n) => [n.id, n]));

      // Everything structural is drawn in world coordinates under one transform;
      // widths are divided by the scale so a hairline stays a hairline at every
      // zoom. Text is drawn afterwards, in screen space, so it never shrinks.
      ctx.save();
      ctx.translate(view.x, view.y);
      ctx.scale(view.scale, view.scale);

      // Two things can be lit at once and they mean different things: the node
      // you picked, which stays, and the node under the pointer, which is a
      // question you are asking. Selection wins where they disagree.
      const lit = hoverID ?? selected;

      ctx.lineWidth = 1 / view.scale;
      for (const e of graph.edges) {
        const a = at.get(e.source);
        const b = at.get(e.target);
        if (!a || !b) continue;
        const onSelected = selected && (e.source === selected || e.target === selected);
        const onHover = hoverID && (e.source === hoverID || e.target === hoverID);
        ctx.strokeStyle = onSelected ? EDGE_SELECTED : onHover ? EDGE_HOVER : EDGE_REST;
        ctx.beginPath();
        ctx.moveTo(a.x, a.y);
        ctx.lineTo(b.x, b.y);
        ctx.stroke();
      }

      for (const n of placed) {
        ctx.beginPath();
        ctx.arc(n.x, n.y, n.r, 0, Math.PI * 2);
        ctx.fillStyle = kindStyle(n.kind).fill;
        // Everything that is not the answer steps back, so the neighbourhood
        // reads as a shape rather than as a brighter part of an even field.
        ctx.globalAlpha = lit && n.id !== lit ? 0.5 : 0.92;
        ctx.fill();

        // The halo. Drawn in the node's own colour rather than in Mist,
        // because at this size the ring is most of what you see of a small
        // node and a white ring would recolour it.
        if (n.id === hoverID && n.id !== selected) {
          ctx.globalAlpha = 0.5;
          ctx.strokeStyle = kindStyle(n.kind).fill;
          ctx.lineWidth = 4 / view.scale;
          ctx.stroke();
          ctx.lineWidth = 1 / view.scale;
        }

        if (n.id === selected) {
          ctx.globalAlpha = 1;
          ctx.strokeStyle = MIST;
          ctx.lineWidth = 2 / view.scale;
          ctx.stroke();
          ctx.lineWidth = 1 / view.scale;
        }
      }
      ctx.globalAlpha = 1;
      ctx.restore();

      ctx.font = "10px ui-monospace, Menlo, monospace";
      ctx.fillStyle = LABEL;
      for (const label of visibleLabels(labelOrder, view, size.w, size.h)) {
        ctx.fillText(trim(label.node.title, 24), label.x, label.y);
      }
    };

    const frame = requestAnimationFrame(draw);
    return () => cancelAnimationFrame(frame);
    // `hoverID` and not `hover`: the tooltip needs the pointer's position and
    // moves with every pixel, but the picture only changes when the node under
    // it does. Redrawing three thousand nodes per mousemove is the difference
    // between a graph you can explore and a window that stutters.
  }, [graph, placed, labelOrder, selected, hoverID, size.w, size.h, view]);

  const pointAt = (e: React.MouseEvent<HTMLCanvasElement>) => {
    const rect = e.currentTarget.getBoundingClientRect();
    return { sx: e.clientX - rect.left, sy: e.clientY - rect.top };
  };

  // A two-finger scroll on a trackpad is a plain wheel event and a pinch is a
  // wheel event with ctrlKey. Treating both as zoom is what made this feel
  // wrong: on macOS a two-finger scroll means *move the page*, and a map that
  // zooms instead fights the hand. So scroll pans, pinch (and ctrl/⌘ + wheel)
  // zooms — the same split every map on this platform uses.
  //
  // It is a native listener with `{ passive: false }` rather than React's
  // `onWheel`, and that is the whole fix for the second half of the bug. React
  // registers wheel on its root container as **passive**, so `preventDefault`
  // inside a JSX handler is ignored: the graph panned *and* the page scrolled
  // under it at the same time, one gesture moving two things. A canvas that
  // consumes a scroll has to say so to the browser, and only a non-passive
  // listener can say it.
  useEffect(() => {
    const el = canvas.current;
    if (!el) return;

    const onWheel = (e: WheelEvent) => {
      // Claimed before it is handled: whatever this gesture means to the
      // graph, it does not also mean "scroll the page".
      e.preventDefault();
      const rect = el.getBoundingClientRect();
      if (e.ctrlKey || e.metaKey) {
        const factor = Math.exp(-e.deltaY * 0.004);
        setView((v) => zoomAt(v, factor, e.clientX - rect.left, e.clientY - rect.top));
        return;
      }
      setView((v) => ({ ...v, x: v.x - e.deltaX, y: v.y - e.deltaY }));
    };

    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  }, []);

  return (
    <div
      ref={wrap}
      className="relative h-full w-full overflow-hidden overscroll-contain"
    >
      <canvas
        ref={canvas}
        style={{
          width: size.w,
          height: size.h,
          cursor: panning ? "grabbing" : "grab",
          // The gesture is the canvas's. Without this the browser may still
          // hand a trackpad scroll to an ancestor before the listener runs.
          touchAction: "none",
          overscrollBehavior: "contain",
        }}
        onMouseDown={(e) => {
          const { sx, sy } = pointAt(e);
          drag.current = { x: sx, y: sy, travel: 0 };
          setPanning(true);
        }}
        onMouseUp={(e) => {
          const d = drag.current;
          drag.current = null;
          setPanning(false);
          if (!d || d.travel > 4) return;
          const { sx, sy } = pointAt(e);
          const w = screenToWorld(view, sx, sy);
          // A click on nothing is a click on nothing: it clears the selection
          // rather than leaving the operator stuck with one.
          onPick(hitTest(placed, w.x, w.y)?.id ?? null);
        }}
        onMouseMove={(e) => {
          const { sx, sy } = pointAt(e);
          const d = drag.current;
          if (d) {
            const dx = sx - d.x;
            const dy = sy - d.y;
            d.travel += Math.abs(dx) + Math.abs(dy);
            d.x = sx;
            d.y = sy;
            setView((v) => ({ ...v, x: v.x + dx, y: v.y + dy }));
            setHover(null);
            return;
          }
          const w = screenToWorld(view, sx, sy);
          const node = hitTest(placed, w.x, w.y);
          setHover(node ? { node, x: sx, y: sy } : null);
        }}
        onMouseLeave={() => {
          drag.current = null;
          setPanning(false);
          setHover(null);
        }}
      />

      {/* On a surface, not on the picture. These controls were drawn straight
          onto the canvas, so they overlapped whatever the layout had put in
          the top-left corner — in a graph this dense that is node labels, and
          the result was two texts on top of each other, neither readable. The
          hint moved to the opposite corner for the same reason: it is a long
          sentence, and a long sentence lying across a picture is the loudest
          thing on the screen. */}
      <div className="absolute top-3 left-3 flex items-center gap-1 rounded-md bg-overlay/90 p-1 shadow-elev-2 outline outline-edge-strong/70 backdrop-blur-sm">
        <ViewButton
          label="−"
          title="uzaklaş"
          onClick={() => setView((v) => zoomAt(v, 1 / 1.25, size.w / 2, size.h / 2))}
        />
        <ViewButton
          label="+"
          title="yakınlaş"
          onClick={() => setView((v) => zoomAt(v, 1.25, size.w / 2, size.h / 2))}
        />
        <ViewButton label="Sığdır" onClick={fit} />
        <span className="figure px-1.5 text-xs text-muted">
          %{Math.round(view.scale * 100)}
        </span>
      </div>

      <span className="pointer-events-none absolute right-3 bottom-3 rounded-md bg-overlay/80 px-2 py-1 text-xs text-muted/80 backdrop-blur-sm">
        sürükle · kaydır · ⌘+tekerlek yakınlaştırır
      </span>

      {hover && (
        <div
          className="pointer-events-none absolute rounded-md bg-overlay px-2.5 py-1.5 text-xs text-text shadow-elev-2 outline outline-edge-strong/70"
          style={{
            left: Math.min(hover.x + 12, Math.max(size.w - 240, 8)),
            top: Math.max(hover.y - 10, 4),
          }}
        >
          <span className="font-mono">{trim(hover.node.title, 40)}</span>
          <span className="ml-2 text-muted">
            {kindStyle(hover.node.kind).label} · {hover.node.degree}
          </span>
        </div>
      )}

      {graph && graph.nodes.length === 0 && (
        <p className="absolute inset-0 flex items-center justify-center text-xs text-muted">
          henüz düğüm yok — tarama ilerledikçe burası dolacak
        </p>
      )}

      {/* Said out loud, because the alternative is an empty box: a graph this
          size takes a few seconds to settle, and the operator should be able
          to tell that from a picture that has nothing in it. */}
      {graph && graph.nodes.length > 0 && placed.length === 0 && (
        <div className="absolute inset-0 flex flex-col items-center justify-center gap-2.5">
          <p className="text-sm text-muted">yerleşim hesaplanıyor</p>
          {/* A real fraction, so it is a bar and not a sentence with a number
              in it: the simulation reports its own progress, which is exactly
              the case a meter is for. */}
          <Meter value={progress} tone="lime" label="yerleşim ilerlemesi" className="w-40" />
        </div>
      )}
    </div>
  );
}

function ViewButton({
  label,
  title,
  onClick,
}: {
  label: string;
  title?: string;
  onClick: () => void;
}) {
  return (
    <Button
      size="sm"
      // No chrome and no fill of its own: the toolbar around these is the
      // surface now, and a button that tints itself again inside it reads as
      // a darker hole rather than as a control.
      variant="quiet"
      title={title}
      onClick={onClick}
    >
      {label}
    </Button>
  );
}

// --- one node ----------------------------------------------------------------

function NodePanel({
  node,
  versions,
  onOpen,
  className,
}: {
  node: BrainNodeDetail | null;
  versions: BrainNodeVersion[];
  onOpen: (id: string | null) => void;
  /** Scrolling belongs to whoever gave this panel a height — the full-screen
   * overlay does, the rail on a scrolling page does not. */
  className?: string;
}) {
  if (!node) {
    return (
      <Card className={className}>
        <CardHeader title="Düğüm" />
        <CardBody className="text-sm text-muted">Grafikten bir düğüm seçin.</CardBody>
      </Card>
    );
  }

  return (
    <Card className={className}>
      {/* No character count. `trim(title, 40)` cut the name to a number that
          knows nothing about how wide the rail is — it clipped titles that
          fitted and left ones that did not, and `CardHeader` truncates in CSS
          anyway. The width decides now, which is the only thing that can. */}
      <CardHeader title={node.title} subtitle={kindStyle(node.kind).label} />
      {/* `wrap-anywhere` on the body rather than on each paragraph. Everything
          in here is prose a model wrote about a file, and it routinely carries
          a thing no line-breaking rule will break on its own: a session URL, a
          hash, an import path. Without it the longest one of those decides the
          width of the panel, the panel decides the width of the page, and a
          fixed-inset layout that owns the viewport grows a horizontal
          scrollbar. Reading sideways is not reading. */}
      <CardBody className="flex min-w-0 flex-col gap-4 wrap-anywhere">
        <p className="text-sm leading-relaxed text-text">{node.assessment}</p>
        <p className="font-mono text-xs break-all text-muted">{node.source}</p>
        {node.tags && node.tags.length > 0 && (
          <div className="flex flex-wrap gap-1.5">
            {node.tags.map((t) => (
              <span key={t} className="rounded-sm bg-raised px-2 py-1 text-xs text-muted">
                {t}
              </span>
            ))}
          </div>
        )}
        <VersionTimeline versions={versions} />
        {node.neighbors && node.neighbors.length > 0 && (
          <div className="flex min-w-0 flex-col gap-1 border-t border-edge pt-3">
            <span className="label pb-1 text-muted">komşular</span>
            {/* Capped and scrolled inside the card. A hub in this graph has
                hundreds of neighbours, and on a page that scrolls an uncapped
                list does not overflow — it pushes everything under it a
                screen and a half down, which is the same reading problem
                wearing different clothes. */}
            <div className="flex max-h-64 min-w-0 flex-col overflow-y-auto">
              {node.neighbors.map((n) => (
                <button
                  key={n.id}
                  onClick={() => onOpen(n.id)}
                  className="focus-ring flex min-w-0 items-baseline gap-1.5 rounded px-1 py-1 text-left transition-colors hover:bg-raised"
                >
                  <span className="min-w-0 flex-1 truncate text-sm text-text">{n.title}</span>
                  <span className="shrink-0 font-mono text-xs text-muted">{n.relation}</span>
                </button>
              ))}
            </div>
          </div>
        )}
      </CardBody>
    </Card>
  );
}

/**
 * What this file used to mean.
 *
 * Brain re-reads a file whose bytes changed and writes a new assessment over
 * the old one; the store keeps the old readings, and this is where they are
 * legible. Only shown when there is more than one — a file read once has a
 * history of exactly what is already on screen above.
 *
 * There is no diff and no file content, because none is stored: the file is on
 * disk, and what git does not keep is the reading.
 */
function VersionTimeline({ versions }: { versions: BrainNodeVersion[] }) {
  const [open, setOpen] = useState<string | null>(null);
  const lines = useMemo(() => versionLines(versions), [versions]);

  if (lines.length < 2) return null;

  return (
    <div className="flex flex-col gap-1 border-t border-edge pt-2">
      <span className="label text-muted">geçmiş · {lines.length} sürüm</span>
      {lines.map((v) => {
        const expanded = open === v.hash;
        return (
          <div key={v.hash}>
            <button
              onClick={() => setOpen(expanded ? null : v.hash)}
              className="flex w-full items-baseline justify-between gap-2 text-left text-xs transition-colors hover:text-lime"
            >
              <span className={v.current ? "text-text" : "text-muted"}>
                {v.when}
                {v.current && <span className="ml-1.5 text-xs text-muted">şimdiki</span>}
              </span>
              <span className="shrink-0 font-mono text-xs text-muted">
                {v.size && <span className="mr-1.5">{v.size}</span>}
                {v.shortHash}
              </span>
            </button>
            {expanded && v.assessment && (
              <p className="mt-1 border-l-2 border-edge pl-2 text-xs leading-relaxed text-muted wrap-anywhere">
                {v.assessment}
              </p>
            )}
          </div>
        );
      })}
    </div>
  );
}

// --- polling -----------------------------------------------------------------

/**
 * useBrainScan polls the scan's status, fast while it is working and slowly
 * while it is not. It is this screen's own loop rather than the shared runs
 * poll: nothing else in the app reads it, and RunsProvider exists for the board.
 */
function useBrainScan() {
  const [status, setStatus] = useState<BrainScanStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  // The interval is chosen from the *current* status, which is not the one the
  // effect closed over. Reading the state variable there meant reading `null`
  // for the life of the tab — `pollInterval(null)` is one second — so the tab
  // asked the daemon every second no matter what the scan was doing, which is
  // the opposite of what this hook is for. One session logged 41,652 of them.
  const latest = useRef<BrainScanStatus | null>(null);

  const refresh = useCallback(async () => {
    try {
      const res = await api.brainScan();
      latest.current = res.scan;
      setStatus(res.scan);
      setError(null);
    } catch (e: unknown) {
      setError(messageOf(e));
    }
  }, []);

  useEffect(() => {
    let live = true;
    let timer: ReturnType<typeof setTimeout>;

    const tick = async () => {
      await refresh();
      if (!live) return;
      timer = setTimeout(tick, pollInterval(latest.current));
    };
    void tick();

    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [refresh]);

  return { status, error, refresh };
}

function messageOf(e: unknown): string {
  if (e instanceof DaemonError) return e.message;
  return String(e);
}

function trim(s: string, n: number): string {
  return s.length > n ? s.slice(0, n - 1) + "…" : s;
}

/**
 * What the scan is allowed to read.
 *
 * Two lists rather than one, because they are two different permissions: a root
 * is consent being given — "read this folder" — and an exclusion is consent
 * being taken back inside it. Showing them together, on the tab where the sweep
 * is running, is the point: the folders being read and the evidence of the
 * reading belong on one screen or they drift apart in the operator's head.
 *
 * Edits are local until "kaydet". A list that saved on every click would make
 * removing three folders three sweeps' worth of churn, and would leave no
 * moment at which the operator is looking at what they are about to commit.
 */
function ScanPolicyCard({
  policy,
  draft,
  busy,
  error,
  onAddRoot,
  onAddExclude,
  onRemoveRoot,
  onRemoveExclude,
  onSave,
  onReset,
  onRevert,
}: {
  policy: BrainScanPolicy | null;
  draft: ScanPolicyDraft;
  busy: boolean;
  error: string | null;
  onAddRoot: () => void;
  onAddExclude: (kind: "file" | "directory") => void;
  onRemoveRoot: (path: string) => void;
  onRemoveExclude: (path: string) => void;
  onSave: () => void;
  onReset: () => void;
  onRevert: () => void;
}) {
  if (!policy) {
    return (
      <Card>
        <CardBody className="text-xs text-muted">tarama izinleri okunuyor…</CardBody>
      </Card>
    );
  }

  const dirty = isDirty(draft, {
    roots: policy.roots ?? [],
    excludes: policy.excludes ?? [],
  });

  return (
    <Card>
      <CardHeader
        title="Tarama izinleri"
        subtitle={describePolicy(draft)}
        aside={
          <div className="flex items-center gap-2">
            {/* Said out loud, because "these are the folders Mimir picked" and
                "these are the folders you chose" are different sentences and
                only one of them invites a look. */}
            {!policy.configured && <Badge tone="muted">varsayılan</Badge>}
            {dirty && <Badge tone="accent">kaydedilmedi</Badge>}
            {dirty && (
              <Button variant="ghost" onClick={onRevert} disabled={busy}>
                Geri al
              </Button>
            )}
            <Button onClick={onSave} disabled={busy || !dirty}>
              Kaydet
            </Button>
          </div>
        }
      />

      <CardBody className="grid grid-cols-2 gap-6">
        <PathList
          title="Taranan klasörler"
          empty="Hiçbir klasör taranmıyor. Tarama duruyor — bu bir ayar, arıza değil."
          paths={draft.roots}
          busy={busy}
          onRemove={onRemoveRoot}
          action={
            <Button variant="ghost" onClick={onAddRoot} disabled={busy}>
              Klasör ekle
            </Button>
          }
        />

        <PathList
          title="Hariç tutulanlar"
          empty="Hariç tutulan yok. Kimlik dosyaları (.env, *.pem, id_rsa …) zaten hiç okunmuyor."
          paths={draft.excludes}
          busy={busy}
          onRemove={onRemoveExclude}
          action={
            <div className="flex gap-2">
              <Button variant="ghost" onClick={() => onAddExclude("directory")} disabled={busy}>
                Klasör
              </Button>
              <Button variant="ghost" onClick={() => onAddExclude("file")} disabled={busy}>
                Dosya
              </Button>
            </div>
          }
        />
      </CardBody>

      <CardBody className="flex items-center justify-between border-t border-edge">
        <span className="text-xs text-muted">
          Hariç tutulan bir yol bir daha okunmaz. Bilgi grafiğinde ondan gelmiş düğümler varsa
          yerinde kalır — silmek ayrı bir iş.
        </span>
        {policy.configured && (
          <Button variant="ghost" onClick={onReset} disabled={busy}>
            Varsayılana dön
          </Button>
        )}
      </CardBody>

      {error && (
        // internal/project's guards explain themselves ("home dizini", "bir
        // dizin değil"), so the daemon's own sentence is shown as written.
        <CardBody className="border-t border-edge text-xs text-bad">{error}</CardBody>
      )}
    </Card>
  );
}

function PathList({
  title,
  empty,
  paths,
  busy,
  action,
  onRemove,
}: {
  title: string;
  empty: string;
  paths: string[];
  busy: boolean;
  action: React.ReactNode;
  onRemove: (path: string) => void;
}) {
  return (
    <div className="min-w-0">
      <div className="mb-2 flex items-center justify-between gap-2">
        <span className="label text-muted">{title}</span>
        {action}
      </div>
      {paths.length === 0 ? (
        <p className="text-xs text-muted">{empty}</p>
      ) : (
        <ul className="flex flex-col gap-1">
          {paths.map((path) => (
            <li
              key={path}
              className="flex items-center justify-between gap-2 rounded-md bg-raised px-2.5 py-1.5"
            >
              {/* The full path in the tooltip: two folders called `src` are
                  indistinguishable by their last segment, and the short form is
                  what makes the list readable at all. */}
              <span className="truncate font-mono text-xs" title={path}>
                {shortPath(path)}
              </span>
              {/* A glyph, not the word: the row is a path and the word for
                  removing it repeated down the list is louder than the paths.
                  A hand-rolled `<button>` here was also the last control on
                  this screen that drew its own chrome. */}
              <IconButton
                name="close"
                size="sm"
                label={`${shortPath(path)} listeden çıkar`}
                onClick={() => onRemove(path)}
                disabled={busy}
                className="shrink-0 hover:text-bad"
              />
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/**
 * The structural layer's one control.
 *
 * It says two things that look like one and are not: whether the operator wants
 * the parser, and whether this machine has it. A single line saying "off" would
 * leave somebody who has never heard of Graphify with nothing to do about it,
 * so the machine's answer carries the command that changes it — and the command
 * comes from the daemon rather than from here, because the package name is that
 * side's fact and two copies of it would drift.
 */
function StructuralCard({
  state,
  busy,
  error,
  onToggle,
}: {
  state: BrainStructural | null;
  busy: boolean;
  error: string | null;
  onToggle: () => void;
}) {
  if (!state) return null;

  return (
    <Card>
      <CardHeader
        title="Yapısal katman · Graphify"
        subtitle={
          !state.enabled
            ? "Kapalı. Grafik yalnızca dosyalardan kuruluyor."
            : state.installed
              ? `Kurulu${state.version ? ` · ${state.version}` : ""}${state.interpreter ? ` · ${state.interpreter}` : ""} — fonksiyon ve çağrı kenarları modelsiz ekleniyor.`
              : "Kurulu değil. Bulunduğunda semboller ve çağrı kenarları kendiliğinden eklenir; şimdiye kadarki grafik değişmez."
        }
        aside={
          <div className="flex items-center gap-2">
            {state.enabled && (
              <Badge tone={state.installed ? "ok" : "muted"}>
                {state.installed ? "kurulu" : "bulunamadı"}
              </Badge>
            )}
            <Button variant="ghost" onClick={onToggle} disabled={busy}>
              {state.enabled ? "Kapat" : "Aç"}
            </Button>
          </div>
        }
      />
      {state.enabled && !state.installed && (
        <CardBody className="flex flex-col gap-2 border-t border-edge">
          <div className="flex items-baseline gap-3">
            {/* Selectable, because the whole point of it is being copied. */}
            <code className="rounded-md bg-sunken px-2.5 py-1.5 font-mono text-xs break-all text-text select-text">
              {state.install}
            </code>
          </div>
          <span className="text-xs leading-relaxed text-muted">
            Apache-2.0, ücretsiz, model çağrısı yok. Kendi sanal ortamına kuruluyor: bu
            makinedeki <code className="font-mono">pip</code> ya PATH'te yok ya da sistem
            Python'ına yazmayı reddeder, ve Mimir bu klasörü kendiliğinden bulur. Kurulduğunda
            bir sonraki tur onu görür.
          </span>
          {state.looked && state.looked.length > 0 && (
            <div className="flex flex-col gap-0.5 border-t border-edge pt-2">
              <span className="label text-muted">bakılan yerler</span>
              {state.looked.map((path) => (
                <span key={path} className="font-mono text-xs break-all text-muted/80">
                  {path}
                </span>
              ))}
            </div>
          )}
        </CardBody>
      )}
      {error && <CardBody className="border-t border-edge text-xs text-bad">{error}</CardBody>}
    </Card>
  );
}

/**
 * The projects the knowledge base holds, and the two things that can be done
 * to one.
 *
 * This exists because a renamed repository had no way out. Its old path stayed
 * in the graph as a separate project, was re-recorded from transcripts every
 * five minutes, and nothing in the system could delete a node. The two buttons
 * are the whole repair: move it onto where the folder went, or forget it.
 *
 * "klasör yok" is the row worth looking at. A project whose folder is gone
 * cannot be scanned and will never change again — it is either a rename nobody
 * told the graph about, or history nobody wants.
 */
function ProjectsCard({
  projects,
  busy,
  error,
  onMove,
  onForget,
}: {
  projects: BrainProject[];
  busy: string | null;
  error: string | null;
  onMove: (p: BrainProject) => void;
  onForget: (p: BrainProject) => void;
}) {
  const missing = projects.filter((p) => p.on_disk === false).length;

  return (
    <Card>
      <CardHeader
        title="Projeler"
        subtitle={
          missing > 0
            ? `${projects.length} proje · ${missing} tanesinin klasörü yok`
            : `${projects.length} proje`
        }
      />
      <CardBody className="flex flex-col gap-1">
        {projects.length === 0 && (
          <p className="text-xs text-muted">
            Henüz proje yok — tarama ilerledikçe burası dolacak.
          </p>
        )}
        {projects.map((p) => (
          <ProjectRow
            key={p.id}
            project={p}
            busy={busy}
            onMove={() => onMove(p)}
            onForget={() => onForget(p)}
          />
        ))}
      </CardBody>
      <CardBody className="border-t border-edge text-xs text-muted">
        Taşımak geçmişi korur: düğümler, oturumlar ve sohbetler yeni yolun altına geçer. Unutmak
        geri alınamaz — ama diskteki transcript dosyalarına dokunulmaz, onlar Claude Code'un.
      </CardBody>
      {error && <CardBody className="border-t border-edge text-xs text-bad">{error}</CardBody>}
    </Card>
  );
}

/**
 * One project, and the two things that can be done to it.
 *
 * Both behind a menu, because there are eighteen of these rows. Two buttons
 * each is thirty-six controls in a list nobody came here to act on, and when
 * the destructive one is outlined red — which it should be, on its own — the
 * card becomes eighteen red rectangles and the colour stops meaning anything.
 * A row gets one quiet glyph; the verbs are inside it, where "Unut" can be as
 * loud as it needs to be exactly once.
 */
function ProjectRow({
  project,
  busy,
  onMove,
  onForget,
}: {
  project: BrainProject;
  busy: string | null;
  onMove: () => void;
  onForget: () => void;
}) {
  const trigger = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);

  return (
    <div className="flex items-center justify-between gap-3 rounded-md bg-raised px-2.5 py-2">
      <div className="flex min-w-0 items-baseline gap-2">
        <span className="truncate text-sm text-text" title={project.path}>
          {project.label}
        </span>
        <span className="shrink-0 font-mono text-xs text-muted">{project.nodes} düğüm</span>
        {project.on_disk === false && <Badge tone="bad">klasör yok</Badge>}
      </div>
      <IconButton
        ref={trigger}
        name="more"
        label={`${project.label} için işlemler`}
        size="sm"
        aria-haspopup="menu"
        active={open}
        activeAria="expanded"
        loading={busy === project.id}
        disabled={busy !== null && busy !== project.id}
        onClick={() => setOpen((was) => !was)}
        className="shrink-0"
      />
      <Popover
        open={open}
        onClose={() => setOpen(false)}
        anchor={trigger}
        label={`${project.label} işlemleri`}
        align="end"
        width={220}
      >
        <MenuList label={`${project.label} işlemleri`}>
          <MenuItem
            icon="folder"
            title="Taşı"
            detail="geçmişi korur"
            onSelect={() => {
              setOpen(false);
              onMove();
            }}
          />
          <MenuItem
            icon="trash"
            title="Unut"
            detail="geri alınamaz"
            tone="danger"
            onSelect={() => {
              setOpen(false);
              onForget();
            }}
          />
        </MenuList>
      </Popover>
    </div>
  );
}

/**
 * Moving and forgetting, with the two confirmations they need.
 *
 * The folder picker for a move, because a path typed into a box is a path
 * spelled wrong; a plain confirm for forgetting, because it cannot be undone.
 * `onChanged` is what re-reads the list: every id in a moved project changes,
 * so the screen cannot keep showing the rows it had.
 */
function useProjectKeeper(onChanged: () => void) {
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const move = useCallback(
    async (p: BrainProject) => {
      setError(null);
      let picked: string | null = null;
      try {
        const chosen = await open({
          directory: true,
          multiple: false,
          title: `${p.label} nereye taşındı?`,
        });
        picked = typeof chosen === "string" ? chosen : null;
      } catch (err) {
        setError(messageOf(err));
        return;
      }
      if (!picked) return;

      setBusy(p.id);
      try {
        await api.moveBrainProject(p.id, picked);
        onChanged();
      } catch (err) {
        setError(messageOf(err));
      } finally {
        setBusy(null);
      }
    },
    [onChanged],
  );

  const forget = useCallback(
    async (p: BrainProject) => {
      setError(null);
      // The one destructive button in this app, so it asks. `confirm` blocks
      // the WebView, which is exactly what is wanted here and nowhere else.
      if (!window.confirm(`${p.label}: ${p.nodes} düğüm silinecek. Bu geri alınamaz.`)) {
        return;
      }
      setBusy(p.id);
      try {
        await api.forgetBrainProject(p.id);
        onChanged();
      } catch (err) {
        setError(messageOf(err));
      } finally {
        setBusy(null);
      }
    },
    [onChanged],
  );

  return { busy, error, move, forget };
}

/**
 * The structural setting, and the machine's answer to it.
 *
 * A 404 is a daemon built without the feature, which is not a fault worth a red
 * banner — the same judgement useScanPolicy makes about its own route, and the
 * reason the card renders nothing at all rather than an empty shell.
 */
function useStructural() {
  const [state, setState] = useState<BrainStructural | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let live = true;
    api
      .brainStructural()
      .then((res) => live && setState(res))
      .catch(() => live && setState(null));
    return () => {
      live = false;
    };
  }, []);

  const toggle = useCallback(async () => {
    if (!state) return;
    setError(null);
    setBusy(true);
    try {
      setState(
        await api.saveBrainStructural({
          enabled: !state.enabled,
          python: state.python,
        }),
      );
    } catch (err) {
      setError(messageOf(err));
    } finally {
      setBusy(false);
    }
  }, [state]);

  return { state, busy, error, toggle };
}

/**
 * The scan policy, and the edits that have not been sent yet.
 *
 * The draft is separate state rather than a mutation of what the daemon
 * returned, so "kaydedilmedi" can be true and "geri al" can mean something. It
 * is re-seeded whenever a save or a reset confirms a new policy — the daemon's
 * answer wins, because it has resolved the symlinks and dropped the duplicates
 * and the screen would otherwise show a path that is not the one being scanned.
 */
function useScanPolicy() {
  const [policy, setPolicy] = useState<BrainScanPolicy | null>(null);
  const [draft, setDraft] = useState<ScanPolicyDraft>({
    roots: [],
    excludes: [],
  });
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const adopt = useCallback((next: BrainScanPolicy) => {
    setPolicy(next);
    setDraft({ roots: next.roots ?? [], excludes: next.excludes ?? [] });
  }, []);

  const refresh = useCallback(async () => {
    try {
      adopt(await api.brainScanPolicy());
      setError(null);
    } catch (err) {
      // A daemon with no settings store answers 404 here. That is a build
      // without the feature, not a fault worth a red banner on the tab.
      if (err instanceof DaemonError && err.status === 404) return;
      setError(messageOf(err));
    }
  }, [adopt]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // Inside the try, both of them: a picker can reject, and a button that
  // silently does nothing is worse than one that says why.
  const pick = useCallback(
    async (directory: boolean, title: string): Promise<string | null> => {
      const picked = await open({ directory, multiple: false, title });
      return typeof picked === "string" ? picked : null;
    },
    [],
  );

  const addRoot = useCallback(async () => {
    setError(null);
    setBusy(true);
    try {
      const picked = await pick(true, "Taranacak klasör");
      if (picked) setDraft((d) => ({ ...d, roots: addPath(d.roots, picked) }));
    } catch (err) {
      setError(messageOf(err));
    } finally {
      setBusy(false);
    }
  }, [pick]);

  const addExclude = useCallback(
    async (kind: "file" | "directory") => {
      setError(null);
      setBusy(true);
      try {
        const picked = await pick(
          kind === "directory",
          kind === "directory" ? "Hariç tutulacak klasör" : "Hariç tutulacak dosya",
        );
        if (picked) setDraft((d) => ({ ...d, excludes: addPath(d.excludes, picked) }));
      } catch (err) {
        setError(messageOf(err));
      } finally {
        setBusy(false);
      }
    },
    [pick],
  );

  const removeRoot = useCallback((path: string) => {
    setDraft((d) => ({ ...d, roots: removePath(d.roots, path) }));
  }, []);

  const removeExclude = useCallback((path: string) => {
    setDraft((d) => ({ ...d, excludes: removePath(d.excludes, path) }));
  }, []);

  const revert = useCallback(() => {
    if (policy) setDraft({ roots: policy.roots ?? [], excludes: policy.excludes ?? [] });
    setError(null);
  }, [policy]);

  const save = useCallback(async () => {
    setError(null);
    setBusy(true);
    try {
      adopt(
        await api.saveBrainScanPolicy({
          roots: draft.roots,
          excludes: draft.excludes,
        }),
      );
    } catch (err) {
      setError(messageOf(err));
    } finally {
      setBusy(false);
    }
  }, [adopt, draft]);

  const reset = useCallback(async () => {
    setError(null);
    setBusy(true);
    try {
      adopt(await api.resetBrainScanPolicy());
    } catch (err) {
      setError(messageOf(err));
    } finally {
      setBusy(false);
    }
  }, [adopt]);

  return {
    policy,
    draft,
    busy,
    error,
    addRoot,
    addExclude,
    removeRoot,
    removeExclude,
    save,
    reset,
    revert,
  };
}

/** The sentinel for "no project filter", inside the picker only — an empty
 * value would draw as nothing selected, and "the whole machine" is a choice. */
const ALL_PROJECTS = "__all__";
