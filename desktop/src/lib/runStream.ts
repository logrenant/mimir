import {
  api,
  endpoint,
  isTerminal,
  isTerminalStatus,
  wsURL,
  type RunEvent,
} from "./daemon";

/**
 * The rendered shape of a run: what the socket's flat event stream means once
 * it is folded up.
 *
 * Deltas are accumulated here rather than in React state directly, because the
 * daemon sends the *new suffix only* (internal/events: "the new suffix only,
 * never the accumulated message") — appending is the contract, not an
 * optimisation.
 */
export type ToolCard = {
  callID: string;
  name: string;
  args?: unknown;
  risk?: "read" | "write" | "exec";
  ok?: boolean;
  output?: string;
};

export type RunView = {
  text: string;
  reasoning: string;
  /** The CLI's own stderr, which is where "run `claude login`" is written. */
  stderr: string;
  tools: ToolCard[];
  lastSeq: number;
  finished: boolean;
  failed: boolean;
  stopped: boolean;
  error?: string;
  costUSD?: number;
  numTurns?: number;
  rateLimited?: number;
};

export function emptyRun(): RunView {
  return {
    text: "",
    reasoning: "",
    stderr: "",
    tools: [],
    lastSeq: 0,
    finished: false,
    failed: false,
    stopped: false,
  };
}

/**
 * Folds one event into the view.
 *
 * Out-of-order and duplicate events are dropped on `seq`. The server already
 * stitches its transcript replay to the live bus on that field, but a client
 * that trusts arrival order would still double-render anything the server
 * re-sent after a gap — and a duplicated delta is a corrupted message, not a
 * cosmetic glitch.
 */
export function reduceRun(view: RunView, event: RunEvent): RunView {
  if (event.seq <= view.lastSeq) return view;

  const next: RunView = { ...view, lastSeq: event.seq, tools: view.tools };

  switch (event.kind) {
    case "text.delta":
      next.text = view.text + (event.text ?? "");
      break;
    case "reasoning.delta":
      next.reasoning = view.reasoning + (event.text ?? "");
      break;
    case "tool.call":
      next.tools = [
        ...view.tools,
        {
          callID: event.call_id ?? `seq-${event.seq}`,
          name: event.tool_name ?? "tool",
          args: event.args,
          risk: event.risk,
        },
      ];
      break;
    case "tool.result": {
      // Matched by call id, not by position: results do not necessarily arrive
      // in the order the calls were made.
      const idx = view.tools.findIndex((t) => t.callID === event.call_id);
      if (idx >= 0) {
        next.tools = view.tools.map((t, i) =>
          i === idx ? { ...t, ok: event.ok, output: event.output } : t,
        );
      } else {
        next.tools = [
          ...view.tools,
          {
            callID: event.call_id ?? `seq-${event.seq}`,
            name: event.tool_name ?? "tool",
            ok: event.ok,
            output: event.output,
          },
        ];
      }
      break;
    }
    case "stderr":
      next.stderr = view.stderr + (view.stderr ? "\n" : "") + (event.text ?? "");
      break;
    case "rate_limit":
      next.rateLimited = event.utilization;
      break;
    case "run.completed":
      next.finished = true;
      next.costUSD = event.cost_usd;
      next.numTurns = event.num_turns;
      break;
    case "run.failed":
      next.finished = true;
      next.failed = true;
      next.error = event.error;
      break;
    case "run.stopped":
      // Not a failure: nobody should be asked to debug a cancellation.
      next.finished = true;
      next.stopped = true;
      break;
    default:
      break;
  }

  return next;
}

/**
 * Opens the run socket, and keeps watching if it drops.
 *
 * The token rides `Sec-WebSocket-Protocol` because the browser WebSocket API
 * cannot set headers and the daemon refuses it in a query string. Returns a
 * closer; call it on unmount.
 *
 * Two failures used to be invisible here, and both left a panel reading
 * "running" over nothing at all:
 *
 *  - a browser fires `error` and then `close` on a socket that never connected,
 *    so the reason set by the first was erased by the second before anything
 *    could render it. The first non-empty reason is the one that survives now.
 *  - a socket that ended without a terminal event had no recovery path. It now
 *    falls back to polling GET /coding-tasks/{id}, so a run that finished while
 *    the connection was gone is still reported as finished.
 */
export async function openRunStream(
  runID: string,
  onEvent: (event: RunEvent) => void,
  onClose: (reason?: string) => void,
): Promise<() => void> {
  let disposed = false;
  let notified = false;
  let sawTerminal = false;
  let reason: string | undefined;
  let socket: WebSocket | null = null;
  let pollTimer: ReturnType<typeof setTimeout> | null = null;

  // Told once, and told immediately: a watcher that has lost its stream should
  // say so while it recovers, not after.
  const notify = () => {
    if (disposed || notified) return;
    notified = true;
    onClose(reason);
  };

  // The socket is the fast path, not the only one. If it ends without telling
  // us how the run turned out, the daemon still knows — so ask it, backing off
  // rather than hammering a daemon that may itself be restarting.
  const pollUntilTerminal = (delay: number) => {
    if (disposed || sawTerminal) return;
    pollTimer = setTimeout(() => {
      void api
        .getCodingTask(runID)
        .then((run) => {
          if (disposed || sawTerminal) return;
          if (!isTerminalStatus(run.status)) {
            pollUntilTerminal(Math.min(delay * 2, 60_000));
            return;
          }
          sawTerminal = true;
          onEvent({
            kind:
              run.status === "completed"
                ? "run.completed"
                : run.status === "stopped"
                  ? "run.stopped"
                  : "run.failed",
            run_id: run.id,
            // Past anything the socket delivered, so the reducer's replay
            // guard cannot drop the one event that ends the view.
            seq: Number.MAX_SAFE_INTEGER,
            at: run.ended_at ?? new Date().toISOString(),
            error: run.error,
            cost_usd: run.cost_usd,
            num_turns: run.num_turns,
          });
        })
        .catch(() => pollUntilTerminal(Math.min(delay * 2, 60_000)));
    }, delay);
  };

  const ep = await endpoint();
  const { url, protocol } = wsURL(runID, ep);
  socket = new WebSocket(url, protocol);

  socket.onmessage = (message) => {
    try {
      const event = JSON.parse(String(message.data)) as RunEvent;
      onEvent(event);
      if (isTerminal(event)) {
        sawTerminal = true;
        socket?.close();
      }
    } catch {
      /* a frame we cannot parse is dropped: the transcript remains the record */
    }
  };
  socket.onerror = () => {
    reason ??= "the run stream connection failed";
  };
  socket.onclose = () => {
    if (!sawTerminal) {
      // Ended without an outcome: say so now, and keep asking the daemon.
      reason ??= "the run stream connection closed before the run finished";
      pollUntilTerminal(2000);
    }
    notify();
  };

  return () => {
    if (pollTimer) clearTimeout(pollTimer);
    disposed = true;
    if (socket) {
      socket.onclose = null;
      socket.onerror = null;
      socket.close();
    }
  };
}
