import { useEffect, useMemo, useState } from "react";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Card, CardBody, CardHeader } from "../components/ui/card";
import { ProviderModelPicker } from "../components/ModelPicker";
import {
  api,
  CHANNELS,
  DaemonError,
  type LLMProviderList,
  type OutreachChannel,
  type OutreachRule,
  type SettingsView,
} from "../lib/daemon";
import {
  anyDirty,
  describeRule,
  describeSelection,
  draftsFrom,
  isDirty,
  isSavableSelection,
  modelDraftFrom,
  modelForProvider,
  RULE_CACHE_WARNING,
  type ModelDraft,
  type RuleDraft,
} from "../lib/settings";

/**
 * The operator's own configuration, in one place.
 *
 * Everything here used to be somewhere else: the model picker sat on the
 * lead-gen search bar, where it was a per-run choice nobody could see the state
 * of afterwards, and the outreach prompt was a Go constant. Both are decisions
 * about a campaign rather than about a search, so they belong to a screen you
 * open on purpose — and a choice made here survives the screen it was made on,
 * which was the actual complaint about the picker.
 *
 * One save for the whole page rather than a button per card. Electric is the
 * CTA colour and a screen gets one; more to the point, a settings page with
 * three separate saves is a page where you find out later which one you
 * forgot. The bar appears only when something is unsaved and says exactly what
 * it is about to write.
 */
export function Settings() {
  const [view, setView] = useState<SettingsView | null>(null);
  const [providers, setProviders] = useState<LLMProviderList | null>(null);
  const [rules, setRules] = useState<RuleDraft[]>([]);
  const [modelDraft, setModelDraft] = useState<ModelDraft>({
    provider: "",
    model: "",
    saved: { provider: "", model: "" },
  });

  const [channel, setChannel] = useState<OutreachChannel>("email");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  const adopt = (next: SettingsView) => {
    setView(next);
    setRules(draftsFrom(next.rules));
    setModelDraft(modelDraftFrom(next));
  };

  useEffect(() => {
    let live = true;
    Promise.all([api.settings(), api.llmProviders().catch(() => null)])
      .then(([s, p]) => {
        if (!live) return;
        adopt(s);
        setProviders(p);
        setError(null);
      })
      .catch((err) => {
        if (live) setError(describe(err));
      })
      .finally(() => {
        if (live) setLoading(false);
      });
    return () => {
      live = false;
    };
  }, []);

  const dirty = anyDirty(rules, modelDraft);
  const ruleOf = (ch: OutreachChannel) => view?.rules.find((r) => r.channel === ch);
  const draftOf = (ch: OutreachChannel) => rules.find((r) => r.channel === ch);
  const current = draftOf(channel);

  const modelDirty =
    modelDraft.provider !== modelDraft.saved.provider || modelDraft.model !== modelDraft.saved.model;
  const sendable = isSavableSelection(modelDraft.provider, modelDraft.model);

  // What the bar is about to write, named rather than counted: "3 değişiklik"
  // tells the operator nothing they can check.
  const pending = useMemo(() => {
    const parts: string[] = [];
    if (modelDirty) parts.push("model seçimi");
    for (const ch of CHANNELS) {
      if (isDirty(draftOf(ch))) parts.push(`${ruleOf(ch)?.label ?? ch} kuralları`);
    }
    return parts;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rules, modelDirty, view]);

  const editRule = (ch: OutreachChannel, body: string) => {
    setSaved(false);
    setRules((cur) => cur.map((r) => (r.channel === ch ? { ...r, body } : r)));
  };

  const save = async () => {
    if (!dirty || !sendable || saving) return;
    setSaving(true);
    setError(null);
    try {
      // The model first, then the rule files, and each response is adopted as
      // it lands. If the second write fails the first is still saved and the
      // screen says so — a settings page that rolled back a write the daemon
      // accepted would be lying about what is on disk.
      let next = view;
      if (modelDirty) {
        next = await api.saveSettings(modelDraft.provider, modelDraft.model);
        setModelDraft(modelDraftFrom(next));
      }

      const written: OutreachRule[] = [];
      for (const draft of rules) {
        if (!isDirty(draft)) continue;
        written.push(await api.saveRule(draft.channel, draft.body));
      }

      if (next) {
        const merged: SettingsView = {
          ...next,
          rules: next.rules.map((r) => written.find((w) => w.channel === r.channel) ?? r),
        };
        setView(merged);
        setRules((cur) =>
          cur.map((r) => {
            const hit = written.find((w) => w.channel === r.channel);
            return hit ? { channel: r.channel, body: hit.body, saved: hit.body } : r;
          }),
        );
      }
      setSaved(true);
    } catch (err) {
      setError(describe(err));
    } finally {
      setSaving(false);
    }
  };

  const revert = () => {
    if (!view) return;
    adopt(view);
    setError(null);
    setSaved(false);
  };

  const resetRule = async (ch: OutreachChannel) => {
    setSaving(true);
    setError(null);
    try {
      const rule = await api.resetRule(ch);
      setRules((cur) =>
        cur.map((r) => (r.channel === ch ? { channel: ch, body: rule.body, saved: rule.body } : r)),
      );
      setView((cur) =>
        cur ? { ...cur, rules: cur.rules.map((r) => (r.channel === ch ? rule : r)) } : cur,
      );
      setSaved(true);
    } catch (err) {
      setError(describe(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div
      className="mx-auto grid h-full w-full max-w-[980px] grid-rows-[1fr_auto] gap-0 overflow-hidden p-5"
      onKeyDown={(e) => {
        // ⌘S is what anybody editing a text file presses. Without it the only
        // way to save is to scroll to the bar, which is the wrong shape for a
        // page whose main control is a large textarea.
        if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "s") {
          e.preventDefault();
          void save();
        }
      }}
    >
      <div className="min-h-0 space-y-3 overflow-auto pb-3">
        <ModelCard
          view={view}
          providers={providers}
          draft={modelDraft}
          sendable={sendable}
          onProvider={(id) => {
            setSaved(false);
            setModelDraft((d) => ({ ...d, provider: id, model: modelForProvider(providers, id) }));
          }}
          onModel={(id) => {
            setSaved(false);
            setModelDraft((d) => ({ ...d, model: id }));
          }}
        />

        <RulesCard
          loading={loading}
          channel={channel}
          onChannel={setChannel}
          rules={view?.rules ?? []}
          drafts={rules}
          current={current}
          rule={ruleOf(channel)}
          busy={saving}
          onEdit={(body) => editRule(channel, body)}
          onReset={() => void resetRule(channel)}
        />
      </div>

      <SaveBar
        dirty={dirty}
        pending={pending}
        saving={saving}
        saved={saved}
        blocked={!sendable}
        error={error}
        onSave={() => void save()}
        onRevert={revert}
      />
    </div>
  );
}

/* -------------------------------------------------------------------------- */
/* the model                                                                   */
/* -------------------------------------------------------------------------- */

function ModelCard(props: {
  view: SettingsView | null;
  providers: LLMProviderList | null;
  draft: ModelDraft;
  sendable: boolean;
  onProvider: (v: string) => void;
  onModel: (v: string) => void;
}) {
  return (
    <Card>
      <CardHeader
        title="Model"
        subtitle="Lead-gen'in model harcayan aşamaları — sınıflandırma, boşluk analizi, mesaj taslakları — bu seçimi kullanır."
        aside={
          <Badge tone={props.view?.provider ? "accent" : "muted"}>
            {describeSelection(props.view, props.providers)}
          </Badge>
        }
      />
      <CardBody className="space-y-2.5">
        {props.providers ? (
          <ProviderModelPicker
            providers={props.providers}
            provider={props.draft.provider}
            model={props.draft.model}
            onProvider={props.onProvider}
            onModel={props.onModel}
          />
        ) : (
          // The picker is a view of `GET /llm/providers`, never a second copy:
          // with no answer there is nothing honest to offer, and runs still
          // work — they route by class, exactly as before this screen existed.
          <p className="text-xs text-muted">
            Model listesi alınamadı. Çalıştırmalar sınıfa göre yönlendirilmeye devam eder.
          </p>
        )}

        <p className="max-w-[76ch] text-xs leading-relaxed text-muted">
          Boş bırakmak bir eksiklik değil: daemon'un kendi sınıf yönlendirmesi devreye girer, ki
          bu seçim ekranı yokken her çalıştırmanın yaptığı şeydi. Bir seçim yaptığınızda önbellek
          de o modele göre ayrılır — aynı şirket için başka bir modelle yazılmış taslak yeniden
          kullanılmaz.
        </p>

        {!props.sendable && (
          <p className="text-xs text-bad">
            Sağlayıcı seçtiyseniz model de seçmelisiniz — ikisi birlikte gider.
          </p>
        )}
      </CardBody>
    </Card>
  );
}

/* -------------------------------------------------------------------------- */
/* the rule files                                                              */
/* -------------------------------------------------------------------------- */

function RulesCard(props: {
  loading: boolean;
  channel: OutreachChannel;
  onChannel: (ch: OutreachChannel) => void;
  rules: OutreachRule[];
  drafts: RuleDraft[];
  current: RuleDraft | undefined;
  rule: OutreachRule | undefined;
  busy: boolean;
  onEdit: (body: string) => void;
  onReset: () => void;
}) {
  const dirtyHere = isDirty(props.current);

  return (
    <Card className="flex flex-col">
      <CardHeader
        title="Mesaj kuralları"
        subtitle="Her kanalın kendi kural dosyası var; taslak istemine olduğu gibi eklenir."
        aside={
          <div className="flex items-center gap-1 rounded-sm border border-edge p-0.5">
            {props.rules.map((rule) => {
              const on = rule.channel === props.channel;
              const unsaved = isDirty(props.drafts.find((d) => d.channel === rule.channel));
              return (
                <button
                  key={rule.channel}
                  type="button"
                  onClick={() => props.onChannel(rule.channel)}
                  className={`label flex items-center gap-1.5 rounded-sm px-2.5 py-1.5 leading-none transition-colors ${
                    on ? "bg-raised text-mist" : "text-muted hover:text-mist"
                  }`}
                >
                  {rule.label}
                  {/* An unsaved tab has to say so from the other tab, or the
                      operator saves one file and loses the edit on the other. */}
                  {unsaved && <span className="size-1.5 rounded-full bg-electric" />}
                </button>
              );
            })}
          </div>
        }
      />

      <CardBody className="space-y-2.5">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
          <Badge tone={props.rule?.is_default ? "muted" : "accent"}>
            {describeRule(props.rule)}
          </Badge>
          {props.rule?.path && (
            <code
              className="truncate text-[11px] text-muted"
              title="Kural dosyası diskte burada duruyor; kendi düzenleyicinizde de açabilirsiniz."
            >
              {props.rule.path}
            </code>
          )}
          <Button
            variant="ghost"
            className="ml-auto"
            disabled={props.busy || (props.rule?.is_default && !dirtyHere)}
            onClick={props.onReset}
          >
            Varsayılana dön
          </Button>
        </div>

        <textarea
          value={props.current?.body ?? ""}
          onChange={(e) => props.onEdit(e.target.value)}
          disabled={props.loading}
          spellCheck={false}
          // Monospace and a fixed height: this is a file, it has headings and
          // list markers, and reflowing it in a proportional face would hide
          // the structure the operator is editing.
          className="h-[380px] w-full resize-y rounded-sm border border-edge bg-ground px-3 py-2.5 font-mono text-[12px] leading-[1.65] text-text outline-none focus:border-electric"
          placeholder={props.loading ? "yükleniyor…" : ""}
        />

        <p className="max-w-[76ch] border-l-2 border-warn/50 pl-2.5 text-[11px] leading-relaxed text-muted">
          {RULE_CACHE_WARNING}
        </p>
      </CardBody>
    </Card>
  );
}

/* -------------------------------------------------------------------------- */
/* the save bar                                                                */
/* -------------------------------------------------------------------------- */

function SaveBar(props: {
  dirty: boolean;
  pending: string[];
  saving: boolean;
  saved: boolean;
  blocked: boolean;
  error: string | null;
  onSave: () => void;
  onRevert: () => void;
}) {
  if (!props.dirty) {
    // Nothing to save is not nothing to say: an error from the last attempt,
    // or the confirmation that it worked, both belong here.
    if (!props.error && !props.saved) return <div />;
    return (
      <div className="flex items-center gap-3 border-t border-edge pt-3">
        {props.error ? (
          <span className="text-xs text-bad">{props.error}</span>
        ) : (
          <span className="text-xs text-ok">Kaydedildi.</span>
        )}
      </div>
    );
  }

  return (
    <div className="flex flex-wrap items-center gap-3 border-t border-edge pt-3">
      <span className="text-xs text-muted">
        Kaydedilmemiş: <span className="text-mist">{props.pending.join(" · ")}</span>
      </span>
      {props.error && <span className="text-xs text-bad">{props.error}</span>}
      <div className="ml-auto flex items-center gap-2">
        <span className="label text-muted">⌘S</span>
        <Button variant="ghost" onClick={props.onRevert} disabled={props.saving}>
          Geri al
        </Button>
        <Button onClick={props.onSave} disabled={props.saving || props.blocked}>
          {props.saving ? "Kaydediliyor…" : "Kaydet"}
        </Button>
      </div>
    </div>
  );
}

function describe(err: unknown): string {
  if (err instanceof DaemonError) return err.message;
  return err instanceof Error ? err.message : String(err);
}
