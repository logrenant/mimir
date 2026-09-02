import { useCallback, useEffect, useState } from "react";
import { api, DaemonError, type CodingModel } from "../lib/daemon";

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
