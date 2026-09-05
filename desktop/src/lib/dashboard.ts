import type { Account, Diagnostics, DiagnosticsDependency, Run } from "./daemon";
import { isSetTime } from "./board";

/**
 * What the home screen counts, decided here rather than in the component.
 *
 * Same rule as lib/board.ts: the screen draws, this file decides. A count that
 * is wrong is a bug worth a test, and a bug in a `.map()` inside a `<div>` is
 * not testable.
 */

export type Summary = {
  running: number;
  queued: number;
  backlog: number;
  /** Finished today, however they finished. */
  finishedToday: number;
  failedToday: number;
  /** Spend on runs that ended today, in USD. */
  spendToday: number;
  total: number;
};

const EMPTY: Summary = {
  running: 0,
  queued: 0,
  backlog: 0,
  finishedToday: 0,
  failedToday: 0,
  spendToday: 0,
  total: 0,
};

/** Local midnight, because "today" is the operator's day, not UTC's. */
function startOfToday(now: number): number {
  const d = new Date(now);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
}

function endedToday(run: Run, since: number): boolean {
  if (!isSetTime(run.ended_at)) return false;
  const at = new Date(run.ended_at as string).getTime();
  return !Number.isNaN(at) && at >= since;
}

export function summarize(runs: Run[] | null, now = Date.now()): Summary {
  if (!runs) return EMPTY;
  const since = startOfToday(now);
  const out: Summary = { ...EMPTY, total: runs.length };

  for (const run of runs) {
    if (run.status === "running") out.running++;
    else if (run.status === "queued") out.queued++;
    else if (run.status === "backlog") out.backlog++;

    if (!endedToday(run, since)) continue;
    out.finishedToday++;
    if (run.status === "failed") out.failedToday++;
    // A stopped run still spent what it spent before the signal.
    out.spendToday += run.cost_usd ?? 0;
  }
  return out;
}

/** Two decimals is the resolution of the number, not of the currency. */
export function formatSpend(usd: number): string {
  if (usd <= 0) return "$0.00";
  if (usd < 0.01) return "<$0.01";
  return `$${usd.toFixed(2)}`;
}

export type DepRow = {
  name: string;
  ok: boolean;
  optional: boolean;
  /** The daemon's own sentence. It already names the command that fixes it. */
  detail: string;
};

/**
 * The order is fixed rather than derived from the object: a health strip whose
 * rows move between polls is unreadable, and `crawl4ai` is first because it is
 * the one that has actually been down.
 */
const DEP_ORDER = ["crawl4ai", "claude", "duckduckgo", "maps_scraper"] as const;

const DEP_LABEL: Record<(typeof DEP_ORDER)[number], string> = {
  crawl4ai: "Crawl4AI",
  claude: "claude CLI",
  duckduckgo: "DuckDuckGo",
  maps_scraper: "Maps sidecar",
};

export function dependencyRows(diagnostics: Diagnostics | null): DepRow[] {
  const deps = diagnostics?.dependencies;
  if (!deps) return [];
  const out: DepRow[] = [];
  for (const key of DEP_ORDER) {
    const dep = deps[key] as DiagnosticsDependency | undefined;
    if (!dep) continue;
    out.push({
      name: DEP_LABEL[key],
      ok: dep.ok,
      optional: dep.optional === true,
      detail: dep.detail ?? "",
    });
  }
  return out;
}

/** True when something the daemon needs is down — the strip earns attention. */
export function hasBlockingFault(rows: DepRow[]): boolean {
  return rows.some((row) => !row.ok && !row.optional);
}

export type AccountLoad = {
  /** The run occupying the account, if any. Capacity is one. */
  busyWith: Run | null;
  /** Cards waiting for it to come free. */
  waiting: number;
};

/**
 * Whether the account is busy, and how much is behind it.
 *
 * Every queued card counts, not just the ones naming this account: there is one
 * account, so everything in the queue is waiting for the same identity. Nothing
 * is returned with no account connected — an idle lane that does not exist
 * would read as capacity Mimir does not have.
 */
export function accountLoad(account: Account | null, runs: Run[] | null): AccountLoad | null {
  if (!account) return null;
  const live = runs ?? [];
  return {
    busyWith: live.find((r) => r.status === "running") ?? null,
    waiting: live.filter((r) => r.status === "queued").length,
  };
}

/**
 * Runs worth showing as "just finished", newest first.
 *
 * Ordered by `ended_at` rather than by the board's own ordering: the board
 * sorts by whichever timestamp a card has, which puts a card created an hour
 * ago above a run that finished a minute ago.
 */
export function recentlyFinished<T extends Run>(runs: T[] | null, limit = 6): T[] {
  if (!runs) return [];
  return runs
    .filter((run) => isSetTime(run.ended_at))
    .sort((a, b) => (b.ended_at as string).localeCompare(a.ended_at as string))
    .slice(0, limit);
}
