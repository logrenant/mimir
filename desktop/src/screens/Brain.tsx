import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Card, CardBody, CardHeader } from "../components/ui/card";
import { ProviderModelPicker } from "../components/ModelPicker";
import {
  api,
  DaemonError,
  type BrainGraph,
  type BrainNodeDetail,
  type BrainNodeVersion,
  type BrainProject,
  type BrainScanStatus,
  type LLMProviderList,
} from "../lib/daemon";
import {
  fitView,
  hitTest,
  initialView,
  kindLegend,
  kindStyle,
  layout,
  pollInterval,
  screenToWorld,
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
export function Brain() {
  const { status, error, refresh } = useBrainScan();
  const [projects, setProjects] = useState<BrainProject[]>([]);
  const [project, setProject] = useState<string>("");
  const [graph, setGraph] = useState<BrainGraph | null>(null);
  const [graphError, setGraphError] = useState<string | null>(null);
  const [selectedID, setSelectedID] = useState<string | null>(null);
  const [selected, setSelected] = useState<BrainNodeDetail | null>(null);
  const [versions, setVersions] = useState<BrainNodeVersion[]>([]);
  const [busy, setBusy] = useState(false);
  const [full, setFull] = useState(false);

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
      .brainGraph({ project: project || undefined })
      .then((res) => live && setGraph(res))
      .catch((e: unknown) => live && setGraphError(messageOf(e)));
    return () => {
      live = false;
    };
  }, [project, sweeps]);

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
    <div className="grid h-full grid-rows-[auto_1fr] gap-4 overflow-hidden p-6">
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

      <div className="grid min-h-0 grid-cols-[1fr_18rem] gap-4">
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
          full={false}
          onToggleFull={() => setFull(true)}
        />

        <div className="grid min-h-0 grid-rows-[auto_1fr] gap-4">
          <Card>
            <CardHeader title="Türler" />
            <CardBody className="flex flex-wrap gap-2">
              {kindLegend().map(({ kind, style }) => (
                <span key={kind} className="flex items-center gap-1.5 text-[11px] text-muted">
                  <span
                    className="inline-block h-2 w-2 rounded-full"
                    style={{ background: style.fill }}
                  />
                  {style.label}
                </span>
              ))}
            </CardBody>
          </Card>
          <NodePanel node={selected} versions={versions} onOpen={pickNode} />
        </div>
      </div>

      {/* Full screen is an overlay rather than the browser's fullscreen API:
          this window is an app shell, and the picture should cover it without
          the OS animating a new space in and taking the title bar with it. */}
      {full && (
        <div className="fixed inset-0 z-50 grid grid-cols-[1fr_18rem] gap-4 bg-ground p-4">
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
            full
            onToggleFull={() => setFull(false)}
          />
          <NodePanel node={selected} versions={versions} onOpen={pickNode} />
        </div>
      )}
    </div>
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
  full: boolean;
  onToggleFull: () => void;
}) {
  return (
    <Card className="grid min-h-0 grid-rows-[auto_1fr] overflow-hidden">
      <CardHeader
        title="Bilgi grafiği"
        subtitle={
          graph
            ? `${graph.nodes.length} düğüm · ${graph.edges.length} bağ${graph.truncated ? " · en bağlantılı olanlar" : ""}`
            : "yükleniyor…"
        }
        aside={
          <div className="flex items-center gap-2">
            <select
              value={project}
              onChange={(e) => onProject(e.target.value)}
              className="label rounded-sm border border-edge bg-raised px-2 py-1 text-text"
            >
              <option value="">tüm makine</option>
              {projects.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.label} ({p.nodes})
                </option>
              ))}
            </select>
            <button
              type="button"
              onClick={onToggleFull}
              className="label rounded-sm border border-edge px-2 py-1 text-muted hover:border-muted/60 hover:text-text"
            >
              {full ? "kapat" : "tam ekran"}
            </button>
          </div>
        }
      />
      <CardBody className="min-h-0 p-0">
        {graphError ? (
          <p className="p-4 text-xs text-bad">{graphError}</p>
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
        <CardBody className="text-xs text-muted">tarama durumu okunuyor…</CardBody>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader
        title="Sürekli tarama"
        subtitle={`${status.provider} · ${status.model} · ${status.roots.join(" · ")}`}
        aside={
          <div className="flex items-center gap-2">
            <PhaseBadge status={status} />
            <ProviderModelPicker
              providers={providers}
              provider={provider}
              model={model}
              onProvider={onProvider}
              onModel={onModel}
            />
            {status.paused ? (
              <Button onClick={onResume} disabled={busy}>
                sürdür
              </Button>
            ) : (
              <Button variant="ghost" onClick={onPause} disabled={busy}>
                duraklat
              </Button>
            )}
            <Button variant="ghost" onClick={onNow} disabled={busy || status.paused}>
              şimdi tara
            </Button>
          </div>
        }
      />
      <CardBody className="flex flex-wrap gap-6">
        <Stat label="düğüm" value={status.nodes_total} />
        <Stat label="bu turda" value={status.scanned_session} />
        <Stat label="kalan" value={status.remaining} />
        <Stat label="değişmemiş" value={status.skipped_unchanged} />
        <Stat label="okunamayan" value={status.unreadable_session} />
        <Stat label="tur" value={status.sweeps} />
        <Stat
          label="proje"
          value={status.project_count ? `${status.project_index}/${status.project_count}` : "—"}
        />
        <Stat label="şu an" value={status.project_label || "—"} />
      </CardBody>
      {/* The daemon's own words, not a paraphrase: a message an operator can
          act on is the whole reason it is carried this far. */}
      {status.last_error && (
        <CardBody className="border-t border-edge text-[11px] text-bad">
          {status.last_error}
        </CardBody>
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
    <div className="flex flex-col gap-1">
      <span className="label text-muted">{label}</span>
      <span className="font-mono text-sm text-text">{value}</span>
    </div>
  );
}

// --- the picture -------------------------------------------------------------

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
  const [hover, setHover] = useState<{ node: LayoutNode; x: number; y: number } | null>(null);
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
      setSize({ w: Math.floor(entry.contentRect.width), h: Math.floor(entry.contentRect.height) });
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // The layout lives in a world much larger than this box — see worldSize. It
  // is memoised on the graph alone, so resizing the window or opening full
  // screen re-frames the same picture instead of computing a different one.
  const placed = useMemo(() => {
    if (!graph || graph.nodes.length === 0) return [];
    const world = worldSize(graph.nodes.length);
    return layout(graph.nodes, graph.edges, { ...world, seed: 11 });
  }, [graph]);

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

  useEffect(() => {
    const el = canvas.current;
    if (!el || !graph) return;

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

    ctx.lineWidth = 1 / view.scale;
    for (const e of graph.edges) {
      const a = at.get(e.source);
      const b = at.get(e.target);
      if (!a || !b) continue;
      const lit = selected && (e.source === selected || e.target === selected);
      ctx.strokeStyle = lit ? "rgba(37,71,232,0.8)" : "rgba(138,144,153,0.16)";
      ctx.beginPath();
      ctx.moveTo(a.x, a.y);
      ctx.lineTo(b.x, b.y);
      ctx.stroke();
    }

    for (const n of placed) {
      ctx.beginPath();
      ctx.arc(n.x, n.y, n.r, 0, Math.PI * 2);
      ctx.fillStyle = kindStyle(n.kind).fill;
      ctx.globalAlpha = selected && n.id !== selected ? 0.55 : 0.92;
      ctx.fill();
      if (n.id === selected) {
        ctx.globalAlpha = 1;
        ctx.strokeStyle = "#eef0f2";
        ctx.lineWidth = 2 / view.scale;
        ctx.stroke();
        ctx.lineWidth = 1 / view.scale;
      }
    }
    ctx.globalAlpha = 1;
    ctx.restore();

    ctx.font = "10px ui-monospace, Menlo, monospace";
    ctx.fillStyle = "#9aa1ab";
    for (const label of visibleLabels(placed, view, size.w, size.h)) {
      ctx.fillText(trim(label.node.title, 24), label.x, label.y);
    }
  }, [graph, placed, selected, size.w, size.h, view]);

  const pointAt = (e: React.MouseEvent<HTMLCanvasElement>) => {
    const rect = e.currentTarget.getBoundingClientRect();
    return { sx: e.clientX - rect.left, sy: e.clientY - rect.top };
  };

  // A two-finger scroll on a trackpad is a plain wheel event and a pinch is a
  // wheel event with ctrlKey. Treating both as zoom is what made this feel
  // wrong: on macOS a two-finger scroll means *move the page*, and a map that
  // zooms instead fights the hand. So scroll pans, pinch (and ctrl/⌘ + wheel)
  // zooms — the same split every map on this platform uses.
  const onWheel = (e: React.WheelEvent<HTMLCanvasElement>) => {
    const rect = e.currentTarget.getBoundingClientRect();
    if (e.ctrlKey || e.metaKey) {
      const factor = Math.exp(-e.deltaY * 0.004);
      setView((v) => zoomAt(v, factor, e.clientX - rect.left, e.clientY - rect.top));
      return;
    }
    setView((v) => ({ ...v, x: v.x - e.deltaX, y: v.y - e.deltaY }));
  };

  return (
    <div ref={wrap} className="relative h-full w-full overflow-hidden">
      <canvas
        ref={canvas}
        style={{ width: size.w, height: size.h, cursor: panning ? "grabbing" : "grab" }}
        onWheel={onWheel}
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

      <div className="absolute top-2 left-2 flex items-center gap-1">
        <ViewButton label="−" title="uzaklaş" onClick={() => setView((v) => zoomAt(v, 1 / 1.25, size.w / 2, size.h / 2))} />
        <ViewButton label="+" title="yakınlaş" onClick={() => setView((v) => zoomAt(v, 1.25, size.w / 2, size.h / 2))} />
        <ViewButton label="sığdır" onClick={fit} />
        <span className="label px-1 text-[10px] text-muted">%{Math.round(view.scale * 100)}</span>
        <span className="px-1 text-[10px] text-muted/70">sürükle · kaydır · ⌘+tekerlek yakınlaştırır</span>
      </div>

      {hover && (
        <div
          className="pointer-events-none absolute rounded-sm border border-edge bg-panel px-2 py-1 text-[11px] text-text"
          style={{ left: Math.min(hover.x + 12, Math.max(size.w - 240, 8)), top: Math.max(hover.y - 10, 4) }}
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
    </div>
  );
}

function ViewButton({ label, title, onClick }: { label: string; title?: string; onClick: () => void }) {
  return (
    <button
      type="button"
      title={title}
      onClick={onClick}
      className="label rounded-sm border border-edge bg-panel/80 px-2 py-1 text-muted hover:border-muted/60 hover:text-text"
    >
      {label}
    </button>
  );
}

// --- one node ----------------------------------------------------------------

function NodePanel({
  node,
  versions,
  onOpen,
}: {
  node: BrainNodeDetail | null;
  versions: BrainNodeVersion[];
  onOpen: (id: string | null) => void;
}) {
  if (!node) {
    return (
      <Card className="min-h-0 overflow-auto">
        <CardHeader title="Düğüm" />
        <CardBody className="text-xs text-muted">bir düğüme tıkla</CardBody>
      </Card>
    );
  }

  return (
    <Card className="min-h-0 overflow-auto">
      <CardHeader title={trim(node.title, 40)} subtitle={kindStyle(node.kind).label} />
      <CardBody className="flex flex-col gap-3">
        <p className="text-xs leading-relaxed text-text">{node.assessment}</p>
        <p className="font-mono text-[10px] break-all text-muted">{node.source}</p>
        {node.tags && node.tags.length > 0 && (
          <div className="flex flex-wrap gap-1">
            {node.tags.map((t) => (
              <span key={t} className="rounded-sm border border-edge px-1.5 py-0.5 text-[10px] text-muted">
                {t}
              </span>
            ))}
          </div>
        )}
        <VersionTimeline versions={versions} />
        {node.neighbors && node.neighbors.length > 0 && (
          <div className="flex flex-col gap-1 border-t border-edge pt-2">
            <span className="label text-muted">komşular</span>
            {node.neighbors.map((n) => (
              <button
                key={n.id}
                onClick={() => onOpen(n.id)}
                className="truncate text-left text-[11px] text-text hover:text-electric"
              >
                {trim(n.title, 34)}
                <span className="ml-1 text-muted">· {n.relation}</span>
              </button>
            ))}
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
              className="flex w-full items-baseline justify-between gap-2 text-left text-[11px] hover:text-electric"
            >
              <span className={v.current ? "text-text" : "text-muted"}>
                {v.when}
                {v.current && <span className="ml-1.5 text-[10px] text-muted">şimdiki</span>}
              </span>
              <span className="shrink-0 font-mono text-[10px] text-muted">
                {v.size && <span className="mr-1.5">{v.size}</span>}
                {v.shortHash}
              </span>
            </button>
            {expanded && v.assessment && (
              <p className="mt-1 border-l-2 border-edge pl-2 text-[11px] leading-relaxed text-muted">
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

  const refresh = useCallback(async () => {
    try {
      const res = await api.brainScan();
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
      timer = setTimeout(tick, pollInterval(status));
    };
    void tick();

    return () => {
      live = false;
      clearTimeout(timer);
    };
    // status is read for the interval only; re-subscribing on every status
    // would restart the timer on every tick.
    // eslint-disable-next-line react-hooks/exhaustive-deps
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
