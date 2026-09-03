import { describe, expect, test } from "vitest";

import {
  ALL_CATEGORIES,
  ALL_REGIONS,
  bucketByCategory,
  contactLines,
  describeRun,
  draftQueue,
  filterCompanies,
  leadsQueryFrom,
  ledgerTotals,
  railFromCounts,
  sortCompanies,
  totals,
} from "./leadgen";
import type { LeadCategoryCount, LeadCompany, LeadgenReport } from "./daemon";

function company(over: Partial<LeadCompany> = {}): LeadCompany {
  return {
    place_id: over.name ?? "p",
    name: "A",
    category: "health",
    ...over,
  } as LeadCompany;
}

describe("the category rail", () => {
  // Largest first, because the rail is where you start looking. "unknown" is
  // last whatever its size: it is the absence of an answer, and a screen that
  // opened on it would suggest the classification worked.
  test("orders buckets by size and pushes unknown last", () => {
    const buckets = bucketByCategory([
      company({ name: "u1", category: "unknown" }),
      company({ name: "u2", category: "unknown" }),
      company({ name: "u3", category: "unknown" }),
      company({ name: "h1", category: "health" }),
      company({ name: "h2", category: "health" }),
      company({ name: "r1", category: "retail" }),
    ]);

    expect(buckets.map((b) => b.category)).toEqual(["health", "retail", "unknown"]);
    expect(buckets[0].count).toBe(2);
    expect(buckets[0].share).toBeCloseTo(2 / 6);
  });

  test("counts the companies with no website — the ones worth calling", () => {
    const buckets = bucketByCategory([
      company({ name: "a", website: "https://a.example" }),
      company({ name: "b" }),
      company({ name: "c" }),
    ]);
    expect(buckets[0].withoutWebsite).toBe(2);
  });

  test("a company with no category counts as unknown, never as its own bucket", () => {
    const buckets = bucketByCategory([company({ name: "a", category: "" })]);
    expect(buckets.map((b) => b.category)).toEqual(["unknown"]);
  });
});

describe("filtering", () => {
  const rows = [
    company({ name: "Kadıköy Diş", address: "Moda Caddesi", category: "health" }),
    company({ name: "Şişli Lokanta", address: "Halaskargazi", category: "restaurant", website: "https://x.example" }),
  ];

  // A Turkish operator types "kadikoy" for "Kadıköy"; a search that misses that
  // is a search that looks broken.
  test("folds Turkish characters both ways", () => {
    expect(filterCompanies(rows, { text: "kadikoy" })).toHaveLength(1);
    expect(filterCompanies(rows, { text: "SIŞLI" })).toHaveLength(1);
    expect(filterCompanies(rows, { text: "moda" })).toHaveLength(1);
  });

  test("the all-categories sentinel is not treated as a category", () => {
    expect(filterCompanies(rows, { category: ALL_CATEGORIES })).toHaveLength(2);
    expect(filterCompanies(rows, { category: "health" })).toHaveLength(1);
  });

  test("only-without-website keeps exactly the companies with none", () => {
    const got = filterCompanies(rows, { onlyWithoutWebsite: true });
    expect(got.map((c) => c.name)).toEqual(["Kadıköy Diş"]);
  });
});

describe("sorting", () => {
  // A list that reshuffles under the cursor is a list nobody trusts, so ties
  // break on name rather than on input order.
  test("is stable on ties", () => {
    const rows = [
      company({ name: "Zeta", rating: 4.5 }),
      company({ name: "Alfa", rating: 4.5 }),
      company({ name: "Beta", rating: 5 }),
    ];
    expect(sortCompanies(rows, "rating").map((c) => c.name)).toEqual(["Beta", "Alfa", "Zeta"]);
    expect(sortCompanies(rows, "name").map((c) => c.name)).toEqual(["Alfa", "Beta", "Zeta"]);
  });

  test("a missing rating sorts last rather than crashing", () => {
    const rows = [company({ name: "A" }), company({ name: "B", rating: 3 })];
    expect(sortCompanies(rows, "rating").map((c) => c.name)).toEqual(["B", "A"]);
  });
});

describe("totals", () => {
  test("counts what the header states", () => {
    const report = {
      companies: [
        company({ name: "a", website: "https://a.example", phone: "0216" }),
        company({ name: "b", category: "retail" }),
      ],
    } as LeadgenReport;

    expect(totals(report)).toMatchObject({
      companies: 2,
      withWebsite: 1,
      withoutWebsite: 1,
      withPhone: 1,
      categories: 2,
      unclassified: false,
    });
  });

  // Everything in "unknown" is not a result, it is a classification that did
  // not run — and the screen has to say so rather than show an empty rail.
  test("flags a run where nothing was classified", () => {
    const report = {
      companies: [company({ name: "a", category: "unknown" }), company({ name: "b", category: "unknown" })],
    } as LeadgenReport;

    expect(totals(report).unclassified).toBe(true);
  });

  test("an empty report is not 'unclassified'", () => {
    expect(totals(null).unclassified).toBe(false);
    expect(totals({ companies: [] } as unknown as LeadgenReport).companies).toBe(0);
  });
});

describe("the draft queue", () => {
  // Undecided first: a queue that keeps finished work at the top makes the
  // operator scroll past it to find the next decision.
  test("puts undecided drafts first and counts the rest", () => {
    const q = draftQueue([
      company({ name: "sent", email: "merhaba", email_status: "sent" }),
      company({ name: "open", email: "merhaba" }),
      company({ name: "skipped", email: "merhaba", email_status: "skipped" }),
      company({ name: "no draft" }),
    ]);

    expect(q.items.map((i) => i.company.name)).toEqual(["open", "sent", "skipped"]);
    expect(q.items[0].index).toBe(1);
    expect(q).toMatchObject({ pending: 1, sent: 1, skipped: 1 });
  });

  test("a company with no draft is not in the queue", () => {
    expect(draftQueue([company({ name: "a", email: "   " })]).items).toHaveLength(0);
  });
});

describe("contact lines", () => {
  test("lists only what is known, and links what can be acted on", () => {
    const lines = contactLines(company({ name: "a", phone: "02161234567", address: "Moda" }));
    expect(lines.map((l) => l.label)).toEqual(["telefon", "adres"]);
    expect(lines[0].href).toBe("tel:02161234567");
    expect(lines[1].href).toBeUndefined();
  });
});

describe("the ledger rail", () => {
  const counts: LeadCategoryCount[] = [
    { category: "unknown", company_count: 9, without_website: 4 },
    { category: "health", company_count: 5, without_website: 2 },
    { category: "retail", company_count: 7, without_website: 0 },
  ];

  // The daemon counts over every saved business; this only orders the answer,
  // by the same rule a run's rail uses — so switching sources does not reorder
  // the rail under the cursor.
  test("orders server counts by size with unknown last", () => {
    const rail = railFromCounts(counts);
    expect(rail.map((b) => b.category)).toEqual(["retail", "health", "unknown"]);
    expect(rail[0].count).toBe(7);
    expect(rail[0].withoutWebsite).toBe(0);
  });

  test("the meter share is of the whole ledger, not of the page", () => {
    const rail = railFromCounts(counts);
    expect(rail[0].share).toBeCloseTo(7 / 21);
  });

  test("no counts is an empty rail, not a divide by zero", () => {
    expect(railFromCounts([])).toEqual([]);
    expect(ledgerTotals([])).toEqual({ companies: 0, withoutWebsite: 0, categories: 0 });
  });

  test("totals sum the server's counts", () => {
    expect(ledgerTotals(counts)).toEqual({ companies: 21, withoutWebsite: 6, categories: 3 });
  });
});

describe("the ledger query", () => {
  // The two sentinels are this screen's, not the daemon's, and sending either
  // as a filter would match nothing.
  test("neither sentinel crosses the wire", () => {
    expect(
      leadsQueryFrom({
        category: ALL_CATEGORIES,
        region: ALL_REGIONS,
        text: "",
        onlyWithoutWebsite: false,
      }),
    ).toEqual({
      category: undefined,
      region: undefined,
      q: undefined,
      without_website: undefined,
    });
  });

  test("carries a real filter through, trimmed", () => {
    expect(
      leadsQueryFrom({
        category: "health",
        region: "Denizli",
        text: "  kadıköy  ",
        onlyWithoutWebsite: true,
      }),
    ).toEqual({
      category: "health",
      region: "Denizli",
      q: "kadıköy",
      without_website: true,
    });
  });

  // No limit is sent on purpose: the page bound is the daemon's constant, and a
  // client that always sent one would quietly become where it lives.
  test("sends no page bound of its own", () => {
    const q = leadsQueryFrom({
      category: ALL_CATEGORIES,
      region: ALL_REGIONS,
      text: "",
      onlyWithoutWebsite: false,
    });
    expect(q).not.toHaveProperty("limit");
    expect(q).not.toHaveProperty("offset");
  });
});

describe("the run picker", () => {
  test("names a run by where it looked, how many it found, and when", () => {
    const label = describeRun({
      id: "run-1",
      query: "Kadıköy diş kliniği",
      region: "Kadıköy",
      company_count: 12,
      ran_at: Date.UTC(2026, 8, 3) / 1000,
    });
    expect(label).toContain("Kadıköy");
    expect(label).toContain("12");
  });

  test("falls back to the query when a run carried no region label", () => {
    const label = describeRun({
      id: "run-2",
      query: "Beşiktaş kuaför",
      company_count: 4,
      ran_at: Date.UTC(2026, 8, 3) / 1000,
    });
    expect(label).toContain("Beşiktaş kuaför");
  });
});
