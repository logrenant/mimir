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
import { api, DaemonError, type Run } from "../lib/daemon";
import { pollInterval, isSetTime } from "../lib/board";
import { useTerminals } from "./TerminalsProvider";

/**
 * One poll loop for every screen that shows runs.
 *
 * The daemon has no cross-project run route — `internal/store` only indexes by
 * project — so a board is one `GET /coding-tasks` per registered project. That
 * fan-out is affordable once and wasteful twice, and the dashboard and the
 * board want exactly the same array. So it lives here, above both.
 *
 * Mounted inside `TerminalsProvider` on purpose: every refresh is also what
 * tells the terminals which sessions are still alive, and which running jobs
 * have no terminal yet.
 */

export type BoardRun = Run & { projectName: string };

type RunsAPI = {
  runs: BoardRun[] | null;
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
      const { projects } = await api.listProjects();
      const perProject = await Promise.all(
        projects.map(async (project) => {
          const { runs: projectRuns } = await api.listCodingTasks(project.id);
          return projectRuns.map((run) => ({ ...run, projectName: project.display_name }));
        }),
      );
      const merged = perProject.flat().sort((a, b) => runTime(b).localeCompare(runTime(a)));
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
    () => ({ runs, error, loading, refresh: () => void refresh() }),
    [runs, error, loading, refresh],
  );

  return <RunsContext.Provider value={value}>{children}</RunsContext.Provider>;
}
