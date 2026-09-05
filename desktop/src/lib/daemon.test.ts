import { beforeEach, describe, expect, test, vi } from "vitest";

const invoke = vi.fn();
vi.mock("@tauri-apps/api/core", () => ({ invoke: (...args: unknown[]) => invoke(...args) }));

const { api, DaemonError, endpoint, request, wsURL, __resetEndpointCache } = await import("./daemon");

const READY = { state: "ready", base_url: "http://127.0.0.1:51423", token: "a".repeat(64) };

/** Answers `get_daemon_endpoint` with a ready daemon and `daemon_request` with one reply. */
function withDaemon(reply: { status: number; body: string }) {
  invoke.mockImplementation(async (command: string) => {
    if (command === "get_daemon_endpoint") return READY;
    if (command === "daemon_request") return reply;
    throw new Error(`unexpected command ${command}`);
  });
}

beforeEach(() => {
  invoke.mockReset();
  __resetEndpointCache();
});

describe("endpoint", () => {
  test("comes from Tauri IPC, and only from there", async () => {
    invoke.mockResolvedValue(READY);

    const ep = await endpoint();

    expect(invoke).toHaveBeenCalledWith("get_daemon_endpoint");
    expect(ep.token).toBe(READY.token);
  });

  test("a daemon that failed to start is an error, not an empty endpoint", async () => {
    invoke.mockResolvedValue({ state: "failed", message: "could not listen on 127.0.0.1:51423" });
    await expect(endpoint()).rejects.toThrow(/could not listen/);
  });

  test("a daemon still starting is its own error code", async () => {
    invoke.mockResolvedValue({ state: "starting" });
    await expect(endpoint()).rejects.toMatchObject({ code: "daemon_starting" });
  });
});

describe("request", () => {
  // REST goes through the Rust shell because the WebView cannot make the call
  // itself: a cross-origin fetch carrying Authorization is preflighted, and the
  // daemon answers OPTIONS with 401 by design. Observed in a running window.
  test("goes through the shell, never through fetch", async () => {
    const fetchSpy = vi.fn();
    vi.stubGlobal("fetch", fetchSpy);
    withDaemon({ status: 200, body: JSON.stringify({ ok: true, version: "0.1.0", uptime_ms: 5 }) });

    const health = await api.health();

    expect(health.ok).toBe(true);
    expect(fetchSpy).not.toHaveBeenCalled();
    expect(invoke).toHaveBeenCalledWith("daemon_request", {
      method: "GET",
      path: "/healthz",
      body: null,
    });
    vi.unstubAllGlobals();
  });

  test("the token is never handed to the WebView for a REST call", async () => {
    withDaemon({ status: 200, body: "{}" });

    await api.listProjects();

    const commands = invoke.mock.calls.map((call) => call[0] as string);
    expect(commands).toEqual(["daemon_request"]);
    expect(JSON.stringify(invoke.mock.calls)).not.toContain(READY.token);
  });

  test("maps the daemon's error envelope onto a typed error", async () => {
    withDaemon({
      status: 400,
      body: JSON.stringify({
        error: { code: "invalid_path", message: "refusing the home directory" },
      }),
    });

    await expect(api.registerProject("/Users/me")).rejects.toMatchObject({
      code: "invalid_path",
      message: "refusing the home directory",
      status: 400,
    });
  });

  test("a non-JSON error body still produces a usable message", async () => {
    withDaemon({ status: 502, body: "<html>gateway</html>" });

    const err = await request("/healthz").catch((e: unknown) => e);

    expect(err).toBeInstanceOf(DaemonError);
    expect((err as InstanceType<typeof DaemonError>).code).toBe("http_502");
  });

  test("a dead daemon is 'unreachable', not a fabricated status", async () => {
    invoke.mockRejectedValue("could not reach the daemon: connection refused");

    await expect(request("/healthz")).rejects.toMatchObject({ code: "unreachable" });
  });

  test("serialises a POST body as JSON", async () => {
    withDaemon({ status: 201, body: JSON.stringify({ id: "p1" }) });

    await api.registerProject("/Users/me/code");

    expect(invoke).toHaveBeenCalledWith("daemon_request", {
      method: "POST",
      path: "/projects",
      body: JSON.stringify({ path: "/Users/me/code" }),
    });
  });

  test("an empty body is not parsed as JSON", async () => {
    withDaemon({ status: 204, body: "" });
    await expect(request("/healthz")).resolves.toBeUndefined();
  });
});

describe("accounts", () => {
  test("starting a login is a POST with no body", async () => {
    withDaemon({ status: 202, body: JSON.stringify({ state: "waiting" }) });

    await api.startAccountLogin();

    expect(invoke).toHaveBeenCalledWith("daemon_request", {
      method: "POST",
      path: "/accounts/login",
      body: null,
    });
  });

  // The reset is what the app calls on its way out as well, so it has to be a
  // route with no arguments: there is no id to hold at that point, and no
  // screen left to read an error from.
  test("a reset is a POST with no body", async () => {
    withDaemon({ status: 204, body: "" });

    await api.resetAccounts();

    expect(invoke).toHaveBeenCalledWith("daemon_request", {
      method: "POST",
      path: "/accounts/reset",
      body: null,
    });
  });
});

describe("brain scan", () => {
  // A click that never opened the picker has to keep meaning what it meant
  // before the picker existed: the daemon's own distil routing.
  test("scan now without a selection sends no body", async () => {
    withDaemon({ status: 202, body: JSON.stringify({ scan: {} }) });

    await api.scanBrainNow();

    expect(invoke).toHaveBeenCalledWith("daemon_request", {
      method: "POST",
      path: "/brain/scan/now",
      body: null,
    });
  });

  // The pair travels together or not at all: the daemon rejects a model whose
  // provider it was not given, so a client that sent one alone would only ever
  // get a 400.
  test("scan now sends the chosen provider and model", async () => {
    withDaemon({ status: 202, body: JSON.stringify({ scan: {} }) });

    await api.scanBrainNow({ provider: "claude", model: "claude-haiku-4-5-20251001" });

    expect(invoke).toHaveBeenCalledWith("daemon_request", {
      method: "POST",
      path: "/brain/scan/now",
      body: JSON.stringify({ provider: "claude", model: "claude-haiku-4-5-20251001" }),
    });
  });
});

describe("lead-gen export", () => {
  // The export takes the same search the run takes plus the file's own
  // options: the region cache is what keeps that from being a second search.
  test("posts the search and the enrich flag", async () => {
    withDaemon({
      status: 200,
      body: JSON.stringify({
        path: "/Users/x/exports/Kadikoy-20260902-210405.xlsx",
        sheets: ["Özet", "health"],
        companies: 5,
        with_phone: 3,
        with_email: 2,
        with_website: 4,
        enriched: true,
      }),
    });

    const res = await api.exportLeadgen({ query: "Kadıköy diş kliniği", region: "Kadıköy", enrich: true });

    expect(res.sheets).toEqual(["Özet", "health"]);
    expect(invoke).toHaveBeenCalledWith("daemon_request", {
      method: "POST",
      path: "/maps/leadgen/export",
      body: JSON.stringify({ query: "Kadıköy diş kliniği", region: "Kadıköy", enrich: true }),
    });
  });
});

describe("lead-gen", () => {
  test("runLeadgen POSTs the request body and returns the Report", async () => {
    const report = {
      region: "Kadikoy",
      query: "dentists",
      from_cache: false,
      ran_categorize: true,
      ran_gap_analysis: true,
      ran_emails: false,
      companies: [{ place_id: "p1", name: "Acme Dental", category: "health" }],
      categories: [{ category: "health", company_count: 1, gap_analysis: "- no websites" }],
    };
    withDaemon({ status: 200, body: JSON.stringify(report) });

    const got = await api.runLeadgen({ query: "dentists", region: "Kadikoy", gap_analysis: true });

    expect(got.categories[0].gap_analysis).toBe("- no websites");
    expect(invoke).toHaveBeenCalledWith("daemon_request", {
      method: "POST",
      path: "/maps/leadgen",
      body: JSON.stringify({ query: "dentists", region: "Kadikoy", gap_analysis: true }),
    });
  });

  // The search route no longer carries a model. The daemon reads the operator's
  // saved default behind it, so a body with provider/model in it would be this
  // client re-introducing the per-run picker the settings screen replaced.
  test("runLeadgen sends no provider or model", async () => {
    withDaemon({ status: 200, body: JSON.stringify({ companies: [], categories: [] }) });

    await api.runLeadgen({ query: "dentists" });

    const [, args] = invoke.mock.calls.find(([cmd]) => cmd === "daemon_request")!;
    expect(JSON.parse((args as { body: string }).body)).toEqual({ query: "dentists" });
  });

  test("draftOutreach POSTs the ticked place ids and the chosen channels", async () => {
    const result = {
      companies: [
        {
          place_id: "p1",
          name: "Acme Dental",
          category: "health",
          drafts: [{ channel: "whatsapp", body: "Merhaba", status: "draft" }],
        },
      ],
      categories: [{ category: "health", company_count: 1 }],
      notes: ["whatsapp draft skipped for p2: no place_id to key it by"],
    };
    withDaemon({ status: 200, body: JSON.stringify(result) });

    const got = await api.draftOutreach({
      place_ids: ["p1", "p2"],
      channels: ["email", "whatsapp"],
      region: "Kadıköy",
    });

    expect(got.companies[0].drafts?.[0].channel).toBe("whatsapp");
    // The notes are the pipeline's own diagnostics and reach the screen intact.
    expect(got.notes).toHaveLength(1);
    expect(invoke).toHaveBeenCalledWith("daemon_request", {
      method: "POST",
      path: "/maps/outreach",
      body: JSON.stringify({
        place_ids: ["p1", "p2"],
        channels: ["email", "whatsapp"],
        region: "Kadıköy",
      }),
    });
  });

  test("setOutreachStatus carries the channel, and tolerates a 204", async () => {
    withDaemon({ status: 204, body: "" });

    await expect(api.setOutreachStatus("p1", "whatsapp", "sent")).resolves.toBeUndefined();
    expect(invoke).toHaveBeenCalledWith("daemon_request", {
      method: "POST",
      path: "/maps/outreach/status",
      // No prompt version: the server resolves which draft "the one I am
      // looking at" means, and this client must never send one.
      body: JSON.stringify({ place_id: "p1", channel: "whatsapp", status: "sent" }),
    });
  });

  test("a rejected status surfaces the daemon's message", async () => {
    withDaemon({
      status: 400,
      body: JSON.stringify({
        error: { code: "bad_request", message: "status must be draft, sent, or skipped" },
      }),
    });

    await expect(api.setOutreachStatus("p1", "email", "sent")).rejects.toMatchObject({
      code: "bad_request",
      status: 400,
    });
  });
});

describe("settings", () => {
  const view = {
    provider: "agy",
    model: "gemini-3.1-pro-high",
    routed: { provider: "claude", model: "claude-opus-5" },
    rules: [
      { channel: "email", label: "E-posta", path: "/s/rules/email.md", body: "# kural", is_default: false, updated_at: 1_757_000_000 },
      { channel: "whatsapp", label: "WhatsApp", path: "/s/rules/whatsapp.md", body: "# kural", is_default: true },
    ],
  };

  test("the screen is one GET, not three", async () => {
    withDaemon({ status: 200, body: JSON.stringify(view) });

    const got = await api.settings();

    expect(got.rules).toHaveLength(2);
    expect(got.routed.provider).toBe("claude");
    expect(invoke).toHaveBeenCalledWith("daemon_request", {
      method: "GET",
      path: "/settings",
      body: null,
    });
  });

  // PUT, not PATCH: the pair is one decision, and the daemon replaces it whole.
  test("saveSettings PUTs the provider and model together", async () => {
    withDaemon({ status: 200, body: JSON.stringify(view) });

    await api.saveSettings("agy", "gemini-3.1-pro-high");

    expect(invoke).toHaveBeenCalledWith("daemon_request", {
      method: "PUT",
      path: "/settings",
      body: JSON.stringify({ provider: "agy", model: "gemini-3.1-pro-high" }),
    });
  });

  test("clearing the model is a legal save, not an omitted field", async () => {
    withDaemon({ status: 200, body: JSON.stringify({ ...view, provider: "", model: "" }) });

    const got = await api.saveSettings("", "");

    expect(got.provider).toBe("");
    expect(invoke).toHaveBeenCalledWith("daemon_request", {
      method: "PUT",
      path: "/settings",
      body: JSON.stringify({ provider: "", model: "" }),
    });
  });

  test("saveRule PUTs one channel's body", async () => {
    withDaemon({ status: 200, body: JSON.stringify(view.rules[1]) });

    const got = await api.saveRule("whatsapp", "# yeni kural");

    expect(got.is_default).toBe(true);
    expect(invoke).toHaveBeenCalledWith("daemon_request", {
      method: "PUT",
      path: "/settings/rules",
      body: JSON.stringify({ channel: "whatsapp", body: "# yeni kural" }),
    });
  });

  // A route rather than "send the shipped default back": a client holding a
  // copy of the default is exactly the drift the daemon publishes lists to avoid.
  test("resetRule asks the daemon for its own default", async () => {
    withDaemon({ status: 200, body: JSON.stringify(view.rules[0]) });

    await api.resetRule("email");

    expect(invoke).toHaveBeenCalledWith("daemon_request", {
      method: "POST",
      path: "/settings/rules/reset",
      body: JSON.stringify({ channel: "email" }),
    });
  });
});

describe("wsURL", () => {
  test("carries the token in the subprotocol, never the query string", () => {
    const { url, protocol } = wsURL("run-1", { base_url: READY.base_url, token: READY.token });

    expect(url).toBe("ws://127.0.0.1:51423/ws/runs/run-1");
    expect(url).not.toContain(READY.token);
    expect(url).not.toContain("?");
    // internal/api/middleware.go reads exactly this prefix.
    expect(protocol).toBe(`mimir.bearer.${READY.token}`);
  });

  test("escapes a run id rather than splicing it into a path", () => {
    const { url } = wsURL("../../healthz", { base_url: READY.base_url, token: READY.token });
    expect(url).toBe("ws://127.0.0.1:51423/ws/runs/..%2F..%2Fhealthz");
  });
});

describe("brain routes", () => {
  test("sends the scan controls as POSTs with no body", async () => {
    const calls: { method: string; path: string; body?: string }[] = [];
    invoke.mockImplementation(async (command: string, args: unknown) => {
      if (command === "get_daemon_endpoint") return READY;
      calls.push(args as { method: string; path: string; body?: string });
      return { status: 200, body: JSON.stringify({ scan: { phase: "idle" } }) };
    });

    await api.pauseBrainScan();
    await api.resumeBrainScan();
    await api.scanBrainNow();

    expect(calls.map((c) => `${c.method} ${c.path}`)).toEqual([
      "POST /brain/scan/pause",
      "POST /brain/scan/resume",
      "POST /brain/scan/now",
    ]);
    // The daemon's handlers deliberately do not decode a body: there is nothing
    // to configure about a scan, and DisallowUnknownFields would turn one into
    // a 400 on every click.
    for (const c of calls) expect(c.body ?? "").toBe("");
  });

  // The daemon accepts a filesystem path at exactly two routes and the graph is
  // not one of them, so the filter travels as the opaque id it was handed.
  test("passes the project as an opaque id, never a path", async () => {
    let path = "";
    invoke.mockImplementation(async (command: string, args: unknown) => {
      if (command === "get_daemon_endpoint") return READY;
      path = (args as { path: string }).path;
      return {
        status: 200,
        body: JSON.stringify({ nodes: [], edges: [], total_nodes: 0, truncated: false }),
      };
    });

    await api.brainGraph({ project: "proj-abc123", limit: 500 });
    expect(path).toBe("/brain/graph?project=proj-abc123&limit=500");

    await api.brainGraph();
    expect(path).toBe("/brain/graph");
  });
});

describe("the lead ledger", () => {
  test("an empty filter asks for the daemon's own defaults", async () => {
    withDaemon({ status: 200, body: JSON.stringify({ companies: [], limit: 200, offset: 0 }) });

    await api.listLeads();

    expect(invoke).toHaveBeenCalledWith("daemon_request", {
      method: "GET",
      path: "/maps/leads",
      body: null,
    });
  });

  test("only the fields that are set reach the query string", async () => {
    withDaemon({ status: 200, body: JSON.stringify({ companies: [], limit: 200, offset: 0 }) });

    await api.listLeads({ category: "health", q: "", without_website: true });

    const call = invoke.mock.calls.find((c) => c[0] === "daemon_request");
    const path = (call?.[1] as { path: string }).path;
    expect(path).toContain("category=health");
    expect(path).toContain("without_website=1");
    expect(path).not.toContain("q=");
    expect(path).not.toContain("limit=");
  });

  test("a Turkish filter is encoded, not sent raw", async () => {
    withDaemon({ status: 200, body: JSON.stringify({ categories: [] }) });

    await api.leadCategories({ q: "Kadıköy diş" });

    const call = invoke.mock.calls.find((c) => c[0] === "daemon_request");
    const path = (call?.[1] as { path: string }).path;
    expect(path.startsWith("/maps/leads/categories?q=")).toBe(true);
    expect(path).not.toContain(" ");
  });
});
