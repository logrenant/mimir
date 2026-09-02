import type { Run, RunStatus } from "./daemon";

/**
 * The board's rules, kept out of the component that draws it.
 *
 * Two of these columns belong to the operator and four states belong to the
 * runner, and that split is the whole design: a card can be dragged between
 * Backlog and Queued because those are decisions, and cannot be dragged into
 * Running or out of Done because those are facts.
 */

export type ColumnID = "backlog" | "queued" | "running" | "done" | "failed";

export type Column = {
  id: ColumnID;
  label: string;
  /** The run statuses this column shows. */
  statuses: RunStatus[];
  /** Whether a card may be dropped here at all. */
  droppable: boolean;
};

export const BOARD_COLUMNS: Column[] = [
  { id: "backlog", label: "Backlog", statuses: ["backlog"], droppable: true },
  { id: "queued", label: "Queued", statuses: ["queued"], droppable: true },
  { id: "running", label: "Running", statuses: ["running"], droppable: false },
  { id: "done", label: "Done", statuses: ["completed"], droppable: false },
  // A stopped run sits with the failures because that is where the operator
  // looks for "did not finish", but it keeps its own badge: nothing went wrong.
  { id: "failed", label: "Failed", statuses: ["failed", "stopped"], droppable: false },
];

export function columnOf(status: RunStatus): ColumnID | null {
  const column = BOARD_COLUMNS.find((c) => c.statuses.includes(status));
  return column ? column.id : null;
}

/** Groups runs into columns, preserving the order they arrived in. */
export function groupRuns(runs: Run[]): Record<ColumnID, Run[]> {
  const grouped: Record<ColumnID, Run[]> = {
    backlog: [],
    queued: [],
    running: [],
    done: [],
    failed: [],
  };
  for (const run of runs) {
    const column = columnOf(run.status);
    if (column) grouped[column].push(run);
  }
  // Queued is the one column with a meaningful order: it is a queue, and the
  // dispatcher takes the oldest first.
  grouped.queued.sort((a, b) => (a.queued_at ?? "").localeCompare(b.queued_at ?? ""));
  return grouped;
}

export type Move =
  | { allowed: true; action: "enqueue" | "dequeue" }
  | { allowed: false; reason: string };

/**
 * Whether a card may be dragged from one column to another, and what that
 * means.
 *
 * Refusals carry a reason because a drop that silently springs back reads as a
 * broken board rather than a rule.
 */
export function allowedMove(from: ColumnID, to: ColumnID): Move {
  if (from === to) return { allowed: false, reason: "" };
  if (from === "backlog" && to === "queued") {
    return { allowed: true, action: "enqueue" };
  }
  if (from === "queued" && to === "backlog") {
    return { allowed: true, action: "dequeue" };
  }
  if (to === "running") {
    return { allowed: false, reason: "Running kolonuna sürüklenmez — Queued'a bırakın, sıra gelince başlar." };
  }
  if (to === "done" || to === "failed") {
    return { allowed: false, reason: "Bir çalışmanın sonucunu elle yazamazsınız." };
  }
  return { allowed: false, reason: "Bitmiş bir kart tekrar kuyruğa alınamaz." };
}

/** The actions a card offers, given where it is. */
export type CardAction = "run" | "stop" | "dequeue" | "delete" | "terminal";

export function actionsFor(status: RunStatus): CardAction[] {
  switch (status) {
    case "backlog":
      return ["run", "delete"];
    case "queued":
      return ["dequeue", "terminal"];
    case "running":
      return ["stop", "terminal"];
    default:
      return ["terminal", "delete"];
  }
}

/**
 * How often the board should ask the daemon again.
 *
 * A board with nothing moving does not need a request every four seconds, and
 * a board with a run in flight wants one sooner than that.
 */
export function pollInterval(runs: Run[] | null): number {
  if (!runs) return 1000;
  const live = runs.some((r) => r.status === "running" || r.status === "queued");
  return live ? 2000 : 15000;
}

/** Go's zero time marshals as year 1; it means "not set", not 1 January 1. */
export function isSetTime(value: string | undefined): boolean {
  return !!value && !value.startsWith("0001-01-01");
}

/** A card's headline: its title if it has one, else the first line of the prompt. */
export function cardTitle(run: Run): string {
  if (run.title && run.title.trim()) return run.title.trim();
  const firstLine = run.prompt.split("\n").find((l) => l.trim()) ?? run.prompt;
  return firstLine.length > 90 ? `${firstLine.slice(0, 90)}…` : firstLine;
}

/** mm:ss for the running card's clock. */
export function elapsedLabel(from: string | undefined, now: number): string {
  if (!isSetTime(from)) return "";
  const started = Date.parse(from as string);
  if (Number.isNaN(started)) return "";
  const seconds = Math.max(0, Math.floor((now - started) / 1000));
  const mm = String(Math.floor(seconds / 60)).padStart(2, "0");
  const ss = String(seconds % 60).padStart(2, "0");
  return `${mm}:${ss}`;
}

/**
 * How long ago, in words. Shared because three surfaces show it and a card
 * that says "5 dk önce" next to one that says "az önce" for the same instant
 * reads as a bug.
 */
export function formatRelativeTime(iso: string, now = Date.now()): string {
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return iso;
  const minutes = Math.round((now - then) / 60000);
  if (minutes < 1) return "az önce";
  if (minutes < 60) return `${minutes} dk önce`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours} sa önce`;
  return `${Math.round(hours / 24)} gün önce`;
}
