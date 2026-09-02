import { useEffect, useState } from "react";
import { api, DaemonError, type Project } from "../lib/daemon";
import { AccountSelect } from "./AccountPicker";
import { useAccounts } from "./AccountsProvider";
import { defaultModelID, ModelSelect, useModels } from "./ModelPicker";
import { TaskComposer, type Attached } from "./TaskComposer";
import { HoverButton } from "./hover";

/**
 * Writing a task down: a card first, tokens only when somebody runs it.
 *
 * Its own file because the board is no longer the only place a task starts —
 * the dashboard opens the same form, and two copies of a create form is how
 * the two screens end up sending different requests.
 *
 * Three choices, in the order they matter: which folder, which identity spends
 * the limit, and which model spends it how fast.
 */
export function NewTaskOverlay({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [projectID, setProjectID] = useState("");
  const [title, setTitle] = useState("");
  const [prompt, setPrompt] = useState("");
  const [attachments, setAttachments] = useState<Attached[]>([]);
  const [accountID, setAccountID] = useState("");
  const [modelID, setModelID] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const { accounts, statuses } = useAccounts();
  const { models } = useModels();

  // The form opens on the daemon's default rather than on an "automatic"
  // entry: the operator asked to be able to tell the models apart, and a form
  // that will not say which one it is about tells them apart from nothing.
  useEffect(() => {
    setModelID((current) => current || defaultModelID(models));
  }, [models]);

  useEffect(() => {
    void api
      .listProjects()
      .then(({ projects: list }) => {
        setProjects(list);
        if (list.length > 0) setProjectID((current) => current || list[0].id);
      })
      .catch((err: unknown) => setError(err instanceof DaemonError ? err.message : String(err)));
  }, []);

  const create = async (start: boolean) => {
    if (!projectID || !prompt.trim()) return;
    setBusy(true);
    setError(null);
    try {
      await api.createCodingTask({
        project_id: projectID,
        title: title.trim(),
        prompt: prompt.trim(),
        account_id: accountID,
        model: modelID,
        attachment_ids: attachments.map((a) => a.id),
        start,
      });
      onCreated();
    } catch (err) {
      setError(err instanceof DaemonError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div onClick={onClose} style={{ position: "absolute", inset: 0, background: "rgba(10,11,13,.86)", display: "grid", placeItems: "center", padding: 28 }}>
      <div
        onClick={(e) => e.stopPropagation()}
        style={{ width: 560, maxWidth: "100%", background: "#16181c", border: "1px solid #24272d", borderRadius: 10, padding: 18, display: "flex", flexDirection: "column", gap: 13 }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <h2 className="display" style={{ margin: 0, font: "400 15px/1.2 Aldrich,ui-sans-serif,system-ui", color: "#eef0f2" }}>
            Yeni task
          </h2>
          <div style={{ flex: 1 }} />
          <HoverButton base="background:none;border:none;cursor:pointer;font:400 13px/1 ui-monospace,Menlo,monospace;color:#6b7079" hover="color:#eef0f2" onClick={onClose}>
            ✕
          </HoverButton>
        </div>

        <label style={{ display: "flex", flexDirection: "column", gap: 5 }}>
          <span className="label" style={{ color: "#6b7079" }}>PROJE</span>
          <select
            value={projectID}
            onChange={(e) => setProjectID(e.target.value)}
            style={{ background: "#101114", border: "1px solid #24272d", borderRadius: 6, padding: "7px 9px", font: "450 12px/1 ui-sans-serif,system-ui", color: "#eef0f2", outline: "none" }}
          >
            {(projects ?? []).map((p) => (
              <option key={p.id} value={p.id}>
                {p.display_name} — {p.path}
              </option>
            ))}
          </select>
        </label>

        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 10 }}>
          <label style={{ display: "flex", flexDirection: "column", gap: 5, minWidth: 0 }}>
            <span className="label" style={{ color: "#6b7079" }}>HESAP</span>
            <AccountSelect
              accounts={accounts}
              statuses={statuses}
              value={accountID}
              onChange={setAccountID}
              disabled={busy}
            />
          </label>

          <label style={{ display: "flex", flexDirection: "column", gap: 5, minWidth: 0 }}>
            <span className="label" style={{ color: "#6b7079" }}>MODEL</span>
            <ModelSelect models={models} value={modelID} onChange={setModelID} disabled={busy} />
          </label>
        </div>

        <TaskComposer
          title={title}
          onTitleChange={setTitle}
          prompt={prompt}
          onPromptChange={setPrompt}
          attachments={attachments}
          onAttachmentsChange={setAttachments}
          disabled={busy}
          onSubmit={() => void create(true)}
        />

        {error && <p style={{ margin: 0, font: "400 11px/1.5 ui-monospace,Menlo,monospace", color: "#e5484d" }}>{error}</p>}

        <div style={{ display: "flex", gap: 8 }}>
          <HoverButton
            base="background:#2547e8;border:none;border-radius:5px;padding:7px 13px;cursor:pointer;font:500 11.5px/1 ui-sans-serif,system-ui;color:#eef0f2"
            hover="background:#1d3ac4"
            disabled={busy || !prompt.trim() || !projectID}
            onClick={() => void create(true)}
          >
            Kuyruğa al
          </HoverButton>
          <HoverButton
            base="background:none;border:1px solid #24272d;border-radius:5px;padding:7px 13px;cursor:pointer;font:450 11.5px/1 ui-sans-serif,system-ui;color:#8a9099"
            hover="border-color:#343841;color:#eef0f2"
            disabled={busy || !prompt.trim() || !projectID}
            onClick={() => void create(false)}
          >
            Backlog'a koy
          </HoverButton>
        </div>
      </div>
    </div>
  );
}

