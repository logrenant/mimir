import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import {
  api,
  DaemonError,
  type LimitReport,
  type Run,
} from "../lib/daemon";
import { pollInterval, isSetTime } from "../lib/board";
import { useTerminals } from "./TerminalsProvider";

/**
 * One poll loop for every screen that shows runs.
 *
 * One request. This used to fan out over the project registry and merge the
 * answers, because the daemon indexed runs only by project and had no
 * cross-project route. It has one now, and needs one: a card on the worker
 * lane belongs to no project at all, so no amount of per-project querying
 * would ever have found it.
 *
 * The project list is still read, for one reason: to put a name on the card.
 * A card with no project shows its sub-agent in that slot instead.
 *
 * Mounted inside `TerminalsProvider` on purpose: every refresh is also what
 * tells the terminals which sessions are still alive, and which running jobs
 * have no terminal yet.
 */

export type BoardRun = Run & { projectName: string };

type RunsAPI = {
  runs: BoardRun[] | null;
  /**
   * What the queue is currently waiting on, and until when.
   *
   * Read here rather than by the board because it belongs to the same poll: a
   * parked card and a card that has simply not started yet are both `queued`,
   * and the only thing that separates them is this. A second loop for it would
   * be a second answer that can disagree with the first about the moment a
   * pause lifted.
   */
  limits: LimitReport | null;
  error: string | null;
  loading: boolean;
  refresh: () => void;
};

const RunsContext = createContext<RunsAPI | null>(null);

export function useRuns(): RunsAPI {
  const ctx = useContext(RunsContext);
  if (!ctx) throw new Error("useRuns must be used inside <RunsProvider>");
  return ctx;
}

/** The most recent timestamp a run actually has — a backlog card has only one. */
export function runTime(run: Run): string {
  for (const value of [run.started_at, run.queued_at, run.created_at]) {
    if (isSetTime(value)) return value as string;
  }
  return "";
}

export function RunsProvider({ children }: { children: ReactNode }) {
  const [runs, setRuns] = useState<BoardRun[] | null>(null);
  const [limits, setLimits] = useState<LimitReport | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const terminals = useTerminals();

  // `adopt` changes identity on every session change, and a refresh callback
  // that changes identity restarts the interval. The ref keeps the loop stable.
  const adopt = useRef(terminals.adopt);
  adopt.current = terminals.adopt;
  const sync = useRef(terminals.sync);
  sync.current = terminals.sync;

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      // Both at once: the cards are the answer and the projects are only a
      // lookup table for the label, so neither has to wait for the other.
      // Three at once, and the third is allowed to fail on its own. A daemon
      // that cannot answer "why is nothing running" can still draw the board,
      // and losing the badge is a smaller loss than losing the cards.
      const [{ runs: all }, { projects }, held] = await Promise.all([
        api.listCodingTasks(),
        api.listProjects(),
        api.queueLimits(20).catch(() => null),
      ]);
      setLimits(held);
      const nameOf = new Map(projects.map((p) => [p.id, p.display_name]));
      // Already ordered by the daemon; sorted again because the two clients
      // must not disagree about what "most recent" means for a backlog card
      // that has only ever had a created_at.
      const merged: BoardRun[] = all
        .map((run) => ({
          ...run,
          projectName: nameOf.get(run.project_id) ?? "",
        }))
        .sort((a, b) => runTime(b).localeCompare(runTime(a)));
      setRuns(merged);
      setError(null);
      // Order matters: correct the badges of sessions that already exist
      // before opening any new ones, so an adopted run is never drawn with a
      // status the poll just superseded.
      sync.current(merged);
      adopt.current(merged);
    } catch (err) {
      setError(err instanceof DaemonError ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, []);

  // The interval follows the board rather than a fixed guess: a board with
  // nothing moving does not need a request every few seconds.
  const interval = pollInterval(runs);
  useEffect(() => {
    void refresh();
    const id = window.setInterval(() => void refresh(), interval);
    return () => window.clearInterval(id);
  }, [refresh, interval]);

  const value = useMemo<RunsAPI>(
    () => ({ runs, limits, error, loading, refresh: () => void refresh() }),
    [runs, limits, error, loading, refresh],
  );

  return <RunsContext.Provider value={value}>{children}</RunsContext.Provider>;
}
