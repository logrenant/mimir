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

// PUT is here for the routes that replace a whole document rather than amend
// one — the scan policy, the settings values, a rule file. The distinction is
// the daemon's: PATCH takes the fields that changed, PUT takes the list as it
// should now be.
type RequestOptions = { method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE"; body?: unknown };

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
 * The one Claude account Mimir is connected as.
 *
 * `config_dir` is Mimir's own credential slot, and it is neither a secret nor
 * holds one: the CLI hashes the path to name a keychain entry, and the
 * credential itself never leaves the keychain. It is reported because it is
 * what the operator would type to reach the same slot from a terminal.
 */
export type Account = {
  id: string;
  label: string;
  config_dir: string;
  created_at?: string;
  last_used_at?: string;
};

/** What `claude auth status` says about the slot. Costs nothing to ask. */
export type AccountStatus = {
  logged_in: boolean;
  email?: string;
  org_name?: string;
  subscription_type?: string;
  auth_method?: string;
  error?: string;
};

/**
 * One attempt at connecting the account, as the daemon reports it.
 *
 * `waiting` is the good case: the browser window is open and the CLI is
 * listening on its own loopback callback, so finishing in the browser finishes
 * the login with nothing to type. `code` is the fallback the CLI takes when it
 * could not open a browser itself — then, and only then, a code is pasted back.
 */
export type LoginState = {
  state: "idle" | "opening" | "waiting" | "code" | "done" | "failed";
  url?: string;
  message?: string;
  output?: string;
  email?: string;
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
 * One credential slot the queue is currently waiting on.
 *
 * A pause, not a failure: the account has no tokens left until `resets_at`,
 * and the daemon restarts the pipeline itself when that moment arrives. There
 * is deliberately no way to clear one from here — the only thing that ends it
 * is the window rolling over.
 */
export type Hold = {
  account_id?: string;
  since: string;
  resets_at: string;
  reason?: string;
};

/**
 * One moment in the life of a spent token budget.
 *
 * `run` is a task the budget cut off mid-flight (it went back to the queue with
 * its session, so it resumes rather than restarts), `dispatch` is a queued task
 * that could not be claimed, and `resumed` is the window rolling over and the
 * queue picking itself back up.
 */
export type RateLimitEvent = {
  id: number;
  at: string;
  phase: "run" | "dispatch" | "resumed";
  account_id?: string;
  run_id?: string;
  resets_at?: string;
  detail?: string;
};

export type LimitReport = {
  holds: Hold[];
  log: RateLimitEvent[];
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
  /**
   * The company's own address, found by the contacts stage — where an email is
   * sent, never the letter itself. The two shared one field once and stage 4
   * overwrote the address with the draft; they are separate on the wire now
   * because they are separate things.
   */
  email?: string;
  /** leadgen.CompanyLead.Drafts — at most one per channel. */
  drafts?: Draft[];
};

/**
 * settings.Channel — the closed set of media an outreach message is written
 * for. It is closed on the daemon too: a channel *is* a rule file, so a third
 * one means shipping a third default rather than adding a string here.
 */
export type OutreachChannel = "email" | "whatsapp";

/** Every channel, in the order the daemon lists them. Email first: it is the
 *  one that existed before this set had two members. */
export const CHANNELS: OutreachChannel[] = ["email", "whatsapp"];

/** The human's decision on one draft. */
export type OutreachStatus = "draft" | "sent" | "skipped";

/**
 * leadgen.Draft — one outreach message for one company on one channel.
 *
 * The status is per channel, because sending the email and skipping the
 * WhatsApp line is an ordinary thing to decide.
 */
export type Draft = {
  channel: OutreachChannel;
  body: string;
  status?: OutreachStatus;
  method?: string;
  truncated?: boolean;
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

/**
 * The body POST /maps/leadgen accepts.
 *
 * There is deliberately no provider/model here any more. The route falls back
 * to the operator's saved default (`api.leadgenSelection`), and that default
 * lives on the settings screen — one place to choose a model rather than one
 * per screen that spends one. Leaving the fields on this type would be an
 * invitation to put a second picker back on the search bar.
 */
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

/**
 * The body POST /maps/outreach accepts: the companies the operator ticked, and
 * what to write them.
 *
 * Ids rather than a filter, and that is the whole difference between this and
 * POST /maps/leadgen. A search is "find me companies"; this is "write to these
 * ones". A filter would let one short string spend a region's worth of tokens
 * with nobody having seen how many companies that was.
 */
export type OutreachRequest = {
  place_ids: string[];
  /** Empty means email alone — what a client written before WhatsApp meant. */
  channels?: OutreachChannel[];
  /** Labels the gap analysis. Empty falls back to the companies' own region. */
  region?: string;
};

/** leadgen.OutreachResult — what was written, and what it was written from. */
export type OutreachResult = {
  companies: LeadCompany[];
  categories: CategoryReport[];
  notes?: string[];
};

/**
 * api.ruleView — one channel's rule file as the settings screen sees it.
 *
 * `path` is shown because the operator may well prefer their own editor, and a
 * rule file whose location is a secret is a rule file nobody trusts.
 */
export type OutreachRule = {
  channel: OutreachChannel;
  label: string;
  path: string;
  body: string;
  is_default: boolean;
  updated_at?: number;
};

/**
 * api.settingsView — the whole settings screen in one response.
 *
 * One response because it is one screen: a client that had to fan out to three
 * routes to draw it would show the model picker before the rule files and look
 * broken for the difference.
 */
export type SettingsView = {
  provider: string;
  model: string;
  /** What a run gets when no model is saved: the class routing's own answer. */
  routed: { provider: string; model: string };
  rules: OutreachRule[];
};

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
  /** The operator's "not this one" list, echoed back from the running sweep. */
  excludes?: string[];
};

/**
 * What the scan is permitted to read — GET/PUT /brain/scan/policy.
 *
 * `configured` is the difference between "these are the folders Mimir picked"
 * and "these are the folders you chose". The screen says which, because only
 * one of the two invites a look.
 */
export type BrainScanPolicy = {
  roots: string[];
  excludes: string[];
  configured: boolean;
  default_roots: string[];
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
   * a terminal survive a trip to another screen.
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
  // Connecting is a login the *daemon* runs — it starts `claude auth login`
  // against its own credential slot and opens the authorization page in a
  // private browser window. This returns once that window is open, not once
  // the login is finished, so the caller follows loginState from there.
  startAccountLogin: () => request<LoginState>("/accounts/login", { method: "POST" }),
  loginState: () => request<LoginState>("/accounts/login"),
  // Only for the flow the CLI falls back to when it could not open a browser
  // itself. A single-use authorization code, typed into the waiting process.
  submitLoginCode: (code: string) =>
    request<void>("/accounts/login/code", { method: "POST", body: { code } }),
  // Signs the account out and forgets it — the "çıkış yap" button, and what
  // the shell calls on its way out of the app.
  resetAccounts: () => request<void>("/accounts/reset", { method: "POST" }),
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
  // Puts a failed or stopped card back in the queue. `fresh` is the whole of
  // the difference the two buttons make: false resumes the CLI session the
  // first attempt left behind — the run carries on — and true drops it, so the
  // task is done again from nothing.
  //
  // Either way the run lands in `queued`, not in `running`: capacity is one run
  // per account, so if something is in flight this waits its turn rather than
  // contending with it. That is the queueing the board promises.
  retryCodingTask: (runID: string, fresh = false) =>
    request<Run>(`/coding-tasks/${encodeURIComponent(runID)}/retry`, {
      method: "POST",
      body: { fresh },
    }),
  // Rewrites what a card asks for.
  //
  // A patch: only the fields sent are changed, so a rename does not have to
  // carry the prompt back. The daemon answers 409 for a card that is running or
  // finished — what a run was asked is the record of what was spent.
  editCodingTask: (
    runID: string,
    patch: { title?: string; prompt?: string; model?: string; attachment_ids?: string[] },
  ) =>
    request<Run>(`/coding-tasks/${encodeURIComponent(runID)}`, {
      method: "PATCH",
      body: patch,
    }),
  // Asks the dispatcher to look at the queue again.
  //
  // The daemon pumps its queue when work is released and when a run frees its
  // slot — never when the *account* changes — so a card queued while nothing
  // was connected keeps waiting after the login that could start it. This is
  // the way out, and it answers 409 with the reason when there is still
  // nothing to start with.
  kickQueue: () => request<void>("/coding-tasks/queue/kick", { method: "POST" }),
  // Why the queue is not moving, and when it will be.
  //
  // A spent token budget is the one interruption nobody can act on: the daemon
  // parks the run it cut off, holds the slot, and restarts the pipeline itself
  // when the window rolls over. `holds` is what it is waiting on right now,
  // `log` is the durable record — including the pauses that ended, which is
  // how an operator sees what happened overnight.
  queueLimits: (limit?: number) =>
    request<LimitReport>(
      `/coding-tasks/queue/limits${limit ? `?limit=${limit}` : ""}`,
    ),
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
  // Writes outreach for the companies the operator ticked. The only route that
  // spends model tokens on a decision somebody actually made — which is why it
  // takes ids and the search route takes a query.
  draftOutreach: (body: OutreachRequest) =>
    request<OutreachResult>("/maps/outreach", { method: "POST", body }),
  // 204, no body — the caller updates its own row optimistically. The channel
  // travels with the place id because a company has one draft per channel and
  // "sent" is a decision about one of them, not about the company.
  setOutreachStatus: (placeID: string, channel: OutreachChannel, status: OutreachStatus) =>
    request<void>("/maps/outreach/status", {
      method: "POST",
      body: { place_id: placeID, channel, status },
    }),

  // The operator's own configuration: the model their lead-gen runs spend by
  // default, and the rule file behind each outreach channel. Distinct from
  // `/coding-models` and `/llm/providers`, which publish constants the binary
  // ships — these read and write files the operator owns.
  settings: () => request<SettingsView>("/settings"),
  // PUT, not PATCH: the pair is one decision. Both empty means "route by
  // class", which is what every run did before this screen existed.
  saveSettings: (provider: string, model: string) =>
    request<SettingsView>("/settings", { method: "PUT", body: { provider, model } }),
  // An empty body is a reset on the daemon's side, not an empty prompt: "I
  // cleared the box" means start over far more often than it means "write with
  // no rules at all".
  saveRule: (channel: OutreachChannel, body: string) =>
    request<OutreachRule>("/settings/rules", { method: "PUT", body: { channel, body } }),
  // A route rather than "send the shipped default back": a client that held a
  // copy of the default is exactly the drift `GET /coding-models` avoids.
  resetRule: (channel: OutreachChannel) =>
    request<OutreachRule>("/settings/rules/reset", { method: "POST", body: { channel } }),

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
  /**
   * The scan permission surface. A PUT of the whole document rather than
   * add/remove routes: the screen holds the list, so two rows removed quickly
   * are one edit and not a race.
   */
  brainScanPolicy: () => request<BrainScanPolicy>("/brain/scan/policy"),
  saveBrainScanPolicy: (policy: { roots: string[]; excludes: string[] }) =>
    request<BrainScanPolicy>("/brain/scan/policy", { method: "PUT", body: policy }),
  resetBrainScanPolicy: () =>
    request<BrainScanPolicy>("/brain/scan/policy/reset", { method: "POST" }),
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
