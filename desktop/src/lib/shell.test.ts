import { describe, expect, test } from "vitest";

import { HANDSHAKE_QUIET_MS, shellPhase } from "./shell";

describe("shellPhase", () => {
  // The whole point of this pass: a daemon that answers on the loopback in
  // about two hundred milliseconds must not paint a full-screen splash on the
  // way past. Nothing is shown until silence stops being honest.
  test("a fast handshake shows nothing at all", () => {
    expect(shellPhase({ state: "starting" }, 0)).toBe("quiet");
    expect(shellPhase({ state: "starting" }, HANDSHAKE_QUIET_MS - 1)).toBe("quiet");
  });

  test("a slow one says so, once it is slow enough to wonder about", () => {
    expect(shellPhase({ state: "starting" }, HANDSHAKE_QUIET_MS)).toBe("waiting");
    expect(shellPhase({ state: "starting" }, 5000)).toBe("waiting");
  });

  // The gate's original argument, unchanged: a window that renders a project
  // picker over a transport that does not work is a window that lies.
  test("a failure is reported immediately, not after the grace period", () => {
    expect(shellPhase({ state: "failed", message: "boom" }, 0)).toBe("failed");
    expect(shellPhase({ state: "failed", message: "boom" }, 9999)).toBe("failed");
  });

  test("ready is ready whatever the clock says", () => {
    const ready = {
      state: "ready",
      base_url: "http://127.0.0.1:41999",
      token: "t",
    } as const;
    expect(shellPhase(ready, 0)).toBe("ready");
    expect(shellPhase(ready, 9999)).toBe("ready");
  });
});
