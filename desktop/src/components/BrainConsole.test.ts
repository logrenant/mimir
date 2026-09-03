import { describe, expect, it } from "vitest";

import { phaseLabel, statusOf } from "./BrainConsole";
import type { BrainScanStatus } from "../lib/daemon";

function status(over: Partial<BrainScanStatus> = {}): BrainScanStatus {
  return {
    phase: "idle",
    paused: false,
    roots: [],
    provider: "agy",
    model: "gemini-3.8-flash-high",
    project_index: 0,
    project_count: 0,
    remaining: 0,
    skipped_unchanged: 0,
    eligible: 0,
    scanned_session: 0,
    failed_session: 0,
    unreadable_session: 0,
    scanned_total: 0,
    sweeps: 0,
    nodes_total: 0,
    provider_down: false,
    ...over,
  };
}

describe("the scan's sidebar row", () => {
  // The dot is the same vocabulary the run rows use, so one glance down the
  // sidebar answers "is anything working?" without translating two schemes.
  it("maps the scan onto the run status colours", () => {
    expect(statusOf(null)).toBe("queued");
    expect(statusOf(status({ phase: "scanning" }))).toBe("running");
    expect(statusOf(status({ phase: "discovering" }))).toBe("running");
    expect(statusOf(status({ phase: "idle" }))).toBe("completed");
    expect(statusOf(status({ paused: true }))).toBe("stopped");
    // A provider that is not answering outranks everything else: it is the one
    // state the operator has to do something about.
    expect(statusOf(status({ phase: "scanning", provider_down: true }))).toBe("failed");
  });

  it("names the phase in the operator's language, with the project it is on", () => {
    expect(phaseLabel(status({ phase: "scanning", project_label: "UretimStudio" }))).toBe(
      "tarıyor · UretimStudio",
    );
    expect(phaseLabel(status({ phase: "scanning" }))).toBe("tarıyor");
    expect(phaseLabel(status({ paused: true, phase: "scanning" }))).toBe("duraklatıldı");
    expect(phaseLabel(status({ provider_down: true }))).toBe("sağlayıcı yanıt vermiyor");
    expect(phaseLabel(status({ phase: "backoff" }))).toBe("geri çekildi");
    expect(phaseLabel(status())).toBe("bekliyor");
  });
});
