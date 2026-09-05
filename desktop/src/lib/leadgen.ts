import { CHANNELS } from "./daemon";
import type {
  Draft,
  LeadCategoryCount,
  LeadCompany,
  LeadRegion,
  LeadRun,
  LeadgenReport,
  LeadsQuery,
  OutreachChannel,
  OutreachStatus,
} from "./daemon";

/**
 * The report, read the way the screen asks about it.
 *
 * Everything here is pure and tested, because the interesting decisions in this
 * feature are decisions about *counting* — how many of these companies have no
 * website, which categories are worth showing, how far through the drafts you
 * are — and a decision made inline in JSX is a decision nobody can check.
 */

/**
 * One row of the category rail: the filter, and what it is worth clicking.
 *
 * It carries counts rather than the companies themselves because the rail is
 * fed from two places — a run's companies, which this module groups, and the
 * ledger's counts, which the daemon computed over rows this client never sees.
 * A bucket holding companies could only ever describe the first.
 */
export type CategoryBucket = {
  category: string;
  count: number;
  /** Companies in this bucket with no website at all — the leads worth calling. */
  withoutWebsite: number;
  /** Share of the whole, 0–1, for the rail's meter. */
  share: number;
};

/** The sentinel the rail uses for "no filter". Not a category name. */
export const ALL_CATEGORIES = "__all__";

/**
 * Group companies by category, largest bucket first.
 *
 * Largest first rather than alphabetical: the rail is a place to start looking,
 * and the biggest group is where the work is. "unknown" is pushed last whatever
 * its size — it is the absence of an answer, not an answer, and a screen that
 * opens on it would suggest the classification worked.
 */
export function bucketByCategory(companies: LeadCompany[]): CategoryBucket[] {
  const groups = new Map<string, LeadCompany[]>();
  for (const company of companies) {
    const key = company.category || "unknown";
    groups.set(key, [...(groups.get(key) ?? []), company]);
  }

  const total = companies.length || 1;
  return sortBuckets(
    [...groups.entries()].map(([category, rows]) => ({
      category,
      count: rows.length,
      withoutWebsite: rows.filter((c) => !c.website).length,
      share: rows.length / total,
    })),
  );
}

/**
 * The same rail, built from the daemon's own counts (task-63).
 *
 * The ledger's rail cannot be derived from what is on screen: the table shows
 * one page and the rail has to describe every business ever saved. So the
 * counting happens in SQL and this only orders it — by the same rule
 * bucketByCategory uses, so switching between a run and the ledger does not
 * reorder the rail under the cursor.
 */
export function railFromCounts(counts: LeadCategoryCount[]): CategoryBucket[] {
  const total = counts.reduce((sum, c) => sum + c.company_count, 0) || 1;
  return sortBuckets(
    counts.map((c) => ({
      category: c.category || "unknown",
      count: c.company_count,
      withoutWebsite: c.without_website,
      share: c.company_count / total,
    })),
  );
}

/**
 * Largest first, "unknown" last whatever its size — it is the absence of an
 * answer, not an answer.
 */
function sortBuckets(buckets: CategoryBucket[]): CategoryBucket[] {
  return [...buckets].sort((a, b) => {
    if ((a.category === "unknown") !== (b.category === "unknown")) {
      return a.category === "unknown" ? 1 : -1;
    }
    if (b.count !== a.count) return b.count - a.count;
    return a.category.localeCompare(b.category);
  });
}

/** How many businesses the ledger holds under the current filter. */
export function ledgerTotals(counts: LeadCategoryCount[]): {
  companies: number;
  withoutWebsite: number;
  categories: number;
} {
  return {
    companies: counts.reduce((sum, c) => sum + c.company_count, 0),
    withoutWebsite: counts.reduce((sum, c) => sum + c.without_website, 0),
    categories: counts.filter((c) => c.company_count > 0).length,
  };
}

/** What the ledger view is currently asking for. */
export type LedgerFilter = {
  category: string;
  text: string;
  onlyWithoutWebsite: boolean;
  /**
   * The place, not the crawl. Every search of one region rolls into a single
   * row: a region searched seventeen times is one Denizli, and listing runs
   * put seventeen near-identical rows in front of an operator who has one.
   */
  region: string;
};

export const ALL_REGIONS = "__all_regions__";

/**
 * Turn the screen's filter into the daemon's query.
 *
 * The ledger filters on the server because it is bigger than a page: filtering
 * a rendered page would silently mean "search the two hundred rows you happen
 * to be looking at". The two sentinels never cross the wire.
 */
export function leadsQueryFrom(filter: LedgerFilter): LeadsQuery {
  return {
    category: filter.category === ALL_CATEGORIES ? undefined : filter.category,
    region: filter.region === ALL_REGIONS ? undefined : filter.region,
    q: filter.text.trim() || undefined,
    without_website: filter.onlyWithoutWebsite || undefined,
  };
}

/** How a past run reads in the run picker. */
export function describeRun(run: LeadRun): string {
  const when = new Date(run.ran_at * 1000).toLocaleDateString("tr-TR", {
    day: "2-digit",
    month: "short",
  });
  return `${run.region || run.query} · ${run.company_count} · ${when}`;
}

/**
 * How a place reads in the picker.
 *
 * The phone count is shown because it is the number that decides whether the
 * list is workable: a region of seventy companies with four phone numbers is
 * seventy rows and four leads, and an operator should see that before opening
 * it rather than after scrolling it.
 */
export function describeRegion(region: LeadRegion): string {
  return `${region.region} · ${region.companies} şirket · ${region.with_phone} tel`;
}

export type CompanyFilter = {
  category?: string;
  /** Matched against name and address, case- and diacritic-insensitively. */
  text?: string;
  /** Only companies with no website — the list this whole feature is for. */
  onlyWithoutWebsite?: boolean;
};

/** Turkish needs folding to match: "Kadıköy" typed as "kadikoy" must hit. */
function fold(s: string): string {
  return s
    .toLocaleLowerCase("tr")
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/ı/g, "i")
    .replace(/ş/g, "s")
    .replace(/ğ/g, "g")
    .replace(/ç/g, "c")
    .replace(/ö/g, "o")
    .replace(/ü/g, "u");
}

export function filterCompanies(companies: LeadCompany[], filter: CompanyFilter): LeadCompany[] {
  const needle = fold(filter.text?.trim() ?? "");

  return companies.filter((c) => {
    if (filter.category && filter.category !== ALL_CATEGORIES) {
      if ((c.category || "unknown") !== filter.category) return false;
    }
    if (filter.onlyWithoutWebsite && c.website) return false;
    if (!needle) return true;
    return fold(`${c.name} ${c.address ?? ""}`).includes(needle);
  });
}

export type SortKey = "rating" | "name" | "reviews";

/**
 * Sort, with a stable tie-break on name so two runs of the same data present
 * in the same order — a list that reshuffles under the cursor is a list nobody
 * trusts.
 */
export function sortCompanies(companies: LeadCompany[], key: SortKey): LeadCompany[] {
  const byName = (a: LeadCompany, b: LeadCompany) => a.name.localeCompare(b.name, "tr");

  return [...companies].sort((a, b) => {
    switch (key) {
      case "rating": {
        const diff = (b.rating ?? 0) - (a.rating ?? 0);
        return diff !== 0 ? diff : byName(a, b);
      }
      case "reviews": {
        const diff = (b.review_count ?? 0) - (a.review_count ?? 0);
        return diff !== 0 ? diff : byName(a, b);
      }
      default:
        return byName(a, b);
    }
  });
}

/** The counts the results header states. */
export type ResultTotals = {
  companies: number;
  withWebsite: number;
  withoutWebsite: number;
  withPhone: number;
  categories: number;
  /** True when nothing was classified — every company landed in "unknown". */
  unclassified: boolean;
};

export function totals(report: LeadgenReport | null): ResultTotals {
  const companies = report?.companies ?? [];
  const withWebsite = companies.filter((c) => c.website).length;
  const categories = new Set(companies.map((c) => c.category || "unknown"));

  return {
    companies: companies.length,
    withWebsite,
    withoutWebsite: companies.length - withWebsite,
    withPhone: companies.filter((c) => c.phone).length,
    categories: categories.size,
    unclassified: companies.length > 0 && categories.size === 1 && categories.has("unknown"),
  };
}

/**
 * Why nothing was classified, in the run's own words.
 *
 * The pipeline records a failed classify batch as a note and carries on — the
 * companies are still real. But a screen that then shows one "unknown" bucket
 * and says nothing has quietly turned a broken dependency into what looks like
 * a result, so the header asks for this and prints it.
 */
export function classifyNote(report: LeadgenReport | null): string | null {
  const note = (report?.notes ?? []).find((n) => n.startsWith("classify batch"));
  return note ?? null;
}

/* -------------------------------------------------------------------------- */
/* the selection: which companies get written to                               */
/* -------------------------------------------------------------------------- */

/**
 * The ticked companies, by place id.
 *
 * A set of ids rather than a flag on each row, for one reason that decides the
 * whole feature: the table is filtered and paged on the daemon, so a row that
 * scrolls out of the filter is still a company the operator chose. A boolean
 * living on the row would silently drop those the moment the filter changed,
 * and the operator would send to fewer companies than they ticked without ever
 * being told.
 *
 * The cost of that choice is that the selection can outrun what is on screen,
 * so `summarize` reports the invisible part explicitly rather than hiding it.
 */
export type Selection = ReadonlySet<string>;

export const EMPTY_SELECTION: Selection = new Set<string>();

/** Ticks or unticks one company. */
export function toggleOne(selection: Selection, placeID: string): Selection {
  const next = new Set(selection);
  if (!next.delete(placeID)) next.add(placeID);
  return next;
}

/** Ticks or unticks a list at once — the header checkbox, and shift-click. */
export function setMany(selection: Selection, placeIDs: string[], on: boolean): Selection {
  const next = new Set(selection);
  for (const id of placeIDs) {
    if (on) next.add(id);
    else next.delete(id);
  }
  return next;
}

/**
 * The rows between two clicks, inclusive, in either direction.
 *
 * Shift-click is the one interaction that makes a sixty-row list workable, and
 * it is a range over what is *rendered* — the order the operator sees — not
 * over ids in a set.
 */
export function rangeOf(placeIDs: string[], from: number, to: number): string[] {
  if (from < 0 || to < 0) return [];
  const [lo, hi] = from <= to ? [from, to] : [to, from];
  return placeIDs.slice(lo, hi + 1);
}

/** What the header checkbox shows for the rows currently in the table. */
export type HeaderState = "none" | "some" | "all";

export function headerState(selection: Selection, visible: string[]): HeaderState {
  if (visible.length === 0) return "none";
  let hit = 0;
  for (const id of visible) if (selection.has(id)) hit++;
  if (hit === 0) return "none";
  return hit === visible.length ? "all" : "some";
}

/**
 * What the selection is worth acting on, stated before the click.
 *
 * Every field here answers a question the operator would otherwise have to
 * answer by scrolling: how many companies, how many of them this page can even
 * show me, how many can actually be reached on each channel, and how many
 * already have something written. `messages` is the honest cost — one model
 * call per company per channel — and it is what the button prints.
 */
export type SelectionSummary = {
  total: number;
  /** Ticked rows that are in the table right now. The rest are off-filter. */
  visible: number;
  /** Ticked but not on screen — stated, never hidden. */
  offscreen: number;
  withPhone: number;
  withEmail: number;
  /** Ticked companies that already hold a draft on the chosen channels. */
  alreadyDrafted: number;
  /** Companies × channels: what pressing the button spends. */
  messages: number;
};

export function summarize(
  selection: Selection,
  rows: LeadCompany[],
  channels: OutreachChannel[],
): SelectionSummary {
  const picked = rows.filter((c) => selection.has(c.place_id));

  return {
    total: selection.size,
    visible: picked.length,
    offscreen: Math.max(selection.size - picked.length, 0),
    withPhone: picked.filter((c) => (c.phone ?? "").trim() !== "").length,
    withEmail: picked.filter((c) => (c.email ?? "").trim() !== "").length,
    alreadyDrafted: picked.filter((c) => channels.some((ch) => draftFor(c, ch))).length,
    messages: selection.size * Math.max(channels.length, 1),
  };
}

/**
 * The channels a run should ask for, keeping the daemon's order.
 *
 * Toggling is a set operation and a set has no order, but the daemon drafts one
 * channel at a time in the order it is given, and an operator who ticked
 * WhatsApp first should not get a different sequence of notes for it.
 */
export function toggleChannel(
  channels: OutreachChannel[],
  channel: OutreachChannel,
): OutreachChannel[] {
  const next = new Set(channels);
  if (!next.delete(channel)) next.add(channel);
  return CHANNELS.filter((c) => next.has(c));
}

/* -------------------------------------------------------------------------- */
/* the drafts                                                                  */
/* -------------------------------------------------------------------------- */

/** One company's draft on one channel, or undefined. */
export function draftFor(company: LeadCompany, channel: OutreachChannel): Draft | undefined {
  return (company.drafts ?? []).find((d) => d.channel === channel);
}

/** A drafted message, and where it is in the review. */
export type DraftItem = {
  company: LeadCompany;
  draft: Draft;
  /** Position in the queue, 1-based, for "3 / 12". */
  index: number;
};

export type DraftQueue = {
  channel: OutreachChannel;
  items: DraftItem[];
  /** Drafts still waiting for a decision — the actual work remaining. */
  pending: number;
  sent: number;
  skipped: number;
};

/**
 * The drafts on one channel, in review order: undecided first.
 *
 * One queue per channel rather than one list of everything, because a decision
 * is per channel — sending the email and skipping the WhatsApp line is an
 * ordinary thing to decide — and a mixed list would make the operator read the
 * medium off each row before knowing what "Gönderildi" would mean.
 *
 * A queue that keeps sent drafts at the top makes the operator scroll past
 * finished work to find the next decision. Within each group the search order
 * is kept, so a draft does not move because its neighbour was marked.
 */
export function draftQueue(companies: LeadCompany[], channel: OutreachChannel): DraftQueue {
  const rows: { company: LeadCompany; draft: Draft }[] = [];
  for (const company of companies) {
    const draft = draftFor(company, channel);
    if (draft && draft.body.trim() !== "") rows.push({ company, draft });
  }

  const decided = (d: Draft) => (d.status === "sent" || d.status === "skipped" ? 1 : 0);
  const ordered = [...rows].sort((a, b) => decided(a.draft) - decided(b.draft));

  return {
    channel,
    items: ordered.map((row, i) => ({ ...row, index: i + 1 })),
    pending: rows.filter((r) => decided(r.draft) === 0).length,
    sent: rows.filter((r) => r.draft.status === "sent").length,
    skipped: rows.filter((r) => r.draft.status === "skipped").length,
  };
}

/** How many drafts each channel holds — the count on the channel tabs. */
export function draftCounts(companies: LeadCompany[]): Record<OutreachChannel, number> {
  const out = { email: 0, whatsapp: 0 } as Record<OutreachChannel, number>;
  for (const company of companies) {
    for (const draft of company.drafts ?? []) {
      if (draft.body.trim() !== "") out[draft.channel] = (out[draft.channel] ?? 0) + 1;
    }
  }
  return out;
}

/**
 * Writes one channel's draft back onto a company, replacing any earlier draft
 * on that channel. Mirrors `CompanyLead.setDraft` in the pipeline: a re-draft
 * must not leave two rows for one message.
 */
export function withDraft(company: LeadCompany, draft: Draft): LeadCompany {
  const rest = (company.drafts ?? []).filter((d) => d.channel !== draft.channel);
  return { ...company, drafts: [...rest, draft] };
}

/** Records a decision on one company's draft, leaving the other channel alone. */
export function withDraftStatus(
  company: LeadCompany,
  channel: OutreachChannel,
  status: OutreachStatus,
): LeadCompany {
  return {
    ...company,
    drafts: (company.drafts ?? []).map((d) => (d.channel === channel ? { ...d, status } : d)),
  };
}

/**
 * Merges what a draft run returned into the rows already on screen.
 *
 * The run answers with the companies it wrote for, which is a subset of the
 * table — everything else has to stay exactly as it was, including the drafts
 * it already had on the channel this run did not ask for.
 */
export function mergeDrafts<T extends LeadCompany>(rows: T[], written: LeadCompany[]): T[] {
  const byID = new Map(written.map((c) => [c.place_id, c]));
  return rows.map((row) => {
    const hit = byID.get(row.place_id);
    if (!hit) return row;
    let next = row;
    for (const draft of hit.drafts ?? []) next = withDraft(next, draft) as T;
    // The run also resolves the address the ledger holds; keep it if it found
    // one and the row had none.
    return hit.email && !next.email ? { ...next, email: hit.email } : next;
  });
}

/**
 * A wa.me address for a Turkish number, or undefined when the shape is not one
 * this can be sure about.
 *
 * Undefined rather than a best guess: a wrong number here opens a chat with a
 * stranger, and the plain number the panel already shows is a perfectly good
 * fallback. Only the two forms Google Maps actually returns for Turkey are
 * accepted — a national number with a leading zero, and one already carrying
 * the +90 country code.
 */
export function whatsappHref(phone: string | undefined): string | undefined {
  const digits = (phone ?? "").replace(/\D/g, "");
  if (digits === "") return undefined;

  // +90 5xx xxx xx xx / 90 216 xxx xx xx — 12 digits, country code included.
  if (digits.length === 12 && digits.startsWith("90")) return `https://wa.me/${digits}`;
  // 0216 xxx xx xx — 11 digits, national form.
  if (digits.length === 11 && digits.startsWith("0")) return `https://wa.me/90${digits.slice(1)}`;
  return undefined;
}

/** How a company can be reached, as the detail panel lists it. */
export function contactLines(company: LeadCompany): { label: string; value: string; href?: string }[] {
  const out: { label: string; value: string; href?: string }[] = [];
  if (company.website) out.push({ label: "web", value: company.website, href: company.website });
  if (company.phone) out.push({ label: "telefon", value: company.phone, href: `tel:${company.phone}` });
  if (company.address) out.push({ label: "adres", value: company.address });
  return out;
}
