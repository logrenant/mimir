import { useCallback, useEffect, useState } from "react";
import {
  api,
  DaemonError,
  type CodingModel,
  type LLMProvider,
  type LLMProviderList,
} from "../lib/daemon";

/**
 * Which model a task spends its account's limit on.
 *
 * The list comes from `GET /coding-models`, not from a constant here. The
 * daemon validates against its own allow-list and refuses anything else with a
 * 400, so a second copy in this file would drift the first time a generation
 * ships and start offering options the daemon rejects.
 *
 * The select has no "automatic" entry. The operator asked to be able to tell
 * the models apart, and a card that says "default" tells them apart from
 * nothing; the daemon's default is simply the option that starts selected. The
 * empty string still means "the daemon decides" on the wire, which is what a
 * client written before this picker keeps sending.
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
  return (
    <select
      value={value}
      disabled={disabled || models === null}
      onChange={(e) => onChange(e.target.value)}
      style={{
        background: "#101114",
        border: "1px solid #24272d",
        borderRadius: 6,
        padding: "7px 9px",
        font: "450 12px/1 ui-sans-serif,system-ui",
        color: "#eef0f2",
        outline: "none",
      }}
    >
      {models === null && <option value="">yükleniyor…</option>}
      {(models ?? []).map((m) => (
        <option key={m.id} value={m.id}>
          {m.label}
          {m.default ? " — varsayılan" : ""}
        </option>
      ))}
    </select>
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
 * The empty option is first and is the opening state. It is not "no model", it
 * is the daemon's own class routing, which is what every run did before this
 * control existed and what every run still does when nobody touches it.
 */
export function ProviderModelPicker(props: {
  providers: LLMProviderList | null;
  provider: string;
  model: string;
  onProvider: (v: string) => void;
  onModel: (v: string) => void;
}) {
  // No list means the daemon did not answer. Showing an empty dropdown would
  // suggest there is nothing to choose; showing nothing correctly says the
  // choice is not on offer, and the run still works.
  if (!props.providers || props.providers.providers.length === 0) return null;

  const chosen: LLMProvider | undefined = props.providers.providers.find(
    (p) => p.id === props.provider,
  );

  return (
    <div className="flex items-center gap-2">
      <span className="label text-muted">model</span>
      <select
        value={props.provider}
        onChange={(e) => props.onProvider(e.target.value)}
        className="rounded-sm border border-edge bg-ground px-2 py-1.5 text-xs text-text outline-none"
      >
        <option value="">varsayılan ({props.providers.routed.provider})</option>
        {props.providers.providers.map((p) => (
          <option key={p.id} value={p.id}>
            {p.label}
          </option>
        ))}
      </select>
      {chosen && (
        <select
          value={props.model}
          onChange={(e) => props.onModel(e.target.value)}
          className="max-w-56 rounded-sm border border-edge bg-ground px-2 py-1.5 text-xs text-text outline-none"
        >
          {chosen.models.map((m) => (
            <option key={m.id} value={m.id}>
              {m.label}
              {m.id === chosen.default_model ? " · varsayılan" : ""}
            </option>
          ))}
        </select>
      )}
    </div>
  );
}
