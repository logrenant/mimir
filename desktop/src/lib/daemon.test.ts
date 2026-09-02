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

  test("setEmailStatus POSTs place_id and status, and tolerates a 204", async () => {
    withDaemon({ status: 204, body: "" });

    await expect(api.setEmailStatus("p1", "sent")).resolves.toBeUndefined();
    expect(invoke).toHaveBeenCalledWith("daemon_request", {
      method: "POST",
      path: "/maps/emails/status",
      body: JSON.stringify({ place_id: "p1", status: "sent" }),
    });
  });

  test("a rejected status surfaces the daemon's message", async () => {
    withDaemon({
      status: 400,
      body: JSON.stringify({
        error: { code: "bad_request", message: "status must be draft, sent, or skipped" },
      }),
    });

    await expect(api.setEmailStatus("p1", "sent")).rejects.toMatchObject({
      code: "bad_request",
      status: 400,
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
