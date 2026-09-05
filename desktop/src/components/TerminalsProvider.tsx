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
import { api, DaemonError, type Run, type RunEvent } from "../lib/daemon";
import { openRunStream } from "../lib/runStream";
import {
  appendEvent,
  closeSession as dropSession,
  openSession,
  orderedSessions,
  runsToAdopt,
  staleDismissals,
  type Session,
  type SessionMap,
} from "../lib/terminals";

/**
 * Holds every open terminal, above the screen that draws them.
 *
 * Mounted above `Dashboard` on purpose: a session's socket must survive the
 * operator looking at the board, so the subscription cannot live in the
 * Terminals screen's own lifetime. Switching away from a running job and back
 * has to show what happened while you were gone, and the daemon's transcript
 * replay only covers what it can see — not what a component unmounted through.
 *
 * All the rules live in lib/terminals.ts; this is the wiring.
 */

type TerminalsAPI = {
  sessions: Session[];
  activeID: string | null;
  setActive: (runID: string) => void;
  /** Opens (or re-focuses) a run's terminal and starts watching it. */
  open: (run: Run) => void;
  close: (runID: string) => void;
  stop: (runID: string) => Promise<void>;
  /**
   * Puts a finished run back in the queue from the terminal it failed in.
   *
   * fresh = false resumes the CLI session, which is what a run cut off by a
   * dropped connection or a conflict wants: it carries on rather than redoing
   * the half it finished. fresh = true throws that session away.
   */
  retry: (runID: string, fresh: boolean) => Promise<void>;
  /** Reconciles a session's badge with what the board just fetched. */
  sync: (runs: Run[]) => void;
  /**
   * Opens a terminal for every running job that has none.
   *
   * "Every running job gets a terminal" used to hold only for the jobs the
   * operator had clicked: a card released from the queue, or a task started
   * from the menu bar, opened nothing until somebody went looking for it.
   * Called from the poll loop, which is the only thing that sees those.
   */
  adopt: (runs: Run[]) => void;
};

const TerminalsContext = createContext<TerminalsAPI | null>(null);

export function useTerminals(): TerminalsAPI {
  const ctx = useContext(TerminalsContext);
  if (!ctx) throw new Error("useTerminals must be used inside <TerminalsProvider>");
  return ctx;
}

export function TerminalsProvider({ children }: { children: ReactNode }) {
  const [sessions, setSessions] = useState<SessionMap>({});
  const [activeID, setActiveID] = useState<string | null>(null);

  // One closer per watched run. A ref, not state: closing a socket is not
  // something the tree should re-render for.
  const closers = useRef(new Map<string, () => void>());

  // Runs the operator closed by hand. Without this, the next poll would reopen
  // the tab and the close button would appear not to work.
  const dismissed = useRef(new Set<string>());

  const patch = useCallback((runID: string, fn: (s: Session) => Session) => {
    setSessions((current) => {
      const session = current[runID];
      if (!session) return current;
      return { ...current, [runID]: fn(session) };
    });
  }, []);

  const watch = useCallback(
    (runID: string) => {
      if (closers.current.has(runID)) return;
      // Registered before the socket exists so a second open cannot race a
      // second subscription onto the same run.
      closers.current.set(runID, () => {});

      const onEvent = (event: RunEvent) => patch(runID, (s) => appendEvent(s, event));
      const onClose = (reason?: string) =>
        patch(runID, (s) => ({ ...s, closedReason: reason ?? null }));

      void openRunStream(runID, onEvent, onClose)
        .then((closer) => {
          if (!closers.current.has(runID)) {
            closer(); // closed while we were connecting
            return;
          }
          closers.current.set(runID, closer);
        })
        .catch((err: unknown) => {
          // The rejection used to be dropped, which left a terminal that
          // showed nothing and explained nothing.
          closers.current.delete(runID);
          patch(runID, (s) => ({
            ...s,
            closedReason: err instanceof DaemonError ? err.message : String(err),
          }));
        });
    },
    [patch],
  );

  const open = useCallback(
    (run: Run) => {
      setSessions((current) => openSession(current, run));
      setActiveID(run.id);
      watch(run.id);
    },
    [watch],
  );

  const close = useCallback((runID: string) => {
    dismissed.current.add(runID);
    closers.current.get(runID)?.();
    closers.current.delete(runID);
    setSessions((current) => dropSession(current, runID));
    setActiveID((id) => (id === runID ? null : id));
  }, []);

  const stop = useCallback(
    async (runID: string) => {
      try {
        await api.stopCodingTask(runID);
      } catch (err) {
        patch(runID, (s) => ({
          ...s,
          closedReason: err instanceof DaemonError ? err.message : String(err),
        }));
      }
    },
    [patch],
  );

  const retry = useCallback(
    async (runID: string, fresh: boolean) => {
      try {
        const run = await api.retryCodingTask(runID, fresh);
        // The tab is already open and keeps its scrollback; only the badge and
        // the session it may resume next are out of date.
        patch(runID, (s) => ({
          ...s,
          status: run.status,
          sessionID: run.session_id || (fresh ? "" : s.sessionID),
          closedReason: null,
        }));
        watch(runID);
      } catch (err) {
        patch(runID, (s) => ({
          ...s,
          closedReason: err instanceof DaemonError ? err.message : String(err),
        }));
      }
    },
    [patch, watch],
  );

  const adopt = useCallback(
    (runs: Run[]) => {
      // A dismissal is about one run, not about that id forever, so it is
      // dropped as soon as the run it referred to is over.
      for (const id of staleDismissals(runs, dismissed.current)) dismissed.current.delete(id);

      setSessions((current) => {
        const missing = runsToAdopt(runs, current, dismissed.current);
        if (missing.length === 0) return current;
        let next = current;
        for (const run of missing) next = openSession(next, run);
        // Watching is a side effect, but it is idempotent and keyed by run id,
        // so doing it from the updater costs nothing if React calls it twice.
        for (const run of missing) watch(run.id);
        return next;
      });
    },
    [watch],
  );

  // The board polls anyway; letting its answer correct the tabs keeps a
  // session's badge honest even when its socket died without saying so.
  const sync = useCallback((runs: Run[]) => {
    setSessions((current) => {
      let changed = false;
      const next = { ...current };
      for (const run of runs) {
        const session = next[run.id];
        if (!session) continue;
        // The session id is corrected the same way and for the same reason as
        // the badge: a run that ended while nothing was watching it has one the
        // socket never delivered, and "devam et" is drawn from it.
        const sessionID = run.session_id || session.sessionID;
        if (session.status !== run.status || session.sessionID !== sessionID) {
          next[run.id] = { ...session, status: run.status, sessionID };
          changed = true;
        }
      }
      return changed ? next : current;
    });
  }, []);

  // Sockets are the provider's to own, so they are the provider's to release.
  useEffect(() => {
    const open = closers.current;
    return () => {
      for (const closer of open.values()) closer();
      open.clear();
    };
  }, []);

  const ordered = useMemo(() => orderedSessions(sessions), [sessions]);

  // A closed tab, or the first one ever opened, needs somewhere to land.
  useEffect(() => {
    if (activeID && sessions[activeID]) return;
    setActiveID(ordered.length > 0 ? ordered[0].runID : null);
  }, [activeID, ordered, sessions]);

  const value = useMemo<TerminalsAPI>(
    () => ({ sessions: ordered, activeID, setActive: setActiveID, open, close, stop, retry, sync, adopt }),
    [ordered, activeID, open, close, stop, retry, sync, adopt],
  );

  return <TerminalsContext.Provider value={value}>{children}</TerminalsContext.Provider>;
}
