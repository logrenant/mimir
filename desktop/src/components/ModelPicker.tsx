import { useCallback, useEffect, useState, type ReactNode } from "react";
import {
  api,
  DaemonError,
  type CodingModel,
  type LLMProvider,
  type LLMProviderList,
} from "../lib/daemon";
import { cn } from "../lib/cn";
import { availabilityNote, isSelectable, modelsFor } from "../lib/settings";
import { Picker, type Choice } from "./ui/picker";

/**
 * Which model a task spends its account's limit on.
 *
 * The list comes from `GET /coding-models`, not from a constant here. The
 * daemon validates against its own allow-list and refuses anything else with a
 * 400, so a second copy in this file would drift the first time a generation
 * ships and start offering options the daemon rejects.
 *
 * The picker has no "automatic" entry. The operator asked to be able to tell
 * the models apart, and a card that says "default" tells them apart from
 * nothing; the daemon's default is simply the option that starts selected. The
 * empty string still means "the daemon decides" on the wire, which is what a
 * client written before this picker keeps sending.
 *
 * It stopped being a native `<select>` for the reason `ui/picker` exists: an
 * option there can hold a string and nothing else, so "which of these is the
 * default" had to be glued onto the end of a label — `"Opus 5 — varsayılan"` —
 * where it reads as part of the model's name. It is a line of its own now,
 * under the option it belongs to.
 */

export function useModels() {
  const [models, setModels] = useState<CodingModel[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      const { models: list } = await api.listCodingModels();
      setModels(list);
      setError(null);
    } catch (err) {
      setModels([]);
      setError(err instanceof DaemonError ? err.message : String(err));
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  return { models, error, refresh };
}

/**
 * The provider list, for a screen that lets one run choose its own model.
 *
 * A hook rather than a fetch in each screen because there are now four of them
 * — settings, brain, katalog and the board's card panel — and the failure case
 * is the part worth having in one place: a daemon that will not answer is not
 * an error to show, it is a picker that does not appear, because the run still
 * works and routes the way it did before any picker existed.
 */
export function useProviders() {
  const [providers, setProviders] = useState<LLMProviderList | null>(null);

  useEffect(() => {
    let live = true;
    api
      .llmProviders()
      .then((res) => live && setProviders(res))
      .catch(() => live && setProviders(null));
    return () => {
      live = false;
    };
  }, []);

  return providers;
}

/** Turns a model id into something worth putting on a card. */
export function modelLabel(models: CodingModel[] | null, id: string | undefined): string {
  if (!id) return "";
  const match = models?.find((m) => m.id === id);
  if (match) return match.label;
  // A run that already finished can carry the exact build the CLI reported,
  // which is longer than anything in the list and still worth showing.
  return id.replace(/^claude-/, "");
}

export function ModelSelect({
  models,
  value,
  onChange,
  disabled,
}: {
  models: CodingModel[] | null;
  value: string;
  onChange: (id: string) => void;
  disabled?: boolean;
}) {
  const choices: Choice<string>[] = (models ?? []).map((m) => ({
    value: m.id,
    label: m.label,
    detail: m.default ? "daemon'un varsayılanı" : undefined,
  }));

  return (
    <Picker
      label="Model"
      choices={choices}
      value={value}
      onChange={onChange}
      disabled={disabled || models === null}
      placeholder={models === null ? "yükleniyor…" : "model seçin"}
    />
  );
}

/**
 * The id a fresh form should start on: the daemon's default, or "" until the
 * list arrives. Exported so every composer opens on the same choice.
 */
export function defaultModelID(models: CodingModel[] | null): string {
  return models?.find((m) => m.default)?.id ?? "";
}

/**
 * Which provider and model spend this run.
 *
 * Shared by leadgen and the brain scan: both let an operator route one run by
 * hand, and the pairing rule is the same one twice.
 *
 * Two controls rather than one flat list of every provider/model pair: the
 * provider is the decision that matters — free tier or your Claude quota — and
 * burying it inside twenty entries makes the expensive choice as easy to make
 * by accident as the free one. Choosing the provider first also means the
 * second control can only ever offer combinations the daemon will run.
 *
 * Both controls are always drawn, and this is the fix for "the model cannot be
 * changed". The model control used to be *absent* until a provider was picked,
 * so the opening state was a single dropdown under a label reading "Model"
 * which, when opened, offered providers. There was no way to tell from the
 * screen that a model choice existed at all, let alone what to do to reach it.
 * It is present and disabled now, showing the model the daemon would route to,
 * and it says what would make it live.
 *
 * Each control carries its own name for the same reason: one label over two
 * controls named the wrong one.
 */
export function ProviderModelPicker(props: {
  providers: LLMProviderList | null;
  provider: string;
  model: string;
  onProvider: (v: string) => void;
  onModel: (v: string) => void;
  /** Hides the "let the daemon route it" option, for a surface where the
   *  daemon's own routing is not one of the answers. */
  routedLabel?: string;
}) {
  // No list means the daemon did not answer. Showing an empty dropdown would
  // suggest there is nothing to choose; showing nothing correctly says the
  // choice is not on offer, and the run still works.
  if (!props.providers || props.providers.providers.length === 0) return null;

  const routed = props.providers.routed;
  const chosen: LLMProvider | undefined = props.providers.providers.find(
    (p) => p.id === props.provider,
  );

  const available = props.providers.available;

  const providerChoices: Choice<string>[] = [
    {
      value: ROUTED,
      label: props.routedLabel ?? "Varsayılan",
      detail: `daemon yönlendirir · ${routed.provider}`,
      icon: "pulse",
    },
    // What this machine can actually run. A provider whose CLI is not installed
    // is not a choice, it is a failure with an extra click in front of it; one
    // that is installed but signed out is a choice with a warning, because the
    // fix is a login rather than an install.
    ...props.providers.providers.map((p) => ({
      value: p.id,
      label: p.label,
      detail: availabilityNote(available, p.id) || undefined,
      disabled: !isSelectable(available, p.id),
    })),
  ];

  // Discovered models win over an empty catalogue: ollama's models are files on
  // this machine, so the pinned table lists none and the daemon reports what it
  // found.
  const models = modelsFor(props.providers, available, props.provider);

  return (
    <div className="flex flex-wrap items-end gap-3">
      <Labelled name="Sağlayıcı" className="w-52">
        <Picker
          label="Sağlayıcı"
          choices={providerChoices}
          value={props.provider || ROUTED}
          onChange={(next) => props.onProvider(next === ROUTED ? "" : next)}
        />
      </Labelled>
      <Labelled name="Model" className="w-56">
        <Picker
          label="Model"
          choices={models.map((m) => ({
            value: m.id,
            label: m.label,
            detail: m.id === chosen?.default_model ? "sağlayıcının varsayılanı" : undefined,
          }))}
          value={chosen ? props.model : ""}
          onChange={props.onModel}
          disabled={!chosen}
          placeholder={chosen ? "model seçin" : `${routed.model} · sağlayıcı seçin`}
        />
      </Labelled>
    </div>
  );
}

/**
 * A control with its name above it, which is what a form of two dropdowns
 * needs and a single shared label cannot do.
 *
 * A `<div>` and not a `<label>`: a `<label>` wrapping a `<button>` forwards its
 * own click to that button, so every click on the trigger arrives twice and the
 * menu opens and shuts in the same gesture. `Picker` gives the trigger its
 * accessible name through `aria-haspopup` and the menu's own label, so nothing
 * is lost by not being a real label element.
 */
function Labelled({
  name,
  className,
  children,
}: {
  name: string;
  className?: string;
  children: ReactNode;
}) {
  return (
    <div className={cn("flex flex-col gap-1.5", className)}>
      <span className="label text-muted">{name}</span>
      {children}
    </div>
  );
}

/** The same sentinel trick `AgentSelect` uses, for the same reason: routing by
 * the daemon is a choice, and an empty value would draw as no choice at all. */
const ROUTED = "__routed__";
