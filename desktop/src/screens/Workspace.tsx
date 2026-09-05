import { useCallback, useEffect, useRef, useState } from "react";
import { open } from "@tauri-apps/plugin-dialog";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Card, CardBody, CardHeader } from "../components/ui/card";
import { api, DaemonError, isTerminalStatus, type Project, type Run } from "../lib/daemon";
import { emptyRun, openRunStream, reduceRun, type RunView } from "../lib/runStream";
import { TaskComposer, type Attached } from "../components/TaskComposer";
import { AccountPanel } from "../components/AccountPanel";
import { defaultModelID, ModelSelect, useModels } from "../components/ModelPicker";
import { useTerminals } from "../components/TerminalsProvider";

/**
 * Pick a folder, give it a task, watch the run.
 *
 * The path leaves this screen exactly once, at registration. Everything
 * afterwards carries the project id the daemon handed back — the frontend never
 * sends a filesystem path a second time (docs/ROADMAP.md §B.4).
 */
export function Workspace({ onGoTerminals }: { onGoTerminals?: () => void } = {}) {
  const [projects, setProjects] = useState<Project[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [prompt, setPrompt] = useState("");
  const [attachments, setAttachments] = useState<Attached[]>([]);
  const [modelID, setModelID] = useState("");
  const [run, setRun] = useState<Run | null>(null);
  const [view, setView] = useState<RunView>(emptyRun);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const terminals = useTerminals();
  const { models } = useModels();

  // Opens on the daemon's default rather than on a blank that means "whatever":
  // the runner is where a model choice is most likely to be deliberate.
  useEffect(() => {
    setModelID((current) => current || defaultModelID(models));
  }, [models]);

  const refresh = useCallback(async () => {
    try {
      const { projects: list } = await api.listProjects();
      setProjects(list);
      setSelected((current) => current ?? list[0]?.id ?? null);
    } catch (err) {
      setError(describe(err));
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const pickFolder = async () => {
    setError(null);
    setBusy(true);
    try {
      // The native NSOpenPanel, via Tauri's dialog plugin — one maintained
      // picker, replacing goat v1's two divergent ones. Inside the try: the
      // picker can reject, and a button that silently does nothing is worse
      // than one that says why.
      const picked = await open({ directory: true, multiple: false, title: "Choose a project folder" });
      if (typeof picked !== "string") return;

      const project = await api.registerProject(picked);
      setSelected(project.id);
      await refresh();
    } catch (err) {
      // internal/project's guards explain themselves ("refusing the home
      // directory", "not a directory"), so the message is shown as written.
      setError(describe(err));
    } finally {
      setBusy(false);
    }
  };

  const start = async () => {
    if (!selected || !prompt.trim()) return;
    setError(null);
    setBusy(true);
    try {
      const started = await api.createCodingTask({
        project_id: selected,
        prompt: prompt.trim(),
        model: modelID,
        attachment_ids: attachments.map((a) => a.id),
      });
      // Only once the run exists: setting the view first meant a failed start
      // re-rendered the *previous* run with a blank panel, reading "running"
      // over nothing.
      setView(emptyRun());
      setRun(started);
      setAttachments([]);
      terminals.open(started);
    } catch (err) {
      setError(describe(err));
    } finally {
      setBusy(false);
    }
  };

  // Held until the run is over, not until the POST returns: re-enabling on the
  // response let a second click replace `run` and abandon the first stream
  // with nothing watching it.
  const running = run !== null && !view.finished;

  return (
    <div className="mx-auto flex h-full max-w-5xl flex-col gap-4 overflow-y-auto p-6">
      <Card>
        <CardHeader
          title="Project"
          subtitle="Registered once; every later call carries only its id"
          aside={
            <Button variant="ghost" onClick={() => void pickFolder()} disabled={busy || running}>
              Choose folder…
            </Button>
          }
        />
        <CardBody>
          {projects.length === 0 ? (
            <p className="text-sm text-muted">
              No project yet. A coding task cannot start until a folder is registered — that is the
              scope boundary, not a setting.
            </p>
          ) : (
            <div className="space-y-1">
              {projects.map((project) => (
                <label
                  key={project.id}
                  className="flex cursor-pointer items-center gap-3 rounded px-2 py-1.5 hover:bg-edge/40"
                >
                  <input
                    type="radio"
                    name="project"
                    className="accent-electric"
                    checked={selected === project.id}
                    onChange={() => setSelected(project.id)}
                  />
                  <span className="text-sm">{project.display_name}</span>
                  <span className="truncate text-xs text-muted" title={project.path}>
                    {project.path}
                  </span>
                </label>
              ))}
            </div>
          )}
        </CardBody>
      </Card>

      <Card>
        <CardHeader
          title="Account"
          subtitle="One Claude identity, signed out when Mimir closes"
        />
        <CardBody>
          <AccountPanel />
        </CardBody>
      </Card>

      <Card>
        <CardHeader title="Task" subtitle="Runs in the selected folder, with file tools" />
        <CardBody className="space-y-3">
          <TaskComposer
            prompt={prompt}
            onPromptChange={setPrompt}
            attachments={attachments}
            onAttachmentsChange={setAttachments}
            disabled={busy || running}
            rows={4}
            onSubmit={() => void start()}
            placeholder="What should Claude do in this folder? Paste or drop an image to attach it."
          />
          <div className="flex flex-wrap items-center gap-3">
            <ModelSelect
              models={models}
              value={modelID}
              onChange={setModelID}
              disabled={busy || running}
            />
            <Button onClick={() => void start()} disabled={busy || running || !selected || !prompt.trim()}>
              {running ? "Running…" : "Start run"}
            </Button>
            {run && onGoTerminals && (
              <Button variant="ghost" onClick={onGoTerminals}>
                Watch in Terminals
              </Button>
            )}
            {running && (
              <Button variant="ghost" onClick={() => void api.stopCodingTask(run.id).catch((err: unknown) => setError(describe(err)))}>
                Stop
              </Button>
            )}
            {error && <span className="text-sm text-bad">{error}</span>}
          </div>
        </CardBody>
      </Card>

      {run ? <RunPanel run={run} view={view} setView={setView} /> : <IdlePanel />}
    </div>
  );
}

function IdlePanel() {
  return (
    <Card className="grid place-items-center">
      <p className="p-8 text-sm text-muted">
        The run stream appears here: text, reasoning, and every tool call as it happens.
      </p>
    </Card>
  );
}

function RunPanel({
  run,
  view,
  setView,
}: {
  run: Run;
  view: RunView;
  setView: (update: (previous: RunView) => RunView) => void;
}) {
  const [closed, setClosed] = useState<string | null>(null);
  const bottom = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let close: (() => void) | undefined;
    let cancelled = false;

    void openRunStream(
      run.id,
      (event) => setView((previous) => reduceRun(previous, event)),
      (reason) => setClosed(reason ?? null),
    )
      .then((closer) => {
        if (cancelled) closer();
        else close = closer;
      })
      // The rejection used to be swallowed by `void`, which left the badge
      // reading "running" over an empty panel for the whole session.
      .catch((err: unknown) => {
        if (!cancelled) setClosed(describe(err));
      });

    return () => {
      cancelled = true;
      close?.();
    };
  }, [run.id, setView]);

  useEffect(() => {
    bottom.current?.scrollIntoView({ behavior: "smooth", block: "end" });
  }, [view.text, view.tools.length]);

  const tone = view.failed ? "bad" : view.stopped ? "muted" : view.finished ? "ok" : "warn";
  const label = view.failed
    ? "failed"
    : view.stopped
      ? "stopped"
      : view.finished
        ? "completed"
        : isTerminalStatus(run.status)
          ? run.status
          : run.status === "queued"
            ? "queued"
            : "running";

  return (
    <Card className="flex min-h-0 flex-col">
      <CardHeader
        title="Run"
        subtitle={`${run.id}${view.costUSD ? ` · $${view.costUSD.toFixed(4)}` : ""}${
          view.numTurns ? ` · ${view.numTurns} turns` : ""
        }`}
        aside={<Badge tone={tone}>{label}</Badge>}
      />
      <CardBody className="min-h-0 flex-1 overflow-auto">
        {view.reasoning && (
          <details className="mb-3 rounded border border-edge bg-ground/60 p-3">
            <summary className="cursor-pointer text-xs text-muted">Reasoning</summary>
            <pre className="mt-2 whitespace-pre-wrap text-xs text-muted">{view.reasoning}</pre>
          </details>
        )}

        {view.tools.map((tool) => (
          <details key={tool.callID} className="mb-2 rounded border border-edge bg-ground/60">
            <summary className="flex cursor-pointer items-center gap-2 px-3 py-2 text-xs">
              <span className="font-medium">{tool.name}</span>
              {/* Risk comes from the daemon, which classifies an unknown tool
                  as the most dangerous class rather than the least. */}
              {tool.risk && (
                <Badge tone={tool.risk === "read" ? "muted" : tool.risk === "write" ? "warn" : "bad"}>
                  {tool.risk}
                </Badge>
              )}
              {tool.ok !== undefined && (
                <Badge tone={tool.ok ? "ok" : "bad"}>{tool.ok ? "ok" : "error"}</Badge>
              )}
            </summary>
            <pre className="max-h-64 overflow-auto whitespace-pre-wrap border-t border-edge p-3 text-xs text-muted">
              {tool.output ?? JSON.stringify(tool.args, null, 2) ?? ""}
            </pre>
          </details>
        ))}

        <pre className="whitespace-pre-wrap text-sm leading-relaxed">{view.text}</pre>

        {/* The CLI's stderr, which is where "run `claude login`" is written and
            where a run that could never work says so. */}
        {view.stderr && (
          <details className="mt-3 rounded border border-edge bg-ground/60 p-3" open={!view.text}>
            <summary className="cursor-pointer text-xs text-warn">stderr</summary>
            <pre className="mt-2 whitespace-pre-wrap text-xs text-warn">{view.stderr}</pre>
          </details>
        )}

        {view.error && <p className="mt-3 text-sm text-bad">{view.error}</p>}
        {closed && <p className="mt-3 text-sm text-warn">{closed}</p>}
        <div ref={bottom} />
      </CardBody>
    </Card>
  );
}

function describe(err: unknown): string {
  if (err instanceof DaemonError) return err.message;
  return err instanceof Error ? err.message : String(err);
}
