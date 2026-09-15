import type { CatalogueEntry, Connection } from "./daemon";

/**
 * What the connections list decides, out of the JSX.
 *
 * The list has one job the old provider dropdown did not: it has to say *what
 * a connection is* — who bills for it, how it is reached, and whether two rows
 * are really one wallet. Every one of those has a right answer, so they are
 * here with tests rather than as conditions inside markup.
 */

/** How a connection reads to somebody deciding whether to spend on it. */
export type ConnectionState = "ok" | "signed-out" | "missing" | "disabled" | "unknown";

/**
 * The state, from what the daemon said.
 *
 * `unknown` is a real answer and not a failure: establishing whether a login
 * works costs a model call, so a screen that has not asked must not claim the
 * login is fine. That distinction is the whole reason `probed` exists on the
 * wire.
 */
export function connectionState(c: Connection): ConnectionState {
  if (!c.enabled) return "disabled";
  const a = c.availability;
  if (!a) return "unknown";
  if (!a.installed) return "missing";
  if (a.probed && !a.signed_in) return "signed-out";
  if (a.probed && a.signed_in) return "ok";
  return "unknown";
}

const STATE_LABELS: Record<ConnectionState, string> = {
  ok: "bağlı",
  "signed-out": "oturum kapalı",
  missing: "kurulu değil",
  disabled: "kapalı",
  unknown: "sınanmadı",
};

export function stateLabel(state: ConnectionState): string {
  return STATE_LABELS[state];
}

export function stateTone(state: ConnectionState): "ok" | "warn" | "bad" | "muted" {
  switch (state) {
    case "ok":
      return "ok";
    case "signed-out":
      return "warn";
    case "missing":
      return "bad";
    default:
      return "muted";
  }
}

/** The detail line under a row — the CLI's own sentence, when there is one. */
export function stateDetail(c: Connection): string {
  return c.availability?.detail ?? "";
}

export type VendorGroup = {
  vendor: string;
  label: string;
  connections: Connection[];
  /** True when more than one connection bills the same vendor. */
  shared: boolean;
};

const VENDOR_LABELS: Record<string, string> = {
  anthropic: "Anthropic",
  google: "Google",
  local: "Yerel",
};

export function vendorLabel(vendor: string): string {
  return VENDOR_LABELS[vendor] ?? vendor;
}

/**
 * The rows, grouped by who bills for them.
 *
 * This is the visible payoff of getting the model right. `agy` and `gemini` are
 * two binaries and one company: Antigravity has no subscription of its own, it
 * rides a Google AI plan. A list that draws them as two independent providers
 * tells the operator they have two budgets when they have one — and the moment
 * one of them reports a rate limit, that misreading is expensive.
 *
 * Groups keep the daemon's order; within a group, connections keep theirs.
 */
export function groupByVendor(connections: Connection[]): VendorGroup[] {
  const order: string[] = [];
  const byVendor = new Map<string, Connection[]>();

  for (const c of connections) {
    const vendor = c.vendor || "other";
    if (!byVendor.has(vendor)) {
      byVendor.set(vendor, []);
      order.push(vendor);
    }
    byVendor.get(vendor)?.push(c);
  }

  return order.map((vendor) => {
    const list = byVendor.get(vendor) ?? [];
    return {
      vendor,
      label: vendorLabel(vendor),
      connections: list,
      shared: list.length > 1,
    };
  });
}

/**
 * The sentence a shared group carries.
 *
 * Said out loud rather than implied by the grouping, because "these two share a
 * wallet" is exactly the thing an operator cannot see and will be billed for.
 * Empty when the group is one connection — a note that is always there is a
 * note nobody reads.
 */
export function sharedNote(group: VendorGroup): string {
  if (!group.shared) return "";
  return `Bu bağlantılar aynı ${group.label} hesabından harcayabilir.`;
}

/** The chip that says how a connection is reached. */
export function transportLabel(c: Connection): string {
  return c.transport === "api" ? "API" : "CLI";
}

/**
 * Whether a connection can be chosen for work that needs a schema.
 *
 * Brain's distil sends one and parses what comes back, so a connection that
 * cannot serve a schema answers in prose and the pass stores a node with no
 * assessment. The daemon refuses the pairing; a picker that offers it wastes
 * the operator's afternoon getting to that refusal.
 */
export function canServeSchema(c: Connection): boolean {
  return c.availability?.structured_output ?? false;
}

// --- the catalogue -----------------------------------------------------------

/**
 * What can be added, grouped for offering.
 *
 * Entries already configured are filtered out: the four CLI connections ship as
 * built-ins and appear in the list above, so offering them again would be
 * offering a second name for one login.
 */
export function addable(
  catalogue: CatalogueEntry[],
  configured: Connection[],
): CatalogueEntry[] {
  const have = new Set(configured.map((c) => c.id));
  return catalogue.filter((e) => !have.has(e.id));
}

/** Whether this build can actually connect this entry today. */
export function isConnectable(entry: CatalogueEntry): boolean {
  return entry.status === "available";
}

/**
 * The badge on a catalogue row.
 *
 * "Yakında" is a promise about the product's shape, not about a date, and it
 * carries the entry's own reason underneath rather than a generic one.
 */
export function catalogueBadge(entry: CatalogueEntry): string {
  return isConnectable(entry) ? "eklenebilir" : "yakında";
}

/** How the operator will authenticate it, said before they pick. */
export function authLabel(entry: CatalogueEntry): string {
  return entry.auth === "api-key" ? "API anahtarı" : "CLI oturumu";
}

/**
 * Whether this row should offer a way in.
 *
 * Only when there is something to fix: a connection that is signed out or whose
 * binary is missing. A "connect" button on a working connection is a button
 * that invites somebody to break it.
 */
export function needsConnecting(c: Connection): boolean {
  const state = connectionState(c);
  return state === "signed-out" || state === "missing";
}
