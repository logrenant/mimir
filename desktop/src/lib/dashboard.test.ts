import { describe, expect, test } from "vitest";
import type { Account, Diagnostics, Run, RunStatus } from "./daemon";
import {
  accountLoad,
  dependencyRows,
  formatSpend,
  hasBlockingFault,
  recentlyFinished,
  summarize,
} from "./dashboard";

function run(partial: Partial<Run> & { id: string; status: RunStatus }): Run {
  return { project_id: "p", prompt: "do something", ...partial } as Run;
}

const NOON = new Date("2026-09-02T12:00:00").getTime();
const thisMorning = new Date("2026-09-02T09:30:00").toISOString();
const lastNight = new Date("2026-09-01T23:30:00").toISOString();

describe("summarize", () => {
  test("no runs yet is not the same as no runs, but both count as zero", () => {
    expect(summarize(null).total).toBe(0);
    expect(summarize([]).total).toBe(0);
  });

  test("counts the three states the operator can act on", () => {
    const s = summarize(
      [
        run({ id: "a", status: "running" }),
        run({ id: "b", status: "queued" }),
        run({ id: "c", status: "queued" }),
        run({ id: "d", status: "backlog" }),
        run({ id: "e", status: "completed" }),
      ],
      NOON,
    );
    expect([s.running, s.queued, s.backlog, s.total]).toEqual([1, 2, 1, 5]);
  });

  test("'today' is the operator's day: yesterday's run does not count", () => {
    const s = summarize(
      [
        run({ id: "a", status: "completed", ended_at: thisMorning, cost_usd: 0.4 }),
        run({ id: "b", status: "completed", ended_at: lastNight, cost_usd: 9 }),
      ],
      NOON,
    );
    expect(s.finishedToday).toBe(1);
    expect(s.spendToday).toBeCloseTo(0.4);
  });

  test("a stopped run still spent what it spent", () => {
    const s = summarize(
      [run({ id: "a", status: "stopped", ended_at: thisMorning, cost_usd: 0.12 })],
      NOON,
    );
    expect(s.spendToday).toBeCloseTo(0.12);
    expect(s.failedToday).toBe(0);
  });

  test("the zero timestamp the daemon writes for 'never' is not a date", () => {
    const s = summarize(
      [run({ id: "a", status: "backlog", ended_at: "0001-01-01T00:00:00Z", cost_usd: 5 })],
      NOON,
    );
    expect(s.finishedToday).toBe(0);
    expect(s.spendToday).toBe(0);
  });
});

describe("formatSpend", () => {
  test("a real amount reads as one", () => {
    expect(formatSpend(1.239)).toBe("$1.24");
  });

  test("a nonzero amount never renders as zero", () => {
    expect(formatSpend(0.004)).toBe("<$0.01");
    expect(formatSpend(0)).toBe("$0.00");
  });
});

describe("dependencyRows", () => {
  const diagnostics = {
    daemon: { ok: true, version: "2.3.0", uptime_ms: 1, store: "", projects: 1 },
    dependencies: {
      claude: { ok: true, detail: "" },
      crawl4ai: { ok: false, detail: "not reachable — run `make crawl-up`" },
      maps_scraper: { ok: false, detail: "run `make maps-up`", optional: true },
    },
  } as unknown as Diagnostics;

  test("nothing fetched yet is an empty strip, not a wall of red", () => {
    expect(dependencyRows(null)).toEqual([]);
  });

  test("order is fixed, so rows do not move between polls", () => {
    expect(dependencyRows(diagnostics).map((r) => r.name)).toEqual([
      "Crawl4AI",
      "claude CLI",
      "Maps sidecar",
    ]);
  });

  test("the daemon's own sentence is carried through — it names the fix", () => {
    const crawl = dependencyRows(diagnostics)[0];
    expect(crawl.detail).toContain("make crawl-up");
  });

  test("an optional dependency being down is not a fault", () => {
    expect(hasBlockingFault(dependencyRows(diagnostics))).toBe(true);
    const healthy = dependencyRows({
      ...diagnostics,
      dependencies: {
        claude: { ok: true, detail: "" },
        maps_scraper: { ok: false, detail: "", optional: true },
      },
    } as unknown as Diagnostics);
    expect(hasBlockingFault(healthy)).toBe(false);
  });
});

describe("accountLoad", () => {
  const accounts = [
    { id: "acc-1", label: "birinci", config_dir: "", is_default: true },
    { id: "acc-2", label: "ikinci", config_dir: "/b", is_default: false },
  ] as Account[];

  test("capacity is one: a slot is busy with a run or it is free", () => {
    const load = accountLoad(accounts, [
      run({ id: "a", status: "running", account_id: "acc-1" }),
      run({ id: "b", status: "queued", requested_account_id: "acc-1" }),
    ]);
    expect(load[0].busyWith?.id).toBe("a");
    expect(load[0].waiting).toBe(1);
    expect(load[1].busyWith).toBeNull();
    expect(load[1].waiting).toBe(0);
  });

  test("an unpinned queued card waits for nobody in particular", () => {
    const load = accountLoad(accounts, [run({ id: "b", status: "queued" })]);
    expect(load.every((a) => a.waiting === 0)).toBe(true);
  });

  test("a finished run does not hold a slot", () => {
    const load = accountLoad(accounts, [
      run({ id: "a", status: "completed", account_id: "acc-1" }),
    ]);
    expect(load[0].busyWith).toBeNull();
  });
});

describe("recentlyFinished", () => {
  test("ordered by when it ended, not by when the card was written", () => {
    const finished = recentlyFinished([
      run({ id: "old", status: "completed", ended_at: lastNight }),
      run({ id: "new", status: "failed", ended_at: thisMorning }),
      run({ id: "never", status: "backlog" }),
    ]);
    expect(finished.map((r) => r.id)).toEqual(["new", "old"]);
  });
});
