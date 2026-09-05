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

    expect(anyDirty(drafts, model)).toBe(false);
    expect(anyDirty([drafts[0], { ...drafts[1], body: "# yeni" }], model)).toBe(true);
  });

  test("anyDirty also sees an unsaved model choice", () => {
    const drafts = draftsFrom(view().rules);
    expect(
      anyDirty(drafts, { provider: "agy", model: "gemini-3.1-pro-high", saved: { provider: "", model: "" } }),
    ).toBe(true);
  });
});

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
