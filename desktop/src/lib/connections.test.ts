import { describe, expect, test } from "vitest";

import type { CatalogueEntry, Connection } from "./daemon";
import {
  canServeSchema,
  connectionState,
  groupByVendor,
  sharedNote,
  stateLabel,
  transportLabel,
  addable,
  isConnectable,
  catalogueBadge,
  authLabel,
  needsConnecting,
} from "./connections";

const conn = (over: Partial<Connection> = {}): Connection => ({
  id: "agy",
  label: "Antigravity",
  vendor: "google",
  adapter: "agy-cli",
  transport: "cli",
  default_model: "m",
  enabled: true,
  builtin: true,
  connect: { kind: "manual", hint: "" },
  ...over,
});

const availability = (over: Partial<NonNullable<Connection["availability"]>> = {}) => ({
  provider: "agy",
  model: "m",
  installed: true,
  signed_in: false,
  probed: false,
  structured_output: false,
  agentic: false,
  ...over,
});

describe("connectionState", () => {
  // Not knowing is not the same as knowing it is fine. Establishing whether a
  // login works costs a model call, so a screen that has not asked must not
  // claim an answer.
  test("an unprobed connection is unknown, not ok", () => {
    expect(connectionState(conn({ availability: availability() }))).toBe("unknown");
    expect(connectionState(conn())).toBe("unknown");
  });

  test("probed and signed in is the only way to read ok", () => {
    expect(
      connectionState(conn({ availability: availability({ probed: true, signed_in: true }) })),
    ).toBe("ok");
    expect(
      connectionState(conn({ availability: availability({ probed: true, signed_in: false }) })),
    ).toBe("signed-out");
  });

  test("a missing binary outranks everything else about it", () => {
    expect(
      connectionState(
        conn({ availability: availability({ installed: false, probed: true }) }),
      ),
    ).toBe("missing");
  });

  test("a disabled connection says so rather than reporting its CLI", () => {
    expect(
      connectionState(
        conn({ enabled: false, availability: availability({ probed: true, signed_in: true }) }),
      ),
    ).toBe("disabled");
  });

  test("every state has a word", () => {
    for (const s of ["ok", "signed-out", "missing", "disabled", "unknown"] as const) {
      expect(stateLabel(s).length).toBeGreaterThan(0);
    }
  });
});

describe("groupByVendor", () => {
  // The visible payoff of getting the model right. `agy` and `gemini` are two
  // binaries and one company: Antigravity has no subscription of its own, it
  // rides a Google AI plan. Drawing them as independent providers tells the
  // operator they have two budgets when they have one.
  test("two Google binaries are one vendor, and the group says it is shared", () => {
    const groups = groupByVendor([
      conn({ id: "agy", vendor: "google" }),
      conn({ id: "claude", vendor: "anthropic", label: "Claude Code" }),
      conn({ id: "gemini", vendor: "google", label: "Gemini CLI" }),
    ]);

    const google = groups.find((g) => g.vendor === "google");
    expect(google?.connections.map((c) => c.id)).toEqual(["agy", "gemini"]);
    expect(google?.shared).toBe(true);
    expect(sharedNote(google!)).toContain("Google");
  });

  // A note that is always there is a note nobody reads.
  test("a lone connection carries no shared note", () => {
    const groups = groupByVendor([conn({ id: "claude", vendor: "anthropic" })]);
    expect(groups[0].shared).toBe(false);
    expect(sharedNote(groups[0])).toBe("");
  });

  test("groups keep the daemon's order", () => {
    const groups = groupByVendor([
      conn({ id: "ollama", vendor: "local" }),
      conn({ id: "agy", vendor: "google" }),
    ]);
    expect(groups.map((g) => g.vendor)).toEqual(["local", "google"]);
  });
});

describe("transportLabel", () => {
  test("says how the connection is reached, because that is the bill", () => {
    expect(transportLabel(conn({ transport: "cli" }))).toBe("CLI");
    expect(transportLabel(conn({ transport: "api" }))).toBe("API");
  });
});

describe("canServeSchema", () => {
  // Brain's distil sends a schema and parses what comes back. A connection that
  // cannot serve one answers in prose, and the pass stores a node with no
  // assessment — the silent degradation Capabilities exists to prevent.
  test("unknown capability is not a yes", () => {
    expect(canServeSchema(conn())).toBe(false);
    expect(
      canServeSchema(conn({ availability: availability({ structured_output: true }) })),
    ).toBe(true);
  });
});

describe("the catalogue", () => {
  const entry = (over: Partial<CatalogueEntry> = {}): CatalogueEntry => ({
    id: "deepseek",
    label: "DeepSeek",
    vendor: "deepseek",
    adapter: "openai-compatible",
    transport: "api",
    auth: "api-key",
    status: "coming-soon",
    note: "Adaptör, gerçek bir hesaba karşı sınanabildiğinde eklenecek.",
    ...over,
  });

  // The four CLI connections ship as built-ins and are already in the list
  // above; offering them again would be offering a second name for one login.
  test("what is already configured is not offered again", () => {
    const cat = [entry({ id: "claude", status: "available" }), entry()];
    const got = addable(cat, [
      {
        id: "claude",
        label: "Claude Code",
        vendor: "anthropic",
        adapter: "claude-cli",
        transport: "cli",
        default_model: "m",
        enabled: true,
        builtin: true,
        connect: { kind: "daemon", hint: "" },
      },
    ]);
    expect(got.map((e) => e.id)).toEqual(["deepseek"]);
  });

  // "Coming soon" is a promise about the product's shape, not a date — and the
  // row carries the entry's own reason rather than a generic one.
  test("a coming-soon entry is not connectable and says so", () => {
    expect(isConnectable(entry())).toBe(false);
    expect(catalogueBadge(entry())).toBe("yakında");
    expect(isConnectable(entry({ status: "available" }))).toBe(true);
    expect(catalogueBadge(entry({ status: "available" }))).toBe("eklenebilir");
  });

  // Said before the operator picks, because "do I need a key for this" changes
  // whether they can act on it at all.
  test("says how it will authenticate", () => {
    expect(authLabel(entry())).toBe("API anahtarı");
    expect(authLabel(entry({ auth: "cli-login" }))).toBe("CLI oturumu");
  });
});

describe("needsConnecting", () => {
  const c = (over: Partial<Connection> = {}): Connection => ({
    id: "gemini",
    label: "Gemini CLI",
    vendor: "google",
    adapter: "gemini-cli",
    transport: "cli",
    default_model: "m",
    enabled: true,
    builtin: true,
    connect: { kind: "manual", command: "gemini", hint: "" },
    ...over,
  });
  const av = (over = {}) => ({
    provider: "gemini",
    model: "m",
    installed: true,
    signed_in: false,
    probed: false,
    structured_output: false,
    agentic: true,
    ...over,
  });

  // Only when there is something to fix. A "connect" button on a working
  // connection invites somebody to break it.
  test("offered only for a signed-out or missing connection", () => {
    expect(needsConnecting(c({ availability: av({ probed: true }) }))).toBe(true);
    expect(needsConnecting(c({ availability: av({ installed: false }) }))).toBe(true);
    expect(
      needsConnecting(c({ availability: av({ probed: true, signed_in: true }) })),
    ).toBe(false);
    // Unprobed is "nobody asked", not "broken".
    expect(needsConnecting(c({ availability: av() }))).toBe(false);
  });
});
