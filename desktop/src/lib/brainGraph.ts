/**
 * The layout behind the Brain tab's picture.
 *
 * A force-directed graph is a physics loop and a bit of arithmetic, and this
 * app owns both rather than pulling in d3: five dependencies is the whole
 * front end, every one of them pinned, and a layout nobody can unit-test is a
 * layout that drifts. Everything here is pure — positions in, positions out —
 * so the canvas component is a renderer and this file is what the tests hold.
 *
 * The physics is Fruchterman–Reingold: every pair repels, every edge pulls,
 * and a falling temperature caps how far a node may move per tick so the
 * picture settles instead of oscillating. The one departure from the textbook
 * is the repulsion, which is computed against a uniform grid rather than every
 * other node — at three thousand nodes the honest O(n²) pass is nine million
 * pairs per tick, which is a frozen window.
 */

import type { BrainGraphEdge, BrainGraphNode } from "./daemon";

export type LayoutNode = {
  id: string;
  x: number;
  y: number;
  vx: number;
  vy: number;
  r: number;
  degree: number;
  kind: string;
  title: string;
  project?: string;
};

export type LayoutOptions = {
  /** The *world* the graph is laid out in, which is not the viewport. */
  width: number;
  height: number;
  /** Ticks to run before the picture is considered settled. */
  ticks?: number;
  /** Seed for the initial scatter. The same graph must draw the same twice. */
  seed?: number;
};

/**
 * worldSize is how much room the layout gets, and it is deliberately much
 * larger than the box on screen.
 *
 * Laying six hundred nodes out inside a viewport is what made the first version
 * a ball of overlapping dots with unreadable labels: the repulsion has nowhere
 * to push, so everything piles up in the middle and the leftovers get clamped
 * into a lattice against the border. The layout gets a world, the viewport is a
 * window onto it, and the operator pans.
 *
 * Square-root of the count because area is what a graph needs, not width.
 */
export function worldSize(count: number): { width: number; height: number } {
  const side = Math.max(1100, Math.min(Math.sqrt(Math.max(count, 1)) * 95, 5200));
  return { width: side, height: side };
}

/**
 * READABLE_SCALE is the floor the picture opens at.
 *
 * Framing everything is the wrong default for a graph this size: six hundred
 * nodes in a three-thousand-pixel world, fitted into a panel, is a scale near
 * 0.2 and dots two pixels across — technically the whole graph, practically a
 * texture. So the first view is readable and the operator pans; "sığdır" is
 * there for when the overview is what they actually want.
 */
export const READABLE_SCALE = 0.5;

/** initialView frames the graph, but never smaller than READABLE_SCALE. */
export function initialView(
  nodes: LayoutNode[],
  viewportW: number,
  viewportH: number,
): View {
  const fitted = fitView(nodes, viewportW, viewportH);
  if (nodes.length === 0 || fitted.scale >= READABLE_SCALE) return fitted;

  // Same centre, readable scale.
  const cx = (fitted.x - viewportW / 2) / -fitted.scale;
  const cy = (fitted.y - viewportH / 2) / -fitted.scale;
  return {
    scale: READABLE_SCALE,
    x: viewportW / 2 - cx * READABLE_SCALE,
    y: viewportH / 2 - cy * READABLE_SCALE,
  };
}

/** How the world is mapped onto the canvas: screen = world * scale + offset. */
export type View = { scale: number; x: number; y: number };

export const MIN_SCALE = 0.08;
export const MAX_SCALE = 4;

export function clampScale(scale: number): number {
  return Math.max(MIN_SCALE, Math.min(scale, MAX_SCALE));
}

export function screenToWorld(view: View, sx: number, sy: number): { x: number; y: number } {
  return { x: (sx - view.x) / view.scale, y: (sy - view.y) / view.scale };
}

export function worldToScreen(view: View, wx: number, wy: number): { x: number; y: number } {
  return { x: wx * view.scale + view.x, y: wy * view.scale + view.y };
}

/** Zoom about a point on screen, so the thing under the pointer stays under it —
 * the difference between zooming and being teleported. */
export function zoomAt(view: View, factor: number, sx: number, sy: number): View {
  const scale = clampScale(view.scale * factor);
  const before = screenToWorld(view, sx, sy);
  return { scale, x: sx - before.x * scale, y: sy - before.y * scale };
}

/** fitView frames everything that was laid out, with a margin. */
export function fitView(
  nodes: LayoutNode[],
  viewportW: number,
  viewportH: number,
  padding = 48,
): View {
  if (nodes.length === 0 || viewportW <= 0 || viewportH <= 0) {
    return { scale: 1, x: 0, y: 0 };
  }
  let minX = Infinity;
  let minY = Infinity;
  let maxX = -Infinity;
  let maxY = -Infinity;
  for (const n of nodes) {
    minX = Math.min(minX, n.x - n.r);
    minY = Math.min(minY, n.y - n.r);
    maxX = Math.max(maxX, n.x + n.r);
    maxY = Math.max(maxY, n.y + n.r);
  }
  const w = Math.max(maxX - minX, 1);
  const h = Math.max(maxY - minY, 1);
  const scale = clampScale(Math.min((viewportW - padding * 2) / w, (viewportH - padding * 2) / h));
  return {
    scale,
    x: viewportW / 2 - ((minX + maxX) / 2) * scale,
    y: viewportH / 2 - ((minY + maxY) / 2) * scale,
  };
}

/**
 * mulberry32 — a seeded PRNG in four lines.
 *
 * Math.random() would make every render a different picture and every test a
 * coin toss. A node that moved because the app re-rendered is a node the eye
 * follows for no reason.
 */
export function rng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** degrees counts edges per node id. The daemon sends a degree per node, but it
 * is the degree in the *whole* graph; inside a filtered view what matters is
 * how connected a node is here. */
export function degrees(edges: BrainGraphEdge[]): Map<string, number> {
  const out = new Map<string, number>();
  for (const e of edges) {
    out.set(e.source, (out.get(e.source) ?? 0) + 1);
    out.set(e.target, (out.get(e.target) ?? 0) + 1);
  }
  return out;
}

/** Radius from degree. Square root, not linear: area is what the eye reads as
 * size, and a hub with thirty edges must not be thirty times a leaf. */
export function radiusFor(degree: number): number {
  return 3 + Math.sqrt(degree) * 2.4;
}

export function seedLayout(
  nodes: BrainGraphNode[],
  edges: BrainGraphEdge[],
  opts: LayoutOptions,
): LayoutNode[] {
  const local = degrees(edges);
  const random = rng(opts.seed ?? 1);
  const cx = opts.width / 2;
  const cy = opts.height / 2;
  const spread = Math.min(opts.width, opts.height) * 0.46;

  return nodes.map((n) => {
    // A ring rather than a square: a scatter that starts uniform in a box
    // spends its first fifty ticks becoming a disc anyway.
    const angle = random() * Math.PI * 2;
    const radius = Math.sqrt(random()) * spread;
    const degree = local.get(n.id) ?? n.degree ?? 0;
    return {
      id: n.id,
      x: cx + Math.cos(angle) * radius,
      y: cy + Math.sin(angle) * radius,
      vx: 0,
      vy: 0,
      r: radiusFor(degree),
      degree,
      kind: n.kind,
      title: n.title,
      project: n.project,
    };
  });
}

/**
 * layout runs the whole simulation and returns the settled positions.
 *
 * The tick budget is fixed rather than run-until-stable: a person opening a tab
 * wants a picture now, and the difference between three hundred ticks and a
 * thousand is invisible at this size.
 */
export function layout(
  nodes: BrainGraphNode[],
  edges: BrainGraphEdge[],
  opts: LayoutOptions,
): LayoutNode[] {
  const placed = seedLayout(nodes, edges, opts);
  if (placed.length === 0) return placed;

  const index = new Map(placed.map((n) => [n.id, n]));
  // Edges whose endpoints are not both present are dropped rather than
  // trusted. The daemon promises they cannot happen; a canvas that believed
  // one would either draw a line to nowhere or throw.
  const links = edges
    .map((e) => ({ a: index.get(e.source), b: index.get(e.target), w: e.weight }))
    .filter((l): l is { a: LayoutNode; b: LayoutNode; w: number } => !!l.a && !!l.b);

  const area = opts.width * opts.height;
  const k = Math.sqrt(area / placed.length);
  const ticks = opts.ticks ?? 320;
  let temperature = Math.min(opts.width, opts.height) / 8;
  const cooling = temperature / (ticks + 1);

  for (let step = 0; step < ticks; step++) {
    repel(placed, k, opts);
    attract(links, k);
    move(placed, temperature, opts);
    temperature = Math.max(temperature - cooling, 0.2);
  }
  // The forces settle the *structure*; they do not guarantee that two dots are
  // not on top of each other, and two overlapping dots are one dot as far as a
  // reader is concerned. A few passes of plain separation afterwards cost
  // nothing and are what makes the picture countable.
  separate(placed, k);
  return placed;
}

/** separate pushes overlapping nodes apart, using the same grid as the
 * repulsion so it stays linear in the node count. */
function separate(nodes: LayoutNode[], k: number): void {
  const gap = 6;
  const cell = Math.max(k / 2, 24);
  for (let pass = 0; pass < 12; pass++) {
    const buckets = new Map<string, LayoutNode[]>();
    for (const n of nodes) {
      const key = `${Math.floor(n.x / cell)}:${Math.floor(n.y / cell)}`;
      const list = buckets.get(key);
      if (list) list.push(n);
      else buckets.set(key, [n]);
    }

    let moved = false;
    for (const n of nodes) {
      const gx = Math.floor(n.x / cell);
      const gy = Math.floor(n.y / cell);
      for (let dx = -1; dx <= 1; dx++) {
        for (let dy = -1; dy <= 1; dy++) {
          const others = buckets.get(`${gx + dx}:${gy + dy}`);
          if (!others) continue;
          for (const o of others) {
            if (o === n || o.id <= n.id) continue;
            const ddx = o.x - n.x;
            const ddy = o.y - n.y;
            const dist = Math.hypot(ddx, ddy);
            const want = n.r + o.r + gap;
            if (dist >= want) continue;
            const push = (want - (dist || 0.01)) / 2;
            const ux = dist === 0 ? 1 : ddx / dist;
            const uy = dist === 0 ? 0 : ddy / dist;
            n.x -= ux * push;
            n.y -= uy * push;
            o.x += ux * push;
            o.y += uy * push;
            moved = true;
          }
        }
      }
    }
    if (!moved) return;
  }
}

/** repel is the O(n²) half, made cheap by only ever comparing nodes in
 * neighbouring grid cells: past about two k the force is a rounding error, and
 * summing three thousand rounding errors is how a frame budget disappears. */
function repel(nodes: LayoutNode[], k: number, opts: LayoutOptions): void {
  const cell = k * 2;
  const buckets = new Map<string, LayoutNode[]>();

  const key = (x: number, y: number) => `${Math.floor(x / cell)}:${Math.floor(y / cell)}`;
  for (const n of nodes) {
    n.vx = 0;
    n.vy = 0;
    const bucket = key(n.x, n.y);
    const list = buckets.get(bucket);
    if (list) list.push(n);
    else buckets.set(bucket, [n]);
  }

  for (const n of nodes) {
    const gx = Math.floor(n.x / cell);
    const gy = Math.floor(n.y / cell);
    for (let dx = -1; dx <= 1; dx++) {
      for (let dy = -1; dy <= 1; dy++) {
        const others = buckets.get(`${gx + dx}:${gy + dy}`);
        if (!others) continue;
        for (const o of others) {
          if (o === n) continue;
          let ddx = n.x - o.x;
          let ddy = n.y - o.y;
          let dist = Math.hypot(ddx, ddy);
          if (dist === 0) {
            // Two nodes on the same point have no direction to separate along,
            // so they are given one. Without this they stay welded together for
            // the whole run.
            ddx = (n.id < o.id ? 1 : -1) * 0.01;
            ddy = 0.01;
            dist = 0.0142;
          }
          const force = (k * k) / dist;
          n.vx += (ddx / dist) * force;
          n.vy += (ddy / dist) * force;
        }
      }
    }
  }

  // A pull to the middle, or anything the repulsion pushed outward never comes
  // back. It is stronger for a node with no edges than for one with many:
  // otherwise the loose half of a knowledge base — files nothing has linked yet
  // — drifts into a wide halo and the structure everybody came to look at ends
  // up as a speck in the centre.
  const cx = opts.width / 2;
  const cy = opts.height / 2;
  for (const n of nodes) {
    const gravity = n.degree === 0 ? 0.05 : 0.014;
    n.vx += (cx - n.x) * gravity;
    n.vy += (cy - n.y) * gravity;
  }
}

function attract(links: { a: LayoutNode; b: LayoutNode; w: number }[], k: number): void {
  for (const l of links) {
    const dx = l.a.x - l.b.x;
    const dy = l.a.y - l.b.y;
    const dist = Math.hypot(dx, dy) || 0.01;
    // Weight scales the pull: a semantic verdict from the model should hold
    // two nodes closer than a shared tag does.
    const force = ((dist * dist) / k) * (0.5 + l.w);
    const fx = (dx / dist) * force;
    const fy = (dy / dist) * force;
    l.a.vx -= fx;
    l.a.vy -= fy;
    l.b.vx += fx;
    l.b.vy += fy;
  }
}

function move(nodes: LayoutNode[], temperature: number, opts: LayoutOptions): void {
  // The bound is the world's, not the viewport's, and it is generous on
  // purpose. A tight clamp is what produced the regular lattice of dots along
  // the old border: everything the repulsion pushed outward stacked up against
  // the wall in rows, which reads as structure and is nothing of the kind.
  const margin = Math.min(opts.width, opts.height) * 0.02;
  for (const n of nodes) {
    const speed = Math.hypot(n.vx, n.vy) || 1;
    const step = Math.min(speed, temperature);
    n.x += (n.vx / speed) * step;
    n.y += (n.vy / speed) * step;
    n.x = clamp(n.x, margin, opts.width - margin);
    n.y = clamp(n.y, margin, opts.height - margin);
  }
}

function clamp(v: number, lo: number, hi: number): number {
  return v < lo ? lo : v > hi ? hi : v;
}

/** hitTest returns the topmost node under a point, or null. Smallest first, so
 * a leaf sitting on top of a hub is still clickable. */
export function hitTest(nodes: LayoutNode[], x: number, y: number): LayoutNode | null {
  let best: LayoutNode | null = null;
  let bestDist = Infinity;
  for (const n of nodes) {
    const d = Math.hypot(n.x - x, n.y - y);
    // A few pixels of slack: these dots are three to twelve pixels across and
    // a pointer is not a scalpel.
    if (d <= n.r + 4 && d < bestDist) {
      best = n;
      bestDist = d;
    }
  }
  return best;
}

/**
 * kindStyle maps a node kind onto the brand's four colours.
 *
 * The obvious thing is a hue per kind, the way every knowledge-graph screenshot
 * does it. The brand system says four colours and no fifth (desktop/AGENTS.md),
 * and it is right for the same reason it is right everywhere else in this app:
 * eleven hues would say "these categories are unrelated", when what the picture
 * is actually about is one body of knowledge with a few kinds of thing in it.
 * So the kinds are separated by *role* — Electric for what the operator writes,
 * Lime for what the machine recorded, Mist for the files — and by weight.
 */
export type KindStyle = { fill: string; label: string };

const KIND_STYLES: Record<string, KindStyle> = {
  file: { fill: "#8a9099", label: "dosya" },
  session: { fill: "#c6f04a", label: "oturum" },
  commit: { fill: "#6f7b3f", label: "commit" },
  decision: { fill: "#2547e8", label: "karar" },
  research: { fill: "#5b74f0", label: "araştırma" },
  note: { fill: "#eef0f2", label: "not" },
};

const UNKNOWN_KIND: KindStyle = { fill: "#4f545e", label: "diğer" };

export function kindStyle(kind: string): KindStyle {
  return KIND_STYLES[kind] ?? UNKNOWN_KIND;
}

export function kindLegend(): { kind: string; style: KindStyle }[] {
  return Object.keys(KIND_STYLES).map((kind) => ({ kind, style: KIND_STYLES[kind] }));
}

/**
 * visibleLabels decides which nodes get their title drawn.
 *
 * Two rules, both learned from the version that had neither: only what is on
 * screen, and only where the text does not land on text already placed. Every
 * node labelled is the screenshot everyone has seen and nobody can read — and
 * at this density even the hubs overwrite each other.
 */
export function visibleLabels(
  nodes: LayoutNode[],
  view: View,
  viewportW: number,
  viewportH: number,
  max = 40,
): { node: LayoutNode; x: number; y: number }[] {
  const charW = 5.6;
  const lineH = 13;
  const out: { node: LayoutNode; x: number; y: number }[] = [];
  const boxes: { x1: number; y1: number; x2: number; y2: number }[] = [];

  // Most connected first: when two labels collide, the hub is the one worth
  // keeping.
  for (const n of [...nodes].sort((a, b) => b.degree - a.degree)) {
    if (out.length >= max) break;
    const p = worldToScreen(view, n.x, n.y);
    if (p.x < -40 || p.y < -20 || p.x > viewportW + 40 || p.y > viewportH + 20) continue;

    const x = p.x + n.r * view.scale + 5;
    const y = p.y + 3;
    const box = {
      x1: x,
      y1: y - lineH,
      x2: x + Math.min(n.title.length, 24) * charW,
      y2: y + 3,
    };
    if (boxes.some((b) => !(box.x2 < b.x1 || box.x1 > b.x2 || box.y2 < b.y1 || box.y1 > b.y2))) {
      continue;
    }
    boxes.push(box);
    out.push({ node: n, x, y });
  }
  return out;
}

/**
 * pollInterval for the scan status: fast while something is happening, slow
 * while nothing is. The same shape as the board's, and for the same reason —
 * a screen nobody is watching should not be asking every two seconds.
 */
export function pollInterval(status: BrainScanStatusLike | null): number {
  if (!status) return 1000;
  if (status.phase === "scanning" || status.phase === "discovering") return 2000;
  if (status.phase === "backoff") return 5000;
  return 30000;
}

type BrainScanStatusLike = { phase: string };

/* -------------------------------------------------------------------------- */
/* version history                                                             */
/* -------------------------------------------------------------------------- */

/** One row of the node panel's version timeline, ready to render. */
export type VersionLine = {
  hash: string;
  /** Short hash — a full sha256 in a 288px panel is a wall. */
  shortHash: string;
  when: string;
  size: string;
  title: string;
  assessment: string;
  /** True for the reading that is current — the one the panel shows above. */
  current: boolean;
};

type VersionLike = {
  content_hash: string;
  seen_at: number;
  size_bytes?: number;
  title?: string;
  assessment?: string;
};

/**
 * The timeline, newest first.
 *
 * Formatting lives here rather than in JSX for the same reason the lead-gen
 * counting does: a date format and a "which one is current" decision are both
 * things a test can pin, and neither is visible in a rendered panel until it is
 * wrong.
 */
export function versionLines(versions: VersionLike[]): VersionLine[] {
  return versions.map((v, i) => ({
    hash: v.content_hash,
    shortHash: v.content_hash.slice(0, 8),
    when: formatSeen(v.seen_at),
    size: formatBytes(v.size_bytes),
    title: v.title ?? "",
    assessment: v.assessment ?? "",
    // The list is newest first, so the first row is what the node says now.
    current: i === 0,
  }));
}

function formatSeen(unix: number): string {
  if (!unix) return "—";
  return new Date(unix * 1000).toLocaleString("tr-TR", {
    day: "2-digit",
    month: "short",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/** Bytes as a person reads them. Deliberately coarse: this is a size, not a measurement. */
export function formatBytes(n?: number): string {
  if (!n || n <= 0) return "";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}
