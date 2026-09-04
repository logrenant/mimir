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

/**
 * The interactive terminal socket's address and its auth.
 *
 * Same subprotocol trick as wsURL: the token is a request header, never a query
 * parameter. The size travels in the query because the shell needs a sensible
 * winsize from its very first line — waiting for the client's first resize
 * frame would let the prompt wrap against a default 80 columns.
 */
export function ptyWSURL(
  profile: string,
  size: { rows: number; cols: number },
  ep: DaemonEndpoint,
): { url: string; protocol: string } {
  const q = `profile=${encodeURIComponent(profile)}&rows=${size.rows}&cols=${size.cols}`;
  return {
    url: `${ep.base_url.replace(/^http/, "ws")}/ws/terminals/pty?${q}`,
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
  daemon: {
    ok: boolean;
    version: string;
    uptime_ms: number;
    store: string;
    projects: number;
    places_configured?: boolean;
    /**
     * The region-search providers, in the order they will be tried, and
     * whether the first one spends nothing. `places_configured` alone stopped
     * describing this the moment the free scrape became the primary: a machine
     * with no Google key still has region search.
     */
    region_sources?: string[];
    region_search_free?: boolean;
  };
  dependencies?: {
    crawl4ai?: DiagnosticsDependency;
    // The distil tier and the reason tier, separately: since task-51 agy has
    // no fallback, so a healthy claude says nothing about whether a page can
    // be summarised or a file distilled.
    agy?: DiagnosticsDependency;
    claude?: DiagnosticsDependency;
    pdftotext?: DiagnosticsDependency;
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
  /**
   * Found by the daemon's scan of the accounts directory rather than
   * registered here. Those slots answer to the filesystem: removing one means
   * removing its directory, so the app does not offer to forget them.
   */
  discovered?: boolean;
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
  /** Which provider answered: "mapscrape" (free) or "places_api" (billed). */
  source?: string;
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
  /**
   * Which provider and model run this search's model stages. Both are
   * optional and must travel together; omitting them routes by class, which
   * is what the daemon does for everything it starts on its own.
   */
  provider?: string;
  model?: string;
};

/**
 * config.LLMProviderChoice — one provider with the models it accepts.
 *
 * Nested rather than two flat lists because the pairing is the constraint:
 * a Gemini id means nothing to the `claude` CLI, and the daemon rejects the
 * combination rather than guessing.
 */
export type LLMProvider = {
  id: string;
  label: string;
  default_model: string;
  models: LLMModel[];
};

/**
 * config.CodingModelChoice as this route marshals it — id and label only.
 *
 * Not `CodingModel`, which carries the `default` flag the coding-model route
 * computes: here "which is default" is a property of the provider, not of the
 * model, so the flag would have nowhere honest to come from.
 */
export type LLMModel = { id: string; label: string };

/** GET /llm/providers — the picker's whole vocabulary. */
export type LLMProviderList = {
  providers: LLMProvider[];
  /** What a run gets when it sends no selection: the class routing's answer. */
  routed: { provider: string; model: string };
};

export type EmailStatus = "sent" | "skipped" | "draft";

/** What POST /maps/leadgen/export accepts: the same search, plus the file. */
export type LeadgenExportRequest = LeadgenRequest & {
  /** Open each company's website for a phone number and an email address. */
  enrich?: boolean;
  dir?: string;
};

/**
 * One row of the lead ledger — every business a run has ever returned, kept
 * across sessions (task-63). It is `LeadCompany` plus the two timestamps a
 * record has and a run result does not, so one table renders both.
 */
export type SavedLead = LeadCompany & {
  first_seen_at: number;
  last_seen_at: number;
};

/** The category rail, counted by the daemon rather than by this client. */
export type LeadCategoryCount = {
  category: string;
  company_count: number;
  without_website: number;
};

/** One past lead-gen run. */
export type LeadRun = {
  id: string;
  query: string;
  region?: string;
  source?: string;
  company_count: number;
  with_gaps?: boolean;
  with_emails?: boolean;
  ran_at: number;
};

/** What GET /maps/leads accepts. Every field is optional; the defaults are the
 *  daemon's, not this client's. */
export type LeadsQuery = {
  category?: string;
  run_id?: string;
  region?: string;
  q?: string;
  without_website?: boolean;
  limit?: number;
  offset?: number;
};

/**
 * One place the ledger holds leads for, mirroring api.leadRegionView.
 *
 * The picker's unit. A region searched seventeen times is one region, not
 * seventeen rows — which is what listing runs used to show.
 */
export type LeadRegion = {
  region: string;
  companies: number;
  runs: number;
  with_phone: number;
  with_site: number;
  last_ran_at?: string;
};

/** The workbook that was written: one sheet per category, plus a summary. */
export type LeadgenExportResult = {
  path: string;
  sheets: string[];
  companies: number;
  with_phone: number;
  with_email: number;
  with_website: number;
  enriched: boolean;
};

// --- brain -------------------------------------------------------------------
//
// The resident scan and the graph it builds. One definition per Go struct, tags
// included: brain.ScanStatus, api.graphNode, api.graphEdge, api.brainProject.

export type ScanPhase = "idle" | "discovering" | "scanning" | "backoff" | "paused";

export type BrainScanStatus = {
  phase: ScanPhase;
  paused: boolean;
  roots: string[];
  provider: string;
  model: string;
  project?: string;
  project_label?: string;
  project_index: number;
  project_count: number;

  remaining: number;
  skipped_unchanged: number;
  eligible: number;

  scanned_session: number;
  failed_session: number;
  unreadable_session: number;
  scanned_total: number;
  sweeps: number;
  nodes_total: number;

  sweep_started?: string;
  last_pass_at?: string;
  last_sweep_ended?: string;
  next_sweep_at?: string;

  provider_down: boolean;
  backoff_until?: string;
  last_error?: string;
};

export type BrainScanEvent = {
  seq: number;
  at: string;
  // "changed" is a file that was already known and moved — the one line that
  // says the detection is working, so the console draws it apart from "file".
  kind:
    | "sweep"
    | "project"
    | "file"
    | "changed"
    | "failed"
    | "unreadable"
    | "pass"
    | "control"
    | "backoff";
  project?: string;
  text: string;
};

export type BrainGraphNode = {
  id: string;
  kind: string;
  title: string;
  project?: string;
  tags?: string[];
  degree: number;
  updated_at: number;
};

export type BrainGraphEdge = {
  source: string;
  target: string;
  kind: string;
  weight: number;
};

export type BrainGraph = {
  nodes: BrainGraphNode[];
  edges: BrainGraphEdge[];
  project?: string;
  total_nodes: number;
  truncated: boolean;
};

export type BrainProject = {
  id: string;
  label: string;
  path: string;
  nodes: number;
  files: number;
  updated_at: number;
};

export type BrainNeighbor = {
  id: string;
  title: string;
  kind: string;
  relation: string;
  weight: number;
};

export type BrainNodeDetail = {
  id: string;
  project_path?: string;
  kind: string;
  source: string;
  title: string;
  assessment: string;
  tags?: string[];
  aliases?: string[];
  provider?: string;
  model?: string;
  updated_at?: string;
  neighbors?: BrainNeighbor[];
};

/**
 * One entry in a node's history (task-67): at this moment, at this content
 * hash, this is what the source meant.
 *
 * There is no file content here and there never will be — the file is still on
 * disk, and for anything under version control git already keeps the bytes.
 * What this carries is the reading, which is the part nothing else has.
 */
export type BrainNodeVersion = {
  content_hash: string;
  seen_at: number;
  size_bytes?: number;
  modified_at?: number;
  title?: string;
  assessment?: string;
  tags?: string[];
  model?: string;
};

/**
 * leadsQuery renders a ledger filter as a query string. Empty values are left
 * out rather than sent blank, so the daemon applies its own defaults — the page
 * bounds are the server's (SD-1), and a client that always sent `limit=` would
 * quietly become the place they live.
 */
function leadsQuery(q?: LeadsQuery): string {
  if (!q) return "";
  const p = new URLSearchParams();
  if (q.category) p.set("category", q.category);
  if (q.run_id) p.set("run_id", q.run_id);
  if (q.region) p.set("region", q.region);
  if (q.q) p.set("q", q.q);
  if (q.without_website) p.set("without_website", "1");
  if (q.limit) p.set("limit", String(q.limit));
  if (q.offset) p.set("offset", String(q.offset));
  const query = p.toString();
  return query ? "?" + query : "";
}

/**
 * One identity a terminal session can be opened as, mirroring
 * ptyterm.Profile. `command` is the line typed at the operator's own prompt.
 */
export type TerminalProfile = {
  name: string;
  command: string;
  /**
   * Whether the daemon has a live shell for this profile. Not the same as
   * "you are looking at it": a session outlives its viewer, which is what lets
   * both accounts stay open at once.
   */
  running?: boolean;
};

export const api = {
  health: () => request<Health>("/healthz"),
  terminalProfiles: () => request<{ profiles: TerminalProfile[] }>("/terminals/profiles"),
  // The only thing that ends a shell now that closing a viewer does not.
  killTerminal: (profile: string) =>
    request<void>(`/terminals/${encodeURIComponent(profile)}`, { method: "DELETE" }),
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
  // Re-reads the accounts directory. The daemon already scans at startup, so
  // this is for the moment right after a new slot is created and signed in.
  scanAccounts: () => request<{ accounts: Account[] }>("/accounts/scan", { method: "POST" }),
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
  // The model picker's vocabulary, published by the daemon rather than
  // mirrored here: a second copy would drift the first time a generation
  // ships, and the daemon would reject a pair this app had just offered.
  llmProviders: () => request<LLMProviderList>("/llm/providers"),
  runLeadgen: (body: LeadgenRequest) =>
    request<LeadgenReport>("/maps/leadgen", { method: "POST", body }),
  // Runs the same search — the region cache means it does not re-search — and
  // writes the workbook. Enrichment is one page fetch per company, so it is
  // the caller's decision, not a default.
  exportLeadgen: (body: LeadgenExportRequest) =>
    request<LeadgenExportResult>("/maps/leadgen/export", { method: "POST", body }),
  // The ledger: three reads that cost nothing. Separate routes from the run
  // above because they are not a search — they are what earlier searches found,
  // and they answer on a daemon with no region source at all.
  listLeads: (q?: LeadsQuery) =>
    request<{ companies: SavedLead[]; limit: number; offset: number }>(
      "/maps/leads" + leadsQuery(q),
    ),
  // The rail is counted by the daemon: it spans the whole ledger, not the page
  // the table happens to be showing.
  leadCategories: (q?: LeadsQuery) =>
    request<{ categories: LeadCategoryCount[] }>("/maps/leads/categories" + leadsQuery(q)),
  leadRuns: () => request<{ runs: LeadRun[] }>("/maps/leads/runs"),
  // Regions, not runs, are what the picker offers: one row per place.
  leadRegions: () => request<{ regions: LeadRegion[] }>("/maps/leads/regions"),
  // 204, no body — the caller updates its own row optimistically.
  setEmailStatus: (placeID: string, status: EmailStatus) =>
    request<void>("/maps/emails/status", {
      method: "POST",
      body: { place_id: placeID, status },
    }),

  // The Brain tab. The three controls take no body: there is nothing to
  // configure about a scan, and the daemon's handlers do not decode one.
  brainScan: () => request<{ scan: BrainScanStatus }>("/brain/scan"),
  pauseBrainScan: () => request<{ scan: BrainScanStatus }>("/brain/scan/pause", { method: "POST" }),
  resumeBrainScan: () => request<{ scan: BrainScanStatus }>("/brain/scan/resume", { method: "POST" }),
  /**
   * POST /brain/scan/now — wake the resident loop for one sweep.
   *
   * The selection is optional and applies to that sweep alone. Sending nothing
   * is the older contract and still means "the configured distil routing", so
   * the button works the same when the picker was never opened.
   */
  scanBrainNow: (sel?: { provider: string; model: string }) =>
    request<{ scan: BrainScanStatus }>("/brain/scan/now", {
      method: "POST",
      // `request` serialises the body itself; passing the object keeps this the
      // same shape as every other POST here.
      ...(sel?.provider ? { body: sel } : {}),
    }),
  // `after` is the last sequence the console rendered, so an open tab asks for
  // the handful of lines it is missing rather than the whole buffer.
  brainScanLog: (after: number) =>
    request<{ events: BrainScanEvent[]; seq: number }>(
      `/brain/scan/log?after=${encodeURIComponent(String(after))}`,
    ),
  brainProjects: () => request<{ projects: BrainProject[] }>("/brain/projects"),
  // `project` is the opaque id from /brain/projects, never a path: the daemon
  // accepts a filesystem path at exactly two routes and this is not one of
  // them (internal/api/AGENTS.md).
  brainGraph: (opts?: { project?: string; limit?: number }) => {
    const q = new URLSearchParams();
    if (opts?.project) q.set("project", opts.project);
    if (opts?.limit) q.set("limit", String(opts.limit));
    const query = q.toString();
    return request<BrainGraph>("/brain/graph" + (query ? "?" + query : ""));
  },
  // History rides node detail: a node with one version is the common case, and
  // a second call for every file on the machine would be a request that almost
  // always answers "nothing to show".
  brainNode: (id: string) =>
    request<{ node: BrainNodeDetail; versions?: BrainNodeVersion[] }>(
      `/brain/nodes/${encodeURIComponent(id)}`,
    ),
  brainNodeVersions: (id: string) =>
    request<{ versions: BrainNodeVersion[] }>(
      `/brain/nodes/${encodeURIComponent(id)}/versions`,
    ),
};

/** Test seam: drops the cached endpoint so a test can hand over a new one. */
export function __resetEndpointCache(): void {
  cached = null;
}
