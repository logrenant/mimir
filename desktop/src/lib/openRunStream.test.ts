import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import type { Run, RunEvent } from "./daemon";

const getCodingTask = vi.fn<(runID: string) => Promise<Run>>();

vi.mock("./daemon", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./daemon")>();
  return {
    ...actual,
    endpoint: async () => ({ base_url: "http://127.0.0.1:1234", token: "t" }),
    api: { ...actual.api, getCodingTask: (id: string) => getCodingTask(id) },
  };
});

const { openRunStream } = await import("./runStream");

/** A WebSocket that never touches the network and can be driven by hand. */
class FakeSocket {
  static last: FakeSocket | null = null;

  onmessage: ((e: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  onclose: (() => void) | null = null;
  closed = false;

  constructor() {
    FakeSocket.last = this;
  }

  close() {
    this.closed = true;
    this.onclose?.();
  }

  deliver(event: Partial<RunEvent> & { kind: RunEvent["kind"]; seq: number }) {
    this.onmessage?.({ data: JSON.stringify({ run_id: "run-1", at: "", ...event }) });
  }
}

beforeEach(() => {
  vi.useFakeTimers();
  getCodingTask.mockReset();
  FakeSocket.last = null;
  (globalThis as { WebSocket: unknown }).WebSocket = FakeSocket;
});

afterEach(() => {
  vi.useRealTimers();
});

describe("openRunStream", () => {
  test("a socket that ends on a terminal event closes cleanly, with no reason to show", async () => {
    const events: RunEvent[] = [];
    const closes: (string | undefined)[] = [];
    await openRunStream("run-1", (e) => events.push(e), (r) => closes.push(r));

    FakeSocket.last?.deliver({ kind: "run.completed", seq: 1, cost_usd: 0.1 });

    expect(events).toHaveLength(1);
    expect(closes).toEqual([undefined]);
    expect(getCodingTask).not.toHaveBeenCalled();
  });

  test("keeps the error's reason, which onclose used to erase", async () => {
    const closes: (string | undefined)[] = [];
    await openRunStream("run-1", () => {}, (r) => closes.push(r));

    // A browser fires error and then close on a socket that never connected.
    // The reason set by the first has to survive the second, or the one
    // message explaining the failure can never render.
    FakeSocket.last?.onerror?.();
    FakeSocket.last?.onclose?.();

    expect(closes).toEqual(["the run stream connection failed"]);
  });

  test("a socket that drops mid-run says so, then asks the daemon how it ended", async () => {
    const events: RunEvent[] = [];
    const closes: (string | undefined)[] = [];
    await openRunStream("run-1", (e) => events.push(e), (r) => closes.push(r));

    getCodingTask
      .mockResolvedValueOnce({ id: "run-1", status: "running" } as Run)
      .mockResolvedValueOnce({ id: "run-1", status: "completed", cost_usd: 0.5, num_turns: 3 } as Run);

    FakeSocket.last?.onclose?.();
    expect(closes[0]).toContain("closed before the run finished");

    await vi.advanceTimersByTimeAsync(2000);
    expect(events).toHaveLength(0); // still running: keep waiting
    await vi.advanceTimersByTimeAsync(4000);

    // The run finished while nothing was watching, and the view is told.
    expect(events).toHaveLength(1);
    expect(events[0].kind).toBe("run.completed");
    expect(events[0].cost_usd).toBe(0.5);
  });

  test("a stopped run comes back as a stop, not as a failure", async () => {
    const events: RunEvent[] = [];
    await openRunStream("run-1", (e) => events.push(e), () => {});

    getCodingTask.mockResolvedValue({ id: "run-1", status: "stopped" } as Run);
    FakeSocket.last?.onclose?.();
    await vi.advanceTimersByTimeAsync(2000);

    expect(events[0].kind).toBe("run.stopped");
  });

  test("the closer stops the polling, so an unmounted panel does not keep asking", async () => {
    const events: RunEvent[] = [];
    const close = await openRunStream("run-1", (e) => events.push(e), () => {});

    getCodingTask.mockResolvedValue({ id: "run-1", status: "completed" } as Run);
    FakeSocket.last?.onclose?.();
    close();

    await vi.advanceTimersByTimeAsync(30_000);
    expect(getCodingTask).not.toHaveBeenCalled();
    expect(events).toHaveLength(0);
  });
});
