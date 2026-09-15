import { motion } from "framer-motion";
import { useEffect, useMemo, useState } from "react";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Textarea } from "../components/ui/field";
import { Icon } from "../components/ui/icon";
import { Kbd } from "../components/ui/kbd";
import { Masthead } from "../components/ui/masthead";
import { Rail, RailItem } from "../components/ui/rail";
import { Tabs } from "../components/ui/tabs";
import { dock } from "../lib/motion";
import { Card, CardBody, CardHeader } from "../components/ui/card";
import { ConnectionsCard } from "../components/ConnectionsCard";
import { ProviderModelPicker } from "../components/ModelPicker";
import { SkillsCard } from "../components/SkillsCard";
import {
  api,
  CHANNELS,
  DaemonError,
  type LLMChoice,
  type LLMProviderList,
  type OutreachChannel,
  type OutreachRule,
  type SettingsView,
} from "../lib/daemon";
import {
  anyDirty,
  classDraftFrom,
  classesDirty,
  dirtySections,
  sectionLabel,
  SETTINGS_SECTIONS,
  type ClassDraft,
  type SettingsSection,
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
  // The two class defaults, and whether a login probe is in flight. Kept apart
  // from modelDraft because they answer a different question: that one is what
  // a lead-gen run spends, these are what the daemon spends on its own.
  const [classDraft, setClassDraft] = useState<ClassDraft>({
    distill: {},
    reason: {},
    saved: { distill: {}, reason: {} },
  });
  const [probing, setProbing] = useState(false);
  const [section, setSection] = useState<SettingsSection>("connections");
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
    setClassDraft(classDraftFrom(next));
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

  const ruleOf = (ch: OutreachChannel) =>
    view?.rules.find((r) => r.channel === ch);
  const draftOf = (ch: OutreachChannel) => rules.find((r) => r.channel === ch);
  const current = draftOf(channel);

  const modelDirty =
    modelDraft.provider !== modelDraft.saved.provider ||
    modelDraft.model !== modelDraft.saved.model;
  const sendable = isSavableSelection(modelDraft.provider, modelDraft.model);

  const classDirty = classesDirty(classDraft);
  const dirtyList = dirtySections(rules, modelDraft, classDraft);
  const dirty = anyDirty(rules, modelDraft, classDraft);

  // What the bar is about to write, named rather than counted: "3 değişiklik"
  // tells the operator nothing they can check.
  const pending = useMemo(() => {
    const parts: string[] = [];
    if (modelDirty) parts.push("model seçimi");
    if (classDirty) parts.push("daemon'ın kendi çağrıları");
    for (const ch of CHANNELS) {
      if (isDirty(draftOf(ch)))
        parts.push(`${ruleOf(ch)?.label ?? ch} kuralları`);
    }
    return parts;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rules, modelDirty, classDirty, view]);

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
    setClassDraft(classDraftFrom(next));
      }

      const written: OutreachRule[] = [];
      for (const draft of rules) {
        if (!isDirty(draft)) continue;
        written.push(await api.saveRule(draft.channel, draft.body));
      }

      if (next) {
        const merged: SettingsView = {
          ...next,
          rules: next.rules.map(
            (r) => written.find((w) => w.channel === r.channel) ?? r,
          ),
        };
        setView(merged);
        setRules((cur) =>
          cur.map((r) => {
            const hit = written.find((w) => w.channel === r.channel);
            return hit
              ? { channel: r.channel, body: hit.body, saved: hit.body }
              : r;
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
        cur.map((r) =>
          r.channel === ch
            ? { channel: ch, body: rule.body, saved: rule.body }
            : r,
        ),
      );
      setView((cur) =>
        cur
          ? {
              ...cur,
              rules: cur.rules.map((r) => (r.channel === ch ? rule : r)),
            }
          : cur,
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
      className="grid h-full w-full grid-rows-[auto_1fr_auto] overflow-hidden px-8 pt-6 pb-5"
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
      {/* Every other screen names itself and this one did not, which is how a
          settings page ends up reading as a dialog somebody forgot to close. */}
      <Masthead title="Ayarlar" className="mb-6" />

      {/* A rail, not a longer column and not a top tab strip. The connections
          list is a list, and a column that already held three cards and a 380px
          textarea cannot also hold "everything you have and whether it works".
          A horizontal strip would collide with the segmented tabs RulesCard
          already draws for its two channels; a rail is a different axis. */}
      <div className="mx-auto grid min-h-0 w-full max-w-[1020px] grid-cols-[11rem_minmax(0,1fr)] gap-6 overflow-hidden">
        <SectionRail active={section} dirty={dirtyList} onSelect={setSection} />

        <div className="flex min-h-0 flex-col gap-5 overflow-auto pb-4">
        {section === "connections" && <ConnectionsCard />}

        {section === "routing" && (
        <>
        <ClassDefaultsCard
          providers={providers}
          distill={classDraft.distill}
          reason={classDraft.reason}
          probing={probing}
          onDistill={(c) => {
            setSaved(false);
            setClassDraft((d) => ({ ...d, distill: c }));
          }}
          onReason={(c) => {
            setSaved(false);
            setClassDraft((d) => ({ ...d, reason: c }));
          }}
          onProbe={() => {
            setProbing(true);
            // The expensive question, asked only when somebody asks it: one
            // completion per provider, to find out whether the logins work.
            void api
              .llmProviders(true)
              .then(setProviders)
              .catch(() => {})
              .finally(() => setProbing(false));
          }}
        />
        <ModelCard
          view={view}
          providers={providers}
          draft={modelDraft}
          sendable={sendable}
          onProvider={(id) => {
            setSaved(false);
            setModelDraft((d) => ({
              ...d,
              provider: id,
              model: modelForProvider(providers, id),
            }));
          }}
          onModel={(id) => {
            setSaved(false);
            setModelDraft((d) => ({ ...d, model: id }));
          }}
        />

        </>
        )}

        {/* Above the outreach rules because it governs more: every sub-agent
            is held to a skill, while the rule files shape one channel of one
            of them. */}
        {section === "skills" && <SkillsCard />}

        {section === "rules" && (
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
        )}
        </div>
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
    <Card elevation="raised">
      <CardHeader
        title="Model"
        subtitle="Lead-gen'in model harcayan aşamaları — sınıflandırma, boşluk analizi, mesaj taslakları — bu seçimi kullanır."
        aside={
          <Badge tone={props.view?.provider ? "accent" : "muted"}>
            {describeSelection(props.view, props.providers)}
          </Badge>
        }
      />
      <CardBody className="flex flex-col gap-3.5">
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
          <p className="text-sm text-muted">
            Model listesi alınamadı. Çalıştırmalar sınıfa göre yönlendirilmeye
            devam eder.
          </p>
        )}

        <p className="max-w-[76ch] text-sm leading-relaxed text-muted">
          Boş bırakmak bir eksiklik değil: daemon'un kendi sınıf yönlendirmesi
          devreye girer, ki bu seçim ekranı yokken her çalıştırmanın yaptığı
          şeydi. Bir seçim yaptığınızda önbellek de o modele göre ayrılır — aynı
          şirket için başka bir modelle yazılmış taslak yeniden kullanılmaz.
        </p>

        {!props.sendable && (
          <p className="text-sm text-bad">
            Sağlayıcı seçtiyseniz model de seçmelisiniz — ikisi birlikte gider.
          </p>
        )}
      </CardBody>
    </Card>
  );
}

/**
 * The screen's sections.
 *
 * It marks the ones holding something unsaved, which is the failure a rail
 * introduces if nobody guards it: an edit in a section that has been navigated
 * away from is otherwise invisible until it is lost. Connections never carry a
 * mark — that card writes immediately, so it has nothing to be unsaved.
 */
function SectionRail({
  active,
  dirty,
  onSelect,
}: {
  active: SettingsSection;
  dirty: SettingsSection[];
  onSelect: (s: SettingsSection) => void;
}) {
  return (
    <Rail label="Ayarlar bölümleri" className="pt-1">
      {SETTINGS_SECTIONS.map((key) => (
        <RailItem
          key={key}
          label={sectionLabel(key)}
          on={key === active}
          onSelect={() => onSelect(key)}
          layoutId="settings-rail"
          mark={
            dirty.includes(key) ? (
              <span
                aria-label="kaydedilmemiş"
                className="size-1.5 shrink-0 rounded-full bg-lime"
              />
            ) : undefined
          }
        />
      ))}
    </Rail>
  );
}

/**
 * What the daemon spends on its *own* work.
 *
 * This is the answer to "the model should be selectable in every task, Brain
 * included": Brain's distil and relation passes, refine's five profiles and the
 * catalog rewrite never carried a selection, because they are not runs somebody
 * started — they are the daemon working on its own. Which *class* a piece of
 * work belongs to stays in the daemon's code, because that is a property of the
 * work. Which provider serves a class on this machine is not, and this is where
 * the operator says so.
 *
 * A machine-wide Brain scan is thousands of calls. Before this the only way to
 * point them somewhere else was to edit a constant and rebuild.
 */
function ClassDefaultsCard(props: {
  providers: LLMProviderList | null;
  distill: LLMChoice;
  reason: LLMChoice;
  onDistill: (c: LLMChoice) => void;
  onReason: (c: LLMChoice) => void;
  onProbe: () => void;
  probing: boolean;
}) {
  if (!props.providers) return null;

  return (
    <Card elevation="raised">
      <CardHeader
        title="Daemon'ın kendi çağrıları"
        subtitle="Brain'in tarama ve ilişki pasoları, refine, katalog yeniden yazımı. Boş bırakıldığında sınıf yönlendirmesi karar verir."
        aside={
          <Button
            size="sm"
            variant="quiet"
            icon="pulse"
            disabled={props.probing}
            onClick={props.onProbe}
          >
            {props.probing ? "sınanıyor…" : "oturumları sına"}
          </Button>
        }
      />
      <CardBody className="flex flex-col gap-5">
        <div className="flex flex-col gap-2">
          <p className="label text-muted">sıkıştırma · distil</p>
          <ProviderModelPicker
            providers={props.providers}
            provider={props.distill.provider ?? ""}
            model={props.distill.model ?? ""}
            routedLabel="Sınıf yönlendirmesi"
            onProvider={(provider) =>
              props.onDistill({ provider, model: modelForProvider(props.providers, provider) })
            }
            onModel={(model) => props.onDistill({ ...props.distill, model })}
          />
          {/* The one pairing that fails silently, said before it can be
              chosen: Brain sends a schema and parses what comes back, and a
              provider that cannot serve one answers in prose. The daemon
              refuses it — but a picker that offers it is a picker that wastes
              the operator's afternoon. */}
          <p className="text-sm leading-relaxed text-muted">
            Bu sınıf şema ile çağırıyor. Yapısal çıktı veremeyen bir sağlayıcı
            (gemini, ollama) burada daemon tarafından reddedilir.
          </p>
        </div>

        <div className="flex flex-col gap-2">
          <p className="label text-muted">sentez · reason</p>
          <ProviderModelPicker
            providers={props.providers}
            provider={props.reason.provider ?? ""}
            model={props.reason.model ?? ""}
            routedLabel="Sınıf yönlendirmesi"
            onProvider={(provider) =>
              props.onReason({ provider, model: modelForProvider(props.providers, provider) })
            }
            onModel={(model) => props.onReason({ ...props.reason, model })}
          />
        </div>
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
    <Card elevation="raised" className="flex flex-col">
      <CardHeader
        title="Mesaj kuralları"
        subtitle="Her kanalın kendi kural dosyası var; taslak istemine olduğu gibi eklenir."
        aside={
          <Tabs
            id="settings-rules"
            variant="segmented"
            active={props.channel}
            onSelect={props.onChannel}
            tabs={props.rules.map((rule) => ({
              key: rule.channel,
              label: rule.label,
              // An unsaved tab has to say so from the other tab, or the
              // operator saves one file and loses the edit on the other.
              dirty: isDirty(
                props.drafts.find((d) => d.channel === rule.channel),
              ),
            }))}
          />
        }
      />

      <CardBody className="flex flex-col gap-3">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          <Badge tone={props.rule?.is_default ? "muted" : "accent"}>
            {describeRule(props.rule)}
          </Badge>
          {props.rule?.path && (
            <code
              className="truncate text-xs text-muted"
              title="Kural dosyası diskte burada duruyor; kendi düzenleyicinizde de açabilirsiniz."
            >
              {props.rule.path}
            </code>
          )}
          <Button
            variant="ghost"
            size="sm"
            icon="refresh"
            className="ml-auto"
            disabled={props.busy || (props.rule?.is_default && !dirtyHere)}
            onClick={props.onReset}
          >
            Varsayılana dön
          </Button>
        </div>

        {/* Monospace and a fixed height: this is a file, it has headings and
            list markers, and reflowing it in a proportional face would hide
            the structure the operator is editing. `ui/field` is already
            monospaced for exactly this reason. */}
        <Textarea
          value={props.current?.body ?? ""}
          onChange={(e) => props.onEdit(e.target.value)}
          disabled={props.loading}
          spellCheck={false}
          className="h-[380px]"
          placeholder={props.loading ? "yükleniyor…" : ""}
        />

        <p className="max-w-[76ch] border-l-2 border-warn/60 pl-3 text-sm leading-relaxed text-muted">
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
      <div className="mx-auto flex w-full max-w-[1020px] items-center gap-2 border-t border-edge pt-4">
        {props.error ? (
          <span className="flex items-center gap-1.5 text-sm text-bad">
            <Icon name="alert" size={14} />
            {props.error}
          </span>
        ) : (
          <span className="flex items-center gap-1.5 text-sm text-ok">
            <Icon name="check" size={14} />
            Kaydedildi.
          </span>
        )}
      </div>
    );
  }

  // It rises from the edge it is docked to. The bar appears only when there is
  // something unsaved, so its arrival is the notification — and something that
  // simply blinks into existence under the page reads as a rendering artefact
  // rather than as a thing that just became true.
  return (
    <motion.div
      variants={dock}
      initial="hidden"
      animate="shown"
      className="mx-auto flex w-full max-w-[1020px] flex-wrap items-center gap-3 border-t border-edge pt-4"
    >
      <span className="text-sm text-muted">
        Kaydedilmemiş: <span className="text-mist">{props.pending.join(" · ")}</span>
      </span>
      {props.error && (
        <span className="flex items-center gap-1.5 text-sm text-bad">
          <Icon name="alert" size={14} />
          {props.error}
        </span>
      )}
      <div className="ml-auto flex items-center gap-2.5">
        {/* The shortcut, drawn. It was a `.label` span reading "⌘S", which at
            caps tracking is a word rather than a key. */}
        <span className="flex items-center gap-1">
          <Kbd>⌘</Kbd>
          <Kbd>S</Kbd>
        </span>
        <Button variant="secondary" onClick={props.onRevert} disabled={props.saving}>
          Geri al
        </Button>
        <Button icon="check" loading={props.saving} onClick={props.onSave} disabled={props.blocked}>
          Kaydet
        </Button>
      </div>
    </motion.div>
  );
}

function describe(err: unknown): string {
  if (err instanceof DaemonError) return err.message;
  return err instanceof Error ? err.message : String(err);
}
