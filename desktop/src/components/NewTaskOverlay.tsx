import { useEffect, useState } from "react";
import { AgentSelect, needsProject, useAgents } from "./AgentPicker";
import { api, DaemonError, type Project } from "../lib/daemon";
import { defaultModelID, ModelSelect, useModels } from "./ModelPicker";
import { TaskComposer, type Attached } from "./TaskComposer";
import { Button } from "./ui/button";
import { FieldLabel } from "./ui/field";
import { Overlay } from "./ui/overlay";
import { Picker } from "./ui/picker";

/**
 * Writing a task down: a card first, tokens only when somebody runs it.
 *
 * Its own file because the board is no longer the only place a task starts —
 * the dashboard opens the same form, and two copies of a create form is how
 * the two screens end up sending different requests.
 *
 * What you say comes first, and who runs it comes after. The three routing
 * choices — sub-agent, folder, model — used to sit above the composer, so the
 * dialog opened by asking the operator to classify work they had not written
 * down yet. All three have defaults that are correct most of the time, and the
 * sub-agent's default is "let the daemon decide", which is the whole point of
 * it. Putting them under the thing they route is what makes them optional in
 * practice as well as in the type.
 *
 * There is no fourth choice: Mimir has one Claude account, so which identity
 * pays is not a decision anyone gets to make here.
 */
export function NewTaskOverlay({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: () => void;
}) {
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [projectID, setProjectID] = useState("");
  const [title, setTitle] = useState("");
  const [prompt, setPrompt] = useState("");
  const [attachments, setAttachments] = useState<Attached[]>([]);
  const [modelID, setModelID] = useState("");
  const [agent, setAgent] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const { models } = useModels();
  const { catalogue } = useAgents();

  // Only an agent that works inside a folder needs one. Asking for a project
  // to run a region search would make the operator register a directory
  // nothing is ever going to open.
  const wantsProject = needsProject(catalogue, agent);

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
      .catch((err: unknown) =>
        setError(err instanceof DaemonError ? err.message : String(err)),
      );
  }, []);

  const create = async (start: boolean) => {
    if ((wantsProject && !projectID) || !prompt.trim()) return;
    setBusy(true);
    setError(null);
    try {
      await api.createCodingTask({
        project_id: wantsProject ? projectID : "",
        title: title.trim(),
        prompt: prompt.trim(),
        model: modelID,
        // Omitted rather than sent empty: an absent agent is what asks the
        // daemon to choose, and "" would be a key it has to reject.
        ...(agent ? { agent } : {}),
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

  const blocked = busy || !prompt.trim() || (wantsProject && !projectID);

  return (
    <Overlay
      open
      onClose={onClose}
      title="Yeni task"
      subtitle="Kart önce yazılır; token ancak biri çalıştırınca harcanır."
      footer={
        <>
          <Button
            variant="secondary"
            disabled={blocked}
            onClick={() => void create(false)}
          >
            Backlog'a koy
          </Button>
          <Button
            icon="play"
            loading={busy}
            disabled={blocked}
            onClick={() => void create(true)}
          >
            Kuyruğa al
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-5">
        {/* The composer comes first now. The three routing choices used to sit
            above it, so the form opened by asking which sub-agent and which
            model should handle a task the operator had not written yet — a
            dialog that makes you classify your own work before you have said
            what it is. Write the thing; then decide who runs it. */}
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

        <div className="grid grid-cols-2 gap-4">
          {wantsProject && (
            <div className="flex min-w-0 flex-col gap-2">
              <FieldLabel>Proje</FieldLabel>
              <Picker
                label="Proje"
                choices={(projects ?? []).map((p) => ({
                  value: p.id,
                  label: p.display_name,
                  detail: p.path,
                  icon: "folder" as const,
                }))}
                value={projectID}
                onChange={setProjectID}
                disabled={busy}
                placeholder={projects === null ? "yükleniyor…" : "proje seçin"}
              />
            </div>
          )}

          <div className="flex min-w-0 flex-col gap-2">
            <FieldLabel>Model</FieldLabel>
            <ModelSelect
              models={models}
              value={modelID}
              onChange={setModelID}
              disabled={busy}
            />
          </div>
        </div>

        <AgentSelect value={agent} onChange={setAgent} disabled={busy} />

        {error && (
          <p className="font-mono text-xs leading-[1.5] text-bad">{error}</p>
        )}
      </div>
    </Overlay>
  );
}
