import { describe, expect, test } from "vitest";

import {
  anyDirty,
  describeRule,
  describeSelection,
  draftsFrom,
  isDirty,
  isSavableSelection,
  modelDraftFrom,
  modelForProvider,
  type ModelDraft,
  isSelectable,
  availabilityNote,
  modelsFor,
  classesDirty,
  classDraftFrom,
  type ClassDraft,
  dirtySections,
  sectionLabel,
  SETTINGS_SECTIONS,
} from "./settings";
import type { LLMProviderList, OutreachRule, SettingsView } from "./daemon";

const providers: LLMProviderList = {
  providers: [
    {
      id: "claude",
      label: "Claude CLI",
      default_model: "claude-opus-5",
      models: [
        { id: "claude-opus-5", label: "Opus 5" },
        { id: "claude-sonnet-5", label: "Sonnet 5" },
      ],
    },
    {
      id: "agy",
      label: "Gemini CLI",
      default_model: "gemini-3.1-pro-high",
      models: [{ id: "gemini-3.1-pro-high", label: "Gemini 3.1 Pro" }],
    },
  ],
  routed: { provider: "claude", model: "claude-opus-5" },
};

function rule(over: Partial<OutreachRule> = {}): OutreachRule {
  return {
    channel: "email",
    label: "E-posta",
    path: "/s/rules/email.md",
    body: "# kural",
    is_default: true,
    ...over,
  };
}

function view(over: Partial<SettingsView> = {}): SettingsView {
  return {
    provider: "",
    model: "",
    routed: { provider: "claude", model: "claude-opus-5" },
    distill: {},
    reason: {},
    rules: [rule(), rule({ channel: "whatsapp", label: "WhatsApp" })],
    ...over,
  };
}

describe("the rule editors", () => {
  test("a freshly loaded file is not dirty", () => {
    const drafts = draftsFrom(view().rules);
    expect(drafts.map(isDirty)).toEqual([false, false]);
  });

  test("an edit is dirty until it matches what was loaded again", () => {
    const [email] = draftsFrom(view().rules);
    expect(isDirty({ ...email, body: "# başka" })).toBe(true);
    // Typing a change and undoing it by hand is not a change.
    expect(isDirty({ ...email, body: email.saved })).toBe(false);
  });

  // Two files behind two tabs: the save bar has to know about the tab nobody
  // is looking at, or an edit on the other channel is lost silently.
  test("anyDirty sees the tab that is not on screen", () => {
    const drafts = draftsFrom(view().rules);
    const model: ModelDraft = { provider: "", model: "", saved: { provider: "", model: "" } };

    expect(anyDirty(drafts, model, clean)).toBe(false);
    expect(anyDirty([drafts[0], { ...drafts[1], body: "# yeni" }], model, clean)).toBe(true);
  });

  test("anyDirty also sees an unsaved model choice", () => {
    const drafts = draftsFrom(view().rules);
    expect(
      anyDirty(
        drafts,
        { provider: "agy", model: "gemini-3.1-pro-high", saved: { provider: "", model: "" } },
        clean,
      ),
    ).toBe(true);
  });

  // The regression. The save bar armed on `anyDirty || classesDirty` while
  // `save()` branched on `modelDirty` alone, so editing only a class default
  // wrote nothing and then said "Kaydedildi." The two rules are one function
  // now, and this is the test that keeps them one.
  test("anyDirty sees a class default nobody else was watching", () => {
    const drafts = draftsFrom(view().rules);
    const model: ModelDraft = { provider: "", model: "", saved: { provider: "", model: "" } };

    expect(
      anyDirty(drafts, model, {
        distill: { provider: "gemini", model: "gemini-2.5-flash" },
        reason: {},
        saved: { distill: {}, reason: {} },
      }),
    ).toBe(true);
  });
});

describe("classesDirty", () => {
  test("an untouched pair is not a change", () => {
    expect(classesDirty(clean)).toBe(false);
    expect(
      classesDirty({
        distill: { provider: "agy", model: "x" },
        reason: {},
        saved: { distill: { provider: "agy", model: "x" }, reason: {} },
      }),
    ).toBe(false);
  });

  // Undefined and "" are the same absence. Treating them as different would
  // arm the save bar on a page nobody touched.
  test("an absent field and an empty one are the same absence", () => {
    expect(
      classesDirty({
        distill: { provider: "", model: "" },
        reason: {},
        saved: { distill: {}, reason: {} },
      }),
    ).toBe(false);
  });

  test("either class counts", () => {
    expect(
      classesDirty({
        distill: {},
        reason: { provider: "claude", model: "claude-opus-5" },
        saved: { distill: {}, reason: {} },
      }),
    ).toBe(true);
  });

  test("classDraftFrom seeds both halves from the daemon's answer", () => {
    const d = classDraftFrom(view({ distill: { provider: "agy", model: "m" } }));
    expect(d.distill.provider).toBe("agy");
    expect(classesDirty(d)).toBe(false);
  });
});

const clean: ClassDraft = { distill: {}, reason: {}, saved: { distill: {}, reason: {} } };

describe("the model choice", () => {
  test("switching provider names that provider's default explicitly", () => {
    // Not "": the model control has no empty option, so a cleared value would
    // display whichever model is listed first while a run spent a different one.
    expect(modelForProvider(providers, "agy")).toBe("gemini-3.1-pro-high");
    expect(modelForProvider(providers, "claude")).toBe("claude-opus-5");
  });

  test("clearing the provider clears the model — the two travel together", () => {
    expect(modelForProvider(providers, "")).toBe("");
  });

  test("a provider the daemon never published has no model to offer", () => {
    expect(modelForProvider(providers, "ghost")).toBe("");
    expect(modelForProvider(null, "claude")).toBe("");
  });

  // Both empty is legal and means "route by class". A provider with no model is
  // not, and the screen should not offer a save that comes back as a 400.
  test("a half-made choice is not savable", () => {
    expect(isSavableSelection("", "")).toBe(true);
    expect(isSavableSelection("agy", "gemini-3.1-pro-high")).toBe(true);
    expect(isSavableSelection("agy", "")).toBe(false);
  });

  test("the draft opens on what is saved", () => {
    expect(modelDraftFrom(view({ provider: "agy", model: "gemini-3.1-pro-high" }))).toEqual({
      provider: "agy",
      model: "gemini-3.1-pro-high",
      saved: { provider: "agy", model: "gemini-3.1-pro-high" },
    });
  });
});

describe("what a run will spend, in words", () => {
  // The crash. A provider whose models are discovered rather than pinned
  // publishes none, so `models` is absent — and `provider?.models.find(...)`
  // compiled, shipped, and took the settings screen down the first time a class
  // default named ollama.
  test("a provider with no pinned model list does not crash the line", () => {
    const discovered: LLMProviderList = {
      routed: { provider: "agy", model: "x" },
      providers: [{ id: "ollama", label: "Ollama", default_model: "qwen3:8b" }],
    };
    const got = describeSelection(
      view({ provider: "ollama", model: "qwen3:8b" }),
      discovered,
    );
    expect(got).toContain("Ollama");
    expect(got).toContain("qwen3:8b");
  });

  // The empty selection is not "no model", it is the daemon's own class
  // routing — a blank field there would read as unconfigured.
  test("no choice names the routing that answers instead", () => {
    expect(describeSelection(view(), providers)).toBe("sınıfa göre · claude · claude-opus-5");
  });

  test("a choice reads with the labels the daemon published", () => {
    expect(describeSelection(view({ provider: "agy", model: "gemini-3.1-pro-high" }), providers)).toBe(
      "Gemini CLI · Gemini 3.1 Pro",
    );
  });

  // The list is a view of GET /llm/providers and may not have arrived. The ids
  // are still the truth about what is saved, so they are shown as they are.
  test("without the provider list the saved ids stand in for the labels", () => {
    expect(describeSelection(view({ provider: "agy", model: "gemini-3.1-pro-high" }), null)).toBe(
      "agy · gemini-3.1-pro-high",
    );
  });

  test("nothing loaded yet is a dash, not an invented default", () => {
    expect(describeSelection(null, providers)).toBe("—");
  });
});

describe("how a rule file reads under its tab", () => {
  test("the shipped text says so", () => {
    expect(describeRule(rule())).toBe("varsayılan metin");
  });

  // "düzenlendi" alone leaves the operator unable to tell this morning's change
  // from July's, which is the question they opened the screen to answer.
  test("an edited file carries when it was edited", () => {
    const at = Date.UTC(2026, 8, 5, 11, 2) / 1000;
    expect(describeRule(rule({ is_default: false, updated_at: at }))).toMatch(/^düzenlendi · /);
  });

  test("an edited file with no timestamp still says it was edited", () => {
    expect(describeRule(rule({ is_default: false }))).toBe("düzenlendi");
  });

  test("no rule yet renders nothing rather than a placeholder", () => {
    expect(describeRule(undefined)).toBe("");
  });
});

describe("isSelectable", () => {
  // A provider whose CLI is not installed is not a choice, it is a failure with
  // an extra click in front of it.
  test("a provider that is not installed cannot be picked", () => {
    const available = [
      { provider: "gemini", model: "g", installed: true, signed_in: false, probed: false, structured_output: false, agentic: true },
      { provider: "codex", model: "c", installed: false, signed_in: false, probed: false, structured_output: false, agentic: true },
    ];
    expect(isSelectable(available, "gemini")).toBe(true);
    expect(isSelectable(available, "codex")).toBe(false);
  });

  // Absent availability is a daemon with no router wired, which is not the same
  // as "nothing is installed" — a whole picker must not go dark over a missing
  // field.
  test("no availability at all leaves everything selectable", () => {
    expect(isSelectable(undefined, "gemini")).toBe(true);
  });
});

describe("availabilityNote", () => {
  test("says what is wrong, in the CLI's own words", () => {
    const available = [
      { provider: "gemini", model: "g", installed: true, signed_in: false, probed: true, detail: "Please set an Auth method", structured_output: false, agentic: true },
      { provider: "codex", model: "c", installed: false, signed_in: false, probed: false, structured_output: false, agentic: true },
      { provider: "agy", model: "a", installed: true, signed_in: true, probed: true, structured_output: true, agentic: false },
    ];
    expect(availabilityNote(available, "codex")).toBe("kurulu değil");
    expect(availabilityNote(available, "gemini")).toBe("Please set an Auth method");
    expect(availabilityNote(available, "agy")).toBe("");
  });

  // Not knowing is not the same as knowing it is broken: before a probe there
  // is nothing to report about a login.
  test("says nothing about a login nobody has tested", () => {
    const available = [
      { provider: "gemini", model: "g", installed: true, signed_in: false, probed: false, structured_output: false, agentic: true },
    ];
    expect(availabilityNote(available, "gemini")).toBe("");
  });
});

describe("modelsFor", () => {
  const providers = {
    routed: { provider: "agy", model: "x" },
    providers: [
      { id: "ollama", label: "Ollama", default_model: "qwen3:8b", models: [] },
      { id: "claude", label: "Claude", default_model: "s", models: [{ id: "s", label: "Sonnet 5" }] },
    ],
  };

  // A provider whose models are files on this machine publishes an empty
  // catalogue on purpose; the real list is what the daemon found.
  test("discovered models win over an empty catalogue", () => {
    const available = [
      { provider: "ollama", model: "qwen3:8b", installed: true, signed_in: true, probed: false, structured_output: false, agentic: false, models: ["qwen3:8b", "gpt-oss:20b"] },
    ];
    expect(modelsFor(providers, available, "ollama").map((m) => m.id)).toEqual([
      "qwen3:8b",
      "gpt-oss:20b",
    ]);
  });

  test("a pinned catalogue is used when there is nothing to discover", () => {
    expect(modelsFor(providers, [], "claude").map((m) => m.label)).toEqual(["Sonnet 5"]);
  });
});

describe("the section rail", () => {
  // A rail hides what is not on screen, so it has to mark what it hid. Without
  // this an edit in a scrolled-away section is invisible until it is lost.
  test("marks the section that holds the unsaved change", () => {
    const drafts = draftsFrom(view().rules);
    const model: ModelDraft = { provider: "", model: "", saved: { provider: "", model: "" } };

    expect(dirtySections(drafts, model, clean)).toEqual([]);

    expect(
      dirtySections(drafts, { ...model, provider: "agy", model: "x" }, clean),
    ).toEqual(["routing"]);

    expect(
      dirtySections(drafts, model, {
        distill: { provider: "gemini" },
        reason: {},
        saved: { distill: {}, reason: {} },
      }),
    ).toEqual(["routing"]);

    expect(
      dirtySections([drafts[0], { ...drafts[1], body: "# yeni" }], model, clean),
    ).toEqual(["rules"]);
  });

  // Connections write immediately, so they can never be unsaved. A rail dot
  // there would be a dot that never clears.
  test("connections are never marked, because that card owns its own writes", () => {
    const drafts = draftsFrom(view().rules);
    const model: ModelDraft = { provider: "agy", model: "x", saved: { provider: "", model: "" } };
    expect(dirtySections(drafts, model, clean)).not.toContain("connections");
  });

  test("every section has a word", () => {
    for (const s of SETTINGS_SECTIONS) {
      expect(sectionLabel(s).length).toBeGreaterThan(0);
    }
  });
});
