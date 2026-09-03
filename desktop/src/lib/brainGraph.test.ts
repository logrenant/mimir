import { describe, expect, it } from "vitest";

import type { BrainGraphEdge, BrainGraphNode } from "./daemon";
import {
  degrees,
  fitView,
  formatBytes,
  hitTest,
  initialView,
  kindStyle,
  layout,
  MAX_SCALE,
  MIN_SCALE,
  pollInterval,
  radiusFor,
  READABLE_SCALE,
  rng,
  screenToWorld,
  seedLayout,
  versionLines,
  visibleLabels,
  worldSize,
  worldToScreen,
  zoomAt,
  type View,
} from "./brainGraph";

function graph(size: number): { nodes: BrainGraphNode[]; edges: BrainGraphEdge[] } {
  const nodes: BrainGraphNode[] = [];
  const edges: BrainGraphEdge[] = [];
  for (let i = 0; i < size; i++) {
    nodes.push({ id: `n${i}`, kind: "file", title: `file ${i}`, degree: 0, updated_at: 0 });
    if (i > 0) edges.push({ source: "n0", target: `n${i}`, kind: "tag", weight: 0.6 });
  }
  return { nodes, edges };
}

const opts = { width: 800, height: 600, ticks: 40, seed: 7 };

describe("rng", () => {
  it("is deterministic, so the same graph draws the same picture twice", () => {
    const a = rng(42);
    const b = rng(42);
    expect([a(), a(), a()]).toEqual([b(), b(), b()]);
  });
});

describe("degrees", () => {
  it("counts both ends of every edge", () => {
    const { edges } = graph(4);
    const d = degrees(edges);
    expect(d.get("n0")).toBe(3);
    expect(d.get("n1")).toBe(1);
  });

  it("sizes a hub by area, not by count", () => {
    // Thirty edges must not draw a node thirty times a leaf.
    expect(radiusFor(30)).toBeLessThan(radiusFor(1) * 5);
    expect(radiusFor(30)).toBeGreaterThan(radiusFor(1));
  });
});

describe("layout", () => {
  it("is deterministic for the same seed", () => {
    const { nodes, edges } = graph(30);
    const a = layout(nodes, edges, opts);
    const b = layout(nodes, edges, opts);
    expect(a.map((n) => [n.id, Math.round(n.x), Math.round(n.y)])).toEqual(
      b.map((n) => [n.id, Math.round(n.x), Math.round(n.y)]),
    );
  });

  it("keeps every node inside the viewport", () => {
    const { nodes, edges } = graph(60);
    for (const n of layout(nodes, edges, opts)) {
      expect(n.x).toBeGreaterThanOrEqual(0);
      expect(n.x).toBeLessThanOrEqual(opts.width);
      expect(n.y).toBeGreaterThanOrEqual(0);
      expect(n.y).toBeLessThanOrEqual(opts.height);
      expect(Number.isFinite(n.x) && Number.isFinite(n.y)).toBe(true);
    }
  });

  it("separates two nodes that start on the same point", () => {
    const nodes: BrainGraphNode[] = [
      { id: "a", kind: "file", title: "a", degree: 0, updated_at: 0 },
      { id: "b", kind: "file", title: "b", degree: 0, updated_at: 0 },
    ];
    const placed = layout(nodes, [], { ...opts, seed: 1 });
    placed[0].x = placed[1].x;
    placed[0].y = placed[1].y;
    const again = layout(nodes, [], { ...opts, seed: 1, ticks: 60 });
    const dist = Math.hypot(again[0].x - again[1].x, again[0].y - again[1].y);
    expect(dist).toBeGreaterThan(1);
  });

  it("pulls a connected pair closer than an unconnected one", () => {
    const nodes: BrainGraphNode[] = [
      { id: "a", kind: "file", title: "a", degree: 1, updated_at: 0 },
      { id: "b", kind: "file", title: "b", degree: 1, updated_at: 0 },
      { id: "c", kind: "file", title: "c", degree: 0, updated_at: 0 },
      { id: "d", kind: "file", title: "d", degree: 0, updated_at: 0 },
    ];
    const linked: BrainGraphEdge[] = [{ source: "a", target: "b", kind: "semantic", weight: 1 }];
    const placed = layout(nodes, linked, { width: 600, height: 600, ticks: 300, seed: 3 });
    const at = (id: string) => placed.find((n) => n.id === id)!;
    const linkedDist = Math.hypot(at("a").x - at("b").x, at("a").y - at("b").y);
    const looseDist = Math.hypot(at("c").x - at("d").x, at("c").y - at("d").y);
    expect(linkedDist).toBeLessThan(looseDist);
  });

  // The daemon guarantees it; the canvas is what breaks if it ever stops being
  // true, and a thrown exception in a render is a blank screen with no message.
  it("ignores an edge whose endpoints are not both present", () => {
    const { nodes, edges } = graph(5);
    const dangling = [...edges, { source: "n0", target: "ghost", kind: "tag", weight: 1 }];
    expect(() => layout(nodes, dangling, opts)).not.toThrow();
    expect(layout(nodes, dangling, opts)).toHaveLength(5);
  });

  it("handles an empty graph", () => {
    expect(layout([], [], opts)).toEqual([]);
  });
});

describe("hitTest", () => {
  it("finds the node under the pointer and nothing under empty space", () => {
    const placed = seedLayout(graph(3).nodes, graph(3).edges, opts);
    const target = placed[1];
    expect(hitTest(placed, target.x, target.y)?.id).toBe(target.id);
    expect(hitTest(placed, -500, -500)).toBeNull();
  });
});

describe("kindStyle", () => {
  it("stays inside the brand's colours and names an unknown kind rather than throwing", () => {
    const allowed = new Set(["#8a9099", "#c6f04a", "#6f7b3f", "#2547e8", "#5b74f0", "#eef0f2", "#4f545e"]);
    for (const kind of ["file", "session", "commit", "decision", "research", "note", "invented"]) {
      expect(allowed.has(kindStyle(kind).fill)).toBe(true);
      expect(kindStyle(kind).label).not.toBe("");
    }
  });
});

describe("pollInterval", () => {
  it("asks often while something is happening and rarely while nothing is", () => {
    expect(pollInterval(null)).toBe(1000);
    expect(pollInterval({ phase: "scanning" })).toBe(2000);
    expect(pollInterval({ phase: "backoff" })).toBe(5000);
    expect(pollInterval({ phase: "idle" })).toBe(30000);
    expect(pollInterval({ phase: "paused" })).toBe(30000);
  });
});

describe("world space", () => {
  it("gives a bigger graph more room, without letting it run away", () => {
    expect(worldSize(20).width).toBeLessThan(worldSize(2000).width);
    expect(worldSize(1).width).toBeGreaterThanOrEqual(1100);
    expect(worldSize(100000).width).toBeLessThanOrEqual(5200);
  });

  // The whole reason the layout left the viewport: six hundred nodes in a box
  // the size of a panel is a ball of overlapping dots.
  it("lays a graph out larger than any viewport, so it can be panned", () => {
    const { nodes, edges } = graph(400);
    const world = worldSize(nodes.length);
    const placed = layout(nodes, edges, { ...world, ticks: 60, seed: 3 });
    const spanX = Math.max(...placed.map((n) => n.x)) - Math.min(...placed.map((n) => n.x));
    expect(spanX).toBeGreaterThan(900);
  });

  it("stops overlapping dots being drawn as one dot", () => {
    const { nodes, edges } = graph(120);
    const world = worldSize(nodes.length);
    const placed = layout(nodes, edges, { ...world, ticks: 80, seed: 5 });

    let overlaps = 0;
    for (let i = 0; i < placed.length; i++) {
      for (let j = i + 1; j < placed.length; j++) {
        const d = Math.hypot(placed[i].x - placed[j].x, placed[i].y - placed[j].y);
        if (d < placed[i].r + placed[j].r) overlaps++;
      }
    }
    expect(overlaps).toBe(0);
  });
});

describe("view", () => {
  it("keeps the point under the cursor fixed while zooming", () => {
    const before: View = { scale: 1, x: 40, y: -20 };
    const world = screenToWorld(before, 300, 200);
    const after = zoomAt(before, 2, 300, 200);
    const back = worldToScreen(after, world.x, world.y);
    expect(back.x).toBeCloseTo(300, 6);
    expect(back.y).toBeCloseTo(200, 6);
  });

  it("clamps the zoom at both ends", () => {
    expect(zoomAt({ scale: 1, x: 0, y: 0 }, 1000, 0, 0).scale).toBe(MAX_SCALE);
    expect(zoomAt({ scale: 1, x: 0, y: 0 }, 0.00001, 0, 0).scale).toBe(MIN_SCALE);
  });

  it("frames every node when it fits the view", () => {
    const { nodes, edges } = graph(80);
    const placed = layout(nodes, edges, { ...worldSize(80), ticks: 60, seed: 2 });
    const view = fitView(placed, 900, 600);
    for (const n of placed) {
      const p = worldToScreen(view, n.x, n.y);
      expect(p.x).toBeGreaterThanOrEqual(-1);
      expect(p.x).toBeLessThanOrEqual(901);
      expect(p.y).toBeGreaterThanOrEqual(-1);
      expect(p.y).toBeLessThanOrEqual(601);
    }
  });

  it("fits an empty graph without dividing by zero", () => {
    expect(fitView([], 800, 600)).toEqual({ scale: 1, x: 0, y: 0 });
  });
});

describe("visibleLabels", () => {
  it("labels nothing that is off screen and nothing that would overlap", () => {
    const { nodes, edges } = graph(200);
    const placed = layout(nodes, edges, { ...worldSize(200), ticks: 60, seed: 9 });
    const view = fitView(placed, 800, 600);
    const labels = visibleLabels(placed, view, 800, 600, 40);

    expect(labels.length).toBeLessThanOrEqual(40);
    for (const l of labels) {
      expect(l.x).toBeGreaterThan(-40);
      expect(l.x).toBeLessThan(840);
    }
    // No two label boxes share space: overlapping text is what made the first
    // version unreadable. The box is measured the way the implementation
    // measures it — by the title's own length, capped at what it draws.
    const box = (l: (typeof labels)[number]) => ({
      x1: l.x,
      y1: l.y - 13,
      x2: l.x + Math.min(l.node.title.length, 24) * 5.6,
      y2: l.y + 3,
    });
    for (let i = 0; i < labels.length; i++) {
      for (let j = i + 1; j < labels.length; j++) {
        const a = box(labels[i]);
        const b = box(labels[j]);
        const overlap = !(a.x2 < b.x1 || a.x1 > b.x2 || a.y2 < b.y1 || a.y1 > b.y2);
        expect(overlap).toBe(false);
      }
    }
  });

  // The hub is the label worth keeping when two collide.
  it("prefers the most connected node when labels compete", () => {
    const nodes: BrainGraphNode[] = [
      { id: "hub", kind: "file", title: "hub", degree: 9, updated_at: 0 },
      { id: "leaf", kind: "file", title: "leaf", degree: 0, updated_at: 0 },
    ];
    const placed = layout(nodes, [], { width: 1400, height: 1400, ticks: 10, seed: 1 });
    placed[0].degree = 9;
    placed[1].degree = 0;
    placed[1].x = placed[0].x + 1;
    placed[1].y = placed[0].y;
    const labels = visibleLabels(placed, { scale: 1, x: 0, y: 0 }, 4000, 4000, 40);
    expect(labels[0].node.id).toBe("hub");
  });
});

describe("initialView", () => {
  // Fitting everything is the wrong default at this size: it is the whole graph
  // rendered as a texture.
  it("never opens below the readable floor, however small the box", () => {
    const { nodes, edges } = graph(600);
    const placed = layout(nodes, edges, { ...worldSize(600), ticks: 40, seed: 4 });

    // A box this small cannot frame the graph, so fitting it would be a
    // texture: the opening view refuses to go below the floor and the operator
    // pans instead.
    expect(fitView(placed, 380, 260).scale).toBeLessThan(READABLE_SCALE);
    expect(initialView(placed, 380, 260).scale).toBe(READABLE_SCALE);
  });

  it("frames a small graph completely, because it fits", () => {
    const { nodes, edges } = graph(6);
    const placed = layout(nodes, edges, { ...worldSize(6), ticks: 40, seed: 4 });
    const view = initialView(placed, 1200, 800);
    expect(view.scale).toBe(fitView(placed, 1200, 800).scale);
    expect(view.scale).toBeGreaterThanOrEqual(READABLE_SCALE);
  });

  it("keeps the graph centred at the readable scale", () => {
    const { nodes, edges } = graph(600);
    const placed = layout(nodes, edges, { ...worldSize(600), ticks: 40, seed: 4 });
    const view = initialView(placed, 1200, 800);
    const cx = placed.reduce((a, n) => a + n.x, 0) / placed.length;
    const cy = placed.reduce((a, n) => a + n.y, 0) / placed.length;
    const centre = worldToScreen(view, cx, cy);
    expect(Math.abs(centre.x - 600)).toBeLessThan(220);
    expect(Math.abs(centre.y - 400)).toBeLessThan(220);
  });
});

describe("the version timeline", () => {
  const versions = [
    { content_hash: "b".repeat(64), seen_at: 1_700_003_600, size_bytes: 2048, assessment: "yeni okuma" },
    { content_hash: "a".repeat(64), seen_at: 1_700_000_000, size_bytes: 1024, assessment: "eski okuma" },
  ];

  // The list is newest first, so the first row is what the panel already shows
  // above it — saying so is what stops the timeline reading as two unrelated
  // assessments.
  it("marks only the newest reading as current", () => {
    const lines = versionLines(versions);
    expect(lines.map((l) => l.current)).toEqual([true, false]);
  });

  it("shortens the hash — a full sha256 does not fit a side panel", () => {
    expect(versionLines(versions)[0].shortHash).toHaveLength(8);
  });

  it("keeps each reading with its own version", () => {
    expect(versionLines(versions)[1].assessment).toBe("eski okuma");
  });

  it("a missing size is blank, not '0 B'", () => {
    expect(formatBytes(undefined)).toBe("");
    expect(formatBytes(0)).toBe("");
  });

  it("sizes read the way a person reads them", () => {
    expect(formatBytes(900)).toBe("900 B");
    expect(formatBytes(2048)).toBe("2 KB");
    expect(formatBytes(3 * 1024 * 1024)).toBe("3.0 MB");
  });
});
