import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { open } from "@tauri-apps/plugin-dialog";

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
  type BrainScanPolicy,
  type BrainScanStatus,
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
  const scanPolicy = useScanPolicy();
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
    <div className="grid h-full grid-rows-[auto_auto_1fr] gap-4 overflow-hidden p-6">
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

  const dirty = isDirty(draft, { roots: policy.roots, excludes: policy.excludes });

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
                geri al
              </Button>
            )}
            <Button onClick={onSave} disabled={busy || !dirty}>
              kaydet
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
              klasör ekle
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
                klasör
              </Button>
              <Button variant="ghost" onClick={() => onAddExclude("file")} disabled={busy}>
                dosya
              </Button>
            </div>
          }
        />
      </CardBody>

      <CardBody className="flex items-center justify-between border-t border-edge">
        <span className="text-[11px] text-muted">
          Hariç tutulan bir yol bir daha okunmaz. Bilgi grafiğinde ondan gelmiş düğümler
          varsa yerinde kalır — silmek ayrı bir iş.
        </span>
        {policy.configured && (
          <Button variant="ghost" onClick={onReset} disabled={busy}>
            varsayılana dön
          </Button>
        )}
      </CardBody>

      {error && (
        // internal/project's guards explain themselves ("home dizini", "bir
        // dizin değil"), so the daemon's own sentence is shown as written.
        <CardBody className="border-t border-edge text-[11px] text-bad">{error}</CardBody>
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
        <span className="text-[11px] uppercase tracking-wide text-muted">{title}</span>
        {action}
      </div>
      {paths.length === 0 ? (
        <p className="text-[11px] text-muted">{empty}</p>
      ) : (
        <ul className="flex flex-col gap-1">
          {paths.map((path) => (
            <li
              key={path}
              className="flex items-center justify-between gap-2 rounded border border-edge px-2 py-1"
            >
              {/* The full path in the tooltip: two folders called `src` are
                  indistinguishable by their last segment, and the short form is
                  what makes the list readable at all. */}
              <span className="truncate font-mono text-[11px]" title={path}>
                {shortPath(path)}
              </span>
              <button
                type="button"
                className="shrink-0 text-[11px] text-muted hover:text-bad disabled:opacity-40"
                onClick={() => onRemove(path)}
                disabled={busy}
                aria-label={`${path} listeden çıkar`}
              >
                çıkar
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
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
  const [draft, setDraft] = useState<ScanPolicyDraft>({ roots: [], excludes: [] });
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
      adopt(await api.saveBrainScanPolicy({ roots: draft.roots, excludes: draft.excludes }));
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
