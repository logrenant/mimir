import { describe, expect, it } from "vitest";
import { CONSOLE_TEXT, LINE_CLASS, scanLineClass } from "./lineColors";
import type { BrainScanEvent } from "./daemon";

const KINDS: BrainScanEvent["kind"][] = [
  "sweep", "project", "file", "changed", "failed", "unreadable", "pass", "control", "backoff",
];

describe("LINE_CLASS", () => {
  // The point of the module: the values come from @theme, so a hex here would
  // mean a fourth palette had quietly reappeared.
  it("carries no raw colour", () => {
    for (const cls of Object.values(LINE_CLASS)) {
      expect(cls).not.toMatch(/#[0-9a-f]{3,8}/i);
    }
  });

  it("names only tokens the theme defines", () => {
    const tokens = ["text", "muted", "electric", "ok", "bad", "mist", "lime"];
    for (const cls of Object.values(LINE_CLASS)) {
      const token = cls.replace(/^text-/, "").split("/")[0];
      expect(tokens).toContain(token);
    }
  });

  it("puts model output at full contrast and nothing else", () => {
    expect(LINE_CLASS.text).toBe("text-text");
    const full = Object.entries(LINE_CLASS).filter(([, c]) => c === "text-text");
    expect(full).toHaveLength(1);
  });

  // stderr used to be #e5a23d, an amber that existed in no token. It is in the
  // failure family now, but dimmer than an actual failure.
  it("separates stderr from a real failure without a fifth hue", () => {
    expect(LINE_CLASS.stderr).not.toBe(LINE_CLASS.bad);
    expect(LINE_CLASS.stderr.startsWith("text-bad")).toBe(true);
  });
});

describe("scanLineClass", () => {
  it("answers for every event kind the daemon can send", () => {
    for (const kind of KINDS) {
      expect(Object.values(LINE_CLASS)).toContain(scanLineClass(kind));
    }
  });

  it("draws a re-read apart from a first read", () => {
    expect(scanLineClass("changed")).not.toBe(scanLineClass("file"));
  });

  it("reports a failed file as a failure", () => {
    expect(scanLineClass("failed")).toBe(LINE_CLASS.bad);
  });
});

describe("CONSOLE_TEXT", () => {
  it("is monospaced and keeps the larger leading of the two old copies", () => {
    expect(CONSOLE_TEXT).toContain("font-mono");
    expect(CONSOLE_TEXT).toContain("11.5px");
    expect(CONSOLE_TEXT).toContain("1.65");
  });
});
