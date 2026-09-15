import { describe, expect, test } from "vitest";
import type { LimitReport, Run, RunStatus } from "./daemon";
import {
  activeCatalogRun,
  catalogRunLine,
  catalogRunsFor,
  lastCatalogRun,
  actionsFor,
  allowedMove,
  canContinue,
  canEdit,
  cardTitle,
  catalogParams,
  catalogSummary,
  columnOf,
  elapsedLabel,
  groupRuns,
  heldLabel,
  heldUntil,
  isStalled,
  catalogSelection,
  modelControlFor,
  pollInterval,
  retryOutcome,
  withCatalogSelection,
} from "./board";

function run(partial: Partial<Run> & { id: string; status: RunStatus }): Run {
  return { project_id: "p", prompt: "do something", ...partial } as Run;
}

describe("columns", () => {
  test("every run status lands in exactly one column", () => {
    const statuses: RunStatus[] = ["backlog", "queued", "running", "completed", "failed", "stopped"];
    for (const status of statuses) {
      expect(columnOf(status)).not.toBeNull();
    }
  });

  test("a stopped run sits with the failures, where 'did not finish' is looked for", () => {
    expect(columnOf("stopped")).toBe("failed");
  });

  test("queued keeps its queue order, because the dispatcher takes the oldest first", () => {
    const grouped = groupRuns([
      run({ id: "b", status: "queued", queued_at: "2026-09-01T10:05:00Z" }),
      run({ id: "a", status: "queued", queued_at: "2026-09-01T10:00:00Z" }),
    ]);
    expect(grouped.queued.map((r) => r.id)).toEqual(["a", "b"]);
  });
});

describe("allowedMove", () => {
  test("the two operator columns exchange cards", () => {
    expect(allowedMove("backlog", "queued")).toEqual({ allowed: true, action: "enqueue" });
    expect(allowedMove("queued", "backlog")).toEqual({ allowed: true, action: "dequeue" });
  });

  test("running, done and failed are the runner's to write, and say so", () => {
    for (const target of ["running", "done", "failed"] as const) {
      const move = allowedMove("backlog", target);
      expect(move.allowed).toBe(false);
      // A drop that silently springs back reads as a broken board, not a rule.
      if (!move.allowed) expect(move.reason.length).toBeGreaterThan(0);
    }
  });

  test("a failure dropped back in the queue is a retry", () => {
    expect(allowedMove("failed", "queued")).toEqual({ allowed: true, action: "retry" });
  });

  test("but there is no un-failing a card into the backlog", () => {
    const move = allowedMove("failed", "backlog");
    expect(move.allowed).toBe(false);
    if (!move.allowed) expect(move.reason).toContain("Queued");
  });

  test("and a finished card has nothing left to ask for", () => {
    const move = allowedMove("done", "queued");
    expect(move.allowed).toBe(false);
    if (!move.allowed) expect(move.reason.length).toBeGreaterThan(0);
  });

  test("a drop back onto the same column is a no-op, not a complaint", () => {
    const move = allowedMove("queued", "queued");
    expect(move.allowed).toBe(false);
    if (!move.allowed) expect(move.reason).toBe("");
  });
});

describe("actionsFor", () => {
  test("a backlog card can be run or thrown away, but not stopped", () => {
    expect(actionsFor("backlog")).toEqual(["run", "edit", "delete"]);
  });

  test("a queued card can be dequeued, watched, and can ask the queue why it is waiting", () => {
    expect(actionsFor("queued")).toEqual(["kick", "edit", "dequeue", "terminal"]);
  });

  test("a running card can be stopped and watched, never deleted underneath itself", () => {
    expect(actionsFor("running")).toContain("stop");
    expect(actionsFor("running")).not.toContain("delete");
  });
});

describe("pollInterval", () => {
  test("backs right off when nothing is moving", () => {
    expect(pollInterval([run({ id: "a", status: "completed" })])).toBe(15000);
  });

  test("and speeds up while anything is queued or in flight", () => {
    expect(pollInterval([run({ id: "a", status: "queued" })])).toBe(2000);
    expect(pollInterval([run({ id: "a", status: "running" })])).toBe(2000);
  });
});

describe("cardTitle", () => {
  test("prefers the title the operator wrote", () => {
    expect(cardTitle(run({ id: "a", status: "backlog", title: "fix the board" }))).toBe("fix the board");
  });

  test("falls back to the first non-empty line of the prompt", () => {
    expect(cardTitle(run({ id: "a", status: "backlog", prompt: "\n\nadd a test\nthen run it" }))).toBe(
      "add a test",
    );
  });
});

describe("elapsedLabel", () => {
  test("counts from the start, in mm:ss", () => {
    const started = "2026-09-01T10:00:00Z";
    expect(elapsedLabel(started, Date.parse(started) + 91_000)).toBe("01:31");
  });

  test("Go's zero time is not a start time", () => {
    // encoding/json marshals a zero time.Time as year 1 rather than omitting
    // it, so a backlog card still carries a started_at string.
    expect(elapsedLabel("0001-01-01T00:00:00Z", Date.now())).toBe("");
  });
});

describe("picking a failed run back up", () => {
  test("a failed or stopped card offers both ways of going again", () => {
    for (const status of ["failed", "stopped"] as const) {
      expect(actionsFor(status)).toContain("continue");
      expect(actionsFor(status)).toContain("retry");
    }
  });

  test("continue leads, because it is the one that keeps the work already done", () => {
    const actions = actionsFor("failed");
    expect(actions.indexOf("continue")).toBeLessThan(actions.indexOf("retry"));
  });

  test("a completed run has nothing left to pick up", () => {
    expect(actionsFor("completed")).not.toContain("continue");
    expect(actionsFor("completed")).not.toContain("retry");
  });

  test("a backlog card is Enqueue's business, not a retry's", () => {
    expect(actionsFor("backlog")).toEqual(["run", "edit", "delete"]);
  });

  test("continue needs a session to resume — without one it would silently start over", () => {
    expect(canContinue(run({ id: "a", status: "failed", session_id: "sess-1" }))).toBe(true);
    expect(canContinue(run({ id: "b", status: "failed" }))).toBe(false);
    expect(canContinue(run({ id: "c", status: "failed", session_id: "   " }))).toBe(false);
  });

  test("only a run that stopped short can be continued", () => {
    expect(canContinue(run({ id: "d", status: "completed", session_id: "sess-1" }))).toBe(false);
    expect(canContinue(run({ id: "e", status: "running", session_id: "sess-1" }))).toBe(false);
    expect(canContinue(run({ id: "f", status: "stopped", session_id: "sess-1" }))).toBe(true);
  });

  test("a retry waits when something is already in flight — capacity is one run", () => {
    expect(retryOutcome([run({ id: "a", status: "running" })])).toBe("queued");
    expect(retryOutcome([run({ id: "a", status: "queued" })])).toBe("starts");
    expect(retryOutcome([run({ id: "a", status: "completed" })])).toBe("starts");
    expect(retryOutcome([])).toBe("starts");
  });

  test("a board that has not loaded yet does not claim the queue is busy", () => {
    expect(retryOutcome(null)).toBe("starts");
  });
});

describe("isStalled", () => {
  const at = Date.parse("2026-09-01T10:00:00Z");
  const queued = run({ id: "a", status: "queued", queued_at: "2026-09-01T10:00:00Z" });

  test("a card that has just been queued is waiting its turn, not stuck", () => {
    expect(isStalled(queued, [queued], at + 5_000)).toBe(false);
  });

  test("nor is one waiting behind a run that is actually going", () => {
    const runs = [queued, run({ id: "b", status: "running" })];
    expect(isStalled(queued, runs, at + 600_000)).toBe(false);
  });

  test("but a card queued long ago with nothing running is a queue that stopped", () => {
    expect(isStalled(queued, [queued], at + 600_000)).toBe(true);
  });

  test("and only queued cards can stall", () => {
    expect(isStalled(run({ id: "c", status: "failed" }), null, at)).toBe(false);
  });
});

describe("canEdit", () => {
  test("intent is editable — including the failed card a fix is worth making to", () => {
    for (const status of ["backlog", "queued", "failed", "stopped"] as RunStatus[]) {
      expect(canEdit(status)).toBe(true);
      expect(actionsFor(status)).toContain("edit");
    }
  });

  test("but what was spent is a record, not a field", () => {
    for (const status of ["running", "completed"] as RunStatus[]) {
      expect(canEdit(status)).toBe(false);
      expect(actionsFor(status)).not.toContain("edit");
    }
  });
});

describe("actionsFor, by sub-agent", () => {
  // Resuming means handing `claude --resume` a session id. A sub-agent that
  // spends no CLI session has none, so the button would promise something the
  // daemon cannot do.
  test("does not offer 'continue' for an agent that leaves no session behind", () => {
    expect(actionsFor("failed", "leadgen")).not.toContain("continue");
    expect(actionsFor("stopped", "leadgen")).not.toContain("continue");
  });

  test("still offers 'baştan dene', because starting over always works", () => {
    expect(actionsFor("failed", "leadgen")).toContain("retry");
  });

  test("keeps every other action, which is a property of the status alone", () => {
    expect(actionsFor("backlog", "leadgen")).toEqual(["run", "edit", "delete"]);
    expect(actionsFor("running", "leadgen")).toContain("stop");
  });

  // A card written before sub-agents existed carries no agent, and it was the
  // coding one.
  test("treats a card with no agent as the coding agent", () => {
    expect(actionsFor("failed", undefined)).toContain("continue");
    expect(actionsFor("failed", "")).toContain("continue");
    expect(actionsFor("failed", "coding")).toContain("continue");
  });

  test("offers 'continue' for the other claude-backed agents", () => {
    for (const agent of ["review", "marketing", "graph"]) {
      expect(actionsFor("failed", agent)).toContain("continue");
    }
  });
});

// --- katalog kartı ve park rozeti (task-88) ---

/** A card, with the two fields every Run must carry already filled in. */
function card(over: Partial<Run> = {}): Run {
  return run({ id: "r1", status: "queued", ...over });
}

describe("catalogParams", () => {
  test("reads what the card will rewrite, from the card itself", () => {
    // Never from a second request: task-80 removed the board's 1+N fan-out, and
    // a card body that fetched its own import would put it back one card at a
    // time.
    const got = catalogParams(
      card({
        agent: "catalog",
        params: '{"import_id":"imp_1","product_ids":["a","b"],"research":false}',
      }),
    );
    expect(got?.import_id).toBe("imp_1");
    expect(got?.product_ids).toHaveLength(2);
  });

  test("draws nothing for a card it cannot decode", () => {
    // A card whose params will not parse is one the daemon is about to refuse
    // with a reason; guessing here would put a second, wronger reason on it.
    expect(catalogParams(card({ agent: "catalog", params: "{" }))).toBeNull();
    expect(catalogParams(card({ agent: "catalog", params: "{}" }))).toBeNull();
    expect(
      catalogParams(card({ agent: "leadgen", params: '{"import_id":"i","product_ids":[]}' })),
    ).toBeNull();
  });
});

describe("catalogSummary", () => {
  test("says how much work the card is", () => {
    expect(
      catalogSummary(
        card({ agent: "catalog", params: '{"import_id":"i","product_ids":["a","b","c"]}' }),
      ),
    ).toBe("3 ürün");
  });

  test("names the two things that change what a pass costs", () => {
    const got = catalogSummary(
      card({
        agent: "catalog",
        params:
          '{"import_id":"i","product_ids":["a"],"research":false,"fields":["title","tags"]}',
      }),
    );
    expect(got).toContain("araştırmasız");
    expect(got).toContain("2 alan");
  });
});

describe("heldUntil", () => {
  const at = "2026-09-07T19:40:00Z";
  const workerHold: LimitReport = {
    holds: [{ account_id: "worker-lane", since: at, resets_at: at }],
    log: [],
  };

  // A parked card and a card that has simply not started are both `queued`, and
  // an operator who cannot tell them apart reads an overnight pause as a hang.
  test("separates a parked card from one that is merely waiting its turn", () => {
    const parked = card({ id: "r1", agent: "catalog", status: "queued" });
    expect(heldUntil(parked, workerHold)).toBe(at);
    expect(heldUntil(parked, { holds: [], log: [] })).toBeNull();
    expect(heldUntil(parked, null)).toBeNull();
  });

  test("a running card is not waiting on anything", () => {
    expect(heldUntil(card({ id: "r1", agent: "catalog", status: "running" }), workerHold)).toBeNull();
  });

  // Reporting one lane as blocked by the other would send the operator to look
  // at a limit that has no bearing on it.
  test("a coding card is not held by the worker lane's budget", () => {
    const coding = card({ agent: "coding", status: "queued", account_id: "acc_1" });
    expect(heldUntil(coding, workerHold)).toBeNull();

    const accountHold: LimitReport = {
      holds: [{ account_id: "acc_1", since: at, resets_at: at }],
      log: [],
    };
    expect(heldUntil(coding, accountHold)).toBe(at);
    // And the catalog card is not held by a credential slot it never took.
    expect(heldUntil(card({ id: "r1", agent: "catalog", status: "queued" }), accountHold)).toBeNull();
  });

  test("a hold with no slot at all matches nothing", () => {
    const anonymous: LimitReport = {
      holds: [{ account_id: "", since: at, resets_at: at }],
      log: [],
    };
    expect(heldUntil(card({ id: "r1", agent: "coding", status: "queued" }), anonymous)).toBeNull();
  });
});

describe("heldLabel", () => {
  test("says when the work carries on, because that is the only useful answer", () => {
    const at = "2026-09-07T19:40:00Z";
    const label = heldLabel(card({ id: "r1", agent: "catalog", status: "queued" }), {
      holds: [{ account_id: "worker-lane", since: at, resets_at: at }],
      log: [],
    });
    expect(label).toContain("limit");
    expect(label).toMatch(/\d{2}:\d{2}/);
  });

  test("is empty for a card nothing is holding", () => {
    expect(heldLabel(card({ id: "r1", status: "queued" }), { holds: [], log: [] })).toBe("");
  });

  test("degrades to the bare word when the daemon sent a time it cannot parse", () => {
    expect(
      heldLabel(card({ id: "r1", agent: "catalog", status: "queued" }), {
        holds: [{ account_id: "worker-lane", since: "x", resets_at: "x" }],
        log: [],
      }),
    ).toBe("limit");
  });
});

describe("the catalog's view of the board", () => {
  const card = (
    id: string,
    status: RunStatus,
    importID: string,
    extra: Partial<Run> = {},
  ): Run =>
    run({
      id,
      status,
      agent: "catalog",
      params: JSON.stringify({ import_id: importID, product_ids: ["a", "b", "c"] }),
      ...extra,
    });

  test("finds only this import's cards, and never another agent's", () => {
    const runs = [
      card("1", "running", "imp_1"),
      card("2", "completed", "imp_2"),
      run({ id: "3", status: "running", agent: "leadgen", params: '{"region":"x"}' }),
      run({ id: "4", status: "running" }),
    ];
    expect(catalogRunsFor(runs, "imp_1").map((r) => r.id)).toEqual(["1"]);
    expect(catalogRunsFor(runs, "")).toEqual([]);
    expect(catalogRunsFor(null, "imp_1")).toEqual([]);
  });

  test("the active pass is the live one, newest first", () => {
    const runs = [
      card("old", "completed", "imp_1", { ended_at: "2026-09-01T10:00:00Z" }),
      card("live", "running", "imp_1", { started_at: "2026-09-01T11:00:00Z" }),
      card("waiting", "queued", "imp_1", { queued_at: "2026-09-01T10:30:00Z" }),
    ];
    expect(activeCatalogRun(runs, "imp_1")?.id).toBe("live");
    // And nothing live is not a failure — it is a screen with nothing to say.
    expect(activeCatalogRun([card("old", "completed", "imp_1")], "imp_1")).toBeNull();
  });

  // "Did that work?" is answered by the last pass whatever became of it, which
  // is the question an operator has after pressing the button and looking away.
  test("the last pass is the most recent of any status", () => {
    const runs = [
      card("a", "completed", "imp_1", { ended_at: "2026-09-01T10:00:00Z" }),
      card("b", "failed", "imp_1", { ended_at: "2026-09-01T12:00:00Z" }),
    ];
    expect(lastCatalogRun(runs, "imp_1")?.id).toBe("b");
  });

  describe("catalogRunLine", () => {
    test("says what the pass is and what it is doing", () => {
      expect(catalogRunLine(card("1", "running", "imp_1"), null)).toBe("3 ürün · yazılıyor");
      expect(catalogRunLine(card("1", "queued", "imp_1"), null)).toBe("3 ürün · sırada");
      expect(catalogRunLine(card("1", "completed", "imp_1"), null)).toBe("3 ürün · bitti");
      expect(catalogRunLine(card("1", "failed", "imp_1"), null)).toBe("3 ürün · başarısız");
      expect(catalogRunLine(null, null)).toBe("");
    });

    // A parked card reads as parked, the same way it does on the board — and
    // through `heldUntil`, never through a guess at `run.error`, which for a
    // parked row is the provider's sentence about a budget rather than a
    // statement about this card.
    test("a parked card says it is parked, not that it is waiting its turn", () => {
      const limits: LimitReport = {
        holds: [{ account_id: "worker-lane", since: "", resets_at: "2026-09-01T19:40:00Z" }],
      } as LimitReport;
      const line = catalogRunLine(card("1", "queued", "imp_1"), limits);
      expect(line).toContain("3 ürün · limit");
      expect(line).not.toContain("sırada");
    });
  });
});


// --- hangi modeli harcıyor (kartın kendi seçimi) ---

describe("a catalog card's own model", () => {
  const catalogCard = (params: object) =>
    card({ agent: "catalog", params: JSON.stringify(params) });

  test("is read from the params and not from the run's model column", () => {
    // The column is rewritten with whatever the *last* attempt spent, so a card
    // re-pointed and not yet re-run would otherwise show the old model.
    const run = catalogCard({
      import_id: "imp_1",
      product_ids: ["a"],
      provider: "claude",
      model: "claude-sonnet-5",
    });
    expect(catalogSelection({ ...run, model: "qwen3:8b" })).toEqual({
      provider: "claude",
      model: "claude-sonnet-5",
    });
  });

  test("is empty for a card that names none, which is the saved default", () => {
    expect(catalogSelection(catalogCard({ import_id: "imp_1", product_ids: ["a"] }))).toEqual({
      provider: "",
      model: "",
    });
  });

  describe("withCatalogSelection", () => {
    const run = catalogCard({
      import_id: "imp_1",
      product_ids: ["a", "b"],
      fields: ["title"],
      lang: "ar",
      research: false,
    });

    // The route takes params whole, so everything the card already says has to
    // come back with it — including keys this build has never heard of.
    test("carries the rest of the card through untouched", () => {
      const next = withCatalogSelection(run, "claude", "claude-sonnet-5");
      expect(next).toEqual({
        import_id: "imp_1",
        product_ids: ["a", "b"],
        fields: ["title"],
        lang: "ar",
        research: false,
        provider: "claude",
        model: "claude-sonnet-5",
      });
    });

    // Clearing the provider is "follow the saved choice again", which is an
    // absent key rather than an empty one: the daemon reads absence as routed.
    test("an empty provider drops the pair rather than pinning an empty one", () => {
      const pinned = catalogCard({
        import_id: "imp_1",
        product_ids: ["a"],
        provider: "ollama",
        model: "qwen3:8b",
      });
      const next = withCatalogSelection(pinned, "", "");
      expect(next).not.toHaveProperty("provider");
      expect(next).not.toHaveProperty("model");
      expect(next?.import_id).toBe("imp_1");
    });

    test("refuses a card whose params it cannot read", () => {
      expect(withCatalogSelection(card({ agent: "catalog", params: "{" }), "claude", "x")).toBeNull();
      expect(withCatalogSelection(card({ agent: "coding" }), "claude", "x")).toBeNull();
    });
  });

  // The control an operator is offered has to be wired to the pass they are
  // looking at. Offering the coding-model dropdown for a catalog card is the
  // whole of "I changed the model and nothing changed".
  test("each kind of card is offered the control that is actually wired", () => {
    expect(modelControlFor(card({ agent: "catalog" }))).toBe("catalog");
    expect(modelControlFor(card({ agent: "leadgen" }))).toBe("daemon");
    expect(modelControlFor(card({ agent: "coding" }))).toBe("coding");
    expect(modelControlFor(card({ agent: "review" }))).toBe("coding");
    expect(modelControlFor(card({}))).toBe("coding");
  });
});
