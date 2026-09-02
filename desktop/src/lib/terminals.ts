import type { Run, RunEvent, RunStatus } from "./daemon";

/**
 * The terminal sessions: one per run the operator has watched.
 *
 * A pure store with a reducer, kept out of React so the rules — what a line
 * looks like, how much scrollback survives, when a session is over — can be
 * tested without rendering anything (desktop/AGENTS.md).
 *
 * The lines are synthesised from typed events rather than captured from a
 * terminal. There is no PTY: the daemon spawns `claude` with pipes and parses
 * its stream-json, so what a "terminal" can honestly show here is that stream
 * plus the CLI's stderr — which is exactly what the operator needs to tell a
 * broken run from a thinking one.
 */

export type LineKind =
  | "meta"
  | "text"
  | "reasoning"
  | "tool"
  | "result"
  | "stderr"
  | "ok"
  | "bad";

export type TerminalLine = {
  seq: number;
  kind: LineKind;
  text: string;
};

export type Session = {
  runID: string;
  projectID: string;
  title: string;
  status: RunStatus;
  lines: TerminalLine[];
  lastSeq: number;
  /** Set when the socket closed for a reason worth showing. */
  closedReason: string | null;
};

/** Scrollback ceiling. A long run is thousands of lines; the tail is the part
 * anyone reads, and an unbounded array is a leak with a nice name. */
export const MAX_LINES = 5000;

export function newSession(run: Run): Session {
  return {
    runID: run.id,
    projectID: run.project_id,
    title: run.title?.trim() || run.prompt.split("\n")[0] || run.id,
    status: run.status,
    lines: [],
    lastSeq: 0,
    closedReason: null,
  };
}

function truncate(value: string, max = 160): string {
  const flat = value.replace(/\s+/g, " ").trim();
  return flat.length > max ? `${flat.slice(0, max)}…` : flat;
}

function describeArgs(args: unknown): string {
  if (args === undefined || args === null) return "";
  if (typeof args === "string") return truncate(args);
  if (typeof args !== "object") return truncate(String(args));

  const record = args as Record<string, unknown>;
  // The one field that says what a call is actually doing, in the order a
  // reader would look for it.
  for (const key of ["command", "file_path", "path", "pattern", "query", "url", "description"]) {
    const value = record[key];
    if (typeof value === "string" && value.trim()) return truncate(value);
  }
  return truncate(JSON.stringify(record));
}

/**
 * One event as a terminal line, or null for events that add nothing to read.
 *
 * Text and reasoning deltas are suffixes by contract, so they are appended to
 * the previous line of the same kind rather than starting a new one — see
 * appendEvent.
 */
export function formatEventLine(event: RunEvent): TerminalLine | null {
  switch (event.kind) {
    case "run.started":
      return {
        seq: event.seq,
        kind: "meta",
        text: `▶ run started · ${event.model ?? "model"} · session ${event.session_id ?? "?"}`,
      };
    case "text.delta":
      return { seq: event.seq, kind: "text", text: event.text ?? "" };
    case "reasoning.delta":
      return { seq: event.seq, kind: "reasoning", text: event.text ?? "" };
    case "tool.call": {
      const detail = describeArgs(event.args);
      const risk = event.risk ? ` [${event.risk}]` : "";
      return {
        seq: event.seq,
        kind: "tool",
        text: `● ${event.tool_name ?? "tool"}${risk}${detail ? ` ${detail}` : ""}`,
      };
    }
    case "tool.result":
      return {
        seq: event.seq,
        kind: "result",
        text: `${event.ok === false ? "✗" : "→"} ${event.tool_name ?? "tool"} ${truncate(event.output ?? "", 200)}`,
      };
    case "stderr":
      return { seq: event.seq, kind: "stderr", text: `! ${event.text ?? ""}` };
    case "rate_limit":
      return {
        seq: event.seq,
        kind: "meta",
        text: `⚠ rate limit ${Math.round((event.utilization ?? 0) * 100)}% used`,
      };
    case "run.completed":
      return {
        seq: event.seq,
        kind: "ok",
        text: `✓ completed · ${event.num_turns ?? 0} turns · $${(event.cost_usd ?? 0).toFixed(4)}`,
      };
    case "run.stopped":
      return { seq: event.seq, kind: "bad", text: "✗ stopped by the operator" };
    case "run.failed":
      return { seq: event.seq, kind: "bad", text: `✗ failed · ${event.error ?? "no reason given"}` };
    default:
      return null;
  }
}

/** Statuses a terminal line implies, so a session's badge does not need a poll. */
function statusFromEvent(event: RunEvent, current: RunStatus): RunStatus {
  switch (event.kind) {
    case "run.started":
      return "running";
    case "run.completed":
      return "completed";
    case "run.failed":
      return "failed";
    case "run.stopped":
      return "stopped";
    default:
      return current;
  }
}

/**
 * Folds one event into a session.
 *
 * Replayed events are dropped by sequence number, the same guard runStream's
 * reducer uses: the socket replays the transcript on connect, and a reconnect
 * replays it again.
 */
export function appendEvent(session: Session, event: RunEvent): Session {
  if (event.seq <= session.lastSeq) return session;

  const line = formatEventLine(event);
  if (!line) {
    return { ...session, lastSeq: event.seq, status: statusFromEvent(event, session.status) };
  }

  let lines = session.lines;
  const last = lines[lines.length - 1];
  const isDelta = line.kind === "text" || line.kind === "reasoning";

  if (isDelta && last && last.kind === line.kind) {
    // A delta is the new suffix of the same message, not a new line.
    lines = [...lines.slice(0, -1), { ...last, seq: line.seq, text: last.text + line.text }];
  } else {
    lines = [...lines, line];
  }
  if (lines.length > MAX_LINES) lines = lines.slice(lines.length - MAX_LINES);

  return {
    ...session,
    lines,
    lastSeq: event.seq,
    status: statusFromEvent(event, session.status),
  };
}

/** The whole buffer as text, for "copy all". */
export function sessionText(session: Session): string {
  return session.lines.map((l) => l.text).join("\n");
}

export type SessionMap = Record<string, Session>;

/** Opens a session, or leaves an existing one alone so its scrollback survives. */
export function openSession(sessions: SessionMap, run: Run): SessionMap {
  if (sessions[run.id]) {
    return { ...sessions, [run.id]: { ...sessions[run.id], status: run.status } };
  }
  return { ...sessions, [run.id]: newSession(run) };
}

export function closeSession(sessions: SessionMap, runID: string): SessionMap {
  const next = { ...sessions };
  delete next[runID];
  return next;
}

/** Newest first, so the tab bar puts what just started nearest to hand. */
export function orderedSessions(sessions: SessionMap): Session[] {
  const live = (s: Session) => (s.status === "running" || s.status === "queued" ? 0 : 1);
  return Object.values(sessions).sort((a, b) => live(a) - live(b));
}

/**
 * Past runs worth offering as a terminal, newest first.
 *
 * A finished run still has its transcript, and the socket replays it — so
 * "recents" is not a second store, it is the board's own list minus what is
 * already open. Runs that never started are excluded: a backlog card has
 * nothing to replay, and offering one would open an empty console.
 */
export function recentRuns(runs: Run[], open: SessionMap, limit = 40): Run[] {
  return runs
    .filter((run) => !open[run.id])
    .filter((run) => run.status !== "backlog" && run.status !== "queued")
    .slice(0, limit);
}

/**
 * The runs that should get a terminal without anyone asking for one.
 *
 * "Every running job opens a terminal" only held for jobs the operator had
 * clicked; a run released from the queue, or started from the menu bar, opened
 * nothing until somebody went looking. This closes that gap from the poll loop.
 *
 * Only `running`. Adopting `queued` would open a console per released card and
 * fill the dashboard with panels that have nothing to print — the queue is a
 * list on that screen, not a terminal.
 *
 * `dismissed` is what the operator closed by hand. Reopening it on the next
 * poll would make the close button do nothing, which is worse than missing a
 * terminal.
 */
export function runsToAdopt(runs: Run[], open: SessionMap, dismissed: Set<string>): Run[] {
  return runs.filter(
    (run) => run.status === "running" && !open[run.id] && !dismissed.has(run.id),
  );
}

/**
 * Ids in `dismissed` whose run is over.
 *
 * A dismissal is about this run, not about this id forever — and since ids are
 * never reused, keeping them would only grow a set nothing reads.
 */
export function staleDismissals(runs: Run[], dismissed: Set<string>): string[] {
  const live = new Set(
    runs.filter((r) => r.status === "running" || r.status === "queued").map((r) => r.id),
  );
  return [...dismissed].filter((id) => !live.has(id));
}

/** The last n lines, for a panel too small to show a scrollback. */
export function tailLines(session: Session, n: number): TerminalLine[] {
  return n >= session.lines.length ? session.lines : session.lines.slice(session.lines.length - n);
}
