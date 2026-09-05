import type { LLMProviderList, OutreachChannel, OutreachRule, SettingsView } from "./daemon";

/**
 * The settings screen's decisions, as functions.
 *
 * The screen itself is a form, and a form is mostly markup — but the questions
 * behind it are not obvious and each has exactly one right answer: has this
 * rule file been edited since it was loaded, what will a run actually spend if
 * nobody touches the picker, is this pair even sendable. Those live here, with
 * tests, rather than as conditions inside JSX where nobody can check them.
 */

/** One channel's rule file while it is being edited. */
export type RuleDraft = {
  channel: OutreachChannel;
  /** What the editor holds right now. */
  body: string;
  /** What the daemon last confirmed, for the dirty check and for reverting. */
  saved: string;
};

export function draftsFrom(rules: OutreachRule[]): RuleDraft[] {
  return rules.map((r) => ({ channel: r.channel, body: r.body, saved: r.body }));
}

/** Whether the editor differs from what is on disk. */
export function isDirty(draft: RuleDraft | undefined): boolean {
  return draft !== undefined && draft.body !== draft.saved;
}

/** Whether anything at all is unsaved — what the "leave the page" guard asks. */
export function anyDirty(drafts: RuleDraft[], settings: ModelDraft): boolean {
  return drafts.some(isDirty) || settings.provider !== settings.saved.provider ||
    settings.model !== settings.saved.model;
}

/** The model choice while it is being edited. */
export type ModelDraft = {
  provider: string;
  model: string;
  saved: { provider: string; model: string };
};

export function modelDraftFrom(view: SettingsView): ModelDraft {
  return {
    provider: view.provider,
    model: view.model,
    saved: { provider: view.provider, model: view.model },
  };
}

/**
 * The model a provider change should land on.
 *
 * Naming the new provider's default explicitly rather than clearing to "": the
 * model control has no empty option, so a cleared value would display whichever
 * model happens to be listed first while a run spent a different one.
 *
 * The empty provider is the daemon's class routing and carries no model — the
 * two travel together or not at all.
 */
export function modelForProvider(providers: LLMProviderList | null, providerID: string): string {
  if (!providerID) return "";
  return providers?.providers.find((p) => p.id === providerID)?.default_model ?? "";
}

/**
 * Whether the pair can be sent.
 *
 * Both empty is legal and means "route by class". A provider with no model is
 * not: the daemon rejects it, and the screen should not offer a save that is
 * going to come back as a 400.
 */
export function isSavableSelection(provider: string, model: string): boolean {
  if (provider === "") return model === "";
  return model !== "";
}

/**
 * What a lead-gen run will spend, in words.
 *
 * The empty selection is not "no model", it is the daemon's own class routing,
 * and a screen that showed a blank field there would read as unconfigured
 * rather than as configured to the default.
 */
export function describeSelection(view: SettingsView | null, providers: LLMProviderList | null): string {
  if (!view) return "—";
  if (!view.provider) {
    return `sınıfa göre · ${view.routed.provider || "varsayılan"}${
      view.routed.model ? ` · ${view.routed.model}` : ""
    }`;
  }

  const provider = providers?.providers.find((p) => p.id === view.provider);
  const model = provider?.models.find((m) => m.id === view.model);
  return `${provider?.label ?? view.provider} · ${model?.label ?? view.model}`;
}

/**
 * How a rule file reads under its tab: whether it is still the shipped text,
 * and when the operator last changed it.
 *
 * "düzenlendi" without a date would leave the operator unable to tell a change
 * they made this morning from one they made in July, which is exactly the
 * question they open this screen to answer.
 */
export function describeRule(rule: OutreachRule | undefined): string {
  if (!rule) return "";
  if (rule.is_default) return "varsayılan metin";
  if (!rule.updated_at) return "düzenlendi";
  const when = new Date(rule.updated_at * 1000).toLocaleString("tr-TR", {
    day: "2-digit",
    month: "short",
    hour: "2-digit",
    minute: "2-digit",
  });
  return `düzenlendi · ${when}`;
}

/**
 * The warning the rule editors carry, in one place because it is the one thing
 * about this screen that is not obvious.
 *
 * A rule file is part of the drafting prompt, so its text is part of the cache
 * key. Editing it invalidates every draft written under the old text —
 * including ones already marked "sent". That is the honest trade (the
 * alternative is showing a draft the current rules never produced), and it has
 * to be said on the screen where the edit happens rather than discovered
 * afterwards.
 */
export const RULE_CACHE_WARNING =
  "Kural dosyası taslak isteminin parçasıdır: kaydettiğinizde eski kurallarla " +
  "yazılmış taslaklar geçersiz olur ve bir sonraki çalıştırmada yeniden yazılır — " +
  "“gönderildi” işaretlenmiş olanlar dahil.";
