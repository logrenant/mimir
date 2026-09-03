import type {
  LeadCategoryCount,
  LeadCompany,
  LeadRun,
  LeadgenReport,
  LeadsQuery,
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
  runID: string;
};

export const ALL_RUNS = "__all_runs__";

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
    run_id: filter.runID === ALL_RUNS ? undefined : filter.runID,
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

/** A drafted email, and where it is in the review. */
export type DraftItem = {
  company: LeadCompany;
  /** Position in the queue, 1-based, for "3 / 12". */
  index: number;
};

export type DraftQueue = {
  items: DraftItem[];
  /** Drafts still waiting for a decision — the actual work remaining. */
  pending: number;
  sent: number;
  skipped: number;
};

/**
 * The drafts, in review order: undecided first.
 *
 * A queue that keeps sent drafts at the top makes the operator scroll past
 * finished work to find the next decision. Within each group the search order
 * is kept, so a draft does not move because its neighbour was marked.
 */
export function draftQueue(companies: LeadCompany[]): DraftQueue {
  const drafted = companies.filter((c) => (c.email ?? "").trim() !== "");
  const rank = (c: LeadCompany) => (c.email_status === "sent" || c.email_status === "skipped" ? 1 : 0);

  const ordered = [...drafted].sort((a, b) => rank(a) - rank(b));

  return {
    items: ordered.map((company, i) => ({ company, index: i + 1 })),
    pending: drafted.filter((c) => rank(c) === 0).length,
    sent: drafted.filter((c) => c.email_status === "sent").length,
    skipped: drafted.filter((c) => c.email_status === "skipped").length,
  };
}

/** How a company can be reached, as the detail panel lists it. */
export function contactLines(company: LeadCompany): { label: string; value: string; href?: string }[] {
  const out: { label: string; value: string; href?: string }[] = [];
  if (company.website) out.push({ label: "web", value: company.website, href: company.website });
  if (company.phone) out.push({ label: "telefon", value: company.phone, href: `tel:${company.phone}` });
  if (company.address) out.push({ label: "adres", value: company.address });
  return out;
}
