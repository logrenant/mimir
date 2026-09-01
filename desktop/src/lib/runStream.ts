import { endpoint, isTerminal, wsURL, type RunEvent } from "./daemon";

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
  tools: ToolCard[];
  lastSeq: number;
  finished: boolean;
  failed: boolean;
  error?: string;
  costUSD?: number;
  numTurns?: number;
  rateLimited?: number;
};

export function emptyRun(): RunView {
  return { text: "", reasoning: "", tools: [], lastSeq: 0, finished: false, failed: false };
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
    default:
      break;
  }

  return next;
}

/**
 * Opens the run socket.
 *
 * The token rides `Sec-WebSocket-Protocol` because the browser WebSocket API
 * cannot set headers and the daemon refuses it in a query string. Returns a
 * closer; call it on unmount.
 */
export async function openRunStream(
  runID: string,
  onEvent: (event: RunEvent) => void,
  onClose: (reason?: string) => void,
): Promise<() => void> {
  const ep = await endpoint();
  const { url, protocol } = wsURL(runID, ep);
  const socket = new WebSocket(url, protocol);

  socket.onmessage = (message) => {
    try {
      const event = JSON.parse(String(message.data)) as RunEvent;
      onEvent(event);
      if (isTerminal(event)) socket.close();
    } catch {
      /* a frame we cannot parse is dropped: the transcript remains the record */
    }
  };
  socket.onerror = () => onClose("the run stream connection failed");
  socket.onclose = () => onClose();

  return () => {
    socket.onclose = null;
    socket.close();
  };
}
