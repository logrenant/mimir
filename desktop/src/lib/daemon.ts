import { invoke } from "@tauri-apps/api/core";

/**
 * The one way this app talks to mimir-daemon.
 *
 * REST goes through the Rust shell, not through `fetch`. That is forced, not
 * stylistic: a `fetch` with an `Authorization` header from `tauri://localhost`
 * to `http://127.0.0.1:<port>` is cross-origin, so the WebView sends a CORS
 * preflight, and the daemon answers `OPTIONS` with 401 because it carries no
 * CORS headers on purpose ("No CORS headers, ever" — internal/api/api.go).
 * Verified in a running window before this was written. Taking the browser out
 * of the HTTP path also keeps the token out of the WebView entirely for REST.
 *
 * The WebSocket is the exception and needs `endpoint()`: a browser socket
 * cannot set headers, its handshake is exempt from preflight, and the daemon
 * accepts the token as a subprotocol precisely for this client. It is never a
 * query parameter — a URL lands in logs, history and referrers.
 */

export type DaemonEndpoint = { base_url: string; token: string };

export type DaemonStatus =
  | { state: "starting" }
  | { state: "ready"; base_url: string; token: string }
  | { state: "failed"; message: string };

export class DaemonError extends Error {
  code: string;
  status: number;

  constructor(code: string, message: string, status = 0) {
    super(message);
    this.name = "DaemonError";
    this.code = code;
    this.status = status;
  }
}

let cached: DaemonEndpoint | null = null;

/** Asks the Rust shell where the daemon is. Cached: it does not move. */
export async function endpoint(): Promise<DaemonEndpoint> {
  if (cached) return cached;

  const status = await invoke<DaemonStatus>("get_daemon_endpoint");
  if (status.state === "ready") {
    cached = { base_url: status.base_url, token: status.token };
    return cached;
  }
  if (status.state === "failed") {
    throw new DaemonError("daemon_failed", status.message);
  }
  throw new DaemonError("daemon_starting", "the daemon is still starting");
}

/** Current handshake state, without throwing — what the connection screen renders. */
export async function status(): Promise<DaemonStatus> {
  return invoke<DaemonStatus>("get_daemon_endpoint");
}

/** Re-runs the supervisor after a failure. */
export async function restart(): Promise<void> {
  cached = null;
  await invoke("restart_daemon");
}

type ErrorEnvelope = { error?: { code?: string; message?: string } };
type ProxyResponse = { status: number; body: string };

type RequestOptions = { method?: "GET" | "POST" | "DELETE"; body?: unknown };

export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const method = options.method ?? "GET";

  let response: ProxyResponse;
  try {
    response = await invoke<ProxyResponse>("daemon_request", {
      method,
      path,
      body: options.body === undefined ? null : JSON.stringify(options.body),
    });
  } catch (cause) {
    // The shell rejects only on a transport failure or a daemon that is not
    // ready. Neither is an HTTP status, so it gets its own code rather than a
    // fabricated one.
    throw new DaemonError("unreachable", String(cause));
  }

  if (response.status < 200 || response.status >= 300) {
    // The daemon's own envelope is {"error":{"code","message"}} and its
    // messages are already written for a human (internal/project's path guards
    // say what is wrong and what to do), so they are surfaced verbatim.
    let code = `http_${response.status}`;
    let message = `request failed with ${response.status}`;
    try {
      const parsed = JSON.parse(response.body) as ErrorEnvelope;
      if (parsed.error?.code) code = parsed.error.code;
      if (parsed.error?.message) message = parsed.error.message;
    } catch {
      /* not JSON: keep the status-derived message */
    }
    throw new DaemonError(code, message, response.status);
  }

  if (!response.body) return undefined as T;
  return JSON.parse(response.body) as T;
}

/**
 * The live run socket's address and its auth.
 *
 * A browser's WebSocket cannot set headers, so the daemon accepts the token in
 * the subprotocol list and echoes it back (internal/api/middleware.go). It is
 * deliberately not a query parameter.
 */
export function wsURL(runID: string, ep: DaemonEndpoint): { url: string; protocol: string } {
  return {
    url: `${ep.base_url.replace(/^http/, "ws")}/ws/runs/${encodeURIComponent(runID)}`,
    protocol: `mimir.bearer.${ep.token}`,
  };
}

// ---- the daemon's own shapes, mirrored ------------------------------------
// One definition per Go struct, tags included, so a rename on either side is a
// type error here rather than an undefined at runtime.

export type Health = { ok: boolean; version: string; uptime_ms: number };

export type DiagnosticsDependency = {
  ok: boolean;
  detail: string;
  optional?: boolean;
  model_present?: boolean;
};

export type Diagnostics = {
  daemon: { ok: boolean; version: string; uptime_ms: number; store: string; projects: number };
  dependencies?: {
    crawl4ai?: DiagnosticsDependency;
    claude?: DiagnosticsDependency;
    duckduckgo?: DiagnosticsDependency;
    maps_scraper?: DiagnosticsDependency;
    versions?: Record<string, string>;
    code?: string;
    message?: string;
  };
};

export type Project = {
  id: string;
  path: string;
  display_name: string;
  created_at: string;
  last_used_at: string;
};

/**
 * store.RunStatus* — the whole life of a coding task, not just its execution.
 *
 * `backlog` and `queued` are the operator's half: a card written down, and a
 * card released. Everything after that belongs to the runner.
 */
export type RunStatus =
  | "backlog"
  | "queued"
  | "running"
  | "completed"
  | "failed"
  | "stopped";

/**
 * A Claude Code credential slot.
 *
 * `config_dir` is not a secret and holds none: the CLI hashes it to name a
 * keychain entry, and the credential itself never leaves the keychain. It is
 * reported so two slots can be told apart.
 */
export type Account = {
  id: string;
  label: string;
  config_dir: string;
  is_default: boolean;
  created_at?: string;
  last_used_at?: string;
};

/** What `claude auth status` says about one slot. Costs nothing to ask. */
export type AccountStatus = {
  logged_in: boolean;
  email?: string;
  org_name?: string;
  subscription_type?: string;
  auth_method?: string;
  error?: string;
};

export type Run = {
  id: string;
  project_id: string;
  title?: string;
  prompt: string;
  status: RunStatus;
  session_id?: string;
  model?: string;
  /** The slot the operator pinned; absent means "any free one". */
  requested_account_id?: string;
  /** The slot it actually ran on, filled in when the dispatcher claimed it. */
  account_id?: string;
  attachments?: string[];
  cost_usd?: number;
  num_turns?: number;
  error?: string;
  created_at?: string;
  queued_at?: string;
  started_at?: string;
  ended_at?: string;
};

/**
 * One entry of the daemon's model allow-list.
 *
 * The list is a constant in `internal/config`, published rather than mirrored:
 * a second copy here would drift the first time a generation ships, and the
 * daemon would then reject an option this app had just offered.
 */
export type CodingModel = {
  id: string;
  label: string;
  default: boolean;
};

/** coderunner.Attachment, plus the base64 the fetch route adds. */
export type Attachment = {
  id: string;
  filename: string;
  mime: string;
  bytes: number;
  data_base64?: string;
};

/** What POST /coding-tasks accepts. `start: false` puts it on the board. */
export type CreateTaskRequest = {
  project_id: string;
  prompt: string;
  title?: string;
  attachment_ids?: string[];
  /** Pin the run to one account. Omit for the first free one. */
  account_id?: string;
  /** One of GET /coding-models' ids. Omit for the daemon's default. */
  model?: string;
  start?: boolean;
};

/** internal/events.Event — one flat struct, as the Go doc comment explains. */
export type RunEvent = {
  kind:
    | "run.started"
    | "text.delta"
    | "reasoning.delta"
    | "tool.call"
    | "tool.result"
    | "rate_limit"
    | "stderr"
    | "run.completed"
    | "run.failed"
    | "run.stopped";
  run_id: string;
  seq: number;
  at: string;
  text?: string;
  call_id?: string;
  tool_name?: string;
  args?: unknown;
  risk?: "read" | "write" | "exec";
  ok?: boolean;
  output?: string;
  session_id?: string;
  model?: string;
  cost_usd?: number;
  duration_ms?: number;
  num_turns?: number;
  utilization?: number;
  resets_at?: number;
  error?: string;
};

export function isTerminal(event: RunEvent): boolean {
  return (
    event.kind === "run.completed" ||
    event.kind === "run.failed" ||
    event.kind === "run.stopped"
  );
}

/** store.IsTerminalStatus — the one definition of "this run is over". */
export function isTerminalStatus(status: RunStatus): boolean {
  return status === "completed" || status === "failed" || status === "stopped";
}

// ---- Maps lead-gen (task-34 routes) -------------------------------------

/** One company as leadgen.CompanyLead marshals it — flat snake_case. */
export type LeadCompany = {
  place_id: string;
  name: string;
  address?: string;
  latitude?: number;
  longitude?: number;
  rating?: number;
  review_count?: number;
  website?: string;
  phone?: string;
  primary_type?: string;
  business_status?: string;
  source?: string;
  category: string;
  category_method?: string;
  email?: string;
  email_status?: "draft" | "sent" | "skipped";
  email_method?: string;
};

/** leadgen.CategoryReport. */
export type CategoryReport = {
  category: string;
  company_count: number;
  gap_analysis?: string;
  gap_method?: string;
  truncated?: boolean;
};

/** leadgen.Report — what POST /maps/leadgen returns. */
export type LeadgenReport = {
  region: string;
  query: string;
  from_cache: boolean;
  ran_categorize: boolean;
  ran_gap_analysis: boolean;
  ran_emails: boolean;
  companies: LeadCompany[];
  categories: CategoryReport[];
  notes?: string[];
};

/** The body POST /maps/leadgen accepts. */
export type LeadgenRequest = {
  query: string;
  region?: string;
  count?: number;
  language_code?: string;
  region_code?: string;
  near?: { latitude: number; longitude: number; radius_meters: number };
  gap_analysis?: boolean;
  emails?: boolean;
};

export type EmailStatus = "sent" | "skipped" | "draft";

export const api = {
  health: () => request<Health>("/healthz"),
  diagnostics: () => request<Diagnostics>("/diagnostics"),
  listProjects: () => request<{ projects: Project[] }>("/projects"),
  listAccounts: () => request<{ accounts: Account[] }>("/accounts"),
  listCodingModels: () => request<{ models: CodingModel[] }>("/coding-models"),
  // The second and last route that takes a filesystem path, validated once
  // there exactly as /projects is.
  registerAccount: (label: string, configDir: string) =>
    request<Account>("/accounts", { method: "POST", body: { label, config_dir: configDir } }),
  deleteAccount: (id: string) =>
    request<void>(`/accounts/${encodeURIComponent(id)}`, { method: "DELETE" }),
  accountStatus: (id: string) =>
    request<AccountStatus>(`/accounts/${encodeURIComponent(id)}/status`),
  registerProject: (path: string) =>
    request<Project>("/projects", { method: "POST", body: { path } }),
  startCodingTask: (projectID: string, prompt: string, extra?: Partial<CreateTaskRequest>) =>
    request<Run>("/coding-tasks", {
      method: "POST",
      body: { project_id: projectID, prompt, ...extra },
    }),
  // The board's create: same route, `start: false`, so a card exists before
  // anybody has decided to spend tokens on it.
  createCodingTask: (body: CreateTaskRequest) =>
    request<Run>("/coding-tasks", { method: "POST", body }),
  enqueueCodingTask: (runID: string) =>
    request<Run>(`/coding-tasks/${encodeURIComponent(runID)}/enqueue`, { method: "POST" }),
  stopCodingTask: (runID: string) =>
    request<Run>(`/coding-tasks/${encodeURIComponent(runID)}/stop`, { method: "POST" }),
  deleteCodingTask: (runID: string) =>
    request<void>(`/coding-tasks/${encodeURIComponent(runID)}`, { method: "DELETE" }),
  uploadAttachment: (filename: string, dataBase64: string) =>
    request<Attachment>("/coding-tasks/attachments", {
      method: "POST",
      body: { filename, data_base64: dataBase64 },
    }),
  getAttachment: (id: string) =>
    request<Attachment>(`/coding-tasks/attachments/${encodeURIComponent(id)}`),
  getCodingTask: (runID: string) =>
    request<Run>(`/coding-tasks/${encodeURIComponent(runID)}`),
  // The board's data source: one project's runs, most recent first. There is
  // no cross-project route (internal/store only indexes by project), so a
  // multi-project board calls this once per registered project.
  listCodingTasks: (projectID: string) =>
    request<{ runs: Run[] }>(`/coding-tasks?project_id=${encodeURIComponent(projectID)}`),
  runLeadgen: (body: LeadgenRequest) =>
    request<LeadgenReport>("/maps/leadgen", { method: "POST", body }),
  // 204, no body — the caller updates its own row optimistically.
  setEmailStatus: (placeID: string, status: EmailStatus) =>
    request<void>("/maps/emails/status", {
      method: "POST",
      body: { place_id: placeID, status },
    }),
};

/** Test seam: drops the cached endpoint so a test can hand over a new one. */
export function __resetEndpointCache(): void {
  cached = null;
}
