import type {
  LLMAvailability,
  LLMChoice,
  LLMProviderList,
  OutreachChannel,
  OutreachRule,
  SettingsView,
} from "./daemon";

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
/**
 * The class defaults, mid-edit, beside what is on the daemon.
 *
 * They live here rather than as comparisons inside JSX, and that is not tidiness
 * — it is the direct cause of a shipped bug. `anyDirty` was a tested function in
 * this file while the class-default comparison was four inline `??` expressions
 * in `Settings.tsx`, so the save bar armed on one rule and `save()` branched on
 * another. Editing only a class default marked the page dirty, wrote nothing,
 * and reported "Kaydedildi."
 */
export type ClassDraft = {
  distill: LLMChoice;
  reason: LLMChoice;
  saved: { distill: LLMChoice; reason: LLMChoice };
};

/** Whether one provider/model pair differs from what was saved. */
function choiceDiffers(a: LLMChoice, b: LLMChoice): boolean {
  return (a.provider ?? "") !== (b.provider ?? "") || (a.model ?? "") !== (b.model ?? "");
}

/** Whether either class default has been edited since it was loaded. */
export function classesDirty(draft: ClassDraft): boolean {
  return (
    choiceDiffers(draft.distill, draft.saved.distill) ||
    choiceDiffers(draft.reason, draft.saved.reason)
  );
}

export function classDraftFrom(view: SettingsView): ClassDraft {
  const distill = view.distill ?? {};
  const reason = view.reason ?? {};
  return { distill, reason, saved: { distill, reason } };
}

/**
 * Whether anything on the page is unsaved.
 *
 * All three kinds in one place, because the save bar asks one question and the
 * bug was that it asked it in two.
 */
export function anyDirty(
  drafts: RuleDraft[],
  settings: ModelDraft,
  classes: ClassDraft,
): boolean {
  return (
    drafts.some(isDirty) ||
    settings.provider !== settings.saved.provider ||
    settings.model !== settings.saved.model ||
    classesDirty(classes)
  );
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
  // `models` is absent for a provider whose models are discovered rather than
  // pinned — ollama publishes none, because they are files on this machine.
  // Guarded rather than assumed: this line crashed the settings screen the
  // first time somebody pointed a class default at ollama.
  const model = provider?.models?.find((m) => m.id === view.model);
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

// --- what this machine can actually run --------------------------------------

/**
 * Whether a provider can be picked here.
 *
 * A provider whose CLI is not installed is not a choice, it is a failure with
 * an extra click in front of it. Absent availability — a daemon with no router
 * wired — is *not* the same as "nothing is installed", so everything stays
 * selectable rather than a whole picker going dark over a missing field.
 */
export function isSelectable(
  available: LLMAvailability[] | undefined,
  providerID: string,
): boolean {
  if (!available) return true;
  const found = available.find((a) => a.provider === providerID);
  return found ? found.installed : true;
}

/**
 * The note shown against a provider: what is wrong with it, in the CLI's own
 * words, or "" when nothing is.
 *
 * Signed-out is only reported after a probe, because before one there is
 * nothing to report — not knowing is not the same as knowing it is broken.
 */
export function availabilityNote(
  available: LLMAvailability[] | undefined,
  providerID: string,
): string {
  const found = available?.find((a) => a.provider === providerID);
  if (!found) return "";
  if (!found.installed) return "kurulu değil";
  if (found.probed && !found.signed_in) return found.detail || "oturum kapalı";
  return "";
}

/**
 * The models a provider offers here.
 *
 * Discovered models win when there are any: a provider whose models are files
 * on this machine (ollama) publishes an empty catalogue on purpose, and the
 * real list is what the daemon found. Everyone else's list is the pinned
 * vendor catalogue.
 */
export function modelsFor(
  providers: LLMProviderList | null,
  available: LLMAvailability[] | undefined,
  providerID: string,
): { id: string; label: string }[] {
  const discovered = available?.find((a) => a.provider === providerID)?.models;
  if (discovered && discovered.length > 0) {
    return discovered.map((id) => ({ id, label: id }));
  }
  const listed = providers?.providers.find((p) => p.id === providerID)?.models ?? [];
  return listed.map((m) => ({ id: m.id, label: m.label }));
}

// --- the screen's own sections -----------------------------------------------

/**
 * The settings screen's sections, in the order the rail offers them.
 *
 * It became a rail rather than staying one scroll column because the
 * connections list is a list: a column that already held three cards and a
 * 380px textarea cannot also hold "everything you have and whether it works"
 * and still be scannable. Not a top tab bar either — `RulesCard` already draws
 * a segmented strip for its two channels, and a second horizontal strip above
 * it reads as nested tabs. A rail is a different axis, so they do not collide.
 */
export const SETTINGS_SECTIONS = ["connections", "routing", "skills", "rules"] as const;

export type SettingsSection = (typeof SETTINGS_SECTIONS)[number];

const SECTION_LABELS: Record<SettingsSection, string> = {
  connections: "Bağlantılar",
  routing: "Yönlendirme",
  skills: "Skill'ler",
  rules: "Kurallar",
};

export function sectionLabel(section: SettingsSection): string {
  return SECTION_LABELS[section];
}

/**
 * Which sections hold something unsaved.
 *
 * The rail marks them, so an edit in a section that has been scrolled away from
 * is still visible — the failure a rail introduces if nobody guards it.
 * Connections are absent on purpose: that card writes immediately, so it has
 * nothing to be unsaved.
 */
export function dirtySections(
  drafts: RuleDraft[],
  model: ModelDraft,
  classes: ClassDraft,
): SettingsSection[] {
  const out: SettingsSection[] = [];
  const modelDirty =
    model.provider !== model.saved.provider || model.model !== model.saved.model;
  if (modelDirty || classesDirty(classes)) out.push("routing");
  if (drafts.some(isDirty)) out.push("rules");
  return out;
}
