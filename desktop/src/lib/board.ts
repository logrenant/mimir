import type { LimitReport, Run, RunStatus } from "./daemon";

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
  {
    id: "failed",
    label: "Failed",
    statuses: ["failed", "stopped"],
    droppable: false,
  },
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
  grouped.queued.sort((a, b) =>
    (a.queued_at ?? "").localeCompare(b.queued_at ?? ""),
  );
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
    return {
      allowed: false,
      reason:
        "Running kolonuna sürüklenmez — Queued'a bırakın, sıra gelince başlar.",
    };
  }
  if (to === "done" || to === "failed") {
    return {
      allowed: false,
      reason: "Bir çalışmanın sonucunu elle yazamazsınız.",
    };
  }
  if (from === "failed" && to === "backlog") {
    return {
      allowed: false,
      reason:
        "Yarım kalmış işi geri almak diye bir şey yok — Queued'a bırakın, kaldığı yerden devam eder.",
    };
  }
  return {
    allowed: false,
    reason: "Tamamlanmış bir kartın yapacak işi kalmadı — yeni bir task açın.",
  };
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

/**
 * What a card offers, given what it is and what runs it.
 *
 * The agent matters for exactly one thing: "continue". Resuming means handing
 * `claude --resume` a session id, and a sub-agent that spends no CLI session
 * has nothing to resume — offering the button would promise something the
 * daemon cannot do. Everything else is a property of the status alone.
 *
 * An absent agent is the coding one, because that is what every card written
 * before sub-agents existed was.
 */
export function actionsFor(status: RunStatus, agent?: string): CardAction[] {
  const actions = actionsForStatus(status);
  if (resumable(agent)) return actions;
  return actions.filter((a) => a !== "continue");
}

/**
 * Whether this sub-agent's runs leave a session behind.
 *
 * A list rather than a flag on the card, because it is a property of the
 * machinery: it is the claude executor that resumes, and which agents use it
 * is decided in internal/agents, not per run.
 */
function resumable(agent?: string): boolean {
  return agent === undefined || agent === "" || CLAUDE_AGENTS.has(agent);
}

const CLAUDE_AGENTS = new Set(["coding", "review", "marketing", "graph"]);

function actionsForStatus(status: RunStatus): CardAction[] {
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
  return (
    status === "backlog" ||
    status === "queued" ||
    status === "failed" ||
    status === "stopped"
  );
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
  const live = runs.some(
    (r) => r.status === "running" || r.status === "queued",
  );
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

/**
 * What a lead-gen card is actually searching for.
 *
 * `params` is an opaque JSON string on the wire on purpose — the daemon's
 * handler is a door, not a floor, and validating an executor's input there
 * would put that executor's knowledge in the HTTP layer. Reading it here is
 * the same bargain from the other side: the board understands the one agent
 * whose card body it draws, and anything it cannot parse simply does not draw.
 */
export type LeadgenParams = { query?: string; region?: string };

export function leadgenParams(run: Run): LeadgenParams | null {
  if (run.agent !== "leadgen" || !run.params) return null;
  try {
    const parsed: unknown = JSON.parse(run.params);
    if (!parsed || typeof parsed !== "object") return null;
    const { query, region } = parsed as LeadgenParams;
    if (typeof query !== "string" && typeof region !== "string") return null;
    return { query, region };
  } catch {
    // A card whose params will not parse is a card the daemon is about to
    // refuse with a reason. Drawing nothing here is honest; guessing is not.
    return null;
  }
}

/** The one-line summary of what a lead-gen card will search. */
export function leadgenSummary(run: Run): string {
  const params = leadgenParams(run);
  if (!params) return "";
  const region = params.region?.trim();
  const query = params.query?.trim();
  if (region && query && region !== query) return `${region} · ${query}`;
  return region || query || "";
}

// --- katalog kartı (task-88) -------------------------------------------------

/**
 * What a catalog card is working on.
 *
 * The same bargain `leadgenParams` states: `params` is opaque on the wire
 * because the daemon's handler is a door, and the board understands the few
 * agents whose card bodies it draws. Anything it cannot parse simply does not
 * draw — a card whose params will not decode is one the daemon is about to
 * refuse with a reason, and guessing at it here would put a second, wronger
 * reason on the same card.
 */
export type CatalogParams = {
  import_id?: string;
  product_ids?: string[];
  fields?: string[];
  research?: boolean;
  lang?: string;
  /**
   * Which model this card spends. Absent means the daemon's own saved choice,
   * read when the pass runs — so a card nobody has touched follows a setting
   * changed tomorrow, and a card that names a pair keeps it.
   */
  provider?: string;
  model?: string;
};

export function catalogParams(run: Run): CatalogParams | null {
  if (run.agent !== "catalog" || !run.params) return null;
  try {
    const parsed: unknown = JSON.parse(run.params);
    if (!parsed || typeof parsed !== "object") return null;
    const p = parsed as CatalogParams;
    if (typeof p.import_id !== "string" || !Array.isArray(p.product_ids)) {
      return null;
    }
    return p;
  } catch {
    return null;
  }
}

/**
 * The one-line summary of what a catalog card will rewrite.
 *
 * Read from `params`, never from a second request. task-80 removed the board's
 * 1+N fan-out, and a card body that fetched its own import would put it back
 * one card at a time.
 */
export function catalogSummary(run: Run): string {
  const p = catalogParams(run);
  if (!p) return "";
  const n = p.product_ids?.length ?? 0;
  const parts = [`${n} ürün`];
  if (p.research === false) parts.push("araştırmasız");
  if (p.fields?.length) parts.push(p.fields.length + " alan");
  return parts.join(" · ");
}

/**
 * The model a catalog card will spend, as the card itself says it.
 *
 * Read from `params` rather than from `run.model`, and the difference is the
 * bug this exists for: the column is written when the card is created and
 * rewritten when it ends, so a card re-run after its model was changed shows
 * the *previous* pass's model until the new one finishes. The params are what
 * the executor will read next time, which is the thing the operator is asking
 * about when they open the card.
 */
export function catalogSelection(run: Run): { provider: string; model: string } {
  const p = catalogParams(run);
  return { provider: p?.provider ?? "", model: p?.model ?? "" };
}

/**
 * A catalog card's params with a different model on them.
 *
 * The whole document, because `PATCH /coding-tasks/{id}` takes params whole:
 * sending back only the two changed keys would drop the product ids the card
 * exists to name. Everything else is carried through untouched, including keys
 * this build has never heard of — a card written by a newer client must not
 * lose half of itself to an older one changing its model.
 *
 * An object rather than a string: the route decodes `params` as JSON, while a
 * run carries it back out as text. The asymmetry is easy to get wrong in the
 * component, so it is settled here.
 *
 * Returns null when this is not a card whose params can be read, which is the
 * same refusal `catalogParams` makes and for the same reason — rewriting a
 * document we could not parse would be guessing at somebody's queued work.
 */
export function withCatalogSelection(
  run: Run,
  provider: string,
  model: string,
): CatalogParams | null {
  const p = catalogParams(run);
  if (!p) return null;
  const next: CatalogParams = { ...p };
  if (provider) {
    next.provider = provider;
    next.model = model;
  } else {
    delete next.provider;
    delete next.model;
  }
  return next;
}

/**
 * Which model control a card's panel should draw.
 *
 * Three answers, because there are three kinds of card and they were all being
 * offered the same one — the coding-model dropdown, which for anything but a
 * claude session edits a field its executor never reads. That is the whole of
 * "I changed the model and nothing changed": the control worked, it just was
 * not wired to the pass.
 *
 * - `coding` — a claude session, whose model *is* the row's column and one of
 *   `GET /coding-models`.
 * - `catalog` — a card that carries its own provider/model in its params, and
 *   the one worker card that can be re-pointed from here.
 * - `daemon` — a worker card with no per-card choice (lead-gen). It gets a
 *   sentence naming where that decision lives instead of a control that would
 *   silently do nothing.
 */
export function modelControlFor(run: Run): "coding" | "catalog" | "daemon" {
  if (run.agent === "catalog") return "catalog";
  if (run.agent === "leadgen") return "daemon";
  return "coding";
}

/**
 * Whether this queued card is waiting on a spent model budget rather than on
 * its turn.
 *
 * The distinction is the whole of task-89 seen from here. A parked card and a
 * card that has simply not started yet are both `queued`, and an operator who
 * cannot tell them apart reads an overnight pause as a hang. The answer comes
 * from `GET /coding-tasks/queue/limits` — the daemon's live holds — and never
 * from guessing at the row's error text, which is prose written for a person.
 *
 * A worker-lane card (catalog, lead-gen) waits on the daemon's own model
 * identity, held under `worker-lane`; an account-lane card waits on its
 * credential slot. Reporting one as blocked by the other would send the
 * operator to look at a limit that has no bearing on it.
 */
export const WORKER_SLOT = "worker-lane";

const WORKER_AGENTS = new Set(["catalog", "leadgen"]);

export function heldUntil(run: Run, limits: LimitReport | null): string | null {
  if (run.status !== "queued" || !limits) return null;
  const slot = WORKER_AGENTS.has(run.agent ?? "")
    ? WORKER_SLOT
    : run.account_id || "";
  for (const hold of limits.holds) {
    const id = hold.account_id ?? "";
    if (id === slot && id !== "") return hold.resets_at;
  }
  return null;
}

/** "limit · 19:40'ta sürüyor", or nothing when the card is simply waiting. */
export function heldLabel(run: Run, limits: LimitReport | null): string {
  const at = heldUntil(run, limits);
  if (!at) return "";
  const d = new Date(at);
  if (Number.isNaN(d.getTime())) return "limit";
  return `limit · ${d.toLocaleTimeString("tr-TR", {
    hour: "2-digit",
    minute: "2-digit",
  })}'te sürüyor`;
}

/**
 * The catalog's own view of the board.
 *
 * The Katalog screen queued a card and then said nothing about it: the answer
 * `POST /catalog/rewrite` returns — a `run_id` — was thrown away, and the
 * screen's only feedback on success was the checkboxes emptying. An operator
 * selected one product, pressed the button, and watched nothing happen for six
 * minutes; the pass was running the whole time, on a different screen they had
 * no reason to open, and it then failed for a reason only that screen carried.
 *
 * These read the same `RunsProvider` array the board reads. A screen that shows
 * runs subscribes to that one poll loop; it does not start a timer.
 */
export function catalogRunsFor(runs: Run[] | null, importID: string): Run[] {
  if (!runs || !importID) return [];
  return runs.filter((r) => catalogParams(r)?.import_id === importID);
}

/** Newest first, by whichever timestamp the row actually carries. */
function byRecency(a: Run, b: Run): number {
  const t = (r: Run) =>
    Date.parse(r.ended_at || r.started_at || r.queued_at || r.created_at || "") || 0;
  return t(b) - t(a);
}

/** The pass that is happening now, if one is. */
export function activeCatalogRun(runs: Run[] | null, importID: string): Run | null {
  const live = catalogRunsFor(runs, importID)
    .filter((r) => r.status === "running" || r.status === "queued")
    .sort(byRecency);
  return live[0] ?? null;
}

/** The most recent pass whatever became of it — what "did that work?" reads. */
export function lastCatalogRun(runs: Run[] | null, importID: string): Run | null {
  const all = [...catalogRunsFor(runs, importID)].sort(byRecency);
  return all[0] ?? null;
}

/**
 * One line about a pass, for the screen that started it.
 *
 * A parked card reads as parked through `heldUntil`, the same way it does on
 * the board — never through a guess at `run.error`, which for a parked row is
 * the provider's own sentence about a budget rather than a statement about
 * this card.
 */
export function catalogRunLine(run: Run | null, limits: LimitReport | null): string {
  if (!run) return "";
  const what = catalogSummary(run) || "yeniden yazım";
  switch (run.status) {
    case "running":
      return `${what} · yazılıyor`;
    case "queued": {
      const held = heldLabel(run, limits);
      return held ? `${what} · ${held}` : `${what} · sırada`;
    }
    case "backlog":
      return `${what} · beklemede`;
    case "completed":
      return `${what} · bitti`;
    case "stopped":
      return `${what} · durduruldu`;
    case "failed":
      return `${what} · başarısız`;
  }
}
