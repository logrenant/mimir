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
  | { allowed: true; action: "enqueue" | "dequeue" | "retry" }
  | { allowed: false; reason: string };

/**
 * Whether a card may be dragged from one column to another, and what that
 * means.
 *
 * Every move that has a daemon operation behind it is a move: Backlog→Queued
 * releases, Queued→Backlog takes back, and Failed→Queued picks the work up
 * again — the same thing the card's own buttons do, because a board where the
 * buttons can do something the drag cannot is a board that is lying about being
 * a board.
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
  // Dropping a failure back in the queue is a retry, and it continues the
  // session when there is one to continue — same rule as the card's buttons.
  if (from === "failed" && to === "queued") {
    return { allowed: true, action: "retry" };
  }
  if (to === "running") {
    return { allowed: false, reason: "Running kolonuna sürüklenmez — Queued'a bırakın, sıra gelince başlar." };
  }
  if (to === "done" || to === "failed") {
    return { allowed: false, reason: "Bir çalışmanın sonucunu elle yazamazsınız." };
  }
  if (from === "failed" && to === "backlog") {
    return { allowed: false, reason: "Yarım kalmış işi geri almak diye bir şey yok — Queued'a bırakın, kaldığı yerden devam eder." };
  }
  return { allowed: false, reason: "Tamamlanmış bir kartın yapacak işi kalmadı — yeni bir task açın." };
}

/** The actions a card offers, given where it is. */
export type CardAction =
  | "run"
  | "stop"
  | "dequeue"
  | "delete"
  | "terminal"
  | "continue"
  | "retry"
  | "kick"
  | "edit";

export function actionsFor(status: RunStatus): CardAction[] {
  switch (status) {
    case "backlog":
      return ["run", "edit", "delete"];
    // Kick is on every queued card rather than only the stuck ones: whether
    // the queue is moving is a question about the daemon, and a card that
    // offers the answer only once it looks stuck is a card the operator has to
    // wait to be allowed to ask.
    case "queued":
      return ["kick", "edit", "dequeue", "terminal"];
    case "running":
      return ["stop", "terminal"];
    // A run that failed or was stopped is the only one with something left to
    // pick up, and it gets both ways of picking it up. Continue leads because
    // it is the one that does not throw away the work already done.
    case "failed":
    case "stopped":
      return ["continue", "retry", "edit", "terminal", "delete"];
    default:
      return ["terminal", "delete"];
  }
}

/**
 * Whether a card's own text may still be rewritten.
 *
 * The same rule the daemon keeps (store.EditableStatuses), and it is here so
 * the board can offer the button rather than discover the refusal: a run that
 * is spending or has spent tokens keeps the prompt it was given, because that
 * text is the record of what was asked. Everything before that is intent, and
 * intent is editable — including a failed card, which is the one an operator
 * most wants to fix before trying again.
 */
export function canEdit(status: RunStatus): boolean {
  return status === "backlog" || status === "queued" || status === "failed" || status === "stopped";
}

/**
 * Whether a run can be continued rather than started over.
 *
 * A session id is the CLI's, written down when it announced itself. Without one
 * there is nothing for `--resume` to attach to — a run that died before it said
 * anything can only be done again — so the board offers "devam et" against the
 * fact rather than against the status alone.
 */
export function canContinue(run: Run): boolean {
  if (run.status !== "failed" && run.status !== "stopped") return false;
  return !!run.session_id && run.session_id.trim() !== "";
}

/**
 * How long a card may sit in Queued with nothing running before the board says
 * so.
 *
 * The queue is pumped when work is released and when a run frees its slot, so a
 * card queued while no account was connected waits with nothing to move it. A
 * few seconds of that is a dispatcher about to pick it up; half a minute of it,
 * with nothing running, is a queue that has stopped.
 */
export const STALL_AFTER_MS = 30_000;

/** Whether this queued card has been waiting with nothing able to start it. */
export function isStalled(run: Run, runs: Run[] | null, now: number): boolean {
  if (run.status !== "queued") return false;
  if (runs?.some((r) => r.status === "running")) return false;
  if (!isSetTime(run.queued_at)) return true;
  const at = Date.parse(run.queued_at as string);
  if (Number.isNaN(at)) return false;
  return now - at > STALL_AFTER_MS;
}

/**
 * What pressing continue or try-again will actually do right now.
 *
 * Both land the card in Queued. Whether that means "starts now" or "waits"
 * depends on something outside the card — capacity is one run at a time — and
 * the button says which, because a card that jumps to Queued and sits there
 * with no explanation reads as a failure to start.
 */
export function retryOutcome(runs: Run[] | null): "queued" | "starts" {
  if (!runs) return "starts";
  return runs.some((r) => r.status === "running") ? "queued" : "starts";
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
